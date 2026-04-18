package kodi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpen(t *testing.T) {
	t.Parallel()

	var methods []string
	var got rpcRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jsonrpc" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		methods = append(methods, got.Method)
		switch got.Method {
		case "XBMC.GetInfoBooleans":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]bool{"System.ScreenSaverActive": true}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		}
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", "rtsp://stream.example:8554/screenshare", "", "", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if len(methods) != 3 || methods[0] != "XBMC.GetInfoBooleans" || methods[1] != "Addons.ExecuteAddon" || methods[2] != "Player.Open" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
	params, ok := got.Params.(map[string]any)
	if !ok {
		t.Fatalf("unexpected params type: %T", got.Params)
	}
	item, ok := params["item"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected item type: %T", params["item"])
	}
	if item["file"] != "rtsp://stream.example:8554/screenshare" {
		t.Fatalf("unexpected file payload: %#v", item)
	}
}

func TestOpenRetriesTransientRPCFailure(t *testing.T) {
	t.Parallel()

	var methods []string
	var openAttempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jsonrpc" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		methods = append(methods, req.Method)
		if req.Method == "XBMC.GetInfoBooleans" {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]bool{"System.ScreenSaverActive": true}})
			return
		}
		if req.Method == "Player.Open" {
			openAttempts++
		}
		if req.Method == "Player.Open" && openAttempts == 1 {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", "rtsp://stream.example:8554/screenshare", "", "", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if openAttempts != 2 {
		t.Fatalf("expected retry after transient RPC failure, got %d Player.Open attempts", openAttempts)
	}
	if len(methods) != 4 || methods[0] != "XBMC.GetInfoBooleans" || methods[1] != "Addons.ExecuteAddon" || methods[2] != "Player.Open" || methods[3] != "Player.Open" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
}

func TestOpenFailsWhenRPCNeverSucceeds(t *testing.T) {
	t.Parallel()

	var openAttempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jsonrpc" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Method == "XBMC.GetInfoBooleans" {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]bool{"System.ScreenSaverActive": true}})
			return
		}
		if req.Method == "Player.Open" {
			openAttempts++
			http.Error(w, "still failing", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", "rtsp://stream.example:8554/screenshare", "", "", server.Client())
	err := client.Open(context.Background())
	if err == nil {
		t.Fatal("expected Open() to fail when RPC never succeeds")
	}
	if !strings.Contains(err.Error(), "open Kodi stream at rtsp://stream.example:8554/screenshare") {
		t.Fatalf("expected RTSP open error, got %v", err)
	}
	if openAttempts != openRetryCount {
		t.Fatalf("expected %d Player.Open attempts, got %d", openRetryCount, openAttempts)
	}
	if client.consumeWokeDisplay() {
		t.Fatal("expected failed Open() to clear wake-tracking state")
	}
}

func TestOpenContinuesWhenWakeFails(t *testing.T) {
	t.Parallel()

	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		methods = append(methods, req.Method)
		if req.Method == "XBMC.GetInfoBooleans" {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]bool{"System.ScreenSaverActive": true}})
			return
		}
		if req.Method == "Addons.ExecuteAddon" {
			http.Error(w, "addon missing", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
	}))
	defer server.Close()

	client := NewClient(server.URL, "rtsp://stream.example:8554/screenshare", "", "", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(methods) != 3 || methods[0] != "XBMC.GetInfoBooleans" || methods[1] != "Addons.ExecuteAddon" || methods[2] != "Player.Open" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
}

func TestStop(t *testing.T) {
	t.Parallel()

	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		methods = append(methods, req.Method)

		switch req.Method {
		case "Player.GetActivePlayers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"playerid": 1, "type": "video"}},
			})
		case "Player.Stop":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		default:
			t.Fatalf("unexpected method: %s", req.Method)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "ignored", "", "", server.Client())
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if len(methods) != 2 {
		t.Fatalf("expected 2 RPC calls, got %d", len(methods))
	}
	if methods[0] != "Player.GetActivePlayers" || methods[1] != "Player.Stop" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
}

func TestStopSendsStandbyWhenWakeWasTracked(t *testing.T) {
	t.Parallel()

	var methods []string
	var commands []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		methods = append(methods, req.Method)

		switch req.Method {
		case "Player.GetActivePlayers":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"playerid": 1, "type": "video"}},
			})
		case "Player.Stop":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		case "Addons.ExecuteAddon":
			params, ok := req.Params.(map[string]any)
			if !ok {
				t.Fatalf("unexpected params type: %T", req.Params)
			}
			addonParams, ok := params["params"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected addon params type: %T", params["params"])
			}
			commands = append(commands, addonParams["command"].(string))
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		default:
			t.Fatalf("unexpected method: %s", req.Method)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "ignored", "", "", server.Client())
	client.setWokeDisplay(true)
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if len(methods) != 3 {
		t.Fatalf("expected 3 RPC calls, got %d", len(methods))
	}
	if methods[0] != "Player.GetActivePlayers" || methods[1] != "Player.Stop" || methods[2] != "Addons.ExecuteAddon" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
	if len(commands) != 1 || commands[0] != "standby" {
		t.Fatalf("unexpected addon commands: %#v", commands)
	}
}

func TestOpenWithBasicAuth(t *testing.T) {
	t.Parallel()

	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jsonrpc" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok {
			t.Fatal("expected basic auth header")
		}
		if username != "kodi" || password != "secret" {
			t.Fatalf("unexpected basic auth credentials: %q / %q", username, password)
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		methods = append(methods, req.Method)
		switch req.Method {
		case "XBMC.GetInfoBooleans":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]bool{"System.ScreenSaverActive": true}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		}
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", "rtsp://stream.example:8554/screenshare", "kodi", "secret", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(methods) != 3 || methods[0] != "XBMC.GetInfoBooleans" || methods[1] != "Addons.ExecuteAddon" || methods[2] != "Player.Open" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
}

func TestOpenSkipsStandbyWhenDisplayWasAlreadyOn(t *testing.T) {
	t.Parallel()

	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		methods = append(methods, req.Method)
		switch req.Method {
		case "XBMC.GetInfoBooleans":
			// Screensaver NOT active → display was already on
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]bool{"System.ScreenSaverActive": false}})
		case "Player.GetActivePlayers":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"playerid": 1, "type": "video"}}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "rtsp://stream.example:8554/screenshare", "", "", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	// wokeDisplay should be false because display was already on
	if client.consumeWokeDisplay() {
		t.Fatal("expected wokeDisplay to be false when display was already on")
	}

	// Stop should NOT send standby
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	// Expect only GetActivePlayers + Player.Stop (no Addons.ExecuteAddon for standby)
	stopMethods := methods[3:] // skip the 3 Open methods (GetInfoBooleans, ExecuteAddon, Player.Open)
	if len(stopMethods) != 2 || stopMethods[0] != "Player.GetActivePlayers" || stopMethods[1] != "Player.Stop" {
		t.Fatalf("unexpected Stop method sequence (expected no standby): %#v", stopMethods)
	}
}

func TestEndpointCredentialsAreParsed(t *testing.T) {
	t.Parallel()

	endpoint, username, password := parseEndpointCredentials("http://kodi:secret@example.com:8080/jsonrpc")
	if endpoint != "http://example.com:8080/jsonrpc" {
		t.Fatalf("unexpected endpoint: %q", endpoint)
	}
	if username != "kodi" || password != "secret" {
		t.Fatalf("unexpected credentials: %q / %q", username, password)
	}

	endpoint, username, password = parseEndpointCredentials("http://example.com:8080/jsonrpc")
	if endpoint != "http://example.com:8080/jsonrpc" || username != "" || password != "" {
		t.Fatalf("expected endpoint without credentials to remain unchanged, got %q %q %q", endpoint, username, password)
	}

	endpoint, username, password = parseEndpointCredentials("::not-a-url::")
	if endpoint != "::not-a-url::" || username != "" || password != "" {
		t.Fatalf("expected invalid URL to remain unchanged, got %q %q %q", endpoint, username, password)
	}

	if !strings.Contains(endpoint, "not-a-url") {
		t.Fatalf("expected invalid endpoint to be preserved")
	}
}
