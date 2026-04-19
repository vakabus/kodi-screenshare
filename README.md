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

Everything runs on the same Raspberry Pi as a single systemd service.

## Requirements

**Target device (Raspberry Pi):**
- LibreELEC with Kodi
- Kodi web server enabled with a password set (Settings → Services → Control)

**Build machine:**
- [Go](https://go.dev/) 1.22+
- `curl`, `ssh`, `tar` (standard Unix tools)
- SSH access to the LibreELEC device (LibreELEC uses `root` with SSH enabled)

## Quick install

The `install.sh` script builds everything, copies it to the Pi over SSH, and starts the service:

```bash
./install.sh
```

It will prompt for the SSH host, Kodi username, and password. That's it — once it finishes, open `https://<your-pi-ip>` in your browser.

## Browser support

- Tested on Firefox on Linux, full-screen sharing works reasonably well.
- Sharing a single tab from Chromium works, but not well. Resing the window breaks the stream.

Your browser will show a certificate warning because of the self-signed TLS cert.

## Development

For local development against a remote Kodi host, use the [Justfile](https://github.com/casey/just): You might have to tweak some variables in the Justfile to make it work against your specific setup.

```bash
# Install just: https://github.com/casey/just#installation
KODI_PASSWORD=yourpassword just run-dev
```

### Project structure

```
cmd/webrtc-bridge/       Main entry point
internal/
  kodi/                  Kodi JSON-RPC client (playback + CEC)
  mediamtx/              MediaMTX subprocess manager + REST API client
  server/                HTTP server (web UI, API hooks, WHIP proxy)
  session/               In-memory session state
web/                     Embedded frontend (HTML/JS)
kodi-addon/              Kodi HDMI-CEC helper addon
deploy/                  Systemd service template
docs/                    Design documents (PRD, technical design)
```

## Uninstalling

SSH into the Pi and run:

```bash
systemctl disable --now webrtc-bridge
rm -rf /storage/kodi-screenshare
rm /storage/.config/system.d/webrtc-bridge.service
rm -rf /storage/.kodi/addons/script.kodi-screenshare-cec
systemctl daemon-reload
```

## License

Licensed under GPL-3.0. Full text in [LICENSE](LICENSE).
