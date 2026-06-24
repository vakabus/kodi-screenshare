set dotenv-load := false

mediamtx_version := env_var_or_default("MEDIAMTX_VERSION", "1.19.1")

# Target architecture for cross-compilation (LibreELEC RPi5 = linux/arm64)
target_os := env_var_or_default("TARGET_OS", "linux")
target_arch := env_var_or_default("TARGET_ARCH", "arm64")
build_dir := "build"
deploy_dir := build_dir + "/kodi-screenshare"

kodi_addon_id := "service.kodi-screenshare"
kodi_addon_zip := build_dir + "/" + kodi_addon_id + ".zip"

default:
    @just --list

# Cross-compile the Go binary for the target architecture
build:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p "{{deploy_dir}}"
    echo "Building webrtc-bridge for {{target_os}}/{{target_arch}}..."
    CGO_ENABLED=0 GOOS="{{target_os}}" GOARCH="{{target_arch}}" \
        go build -trimpath -ldflags="-s -w" \
        -o "{{deploy_dir}}/webrtc-bridge" ./cmd/webrtc-bridge
    echo "Built {{deploy_dir}}/webrtc-bridge"

# Fetch MediaMTX for the target architecture and place it in the deploy dir
fetch-mediamtx-target:
    #!/usr/bin/env bash
    set -euo pipefail
    target_stamp="{{deploy_dir}}/.mediamtx-{{mediamtx_version}}-{{target_os}}-{{target_arch}}"
    if [[ -f "$target_stamp" ]]; then
      echo "MediaMTX already fetched for {{target_os}}/{{target_arch}}"
      exit 0
    fi
    mkdir -p "{{deploy_dir}}"
    asset="mediamtx_v{{mediamtx_version}}_{{target_os}}_{{target_arch}}.tar.gz"
    url="https://github.com/bluenviron/mediamtx/releases/download/v{{mediamtx_version}}/$asset"
    tmp_dir="$(mktemp -d)"
    trap 'rm -rf "$tmp_dir"' EXIT
    echo "Fetching $asset for target..."
    curl -L --fail --output "$tmp_dir/$asset" "$url"
    tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"
    cp "$tmp_dir/mediamtx" "{{deploy_dir}}/mediamtx"
    chmod 0755 "{{deploy_dir}}/mediamtx"
    rm -f "{{deploy_dir}}"/.mediamtx-*
    touch "$target_stamp"
    echo "Fetched target MediaMTX to {{deploy_dir}}/mediamtx"

# Package the full Kodi addon zip (bridge + mediamtx + CEC script)
package-addon: build fetch-mediamtx-target
    #!/usr/bin/env bash
    set -euo pipefail
    addon_dir="{{build_dir}}/{{kodi_addon_id}}"
    rm -rf "$addon_dir"
    cp -r "kodi-addon/{{kodi_addon_id}}" "$addon_dir"
    mkdir -p "$addon_dir/bin"
    cp "{{deploy_dir}}/webrtc-bridge" "$addon_dir/bin/webrtc-bridge"
    cp "{{deploy_dir}}/mediamtx" "$addon_dir/bin/mediamtx"
    chmod 0755 "$addon_dir/bin/webrtc-bridge" "$addon_dir/bin/mediamtx"
    rm -f "{{kodi_addon_zip}}"
    (cd "{{build_dir}}" && zip -qr "../{{kodi_addon_zip}}" "{{kodi_addon_id}}")
    echo "Packaged Kodi addon at {{kodi_addon_zip}}"
    ls -lh "{{kodi_addon_zip}}"

kodi_addon_dir := "/storage/.kodi/addons"

# Build the addon and install it on the target host over SSH
install target_host: package-addon
    #!/usr/bin/env bash
    set -euo pipefail
    echo "==> Installing {{kodi_addon_id}} on {{target_host}}..."
    ssh "root@{{target_host}}" "rm -rf '{{kodi_addon_dir}}/{{kodi_addon_id}}'"
    scp -qr "{{build_dir}}/{{kodi_addon_id}}" "root@{{target_host}}:{{kodi_addon_dir}}/"
    echo "==> Enabling addon (if not already enabled)..."
    ssh "root@{{target_host}}" "kodi-send --action='EnableAddon({{kodi_addon_id}})'" || true
    echo "==> Restarting Kodi..."
    ssh "root@{{target_host}}" "systemctl restart kodi"
    echo "==> Done!"

# Show bridge logs from the target host
logs target_host:
    ssh "root@{{target_host}}" "cat /tmp/kodi-screenshare.log 2>/dev/null; echo '--- kodi.log (kodi-screenshare lines) ---'; grep -i kodi-screenshare /storage/.kodi/temp/kodi.log 2>/dev/null || true"

# Remove build artifacts
clean:
    rm -rf "{{build_dir}}"
