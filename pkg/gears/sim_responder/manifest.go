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

import "github.com/jaab-tech/fluxrig/pkg/sdk"

// Manifest returns the gear manifest.
func (g *Gear) Manifest() sdk.Manifest {
	return sdk.Manifest{
		Type:     "sim_responder",
		Category: sdk.CategoryLogic,
		Status:   sdk.StatusStable,
		Summary:  "Responds to ISO8583 requests as an authorizer simulator with configurable rules and latency.",
		DocSlug:  "sim_responder",
		Terminus: sdk.TerminusTransparent,
		Ports: []sdk.Port{
			{Name: "in", Dir: sdk.PortIn, Role: "request", Summary: "Incoming ISO8583 request from the scheme."},
			{Name: "out", Dir: sdk.PortOut, Role: "response", Summary: "Outgoing ISO8583 response to the scheme."},
		},
		ConfigSchema: configSchema,
	}
}

const configSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "title": "sim_responder Configuration",
  "required": ["spec"],
  "properties": {
    "spec": {
      "type": "string",
      "description": "Spec reference (store URN or file path). Example: iso8583-v87-ascii:v2.2.0"
    },
    "trigger": {
      "type": "string",
      "enum": ["on_load", "on_control"],
      "default": "on_load",
      "description": "on_load processes immediately; on_control drops requests until sim.start arrives."
    },
    "delay": {
      "type": "string",
      "default": "50ms",
      "description": "Base latency to simulate scheme processing time."
    },
    "timezone": {
      "type": "string",
      "default": "local",
      "description": "The clock that $NOW and $RRN render: local, GMT (or UTC), or a zone name such as America/Montevideo."
    },
    "default": {
      "type": "object",
      "additionalProperties": { "type": "string" },
      "description": "Default response field values. Keys can be field numbers (\"39\") or aliases (\"resp_code\")."
    },
    "rules": {
      "type": "array",
      "description": "Conditional response rules, evaluated in order. Each has name, when, set, and an optional per-rule delay.",
      "items": {
        "type": "object",
        "required": ["name", "when", "set"],
        "properties": {
          "name": { "type": "string" },
          "when": { "type": "string", "description": "When expression evaluated against request" },
          "set": {
            "type": "object",
            "additionalProperties": { "type": "string" },
            "description": "Response field overrides when rule matches."
          },
          "delay": { "type": "string", "description": "Per-rule delay override" }
        }
      }
    }
  }
}`
