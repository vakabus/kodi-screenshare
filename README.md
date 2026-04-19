# kodi-screenshare

Share your screen to a TV running [LibreELEC](https://libreelec.tv/) (Kodi) on a Raspberry Pi — no apps, no dongles, just a web browser.

A user opens a URL, clicks "Share Screen", and their desktop is instantly displayed on the TV. When they stop, the TV goes back to idle. It handles HDMI-CEC wake/standby automatically.

## How it works

```
Browser ──WebRTC (H.264)──▶ MediaMTX ──RTSP──▶ Kodi player ──▶ TV
                                ▲                    ▲
                          Go bridge app         JSON-RPC control
```

1. The presenter opens `https://<tv-ip>` and clicks **Share Screen**.
2. The browser captures the screen and sends it via WebRTC (WHIP) to [MediaMTX](https://github.com/bluenviron/mediamtx).
3. MediaMTX re-publishes the stream over RTSP (no transcoding — H.264 passthrough).
4. The Go bridge tells Kodi to play the RTSP stream via JSON-RPC.
5. A bundled Kodi addon handles HDMI-CEC: waking the TV when sharing starts and putting it on standby when sharing ends (only if the app woke it).

Everything runs on the same Raspberry Pi, packaged as a single Kodi addon.

## Requirements

**Target device (Raspberry Pi):**
- LibreELEC with Kodi
- "Unknown sources" enabled in Kodi (Settings → System → Add-ons)

**Build machine:**
- [Go](https://go.dev/) 1.22+
- [just](https://github.com/casey/just) task runner
- `curl`, `zip` (standard Unix tools)

## Quick install

1. **Build the addon zip** on your development machine:

   ```bash
   just package-addon
   ```

   This cross-compiles the Go bridge for linux/arm64, fetches MediaMTX, and packages everything into `build/service.kodi-screenshare.zip`.

2. **Copy the zip** to your Kodi device (USB drive, `scp`, network share, etc.):

   ```bash
   scp build/service.kodi-screenshare.zip root@libreelec.lan:/storage/
   ```

3. **Install in Kodi:** Settings → Add-ons → Install from zip file → select `service.kodi-screenshare.zip`.

4. **Restart Kodi** to start the bridge service. Open `https://<your-pi-ip>` in your browser.

## Browser support

- Tested on Firefox on Linux, full-screen sharing works reasonably well.
- Sharing a single tab from Chromium works, but not well. Resing the window breaks the stream.

Your browser will show a certificate warning because of the self-signed TLS cert.

## Development

The project uses a [Justfile](https://github.com/casey/just) for build tasks. Run `just` to see available recipes.

To iterate on a LibreELEC device over SSH:

```bash
# Build, copy the addon, and restart Kodi on the target
just install libreelec.lan

# View bridge and Kodi logs from the target
just logs libreelec.lan
```

The bridge logs to `/tmp/kodi-screenshare.log` on the device (tmpfs, capped at 1 MB).

### Project structure

```
cmd/webrtc-bridge/       Main entry point
internal/
  kodi/                  Kodi JSON-RPC client (playback + CEC)
  mediamtx/              MediaMTX subprocess manager + REST API client
  server/                HTTP server (web UI, API hooks, WHIP proxy)
  session/               In-memory session state
web/                     Embedded frontend (HTML/JS)
kodi-addon/              Kodi addon (service + CEC helper)
docs/                    Design documents (PRD, technical design)
```

## Uninstalling

In Kodi: Settings → Add-ons → My add-ons → Services → Kodi Screenshare → Uninstall.

## License

Licensed under GPL-3.0. Full text in [LICENSE](LICENSE).
