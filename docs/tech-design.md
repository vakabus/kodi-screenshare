# Technical Design Document: Kodi WebRTC-to-HLS Screen Share Bridge

## 1. System Architecture

The system relies on a single custom Go application running in the background of the LibreELEC OS (either deployed as a standalone static binary or via the official LibreELEC Docker add-on).

The Go application combines three core responsibilities:

1. **HTTP Web Server:** Serves the static HTML/JS frontend to the presenters.
2. **Embedded MediaMTX Server:** Acts as the WebRTC ingest point and HLS/RTMP transmuxer.
3. **Kodi API Controller:** Triggers Kodi playback commands based on stream state events.

### Architecture Diagram (Logical Flow)

```text
[ Presenter Laptop (Browser) ]
        |
        | 1. HTTP GET (Loads HTML/JS Frontend)
        | 2. WebRTC WHIP (Pushes H.264 video track)
        v
[ Custom Go Application (Raspberry Pi 5) ]
        |
        |-- (Embedded MediaMTX Engine) -- Receives WebRTC -> Transmuxes to HLS/RTMP
        |
        | 3. HTTP POST (JSON-RPC playback command)
        v
[ Kodi Web Server (localhost:8080) ]
        |
        | 4. Kodi internal player pulls the HLS/RTMP stream
        v
[ TV Display ]
```


## 2. Component Design

### 2.1. Frontend Web Client

A static single-page application (HTML/JS) served by the Go backend.

- **Capture:** Uses `navigator.mediaDevices.getDisplayMedia({ video: true, audio: false })` to capture the desktop.
- **Publish:** Implements a WebRTC WHIP client (WebRTC HTTP Ingestion Protocol). It creates an RTCPeerConnection and pushes the video track to the Go server's ingestion endpoint (e.g., `http://<pi-ip>:8889/screenshare/whip`).


### 2.2. Go Backend \& MediaMTX Integration

The core backend is written in Go to easily embed the `bluenviron/mediamtx` library.

- **MediaMTX Initialization:** The Go app initializes `mediamtx.NewServer()` in memory. MediaMTX is configured to accept WebRTC (WHIP) on port `8889` and output HLS on port `8888`.
- **Transmuxing:** Because browsers can output WebRTC video as H.264, MediaMTX simply extracts the H.264 NAL units from the RTP packets and packages them into an HLS `.m3u8` playlist or an RTMP stream. This uses virtually zero CPU compared to transcoding via FFmpeg.
- **Webhook/Event Hooks:** MediaMTX allows configuring "RunOnReady" hooks. When the `screenshare` path goes live, MediaMTX triggers an internal Go function.


### 2.3. Kodi JSON-RPC Controller

When the Go application detects that the WebRTC stream has been successfully established and the HLS output is ready, it executes an HTTP request against Kodi's local web server API.

- **API Endpoint:** `http://127.0.0.1:8080/jsonrpc`
- **Payload:**

```json
{
  "jsonrpc": "2.0",
  "method": "Player.Open",
  "params": {
    "item": {
      "file": "http://127.0.0.1:8888/screenshare/stream.m3u8"
    }
  },
  "id": 1
}
```

- **Stop Event:** When the WebRTC connection is terminated by the browser, the Go app sends a `Player.Stop` JSON-RPC command to Kodi to return it to the home screen.


## 3. Implementation Details

### Build and Deployment

- The Go application will be compiled statically for the `arm64` architecture (to run on the Raspberry Pi 5).
- LibreELEC does not have a standard package manager, so the compiled binary can be copied to the `/storage/.config/` directory.
- A custom `systemd` service file will be created in `/storage/.config/system.d/webrtc-bridge.service` to ensure the Go application starts automatically when LibreELEC boots.


### Configuration Constraints

- Presenters' browsers MUST negotiate H.264 as the video codec. If the browser attempts to send VP8 or VP9 (the default for some WebRTC implementations), MediaMTX will not be able to package it into RTMP/HLS without CPU-heavy transcoding. The JavaScript WHIP client must forcefully modify the SDP offer to prioritize the H.264 payload type before sending it to the server.
<span style="display:none">[^1][^10][^2][^3][^4][^5][^6][^7][^8][^9]</span>

<div align="center">⁂</div>

[^1]: https://stackoverflow.com/questions/51451572/screen-sharing-in-native-ios-app-using-webrtc

[^2]: https://www.instructables.com/WebRTC-Video-Chat-in-20-Lines-of-JavaScript/

[^3]: https://www.webology.org/data-cms/articles/20220530124743pmwebology 19 (3) - 120 pdf.pdf

[^4]: https://is.muni.cz/th/vjhke/WebRTC-Communication-Portal.pdf

[^5]: https://www.metered.ca/blog/webrtc-screen-sharing/

[^6]: https://www.videosdk.live/developer-hub/webrtc/webrtc-to-rtmp

[^7]: https://www.scribd.com/document/964661722/ReportIT-1

[^8]: https://dev.to/harshitk/integrating-rtmp-and-webrtc-for-real-time-streaming-2lbb

[^9]: https://www.tmssoftware.com/site/blog.asp?post=1117

[^10]: https://www.atlantis-press.com/article/125982858.pdf
