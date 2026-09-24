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

package sim_responder

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/jaab-tech/fluxrig/pkg/bus"
	"github.com/jaab-tech/fluxrig/pkg/ctrl"
	"github.com/jaab-tech/fluxrig/pkg/fluxmsg"
	"github.com/jaab-tech/fluxrig/pkg/gears/native/iso8583/codec/sdl"
	"github.com/jaab-tech/fluxrig/pkg/manager"
	"github.com/jaab-tech/fluxrig/pkg/sdk"
)

// Gear implements the NativeGear and PortedGear interfaces for the sim_responder gear.
//
// It is a pure logic gear: a decoded request arrives on the "in" port (from
// an io gear through a codec gear), the responder applies defaults and rules,
// and the fields-only response leaves on the "out" port for the downstream
// codec to pack. The gear never touches the wire — no listener, no framing,
// no packing. Transport (TCP/mTLS, framing, connection routing) is the io
// gear's job; bytes are the codec gear's job.
type Gear struct {
	logger *slog.Logger
	name   string
	emit   func(*fluxmsg.FluxMsg)

	cfg       Config
	spec      *sdl.Spec
	meta      *sdl.FieldMeta
	responder *Responder

	mu         sync.Mutex
	running    bool
	paused     bool
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
	g.logger = ctx.Logger().With("type", "sim_responder")
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
	if trigger, ok := config["trigger"].(string); ok {
		g.cfg.Trigger = trigger
	}
	if delay, ok := config["delay"].(string); ok {
		g.cfg.Delay = delay
	}
	if tz, ok := config["timezone"].(string); ok {
		g.cfg.Timezone = tz
	}

	// YAML maps with numeric keys (e.g. ISO field numbers like
	// `39: "00"`) decode as map[any]any, not map[string]any: a bare
	// assertion silently drops the whole map. Normalize both shapes.
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

	// Parse default
	if defaultMap := stringMap(config["default"]); len(defaultMap) > 0 {
		g.cfg.Default = defaultMap
	}

	// Parse rules
	if rulesList, ok := config["rules"].([]any); ok {
		g.cfg.Rules = nil
		for i, r := range rulesList {
			rm, ok := r.(map[string]any)
			if !ok {
				return fmt.Errorf("rule %d: expected a map with name, when and set", i+1)
			}
			rule := ResponderRule{}
			if name, ok := rm["name"].(string); ok {
				rule.Name = name
			}
			if when, ok := rm["when"].(string); ok {
				rule.When = when
			}
			if setMap := stringMap(rm["set"]); len(setMap) > 0 {
				rule.Set = setMap
			}
			if delay, ok := rm["delay"].(string); ok {
				rule.Delay = delay
			}
			// An incomplete rule is kept and reported by Validate, not dropped.
			g.cfg.Rules = append(g.cfg.Rules, rule)
		}
	}

	// Validate required fields
	if g.cfg.Spec == "" {
		return fmt.Errorf("missing 'spec' in configuration")
	}
	if g.cfg.Trigger != "on_load" && g.cfg.Trigger != "on_control" {
		return fmt.Errorf("unknown trigger %q; use on_load or on_control", g.cfg.Trigger)
	}
	// A gear that waits for sim.start answers nothing until it arrives, and that includes
	// the requests that reach it before Start has run.
	g.paused = g.cfg.Trigger == "on_control"

	// Load spec
	if err := g.loadSpec(ctx); err != nil {
		return fmt.Errorf("failed to load spec %q: %w", g.cfg.Spec, err)
	}

	// Build responder
	g.responder = NewResponder(g.spec, g.meta, g.cfg, g.idgen)
	g.responder.SetLogger(g.logger)
	if err := g.responder.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	g.logger.Info("Initialized sim_responder",
		"spec", g.cfg.Spec,
		"spec_id", g.meta.SpecID,
		"spec_version", g.meta.SpecVersion,
		"trigger", g.cfg.Trigger,
		"delay", g.cfg.Delay,
		"rules", len(g.cfg.Rules),
		"default", fmt.Sprintf("%v", g.cfg.Default),
	)

	return nil
}

// Start begins the active lifecycle of the Gear.
func (g *Gear) Start(ctx context.Context, emit func(*fluxmsg.FluxMsg)) error {
	g.mu.Lock()
	g.emit = emit
	g.stopCh = make(chan struct{})
	g.running = true
	g.mu.Unlock()

	g.logger.Info("Starting sim_responder",
		"flux.type", "GEAR",
		"flux.name", g.name,
		"type", "sim_responder",
	)

	// Subscribe to the control plane: sim.start/sim.stop pause and resume
	// processing, sim.reset clears the macro sequence counters. Always
	// subscribed — control works regardless of trigger.
	if cp, ok := g.getControlPlane(); ok {
		ch, err := cp.Subscribe(g.name)
		if err != nil {
			// A gear that waits for sim.start and cannot hear it would sit paused for
			// good with nothing but a log line to say so.
			if g.cfg.Trigger == "on_control" {
				return fmt.Errorf("subscribe to the control plane: %w", err)
			}
			g.logger.Error("Failed to subscribe to control plane", "error", err)
		} else {
			g.ctrlCh = ch
			g.logger.Info("Subscribed to control plane")
		}
	}
	// The paused state was set by Init, before any request or command could arrive,
	// and this loop starts after it.
	go g.controlLoop(ctx)

	return nil
}

// ProcessPort handles a message that arrived on the named input port.
// Implements PortedGear interface: decoded request in, fields-only
// response out (the downstream codec packs the bytes).
func (g *Gear) ProcessPort(ctx context.Context, port string, msg *fluxmsg.FluxMsg) error {
	if port != "in" {
		g.logger.Warn("Received message on unexpected port", "port", port)
		return nil
	}

	g.mu.Lock()
	paused := g.paused
	emit := g.emit
	g.mu.Unlock()
	if paused {
		// Paused via sim.stop or trigger=on_control before sim.start.
		// Drop rather than block: stalling here would stall the pipeline.
		g.logger.Debug("Dropping request while paused", "flux.id", msg.FluxID.String())
		return nil
	}

	// Process the request and generate response
	resp, err := g.responder.ProcessRequest(ctx, msg)
	if err != nil {
		g.logger.Error("Failed to process request", "error", err)
		return err
	}

	if resp != nil {
		if emit == nil {
			// The gear is not started: there is nowhere to send the response.
			g.logger.Warn("Dropping a response: the gear is not started", "flux.id", msg.FluxID.String())
			return nil
		}
		// Emit response on "out" port
		emit(resp)
	}

	return nil
}

// Process handles an incoming message (Filter Mode).
// Not used for PortedGear but required by NativeGear interface.
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
	g.logger.Info("Drained sim_responder")
	return nil
}

// closeStopLocked ends the control loop. It is safe to call more than once and before
// Start: the runtime stops the gears that were never started when another gear fails to
// start. The caller holds mu.
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
	g.logger.Info("Stopped sim_responder")
	return nil
}

// loadSpec loads the spec using sdl.LoadSpecContent (like codec gear)
func (g *Gear) loadSpec(ctx sdk.GearContext) error {
	content, err := g.readSpecContent(ctx)
	if err != nil {
		return err
	}

	// Use sdl.LoadSpecContent for the field metadata (aliases, secure IDs).
	// The moov wire spec is not needed: decoding and packing are the codec
	// gear's job, this gear only works the decoded fields.
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
	spec, err := sdl.ParseSemantic(content)
	if err != nil {
		return nil, err
	}

	protocol := "iso8583"

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

func (g *Gear) handleControlCommand(cmd ctrl.Command) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch cmd.Cmd {
	case ctrl.CmdSimStart:
		g.logger.Info("Received sim.start command, resuming")
		g.paused = false
		g.running = true
	case ctrl.CmdSimStop:
		g.logger.Info("Received sim.stop command, pausing")
		g.paused = true
		g.running = false
	case ctrl.CmdSimReset:
		g.logger.Info("Received sim.reset command, clearing sequence counters")
		g.responder.ResetCounters()
	case ctrl.CmdSimRate:
		g.logger.Debug("sim.rate ignored: responder has no traffic generator")
	}
}

func (g *Gear) controlLoop(ctx context.Context) {
	if g.ctrlCh == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.stopCh:
			return
		case cmd := <-g.ctrlCh:
			g.handleControlCommand(cmd)
		}
	}
}

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

// compile-time checks
var _ sdk.NativeGear = (*Gear)(nil)
var _ sdk.Manifested = (*Gear)(nil)
var _ sdk.PortedGear = (*Gear)(nil)
