// Copyright (c) 2026 JAAB Tech SAS, Uruguay
// SPDX-License-Identifier: BSL-1.1

package sim_responder

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaab-tech/fluxrig/pkg/ctrl"
	"github.com/jaab-tech/fluxrig/pkg/fluxmsg"
	"github.com/jaab-tech/fluxrig/pkg/gears/native/iso8583/codec/sdl"
	"github.com/jaab-tech/fluxrig/pkg/idgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponder_ProcessRequest(t *testing.T) {
	spec := createTestSpec(t)

	cfg := Config{
		Delay: "10ms",
		Default: map[string]string{
			"39": "00",
			"38": "$AUTH",
		},
	}

	responder := NewResponder(spec, createTestFieldMeta(t), cfg, &mockIDGenerator{})
	responder.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	// Create a request FluxMsg
	req := &fluxmsg.FluxMsg{
		FluxID:   func() uuid.UUID { id, _ := uuid.NewV7(); return id }(),
		Metadata: map[string]string{"iso8583.mti": "0100"},
		Data: map[string]any{
			"iso8583.field.2":  "4111111111111111",
			"iso8583.field.11": "000001",
			"iso8583.field.4":  "10000",
			"iso8583.field.41": "TERM0001",
			"card.pan":         "4111111111111111",
			"stan":             "000001",
			"txn_amount":       "10000",
			"terminal_id":      "TERM0001",
		},
	}

	ctx := context.Background()
	resp, err := responder.ProcessRequest(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Empty(t, resp.RawPayload, "responder emits fields only; the codec packs the bytes")
	assert.Equal(t, "0110", resp.Metadata["iso8583.mti"])
	assert.Equal(t, "0100", resp.Metadata["sim.response_to"])
	assert.Equal(t, "000001", resp.Metadata["sim.correlation_id"])

	// Check default response fields
	assert.Equal(t, "00", getRespField(resp, "iso8583.field.39")) // resp_code
	assert.NotEmpty(t, getRespField(resp, "iso8583.field.38"))    // auth_code
}

func TestResponder_EchoValidFields(t *testing.T) {
	spec := createTestSpec(t)

	cfg := Config{
		Delay: "1ms",
		Default: map[string]string{
			"39": "00",
		},
	}

	responder := NewResponder(spec, createTestFieldMeta(t), cfg, &mockIDGenerator{})
	responder.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	req := &fluxmsg.FluxMsg{
		FluxID:   func() uuid.UUID { id, _ := uuid.NewV7(); return id }(),
		Metadata: map[string]string{"iso8583.mti": "0100"},
		Data: map[string]any{
			"iso8583.field.2":  "4111111111111111",
			"iso8583.field.11": "123456",
			"iso8583.field.4":  "5000",
			"iso8583.field.41": "TERM0001",
		},
	}

	ctx := context.Background()
	resp, err := responder.ProcessRequest(ctx, req)

	require.NoError(t, err)
	// STAN (DE 11) should be echoed (response_value: echo for 0110)
	assert.Equal(t, "123456", getRespField(resp, "iso8583.field.11"))
	// Amount (DE 4) should be echoed with response_value: modified for 0110
	assert.Equal(t, "5000", getRespField(resp, "iso8583.field.4"))
	// PAN (DE 2) is NOT valid in response MTI 0110 per reference spec, so NOT echoed
	assert.Empty(t, getRespField(resp, "iso8583.field.2"))
	// Terminal ID (DE 41) is NOT valid in response MTI 0110 per reference spec, so NOT echoed
	assert.Empty(t, getRespField(resp, "iso8583.field.41"))
}

func TestResponder_ConditionalRules(t *testing.T) {
	spec := createTestSpec(t)

	tests := []struct {
		name       string
		amount     string
		expireDate string
		expectCode string
	}{
		{"insufficient funds", "2000000", "2512", "51"},
		{"expired card", "1000", "2401", "54"},
		{"approved", "1000", "2512", "00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Delay: "1ms",
				Default: map[string]string{
					"39": "00",
					"38": "$AUTH",
				},
				Rules: []ResponderRule{
					{
						Name: "Insufficient funds",
						When: "field(4) > 1000000",
						Set:  map[string]string{"resp_code": "51"},
					},
					{
						Name: "Expired card",
						When: "field(14) < 2501",
						Set:  map[string]string{"resp_code": "54"},
					},
				},
			}

			responder := NewResponder(spec, createTestFieldMeta(t), cfg, &mockIDGenerator{})
			responder.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

			req := &fluxmsg.FluxMsg{
				FluxID:   func() uuid.UUID { id, _ := uuid.NewV7(); return id }(),
				Metadata: map[string]string{"iso8583.mti": "0100"},
				Data: map[string]any{
					"iso8583.field.2":  "4111111111111111",
					"iso8583.field.11": "000001",
					"iso8583.field.4":  tt.amount,
					"iso8583.field.14": tt.expireDate,
					"iso8583.field.41": "TERM0001",
				},
			}

			ctx := context.Background()
			resp, err := responder.ProcessRequest(ctx, req)

			require.NoError(t, err)
			assert.Equal(t, tt.expectCode, getRespField(resp, "iso8583.field.39"), "Response code mismatch for %s", tt.name)
		})
	}
}

func TestResponder_RuleDelayOverride(t *testing.T) {
	spec := createTestSpec(t)

	cfg := Config{
		Delay: "100ms",
		Default: map[string]string{
			"39": "00",
		},
		Rules: []ResponderRule{
			{
				Name:  "Fast rule",
				When:  "field(4) > 1000000",
				Set:   map[string]string{"resp_code": "51"},
				Delay: "1ms",
			},
		},
	}

	responder := NewResponder(spec, createTestFieldMeta(t), cfg, &mockIDGenerator{})
	responder.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	req := &fluxmsg.FluxMsg{
		FluxID:   func() uuid.UUID { id, _ := uuid.NewV7(); return id }(),
		Metadata: map[string]string{"iso8583.mti": "0100"},
		Data: map[string]any{
			"iso8583.field.2":  "4111111111111111",
			"iso8583.field.11": "000001",
			"iso8583.field.4":  "2000000", // Triggers fast rule
			"iso8583.field.41": "TERM0001",
		},
	}

	start := time.Now()
	ctx := context.Background()
	_, err := responder.ProcessRequest(ctx, req)
	elapsed := time.Since(start)

	require.NoError(t, err)
	// Should use rule delay (1ms) not base delay (100ms)
	assert.Less(t, elapsed, 50*time.Millisecond, "Should use per-rule delay override")
}

func TestResponder_NoMatchingRule_UsesDefault(t *testing.T) {
	spec := createTestSpec(t)

	cfg := Config{
		Delay: "1ms",
		Default: map[string]string{
			"39": "00",
		},
		Rules: []ResponderRule{
			{
				Name: "Never matches",
				When: "field(4) > 999999999",
				Set:  map[string]string{"resp_code": "99"},
			},
		},
	}

	responder := NewResponder(spec, createTestFieldMeta(t), cfg, &mockIDGenerator{})
	responder.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	req := &fluxmsg.FluxMsg{
		FluxID:   func() uuid.UUID { id, _ := uuid.NewV7(); return id }(),
		Metadata: map[string]string{"iso8583.mti": "0100"},
		Data: map[string]any{
			"iso8583.field.2":  "4111111111111111",
			"iso8583.field.11": "000001",
			"iso8583.field.4":  "1000",
			"iso8583.field.41": "TERM0001",
		},
	}

	ctx := context.Background()
	resp, err := responder.ProcessRequest(ctx, req)

	require.NoError(t, err)
	assert.Equal(t, "00", getRespField(resp, "iso8583.field.39"))
}

func TestConfig_ParseDelay(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"50ms", 50 * time.Millisecond},
		{"1s", 1 * time.Second},
		{"1m30s", 90 * time.Second},
		{"", 0},
	}

	for _, tt := range tests {
		d, err := ParseDelay(tt.input)
		require.NoError(t, err)
		assert.Equal(t, tt.expected, d)
	}
}

// getRespField reads a response field the way the downstream codec does:
// dot-aware Get over nested maps.
func getRespField(resp *fluxmsg.FluxMsg, key string) any {
	val, _ := resp.Get(key)
	return val
}

// Helpers

func createTestSpec(t *testing.T) *sdl.Spec {
	// Load from reference spec file like sim_source tests
	specPath := "../../../../fluxrig/examples/specs/iso8583-v87-ascii.yaml"
	content, err := os.ReadFile(specPath)
	require.NoError(t, err)

	spec, err := sdl.ParseSemantic(content)
	require.NoError(t, err)

	return spec
}

func createTestFieldMeta(t *testing.T) *sdl.FieldMeta {
	specPath := "../../../../fluxrig/examples/specs/iso8583-v87-ascii.yaml"
	content, err := os.ReadFile(specPath)
	require.NoError(t, err)

	_, meta, err := sdl.LoadSpecContent(content, "")
	require.NoError(t, err)

	return meta
}

type mockIDGenerator struct {
	counter uint64
}

func (m *mockIDGenerator) NextFluxID() (uuid.UUID, error) {
	m.counter++
	id, _ := uuid.NewV7()
	return id, nil
}

func (m *mockIDGenerator) NextEntityID(etype idgen.EntityType) uuid.UUID {
	id, _ := uuid.NewV7()
	return id
}

// newSimCommand builds a control-plane command for gear-level tests.
func newSimCommand(cmd string) ctrl.Command {
	return ctrl.Command{Cmd: cmd, Args: map[string]string{}, Src: "test"}
}

// testingLogWriter implements io.Writer for testing
type testingLogWriter struct {
	t *testing.T
}

func (w *testingLogWriter) Write(p []byte) (n int, err error) {
	w.t.Log(string(p))
	return len(p), nil
}

func TestResponder_PropagatesConnID(t *testing.T) {
	spec := createTestSpec(t)

	cfg := Config{
		Delay:   "1ms",
		Default: map[string]string{"39": "00"},
	}

	responder := NewResponder(spec, createTestFieldMeta(t), cfg, &mockIDGenerator{})
	responder.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	req := &fluxmsg.FluxMsg{
		FluxID: func() uuid.UUID { id, _ := uuid.NewV7(); return id }(),
		Metadata: map[string]string{
			"iso8583.mti": "0100",
			"conn.id":     "conn-7",
		},
		Data: map[string]any{
			"iso8583.field.11": "000001",
			"iso8583.field.4":  "1000",
		},
	}

	resp, err := responder.ProcessRequest(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "conn-7", resp.Metadata["conn.id"],
		"without conn.id the io gear cannot route the reply home")
}

func TestGear_PausedDropsRequests(t *testing.T) {
	spec := createTestSpec(t)
	cfg := Config{Delay: "1ms", Default: map[string]string{"39": "00"}}
	responder := NewResponder(spec, createTestFieldMeta(t), cfg, &mockIDGenerator{})
	responder.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	g := &Gear{
		logger:    slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)),
		name:      "issuer",
		responder: responder,
		paused:    true,
	}

	req := &fluxmsg.FluxMsg{
		FluxID:   func() uuid.UUID { id, _ := uuid.NewV7(); return id }(),
		Metadata: map[string]string{"iso8583.mti": "0100"},
		Data:     map[string]any{"iso8583.field.11": "000001"},
	}

	emitted := 0
	g.emit = func(*fluxmsg.FluxMsg) { emitted++ }

	require.NoError(t, g.ProcessPort(context.Background(), "in", req))
	assert.Equal(t, 0, emitted, "paused gear must drop, not queue or emit")

	g.mu.Lock()
	g.paused = false
	g.mu.Unlock()
	require.NoError(t, g.ProcessPort(context.Background(), "in", req))
	assert.Equal(t, 1, emitted, "resumed gear must process")
}

func TestGear_SimResetClearsSequenceCounters(t *testing.T) {
	spec := createTestSpec(t)
	cfg := Config{Delay: "1ms", Default: map[string]string{"39": "00"}}
	responder := NewResponder(spec, createTestFieldMeta(t), cfg, &mockIDGenerator{})
	responder.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))
	responder.seqCounters["rrn"] = 41

	g := &Gear{
		logger:    slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)),
		responder: responder,
	}

	g.handleControlCommand(newSimCommand("sim.reset"))
	assert.Empty(t, responder.seqCounters, "sim.reset must clear macro sequence counters")

	g.handleControlCommand(newSimCommand("sim.stop"))
	assert.True(t, g.paused, "sim.stop must pause processing")

	g.handleControlCommand(newSimCommand("sim.start"))
	assert.False(t, g.paused, "sim.start must resume processing")
}
