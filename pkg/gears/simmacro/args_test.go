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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArgs(t *testing.T) {
	assert.Nil(t, Args(""))
	assert.Nil(t, Args("   "))
	assert.Equal(t, []string{"1", "100"}, Args("1,100"))
	assert.Equal(t, []string{"1", "100"}, Args("1, 100"))
	assert.Equal(t, []string{"a", "500"}, Args(" a ,\t500 "))
	assert.Equal(t, []string{"4111", ""}, Args("4111,"))
}

func TestLocation(t *testing.T) {
	for _, name := range []string{"", "local", "Local", " local "} {
		loc, err := Location(name)
		require.NoError(t, err, name)
		assert.Equal(t, time.Local, loc, name)
	}
	for _, name := range []string{"GMT", "gmt", "UTC", " utc "} {
		loc, err := Location(name)
		require.NoError(t, err, name)
		assert.Equal(t, time.UTC, loc, name)
	}

	loc, err := Location("America/Montevideo")
	require.NoError(t, err, "a zone name must resolve even where the machine has no zone database")
	instant := time.Date(2026, 9, 21, 23, 30, 0, 0, time.UTC)
	assert.Equal(t, "20:30", instant.In(loc).Format("15:04"), "Montevideo is three hours behind GMT")

	_, err = Location("Mars/Olympus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Mars/Olympus")
}

func TestRender(t *testing.T) {
	instant := time.Date(2026, 9, 21, 23, 30, 5, 0, time.UTC)
	cases := map[string]string{
		"MMDD":       "0921",
		"hhmmss":     "233005",
		"MMDDhhmmss": "0921233005",
		"YYMM":       "2609",
		"YYYYMM":     "202609",
		"YYMMDD":     "260921",
	}
	for layout, want := range cases {
		got, ok := Render(instant, layout)
		require.True(t, ok, layout)
		assert.Equal(t, want, got, layout)
	}
	for _, layout := range []string{"", "positional", "tlv"} {
		_, ok := Render(instant, layout)
		assert.False(t, ok, "%q names no part of a date, so the caller falls back to the kind", layout)
	}
}

func TestIsExpiry(t *testing.T) {
	assert.True(t, IsExpiry("YYMM"))
	assert.True(t, IsExpiry("YYYYMM"))
	for _, layout := range []string{"MMDD", "hhmmss", "MMDDhhmmss", "YYMMDD", "positional", ""} {
		assert.False(t, IsExpiry(layout), layout)
	}
}

// The last day of August plus six months is February, and February is the month that
// comes out, not the third of March.
func TestMonthsAhead(t *testing.T) {
	end := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	got := MonthsAhead(end, 6)
	assert.Equal(t, time.Month(2), got.Month())
	assert.Equal(t, 2027, got.Year())
	assert.Equal(t, 1, got.Day())

	assert.Equal(t, "2809", func() string {
		s, _ := Render(MonthsAhead(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC), 24), "YYMM")
		return s
	}())
	assert.Equal(t, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), MonthsAhead(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC), 0))
}
