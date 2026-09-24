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
	"math/rand/v2"
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
)

// Responder handles the response logic for incoming requests.
//
// It is transport-agnostic: it reads a decoded request (MTI metadata plus
// iso8583.field.N Data keys, exactly what the codec gear emits) and returns
// a fields-only response. Packing the response bytes is the downstream codec
// gear's job — the responder never touches the wire.
type Responder struct {
	spec   *sdl.Spec
	meta   *sdl.FieldMeta
	cfg    Config
	idgen  sdk.IDGenerator
	logger *slog.Logger

	// mu guards rand and seqCounters: two wires into the gear run on two goroutines, and
	// sim.reset replaces the counters from a third.
	mu          sync.Mutex
	rand        *rand.Rand
	seqCounters map[string]uint64

	loc   *time.Location   // the zone $NOW and $RRN render in
	clock func() time.Time // replaced by tests; nil reads the machine's clock

	baseDelay time.Duration
}

// NewResponder creates a new responder.
func NewResponder(spec *sdl.Spec, meta *sdl.FieldMeta, cfg Config, idgen sdk.IDGenerator) *Responder {
	src := rand.NewPCG(uint64(time.Now().UnixNano()), 0)
	baseDelay, _ := ParseDelay(cfg.Delay)
	// Validate refuses a timezone that does not resolve, so a failure here falls back to
	// the machine's zone.
	loc, err := simmacro.Location(cfg.Timezone)
	if err != nil {
		loc = time.Local
	}
	return &Responder{
		loc:         loc,
		spec:        spec,
		meta:        meta,
		cfg:         cfg,
		idgen:       idgen,
		rand:        rand.New(src),
		seqCounters: make(map[string]uint64),
		baseDelay:   baseDelay,
	}
}

func (r *Responder) SetLogger(logger *slog.Logger) {
	r.logger = logger
}

// now is the instant $NOW and $RRN render, in the configured zone.
func (r *Responder) now() time.Time {
	t := time.Now()
	if r.clock != nil {
		t = r.clock()
	}
	if r.loc != nil {
		return t.In(r.loc)
	}
	return t
}

// ResetCounters clears the macro sequence counters ($SEQ, $RRN, and the $STAN that
// is generated when the request carries none).
func (r *Responder) ResetCounters() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seqCounters = make(map[string]uint64)
}

// knownMacros are the macros expandMacro implements.
var knownMacros = map[string]bool{
	"AUTH": true, "RRN": true, "STAN": true, "SEQ": true,
	"RAND": true, "UUID": true, "NOW": true, "ENUM": true,
}

// macroName returns the name of a macro template such as $SEQ(name, 5), or "" when the
// template is a literal.
func macroName(template string) string {
	if !strings.HasPrefix(template, "$") {
		return ""
	}
	name, _, _ := strings.Cut(template[1:], "(")
	return name
}

// Validate checks the configuration the way a request would use it, so that a scenario
// with a mistake fails when it is applied and not by answering wrongly: a delay that is
// not a duration, a rule that lacks a name, a when or a set, a when expression that does
// not parse, a field key that names no field, and a macro that does not exist.
func (r *Responder) Validate() error {
	if _, err := simmacro.Location(r.cfg.Timezone); err != nil {
		return err
	}
	if err := validateDelay(r.cfg.Delay); err != nil {
		return fmt.Errorf("delay: %w", err)
	}
	if err := r.validateSet(r.cfg.Default); err != nil {
		return fmt.Errorf("default: %w", err)
	}
	for i, rule := range r.cfg.Rules {
		label := fmt.Sprintf("rule %d (%q)", i+1, rule.Name)
		if rule.Name == "" || rule.When == "" || len(rule.Set) == 0 {
			return fmt.Errorf("%s: name, when and set are all required", label)
		}
		if _, err := sdl.ParseWhen(rule.When); err != nil {
			return fmt.Errorf("%s: invalid when %q: %w", label, rule.When, err)
		}
		if err := validateDelay(rule.Delay); err != nil {
			return fmt.Errorf("%s: delay: %w", label, err)
		}
		if err := r.validateSet(rule.Set); err != nil {
			return fmt.Errorf("%s: set: %w", label, err)
		}
	}
	return nil
}

func validateDelay(s string) error {
	d, err := ParseDelay(s)
	if err != nil {
		return fmt.Errorf("%q is not a duration such as 50ms or 1s", s)
	}
	if d < 0 {
		return fmt.Errorf("%q is negative", s)
	}
	return nil
}

// validateSet checks the keys and the macros of a map of response fields.
func (r *Responder) validateSet(set map[string]string) error {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if id, err := strconv.Atoi(k); err == nil {
			if id <= 0 {
				return fmt.Errorf("field %q: a field number must be positive", k)
			}
		} else if _, ok := r.meta.IDByAlias[k]; !ok {
			for id, subs := range r.meta.SubAliases {
				if _, isSub := subs[k]; isSub {
					return fmt.Errorf("%q names a subfield of field %d: set the whole field", k, id)
				}
			}
			return fmt.Errorf("%q is neither a field number nor an alias of the spec", k)
		}
		if name := macroName(set[k]); strings.HasPrefix(set[k], "$") {
			if !knownMacros[name] {
				return fmt.Errorf("field %q: unknown macro %q", k, "$"+name)
			}
			if err := validateMacroArgs(name, set[k]); err != nil {
				return fmt.Errorf("field %q: %w", k, err)
			}
		}
	}
	return nil
}

// validateMacroArgs checks the arguments of the macros that take them, which would
// otherwise fall back to their defaults without a word.
func validateMacroArgs(name, template string) error {
	_, args, _ := strings.Cut(template, "(")
	parts := simmacro.Args(strings.TrimSuffix(args, ")"))
	switch name {
	case "RAND":
		if len(parts) > 2 {
			return fmt.Errorf("$RAND takes at most two arguments, min and max")
		}
		for _, p := range parts {
			if _, err := strconv.Atoi(p); err != nil {
				return fmt.Errorf("$RAND: %q is not an integer", p)
			}
		}
	case "SEQ":
		if len(parts) > 2 {
			return fmt.Errorf("$SEQ takes at most two arguments, name and start")
		}
		if len(parts) == 2 {
			if _, err := strconv.ParseUint(parts[1], 10, 64); err != nil {
				return fmt.Errorf("$SEQ: the start %q is not a non-negative integer", parts[1])
			}
		}
	}
	return nil
}

// ProcessRequest processes an incoming request and generates a response.
func (r *Responder) ProcessRequest(ctx context.Context, req *fluxmsg.FluxMsg) (*fluxmsg.FluxMsg, error) {
	// Extract request MTI
	reqMTI := req.Metadata["iso8583.mti"]
	if reqMTI == "" {
		return nil, fmt.Errorf("request missing MTI")
	}

	// Find response MTI from spec catalog
	reqMsg, ok := r.spec.Messages.Catalog[reqMTI]
	if !ok {
		return nil, fmt.Errorf("request MTI %q not in catalog", reqMTI)
	}

	respMTI := reqMsg.PairsWith
	if respMTI == "" {
		return nil, fmt.Errorf("request MTI %q has no pairs_with", reqMTI)
	}

	respMsg, ok := r.spec.Messages.Catalog[respMTI]
	if !ok {
		return nil, fmt.Errorf("response MTI %q not in catalog", respMTI)
	}

	// Build response fields
	respFields := make(map[int]string)

	// 1. Echo: copy fields from request that are valid in response
	if reqMsg.Flow == "request" && respMsg.Flow == "response" {
		r.echoValidFields(req, respFields, respMTI)
	}

	// 2. Apply defaults
	for k, v := range r.cfg.Default {
		fieldID := r.resolveFieldKey(k)
		if fieldID > 0 {
			respFields[fieldID] = r.expandMacro(v, fieldID, req, respFields)
		}
	}

	// 3. Evaluate rules in order (resolve effective delay)
	delay := r.baseDelay
	for _, rule := range r.cfg.Rules {
		if r.evalWhen(rule.When, req) {
			r.logger.Debug("Rule matched", "rule", rule.Name, "when", rule.When)
			for k, v := range rule.Set {
				fieldID := r.resolveFieldKey(k)
				if fieldID > 0 {
					respFields[fieldID] = r.expandMacro(v, fieldID, req, respFields)
				}
			}
			// Apply rule-specific delay (overrides base)
			if rule.Delay != "" {
				if d, err := ParseDelay(rule.Delay); err == nil {
					delay = d
				}
			}
			break // First matching rule wins
		}
	}

	// Apply the resolved delay once
	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	// Build the fields-only response. The downstream codec gear packs the
	// bytes from the MTI metadata and the iso8583.field.N Data keys; the
	// responder never touches the wire format.
	fluxID, _ := r.idgen.NextFluxID()

	// Get correlation ID (STAN) from request Data
	correlationID := ""
	if val, ok := getField(req, "iso8583.field.11"); ok {
		if s, ok := asString(val); ok {
			correlationID = s
		}
	}

	resp := &fluxmsg.FluxMsg{
		FluxID: fluxID,
		Metadata: map[string]string{
			"iso8583.mti":        respMTI,
			"iso8583.mti_class":  respMTI[:2],
			"sim.response_to":    reqMTI,
			"sim.correlation_id": correlationID,
		},
		Data: make(map[string]any),
	}

	// Propagate transport routing metadata so the reply returns on the
	// connection the request arrived on (io gear server mode routes by
	// conn.id). Without this the response has no way home.
	if connID, ok := req.Metadata["conn.id"]; ok {
		resp.Metadata["conn.id"] = connID
	}

	// Populate Data for the downstream codec, which reads via dot-aware Get:
	// field keys nest (Set, not flat assignment), aliases land flat.
	for fieldID, val := range respFields {
		key := fmt.Sprintf("iso8583.field.%d", fieldID)
		_ = resp.Set(key, val)
		if alias, ok := r.meta.Aliases[fieldID]; ok {
			_ = resp.Set(alias, val)
		}
	}

	return resp, nil
}

// echoValidFields copies request fields that are valid in the response MTI.
func (r *Responder) echoValidFields(req *fluxmsg.FluxMsg, respFields map[int]string, respMTI string) {
	if _, ok := r.spec.Messages.Catalog[respMTI]; !ok {
		return
	}

	for fieldID, field := range r.spec.Fields {
		// Check if field is valid in response MTI
		validInResp := false
		for _, rule := range field.Messages {
			for _, code := range rule.Codes() {
				if code == respMTI {
					if rule.ResponseValue == "echo" || rule.ResponseValue == "modified" {
						validInResp = true
					}
					break
				}
			}
			if validInResp {
				break
			}
		}

		if !validInResp {
			continue
		}

		// Get value from request (dot-aware: the codec nests dotted keys)
		key := fmt.Sprintf("iso8583.field.%d", fieldID)
		if val, ok := getField(req, key); ok {
			if s, ok := asString(val); ok {
				respFields[fieldID] = s
			}
		}
	}
}

// getField reads a dotted field key from a request, accepting both shapes:
// nested maps (what the codec gear stores via FluxMsg.Set) and legacy flat
// keys (direct map assignment, as the old TCP front-end built them).
func getField(req *fluxmsg.FluxMsg, key string) (any, bool) {
	// Dot-aware lookup first (codec-shaped nested maps). Method call, not
	// getField: this body must not recurse into itself.
	if val, ok := req.Get(key); ok {
		return val, true
	}
	val, ok := req.Data[key]
	return val, ok
}

// asString reads a codec-shaped value: non-UTF8 fields arrive as []byte
// rather than string (see storable in the codec gear).
func asString(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, true
	case []byte:
		return string(s), true
	default:
		return "", false
	}
}

// evalWhen evaluates a when expression against the request.
func (r *Responder) evalWhen(expr string, req *fluxmsg.FluxMsg) bool {
	if expr == "" {
		return false
	}

	// Parse and evaluate when expression
	// For now, use the sdl when evaluator
	whenExpr, err := sdl.ParseWhen(expr)
	if err != nil {
		r.logger.Warn("Failed to parse when expression", "expr", expr, "error", err)
		return false
	}

	// Bind kinds from spec
	whenExpr.BindKinds(func(ref sdl.FieldRef) string {
		field, ok := r.spec.Fields[ref.DE]
		if !ok {
			return ""
		}
		if len(ref.Subs) > 0 {
			return ""
		}
		return field.Format.Kind
	})

	subject := &requestSubject{req: req, meta: r.meta}
	return whenExpr.Eval(subject)
}

// requestSubject implements sdl.Subject for when expression evaluation.
type requestSubject struct {
	req  *fluxmsg.FluxMsg
	meta *sdl.FieldMeta
}

func (s *requestSubject) MTI() string {
	return s.req.Metadata["iso8583.mti"]
}

func (s *requestSubject) Field(de int, subs []string) (string, bool) {
	key := fmt.Sprintf("iso8583.field.%d", de)
	if val, ok := getField(s.req, key); ok {
		if str, ok := asString(val); ok {
			return str, true
		}
	}
	// Try alias (single-part keys land flat, but Get resolves those too)
	if alias, ok := s.meta.Aliases[de]; ok {
		if val, ok := getField(s.req, alias); ok {
			if str, ok := asString(val); ok {
				return str, true
			}
		}
	}
	return "", false
}

// resolveFieldKey resolves a field key (number or alias) to field ID.
func (r *Responder) resolveFieldKey(key string) int {
	if id, err := strconv.Atoi(key); err == nil {
		return id
	}
	if id, ok := r.meta.IDByAlias[key]; ok {
		return id
	}
	for fieldID, subs := range r.meta.SubAliases {
		if _, ok := subs[key]; ok {
			return fieldID
		}
	}
	return 0
}

// expandMacro expands a macro string into a concrete value.
func (r *Responder) expandMacro(template string, fieldID int, req *fluxmsg.FluxMsg, respFields map[int]string) string {
	if !strings.HasPrefix(template, "$") {
		return template
	}

	parts := strings.SplitN(template[1:], "(", 2)
	macro := parts[0]
	var args string
	if len(parts) > 1 {
		args = strings.TrimSuffix(parts[1], ")")
	}

	// The macros draw from r.rand and move the counters: one at a time.
	r.mu.Lock()
	defer r.mu.Unlock()

	switch macro {
	case "AUTH":
		return r.macroAUTH()
	case "RRN":
		return r.macroRRN()
	case "STAN":
		return r.macroSTAN(req)
	case "SEQ":
		return r.macroSEQ(args)
	case "RAND":
		return r.macroRAND(args)
	case "UUID":
		return r.macroUUID()
	case "NOW":
		return r.macroNOW(fieldID)
	case "ENUM":
		return r.macroENUM(fieldID)
	default:
		return template
	}
}

// macroUUID is a UUID v7. It falls back to a random UUID only if the clock or the
// entropy source fails.
func (r *Responder) macroUUID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.New().String()
}

func (r *Responder) macroAUTH() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, 6)
	for i := range result {
		result[i] = chars[r.rand.IntN(len(chars))]
	}
	return string(result)
}

func (r *Responder) macroRRN() string {
	r.seqCounters["rrn"]++
	return r.now().Format("060102") + fmt.Sprintf("%06d", r.seqCounters["rrn"]%1000000)
}

func (r *Responder) macroSTAN(req *fluxmsg.FluxMsg) string {
	// Echo STAN from request
	if val, ok := getField(req, "iso8583.field.11"); ok {
		if s, ok := asString(val); ok {
			return s
		}
	}
	if val, ok := getField(req, "stan"); ok {
		if s, ok := asString(val); ok {
			return s
		}
	}
	r.seqCounters["stan"]++
	return fmt.Sprintf("%06d", r.seqCounters["stan"]%1000000)
}

func (r *Responder) macroSEQ(args string) string {
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
	counter := r.seqCounters[name]
	if counter == 0 {
		counter = start
	}
	r.seqCounters[name] = counter + 1
	return fmt.Sprintf("%d", counter)
}

func (r *Responder) macroRAND(args string) string {
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
	return strconv.Itoa(min + r.rand.IntN(max-min+1))
}

func (r *Responder) macroNOW(fieldID int) string {
	field, ok := r.spec.Fields[fieldID]
	if !ok {
		return r.now().Format("0102150405")
	}
	// The layout the spec declares for the field wins: DE 14 is YYMM and DE 13 is MMDD.
	if s, ok := simmacro.Render(r.now(), field.Format.Layout); ok {
		return s
	}
	switch field.Format.Kind {
	case "date":
		return r.now().Format("0102") // MMDD
	case "time":
		return r.now().Format("150405") // HHMMSS
	case "datetime":
		return r.now().Format("0102150405") // MMDDhhmmss
	default:
		return r.now().Format("0102150405")
	}
}

func (r *Responder) macroENUM(fieldID int) string {
	field, ok := r.spec.Fields[fieldID]
	if !ok || field.ValuesRef == "" {
		return ""
	}
	enum, ok := r.spec.Enums[field.ValuesRef]
	if !ok || len(enum.Values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(enum.Values))
	for k := range enum.Values {
		keys = append(keys, k)
	}
	return keys[r.rand.IntN(len(keys))]
}
