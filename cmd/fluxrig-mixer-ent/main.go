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

// fluxrig-mixer-ent is the fluxrig Mixer with the enterprise gears in its gear
// catalog. It is the open-source Mixer's own command line plus one blank import.
//
// This is the only place the licence file is checked: a Rack cannot
// initialize without first enrolling through a Mixer, so checking here
// covers every deployment, and checking on every Rack too would just repeat
// the same log line once per edge node. See pkg/license and
// cmd/fluxrig-ent/main.go.
package main

import (
	"os"

	"github.com/jaab-tech/fluxrig/cmd/fluxrig-mixer/mixercmd"
	"github.com/jaab-tech/fluxrig/pkg/version"

	"github.com/jaab-tech/fluxrig-ent/pkg/license"

	// Registers sim_source and sim_responder with the engine.
	_ "github.com/jaab-tech/fluxrig-ent/pkg/gears"
)

func main() {
	license.WarnAtStartup(nil, version.BuildDate)
	os.Exit(mixercmd.Run(os.Args[1:]))
}
