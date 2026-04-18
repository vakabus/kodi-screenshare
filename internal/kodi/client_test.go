package kodi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpen(t *testing.T) {
	t.Parallel()

	var got rpcRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			if r.Method != http.MethodGet {
				t.Fatalf("unexpected stream method: %s", r.Method)
			}
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:1,\nsegment.ts\n"))
		case "/jsonrpc":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", server.URL+"/index.m3u8", server.URL+"/index.m3u8", "", "", server.Client())
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
	if item["file"] != server.URL+"/index.m3u8" {
		t.Fatalf("unexpected file payload: %#v", item)
	}
}

func TestOpenWaitsForPlaylistReadiness(t *testing.T) {
	t.Parallel()

	var getCount int
	var postCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			getCount++
			if getCount < 2 {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:1,\nsegment.ts\n"))
		case "/jsonrpc":
			postCount++
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", server.URL+"/index.m3u8", server.URL+"/index.m3u8", "", "", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if getCount < 2 {
		t.Fatalf("expected readiness polling before opening, got %d GETs", getCount)
	}
	if postCount != 1 {
		t.Fatalf("expected exactly one Player.Open RPC, got %d", postCount)
	}
}

func TestOpenFailsWhenPlaylistNeverBecomesReady(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.m3u8":
			http.NotFound(w, r)
		case "/jsonrpc":
			t.Fatal("did not expect Player.Open RPC when stream is not ready")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", server.URL+"/index.m3u8", server.URL+"/index.m3u8", "", "", server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := client.Open(ctx)
	if err == nil {
		t.Fatal("expected Open() to fail when playlist never becomes ready")
	}
	if !strings.Contains(err.Error(), "wait for HLS stream readiness") {
		t.Fatalf("expected readiness error, got %v", err)
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

	client := NewClient(server.URL, "ignored", "", "", "", server.Client())
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
		switch r.URL.Path {
		case "/index.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:1,\nsegment.ts\n"))
		case "/jsonrpc":
			username, password, ok := r.BasicAuth()
			if !ok {
				t.Fatal("expected basic auth header")
			}
			if username != "kodi" || password != "secret" {
				t.Fatalf("unexpected basic auth credentials: %q / %q", username, password)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", server.URL+"/index.m3u8", server.URL+"/index.m3u8", "kodi", "secret", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestOpenUsesSeparateReadinessURL(t *testing.T) {
	t.Parallel()

	var readyChecks int
	var got rpcRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ready/index.m3u8":
			readyChecks++
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:1,\nsegment.ts\n"))
		case "/jsonrpc":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "OK"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL+"/jsonrpc", "http://stream.example/screenshare/index.m3u8", server.URL+"/ready/index.m3u8", "", "", server.Client())
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if readyChecks == 0 {
		t.Fatal("expected readiness URL to be polled")
	}
	params, ok := got.Params.(map[string]any)
	if !ok {
		t.Fatalf("unexpected params type: %T", got.Params)
	}
	item, ok := params["item"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected item type: %T", params["item"])
	}
	if item["file"] != "http://stream.example/screenshare/index.m3u8" {
		t.Fatalf("unexpected file payload: %#v", item)
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
