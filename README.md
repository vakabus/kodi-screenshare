# kodi-screenshare

Share your screen to a TV running [LibreELEC](https://libreelec.tv/) (Kodi) on a Raspberry Pi — no apps, no dongles, just a web browser.

A user opens a URL, clicks "Share Screen", and their desktop is instantly displayed on the meeting room TV. When they stop, the TV goes back to idle. It handles HDMI-CEC wake/standby automatically.

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

To skip the prompts (e.g. in CI), set environment variables instead:

```bash
KODI_SSH_HOST=root@libreelec.lan KODI_USERNAME=kodi KODI_PASSWORD=secret ./install.sh
```

### Configuration

| Variable | Default | Description |
|---|---|---|
| `KODI_SSH_HOST` | *(prompted)* | SSH target for deployment |
| `KODI_USERNAME` | *(prompted)* | Kodi web server username |
| `KODI_PASSWORD` | *(prompted)* | Kodi web server password |
| `TARGET_OS` | `linux` | Go cross-compile target OS |
| `TARGET_ARCH` | `arm64` | Go cross-compile target arch |
| `INSTALL_DIR` | `/storage/kodi-screenshare` | Install path on the Pi |
| `MEDIAMTX_VERSION` | `1.17.1` | MediaMTX release to bundle |

### What gets installed

- `/storage/kodi-screenshare/webrtc-bridge` — the Go binary
- `/storage/kodi-screenshare/mediamtx` — MediaMTX binary
- `/storage/.config/system.d/webrtc-bridge.service` — systemd unit
- `/storage/.kodi/addons/script.kodi-screenshare-cec/` — Kodi CEC addon

The service starts on boot and listens on port 443 (HTTPS with an auto-generated self-signed certificate).

## Browser support

Screen sharing uses the `getDisplayMedia` API with H.264 codec preference via `setCodecPreferences`. Supported in:

- Chrome 76+
- Firefox 128+
- Safari 17.4+
- Edge 79+

Your browser will show a certificate warning because of the self-signed TLS cert — this is expected.

## Development

For local development against a remote Kodi host, use the [Justfile](https://github.com/casey/just):

```bash
# Install just: https://github.com/casey/just#installation
KODI_PASSWORD=yourpassword just run-dev
```

This fetches MediaMTX for your host platform, compiles the Go app, and runs it locally. It auto-detects your LAN IP for the RTSP stream URL so Kodi can reach it from the Pi.

### Useful Justfile targets

| Target | Description |
|---|---|
| `just run-dev` | Run locally against a remote Kodi |
| `just build` | Cross-compile the Go binary |
| `just package` | Build + fetch MediaMTX + assemble deploy package |
| `just install` | Full build + deploy over SSH |
| `just clean` | Remove build artifacts |

### Running tests

```bash
go test ./...
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

## TLS

The bridge serves HTTPS on port 443 with an auto-generated self-signed certificate. This is necessary because browsers require a [secure context](https://developer.mozilla.org/en-US/docs/Web/API/MediaDevices/getDisplayMedia) to use `getDisplayMedia`.

To use your own certificate:

```bash
webrtc-bridge -tls-cert /path/to/cert.pem -tls-key /path/to/key.pem
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

See [LICENSE](LICENSE) for details.
