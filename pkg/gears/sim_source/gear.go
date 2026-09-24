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
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaab-tech/fluxrig-ent/pkg/gears/simmacro"
	"github.com/jaab-tech/fluxrig/pkg/bus"
	"github.com/jaab-tech/fluxrig/pkg/ctrl"
	"github.com/jaab-tech/fluxrig/pkg/fluxmsg"
	"github.com/jaab-tech/fluxrig/pkg/gears/native/iso8583/codec/sdl"
	"github.com/jaab-tech/fluxrig/pkg/manager"
	"github.com/jaab-tech/fluxrig/pkg/sdk"
)

// Gear implements the NativeGear interface for the sim_source gear.
type Gear struct {
	logger *slog.Logger
	name   string
	emit   func(*fluxmsg.FluxMsg)

	cfg        Config
	spec       *sdl.Spec
	meta       *sdl.FieldMeta
	generator  *Generator
	rateEngine *RateEngine

	mu         sync.Mutex
	liveLoops  atomic.Int64 // the generation loops that have not ended
	running    bool
	stopCh     chan struct{}
	ctrlCh     <-chan ctrl.Command
	ctrlCancel context.CancelFunc

	idgen sdk.IDGenerator
	bus   bus.Bus
	mgr   manager.Manager
	cp    ctrl.ControlPlane
}

// Init loads configuration and prepares the gear.
func (g *Gear) Init(ctx sdk.GearContext) error {
	g.name = ctx.GearName()
	g.logger = ctx.Logger().With("type", "sim_source")
	g.idgen = ctx.IDGen()
	g.bus = ctx.Bus()
	g.mgr = ctx.Manager()
	if cp, ok := ctx.ControlPlane().(ctrl.ControlPlane); ok {
		g.cp = cp
	}

	config := ctx.Config()

	// Parse configuration
	g.cfg = DefaultConfig()
	if spec, ok := config["spec"].(string); ok {
		g.cfg.Spec = spec
	}
	if seed, ok := config["seed"].(float64); ok {
		g.cfg.Seed = int64(seed)
	} else if seed, ok := config["seed"].(int64); ok {
		g.cfg.Seed = seed
	} else if seed, ok := config["seed"].(int); ok {
		g.cfg.Seed = int64(seed)
	}
	if trigger, ok := config["trigger"].(string); ok {
		g.cfg.Trigger = trigger
	}
	if schedule, ok := config["schedule"].(string); ok {
		g.cfg.Schedule = schedule
	}
	if tz, ok := config["timezone"].(string); ok {
		g.cfg.Timezone = tz
	}

	// Parse rate config. Numeric fields accept any YAML/JSON number
	// shape (int from YAML maps, float64 from JSON): a bare
	// `.(float64)` assertion silently drops integer values and the
	// gear falls back to the default rate.
	numVal := func(m map[string]any, key string) (float64, bool) {
		v, ok := m[key]
		if !ok {
			return 0, false
		}
		switch n := v.(type) {
		case float64:
			return n, true
		case float32:
			return float64(n), true
		case int:
			return float64(n), true
		case int32:
			return float64(n), true
		case int64:
			return float64(n), true
		case uint64:
			return float64(n), true
		}
		return 0, false
	}
	if months, ok := numVal(config, "expiry_months"); ok {
		g.cfg.ExpiryMonths = int(months)
	}
	if percent, ok := numVal(config, "expired_percent"); ok {
		g.cfg.ExpiredPercent = int(percent)
	}
	if rateMap, ok := config["rate"].(map[string]any); ok {
		if shape, ok := rateMap["shape"].(string); ok {
			g.cfg.Rate.Shape = shape
		}
		if tps, ok := numVal(rateMap, "tps"); ok {
			g.cfg.Rate.TPS = tps
		}
		if from, ok := numVal(rateMap, "from"); ok {
			g.cfg.Rate.From = from
		}
		if to, ok := numVal(rateMap, "to"); ok {
			g.cfg.Rate.To = to
		}
		if over, ok := rateMap["over"].(string); ok {
			g.cfg.Rate.Over = over
		}
		if duration, ok := rateMap["duration"].(string); ok {
			g.cfg.Rate.Duration = duration
		}
	}

	// Parse mix
	if mixList, ok := config["mix"].([]any); ok {
		g.cfg.Mix = nil
		for _, m := range mixList {
			if mm, ok := m.(map[string]any); ok {
				entry := MixEntry{}
				if use, ok := mm["use"].(string); ok {
					entry.Use = use
				}
				if weight, ok := mm["weight"].(float64); ok {
					entry.Weight = int(weight)
				} else if weight, ok := mm["weight"].(int); ok {
					entry.Weight = weight
				}
				if entry.Use != "" && entry.Weight > 0 {
					g.cfg.Mix = append(g.cfg.Mix, entry)
				}
			}
		}
	}

	// Parse defaults
	if defaultsMap, ok := config["defaults"].(map[string]any); ok {
		g.cfg.Defaults = make(map[string]string)
		for k, v := range defaultsMap {
			g.cfg.Defaults[k] = fmt.Sprintf("%v", v)
		}
	}

	// Parse templates
	if tmplMap, ok := config["templates"].(map[string]any); ok {
		g.cfg.Templates = make(map[string]map[string]string)
		for mti, fields := range tmplMap {
			if fieldsMap, ok := fields.(map[string]any); ok {
				g.cfg.Templates[mti] = make(map[string]string)
				for fk, fv := range fieldsMap {
					g.cfg.Templates[mti][fk] = fmt.Sprintf("%v", fv)
				}
			}
		}
	}

	// Parse set. YAML maps with numeric keys (ISO field numbers)
	// decode as map[any]any, not map[string]any: normalize both.
	stringMap := func(v any) map[string]string {
		out := make(map[string]string)
		switch m := v.(type) {
		case map[string]any:
			for k, val := range m {
				out[k] = fmt.Sprintf("%v", val)
			}
		case map[any]any:
			for k, val := range m {
				out[fmt.Sprintf("%v", k)] = fmt.Sprintf("%v", val)
			}
		}
		return out
	}
	if setMap := stringMap(config["set"]); len(setMap) > 0 {
		g.cfg.Set = setMap
	}

	// Validate required fields
	if g.cfg.Spec == "" {
		return fmt.Errorf("missing 'spec' in configuration")
	}
	if g.cfg.Seed == 0 {
		return fmt.Errorf("missing 'seed' in configuration (must be non-zero for reproducibility)")
	}
	if _, err := g.runDuration(); err != nil {
		return err
	}
	if err := validateShape(g.cfg.Rate.Shape); err != nil {
		return err
	}
	if _, err := simmacro.Location(g.cfg.Timezone); err != nil {
		return err
	}
	if g.cfg.ExpiryMonths < 0 {
		return fmt.Errorf("expiry_months must not be negative, got %d", g.cfg.ExpiryMonths)
	}
	if g.cfg.ExpiredPercent < 0 || g.cfg.ExpiredPercent > 100 {
		return fmt.Errorf("expired_percent must be from 0 to 100, got %d", g.cfg.ExpiredPercent)
	}

	// Load spec
	if err := g.loadSpec(ctx); err != nil {
		return fmt.Errorf("failed to load spec %q: %w", g.cfg.Spec, err)
	}

	// Build generator
	g.generator = NewGenerator(g.spec, g.meta, g.cfg, g.idgen)
	g.generator.SetLogger(g.logger)
	if !g.generator.HasValueSource() {
		return fmt.Errorf("spec %q has no x-fluxrig-simulation section and the gear configuration gives no defaults, templates or set: there is nothing to generate from", g.cfg.Spec)
	}

	// Build rate engine
	g.rateEngine = NewRateEngine(g.cfg.Rate, g.logger)

	g.logger.Info("Initialized sim_source",
		"spec", g.cfg.Spec,
		"spec_id", g.meta.SpecID,
		"spec_version", g.meta.SpecVersion,
		"seed", g.cfg.Seed,
		"trigger", g.cfg.Trigger,
		"rate_shape", g.cfg.Rate.Shape,
	)

	return nil
}

// Start begins the active lifecycle of the Gear.
func (g *Gear) Start(ctx context.Context, emit func(*fluxmsg.FluxMsg)) error {
	g.mu.Lock()
	g.emit = emit
	g.stopCh = make(chan struct{})
	g.mu.Unlock()

	g.logger.Info("Starting sim_source",
		"flux.type", "GEAR",
		"flux.name", g.name,
		"type", "sim_source",
	)

	// Subscribe to control plane if trigger is on_control
	if g.cfg.Trigger == "on_control" {
		if cp, ok := g.getControlPlane(); ok {
			ch, err := cp.Subscribe(g.name)
			if err != nil {
				// A gear that waits for sim.start and cannot hear it would sit idle for
				// good with nothing but a log line to say so.
				return fmt.Errorf("subscribe to the control plane: %w", err)
			}
			g.ctrlCh = ch
			g.logger.Info("Subscribed to control plane for sim.start/stop/rate commands")
		}
	}

	// Handle trigger
	switch g.cfg.Trigger {
	case "on_load":
		g.startGeneration(ctx)
	case "on_control":
		// Wait for sim.start command
		go g.controlLoop(ctx)
	case "on_schedule":
		// Cron/RFC3339 scheduling is roadmap, not scaffolding: fail the
		// scenario at apply time rather than run silent and trafficless.
		return fmt.Errorf("trigger %q is roadmap (see sim-source docs); use on_load or on_control", g.cfg.Trigger)
	default:
		return fmt.Errorf("unknown trigger %q", g.cfg.Trigger)
	}

	return nil
}

// Process handles an incoming message (Filter Mode).
// Source gears typically don't process messages, but we implement it for completeness.
func (g *Gear) Process(ctx context.Context, msg *fluxmsg.FluxMsg) (*fluxmsg.FluxMsg, error) {
	return msg, nil
}

// Drain signals the gear to stop accepting new input but complete pending work.
func (g *Gear) Drain(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.ctrlCancel != nil {
		g.ctrlCancel()
	}
	g.closeStopLocked()
	g.running = false
	g.logger.Info("Drained sim_source")
	return nil
}

// closeStopLocked ends the generation loop. It is safe to call more than once and
// before Start. The caller holds mu.
func (g *Gear) closeStopLocked() {
	if g.stopCh == nil {
		return
	}
	select {
	case <-g.stopCh:
	default:
		close(g.stopCh)
	}
}

// Stop acts as the cleanup hook.
func (g *Gear) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.ctrlCancel != nil {
		g.ctrlCancel()
	}
	g.closeStopLocked()
	g.running = false
	g.logger.Info("Stopped sim_source")
	return nil
}

// loadSpec loads the spec using sdl.LoadSpecContent (like codec gear)
func (g *Gear) loadSpec(ctx sdk.GearContext) error {
	content, err := g.readSpecContent(ctx)
	if err != nil {
		return err
	}

	// LoadSpecContent also returns a moov MessageSpec, for gears that pack or
	// unpack wire bytes; this gear only needs the FieldMeta it derives
	// alongside that, since it never touches the wire format itself.
	_, meta, err := sdl.LoadSpecContent(content, "")
	if err != nil {
		return fmt.Errorf("failed to load spec content: %w", err)
	}
	g.meta = meta

	// Parse semantic layer for simulation
	spec, err := sdl.ParseSemantic(content)
	if err != nil {
		return fmt.Errorf("failed to parse semantic layer: %w", err)
	}
	g.spec = spec

	return nil
}

// readSpecContent loads the spec from the store. The spec is a store reference such as
// iso8583-v87-ascii:v2.2.0: reading one from a file path is not supported, and the error
// says why the store did not give it, not only that a file cannot be read.
func (g *Gear) readSpecContent(ctx sdk.GearContext) ([]byte, error) {
	if g.mgr == nil {
		return nil, fmt.Errorf("no spec store is available to load %q", g.cfg.Spec)
	}
	content, err := g.mgr.Load(ctx.Context(), g.cfg.Spec)
	if err != nil {
		return nil, fmt.Errorf("the spec must be a store reference (name:version), a file path is not supported: %w", err)
	}
	return content, nil
}

// extractFieldMeta is kept for compatibility but loadSpec now uses sdl.LoadSpecContent
func (g *Gear) extractFieldMeta(content []byte) (*sdl.FieldMeta, error) {
	// Parse the spec to extract FieldMeta (aliases, secure IDs, etc.)
	spec, err := sdl.ParseSemantic(content)
	if err != nil {
		return nil, err
	}

	protocol := "iso8583" // Spec doesn't have Protocol field, default to iso8583

	meta := &sdl.FieldMeta{
		SpecID:      spec.ID,
		SpecVersion: spec.Version,
		Protocol:    protocol,
		Aliases:     make(map[int]string),
		SubAliases:  make(map[int]map[string]string),
		IDByAlias:   make(map[string]int),
		SecureIDs:   make(map[int]bool),
	}

	for id, f := range spec.Fields {
		if f.Alias != "" {
			meta.Aliases[id] = f.Alias
			meta.IDByAlias[f.Alias] = id
		}
		if f.LogMask != nil && *f.LogMask {
			meta.SecureIDs[id] = true
		} else if isSensitive(f.Sensitivity) {
			meta.SecureIDs[id] = true
		}
		if len(f.Subfields.Parts) > 0 {
			subs := make(map[string]string)
			for i, sf := range f.Subfields.Parts {
				if sf.Name != "" {
					key := sf.Tag
					if key == "" {
						key = fmt.Sprint(i + 1)
					}
					subs[key] = sf.Name
				}
			}
			if len(subs) > 0 {
				meta.SubAliases[id] = subs
			}
		}
	}

	return meta, nil
}

func (g *Gear) getControlPlane() (ctrl.ControlPlane, bool) {
	if g.cp == nil {
		return nil, false
	}
	return g.cp, true
}

func (g *Gear) startGeneration(ctx context.Context) {
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return
	}
	g.running = true
	// The loop keeps the channel it started with. A later sim.stop closes it and a later
	// sim.start makes a new one: reading g.stopCh again on every turn would hand the old
	// loop the new channel, and it would never end.
	stopCh := g.stopCh
	g.mu.Unlock()

	// Init validated the duration; zero means no limit.
	duration, _ := g.runDuration()

	g.liveLoops.Add(1)
	go func() {
		defer g.liveLoops.Add(-1)
		ticker := g.rateEngine.Ticker()
		defer ticker.Stop()

		var deadline <-chan time.Time
		if duration > 0 {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			deadline = timer.C
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-deadline:
				g.logger.Info("sim_source reached its run duration", "duration", duration)
				g.mu.Lock()
				if g.stopCh == stopCh {
					g.running = false
				}
				g.mu.Unlock()
				return
			case <-ticker.C:
				msg, err := g.generator.Generate()
				if err != nil {
					g.logger.Error("Failed to generate message", "error", err)
					continue
				}
				if msg != nil {
					g.emit(msg)
				}
			}
		}
	}()
}

// controlLoop dispatches control-plane commands for the gear's whole
// lifetime, from Start until the runtime cancels ctx (Stop/Drain call
// ctrlCancel for exactly this). It must not also exit on g.stopCh: that
// channel scopes one generation run, closed by sim.stop and recreated by
// the next sim.start, so selecting on it here made a stop command kill
// this loop by the same signal, one iteration later, since a closed
// channel is always ready to receive. The gear kept running and reported
// "stopped" (correct in itself), but nothing was left listening on
// g.ctrlCh afterward: a following sim.start was queued and acknowledged
// (the Mixer's confirmed-delivery only requires that), never dequeued,
// and never logged as received. A dead, but not shut down, control loop.
func (g *Gear) controlLoop(ctx context.Context) {
	if g.ctrlCh == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-g.ctrlCh:
			g.handleControlCommand(cmd)
		}
	}
}

// runDuration is the total run time from rate.duration. Zero means no limit.
func (g *Gear) runDuration() (time.Duration, error) {
	if g.cfg.Rate.Duration == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(g.cfg.Rate.Duration)
	if err != nil {
		return 0, fmt.Errorf("invalid rate.duration %q: %w", g.cfg.Rate.Duration, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid rate.duration %q: must not be negative", g.cfg.Rate.Duration)
	}
	return d, nil
}

// validateShape accepts the rate shapes the engine implements. The manifest declares
// poisson and spike too, and they fail fast at apply time, as the on_schedule trigger
// does, instead of running as a constant rate. An empty shape keeps the current one.
func validateShape(shape string) error {
	switch shape {
	case "", "constant", "ramp":
		return nil
	case "poisson", "spike":
		return fmt.Errorf("rate shape %q is roadmap (see sim-source docs); use constant or ramp", shape)
	default:
		return fmt.Errorf("unknown rate shape %q; use constant or ramp", shape)
	}
}

// seedArg reads the optional seed of a control command.
func (g *Gear) seedArg(cmd ctrl.Command) (int64, bool) {
	seedStr, ok := cmd.Args["seed"]
	if !ok {
		return 0, false
	}
	seed, err := strconv.ParseInt(seedStr, 10, 64)
	if err != nil {
		g.logger.Warn("Ignoring the seed of a control command: not an integer", "seed", seedStr, "cmd", cmd.Cmd)
		return 0, false
	}
	return seed, true
}

// applyRate changes the rate of the run in progress, or of the next one, from a
// sim.rate command.
func (g *Gear) applyRate(args map[string]string) {
	if err := validateShape(args["shape"]); err != nil {
		g.logger.Warn("Ignoring sim.rate", "error", err)
		return
	}
	var tps float64
	if v, ok := args["tps"]; ok {
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			g.logger.Warn("Ignoring sim.rate: tps is not a number", "tps", v)
			return
		}
		tps = parsed
	}
	g.rateEngine.UpdateRate(tps, args["shape"], args["from"], args["to"], args["over"])
}

func (g *Gear) handleControlCommand(cmd ctrl.Command) {
	switch cmd.Cmd {
	case ctrl.CmdSimStart:
		g.logger.Info("Received sim.start command", "args", cmd.Args)
		if seed, ok := g.seedArg(cmd); ok {
			g.mu.Lock()
			g.cfg.Seed = seed
			g.mu.Unlock()
			g.generator.Reseed(seed)
		}
		// startGeneration guards on running internally: do NOT pre-set
		// it here, or the guard exits without spawning the loop.
		// Recreate stopCh: a previous sim.stop closes it, and a loop
		// selecting a closed channel exits immediately.
		g.mu.Lock()
		select {
		case <-g.stopCh:
			g.stopCh = make(chan struct{})
		default:
		}
		g.mu.Unlock()
		g.startGeneration(context.Background())
	case ctrl.CmdSimStop:
		g.logger.Info("Received sim.stop command")
		g.mu.Lock()
		g.running = false
		g.closeStopLocked()
		g.mu.Unlock()
	case ctrl.CmdSimRate:
		g.logger.Info("Received sim.rate command", "args", cmd.Args)
		g.applyRate(cmd.Args)
	case ctrl.CmdSimReset:
		g.logger.Info("Received sim.reset command", "args", cmd.Args)
		// Reset generator counters (STAN, RRN, named sequences) and random seed
		if g.generator != nil {
			if seed, ok := g.seedArg(cmd); ok {
				g.mu.Lock()
				g.cfg.Seed = seed
				g.mu.Unlock()
				g.generator.Reseed(seed)
			} else {
				g.generator.ResetCounters()
			}
		}
		// Restart generation if running. Close the old loop's stopCh first
		// or two loops emit at once; recreate the channel before starting
		// fresh.
		g.mu.Lock()
		wasRunning := g.running
		g.running = false
		g.closeStopLocked()
		if wasRunning {
			g.stopCh = make(chan struct{})
		}
		g.mu.Unlock()
		if wasRunning {
			g.startGeneration(context.Background())
		}
	}
}

// compile-time check
var _ sdk.NativeGear = (*Gear)(nil)
var _ sdk.Manifested = (*Gear)(nil)

// isSensitive reports whether a PCI classification implies the value must never
// reach a log or an analytics store in the clear.
func isSensitive(class string) bool {
	switch class {
	case "pan", "chd", "sad", "pii":
		return true
	default:
		return false
	}
}
