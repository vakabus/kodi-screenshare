# Technical Design Document: Kodi WebRTC-to-RTSP Screen Share Bridge

## 1. System Architecture

The system consists of two processes running on the LibreELEC Raspberry Pi 5:

1. **A custom Go application** — the main orchestrator, serving the web UI, managing session state, and controlling Kodi.
2. **MediaMTX v1.17.1** — run as a **sidecar subprocess** managed by the Go app, handling WebRTC ingest and exposing the live stream for Kodi playback over RTSP.

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
  video: true,
  audio: false   // audio excluded for v1, architecture supports future addition
});
```

#### H.264 Codec Enforcement

The browser **must** send H.264 (not VP8/VP9) so MediaMTX can forward the video into Kodi-compatible playback formats without CPU-heavy transcoding. This is achieved using `RTCRtpTransceiver.setCodecPreferences()`, which is [Baseline 2024](https://caniuse.com/mdn-api_rtcrtptransceiver_setcodecpreferences) — supported in Chrome 76+, Firefox 128+, Safari 17.4+, Edge 79+:

```js
const pc = new RTCPeerConnection();
const transceiver = pc.addTransceiver(track, { direction: 'sendonly' });
const codecs = RTCRtpSender.getCapabilities('video').codecs;
const h264Codecs = codecs.filter(c => c.mimeType === 'video/H264');
transceiver.setCodecPreferences([...h264Codecs, ...codecs.filter(c => c.mimeType !== 'video/H264')]);
```

No raw SDP munging is required.

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

	hlsAlwaysRemux: yes    # HLS remains available for diagnostics/compatibility experiments
	hlsVariant: mpegts
	hlsSegmentCount: 3
	hlsSegmentDuration: 1s

paths:
  screenshare:
    runOnReady: >
      curl -s -X POST http://127.0.0.1:80/api/hooks/ready
    runOnReadyRestart: no
    runOnNotReady: >
      curl -s -X POST http://127.0.0.1:80/api/hooks/not-ready
```

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

2. The RTSP playback command:

```json
{
  "jsonrpc": "2.0",
  "method": "Player.Open",
  "params": {
    "item": {
	      "file": "rtsp://<bridge-lan-ip>:8554/screenshare"
    }
  },
  "id": 1
}
```

- **API Endpoint:** `http://127.0.0.1:8080/jsonrpc`
- **Wake transport:** Kodi JSON-RPC does not expose direct builtin execution for HDMI-CEC commands, so the Go app invokes a small bundled Kodi addon (`script.kodi-screenshare-cec`) using `Addons.ExecuteAddon`. That addon runs `xbmc.executebuiltin('CECActivateSource()')`, `CECStandby()`, or `CECToggleState()` on the Kodi side.
- **Stop Event:** When `/api/hooks/not-ready` fires (stream ended), the Go app sends `Player.Stop`. If this session had previously issued a successful wake command, it then sends a matching standby command through the addon so the TV powers down only when the app believes it woke it earlier.

### 2.4. Output Format: RTSP for Kodi Playback

The system uses **RTSP** as the primary output format for Kodi playback.

- **Rationale:** RTSP provides substantially lower end-to-end delay for Kodi than HLS in this deployment while keeping playback stable and avoiding transcoding.
- **Observed behavior:** In local testing, RTSP playback through Kodi was stable and reduced end-to-end delay to roughly 4 seconds, versus roughly 13 seconds for the best stable HLS variant.
- **MediaMTX forwarding:** Because the browser sends H.264 via WebRTC, MediaMTX can expose the same stream over RTSP without CPU-heavy transcoding.
- **HLS status:** HLS generation remains enabled inside MediaMTX for troubleshooting and compatibility experiments, but Kodi playback is designed around RTSP.


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
│   ├── kodi/                # JSON-RPC client for Kodi
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
- The MediaMTX v1.17.1 `arm64` binary will be bundled alongside the Go binary (downloaded from [MediaMTX releases](https://github.com/bluenviron/mediamtx/releases/tag/v1.17.1)).
- LibreELEC does not have a standard package manager, so both binaries can be copied to the `/storage/.config/` directory.
- The bundled Kodi HDMI-CEC addon (`script.kodi-screenshare-cec`) must also be copied into Kodi's addons directory (for LibreELEC: `/storage/.kodi/addons/`) and enabled.
- A custom `systemd` service file will be created in `/storage/.config/system.d/webrtc-bridge.service` to ensure the Go application (which in turn manages MediaMTX) starts automatically when LibreELEC boots.

### Audio

Audio is **excluded in v1** (`audio: false` in `getDisplayMedia`). The architecture supports adding audio later by:
1. Changing the `getDisplayMedia` constraint to `audio: true`.
2. MediaMTX will automatically include the audio track in the outgoing stream.
3. No changes needed in the Go backend or Kodi controller — `Player.Open` plays whatever the RTSP stream contains.

### Future Considerations

- **Direct power-state awareness:** The current HDMI-CEC logic tracks whether this app successfully issued a wake command, but it does not read the TV's real power state. If a robust query path becomes available, standby behavior could become more precise.
- **Alternative playback transports:** RTMP or future direct WebRTC playback on the receiver side could still be explored if lower latency or broader player compatibility is needed.
