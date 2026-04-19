package kodi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	openRetryCount   = 3
	openRetryDelay   = 500 * time.Millisecond
	wakeAddonID      = "service.kodi-screenshare"
	cecQueryTimeout  = 5 * time.Second
)

type Client struct {
	endpoint    string
	streamURL   string
	mu          sync.Mutex
	wokeDisplay bool
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

func NewClient(endpoint, streamURL string) *Client {
	return &Client{
		endpoint:  endpoint,
		streamURL: streamURL,
	}
}

func (c *Client) Open(ctx context.Context) error {
	tvAlreadyOn := isTVPoweredOn(ctx)
	if tvAlreadyOn {
		log.Printf("TV is already powered on (CEC), will not send standby on stop")
	}

	wokeDisplay := false
	if err := c.wakeDisplay(ctx); err != nil {
		log.Printf("Kodi CEC wake failed (continuing with playback): %v", err)
	} else if !tvAlreadyOn {
		wokeDisplay = true
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

	var lastErr error
	for attempt := 1; attempt <= openRetryCount; attempt++ {
		if err := c.call(ctx, req, nil); err == nil {
			c.setWokeDisplay(wokeDisplay)
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

	c.setWokeDisplay(false)
	return fmt.Errorf("open Kodi stream at %s: %w", c.streamURL, lastErr)
}

// isTVPoweredOn queries the TV's actual power state via HDMI-CEC using
// cec-ctl. Returns true only if the TV reports "pwr-state: on".
// Returns false if the TV is in standby, cec-ctl is unavailable, or
// the query fails for any reason.
func isTVPoweredOn(ctx context.Context) bool {
	queryCtx, cancel := context.WithTimeout(ctx, cecQueryTimeout)
	defer cancel()

	cmd := exec.CommandContext(queryCtx, "cec-ctl", "--give-device-power-status", "--to", "/dev/cec1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("CEC power query failed (assuming TV is off): %v", err)
		return false
	}

	powered := strings.Contains(strings.ToLower(string(output)), "pwr-state: on")
	log.Printf("CEC power query: TV powered on = %t", powered)
	return powered
}

func (c *Client) wakeDisplay(ctx context.Context) error {
	return c.executeCECCommand(ctx, "activate")
}

func (c *Client) standbyDisplay(ctx context.Context) error {
	return c.executeCECCommand(ctx, "standby")
}

func (c *Client) executeCECCommand(ctx context.Context, command string) error {
	return c.call(ctx, rpcRequest{
		JSONRPC: "2.0",
		Method:  "Addons.ExecuteAddon",
		Params: map[string]any{
			"addonid": wakeAddonID,
			"params": map[string]string{
				"command": command,
			},
			"wait": true,
		},
		ID: 1,
	}, nil)
}

func (c *Client) Stop(ctx context.Context) error {
	shouldStandby := c.consumeWokeDisplay()

	var stopErr error
	var players []activePlayer
	if err := c.call(ctx, rpcRequest{
		JSONRPC: "2.0",
		Method:  "Player.GetActivePlayers",
		ID:      1,
	}, &players); err != nil {
		log.Printf("Player.GetActivePlayers failed: %v", err)
		stopErr = err
	} else {
		for _, player := range players {
			if err := c.call(ctx, rpcRequest{
				JSONRPC: "2.0",
				Method:  "Player.Stop",
				Params: map[string]int{
					"playerid": player.PlayerID,
				},
				ID: 1,
			}, nil); err != nil {
				log.Printf("Player.Stop failed: %v", err)
				stopErr = err
			}
		}
	}

	if shouldStandby {
		if err := c.standbyDisplay(ctx); err != nil {
			log.Printf("CEC standby failed: %v", err)
			if stopErr == nil {
				stopErr = err
			}
		}
	}

	return stopErr
}

func (c *Client) setWokeDisplay(woke bool) {
	c.mu.Lock()
	c.wokeDisplay = woke
	c.mu.Unlock()
}

func (c *Client) consumeWokeDisplay() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	woke := c.wokeDisplay
	c.wokeDisplay = false
	return woke
}

func (c *Client) call(ctx context.Context, rpcReq rpcRequest, out any) error {
	body, err := json.Marshal(rpcReq)
	if err != nil {
		return fmt.Errorf("marshal Kodi request: %w", err)
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.endpoint)
	if err != nil {
		return fmt.Errorf("connect to Kodi at %s: %w", c.endpoint, err)
	}
	defer conn.Close()

	// Set deadline from context if present.
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	body = append(body, '\n')
	if _, err := conn.Write(body); err != nil {
		return fmt.Errorf("send Kodi request %s: %w", rpcReq.Method, err)
	}

	var envelope rpcEnvelope
	if err := json.NewDecoder(conn).Decode(&envelope); err != nil {
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
