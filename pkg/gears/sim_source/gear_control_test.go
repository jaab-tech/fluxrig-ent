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
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaab-tech/fluxrig/pkg/ctrl"
	"github.com/jaab-tech/fluxrig/pkg/fluxmsg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const eventually = 10 * time.Second

// newControlGear builds a gear the way Init leaves it, without a Rack around it. The
// gear stops with the test.
func newControlGear(t *testing.T, cfg Config) (*Gear, *atomic.Int64) {
	t.Helper()
	spec, meta, content := loadTestSpec(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	emitted := &atomic.Int64{}
	g := &Gear{
		logger:     logger,
		name:       "traffic",
		cfg:        cfg,
		spec:       spec,
		meta:       meta,
		rateEngine: NewRateEngine(cfg.Rate, logger),
		stopCh:     make(chan struct{}),
		emit:       func(*fluxmsg.FluxMsg) { emitted.Add(1) },
	}
	g.generator = NewGeneratorFromContent(content, spec, meta, cfg, &mockIDGenerator{})
	g.generator.SetLogger(logger)
	t.Cleanup(func() {
		_ = g.Stop()
		g.rateEngine.Stop()
	})
	return g, emitted
}

func controlTestConfig() Config {
	return Config{
		Seed: 42,
		Mix:  []MixEntry{{Use: "0100", Weight: 100}},
		Rate: RateConfig{Shape: "constant", TPS: 200, Duration: "60s"},
	}
}

func isRunning(g *Gear) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

// requireNoLoops waits until every generation loop the gear started has ended.
func requireNoLoops(t *testing.T, g *Gear, msg string) {
	t.Helper()
	require.Eventually(t, func() bool { return g.liveLoops.Load() == 0 }, eventually, 10*time.Millisecond, msg)
}

func TestGear_DrainAndStopAreIdempotent(t *testing.T) {
	cfg := controlTestConfig()

	t.Run("before Start", func(t *testing.T) {
		g, _ := newControlGear(t, cfg)
		g.stopCh = nil
		require.NotPanics(t, func() {
			require.NoError(t, g.Drain(context.Background()))
			require.NoError(t, g.Stop())
		})
	})

	t.Run("twice", func(t *testing.T) {
		g, _ := newControlGear(t, cfg)
		require.NotPanics(t, func() {
			require.NoError(t, g.Drain(context.Background()))
			require.NoError(t, g.Drain(context.Background()))
			require.NoError(t, g.Stop())
			require.NoError(t, g.Stop())
		})
	})
}

func TestRateEngine_StopIsIdempotent(t *testing.T) {
	engine := NewRateEngine(RateConfig{Shape: "ramp", From: 10, To: 100, Over: "1s"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	engine.Ticker()
	require.NotPanics(t, func() {
		engine.Stop()
		engine.Stop()
	})
}

func TestRateEngine_ARampReplacesTheOneBeforeIt(t *testing.T) {
	engine := NewRateEngine(RateConfig{Shape: "ramp", From: 10, To: 100, Over: "1s"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(engine.Stop)

	engine.Ticker()
	engine.mu.Lock()
	first := engine.rampDone
	engine.mu.Unlock()
	require.NotNil(t, first)

	engine.Ticker()
	select {
	case <-first:
	default:
		t.Fatal("a second Ticker left the first ramp goroutine running")
	}
}

func TestRateEngine_UpdateRate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("an empty shape keeps the current one", func(t *testing.T) {
		engine := NewRateEngine(RateConfig{Shape: "constant", TPS: 100}, logger)
		t.Cleanup(engine.Stop)
		engine.Ticker()

		engine.UpdateRate(200, "", "", "", "")

		engine.mu.Lock()
		defer engine.mu.Unlock()
		assert.Equal(t, "constant", engine.cfg.Shape)
		assert.Equal(t, 5*time.Millisecond, engine.calculateInterval())
	})

	t.Run("a ramp starts from a constant rate", func(t *testing.T) {
		engine := NewRateEngine(RateConfig{Shape: "constant", TPS: 100}, logger)
		t.Cleanup(engine.Stop)
		engine.Ticker()

		engine.UpdateRate(0, "ramp", "10", "100", "1s")

		require.Eventually(t, func() bool {
			engine.mu.Lock()
			defer engine.mu.Unlock()
			return engine.currentTPS > 10.0
		}, eventually, 10*time.Millisecond, "the ramp requested at runtime never raised the rate")
	})

	t.Run("a constant rate ends a ramp", func(t *testing.T) {
		engine := NewRateEngine(RateConfig{Shape: "ramp", From: 10, To: 100, Over: "1s"}, logger)
		t.Cleanup(engine.Stop)
		engine.Ticker()

		engine.UpdateRate(50, "constant", "", "", "")

		engine.mu.Lock()
		defer engine.mu.Unlock()
		assert.Nil(t, engine.rampDone, "the ramp goroutine outlived the change to a constant rate")
		assert.Equal(t, 20*time.Millisecond, engine.calculateInterval())
	})
}

func TestGear_RunDurationEndsGeneration(t *testing.T) {
	cfg := controlTestConfig()
	cfg.Rate.Duration = "300ms"
	g, emitted := newControlGear(t, cfg)

	g.startGeneration(context.Background())
	require.Eventually(t, func() bool { return emitted.Load() > 0 }, eventually, 10*time.Millisecond, "nothing was generated")
	require.Eventually(t, func() bool { return !isRunning(g) }, eventually, 10*time.Millisecond, "the run did not end at its duration")
	requireNoLoops(t, g, "the generation loop went on after the run duration")

	// A sim.start begins a new run.
	before := emitted.Load()
	g.handleControlCommand(ctrl.Command{Cmd: ctrl.CmdSimStart})
	require.Eventually(t, func() bool { return emitted.Load() > before }, eventually, 10*time.Millisecond, "sim.start after a finished run generated nothing")
}

func TestGear_RunDurationMustBeValid(t *testing.T) {
	g := &Gear{cfg: controlTestConfig()}

	g.cfg.Rate.Duration = "soon"
	_, err := g.runDuration()
	require.Error(t, err)

	g.cfg.Rate.Duration = "-5s"
	_, err = g.runDuration()
	require.Error(t, err)

	g.cfg.Rate.Duration = ""
	d, err := g.runDuration()
	require.NoError(t, err)
	assert.Zero(t, d, "an empty duration means no limit")
}

// Run under -race: a loop that reads g.stopCh again on every turn races with the
// sim.start that replaces it, and can end up holding the new channel and never stop.
func TestGear_StopAndStartCycles(t *testing.T) {
	g, _ := newControlGear(t, controlTestConfig())

	g.startGeneration(context.Background())
	for i := 0; i < 20; i++ {
		g.handleControlCommand(ctrl.Command{Cmd: ctrl.CmdSimStop})
		g.handleControlCommand(ctrl.Command{Cmd: ctrl.CmdSimStart})
	}
	g.handleControlCommand(ctrl.Command{Cmd: ctrl.CmdSimStop})

	requireNoLoops(t, g, "a generation loop outlived sim.stop")
}

// TestGear_StopAndStartCycles above calls handleControlCommand directly,
// which proves the generation loop's own lifecycle but never exercises
// controlLoop itself: the goroutine Start spawns once, for the gear's whole
// life, to dispatch commands arriving on the real control-plane channel.
// This is a regression test for a real bug found in production-shaped Robot
// suite behaviour, not in this direct-call style of test: controlLoop
// selected on g.stopCh to know when to exit, the same channel sim.stop
// closes to end one generation run. A stop command was handled correctly,
// but the very next loop iteration then saw that same channel already
// closed and returned, ending command dispatch entirely. A following
// sim.start was still queued and acknowledged over the control plane (the
// Mixer's confirmed-delivery only requires that a gear queued it), so
// nothing failed loudly: the gear simply never generated again, silently,
// after its first stop.
func TestGear_ControlLoopKeepsDispatchingAfterASimStop(t *testing.T) {
	g, emitted := newControlGear(t, controlTestConfig())

	ch := make(chan ctrl.Command)
	g.ctrlCh = ch
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.controlLoop(ctx)

	send := func(cmd ctrl.Command) {
		t.Helper()
		select {
		case ch <- cmd:
		case <-time.After(eventually):
			t.Fatalf("controlLoop did not accept %v: it likely exited after an earlier command", cmd.Cmd)
		}
	}

	send(ctrl.Command{Cmd: ctrl.CmdSimStart})
	require.Eventually(t, func() bool { return emitted.Load() > 0 }, eventually, 10*time.Millisecond,
		"no messages emitted after the first sim.start")

	send(ctrl.Command{Cmd: ctrl.CmdSimStop})
	afterStop := emitted.Load()

	// The command that matters: controlLoop must still be alive to receive
	// this, not just to have accepted the stop above.
	send(ctrl.Command{Cmd: ctrl.CmdSimStart})
	require.Eventually(t, func() bool { return emitted.Load() > afterStop }, eventually, 10*time.Millisecond,
		"no messages emitted after sim.start following a sim.stop: controlLoop likely exited")
}

func TestGear_RateCommand(t *testing.T) {
	g, _ := newControlGear(t, controlTestConfig())
	g.rateEngine.Ticker()

	g.handleControlCommand(ctrl.Command{Cmd: ctrl.CmdSimRate, Args: map[string]string{"tps": "500.000000"}})

	g.rateEngine.mu.Lock()
	assert.Equal(t, 2*time.Millisecond, g.rateEngine.calculateInterval(), "sim.rate did not change the rate")
	assert.Equal(t, "constant", g.rateEngine.cfg.Shape, "a sim.rate without a shape changed the shape")
	g.rateEngine.mu.Unlock()

	// A number that is not one is ignored, and the rate stays.
	g.handleControlCommand(ctrl.Command{Cmd: ctrl.CmdSimRate, Args: map[string]string{"tps": "fast"}})
	g.rateEngine.mu.Lock()
	assert.Equal(t, 2*time.Millisecond, g.rateEngine.calculateInterval())
	g.rateEngine.mu.Unlock()
}

func TestValidateShape(t *testing.T) {
	for _, shape := range []string{"", "constant", "ramp"} {
		assert.NoError(t, validateShape(shape), shape)
	}
	for _, shape := range []string{"poisson", "spike"} {
		err := validateShape(shape)
		require.Error(t, err, shape)
		assert.Contains(t, err.Error(), "roadmap", "a shape that is declared and not built must say so")
	}
	assert.Error(t, validateShape("sine"))
}

// A shape the engine does not build is refused, and the rate stays as it was: it must
// not run as a constant rate under another name.
func TestGear_RateCommandRefusesShapesThatAreRoadmap(t *testing.T) {
	g, _ := newControlGear(t, controlTestConfig())
	g.rateEngine.Ticker()

	g.handleControlCommand(ctrl.Command{Cmd: ctrl.CmdSimRate, Args: map[string]string{"shape": "poisson", "tps": "10"}})

	g.rateEngine.mu.Lock()
	defer g.rateEngine.mu.Unlock()
	assert.Equal(t, "constant", g.rateEngine.cfg.Shape)
	assert.Equal(t, 5*time.Millisecond, g.rateEngine.calculateInterval(), "the refused command changed the rate")
}

// The seed a command carries reaches the generator: the same seed gives the sequence
// a new generator with that seed gives.
func TestGear_SeedOfACommandReachesTheGenerator(t *testing.T) {
	spec, meta, content := loadTestSpec(t)
	mix := []MixEntry{{Use: "0100", Weight: 100}}
	// Field 7 is the transmission date and time, taken from the clock and not from the
	// seed, so it differs between two generations a second apart.
	fromSeed := func(values map[int]string) map[int]string {
		delete(values, 7)
		return values
	}
	fresh := func(seed int64) map[int]string {
		gen := NewGeneratorFromContent(content, spec, meta, Config{Seed: seed, Mix: mix}, &mockIDGenerator{})
		gen.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
		return fromSeed(genAllFields(t, gen))
	}
	want := fresh(222)
	require.NotEqual(t, fresh(111), want, "the two seeds must give different values for this test to mean anything")

	for _, cmd := range []ctrl.Command{
		{Cmd: ctrl.CmdSimReset, Args: map[string]string{"seed": "222"}},
		{Cmd: ctrl.CmdSimStart, Args: map[string]string{"seed": "222"}},
	} {
		t.Run(cmd.Cmd, func(t *testing.T) {
			// One message every 100 seconds: the loop never draws from the generator here.
			cfg := Config{Seed: 111, Mix: mix, Rate: RateConfig{Shape: "constant", TPS: 0.01, Duration: "60s"}}
			g, emitted := newControlGear(t, cfg)
			g.handleControlCommand(cmd)
			g.handleControlCommand(ctrl.Command{Cmd: ctrl.CmdSimStop})
			requireNoLoops(t, g, "the loop did not end")
			require.Zero(t, emitted.Load(), "the loop emitted, so the generator was used before the comparison")
			assert.Equal(t, want, fromSeed(genAllFields(t, g.generator)))
		})
	}
}
