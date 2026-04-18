package kodi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	openRetryCount = 3
	openRetryDelay = 500 * time.Millisecond
)

type Client struct {
	endpoint   string
	streamURL  string
	username   string
	password   string
	httpClient *http.Client
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

func NewClient(endpoint, streamURL, username, password string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	parsedEndpoint, parsedUser, parsedPass := parseEndpointCredentials(endpoint)
	if username == "" {
		username = parsedUser
	}
	if password == "" {
		password = parsedPass
	}

	return &Client{
		endpoint:   parsedEndpoint,
		streamURL:  streamURL,
		username:   username,
		password:   password,
		httpClient: httpClient,
	}
}

func (c *Client) Open(ctx context.Context) error {
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

	var lastErr error
	for attempt := 1; attempt <= openRetryCount; attempt++ {
		if err := c.call(ctx, req, nil); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if attempt == openRetryCount {
			break
		}

		timer := time.NewTimer(openRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("open Kodi stream at %s: %w", c.streamURL, ctx.Err())
		case <-timer.C:
		}
	}

	return fmt.Errorf("open Kodi stream at %s: %w", c.streamURL, lastErr)
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
