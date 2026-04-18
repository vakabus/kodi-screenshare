# Product Requirements Document (PRD): Kodi Meeting Room Screen Share

## 1. Overview

The **Kodi Meeting Room Screen Share** is a unified, self-hosted solution designed to provide frictionless, corporate-style screen sharing for meeting rooms. It enables any user with a web browser to instantly cast their laptop screen to a TV running LibreELEC (Kodi) on a Raspberry Pi 5, without requiring the installation of any client-side software, plugins, or browser extensions.

## 2. Problem Statement

Meeting rooms currently rely on hardware dongles (Chromecast, Airtame) or complex software installations to share screens to a communal TV. LibreELEC, a lightweight OS for Kodi, lacks a native desktop environment or web browser, making standard WebRTC screen-sharing solutions impossible to run natively on the TV interface. We need a way for users to cast their screen to Kodi using only a web URL.

## 3. Target Audience

- **Meeting Participants (Presenters):** Employees or guests who need to share their screen to the TV. They are non-technical and expect a "one-click" experience.
- **IT Administrators:** Need a lightweight, containerized, or single-binary solution that can run in the background on an existing LibreELEC Raspberry Pi 5 setup without altering the core OS.


## 4. Key Requirements (Functional)

1. **Zero-Install Client:** Presenters must be able to share their screen using only a standard modern web browser (Chrome, Firefox, Safari, Edge).
2. **WebRTC Ingestion:** The system must capture the presenter's screen using the browser's native `getDisplayMedia` API and transmit it via WebRTC for low-latency streaming.
3. **Format Conversion:** The system must transmux the incoming WebRTC stream into a format natively supported by Kodi (such as RTMP or HLS) without heavy CPU transcoding.
4. **Automated Kodi Playback:** The moment a screen share begins, the system must automatically command Kodi (via its JSON-RPC API) to open and display the stream on the TV.
5. **TV Wake on Share Start:** If the TV is off and Kodi can control it via HDMI-CEC, the system should wake the TV and activate the Kodi input when a screen share starts.
6. **Conditional TV Standby on Share End:** When a screen share ends, the system should return the TV to standby only if this session previously woke the TV via HDMI-CEC, to avoid turning off a display that was already in use for another purpose.
7. **Single-Node Deployment:** The entire server infrastructure (Web UI hosting, WebRTC server, and Kodi controller) must run locally on the same Raspberry Pi 5 that runs LibreELEC.

## 5. Non-Functional Requirements

- **Low Latency:** The delay between the presenter's screen and the TV display should ideally be under 1.5 seconds to accommodate live presentations.
- **Performance:** The solution must not overwhelm the Raspberry Pi 5 CPU (avoiding heavy video transcoding by keeping the video track in H.264 format).
- **Security:** The web interface should ideally be accessible only within the local corporate network (LAN).


## 6. User Flow

1. User walks into the meeting room. The TV may already be on and showing the idle Kodi home screen, or it may be off in standby.
2. A sign on the wall says: "Go to `http://tv.local` to share your screen."
3. User opens the URL on their laptop and clicks a "Share Screen" button.
4. The browser prompts the user to select which window/screen to share.
5. User selects the screen.
6. If the TV is off and HDMI-CEC is available, Kodi wakes the TV and activates itself as the display source.
7. The TV switches from the Kodi home screen to the live feed of the user's laptop.
8. User clicks "Stop Sharing" on the web page, and Kodi stops playback.
9. If this sharing session previously woke the TV, the system returns the TV to standby; otherwise Kodi simply returns to its home screen without powering the TV off.
