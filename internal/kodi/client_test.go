package kodi

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
)

// tcpServer is a helper that accepts TCP connections, reads a JSON-RPC
// request per connection, and writes back the response from handler.
type tcpServer struct {
	listener net.Listener
	wg       sync.WaitGroup
	mu       sync.Mutex
	methods  []string
	handler  func(req rpcRequest) any
}

func newTCPServer(t *testing.T, handler func(req rpcRequest) any) *tcpServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &tcpServer{listener: ln, handler: handler}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer conn.Close()
				scanner := bufio.NewScanner(conn)
				if !scanner.Scan() {
					return
				}
				var req rpcRequest
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					t.Errorf("decode request: %v", err)
					return
				}
				s.mu.Lock()
				s.methods = append(s.methods, req.Method)
				s.mu.Unlock()
				resp := s.handler(req)
				_ = json.NewEncoder(conn).Encode(resp)
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *tcpServer) addr() string {
	return s.listener.Addr().String()
}

func (s *tcpServer) getMethods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.methods))
	copy(out, s.methods)
	return out
}

func TestOpen(t *testing.T) {
	t.Parallel()

	var lastReq rpcRequest
	srv := newTCPServer(t, func(req rpcRequest) any {
		lastReq = req
		return map[string]any{"result": "OK"}
	})

	client := NewClient(srv.addr(), "rtsp://stream.example:8554/screenshare")
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	methods := srv.getMethods()
	if len(methods) != 2 || methods[0] != "Addons.ExecuteAddon" || methods[1] != "Player.Open" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
	params, ok := lastReq.Params.(map[string]any)
	if !ok {
		t.Fatalf("unexpected params type: %T", lastReq.Params)
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

	var openAttempts int
	var mu sync.Mutex
	srv := newTCPServer(t, func(req rpcRequest) any {
		if req.Method == "Player.Open" {
			mu.Lock()
			openAttempts++
			attempt := openAttempts
			mu.Unlock()
			if attempt == 1 {
				return map[string]any{"error": map[string]any{"code": -1, "message": "temporary failure"}}
			}
		}
		return map[string]any{"result": "OK"}
	})

	client := NewClient(srv.addr(), "rtsp://stream.example:8554/screenshare")
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	mu.Lock()
	got := openAttempts
	mu.Unlock()
	if got != 2 {
		t.Fatalf("expected retry after transient RPC failure, got %d Player.Open attempts", got)
	}
	methods := srv.getMethods()
	if len(methods) != 3 || methods[0] != "Addons.ExecuteAddon" || methods[1] != "Player.Open" || methods[2] != "Player.Open" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
}

func TestOpenFailsWhenRPCNeverSucceeds(t *testing.T) {
	t.Parallel()

	var openAttempts int
	var mu sync.Mutex
	srv := newTCPServer(t, func(req rpcRequest) any {
		if req.Method == "Player.Open" {
			mu.Lock()
			openAttempts++
			mu.Unlock()
			return map[string]any{"error": map[string]any{"code": -1, "message": "still failing"}}
		}
		return map[string]any{"result": "OK"}
	})

	client := NewClient(srv.addr(), "rtsp://stream.example:8554/screenshare")
	err := client.Open(context.Background())
	if err == nil {
		t.Fatal("expected Open() to fail when RPC never succeeds")
	}
	if !strings.Contains(err.Error(), "open Kodi stream at rtsp://stream.example:8554/screenshare") {
		t.Fatalf("expected RTSP open error, got %v", err)
	}
	mu.Lock()
	got := openAttempts
	mu.Unlock()
	if got != openRetryCount {
		t.Fatalf("expected %d Player.Open attempts, got %d", openRetryCount, got)
	}
	if client.consumeWokeDisplay() {
		t.Fatal("expected failed Open() to clear wake-tracking state")
	}
}

func TestOpenContinuesWhenWakeFails(t *testing.T) {
	t.Parallel()

	srv := newTCPServer(t, func(req rpcRequest) any {
		if req.Method == "Addons.ExecuteAddon" {
			return map[string]any{"error": map[string]any{"code": -1, "message": "addon missing"}}
		}
		return map[string]any{"result": "OK"}
	})

	client := NewClient(srv.addr(), "rtsp://stream.example:8554/screenshare")
	if err := client.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	methods := srv.getMethods()
	if len(methods) != 2 || methods[0] != "Addons.ExecuteAddon" || methods[1] != "Player.Open" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
}

func TestStop(t *testing.T) {
	t.Parallel()

	srv := newTCPServer(t, func(req rpcRequest) any {
		switch req.Method {
		case "Player.GetActivePlayers":
			return map[string]any{
				"result": []map[string]any{{"playerid": 1, "type": "video"}},
			}
		case "Player.Stop":
			return map[string]any{"result": "OK"}
		default:
			t.Errorf("unexpected method: %s", req.Method)
			return map[string]any{"result": "OK"}
		}
	})

	client := NewClient(srv.addr(), "ignored")
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	methods := srv.getMethods()
	if len(methods) != 2 {
		t.Fatalf("expected 2 RPC calls, got %d", len(methods))
	}
	if methods[0] != "Player.GetActivePlayers" || methods[1] != "Player.Stop" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
}

func TestStopSendsStandbyWhenWakeWasTracked(t *testing.T) {
	t.Parallel()

	var commands []string
	var mu sync.Mutex
	srv := newTCPServer(t, func(req rpcRequest) any {
		switch req.Method {
		case "Player.GetActivePlayers":
			return map[string]any{
				"result": []map[string]any{{"playerid": 1, "type": "video"}},
			}
		case "Player.Stop":
			return map[string]any{"result": "OK"}
		case "Addons.ExecuteAddon":
			params, ok := req.Params.(map[string]any)
			if ok {
				addonParams, ok := params["params"].(map[string]any)
				if ok {
					mu.Lock()
					commands = append(commands, addonParams["command"].(string))
					mu.Unlock()
				}
			}
			return map[string]any{"result": "OK"}
		default:
			return map[string]any{"result": "OK"}
		}
	})

	client := NewClient(srv.addr(), "ignored")
	client.setWokeDisplay(true)
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	methods := srv.getMethods()
	if len(methods) != 3 {
		t.Fatalf("expected 3 RPC calls, got %d", len(methods))
	}
	if methods[0] != "Player.GetActivePlayers" || methods[1] != "Player.Stop" || methods[2] != "Addons.ExecuteAddon" {
		t.Fatalf("unexpected method sequence: %#v", methods)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(commands) != 1 || commands[0] != "standby" {
		t.Fatalf("unexpected addon commands: %#v", commands)
	}
}

func TestStopSendsStandbyEvenWhenPlayerStopFails(t *testing.T) {
	t.Parallel()

	srv := newTCPServer(t, func(req rpcRequest) any {
		switch req.Method {
		case "Player.GetActivePlayers":
			return map[string]any{"error": map[string]any{"code": -1, "message": "kodi busy"}}
		default:
			return map[string]any{"result": "OK"}
		}
	})

	client := NewClient(srv.addr(), "ignored")
	client.setWokeDisplay(true)
	err := client.Stop(context.Background())
	if err == nil {
		t.Fatal("expected Stop() to return error when GetActivePlayers fails")
	}
	// Standby must still be attempted even though player stop failed
	methods := srv.getMethods()
	if len(methods) != 2 || methods[0] != "Player.GetActivePlayers" || methods[1] != "Addons.ExecuteAddon" {
		t.Fatalf("expected standby to be sent despite player error, got method sequence: %#v", methods)
	}
}
