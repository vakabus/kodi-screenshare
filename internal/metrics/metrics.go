// Package metrics collects a lightweight time series of Kodi playback latency
// so it can be visualized on the web UI and logged for later analysis. It does
// not act on the data — it only measures and records.
package metrics

import (
	"context"
	"io"
	"log"
	"sync"
	"time"
)

const (
	defaultInterval   = 2 * time.Second
	defaultMaxSamples = 300 // ~10 minutes at the default 2s interval
	sampleTimeout     = 1500 * time.Millisecond
)

// Sample is one latency measurement.
type Sample struct {
	T   float64 `json:"t"`   // seconds since the current monitoring session started
	Lag float64 `json:"lag"` // Kodi playback buffer lag (proxy for end-to-end latency), seconds
}

// Sampler returns the active player's current playback position in seconds. ok is
// false when there is nothing to measure yet (e.g. no active player). The Monitor
// turns this into a lag estimate by comparing it against wall-clock elapsed time.
type Sampler func(ctx context.Context) (positionSeconds float64, ok bool, err error)

// Monitor periodically samples latency while a share session is active, keeping
// a bounded in-memory ring buffer and logging each sample.
type Monitor struct {
	sampler    Sampler
	interval   time.Duration
	maxSamples int
	logger     *log.Logger

	mu      sync.Mutex
	samples []Sample
	current float64
	active  bool
	start   time.Time
	gen     uint64 // session generation; bumped each Start so stale samples are dropped
	cancel  context.CancelFunc
}

// New creates a Monitor. A nil logger discards sample logs.
func New(sampler Sampler, logger *log.Logger) *Monitor {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Monitor{
		sampler:    sampler,
		interval:   defaultInterval,
		maxSamples: defaultMaxSamples,
		logger:     logger,
	}
}

// Start begins polling. It clears any previous session's samples and is a no-op
// if already running.
func (m *Monitor) Start() {
	m.mu.Lock()
	if m.active {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.active = true
	m.gen++
	m.samples = nil
	m.current = 0
	m.start = time.Now()
	gen := m.gen
	start := m.start
	m.mu.Unlock()

	go m.run(ctx, gen, start)
}

// Stop halts polling. The collected samples remain readable via Snapshot until
// the next Start.
func (m *Monitor) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.active = false
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// Snapshot returns whether monitoring is active, the latest lag, and a copy of
// the buffered samples.
func (m *Monitor) Snapshot() (active bool, current float64, samples []Sample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Sample, len(m.samples))
	copy(out, m.samples)
	return m.active, m.current, out
}

func (m *Monitor) run(ctx context.Context, gen uint64, start time.Time) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sampleCtx, cancel := context.WithTimeout(ctx, sampleTimeout)
			lag, ok, err := m.sampler(sampleCtx)
			cancel()
			if err != nil {
				m.logger.Printf("latency sample failed: %v", err)
				continue
			}
			if !ok {
				continue
			}
			m.record(gen, start, lag)
		}
	}
}

func (m *Monitor) record(gen uint64, start time.Time, position float64) {
	m.mu.Lock()
	// Drop the sample if the monitoring session changed while sampling (a
	// Stop/Start happened), so it doesn't land in the next session's series.
	if !m.active || m.gen != gen {
		m.mu.Unlock()
		return
	}
	// Estimate pipeline latency: wall-clock time since playback started minus how
	// far the player has actually advanced. A live stream's playback position
	// tracks the wall clock at the live edge, so this gap is the end-to-end delay,
	// and it grows whenever the software decoder falls behind (the drift).
	elapsed := time.Since(start).Seconds()
	lag := elapsed - position
	m.samples = append(m.samples, Sample{T: elapsed, Lag: lag})
	if len(m.samples) > m.maxSamples {
		m.samples = append(m.samples[:0], m.samples[len(m.samples)-m.maxSamples:]...)
	}
	m.current = lag
	m.mu.Unlock()

	m.logger.Printf("latency: kodi playback lag ~= %.1fs (position=%.1fs, t=%.0fs)", lag, position, elapsed)
}
