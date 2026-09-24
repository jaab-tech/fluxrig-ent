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
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaab-tech/fluxrig/pkg/ctrl"
	"github.com/jaab-tech/fluxrig/pkg/fluxmsg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestResponder(t *testing.T, cfg Config) *Responder {
	t.Helper()
	r := NewResponder(createTestSpec(t), createTestFieldMeta(t), cfg, &mockIDGenerator{})
	r.SetLogger(quietLogger())
	return r
}

// The runtime stops the gears that were never started when another gear fails to start,
// and it may drain and stop one gear in turn.
func TestGear_StopAndDrainAreIdempotent(t *testing.T) {
	t.Run("before Start", func(t *testing.T) {
		g := &Gear{logger: quietLogger()}
		require.NotPanics(t, func() {
			require.NoError(t, g.Stop())
			require.NoError(t, g.Drain(context.Background()))
		})
	})

	t.Run("twice and in both orders", func(t *testing.T) {
		g := &Gear{logger: quietLogger(), stopCh: make(chan struct{})}
		require.NotPanics(t, func() {
			require.NoError(t, g.Drain(context.Background()))
			require.NoError(t, g.Drain(context.Background()))
			require.NoError(t, g.Stop())
			require.NoError(t, g.Stop())
			require.NoError(t, g.Drain(context.Background()))
		})
	})
}

type failingControlPlane struct{}

func (failingControlPlane) Publish(string, ctrl.Command) error { return nil }
func (failingControlPlane) Subscribe(string) (<-chan ctrl.Command, error) {
	return nil, errors.New("no connection")
}

func TestGear_StartKeepsThePausedStateInitSet(t *testing.T) {
	g := &Gear{logger: quietLogger(), cfg: Config{Trigger: "on_control"}, paused: true}
	require.NoError(t, g.Start(context.Background(), func(*fluxmsg.FluxMsg) {}))
	t.Cleanup(func() { _ = g.Stop() })

	g.mu.Lock()
	defer g.mu.Unlock()
	assert.True(t, g.paused, "Start must not touch the state Init gave: a sim.start handled before it would be lost")
}

func TestGear_StartFailsWhenItCannotHearSimStart(t *testing.T) {
	onControl := &Gear{logger: quietLogger(), cfg: Config{Trigger: "on_control"}, paused: true, cp: failingControlPlane{}}
	require.Error(t, onControl.Start(context.Background(), func(*fluxmsg.FluxMsg) {}),
		"a gear that waits for sim.start and cannot subscribe would sit paused for good")

	onLoad := &Gear{logger: quietLogger(), cfg: Config{Trigger: "on_load"}, cp: failingControlPlane{}}
	require.NoError(t, onLoad.Start(context.Background(), func(*fluxmsg.FluxMsg) {}),
		"an on_load gear answers without the control plane")
	t.Cleanup(func() { _ = onLoad.Stop() })
}

func TestGear_ARequestBeforeStartIsNotAnswered(t *testing.T) {
	g := &Gear{
		logger:    quietLogger(),
		responder: newTestResponder(t, Config{Default: map[string]string{"39": "00"}}),
	}
	req := &fluxmsg.FluxMsg{
		FluxID:   func() uuid.UUID { id, _ := uuid.NewV7(); return id }(),
		Metadata: map[string]string{"iso8583.mti": "0100"},
		Data:     map[string]any{"iso8583.field.11": "000001"},
	}
	require.NotPanics(t, func() {
		require.NoError(t, g.ProcessPort(context.Background(), "in", req))
	})
}

func TestResponder_Validate(t *testing.T) {
	ok := Config{
		Delay:   "50ms",
		Default: map[string]string{"39": "00", "38": "$AUTH"},
		Rules: []ResponderRule{
			{Name: "Big", When: "field(4) > 1000000", Set: map[string]string{"39": "51"}, Delay: "10ms"},
			{Name: "Fraud", When: "field(48) == 'RS01'", Set: map[string]string{"39": "59", "44": "$SEQ(fraud, 500)"}},
			{Name: "Random", When: "mti == '0100'", Set: map[string]string{"38": "$RAND(1, 100)"}},
		},
	}
	require.NoError(t, newTestResponder(t, ok).Validate())

	cases := map[string]struct {
		mutate func(*Config)
		want   string
	}{
		"a delay that is not a duration":   {func(c *Config) { c.Delay = "fast" }, "delay"},
		"a negative delay":                 {func(c *Config) { c.Delay = "-5ms" }, "negative"},
		"a rule delay that is not one":     {func(c *Config) { c.Rules[0].Delay = "soon" }, "rule 1"},
		"a rule without a name":            {func(c *Config) { c.Rules[0].Name = "" }, "required"},
		"a rule without a when":            {func(c *Config) { c.Rules[0].When = "" }, "required"},
		"a rule without a set":             {func(c *Config) { c.Rules[0].Set = nil }, "required"},
		"a when in double quotes":          {func(c *Config) { c.Rules[1].When = `field(48) == "RS01"` }, "invalid when"},
		"a key that names no field":        {func(c *Config) { c.Default["nope"] = "x" }, "neither a field number nor an alias"},
		"a field number below one":         {func(c *Config) { c.Default["0"] = "x" }, "positive"},
		"a macro that does not exist":      {func(c *Config) { c.Default["38"] = "$auth" }, "unknown macro"},
		"a RAND that is not a number":      {func(c *Config) { c.Rules[2].Set["38"] = "$RAND(1, many)" }, "not an integer"},
		"a SEQ start that is not a number": {func(c *Config) { c.Rules[1].Set["44"] = "$SEQ(fraud, x)" }, "start"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Config{
				Delay:   ok.Delay,
				Default: map[string]string{"39": "00", "38": "$AUTH"},
				Rules: []ResponderRule{
					{Name: "Big", When: "field(4) > 1000000", Set: map[string]string{"39": "51"}, Delay: "10ms"},
					{Name: "Fraud", When: "field(48) == 'RS01'", Set: map[string]string{"39": "59", "44": "$SEQ(fraud, 500)"}},
					{Name: "Random", When: "mti == '0100'", Set: map[string]string{"38": "$RAND(1, 100)"}},
				},
			}
			tc.mutate(&cfg)
			err := newTestResponder(t, cfg).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// The documented form has a space after the comma. Without trimming it, $RAND(1, 100)
// read as 0 to 999999 and $SEQ(a, 500) started at 1.
func TestResponder_MacroArgumentsMayHaveSpaces(t *testing.T) {
	r := newTestResponder(t, Config{})
	req := &fluxmsg.FluxMsg{Metadata: map[string]string{}, Data: map[string]any{}}

	assert.Equal(t, "7", r.expandMacro("$RAND(7, 7)", 0, req, nil))
	assert.Equal(t, "7", r.expandMacro("$RAND(7,7)", 0, req, nil))
	assert.Equal(t, "500", r.expandMacro("$SEQ(a, 500)", 0, req, nil))
	assert.Equal(t, "501", r.expandMacro("$SEQ(a, 500)", 0, req, nil))
}

func TestResponder_UUIDIsVersion7(t *testing.T) {
	r := newTestResponder(t, Config{})
	id, err := uuid.Parse(r.expandMacro("$UUID", 0, &fluxmsg.FluxMsg{}, nil))
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), id.Version())
}

// Run under -race: two wires into the gear run on two goroutines, and sim.reset comes
// from a third. The macros draw from one generator and move the same counters.
func TestResponder_MacrosAndResetAreSafeTogether(t *testing.T) {
	r := newTestResponder(t, Config{})
	req := &fluxmsg.FluxMsg{Metadata: map[string]string{}, Data: map[string]any{}}

	var wg sync.WaitGroup
	for _, template := range []string{"$SEQ(a)", "$RRN", "$RAND(1, 9)", "$AUTH", "$STAN"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r.expandMacro(template, 0, req, nil)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r.ResetCounters()
		}
	}()
	wg.Wait()
}

// $NOW and $RRN render the clock of the configured zone, and the date follows the zone.
func TestResponder_NowFollowsTheTimezone(t *testing.T) {
	instant := time.Date(2026, 9, 21, 23, 30, 5, 0, time.UTC)
	render := func(tz, macro string, field int) string {
		r := newTestResponder(t, Config{Timezone: tz})
		r.clock = func() time.Time { return instant }
		return r.expandMacro(macro, field, &fluxmsg.FluxMsg{}, nil)
	}
	const datetime, clock, date = 7, 12, 13 // DE 7, DE 12 and DE 13 of the reference spec

	assert.Equal(t, "0921233005", render("GMT", "$NOW", datetime))
	assert.Equal(t, "0921203005", render("America/Montevideo", "$NOW", datetime))
	assert.Equal(t, "0922083005", render("Asia/Tokyo", "$NOW", datetime))
	assert.Equal(t, "203005", render("America/Montevideo", "$NOW", clock))
	assert.Equal(t, "0922", render("Asia/Tokyo", "$NOW", date))
	assert.Equal(t, instant.In(time.Local).Format("0102150405"), render("", "$NOW", datetime), "the default is the machine's zone")
	assert.Equal(t, "260922000001", render("Asia/Tokyo", "$RRN", 37))
}

func TestResponder_AnUnknownTimezoneIsRefusedWhenApplied(t *testing.T) {
	err := newTestResponder(t, Config{Timezone: "Mars/Olympus"}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Mars/Olympus")
}

// A field that declares a layout is rendered in it: DE 14 is YYMM, and the current month
// is 2609 in September 2026.
func TestResponder_NowUsesTheLayoutOfTheField(t *testing.T) {
	r := newTestResponder(t, Config{Timezone: "GMT"})
	r.clock = func() time.Time { return time.Date(2026, 9, 21, 23, 30, 5, 0, time.UTC) }
	req := &fluxmsg.FluxMsg{}

	assert.Equal(t, "2609", r.expandMacro("$NOW", 14, req, nil))
	assert.Equal(t, "0921", r.expandMacro("$NOW", 13, req, nil))
	assert.Equal(t, "233005", r.expandMacro("$NOW", 12, req, nil))
	assert.Equal(t, "0921233005", r.expandMacro("$NOW", 7, req, nil))
}
