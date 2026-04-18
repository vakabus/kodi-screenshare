MEDIAMTX_VERSION ?= 1.17.1
HOST_UNAME_S := $(shell uname -s | tr '[:upper:]' '[:lower:]')
HOST_UNAME_M := $(shell uname -m)
HOST_MEDIAMTX_ARCH := $(if $(filter x86_64 amd64,$(HOST_UNAME_M)),amd64,$(if $(filter arm64 aarch64,$(HOST_UNAME_M)),arm64,$(HOST_UNAME_M)))
MEDIAMTX_OS ?= $(HOST_UNAME_S)
MEDIAMTX_ARCH ?= $(HOST_MEDIAMTX_ARCH)
MEDIAMTX_DIR := third_party/mediamtx
MEDIAMTX_BIN := $(MEDIAMTX_DIR)/mediamtx
MEDIAMTX_STAMP := $(MEDIAMTX_DIR)/.fetched-$(MEDIAMTX_VERSION)-$(MEDIAMTX_OS)-$(MEDIAMTX_ARCH)
MEDIAMTX_ASSET := mediamtx_v$(MEDIAMTX_VERSION)_$(MEDIAMTX_OS)_$(MEDIAMTX_ARCH).tar.gz
MEDIAMTX_URL := https://github.com/bluenviron/mediamtx/releases/download/v$(MEDIAMTX_VERSION)/$(MEDIAMTX_ASSET)
DEV_LISTEN_ADDR ?= :8081
KODI_ENDPOINT ?= http://127.0.0.1:8080/jsonrpc
KODI_USERNAME ?=
KODI_PASSWORD ?=
STREAM_HOST ?= 127.0.0.1

.PHONY: fetch-mediamtx clean-mediamtx run-dev

fetch-mediamtx: $(MEDIAMTX_STAMP)

$(MEDIAMTX_STAMP):
	@set -eu; \
	mkdir -p $(MEDIAMTX_DIR); \
	tmp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$tmp_dir"' EXIT; \
	echo "Fetching $(MEDIAMTX_ASSET)"; \
	curl -L --fail --output "$$tmp_dir/$(MEDIAMTX_ASSET)" "$(MEDIAMTX_URL)"; \
	tar -xzf "$$tmp_dir/$(MEDIAMTX_ASSET)" -C "$$tmp_dir"; \
	cp "$$tmp_dir/mediamtx" "$(MEDIAMTX_BIN)"; \
	chmod 0755 "$(MEDIAMTX_BIN)"; \
	rm -f $(MEDIAMTX_DIR)/.fetched-*; \
	touch "$(MEDIAMTX_STAMP)"; \
	echo "Fetched MediaMTX to $(MEDIAMTX_BIN)"

clean-mediamtx:
	rm -rf $(MEDIAMTX_DIR)

run-dev: $(MEDIAMTX_STAMP)
	@set -eu; \
	go run ./cmd/webrtc-bridge \
		-listen-addr "$(DEV_LISTEN_ADDR)" \
		-kodi-endpoint "$(KODI_ENDPOINT)" \
		-kodi-username "$(KODI_USERNAME)" \
		-kodi-password "$(KODI_PASSWORD)" \
		-stream-host "$(STREAM_HOST)"