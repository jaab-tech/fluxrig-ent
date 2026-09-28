// Copyright (c) 2026 JAAB Tech SAS, Uruguay
// SPDX-License-Identifier: BSL-1.1
//
// This file is part of fluxrig Enterprise Gears.
// Licensed under the Business Source License 1.1 (BSL-1.1).
// See https://mariadb.com/bsl11/ for the full license text.
//
// Additional Use Grant: none. Production use requires a commercial licence.
// Change Date: Four years after release.
// Change License: Apache-2.0.

package sim_source

import (
	"log/slog"
	"math"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"
)

// maxTPS bounds a configured or requested rate, TPS or ramp From/To alike.
// Above it, or at a NaN/Inf value, calculateInterval's float division either
// underflows to an interval time.Duration rounds to zero or produces a value
// outside what a Duration can represent, and time.NewTicker/.Reset panics on
// either. strconv.ParseFloat parses "nan" and "inf" without error, so this
// needs checking explicitly wherever a rate value is parsed, not inferred
// from the parse's own error.
const maxTPS = 1_000_000

// validRate reports whether v is usable as a TPS or ramp From/To value.
func validRate(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= maxTPS
}

// minTickerInterval is the floor calculateInterval clamps to. Even a TPS
// within [0, maxTPS] can still divide down to an interval below what ticks
// meaningfully; this keeps the ticker itself always constructible.
const minTickerInterval = time.Microsecond

// RateEngine handles rate shaping for the generator.
type RateEngine struct {
	cfg    RateConfig
	logger *slog.Logger

	mu           sync.Mutex
	ticker       *time.Ticker
	currentTPS   float64
	rampStart    time.Time
	rampDuration time.Duration
	poissonSrc   *rand.Rand
	stopCh       chan struct{}
	stopOnce     sync.Once
	rampDone     chan struct{} // ends the ramp goroutine in progress, nil when there is none
}

// NewRateEngine creates a new rate engine.
func NewRateEngine(cfg RateConfig, logger *slog.Logger) *RateEngine {
	re := &RateEngine{
		cfg:        cfg,
		logger:     logger,
		currentTPS: cfg.TPS,
		poissonSrc: rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0)),
		stopCh:     make(chan struct{}),
	}

	// Parse duration strings
	if cfg.Over != "" {
		if d, err := time.ParseDuration(cfg.Over); err == nil {
			re.rampDuration = d
		}
	}
	if cfg.Duration != "" {
		// Total duration handled by caller
	}

	return re
}

// Ticker returns a ticker that fires at the configured rate. Calling it again replaces
// the previous ticker and, for a ramp, restarts the ramp from now.
func (re *RateEngine) Ticker() *time.Ticker {
	re.mu.Lock()
	defer re.mu.Unlock()

	if re.ticker != nil {
		re.ticker.Stop()
	}

	if re.cfg.Shape == "ramp" && re.rampDuration > 0 {
		re.currentTPS = re.cfg.From
	}
	re.ticker = time.NewTicker(re.calculateInterval())

	if re.cfg.Shape == "ramp" && re.rampDuration > 0 {
		re.startRampLocked()
	}

	return re.ticker
}

func (re *RateEngine) calculateInterval() time.Duration {
	if !validRate(re.currentTPS) || re.currentTPS == 0 {
		return time.Hour // Effectively stopped, or a bad rate that slipped past validation somewhere
	}
	interval := time.Duration(float64(time.Second) / re.currentTPS)
	if interval < minTickerInterval {
		return minTickerInterval
	}
	return interval
}

// startRampLocked begins a ramp from now and ends the one in progress, so that at most
// one ramp goroutine adjusts the rate. The caller holds mu.
func (re *RateEngine) startRampLocked() {
	re.endRampLocked()
	re.rampStart = time.Now()
	re.rampDone = make(chan struct{})
	go re.rampLoop(re.rampDone)
}

// endRampLocked ends the ramp in progress, if any. The caller holds mu.
func (re *RateEngine) endRampLocked() {
	if re.rampDone != nil {
		close(re.rampDone)
		re.rampDone = nil
	}
}

func (re *RateEngine) rampLoop(done <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-re.stopCh:
			return
		case <-done:
			return
		case <-ticker.C:
			re.mu.Lock()
			elapsed := time.Since(re.rampStart)
			finished := elapsed >= re.rampDuration
			if finished {
				re.currentTPS = re.cfg.To
			} else {
				progress := float64(elapsed) / float64(re.rampDuration)
				re.currentTPS = re.cfg.From + (re.cfg.To-re.cfg.From)*progress
			}
			if re.ticker != nil {
				re.ticker.Reset(re.calculateInterval())
			}
			re.mu.Unlock()
			if finished {
				return
			}
		}
	}
}

// UpdateRate updates the rate configuration at runtime. An empty shape keeps the
// current one.
func (re *RateEngine) UpdateRate(tps float64, shape, from, to, over string) {
	re.mu.Lock()
	defer re.mu.Unlock()

	if shape != "" {
		re.cfg.Shape = shape
	}
	re.cfg.TPS = tps
	if from != "" {
		if f, err := strconv.ParseFloat(from, 64); err == nil && validRate(f) {
			re.cfg.From = f
		} else {
			re.logger.Warn("Ignoring sim.rate: from is not a usable rate", "from", from)
		}
	}
	if to != "" {
		if t, err := strconv.ParseFloat(to, 64); err == nil && validRate(t) {
			re.cfg.To = t
		} else {
			re.logger.Warn("Ignoring sim.rate: to is not a usable rate", "to", to)
		}
	}
	if over != "" {
		if d, err := time.ParseDuration(over); err == nil {
			re.rampDuration = d
		}
	}

	if re.cfg.Shape == "ramp" && re.rampDuration > 0 {
		re.currentTPS = re.cfg.From
		re.startRampLocked()
	} else {
		re.endRampLocked()
		re.currentTPS = tps
	}

	if re.ticker != nil {
		re.ticker.Reset(re.calculateInterval())
	}
}

// Stop stops the rate engine. It is safe to call more than once.
func (re *RateEngine) Stop() {
	re.stopOnce.Do(func() {
		close(re.stopCh)
		re.mu.Lock()
		defer re.mu.Unlock()
		re.endRampLocked()
		if re.ticker != nil {
			re.ticker.Stop()
		}
	})
}
