package mediamtx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type APIClient struct {
	baseURL    string
	pathName   string
	httpClient *http.Client
}

type WebRTCSession struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Path  string `json:"path"`
}

type webRTCSessionList struct {
	Items []WebRTCSession `json:"items"`
}

type Manager struct {
	BinaryPath  string
	HookBaseURL string
	Logger      *log.Logger
}

func NewAPIClient(baseURL, pathName string, httpClient *http.Client) *APIClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &APIClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		pathName:   pathName,
		httpClient: httpClient,
	}
}

func NewManager(binaryPath, hookBaseURL string, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	return &Manager{
		BinaryPath:  binaryPath,
		HookBaseURL: hookBaseURL,
		Logger:      logger,
	}
}

func (c *APIClient) ListWebRTCSessions(ctx context.Context) ([]WebRTCSession, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v3/webrtcsessions/list", nil)
	if err != nil {
		return nil, fmt.Errorf("build MediaMTX list request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list MediaMTX WebRTC sessions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("MediaMTX list returned %s: %s", resp.Status, string(payload))
	}

	var list webRTCSessionList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode MediaMTX list response: %w", err)
	}

	return list.Items, nil
}

func (c *APIClient) KickWebRTCSession(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v3/webrtcsessions/kick/"+id, nil)
	if err != nil {
		return fmt.Errorf("build MediaMTX kick request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("kick MediaMTX WebRTC session %s: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("MediaMTX kick returned %s: %s", resp.Status, string(payload))
	}

	return nil
}

func (c *APIClient) KickActivePublisher(ctx context.Context) (bool, error) {
	sessions, err := c.ListWebRTCSessions(ctx)
	if err != nil {
		return false, err
	}

	for _, session := range sessions {
		if session.State == "publish" && session.Path == c.pathName {
			if err := c.KickWebRTCSession(ctx, session.ID); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	return false, nil
}

func (m *Manager) Run(ctx context.Context) error {
	if err := validateBinaryPath(m.BinaryPath); err != nil {
		return err
	}

	for {
		configPath, err := m.writeConfig()
		if err != nil {
			return err
		}

		cmd := exec.CommandContext(ctx, m.BinaryPath, configPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		m.Logger.Printf("starting MediaMTX with config %s", filepath.Base(configPath))
		if err := cmd.Start(); err != nil {
			_ = os.Remove(configPath)
			return fmt.Errorf("start MediaMTX: %w", err)
		}

		waitErr := cmd.Wait()
		_ = os.Remove(configPath)

		if ctx.Err() != nil {
			return nil
		}

		m.Logger.Printf("MediaMTX exited: %v; restarting in 1s", waitErr)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

func validateBinaryPath(path string) error {
	if strings.ContainsRune(path, os.PathSeparator) {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("MediaMTX binary not found at %q; run `make fetch-mediamtx`: %w", path, err)
		}
		if info.IsDir() {
			return fmt.Errorf("MediaMTX binary path %q is a directory", path)
		}
		return nil
	}

	if _, err := exec.LookPath(path); err != nil {
		return fmt.Errorf("locate MediaMTX binary %q: %w", path, err)
	}

	return nil
}

func (m *Manager) writeConfig() (string, error) {
	file, err := os.CreateTemp("", "mediamtx-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create MediaMTX config: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(GenerateConfig(m.HookBaseURL)); err != nil {
		return "", fmt.Errorf("write MediaMTX config: %w", err)
	}

	return file.Name(), nil
}

func GenerateConfig(hookBaseURL string) string {
	baseURL := strings.TrimRight(hookBaseURL, "/")
	// HLS is not used for playback (Kodi reads RTSP), so we do not remux it —
	// continuous HLS muxing only wastes CPU on the Pi 5, whose software H.264
	// decoder needs all the headroom it can get to avoid falling behind.
	// rtspTransports: [tcp] pairs with the client-side rtsp_transport=tcp KODIPROP
	// so the Kodi reader uses interleaved TCP (no UDP reorder/loss latency).
	// writeQueueSize bounds the per-reader queue: if Kodi can't keep up, packets
	// are dropped rather than queued into ever-growing latency.
	return fmt.Sprintf(`api: yes
apiAddress: 127.0.0.1:9997

rtspTransports: [tcp]
writeQueueSize: 1024

paths:
  screenshare:
    runOnReady: >
      curl -sk -X POST %s/api/hooks/ready
    runOnReadyRestart: no
    runOnNotReady: >
      curl -sk -X POST %s/api/hooks/not-ready
`, baseURL, baseURL)
}
