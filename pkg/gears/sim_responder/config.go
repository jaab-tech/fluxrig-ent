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

import "time"

// Config holds the configuration for the sim_responder gear.
//
// The responder is a pure logic gear: requests arrive decoded on the "in"
// port (io gear → codec gear) and fields-only responses leave on "out"
// (codec gear → io gear). There is no listen address — transport is the io
// gear's job.
type Config struct {
	// Spec is the spec reference (store URN or file path).
	Spec string `json:"spec" yaml:"spec"`

	// Trigger controls when the responder processes requests.
	// Values: "on_load" (default, process immediately),
	// "on_control" (drop requests until sim.start arrives).
	Trigger string `json:"trigger" yaml:"trigger"`

	// Delay is the base latency to simulate scheme processing time.
	Delay string `json:"delay" yaml:"delay"`

	// Timezone is the clock that $NOW and $RRN render: "local" (the zone of the
	// machine, the default), "GMT" or "UTC", or the name of a zone such as
	// "America/Montevideo".
	Timezone string `json:"timezone" yaml:"timezone"`

	// Default response field values.
	// Keys can be field numbers ("39") or aliases ("resp_code").
	Default map[string]string `json:"default" yaml:"default"`

	// Rules define conditional response logic.
	Rules []ResponderRule `json:"rules" yaml:"rules"`
}

// ResponderRule defines a conditional response rule.
type ResponderRule struct {
	// Name identifies the rule for logging.
	Name string `json:"name" yaml:"name"`

	// When is a when-expression evaluated against the request message.
	When string `json:"when" yaml:"when"`

	// Set defines response field overrides when the rule matches.
	// Keys can be field numbers or aliases.
	Set map[string]string `json:"set" yaml:"set"`

	// Delay overrides the base delay for this rule.
	Delay string `json:"delay,omitempty" yaml:"delay,omitempty"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Trigger: "on_load",
		Delay:   "50ms",
		Default: map[string]string{
			"39": "00", // approved
		},
	}
}

// ParseDelay parses a delay string into a time.Duration.
func ParseDelay(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
