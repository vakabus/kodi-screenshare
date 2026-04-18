set dotenv-load := false

mediamtx_version := env_var_or_default("MEDIAMTX_VERSION", "1.17.1")
host_uname_s := `uname -s | tr '[:upper:]' '[:lower:]'`
host_mediamtx_arch := `case "$(uname -m)" in x86_64|amd64) echo amd64 ;; arm64|aarch64) echo arm64 ;; *) uname -m ;; esac`
mediamtx_os := env_var_or_default("MEDIAMTX_OS", host_uname_s)
mediamtx_arch := env_var_or_default("MEDIAMTX_ARCH", host_mediamtx_arch)
mediamtx_dir := "third_party/mediamtx"
mediamtx_bin := mediamtx_dir + "/mediamtx"
mediamtx_stamp := mediamtx_dir + "/.fetched-" + mediamtx_version + "-" + mediamtx_os + "-" + mediamtx_arch
mediamtx_asset := "mediamtx_v" + mediamtx_version + "_" + mediamtx_os + "_" + mediamtx_arch + ".tar.gz"
mediamtx_url := "https://github.com/bluenviron/mediamtx/releases/download/v" + mediamtx_version + "/" + mediamtx_asset

dev_listen_addr := env_var_or_default("DEV_LISTEN_ADDR", ":8081")
kodi_host := env_var_or_default("KODI_HOST", "libreelec.lan")
kodi_endpoint := "http://" + kodi_host + ":8080/jsonrpc"
kodi_username := env_var_or_default("KODI_USERNAME", "kodi")
kodi_password := env_var("KODI_PASSWORD")

kodi_ssh_host := env_var_or_default("KODI_SSH_HOST", "root@libreelec.lan")
kodi_addon_dir := env_var_or_default("KODI_ADDON_DIR", "/storage/.kodi/addons")
kodi_restart_command := env_var_or_default("KODI_RESTART_COMMAND", "systemctl restart kodi")
kodi_rpc_endpoint := env_var_or_default("KODI_RPC_ENDPOINT", "http://libreelec.lan:8080/jsonrpc")
kodi_cec_addon_id := "script.kodi-screenshare-cec"
kodi_cec_addon_zip := "third_party/" + kodi_cec_addon_id + ".zip"
stream_host := `ip route get $(getent hosts libreelec.lan | cut -f1 -d' ') | cut -f5 -d' ' | head -n 1`

default:
    @just --list

fetch-mediamtx:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -f "{{mediamtx_stamp}}" ]]; then
      echo "MediaMTX already fetched for {{mediamtx_os}}/{{mediamtx_arch}}"
      exit 0
    fi
    mkdir -p "{{mediamtx_dir}}"
    tmp_dir="$(mktemp -d)"
    trap 'rm -rf "$tmp_dir"' EXIT
    echo "Fetching {{mediamtx_asset}}"
    curl -L --fail --output "$tmp_dir/{{mediamtx_asset}}" "{{mediamtx_url}}"
    tar -xzf "$tmp_dir/{{mediamtx_asset}}" -C "$tmp_dir"
    cp "$tmp_dir/mediamtx" "{{mediamtx_bin}}"
    chmod 0755 "{{mediamtx_bin}}"
    rm -f "{{mediamtx_dir}}"/.fetched-*
    touch "{{mediamtx_stamp}}"
    echo "Fetched MediaMTX to {{mediamtx_bin}}"

clean-mediamtx:
    rm -rf "{{mediamtx_dir}}"

run-dev: fetch-mediamtx
    #!/usr/bin/env bash
    set -euo pipefail
    stream_host="{{stream_host}}"
    echo "Using stream host $stream_host"
    go run ./cmd/webrtc-bridge \
      -listen-addr "{{dev_listen_addr}}" \
      -kodi-endpoint "{{kodi_endpoint}}" \
      -kodi-username "{{kodi_username}}" \
      -kodi-password "{{kodi_password}}" \
      -stream-host "$stream_host"

package-kodi-cec-addon:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p third_party
    rm -f "{{kodi_cec_addon_zip}}"
    (cd kodi-addon && zip -qr "../{{kodi_cec_addon_zip}}" "{{kodi_cec_addon_id}}")
    echo "Packaged Kodi CEC addon at {{kodi_cec_addon_zip}}"

enable-kodi-cec-addon:
    #!/usr/bin/env python3
    import base64
    import json
    import urllib.request

    url = "{{kodi_rpc_endpoint}}"
    user = "{{kodi_username}}"
    password = "{{kodi_password}}"
    addon_id = "{{kodi_cec_addon_id}}"

    if not user:
        raise SystemExit("KODI_USERNAME is required to enable the addon via JSON-RPC")
    if not password:
        raise SystemExit("KODI_PASSWORD is required to enable the addon via JSON-RPC")

    print(f"Enabling {addon_id} via {url}")

    headers = {"Content-Type": "application/json"}
    token = base64.b64encode(f"{user}:{password}".encode()).decode()
    headers["Authorization"] = f"Basic {token}"


    def call(method, params=None):
        payload = {"jsonrpc": "2.0", "method": method, "id": 1}
        if params is not None:
            payload["params"] = params
        request = urllib.request.Request(url, data=json.dumps(payload).encode(), headers=headers)
        with urllib.request.urlopen(request, timeout=20) as response:
            data = json.loads(response.read().decode())
        if "error" in data:
            raise SystemExit(f"{method} failed: {data['error']}")
        return data["result"]


    call("Addons.SetAddonEnabled", {"addonid": addon_id, "enabled": True})
    details = call(
        "Addons.GetAddonDetails",
        {"addonid": addon_id, "properties": ["enabled", "installed", "path", "version"]},
    )
    addon = details["addon"]
    if not addon.get("enabled"):
        raise SystemExit(f"Addon {addon_id} is still disabled after enabling attempt")
    print(json.dumps(addon, indent=2, sort_keys=True))

install-kodi-cec-addon:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Installing {{kodi_cec_addon_id}} to {{kodi_ssh_host}}:{{kodi_addon_dir}}"
    tar -C kodi-addon -cf - "{{kodi_cec_addon_id}}" | ssh "{{kodi_ssh_host}}" 'mkdir -p "{{kodi_addon_dir}}" && rm -rf "{{kodi_addon_dir}}/{{kodi_cec_addon_id}}" && tar -C "{{kodi_addon_dir}}" -xf -'
    echo "Installed addon files. Enabling addon via JSON-RPC..."
    just enable-kodi-cec-addon

restart-kodi:
    ssh "{{kodi_ssh_host}}" '{{kodi_restart_command}}'
