package kodi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	streamReadyTimeout  = 12 * time.Second
	streamReadyInterval = 250 * time.Millisecond
)

type Client struct {
	endpoint     string
	streamURL    string
	readinessURL string
	username     string
	password     string
	httpClient   *http.Client
}

type activePlayer struct {
	PlayerID int    `json:"playerid"`
	Type     string `json:"type"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      int    `json:"id"`
}

type rpcEnvelope struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewClient(endpoint, streamURL, readinessURL, username, password string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if readinessURL == "" {
		readinessURL = streamURL
	}

	parsedEndpoint, parsedUser, parsedPass := parseEndpointCredentials(endpoint)
	if username == "" {
		username = parsedUser
	}
	if password == "" {
		password = parsedPass
	}

	return &Client{
		endpoint:     parsedEndpoint,
		streamURL:    streamURL,
		readinessURL: readinessURL,
		username:     username,
		password:     password,
		httpClient:   httpClient,
	}
}

func (c *Client) Open(ctx context.Context) error {
	readyCtx, cancel := context.WithTimeout(ctx, streamReadyTimeout)
	defer cancel()

	if err := c.waitUntilStreamReady(readyCtx); err != nil {
		return err
	}

	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  "Player.Open",
		Params: map[string]any{
			"item": map[string]string{
				"file": c.streamURL,
			},
		},
		ID: 1,
	}

	return c.call(ctx, req, nil)
}

func (c *Client) Stop(ctx context.Context) error {
	var players []activePlayer
	if err := c.call(ctx, rpcRequest{
		JSONRPC: "2.0",
		Method:  "Player.GetActivePlayers",
		ID:      1,
	}, &players); err != nil {
		return err
	}

	for _, player := range players {
		if err := c.call(ctx, rpcRequest{
			JSONRPC: "2.0",
			Method:  "Player.Stop",
			Params: map[string]int{
				"playerid": player.PlayerID,
			},
			ID: 1,
		}, nil); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) call(ctx context.Context, rpcReq rpcRequest, out any) error {
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return fmt.Errorf("marshal Kodi request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build Kodi request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call Kodi method %s: %w", rpcReq.Method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Kodi method %s returned %s: %s", rpcReq.Method, resp.Status, string(payload))
	}

	var envelope rpcEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode Kodi response for %s: %w", rpcReq.Method, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("Kodi method %s failed: %s", rpcReq.Method, envelope.Error.Message)
	}

	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("decode Kodi result for %s: %w", rpcReq.Method, err)
		}
	}

	return nil
}

func (c *Client) waitUntilStreamReady(ctx context.Context) error {
	ticker := time.NewTicker(streamReadyInterval)
	defer ticker.Stop()

	for {
		ready, err := c.isStreamReady(ctx)
		if err == nil && ready {
			return nil
		}

		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("wait for HLS stream readiness at %s: %w", c.readinessURL, err)
			}
			return fmt.Errorf("wait for HLS stream readiness at %s: %w", c.readinessURL, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *Client) isStreamReady(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.readinessURL, nil)
	if err != nil {
		return false, fmt.Errorf("build HLS readiness request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if err != nil {
		return false, fmt.Errorf("read HLS playlist: %w", err)
	}

	playlist := string(body)
	if !strings.Contains(playlist, "#EXTM3U") {
		return false, nil
	}

	return true, nil
}

func parseEndpointCredentials(rawEndpoint string) (endpoint, username, password string) {
	endpoint = rawEndpoint
	parsedURL, err := url.Parse(rawEndpoint)
	if err != nil || parsedURL.User == nil {
		return endpoint, "", ""
	}

	username = parsedURL.User.Username()
	password, _ = parsedURL.User.Password()
	parsedURL.User = nil

	return parsedURL.String(), username, password
}
