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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSeedOffsetNeverOverflowsForAFullRangeSeed is a regression test for a real
// bug: DeterministicClock used to compute time.Duration(seed) * time.Second
// directly. Seed is a full-range int64 (chosen for RNG entropy elsewhere), so
// a realistic seed such as a nanosecond timestamp overflowed a
// time.Duration's own int64-nanosecond range, producing a wrapped, often
// negative, nonsensical $NOW instead of a merely arbitrary one.
func TestSeedOffsetNeverOverflowsForAFullRangeSeed(t *testing.T) {
	for _, seed := range []int64{
		0,
		42,
		math.MaxInt64,
		math.MinInt64,
		time.Now().UnixNano(), // exactly the kind of value an operator plausibly reseeds with
	} {
		offset := SeedOffset(seed)
		assert.GreaterOrEqual(t, int64(offset), int64(0), "seed %d produced a negative offset", seed)
		assert.Less(t, offset, 100*365*24*time.Hour, "seed %d produced an offset outside the documented range", seed)
	}
}

func TestSeedOffsetIsDeterministic(t *testing.T) {
	assert.Equal(t, SeedOffset(12345), SeedOffset(12345))
}

func TestSeedOffsetZeroIsZero(t *testing.T) {
	assert.Equal(t, time.Duration(0), SeedOffset(0))
}
