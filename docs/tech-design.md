# Technical Design Document: Kodi WebRTC-to-RTSP Screen Share Bridge

## 1. System Architecture

The system consists of two processes running on the LibreELEC Raspberry Pi 5:

1. **A custom Go application** — the main orchestrator, serving the web UI, managing session state, and controlling Kodi.
2. **MediaMTX v1.19.1** — run as a **sidecar subprocess** managed by the Go app, handling WebRTC ingest and exposing the live stream for Kodi playback over RTSP.

> **Why not embed MediaMTX?** MediaMTX's entire codebase is under Go's `internal/` package convention, and the maintainer has explicitly refused to expose a public embedding API ([issue #4011](https://github.com/bluenviron/mediamtx/issues/4011), [PR #4020](https://github.com/bluenviron/mediamtx/pull/4020) — both closed). The Go app will instead launch the MediaMTX binary as a child process and communicate via MediaMTX's config hooks and REST API.

The Go application has three core responsibilities:

1. **HTTP Web Server (port 80):** Serves the static HTML/JS frontend to presenters.
2. **Session & Lifecycle Manager:** Tracks active sharing sessions, enforces single-presenter policy, and bridges MediaMTX events to Kodi commands.
3. **Kodi JSON-RPC + CEC Controller:** Triggers Kodi playback commands based on stream state events and coordinates HDMI-CEC wake / conditional standby behavior through a bundled Kodi addon.

### Architecture Diagram (Logical Flow)

```text
[ Presenter Laptop (Browser) ]
        |
        | 1. HTTP GET :80 (Loads HTML/JS Frontend)
        | 2. WebRTC WHIP :8889 (Pushes H.264 video track)
        v
[ MediaMTX (sidecar subprocess) ]
        |
	        |-- Receives WebRTC -> Exposes RTSP stream (:8554)
        |-- runOnReady hook -> HTTP POST :80/api/hooks/ready
        |-- runOnNotReady hook -> HTTP POST :80/api/hooks/not-ready
        v
[ Go Application (port 80) ]
        |
        | 3. HTTP POST (JSON-RPC playback / CEC command)
        v
[ Kodi Web Server (localhost:8080) ]
        |
	        | 4. Kodi internal player pulls RTSP stream
        v
[ TV Display ]
```

### Port Allocation

| Port | Service | Protocol |
|------|---------|----------|
| 80 | Go app — Web UI + API | HTTP |
| 8554 | MediaMTX — RTSP output | RTSP |
| 8889 | MediaMTX — WebRTC WHIP ingest | HTTP |
| 8080 | Kodi — JSON-RPC API (pre-existing) | HTTP |
| 9997 | MediaMTX — Control REST API | HTTP |


## 2. Component Design

### 2.1. Frontend Web Client

A static single-page application (HTML/JS) served by the Go backend on port 80.

#### Capture

Uses `navigator.mediaDevices.getDisplayMedia()` to capture the desktop:

```js
const stream = await navigator.mediaDevices.getDisplayMedia({
  video: { frameRate: { ideal: 30 } },
  audio: true,                  // shares screen/system audio; also gives Kodi an audio clock (§2.5)
  systemAudio: 'include',       // prefer system (not just tab) audio where supported
  selfBrowserSurface: 'exclude' // keep the control tab out of the picker (no hall-of-mirrors)
});
```

#### Codec preference (HEVC → H.264)

The browser must send a codec MediaMTX can forward into Kodi-compatible RTSP **without CPU-heavy transcoding**. We prefer **HEVC (H.265)** when the sender can hardware-encode it, falling back to **H.264** otherwise, via `RTCRtpTransceiver.setCodecPreferences()` ([Baseline 2024](https://caniuse.com/mdn-api_rtcrtptransceiver_setcodecpreferences)):

```js
const caps = RTCRtpSender.getCapabilities('video');
const pick = (t) => caps.codecs.filter(c => c.mimeType.toLowerCase() === t);
const chosen = pick('video/h265').length ? pick('video/h265') : pick('video/h264');
transceiver.setCodecPreferences([...chosen, ...caps.codecs.filter(c => !chosen.includes(c))]);
```

A browser only advertises `video/H265` when it has a hardware HEVC encoder (Chrome 136+, Safari 17.4+/18; never Firefox), so this auto-detects with no UA sniffing and degrades safely — H.264 stays in the list, so if a sender or MediaMTX can't do HEVC the answer is simply H.264. **In practice our Linux Chromium/Firefox presenters don't expose HEVC encode, so H.264 is what's used today.** HEVC matters mainly for offloading the Pi's decoder (see Future Considerations), not for latency. No raw SDP munging is required.

#### WHIP Publishing

Implements a WebRTC WHIP client. It creates an `RTCPeerConnection`, adds the video track, and POSTs the SDP offer to `http://<pi-ip>:8889/screenshare/whip`. The WHIP endpoint is handled natively by MediaMTX. The frontend also performs the follow-up WHIP steps needed for stable browser publishing: it requests ICE server information, applies the SDP answer, and PATCHes trickled ICE candidates after the session URL is established.

#### Frontend State Machine

```text
[Idle] --("Share Screen" click)--> [Requesting Permission]
[Requesting Permission] --( user grants )--> [Checking Availability]
[Requesting Permission] --( user denies )--> [Idle] (show dismissible error)
[Checking Availability] --( GET /api/status → idle )--> [Connecting]
[Checking Availability] --( GET /api/status → active )--> [Confirm Takeover]
[Confirm Takeover] --( user confirms )--> [Connecting] (POST /api/takeover)
[Confirm Takeover] --( user cancels )--> [Idle]
[Connecting] --( WHIP established )--> [Sharing]
[Sharing] --( "Stop" click / track.onended )--> [Idle] (WHIP DELETE)
[Sharing] --( connection lost )--> [Error] --> [Idle]
```


### 2.2. Go Backend

#### MediaMTX Subprocess Management

The Go app launches MediaMTX as a child process on startup using `exec.Command`, passing a generated YAML config. If MediaMTX exits unexpectedly, the Go app restarts it.

MediaMTX configuration (generated at startup as a temp YAML file):

```yaml
api: yes
apiAddress: 127.0.0.1:9997

rtspTransports: [tcp]   # force interleaved TCP for the Kodi reader (no UDP reorder/loss latency)
writeQueueSize: 1024    # if the reader can't keep up, drop packets instead of queuing latency

paths:
  screenshare:
    runOnReady: >
      curl -s -X POST http://127.0.0.1:80/api/hooks/ready
    runOnReadyRestart: no
    runOnNotReady: >
      curl -s -X POST http://127.0.0.1:80/api/hooks/not-ready
```

> **HLS removed:** earlier revisions ran `hlsAlwaysRemux: yes` "for diagnostics," but Kodi plays
> over RTSP only. Continuous HLS muxing wastes CPU on the Pi 5, whose software H.264 decoder
> needs the headroom, so it has been dropped.

This means the Go app needs `curl` available, or alternatively the hooks can call the Go binary itself with a subcommand (e.g., `webrtc-bridge notify ready`). On LibreELEC, `curl` is available by default.

#### Session Manager

The Go app maintains a simple in-memory session state:

```go
type SessionState struct {
    mu       sync.Mutex
    active   bool       // is someone currently sharing?
    // future: presenterIP, startedAt, etc.
}
```

**API Endpoints (served on port 80):**

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/` | GET | Serve the frontend SPA |
| `/api/status` | GET | Returns `{"active": true/false}` — used by frontend to check availability |
| `/api/metrics` | GET | Returns the Kodi playback-lag time series `{"active","current","samples":[{"t","lag"}]}` for the web UI's latency chart |
| `/api/takeover` | POST | Stops current stream (via MediaMTX REST API: `GET /v3/webrtcsessions/list` to find the session, then `POST /v3/webrtcsessions/kick/{id}` to disconnect the publisher), allowing new presenter |
| `/api/hooks/ready` | POST | Called by MediaMTX `runOnReady` — triggers Kodi wake + `Player.Open` |
| `/api/hooks/not-ready` | POST | Called by MediaMTX `runOnNotReady` — triggers Kodi `Player.Stop` and conditional standby |

#### Multi-Presenter Handling

When a second presenter tries to share while someone is already active:
1. Frontend calls `GET /api/status` and receives `{"active": true}`.
2. Frontend shows a confirmation dialog: *"Someone is currently sharing. Take over?"*
3. If confirmed, frontend calls `POST /api/takeover`, which uses the MediaMTX REST API to kick the current publisher: first `GET http://127.0.0.1:9997/v3/webrtcsessions/list` to find the active session ID, then `POST http://127.0.0.1:9997/v3/webrtcsessions/kick/{id}` to disconnect it. The `runOnNotReady` hook fires, stopping Kodi playback. Then the new presenter's WHIP connection proceeds normally.


### 2.3. Kodi JSON-RPC + HDMI-CEC Controller

When the `/api/hooks/ready` endpoint is hit, the Go app sends:

1. A best-effort HDMI-CEC wake command through Kodi addon execution:

```json
{
  "jsonrpc": "2.0",
  "method": "Addons.ExecuteAddon",
  "params": {
    "addonid": "script.kodi-screenshare-cec",
    "params": {
      "command": "activate"
    },
    "wait": true
  },
  "id": 1
}
```

2. The playback command — note it opens a generated **`.strm` file**, not the raw RTSP URL,
   because a raw `Player.Open` URL silently drops the realtime/low-latency KODIPROPs (see §2.4):

```json
{
  "jsonrpc": "2.0",
  "method": "Player.Open",
  "params": {
    "item": {
	      "file": "/storage/.kodi/userdata/kodi-screenshare.strm"
    }
  },
  "id": 1
}
```

- **API Endpoint:** `http://127.0.0.1:8080/jsonrpc`
- **Wake transport:** Kodi JSON-RPC does not expose direct builtin execution for HDMI-CEC commands, so the Go app invokes a small bundled Kodi addon (`script.kodi-screenshare-cec`) using `Addons.ExecuteAddon`. That addon runs `xbmc.executebuiltin('CECActivateSource()')`, `CECStandby()`, or `CECToggleState()` on the Kodi side.
- **Stop Event:** When `/api/hooks/not-ready` fires (stream ended), the Go app sends `Player.Stop`. If this session had previously issued a successful wake command, it then sends a matching standby command through the addon so the TV powers down only when the app believes it woke it earlier.

### 2.4. Output Format: RTSP via `inputstream.ffmpegdirect` (realtime)

The system uses **RTSP** for Kodi playback, opened through the `inputstream.ffmpegdirect` addon
in **realtime mode**. Realtime mode trims startup probing and marks the stream live, but Kodi
still holds a multi-second playback buffer that we drain at runtime (see §2.5).

- **Decode is cheap — software H.264 is *not* the bottleneck (measured).** The Raspberry Pi 5 has
  **no hardware H.264 decoder** (its only hardware video decoder is HEVC), so H.264 is
  software-decoded on the Arm cores — but at 1080p that costs only ~34% of *one* core (~8% of the
  4-core SoC). Earlier revisions of this doc claimed software decode was "the ceiling" and the
  source of a latency drift; **that was wrong** — §2.5 covers where the latency actually lives.
  (4K would be far heavier and is still avoided via the resolution cap below.)
- **`.strm` playback path:** the bridge writes a `.strm` file (default
  `/storage/.kodi/userdata/kodi-screenshare.strm`) and opens *that* via `Player.Open`. A raw
  RTSP URL passed to `Player.Open` silently drops the low-latency hints — they are only honored
  via a `.strm`/playlist entry. The file carries:
  ```
  #KODIPROP:inputstream=inputstream.ffmpegdirect
  #KODIPROP:inputstream.ffmpegdirect.open_mode=ffmpeg
  #KODIPROP:inputstream.ffmpegdirect.is_realtime_stream=true
  #KODIPROP:rtsp_transport=tcp
  rtsp://<stream-host>:8554/screenshare
  ```
  `inputstream.ffmpegdirect` is declared as an addon dependency so LibreELEC auto-installs it.
- **Source-side resolution normalization (no transcoding):** the browser does NOT publish the
  raw `getDisplayMedia` track. It composites the capture into a fixed **16:9 canvas, capped at
  1440p and never upscaled** (`min(source, 2560×1440)`), locked once at share start. This:
  1. bounds the **encoder** load on the presenter's laptop — full-motion content (e.g. a shared
     video) can otherwise saturate the laptop H.264 encoder and add latency *upstream* of Kodi
     (see §2.5); lowering the cap is the lever for that case;
  2. keeps the encoded resolution **constant** for the whole session, so mid-stream resolution
     changes (e.g. resizing a shared browser tab) never emit new SPS/PPS that freeze Kodi on a
     buffering spinner; and
  3. publishes via `canvas.captureStream(30)` — redrawn on each presented source frame, with a
     ~10 fps floor timer — so frames keep flowing at a steady rate even when the shared content
     is static (no demuxer underrun → no spinner).
  The encoder runs with `degradationPreference: 'maintain-resolution'` (drop framerate, never
  resolution under load) and a generous `maxBitrate` (~15 Mbps at 1080p, ~22 at 1440p). All of
  this runs on the presenter's laptop, keeping the no-transcoding constraint on the Pi intact.
- **Latency monitoring (rough proxy — do not over-trust):** while a share is active, the bridge
  polls Kodi (`Player.GetProperties` `time`) and estimates lag as `wall-clock elapsed − playback
  position`, served at `GET /api/metrics` and plotted in the web UI. In practice this proxy is
  **unreliable**: its origin is the moment `Player.Open` returns (not first-frame display), so it
  carries a constant offset, and Kodi's live-stream `time` does not cleanly track the
  displayed-vs-live gap — it tends to read a slow climb even when real latency is stable. Trust
  side-by-side measurement (an on-screen millisecond timer captured in a single screenshot) over
  this number. Measurement only — no automatic remediation.


### 2.5. Latency behaviour and the resync control

Measured empirically (against `ffplay` and the addon source), end-to-end latency has **two
independent sources**:

1. **Kodi's playback buffer (all content).** `ffplay` reading the same MediaMTX RTSP stream —
   even over two Wi-Fi hops — sits at sub-second, while Kodi on loopback holds several seconds.
   So that baseline is Kodi's `VideoPlayer` buffer, not the network, MediaMTX, or decode. And it
   is **not tunable from our side**: `inputstream.ffmpegdirect` (read on the Piers / Kodi-22
   branch) is a pure on-demand demuxer — `DemuxRead()` is just `av_read_frame()` handed to Kodi.
   It does no frame-dropping or live-edge chasing, exposes **no** buffer/latency KODIPROP or
   global setting, and forwards **no** low-latency AVFormat options for RTSP (its option
   passthrough is an HTTP-only whitelist). `is_realtime_stream=true` only trims startup probing
   and disables seek/pause; it does not shrink the steady buffer. Kodi's own `advancedsettings.xml`
   network cache is bypassed because the addon opens the URL via libavformat directly. There is
   simply no config knob — the buffer must be drained at runtime.

2. **Sender-side encoder load (full-motion content only).** On heavy content (e.g. a shared
   video) the laptop H.264 encoder can't keep up in realtime and falls progressively behind, so
   the RTSP stream's own live edge lags the real screen. This — **not** a Kodi clock drift — is
   what produced the "creeps to ~15 s" behaviour seen in testing. Because the delay is *upstream*
   of Kodi, resync (below) cannot recover it; the lever is reducing encoder load (lower the
   resolution cap; `maintain-resolution` already sheds framerate). Low-motion content (slides,
   code, UI — the meeting-room case) does not hit this.

**Resync (drain to the live edge).** Since Kodi's buffer can't be configured smaller, the
frontend drains it on demand: it detaches the published track(s) with `replaceTrack(null)` for a
few seconds, during which Kodi plays through its buffer at normal speed until it reaches the live
edge, then re-attaches. Video **and** audio are detached in lock-step so A/V stays aligned.
Exposed as a manual **Resync** button. Because Kodi's buffer is otherwise stable, a single drain
holds. (Throttling the canvas paint rate does *not* work as a drain in the foreground —
`captureStream` keeps sampling the unchanged canvas at full rate — so only detaching the track
actually starves the input.)

An **automatic** resync was tried and removed: the drain is briefly disruptive, and in practice
**any real activity self-drains** — a workspace switch is a full-frame change that makes the
encoder briefly drop framerate, an accidental mini-resync that pulls Kodi back to the live edge.

**Background-tab limitation.** The canvas compositor is driven by main-thread timers, which the
browser throttles to ~1 fps when the control tab is hidden *behind another tab*, collapsing the
stream. Mitigation today: keep the control page **in its own browser window** (switching to other
windows/apps does not "hide" the tab). A proper fix means moving the compositor into a Web Worker
+ `OffscreenCanvas` (not visibility-throttled), which has real cross-browser uncertainty
(especially Firefox) and is deferred.


## 3. Implementation Details

### Project Structure

Go module: `github.com/vakabus/kodi-screenshare`

```text
kodi-screenshare/
├── cmd/
│   └── webrtc-bridge/       # main entry point
│       └── main.go
├── internal/
│   ├── mediamtx/            # subprocess management, config generation
│   ├── kodi/                # JSON-RPC client for Kodi (+ .strm generation, lag query)
│   ├── metrics/            # playback-latency time series collector
│   ├── server/             # HTTP API + WHIP reverse proxy
│   └── session/             # session state manager
├── web/                     # static frontend assets (HTML/JS/CSS)
│   └── index.html
├── docs/
│   ├── prd.md
│   └── tech-design.md
├── go.mod
└── go.sum
```

### Configuration

The runtime exposes configurable listen / endpoint / stream-host values so the same binary can be used both on the target LibreELEC box and during LAN-based development against a separate Kodi host.

### Build and Deployment

- The Go application will be compiled statically for the `arm64` architecture (to run on the Raspberry Pi 5).
- The MediaMTX v1.19.1 `arm64` binary will be bundled alongside the Go binary (downloaded from [MediaMTX releases](https://github.com/bluenviron/mediamtx/releases/tag/v1.19.1)).
- LibreELEC does not have a standard package manager, so both binaries can be copied to the `/storage/.config/` directory.
- The bundled Kodi HDMI-CEC addon (`script.kodi-screenshare-cec`) must also be copied into Kodi's addons directory (for LibreELEC: `/storage/.kodi/addons/`) and enabled.
- A custom `systemd` service file will be created in `/storage/.config/system.d/webrtc-bridge.service` to ensure the Go application (which in turn manages MediaMTX) starts automatically when LibreELEC boots.

### Audio

Audio is **enabled**: the frontend requests `audio: true` (with `systemAudio: 'include'`) in
`getDisplayMedia` and publishes the captured audio track (Opus) as a second sendonly track.
MediaMTX muxes it into the RTSP stream and Kodi plays it — **no Go backend or Kodi-controller
changes** were needed. Two caveats:
- **Browser/OS support:** `getDisplayMedia` audio capture is effectively a Chromium feature (tick
  "Share audio" / "Share system audio" in the picker). Firefox on Linux typically captures no
  audio, in which case the share is silently video-only; the frontend handles an absent audio
  track gracefully.
- **A/V sync:** Kodi uses the audio as its master clock and paces video to it. Sync is usually
  good but not always perfect. The resync drain (§2.5) detaches both tracks together so they stay
  aligned through a drain.

### Future Considerations

- **Direct power-state awareness:** The current HDMI-CEC logic tracks whether this app successfully issued a wake command, but it does not read the TV's real power state. If a robust query path becomes available, standby behavior could become more precise.
- **Alternative playback transports:** RTMP or future direct WebRTC playback on the receiver side could still be explored if lower latency or broader player compatibility is needed.
- **HEVC to offload the Pi's decoder (efficiency, not latency):** the Pi 5 has a hardware HEVC
  decoder but no hardware H.264 decoder, so HEVC would move decode off the CPU. **This is not a
  latency fix** — at 1080p H.264 decode is already cheap (~8% CPU, §2.4); the latency is Kodi's
  buffer and (for full-motion) the laptop encoder (§2.5), neither of which HEVC touches. Its
  value is power/thermal headroom and room to push higher resolutions. The chain supports it
  without a rewrite: Chrome 136+/Safari 18 send HEVC over WebRTC when the sender has a hardware
  HEVC encoder (Firefox never does; our Linux Chromium senders don't expose it either, so it's
  untested in practice), MediaMTX 1.19.1 carries H265 over WebRTC→RTSP, and the codec selector
  already prefers H265 with a safe H.264 fallback. Revisit if a presenter platform actually
  exposes HEVC encode — and verify the Pi's HW HEVC decoder engages (codec overlay + low CPU).
- **Adaptive resolution for full-motion content:** when the encoder is CPU-bound
  (`qualityLimitationReason === 'cpu'`) for a sustained period, automatically lower the
  resolution cap so the laptop encoder keeps up (§2.5). The 1440p cap is currently static.
- **Web Worker compositor:** move the canvas compositor to a Web Worker + `OffscreenCanvas` to
  survive background-tab throttling (§2.5). Cross-browser support (especially Firefox) is
  uncertain, so it is deferred in favour of the own-window workaround.
