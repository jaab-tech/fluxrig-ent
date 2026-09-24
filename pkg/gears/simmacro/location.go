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

package simmacro

import (
	"fmt"
	"strings"
	"time"

	// The zone database travels inside the binary: a Rack image without tzdata would
	// otherwise refuse every zone name.
	_ "time/tzdata"
)

// Location resolves the timezone setting of a simulator gear, which decides the clock
// that $NOW and $RRN render. The setting is "local" (the zone of the machine, and the
// default when it is empty), "GMT" or "UTC" (the zone ISO 8583 asks of the transmission
// date and time, DE 7), or the name of a zone such as "America/Montevideo".
func Location(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	switch strings.ToLower(name) {
	case "", "local":
		return time.Local, nil
	case "gmt", "utc":
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("timezone %q is not local, GMT or the name of a zone such as America/Montevideo: %w", name, err)
	}
	return loc, nil
}
