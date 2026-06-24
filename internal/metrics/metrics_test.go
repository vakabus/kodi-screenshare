package metrics

import (
	"context"
	"testing"
	"time"
)

func TestRingBufferTrimsToMax(t *testing.T) {
	t.Parallel()

	m := New(nil, nil)
	m.maxSamples = 3
	m.active = true
	m.gen = 1
	start := time.Now()

	// Feed increasing playback positions; lag = elapsed - position, and since the
	// loop runs in well under a second, lag decreases by ~1 per step.
	for i := range 5 {
		m.record(m.gen, start, float64(i))
	}

	_, current, samples := m.Snapshot()
	if len(samples) != 3 {
		t.Fatalf("expected ring buffer capped at 3, got %d", len(samples))
	}
	if current != samples[len(samples)-1].Lag {
		t.Fatalf("expected current to equal the newest sample lag, got %v vs %v", current, samples[len(samples)-1].Lag)
	}
	// Only the three most recent samples (positions 2,3,4) should remain, so lag
	// must be strictly decreasing across them.
	if !(samples[0].Lag > samples[1].Lag && samples[1].Lag > samples[2].Lag) {
		t.Fatalf("expected the newest 3 samples kept in order, got lags %v / %v / %v", samples[0].Lag, samples[1].Lag, samples[2].Lag)
	}
}

func TestRecordDropsStaleSample(t *testing.T) {
	t.Parallel()

	m := New(nil, nil)
	m.active = true
	m.gen = 2
	start := time.Now()

	m.record(1, start, 9) // gen 1 sample arriving in gen 2 session — must be dropped
	if _, _, samples := m.Snapshot(); len(samples) != 0 {
		t.Fatalf("expected stale sample to be dropped, got %d samples", len(samples))
	}

	m.record(2, start, 3) // current-generation sample is kept
	if _, current, samples := m.Snapshot(); len(samples) != 1 || current != samples[0].Lag {
		t.Fatalf("expected one current-gen sample, got %d samples / current %v", len(samples), current)
	}
}

func TestStartStopTogglesActive(t *testing.T) {
	t.Parallel()

	m := New(func(context.Context) (float64, bool, error) { return 0, false, nil }, nil)

	m.Start()
	if active, _, _ := m.Snapshot(); !active {
		t.Fatal("expected monitor active after Start")
	}

	m.Stop()
	if active, _, _ := m.Snapshot(); active {
		t.Fatal("expected monitor inactive after Stop")
	}
}
