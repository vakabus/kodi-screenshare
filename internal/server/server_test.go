package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vakabus/kodi-screenshare/internal/metrics"
	"github.com/vakabus/kodi-screenshare/internal/session"
)

type fakeMonitor struct {
	startCalls int
	stopCalls  int
}

func (f *fakeMonitor) Start() { f.startCalls++ }
func (f *fakeMonitor) Stop()  { f.stopCalls++ }
func (f *fakeMonitor) Snapshot() (bool, float64, []metrics.Sample) {
	return false, 0, nil
}

type fakeKodi struct {
	openCalls int
	stopCalls int
}

func (f *fakeKodi) Open(context.Context) error {
	f.openCalls++
	return nil
}

func (f *fakeKodi) Stop(context.Context) error {
	f.stopCalls++
	return nil
}

type fakeMedia struct {
	kicked bool
}

func (f *fakeMedia) KickActivePublisher(context.Context) (bool, error) {
	f.kicked = true
	return true, nil
}

func TestStatusHooksAndTakeover(t *testing.T) {
	t.Parallel()

	state := session.NewState()
	kodi := &fakeKodi{}
	media := &fakeMedia{}
	monitor := &fakeMonitor{}
	srv := New(state, kodi, media, monitor, "")
	handler := srv.Handler()

	statusReq := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, statusReq)

	if statusRec.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", statusRec.Code)
	}
	var status map[string]bool
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if status["active"] {
		t.Fatal("expected initial session to be inactive")
	}

	readyReq := httptest.NewRequest(http.MethodPost, "/api/hooks/ready", nil)
	readyRec := httptest.NewRecorder()
	handler.ServeHTTP(readyRec, readyReq)

	if readyRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected ready status code: %d", readyRec.Code)
	}
	if !state.IsActive() {
		t.Fatal("expected ready hook to mark session active")
	}
	if kodi.openCalls != 1 {
		t.Fatalf("expected 1 open call, got %d", kodi.openCalls)
	}
	if monitor.startCalls != 1 {
		t.Fatalf("expected ready hook to start the latency monitor once, got %d", monitor.startCalls)
	}

	takeoverReq := httptest.NewRequest(http.MethodPost, "/api/takeover", nil)
	takeoverRec := httptest.NewRecorder()
	handler.ServeHTTP(takeoverRec, takeoverReq)

	if takeoverRec.Code != http.StatusOK {
		t.Fatalf("unexpected takeover status code: %d", takeoverRec.Code)
	}
	if !media.kicked {
		t.Fatal("expected takeover to kick current publisher")
	}

	notReadyReq := httptest.NewRequest(http.MethodPost, "/api/hooks/not-ready", nil)
	notReadyRec := httptest.NewRecorder()
	handler.ServeHTTP(notReadyRec, notReadyReq)

	if notReadyRec.Code != http.StatusNoContent {
		t.Fatalf("unexpected not-ready status code: %d", notReadyRec.Code)
	}
	if state.IsActive() {
		t.Fatal("expected not-ready hook to mark session inactive")
	}
	if kodi.stopCalls != 1 {
		t.Fatalf("expected 1 stop call, got %d", kodi.stopCalls)
	}
	if monitor.stopCalls != 1 {
		t.Fatalf("expected not-ready hook to stop the latency monitor once, got %d", monitor.stopCalls)
	}
}
