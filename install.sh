#!/usr/bin/env bash
#
# install.sh — Build the Kodi addon zip for kodi-screenshare.
#
# Prerequisites (on the build machine):
#   - Go 1.22+
#   - just (https://github.com/casey/just)
#   - curl, zip
#
# Usage:
#   ./install.sh
#
# Optional environment variables:
#   TARGET_OS            Cross-compile OS (default: linux)
#   TARGET_ARCH          Cross-compile arch (default: arm64)
#   MEDIAMTX_VERSION     MediaMTX release version (default: 1.17.1)
#
set -euo pipefail

echo "==> Building Kodi addon zip..."
just package-addon

ZIP_PATH="build/service.kodi-screenshare.zip"
echo ""
echo "==> Done! Addon zip ready at: ${ZIP_PATH}"
echo ""
echo "To install:"
echo "  1. Copy the zip to your Kodi device:"
echo "       scp ${ZIP_PATH} root@libreelec.lan:/storage/"
echo "  2. In Kodi: Settings → Add-ons → Install from zip file"
echo "  3. Configure the addon with your Kodi web server password:"
echo "       Settings → Add-ons → My add-ons → Services → Kodi Screenshare → Configure"
echo "  4. Restart Kodi to start the bridge."
