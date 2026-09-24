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

// Config holds the configuration for the sim_source gear.
type Config struct {
	// Spec is the spec reference (store URN or file path).
	// Example: "iso8583-v87-ascii:v2.2.0"
	Spec string `json:"spec" yaml:"spec"`

	// Seed is the random seed for deterministic generation.
	// Mandatory for reproducibility.
	Seed int64 `json:"seed" yaml:"seed"`

	// Trigger controls when the simulation starts.
	// Values: "on_load" (default), "on_control", "on_schedule"
	Trigger string `json:"trigger" yaml:"trigger"`

	// Schedule is an RFC3339 timestamp or cron expression for on_schedule trigger.
	Schedule string `json:"schedule" yaml:"schedule"`

	// ExpiryMonths is how far ahead an expiry date (a field whose layout is YYMM, DE 14)
	// is generated, in months from the current one. The default is 24.
	ExpiryMonths int `json:"expiry_months" yaml:"expiry_months"`

	// ExpiredPercent is the share of generated expiry dates, from 0 to 100, that are
	// already expired: between one and ExpiryMonths months before the current one. They
	// are drawn from the seed, so a run replays exactly. The default is 0.
	ExpiredPercent int `json:"expired_percent" yaml:"expired_percent"`

	// Timezone is the clock that $NOW and $RRN render: "local" (the zone of the
	// machine, the default), "GMT" or "UTC", or the name of a zone such as
	// "America/Montevideo".
	Timezone string `json:"timezone" yaml:"timezone"`

	// Rate configures the emission rate shaping.
	Rate RateConfig `json:"rate" yaml:"rate"`

	// Mix overrides the spec's simulation mix.
	Mix []MixEntry `json:"mix" yaml:"mix"`

	// Defaults overrides the spec's simulation defaults.
	// Keys can be field numbers ("2") or aliases ("card.pan").
	Defaults map[string]string `json:"defaults" yaml:"defaults"`

	// Templates overrides per-MTI field templates from the spec.
	// Map: MTI -> (field -> macro/literal).
	Templates map[string]map[string]string `json:"templates" yaml:"templates"`

	// Set pins specific fields to fixed values (highest precedence).
	// Keys can be field numbers or aliases.
	Set map[string]string `json:"set" yaml:"set"`
}

// RateConfig defines the rate shaping parameters.
type RateConfig struct {
	// Shape: "constant" or "ramp". "poisson" and "spike" are roadmap and fail fast.
	Shape string `json:"shape" yaml:"shape"`

	// TPS for constant shape.
	TPS float64 `json:"tps" yaml:"tps"`

	// From/To/Over for ramp shape.
	From float64 `json:"from" yaml:"from"`
	To   float64 `json:"to" yaml:"to"`
	Over string  `json:"over" yaml:"over"`

	// Duration for the entire run.
	Duration string `json:"duration" yaml:"duration"`
}

// MixEntry defines one entry in the traffic mix.
type MixEntry struct {
	Use    string `json:"use" yaml:"use"`
	Weight int    `json:"weight" yaml:"weight"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Trigger:      "on_load",
		ExpiryMonths: 24,
		Rate: RateConfig{
			Shape:    "constant",
			TPS:      100,
			Duration: "60s",
		},
	}
}
