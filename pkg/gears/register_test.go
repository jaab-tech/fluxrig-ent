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

package gears

import (
	"slices"
	"testing"

	ossgears "github.com/jaab-tech/fluxrig/pkg/gears"
)

// These tests build the engine's real Factory, not a copy: what they prove is
// that importing this package is all a binary has to do.

func TestImportingThePackageRegistersTheEnterpriseGears(t *testing.T) {
	f := ossgears.NewFactory()

	for _, typ := range []string{"sim_source", "sim_responder"} {
		if !slices.Contains(f.Types(), typ) {
			t.Errorf("%s is missing from the engine factory: %v", typ, f.Types())
			continue
		}
		if _, err := f.Create(typ); err != nil {
			t.Errorf("create %s: %v", typ, err)
		}
		m, ok := f.Manifest(typ)
		if !ok {
			t.Errorf("%s has no manifest in the catalog", typ)
			continue
		}
		if m.Summary == "" || m.Summary == "No manifest declared yet." {
			t.Errorf("%s publishes no real manifest: %q", typ, m.Summary)
		}
	}
}

func TestTheEnterpriseGearsDoNotDisplaceTheEngineOnes(t *testing.T) {
	types := ossgears.NewFactory().Types()

	for _, typ := range []string{"io_tcp", "io_iso8583", "codec_iso8583", "coatcheck", "conductor"} {
		if !slices.Contains(types, typ) {
			t.Errorf("registering the enterprise gears dropped %s: %v", typ, types)
		}
	}
}
