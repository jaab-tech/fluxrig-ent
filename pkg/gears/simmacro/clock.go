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

import "time"

// deterministicClockRangeSeconds bounds SeedOffset's result so multiplying it
// by time.Second can never overflow a time.Duration (int64 nanoseconds, about
// 292 years). A seed is a full-range int64, chosen elsewhere for RNG entropy;
// used directly as a count of seconds, it overflows Duration long before it
// exhausts its own range. 100 years leaves ample margin.
const deterministicClockRangeSeconds = 100 * 365 * 24 * 60 * 60

// SeedOffset turns a seed into a duration safe to add to a fixed epoch for a
// deterministic clock. Converting to uint64 first reads a negative seed's own
// bit pattern rather than a negative remainder, so every seed, negative or
// not, lands in [0, 100 years). The mapping is deterministic (the same seed
// always yields the same offset) but not linear: two different seeds can
// collide on the same offset, which is fine for a clock whose only job is to
// replay identically run over run, not to preserve seed ordering.
func SeedOffset(seed int64) time.Duration {
	seconds := uint64(seed) % uint64(deterministicClockRangeSeconds)
	return time.Duration(seconds) * time.Second
}
