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
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jaab-tech/fluxrig-ent/pkg/gears/simmacro"
	"github.com/jaab-tech/fluxrig/pkg/fluxmsg"
	"github.com/jaab-tech/fluxrig/pkg/gears/native/iso8583/codec/sdl"
	"github.com/jaab-tech/fluxrig/pkg/sdk"
	"gopkg.in/yaml.v3"
)

// SimValue mirrors sdl.simValue (unexported) for simulation value generation.
type SimValue struct {
	Choose struct {
		From    string         `yaml:"from"`
		Weights map[string]int `yaml:"weights"`
	} `yaml:"choose"`
	// Raw carries a scalar literal or macro verbatim, mirrored from
	// sdl.simValue.Raw. expandSimValue expands it when no choice
	// is present.
	Raw string `yaml:"-"`
}

// Generator resolves ISO8583 field values from a spec's simulation section
// into fields-only fluxMsg, the same shape codec_iso8583's decode direction
// produces. It never packs wire bytes: that stays a codec gear's job.
type Generator struct {
	// mu serializes Generate with the resets and the change of seed: the generation
	// loop and the control commands run on different goroutines.
	mu          sync.Mutex
	spec        *sdl.Spec
	meta        *sdl.FieldMeta
	cfg         Config
	idgen       sdk.IDGenerator
	logger      *slog.Logger
	loc         *time.Location   // the zone $NOW and $RRN render in
	clock       func() time.Time // replaced by tests; nil reads the machine's clock
	rand        *rand.Rand
	seqCounters map[string]uint64
	stanCounter uint64
	rrnCounter  uint64
	// Simulation data extracted from spec (unexported in sdl, so we mirror it)
	simDefaults  map[int]SimValue
	simTemplates map[string]map[int]SimValue
}

// NewGenerator creates a new generator.
func NewGenerator(spec *sdl.Spec, meta *sdl.FieldMeta, cfg Config, idgen sdk.IDGenerator) *Generator {
	src := rand.NewPCG(uint64(cfg.Seed), uint64(cfg.Seed>>32))

	// Extract simulation data from spec (using reflection since sdl.simulation is unexported)
	simDefaults, simTemplates := extractSimulationData(spec)

	return &Generator{
		spec:         spec,
		meta:         meta,
		cfg:          cfg,
		idgen:        idgen,
		loc:          zoneOf(cfg.Timezone),
		rand:         rand.New(src),
		seqCounters:  make(map[string]uint64),
		simDefaults:  simDefaults,
		simTemplates: simTemplates,
	}
}

// NewGeneratorFromContent creates a new generator from raw spec content.
// This allows parsing the simulation section which is unexported in sdl.Spec.
func NewGeneratorFromContent(specContent []byte, spec *sdl.Spec, meta *sdl.FieldMeta, cfg Config, idgen sdk.IDGenerator) *Generator {
	src := rand.NewPCG(uint64(cfg.Seed), uint64(cfg.Seed>>32))

	// Parse simulation section from raw content
	simDefaults, simTemplates := parseSimulationFromContent(specContent)

	return &Generator{
		spec:         spec,
		meta:         meta,
		cfg:          cfg,
		idgen:        idgen,
		loc:          zoneOf(cfg.Timezone),
		rand:         rand.New(src),
		seqCounters:  make(map[string]uint64),
		simDefaults:  simDefaults,
		simTemplates: simTemplates,
	}
}

// parseSimulationFromContent parses the x-fluxrig-simulation section from raw YAML content.
func parseSimulationFromContent(content []byte) (map[int]SimValue, map[string]map[int]SimValue) {
	var doc struct {
		Spec struct {
			Simulation struct {
				Mix []struct {
					Use    string `yaml:"use"`
					Weight int    `yaml:"weight"`
				} `yaml:"mix"`
				Defaults  map[int]simValueRaw            `yaml:"defaults"`
				Templates map[string]map[int]simValueRaw `yaml:"templates"`
			} `yaml:"x-fluxrig-simulation"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, nil
	}

	// Convert simValueRaw to SimValue
	defaults := make(map[int]SimValue)
	for k, v := range doc.Spec.Simulation.Defaults {
		defaults[k] = SimValue{
			Choose: struct {
				From    string         `yaml:"from"`
				Weights map[string]int `yaml:"weights"`
			}{
				From:    v.Choose.From,
				Weights: v.Choose.Weights,
			},
		}
		// Handle scalar values (plain strings like "$NOW")
		if v.Choose.From == "" && v.Raw != "" {
			defaults[k] = SimValue{
				Choose: struct {
					From    string         `yaml:"from"`
					Weights map[string]int `yaml:"weights"`
				}{
					From:    v.Raw,
					Weights: nil,
				},
			}
		}
	}

	templates := make(map[string]map[int]SimValue)
	for mti, tmpl := range doc.Spec.Simulation.Templates {
		templates[mti] = make(map[int]SimValue)
		for k, v := range tmpl {
			templates[mti][k] = SimValue{
				Choose: struct {
					From    string         `yaml:"from"`
					Weights map[string]int `yaml:"weights"`
				}{
					From:    v.Choose.From,
					Weights: v.Choose.Weights,
				},
			}
			if v.Choose.From == "" && v.Raw != "" {
				templates[mti][k] = SimValue{
					Choose: struct {
						From    string         `yaml:"from"`
						Weights map[string]int `yaml:"weights"`
					}{
						From:    v.Raw,
						Weights: nil,
					},
				}
			}
		}
	}

	return defaults, templates
}

// HasValueSource reports whether there is anything to build a message from: the
// simulation section of the spec, or defaults, templates or set values in the gear's
// own configuration.
func (g *Generator) HasValueSource() bool {
	return len(g.simDefaults) > 0 || len(g.simTemplates) > 0 ||
		len(g.cfg.Defaults) > 0 || len(g.cfg.Templates) > 0 || len(g.cfg.Set) > 0
}

// ResetCounters resets all sequence counters (STAN, RRN, named sequences)
// to their initial state. This is used for deterministic testing.
func (g *Generator) ResetCounters() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resetLocked()
}

// Reseed sets a new seed and resets the counters and the random generator to it: the
// same seed reproduces the same sequence.
func (g *Generator) Reseed(seed int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cfg.Seed = seed
	g.resetLocked()
}

func (g *Generator) resetLocked() {
	g.stanCounter = 0
	g.rrnCounter = 0
	g.seqCounters = make(map[string]uint64)
	// Re-seed the random generator based on config seed
	g.rand = rand.New(rand.NewPCG(uint64(g.cfg.Seed), uint64(g.cfg.Seed>>32)))
}

// ResetStanCounter resets the STAN counter to zero for deterministic testing.
func (g *Generator) ResetStanCounter() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stanCounter = 0
}

// simValueRaw is a raw version that can unmarshal both scalar and mapping nodes.
type simValueRaw struct {
	Choose struct {
		From    string         `yaml:"from"`
		Weights map[string]int `yaml:"weights"`
	} `yaml:"choose"`
	Raw string `yaml:"-"` // For scalar values
}

func (v *simValueRaw) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		v.Raw = node.Value
		return nil
	}
	type raw simValueRaw
	var out raw
	if err := node.Decode(&out); err != nil {
		return err
	}
	*v = simValueRaw(out)
	return nil
}

// extractSimulationData extracts the simulation defaults and templates from the spec.
// Uses reflection since sdl.simulation is unexported.
func extractSimulationData(spec *sdl.Spec) (map[int]SimValue, map[string]map[int]SimValue) {
	rv := reflect.ValueOf(spec).Elem()
	simField := rv.FieldByName("Simulation")
	if !simField.IsValid() {
		return nil, nil
	}

	var defaults map[int]SimValue
	var templates map[string]map[int]SimValue

	// Extract Defaults
	defaultsField := simField.FieldByName("Defaults")
	if defaultsField.IsValid() && defaultsField.Kind() == reflect.Map {
		defaults = make(map[int]SimValue)
		for _, key := range defaultsField.MapKeys() {
			if key.Kind() == reflect.Int {
				val := defaultsField.MapIndex(key)
				if val.IsValid() {
					defaults[int(key.Int())] = toSimValue(val.Interface())
				}
			}
		}
	}

	// Extract Templates
	templatesField := simField.FieldByName("Templates")
	if templatesField.IsValid() && templatesField.Kind() == reflect.Map {
		templates = make(map[string]map[int]SimValue)
		for _, key := range templatesField.MapKeys() {
			if key.Kind() == reflect.String {
				mti := key.String()
				val := templatesField.MapIndex(key)
				if val.IsValid() && val.Kind() == reflect.Map {
					templates[mti] = make(map[int]SimValue)
					for _, fkey := range val.MapKeys() {
						if fkey.Kind() == reflect.Int {
							fval := val.MapIndex(fkey)
							if fval.IsValid() {
								templates[mti][int(fkey.Int())] = toSimValue(fval.Interface())
							}
						}
					}
				}
			}
		}
	}

	return defaults, templates
}

func (g *Generator) SetLogger(logger *slog.Logger) {
	g.logger = logger
}

// toSimValue converts the unexported sdl.simValue to our local SimValue using reflection.
func toSimValue(v any) SimValue {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		return SimValue{}
	}
	result := SimValue{}
	chooseField := rv.FieldByName("Choose")
	if chooseField.IsValid() {
		fromField := chooseField.FieldByName("From")
		if fromField.IsValid() && fromField.Kind() == reflect.String {
			result.Choose.From = fromField.String()
		}
		weightsField := chooseField.FieldByName("Weights")
		if weightsField.IsValid() && weightsField.Kind() == reflect.Map {
			result.Choose.Weights = make(map[string]int)
			for _, key := range weightsField.MapKeys() {
				if key.Kind() == reflect.String {
					val := weightsField.MapIndex(key)
					if val.IsValid() && val.Kind() == reflect.Int {
						result.Choose.Weights[key.String()] = int(val.Int())
					}
				}
			}
		}
	}
	rawField := rv.FieldByName("Raw")
	if rawField.IsValid() && rawField.Kind() == reflect.String {
		result.Raw = rawField.String()
	}
	return result
}

// isEmptySimValue checks if a simValue is empty (neither choose.from
// nor a scalar Raw).
func isEmptySimValue(v any) bool {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		return true
	}
	chooseField := rv.FieldByName("Choose")
	if chooseField.IsValid() {
		if fromField := chooseField.FieldByName("From"); fromField.IsValid() && fromField.String() != "" {
			return false
		}
	}
	rawField := rv.FieldByName("Raw")
	return !rawField.IsValid() || rawField.String() == ""
}

// Generate produces one FluxMsg with a valid ISO8583 message.
func (g *Generator) Generate() (*fluxmsg.FluxMsg, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Select MTI from mix
	mti := g.selectMTI()
	if mti == "" {
		return nil, fmt.Errorf("no MTI selected from mix")
	}

	// Get message rules for this MTI
	msgRules := g.spec.Messages.Catalog[mti]
	if msgRules.Flow == "response" {
		// Responses are generated by sim_responder, not here
		return nil, nil
	}

	// Build field values for this MTI
	fieldValues := make(map[int]string)

	// Apply Set (highest precedence) - resolve aliases to field numbers
	setFields := g.resolveFieldMap(g.cfg.Set)
	g.logger.Debug("Set fields", "setFields", setFields, "cfg.Set", g.cfg.Set)

	// Apply per-MTI template
	templateFields := g.resolveFieldMap(g.cfg.Templates[mti])

	// Apply defaults
	defaultFields := g.resolveFieldMap(g.cfg.Defaults)

	// Determine which fields are mandatory/optional for this MTI
	requiredFields := g.determineFields(mti)

	// Ensure Set fields are included in output even if not in spec
	for fieldID := range setFields {
		found := false
		for _, f := range requiredFields {
			if f == fieldID {
				found = true
				break
			}
		}
		if !found {
			requiredFields = append(requiredFields, fieldID)
		}
	}

	// Generate values for each required field
	for _, fieldID := range requiredFields {
		// Skip if already set by Set
		if _, ok := setFields[fieldID]; ok {
			continue
		}

		// Check template
		if val, ok := templateFields[fieldID]; ok {
			fieldValues[fieldID] = g.expandMacro(val, fieldID, fieldValues)
			continue
		}

		// Check defaults
		if val, ok := defaultFields[fieldID]; ok {
			fieldValues[fieldID] = g.expandMacro(val, fieldID, fieldValues)
			continue
		}

		// Check spec simulation defaults
		if val, exists := g.spec.Simulation.Defaults[fieldID]; exists && !isEmptySimValue(val) {
			fieldValues[fieldID] = g.expandSimValue(toSimValue(val), fieldID, fieldValues)
			continue
		}

		// Check spec simulation template for this MTI
		if tmpl, exists := g.spec.Simulation.Templates[mti]; exists {
			if val, exists := tmpl[fieldID]; exists && !isEmptySimValue(val) {
				fieldValues[fieldID] = g.expandSimValue(toSimValue(val), fieldID, fieldValues)
				continue
			}
		}

		// Synthesize from field's value domain
		fieldValues[fieldID] = g.synthesizeField(fieldID, fieldValues)
	}

	// Apply Set values (override everything)
	for fieldID, val := range setFields {
		fieldValues[fieldID] = g.expandMacro(val, fieldID, fieldValues)
	}

	// Create FluxMsg. sim_source is a logic gear: it resolves field values
	// and hands them to the pipeline as data, exactly like codec_iso8583's
	// own decode output. It never touches the wire format itself; a
	// downstream codec_iso8583 gear packs the bytes, same as it would for a
	// real counterparty's traffic.
	fluxID, _ := g.idgen.NextFluxID()
	msg := &fluxmsg.FluxMsg{
		FluxID: fluxID,
		Data:   make(map[string]any),
		Metadata: map[string]string{
			"iso8583.mti": mti,
			"sim.seed":    strconv.FormatInt(g.cfg.Seed, 10),
		},
	}

	// Add field data for tracing and downstream logic. Keys go through Set
	// (nested maps for dotted keys): dot-aware readers (codec encode
	// gap-fill, when-evaluators) cannot see flat assignments. Skip empty
	// values: an ungenerated field must be absent, not present-but-empty.
	// An empty alias key (e.g. track2) is picked up downstream by
	// alias-prioritizing encoders and fails content validation there.
	for fieldID, val := range fieldValues {
		if val == "" {
			continue
		}
		key := fmt.Sprintf("iso8583.field.%d", fieldID)
		_ = msg.Set(key, val)
		if alias, ok := g.meta.Aliases[fieldID]; ok {
			_ = msg.Set(alias, val)
		}
	}

	return msg, nil
}

// selectMTI selects an MTI from the mix using weights.
func (g *Generator) selectMTI() string {
	mix := g.cfg.Mix
	if len(mix) == 0 {
		// Use spec's simulation mix
		for _, m := range g.spec.Simulation.Mix {
			mix = append(mix, MixEntry{Use: m.Use})
		}
	}
	if len(mix) == 0 {
		// Default to first request MTI in catalog
		for mti, msg := range g.spec.Messages.Catalog {
			if msg.Flow == "request" {
				return mti
			}
		}
		return ""
	}

	totalWeight := 0
	for _, m := range mix {
		totalWeight += m.Weight
	}
	if totalWeight == 0 {
		return mix[0].Use
	}

	r := g.rand.IntN(totalWeight)
	for _, m := range mix {
		r -= m.Weight
		if r < 0 {
			return m.Use
		}
	}
	return mix[len(mix)-1].Use
}

// determineFields returns the list of field IDs that should be present for the given MTI.
// Returns fields in sorted order for deterministic behavior.
func (g *Generator) determineFields(mti string) []int {
	var fields []int
	for fieldID, field := range g.spec.Fields {
		for _, rule := range field.Messages {
			for _, code := range rule.Codes() {
				if code == mti {
					// Check when condition
					if rule.When != "" {
						// Build a partial message to evaluate the condition
						_ = &partialMessage{
							mti:       mti,
							fieldVals: make(map[int]string),
						}
						// We can't fully evaluate without all fields, so skip conditional for now
						// In a full implementation, we'd evaluate the when expression
					}
					if rule.Usage == "mandatory" || rule.Usage == "optional" {
						fields = append(fields, fieldID)
					}
					break
				}
			}
		}
	}
	// Sort for deterministic field ordering
	sort.Ints(fields)
	return fields
}

// resolveFieldMap converts a map with string keys (field numbers or aliases) to field ID map.
func (g *Generator) resolveFieldMap(input map[string]string) map[int]string {
	result := make(map[int]string)
	for k, v := range input {
		fieldID := g.resolveFieldKey(k)
		if fieldID > 0 {
			result[fieldID] = v
		}
	}
	return result
}

// resolveFieldKey resolves a field key (number or alias) to field ID.
func (g *Generator) resolveFieldKey(key string) int {
	// Try as number first
	if id, err := strconv.Atoi(key); err == nil {
		return id
	}
	// Try as alias
	if id, ok := g.meta.IDByAlias[key]; ok {
		return id
	}
	// Try subfield alias
	for fieldID, subs := range g.meta.SubAliases {
		if _, ok := subs[key]; ok {
			return fieldID // Return parent field ID; subfield handling would need more work
		}
	}
	return 0
}

// expandMacro expands a macro string into a concrete value.
func (g *Generator) expandMacro(template string, fieldID int, currentVals map[int]string) string {
	// Handle macros: $PAN, $STAN, $RRN, $SEQ, $RAND, $UUID, $NOW, $ENUM, $INVALID, $AUTH
	if !strings.HasPrefix(template, "$") {
		return template
	}

	parts := strings.SplitN(template[1:], "(", 2)
	macro := parts[0]
	var args string
	if len(parts) > 1 {
		args = strings.TrimSuffix(parts[1], ")")
	}

	switch macro {
	case "PAN":
		return g.macroPAN(args)
	case "STAN":
		return g.macroSTAN()
	case "RRN":
		return g.macroRRN()
	case "SEQ":
		return g.macroSEQ(args)
	case "RAND":
		return g.macroRAND(args)
	case "UUID":
		return g.macroUUID()
	case "NOW":
		return g.macroNOW(fieldID)
	case "ENUM":
		return g.macroENUM(fieldID)
	case "INVALID":
		return g.macroINVALID(fieldID)
	case "AUTH":
		return g.macroAUTH()
	default:
		return template // Unknown macro, return as-is
	}
}

// expandSimValue expands a SimValue into a concrete value.
// Handles both enum references (choose.from = "enum_name") and macros (e.g., "$SEQ(1)").
func (g *Generator) expandSimValue(val SimValue, fieldID int, currentVals map[int]string) string {
	if val.Choose.From != "" {
		// If it looks like a macro (starts with $), expand it as a macro
		if strings.HasPrefix(val.Choose.From, "$") {
			return g.expandMacro(val.Choose.From, fieldID, currentVals)
		}
		// Otherwise treat as enum reference
		return g.chooseFromEnum(val.Choose.From, val.Choose.Weights)
	}
	if val.Raw != "" {
		// Scalar literal or macro ($SEQ, $RAND, $RRN, $NOW, plain
		// text like "000000"): expandMacro covers both shapes.
		return g.expandMacro(val.Raw, fieldID, currentVals)
	}
	return ""
}

// synthesizeField generates a value from the field's declared value domain.
func (g *Generator) synthesizeField(fieldID int, currentVals map[int]string) string {
	field, ok := g.spec.Fields[fieldID]
	if !ok {
		return ""
	}

	// Check for value set reference
	if field.ValuesRef != "" {
		if enum, ok := g.spec.Enums[field.ValuesRef]; ok && len(enum.Values) > 0 {
			return g.chooseFromEnum(field.ValuesRef, nil)
		}
	}

	// Check format kind
	switch field.Format.Kind {
	case "pan":
		return g.macroPAN("")
	case "amount":
		return g.macroRAND("1,999999")
	case "date", "time", "datetime":
		if s, ok := g.renderDeclared(field.Format.Layout); ok {
			return s
		}
		switch field.Format.Kind {
		case "date":
			return g.now().Format("0102") // MMDD
		case "time":
			return g.now().Format("150405") // hhmmss
		default:
			return g.now().Format("0102150405") // MMDDhhmmss
		}
	}

	// Default: empty
	return ""
}

func (g *Generator) chooseFromEnum(enumName string, weights map[string]int) string {
	enum, ok := g.spec.Enums[enumName]
	if !ok || len(enum.Values) == 0 {
		return ""
	}

	keys := make([]string, 0, len(enum.Values))
	for k := range enum.Values {
		keys = append(keys, k)
	}
	// Sort for deterministic behavior
	sort.Strings(keys)

	if weights != nil && len(weights) > 0 {
		total := 0
		for _, k := range keys {
			total += weights[k]
		}
		if total == 0 {
			total = len(keys)
		}
		r := g.rand.IntN(total)
		for _, k := range keys {
			w := weights[k]
			if w == 0 {
				w = 1
			}
			r -= w
			if r < 0 {
				return k
			}
		}
		return keys[len(keys)-1]
	}

	return keys[g.rand.IntN(len(keys))]
}

// Macro implementations

// macroUUID draws a UUID from the seeded generator, like every other macro, so that a
// run replays exactly. It has the layout of a version 4 UUID. A version 7 UUID carries
// the clock and cannot be reproduced.
func (g *Generator) macroUUID() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], g.rand.Uint64())
	binary.BigEndian.PutUint64(b[8:], g.rand.Uint64())
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // variant 10
	return uuid.UUID(b).String()
}

func (g *Generator) macroPAN(args string) string {
	prefix := "4111"
	length := 16
	parts := simmacro.Args(args)
	if len(parts) >= 1 && parts[0] != "" {
		prefix = parts[0]
	}
	if len(parts) >= 2 {
		// A length under two digits leaves no room for the check digit: keep the default.
		if l, err := strconv.Atoi(parts[1]); err == nil && l >= 2 {
			length = l
		}
	}

	// Generate PAN with Luhn check digit
	body := prefix
	for len(body) < length-1 {
		body += fmt.Sprintf("%d", g.rand.IntN(10))
	}
	body = body[:length-1]

	// Calculate Luhn check digit
	sum := 0
	alt := true
	for i := len(body) - 1; i >= 0; i-- {
		d := int(body[i] - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	checkDigit := (10 - (sum % 10)) % 10
	return body + strconv.Itoa(checkDigit)
}

func (g *Generator) macroSTAN() string {
	g.stanCounter++
	return fmt.Sprintf("%06d", g.stanCounter%1000000)
}

func (g *Generator) macroRRN() string {
	g.rrnCounter++
	// RRN format: YYMMDD + 6-digit sequence
	return g.now().Format("060102") + fmt.Sprintf("%06d", g.rrnCounter%1000000)
}

func (g *Generator) macroSEQ(args string) string {
	name := "default"
	start := uint64(1)
	if parts := simmacro.Args(args); len(parts) > 0 {
		if parts[0] != "" {
			name = parts[0]
		}
		if len(parts) >= 2 {
			if s, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
				start = s
			}
		}
	}
	counter := g.seqCounters[name]
	if counter == 0 {
		counter = start
	}
	g.seqCounters[name] = counter + 1
	return fmt.Sprintf("%d", counter)
}

func (g *Generator) macroRAND(args string) string {
	min, max := 0, 999999
	parts := simmacro.Args(args)
	if len(parts) >= 1 {
		if v, err := strconv.Atoi(parts[0]); err == nil {
			min = v
		}
	}
	if len(parts) >= 2 {
		if v, err := strconv.Atoi(parts[1]); err == nil {
			max = v
		}
	}
	if max < min {
		max = min
	}
	return strconv.Itoa(min + g.rand.IntN(max-min+1))
}

func (g *Generator) macroNOW(fieldID int) string {
	field, ok := g.spec.Fields[fieldID]
	if !ok {
		return g.now().Format("0102150405")
	}
	// The layout the spec declares for the field wins: DE 14 is YYMM and DE 13 is MMDD.
	if s, ok := simmacro.Render(g.now(), field.Format.Layout); ok {
		return s
	}
	switch field.Format.Kind {
	case "date":
		// Use standard date layouts for ISO8583
		// Expiry dates (DE 14) use YYMM, others use MMDD
		return g.now().Format("0102") // MMDD
	case "time":
		return g.now().Format("150405") // HHMMSS
	case "datetime":
		return g.now().Format("0102150405") // MMDDhhmmss
	default:
		return g.now().Format("0102150405")
	}
}

func (g *Generator) macroENUM(fieldID int) string {
	field, ok := g.spec.Fields[fieldID]
	if !ok || field.ValuesRef == "" {
		return ""
	}
	return g.chooseFromEnum(field.ValuesRef, nil)
}

func (g *Generator) macroINVALID(fieldID int) string {
	field, ok := g.spec.Fields[fieldID]
	if !ok || field.ValuesRef == "" {
		return "INVALID"
	}
	enum, ok := g.spec.Enums[field.ValuesRef]
	if !ok || !enum.Closed || len(enum.Values) == 0 {
		return "INVALID"
	}
	// Find a value not in the enum
	for i := 0; i < 1000; i++ {
		candidate := fmt.Sprintf("%02d", i)
		if _, exists := enum.Values[candidate]; !exists {
			return candidate
		}
	}
	return "99" // Fallback
}

func (g *Generator) macroAUTH() string {
	// Generate 6-character alphanumeric auth code
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 6)
	for i := range result {
		result[i] = chars[g.rand.IntN(len(chars))]
	}
	return string(result)
}

// partialMessage implements sdl.Subject for when expression evaluation on partial messages.
type partialMessage struct {
	mti       string
	fieldVals map[int]string
}

func (p *partialMessage) MTI() string { return p.mti }
func (p *partialMessage) Field(de int, subs []string) (string, bool) {
	val, ok := p.fieldVals[de]
	return val, ok
}

// zoneOf resolves the timezone setting. Init has already refused one that does not
// resolve, so a failure here falls back to the machine's zone.
func zoneOf(name string) *time.Location {
	loc, err := simmacro.Location(name)
	if err != nil {
		return time.Local
	}
	return loc
}

// now is the instant $NOW and $RRN render, in the configured zone.
func (g *Generator) now() time.Time {
	t := time.Now()
	if g.clock != nil {
		t = g.clock()
	}
	if g.loc != nil {
		return t.In(g.loc)
	}
	return t
}

// renderDeclared writes the current time in the layout a spec declares for a field. An
// expiry date (YYMM) is generated ExpiryMonths ahead, so that the card it belongs to has
// not expired: DE 14 is the expiry of the card, not the date of the message.
// ExpiredPercent of them are generated already expired instead.
func (g *Generator) renderDeclared(layout string) (string, bool) {
	t := g.now()
	if simmacro.IsExpiry(layout) {
		if g.cfg.ExpiredPercent > 0 && g.rand.IntN(100) < g.cfg.ExpiredPercent {
			// An expired card: one to ExpiryMonths months back, the window being at least one.
			back := 1 + g.rand.IntN(max(g.cfg.ExpiryMonths, 1))
			t = simmacro.MonthsAhead(t, -back)
		} else {
			t = simmacro.MonthsAhead(t, g.cfg.ExpiryMonths)
		}
	}
	return simmacro.Render(t, layout)
}
