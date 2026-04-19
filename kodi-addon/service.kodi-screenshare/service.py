"""Kodi service that starts the webrtc-bridge process on startup."""

import os
import subprocess
import signal

import xbmc
import xbmcaddon


def main() -> None:
    addon = xbmcaddon.Addon()
    addon_path = addon.getAddonInfo("path")
    bin_dir = os.path.join(addon_path, "bin")
    bridge_bin = os.path.join(bin_dir, "webrtc-bridge")
    mediamtx_bin = os.path.join(bin_dir, "mediamtx")

    listen_addr = addon.getSetting("listen_addr") or ":443"
    stream_host = addon.getSetting("stream_host") or "127.0.0.1"

    cmd = [
        bridge_bin,
        "-listen-addr", listen_addr,
        "-mediamtx-path", mediamtx_bin,
        "-stream-host", stream_host,
    ]

    log_path = "/tmp/kodi-screenshare.log"
    max_log_bytes = 1 * 1024 * 1024  # 1 MB

    log_file = open(log_path, "a")  # noqa: SIM115

    monitor = xbmc.Monitor()
    xbmc.log(f"kodi-screenshare: starting bridge: {' '.join(cmd)}", xbmc.LOGINFO)
    xbmc.log(f"kodi-screenshare: bridge log at {log_path}", xbmc.LOGINFO)

    proc = subprocess.Popen(cmd, cwd=bin_dir, stdout=log_file, stderr=subprocess.STDOUT)

    try:
        while not monitor.abortRequested():
            if monitor.waitForAbort(1):
                break
            if proc.poll() is not None:
                xbmc.log(
                    f"kodi-screenshare: bridge exited with code {proc.returncode}",
                    xbmc.LOGWARNING,
                )
                break
            # Truncate the log file if it gets too large.
            try:
                if os.path.getsize(log_path) > max_log_bytes:
                    log_file.close()
                    with open(log_path, "rb") as f:
                        f.seek(-max_log_bytes // 2, os.SEEK_END)
                        f.readline()  # skip partial line
                        tail = f.read()
                    log_file = open(log_path, "w")  # noqa: SIM115
                    log_file.write(tail.decode(errors="replace"))
                    log_file.flush()
                    proc.stdout = log_file  # not needed but keeps reference consistent
            except OSError:
                pass
    finally:
        if proc.poll() is None:
            xbmc.log("kodi-screenshare: stopping bridge", xbmc.LOGINFO)
            proc.send_signal(signal.SIGTERM)
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
        log_file.close()


if __name__ == "__main__":
    main()
