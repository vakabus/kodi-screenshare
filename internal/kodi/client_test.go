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
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", "rtsp://stream.example:8554/screenshare", "", "", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if got.Method != "Player.Open" {
		t.Fatalf("expected Player.Open, got %q", got.Method)
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

	var postCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jsonrpc" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		postCount++
		if postCount == 1 {
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
	if postCount != 2 {
		t.Fatalf("expected retry after transient RPC failure, got %d attempts", postCount)
	}
}

func TestOpenFailsWhenRPCNeverSucceeds(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jsonrpc" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		http.Error(w, "still failing", http.StatusBadGateway)
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

func TestOpenWithBasicAuth(t *testing.T) {
	t.Parallel()

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
		_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", "rtsp://stream.example:8554/screenshare", "kodi", "secret", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
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
