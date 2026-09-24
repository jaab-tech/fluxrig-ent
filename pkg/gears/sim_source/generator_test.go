// Copyright (c) 2026 JAAB Tech SAS, Uruguay
// SPDX-License-Identifier: BSL-1.1

package sim_source

import (
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaab-tech/fluxrig/pkg/gears/native/iso8583/codec/sdl"
	"github.com/jaab-tech/fluxrig/pkg/idgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerator_Macros(t *testing.T) {
	spec, meta, content := loadTestSpec(t)
	idgen := &mockIDGenerator{}
	cfg := Config{Seed: 12345}
	gen := NewGeneratorFromContent(content, spec, meta, cfg, idgen)
	gen.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	t.Run("$PAN generates Luhn-valid PAN", func(t *testing.T) {
		pan := gen.expandMacro("$PAN(4111,16)", 2, map[int]string{})
		assert.Len(t, pan, 16)
		assert.True(t, isLuhnValid(pan))
		assert.Equal(t, "4111", pan[:4])
	})

	t.Run("$STAN generates incrementing sequence", func(t *testing.T) {
		stan1 := gen.expandMacro("$STAN", 11, map[int]string{})
		stan2 := gen.expandMacro("$STAN", 11, map[int]string{})
		assert.Equal(t, "000001", stan1)
		assert.Equal(t, "000002", stan2)
	})

	t.Run("$RRN generates timestamp + sequence", func(t *testing.T) {
		rrn1 := gen.expandMacro("$RRN", 37, map[int]string{})
		rrn2 := gen.expandMacro("$RRN", 37, map[int]string{})
		assert.Len(t, rrn1, 12)
		assert.Len(t, rrn2, 12)
		assert.NotEqual(t, rrn1, rrn2)
	})

	t.Run("$SEQ generates named sequences", func(t *testing.T) {
		seq1 := gen.expandMacro("$SEQ(counter,100)", 0, map[int]string{})
		seq2 := gen.expandMacro("$SEQ(counter,100)", 0, map[int]string{})
		assert.Equal(t, "100", seq1)
		assert.Equal(t, "101", seq2)
	})

	t.Run("$RAND generates within range", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			val := gen.expandMacro("$RAND(10,20)", 0, map[int]string{})
			num := parseInt(t, val)
			assert.True(t, num >= 10 && num <= 20)
		}
	})

	t.Run("$UUID generates valid UUID", func(t *testing.T) {
		uuid := gen.expandMacro("$UUID", 0, map[int]string{})
		assert.NotEmpty(t, uuid)
		assert.Len(t, uuid, 36)
	})

	t.Run("$NOW generates date for date field", func(t *testing.T) {
		now := gen.expandMacro("$NOW", 14, map[int]string{})
		assert.Len(t, now, 4)
	})

	t.Run("$ENUM picks from value set", func(t *testing.T) {
		enumVal := gen.expandMacro("$ENUM", 22, map[int]string{})
		assert.Contains(t, []string{"01", "02", "05", "07", "90", "91"}, enumVal)
	})

	t.Run("$INVALID generates value outside closed set", func(t *testing.T) {
		invalid := gen.expandMacro("$INVALID", 39, map[int]string{})
		assert.NotEmpty(t, invalid)
		enum := spec.Enums["response_code"]
		_, exists := enum.Values[invalid]
		assert.False(t, exists)
	})

	t.Run("$AUTH generates 6-char alphanumeric", func(t *testing.T) {
		auth := gen.expandMacro("$AUTH", 38, map[int]string{})
		assert.Len(t, auth, 6)
		assert.Regexp(t, "^[A-Z0-9]{6}$", auth)
	})
}

func TestGenerator_Generate(t *testing.T) {
	spec, meta, content := loadTestSpec(t)
	idgen := &mockIDGenerator{}
	cfg := Config{Seed: 42, Mix: []MixEntry{{Use: "0100", Weight: 100}}}
	gen := NewGeneratorFromContent(content, spec, meta, cfg, idgen)
	gen.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	// Test field value generation using FULL generation logic (same as Generate())
	fieldValues := genAllFields(t, gen)

	// Check that required fields are generated
	assert.Contains(t, fieldValues, 2, "PAN field should be generated")
	assert.Contains(t, fieldValues, 11, "STAN field should be generated")
	assert.Contains(t, fieldValues, 4, "Amount field should be generated")
	assert.NotEmpty(t, fieldValues[2], "PAN should not be empty")
	assert.NotEmpty(t, fieldValues[11], "STAN should not be empty")
	assert.NotEmpty(t, fieldValues[4], "Amount should not be empty")
}

func TestGenerator_DeterministicSeed(t *testing.T) {
	spec, meta, content := loadTestSpec(t)
	cfg := Config{Seed: 999, Mix: []MixEntry{{Use: "0100", Weight: 100}}}

	gen1 := NewGeneratorFromContent(content, spec, meta, cfg, &mockIDGenerator{})
	gen1.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))
	gen2 := NewGeneratorFromContent(content, spec, meta, cfg, &mockIDGenerator{})
	gen2.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	// Test deterministic field values using FULL generation logic
	// Both generators have same seed, so they should produce identical results
	fieldValues1 := genAllFields(t, gen1)
	fieldValues2 := genAllFields(t, gen2)

	// Same seed should produce identical field values
	assert.Equal(t, fieldValues1, fieldValues2, "Same seed should produce identical field values")
}

// genAllFields generates all field values for a generator using FULL generation logic
func genAllFields(t *testing.T, gen *Generator) map[int]string {
	t.Helper()
	fieldValues := make(map[int]string)
	mti := gen.selectMTI()
	requiredFields := gen.determineFields(mti)

	cfg := Config{} // Use empty config to test spec defaults

	// Debug: log simDefaults
	t.Logf("simDefaults keys: %v", getMapKeys(gen.simDefaults))
	for k, v := range gen.simDefaults {
		t.Logf("  simDefaults[%d]: From=%s, Weights=%v", k, v.Choose.From, v.Choose.Weights)
	}

	// Apply config overrides
	setFields := gen.resolveFieldMap(cfg.Set)
	templateFields := gen.resolveFieldMap(cfg.Templates[mti])
	defaultFields := gen.resolveFieldMap(cfg.Defaults)

	for _, fieldID := range requiredFields {
		if _, ok := setFields[fieldID]; ok {
			continue
		}
		if val, ok := templateFields[fieldID]; ok {
			fieldValues[fieldID] = gen.expandMacro(val, fieldID, fieldValues)
			continue
		}
		if val, ok := defaultFields[fieldID]; ok {
			fieldValues[fieldID] = gen.expandMacro(val, fieldID, fieldValues)
			continue
		}
		// Use Generator's extracted simulation data
		if val, exists := gen.simDefaults[fieldID]; exists && !isEmptySimValue(val) {
			t.Logf("Using simDefault for field %d: From=%s", fieldID, val.Choose.From)
			fieldValues[fieldID] = gen.expandSimValue(val, fieldID, fieldValues)
			continue
		}
		if tmpl, exists := gen.simTemplates[mti]; exists {
			if val, exists := tmpl[fieldID]; exists && !isEmptySimValue(val) {
				fieldValues[fieldID] = gen.expandSimValue(val, fieldID, fieldValues)
				continue
			}
		}
		fieldValues[fieldID] = gen.synthesizeField(fieldID, fieldValues)
	}

	// Apply Set values (override everything)
	for fieldID, val := range setFields {
		fieldValues[fieldID] = gen.expandMacro(val, fieldID, fieldValues)
	}
	return fieldValues
}

// getMapKeys returns the keys of a map for logging
func getMapKeys(m map[int]SimValue) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestGenerator_ValuePrecedence(t *testing.T) {
	spec, meta, content := loadTestSpec(t)
	idgen := &mockIDGenerator{}
	cfg := Config{
		Seed: 123,
		Mix:  []MixEntry{{Use: "0100", Weight: 100}},
		Set:  map[string]string{"pan": "4111111111111111"},
	}
	gen := NewGeneratorFromContent(content, spec, meta, cfg, idgen)
	gen.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	// Test precedence without full message packing
	setFields := gen.resolveFieldMap(cfg.Set)
	assert.Equal(t, "4111111111111111", setFields[2])
}

// sim_source is a logic gear: it must never touch the wire format itself, only
// resolve field values and hand them to the pipeline as data, the same shape
// codec_iso8583's decode direction produces. This is a regression test for a
// real bug: Generate() used to also pack the fields into wire bytes with
// moov/iso8583 and set RawPayload, entirely redundantly, since nothing ever
// read it downstream in the one topology that exercised it end to end
// (test/robot/suites/simulator/scenario_integration.yaml immediately decoded
// it right back with a now-removed codec gear).
func TestGenerator_GenerateEmitsFieldsOnlyNeverRawPayload(t *testing.T) {
	spec, meta, content := loadTestSpec(t)
	cfg := Config{
		Seed: 20260904,
		Mix:  []MixEntry{{Use: "0100", Weight: 100}},
		Set:  map[string]string{"pan": "4111111111111111"},
	}
	gen := NewGeneratorFromContent(content, spec, meta, cfg, &mockIDGenerator{})
	gen.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	msg, err := gen.Generate()
	require.NoError(t, err)
	require.NotNil(t, msg)

	assert.Empty(t, msg.RawPayload, "sim_source must not pack wire bytes; that is a codec gear's job")
	assert.Equal(t, "0100", msg.Metadata["iso8583.mti"])

	pan, ok := msg.Get("iso8583.field.2")
	require.True(t, ok, "the PAN field must be readable the same dot-aware way codec_iso8583's decode output is")
	assert.Equal(t, "4111111111111111", pan)
}

func TestRateEngine(t *testing.T) {
	cfg := RateConfig{Shape: "constant", TPS: 100, Duration: "10s"}
	logger := slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil))
	engine := NewRateEngine(cfg, logger)

	ticker := engine.Ticker()
	require.NotNil(t, ticker)

	interval := time.Second / 100
	assert.Equal(t, interval, engine.calculateInterval())
	ticker.Stop()
}

func TestRateEngine_Ramp(t *testing.T) {
	cfg := RateConfig{Shape: "ramp", From: 10, To: 100, Over: "1s", TPS: 10}
	logger := slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil))
	engine := NewRateEngine(cfg, logger)

	ticker := engine.Ticker()
	require.NotNil(t, ticker)
	// Stop ends rampLoop, which the ticker alone does not.
	t.Cleanup(engine.Stop)

	// rampLoop raises the rate in steps of 100ms and writes currentTPS under mu,
	// so the test reads it under mu too, and waits for the rise rather than for a
	// guessed amount of time.
	require.Eventually(t, func() bool {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		return engine.currentTPS > 10.0
	}, 5*time.Second, 10*time.Millisecond, "the ramp never raised the rate above its starting point")
}

func TestConfig_ResolveFieldKey(t *testing.T) {
	spec, meta, content := loadTestSpec(t)
	gen := NewGeneratorFromContent(content, spec, meta, Config{}, &mockIDGenerator{})
	gen.SetLogger(slog.New(slog.NewTextHandler(&testingLogWriter{t}, nil)))

	assert.Equal(t, 2, gen.resolveFieldKey("2"))
	assert.Equal(t, 11, gen.resolveFieldKey("11"))
	assert.Equal(t, 2, gen.resolveFieldKey("pan"))
	assert.Equal(t, 11, gen.resolveFieldKey("stan"))
}

// Helpers

func loadTestSpec(t *testing.T) (*sdl.Spec, *sdl.FieldMeta, []byte) {
	t.Helper()
	// Load from reference spec file
	specPath := "../../../../fluxrig/examples/specs/iso8583-v87-ascii.yaml"
	content, err := os.ReadFile(specPath)
	require.NoError(t, err)

	// LoadSpecContent also returns a moov MessageSpec, for gears that pack or
	// unpack wire bytes; the generator only needs the FieldMeta.
	_, meta, err := sdl.LoadSpecContent(content, "")
	require.NoError(t, err)

	spec, err := sdl.ParseSemantic(content)
	require.NoError(t, err)

	// Debug: check simulation data in spec
	rv := reflect.ValueOf(spec).Elem()
	simField := rv.FieldByName("Simulation")
	if simField.IsValid() {
		defaultsField := simField.FieldByName("Defaults")
		if defaultsField.IsValid() && defaultsField.Kind() == reflect.Map {
			t.Logf("spec.Simulation.Defaults has %d entries", defaultsField.Len())
			for _, key := range defaultsField.MapKeys() {
				val := defaultsField.MapIndex(key)
				t.Logf("  Default[%v]: kind=%s, valid=%v", key, val.Kind(), val.IsValid())
				if val.Kind() == reflect.Struct {
					chooseField := val.FieldByName("Choose")
					if chooseField.IsValid() {
						t.Logf("  Default[%v].Choose.From = %q", key, val.FieldByName("Choose").FieldByName("From").String())
					}
				}
			}
		}
	}

	return spec, meta, content
}

type mockIDGenerator struct {
	counter uint64
}

func (m *mockIDGenerator) NextFluxID() (uuid.UUID, error) {
	m.counter++
	return uuid.NewV7()
}

func (m *mockIDGenerator) NextEntityID(etype idgen.EntityType) uuid.UUID {
	id, _ := uuid.NewV7()
	return id
}

type testingLogWriter struct {
	t *testing.T
}

func (w *testingLogWriter) Write(p []byte) (n int, err error) {
	w.t.Log(string(p))
	return len(p), nil
}

func parseInt(t *testing.T, s string) int {
	t.Helper()
	val, err := strconv.Atoi(s)
	require.NoError(t, err)
	return val
}

func isLuhnValid(pan string) bool {
	sum := 0
	alt := false
	for i := len(pan) - 1; i >= 0; i-- {
		d := int(pan[i] - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}
