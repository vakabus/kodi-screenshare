package mediamtx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKickActivePublisher(t *testing.T) {
	t.Parallel()

	var kickedID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/webrtcsessions/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"id": "session-1", "state": "publish", "path": "screenshare"},
				},
			})
		case "/v3/webrtcsessions/kick/session-1":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			kickedID = "session-1"
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "screenshare", server.Client())
	kicked, err := client.KickActivePublisher(context.Background())
	if err != nil {
		t.Fatalf("KickActivePublisher() error = %v", err)
	}
	if !kicked {
		t.Fatal("expected a session to be kicked")
	}
	if kickedID != "session-1" {
		t.Fatalf("unexpected kicked ID: %q", kickedID)
	}
}

func TestGenerateConfig(t *testing.T) {
	t.Parallel()

	config := GenerateConfig("http://127.0.0.1:80/")
	if want := "curl -sk -X POST http://127.0.0.1:80/api/hooks/ready"; !strings.Contains(config, want) {
		t.Fatalf("config missing ready hook: %q", want)
	}
	if want := "curl -sk -X POST http://127.0.0.1:80/api/hooks/not-ready"; !strings.Contains(config, want) {
		t.Fatalf("config missing not-ready hook: %q", want)
	}
	if want := "apiAddress: 127.0.0.1:9997"; !strings.Contains(config, want) {
		t.Fatalf("config missing API address: %q", want)
	}
	if want := "hlsVariant: mpegts"; !strings.Contains(config, want) {
		t.Fatalf("config missing HLS compatibility setting: %q", want)
	}
	if want := "hlsSegmentCount: 3"; !strings.Contains(config, want) {
		t.Fatalf("config missing HLS segment count tuning: %q", want)
	}
	if want := "hlsSegmentDuration: 1s"; !strings.Contains(config, want) {
		t.Fatalf("config missing HLS segment duration tuning: %q", want)
	}
}

func TestValidateBinaryPath(t *testing.T) {
	t.Parallel()

	t.Run("existing bundled binary", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		binaryPath := filepath.Join(tempDir, "mediamtx")
		if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write temp binary: %v", err)
		}

		if err := validateBinaryPath(binaryPath); err != nil {
			t.Fatalf("validateBinaryPath() error = %v", err)
		}
	})

	t.Run("missing bundled binary", func(t *testing.T) {
		t.Parallel()

		err := validateBinaryPath(filepath.Join(t.TempDir(), "mediamtx"))
		if err == nil {
			t.Fatal("expected missing bundled binary to error")
		}
		if !strings.Contains(err.Error(), "make fetch-mediamtx") {
			t.Fatalf("expected fetch hint in error, got %q", err)
		}
	})
}
