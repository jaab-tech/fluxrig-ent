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

// Package simmacro holds what the simulator gears share about the macros of their
// field values ($RAND(1, 100), $SEQ(name, 5)).
package simmacro

import "strings"

// Args splits the arguments of a macro at the commas and trims the spaces around each
// one, so that $RAND(1, 100) reads as $RAND(1,100). It returns nil when there are none.
func Args(args string) []string {
	if strings.TrimSpace(args) == "" {
		return nil
	}
	parts := strings.Split(args, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
