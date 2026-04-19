#!/usr/bin/env bash
#
# install.sh — Build and deploy kodi-screenshare to a LibreELEC device over SSH.
#
# Prerequisites (on the build machine):
#   - Go 1.22+
#   - curl
#   - ssh access to the LibreELEC device
#
# Usage:
#   ./install.sh
#
# The script will prompt for the SSH host, Kodi username, and password.
# All prompts can be skipped by setting environment variables:
#   KODI_SSH_HOST, KODI_USERNAME, KODI_PASSWORD
#
# Other optional environment variables:
#   TARGET_OS            Cross-compile OS (default: linux)
#   TARGET_ARCH          Cross-compile arch (default: arm64)
#   INSTALL_DIR          Remote install path (default: /storage/kodi-screenshare)
#   SYSTEMD_DIR          Remote systemd path (default: /storage/.config/system.d)
#   MEDIAMTX_VERSION     MediaMTX release version (default: 1.17.1)
#
set -euo pipefail

# --- Interactive prompts (skipped if env vars are already set) ---
if [[ -z "${KODI_SSH_HOST:-}" ]]; then
    read -rp "SSH host [root@libreelec.lan]: " KODI_SSH_HOST
    KODI_SSH_HOST="${KODI_SSH_HOST:-root@libreelec.lan}"
fi

if [[ -z "${KODI_USERNAME:-}" ]]; then
    read -rp "Kodi web server username [kodi]: " KODI_USERNAME
    KODI_USERNAME="${KODI_USERNAME:-kodi}"
fi

if [[ -z "${KODI_PASSWORD:-}" ]]; then
    read -rsp "Kodi web server password: " KODI_PASSWORD
    echo
    if [[ -z "${KODI_PASSWORD}" ]]; then
        echo "Error: password cannot be empty." >&2
        exit 1
    fi
fi

# --- Configuration ---
# Extract hostname from SSH target (strip user@ prefix)
KODI_HOST="${KODI_SSH_HOST#*@}"
TARGET_OS="${TARGET_OS:-linux}"
TARGET_ARCH="${TARGET_ARCH:-arm64}"
INSTALL_DIR="${INSTALL_DIR:-/storage/kodi-screenshare}"
SYSTEMD_DIR="${SYSTEMD_DIR:-/storage/.config/system.d}"
MEDIAMTX_VERSION="${MEDIAMTX_VERSION:-1.17.1}"
KODI_ADDON_DIR="${KODI_ADDON_DIR:-/storage/.kodi/addons}"
KODI_RPC_ENDPOINT="http://${KODI_HOST}:8080/jsonrpc"
KODI_CEC_ADDON_ID="script.kodi-screenshare-cec"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/build/kodi-screenshare"

# --- Step 1: Cross-compile Go binary ---
echo "==> Building webrtc-bridge for ${TARGET_OS}/${TARGET_ARCH}..."
mkdir -p "${BUILD_DIR}"
CGO_ENABLED=0 GOOS="${TARGET_OS}" GOARCH="${TARGET_ARCH}" \
    go build -trimpath -ldflags="-s -w" \
    -o "${BUILD_DIR}/webrtc-bridge" ./cmd/webrtc-bridge

# --- Step 2: Fetch MediaMTX for target ---
mediamtx_stamp="${BUILD_DIR}/.mediamtx-${MEDIAMTX_VERSION}-${TARGET_OS}-${TARGET_ARCH}"
if [[ ! -f "${mediamtx_stamp}" ]]; then
    asset="mediamtx_v${MEDIAMTX_VERSION}_${TARGET_OS}_${TARGET_ARCH}.tar.gz"
    url="https://github.com/bluenviron/mediamtx/releases/download/v${MEDIAMTX_VERSION}/${asset}"
    tmp_dir="$(mktemp -d)"
    trap 'rm -rf "${tmp_dir}"' EXIT
    echo "==> Fetching MediaMTX ${MEDIAMTX_VERSION} for ${TARGET_OS}/${TARGET_ARCH}..."
    curl -L --fail --output "${tmp_dir}/${asset}" "${url}"
    tar -xzf "${tmp_dir}/${asset}" -C "${tmp_dir}"
    cp "${tmp_dir}/mediamtx" "${BUILD_DIR}/mediamtx"
    chmod 0755 "${BUILD_DIR}/mediamtx"
    rm -f "${BUILD_DIR}"/.mediamtx-*
    touch "${mediamtx_stamp}"
else
    echo "==> MediaMTX already fetched for ${TARGET_OS}/${TARGET_ARCH}"
fi

# --- Step 3: Deploy over SSH ---
echo "==> Deploying to ${KODI_SSH_HOST}..."

# Open a shared SSH connection so we only authenticate once
ctl=$(mktemp -u /tmp/kodi-ssh-XXXXXX)
ssh_opts=(-o "ControlMaster=auto" -o "ControlPath=${ctl}" -o "ControlPersist=60")
cleanup() { ssh "${ssh_opts[@]}" -O exit "${KODI_SSH_HOST}" 2>/dev/null || true; }
trap cleanup EXIT

ssh "${ssh_opts[@]}" -fN "${KODI_SSH_HOST}"

echo "  Stopping webrtc-bridge (if running)..."
ssh "${ssh_opts[@]}" "${KODI_SSH_HOST}" 'systemctl stop webrtc-bridge 2>/dev/null || true'

echo "  Uploading binaries to ${INSTALL_DIR}..."
ssh "${ssh_opts[@]}" "${KODI_SSH_HOST}" "mkdir -p '${INSTALL_DIR}'"
scp -q "${ssh_opts[@]}" "${BUILD_DIR}/webrtc-bridge" "${BUILD_DIR}/mediamtx" "${KODI_SSH_HOST}:${INSTALL_DIR}/"

echo "  Installing systemd service..."
sed \
    -e "s|%KODI_PASSWORD%|${KODI_PASSWORD}|g" \
    -e 's|%STREAM_HOST%|127.0.0.1|g' \
    deploy/webrtc-bridge.service \
    | ssh "${ssh_opts[@]}" "${KODI_SSH_HOST}" "mkdir -p '${SYSTEMD_DIR}' && cat > '${SYSTEMD_DIR}/webrtc-bridge.service'"
ssh "${ssh_opts[@]}" "${KODI_SSH_HOST}" "mkdir -p '${SYSTEMD_DIR}/kodi.target.wants' && ln -sf ../webrtc-bridge.service '${SYSTEMD_DIR}/kodi.target.wants/webrtc-bridge.service'"
ssh "${ssh_opts[@]}" "${KODI_SSH_HOST}" 'systemctl daemon-reload'

echo "  Installing CEC addon..."
tar -C kodi-addon -cf - "${KODI_CEC_ADDON_ID}" \
    | ssh "${ssh_opts[@]}" "${KODI_SSH_HOST}" \
        "mkdir -p '${KODI_ADDON_DIR}' && rm -rf '${KODI_ADDON_DIR}/${KODI_CEC_ADDON_ID}' && tar -C '${KODI_ADDON_DIR}' -xf -"

echo "  Starting webrtc-bridge service..."
ssh "${ssh_opts[@]}" "${KODI_SSH_HOST}" 'systemctl enable --now webrtc-bridge'

# --- Step 4: Enable CEC addon via JSON-RPC ---
echo "  Enabling CEC addon via Kodi JSON-RPC..."
payload='{"jsonrpc":"2.0","method":"Addons.SetAddonEnabled","params":{"addonid":"'"${KODI_CEC_ADDON_ID}"'","enabled":true},"id":1}'
auth_header="$(echo -n "${KODI_USERNAME}:${KODI_PASSWORD}" | base64)"
curl -s -X POST "${KODI_RPC_ENDPOINT}" \
    -H "Content-Type: application/json" \
    -H "Authorization: Basic ${auth_header}" \
    -d "${payload}" > /dev/null

echo ""
echo "==> Done! kodi-screenshare is running on ${KODI_SSH_HOST}"
echo "    Open https://${KODI_HOST} in your browser to share your screen."
