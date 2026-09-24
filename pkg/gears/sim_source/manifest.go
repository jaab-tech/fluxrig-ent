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

import "github.com/jaab-tech/fluxrig/pkg/sdk"

// Manifest returns the gear manifest.
func (g *Gear) Manifest() sdk.Manifest {
	return sdk.Manifest{
		Type:     "sim_source",
		Category: sdk.CategoryLogic,
		Status:   sdk.StatusStable,
		Summary:  "Generates ISO8583 traffic from a spec's simulation section with deterministic macros and configurable rate shaping.",
		DocSlug:  "sim_source",
		Terminus: sdk.TerminusIO,
		Ports: []sdk.Port{
			{Name: "out", Dir: sdk.PortOut, Role: "egress", Summary: "Generated ISO8583 messages emitted into the pipeline."},
		},
		ConfigSchema: configSchema,
	}
}

// configSchema is the JSON schema for the gear configuration.
const configSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "title": "sim_source Configuration",
  "required": ["spec", "seed"],
  "properties": {
    "spec": {
      "type": "string",
      "description": "Spec reference (store URN or file path). Example: iso8583-v87-ascii:v2.2.0"
    },
    "seed": {
      "type": "integer",
      "description": "Random seed for deterministic generation. Mandatory for reproducibility."
    },
    "trigger": {
      "type": "string",
      "enum": ["on_load", "on_control", "on_schedule"],
      "default": "on_load",
      "description": "When to start the simulation. on_schedule is roadmap and fails fast at apply time."
    },
    "schedule": {
      "type": "string",
      "description": "Reserved for the on_schedule trigger; parsed but unused today."
    },
    "expiry_months": {
      "type": "integer",
      "minimum": 0,
      "default": 24,
      "description": "How far ahead an expiry date (a field whose layout is YYMM, DE 14) is generated, in months from the current one."
    },
    "expired_percent": {
      "type": "integer",
      "minimum": 0,
      "maximum": 100,
      "default": 0,
      "description": "The share of generated expiry dates, from 0 to 100, that are already expired: between one and expiry_months months before the current one."
    },
    "timezone": {
      "type": "string",
      "default": "local",
      "description": "The clock that $NOW and $RRN render: local, GMT (or UTC), or a zone name such as America/Montevideo."
    },
    "rate": {
      "type": "object",
      "description": "Rate shape: shape (constant, ramp, poisson [Roadmap], spike [Roadmap]), tps, from, to, over, duration.",
      "properties": {
        "shape": {
          "type": "string",
          "enum": ["constant", "ramp", "poisson", "spike"],
          "default": "constant",
          "description": "poisson and spike are roadmap and fail fast at apply time."
        },
        "tps": { "type": "number", "default": 100 },
        "from": { "type": "number" },
        "to": { "type": "number" },
        "over": { "type": "string", "description": "Duration string for ramp (e.g., 5m)" },
        "duration": { "type": "string", "default": "60s", "description": "Total run duration" }
      }
    },
    "mix": {
      "type": "array",
      "description": "Traffic mix: a list of {use, weight} entries overriding the spec's simulation.mix.",
      "items": {
        "type": "object",
        "required": ["use", "weight"],
        "properties": {
          "use": { "type": "string" },
          "weight": { "type": "integer", "minimum": 1 }
        }
      }
    },
    "defaults": {
      "type": "object",
      "additionalProperties": { "type": "string" },
      "description": "Field defaults. Keys can be field numbers (\"2\") or aliases (\"card.pan\")."
    },
    "templates": {
      "type": "object",
      "additionalProperties": {
        "type": "object",
        "additionalProperties": { "type": "string" }
      },
      "description": "Per-MTI field templates. Map: MTI -> (field -> macro/literal)."
    },
    "set": {
      "type": "object",
      "additionalProperties": { "type": "string" },
      "description": "Pinned field values (highest precedence). Keys can be field numbers or aliases."
    }
  }
}`
