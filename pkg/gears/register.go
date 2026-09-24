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

// Package gears registers the enterprise gears with the fluxrig engine.
//
// Importing this package, usually with a blank import, is enough: init hands
// Register to the engine, and every gear factory the binary builds afterwards
// knows sim_source and sim_responder. The engine never imports this module.
package gears

import (
	ossgears "github.com/jaab-tech/fluxrig/pkg/gears"
	"github.com/jaab-tech/fluxrig/pkg/sdk"

	"github.com/jaab-tech/fluxrig-ent/pkg/gears/sim_responder"
	"github.com/jaab-tech/fluxrig-ent/pkg/gears/sim_source"
)

func init() {
	ossgears.RegisterExtension(Register)
}

// Register adds the enterprise gears to f.
func Register(f *ossgears.Factory) {
	f.Register("sim_source", func() sdk.NativeGear { return &sim_source.Gear{} })
	f.Register("sim_responder", func() sdk.NativeGear { return &sim_responder.Gear{} })
}
