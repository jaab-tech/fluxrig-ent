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

// fluxrig-ent is the fluxrig Rack with the enterprise gears built in. It is the
// open-source Rack's own entry point plus one blank import.
//
// This binary does not check for a licence file: a Rack cannot initialize
// without first enrolling through a Mixer (see pkg/pki's Passport flow), and
// a Rack resuming standalone afterward is resuming a deployment a Mixer
// already checked at least once. See cmd/fluxrig-mixer-ent and pkg/license.
package main

import (
	"fmt"
	"os"

	"github.com/jaab-tech/fluxrig/cmd/fluxrig/commands"

	// Registers sim_source and sim_responder with the engine.
	_ "github.com/jaab-tech/fluxrig-ent/pkg/gears"
)

func main() {
	if err := commands.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
