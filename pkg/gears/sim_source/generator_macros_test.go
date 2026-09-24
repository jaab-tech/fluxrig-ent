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

package sim_source

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMacroGenerator(t *testing.T, seed int64) *Generator {
	t.Helper()
	spec, meta, content := loadTestSpec(t)
	gen := NewGeneratorFromContent(content, spec, meta, Config{Seed: seed}, &mockIDGenerator{})
	gen.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return gen
}

// The documented form has a space after the comma. Without trimming it, $RAND(100, 5000)
// read as 0 to 999999, $SEQ(a, 500) started at 1 and $PAN(4111, 15) had 16 digits.
func TestGenerator_MacroArgumentsMayHaveSpaces(t *testing.T) {
	gen := newMacroGenerator(t, 42)

	assert.Equal(t, "7", gen.expandMacro("$RAND(7, 7)", 0, nil))
	assert.Equal(t, "500", gen.expandMacro("$SEQ(a, 500)", 0, nil))
	assert.Equal(t, "501", gen.expandMacro("$SEQ(a, 500)", 0, nil))

	for i := 0; i < 50; i++ {
		v := parseInt(t, gen.expandMacro("$RAND(100, 5000)", 0, nil))
		assert.GreaterOrEqual(t, v, 100)
		assert.LessOrEqual(t, v, 5000)
	}

	pan := gen.expandMacro("$PAN(4111, 15)", 0, nil)
	assert.Len(t, pan, 15)
	assert.Equal(t, "4111", pan[:4])
}

// A length under two digits has no room for the check digit, and used to slice below zero.
func TestGenerator_PANWithAnImpossibleLengthDoesNotPanic(t *testing.T) {
	gen := newMacroGenerator(t, 42)
	for _, template := range []string{"$PAN(4111, 0)", "$PAN(4111,-3)", "$PAN(4111, 1)"} {
		require.NotPanics(t, func() {
			assert.Len(t, gen.expandMacro(template, 0, nil), 16, "the default length holds")
		}, template)
	}
}

// Every macro is deterministic under the seed, the UUID included.
func TestGenerator_UUIDIsDeterministicUnderTheSeed(t *testing.T) {
	first := newMacroGenerator(t, 7)
	second := newMacroGenerator(t, 7)
	other := newMacroGenerator(t, 8)

	a, b := first.expandMacro("$UUID", 0, nil), second.expandMacro("$UUID", 0, nil)
	assert.Equal(t, a, b, "the same seed gives the same UUID")
	assert.NotEqual(t, a, other.expandMacro("$UUID", 0, nil))

	id, err := uuid.Parse(a)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(4), id.Version())
	assert.Equal(t, uuid.RFC4122, id.Variant())

	first.Reseed(7)
	assert.Equal(t, a, first.expandMacro("$UUID", 0, nil), "a reset to the seed replays it")
}

// A gear with nothing to build a message from would start and emit nothing useful.
func TestGenerator_HasValueSource(t *testing.T) {
	assert.False(t, (&Generator{}).HasValueSource())
	assert.True(t, (&Generator{cfg: Config{Set: map[string]string{"pan": "4111111111111111"}}}).HasValueSource())
	assert.True(t, (&Generator{cfg: Config{Defaults: map[string]string{"39": "00"}}}).HasValueSource())
	assert.True(t, newMacroGenerator(t, 1).HasValueSource(), "the reference spec has a simulation section")
}

// $NOW and $RRN render the clock of the configured zone, and the date follows the zone:
// 23:30 GMT is already the next day in Tokyo.
func TestGenerator_NowFollowsTheTimezone(t *testing.T) {
	instant := time.Date(2026, 9, 21, 23, 30, 5, 0, time.UTC)
	render := func(tz, macro string, field int) string {
		gen := newMacroGenerator(t, 1)
		gen.loc = zoneOf(tz)
		gen.clock = func() time.Time { return instant }
		return gen.expandMacro(macro, field, nil)
	}
	const datetime, clock, date = 7, 12, 13 // DE 7, DE 12 and DE 13 of the reference spec

	assert.Equal(t, "0921233005", render("GMT", "$NOW", datetime))
	assert.Equal(t, "0921233005", render("utc", "$NOW", datetime))
	assert.Equal(t, "0921203005", render("America/Montevideo", "$NOW", datetime))
	assert.Equal(t, "0922083005", render("Asia/Tokyo", "$NOW", datetime))
	assert.Equal(t, "203005", render("America/Montevideo", "$NOW", clock))
	assert.Equal(t, "0921", render("America/Montevideo", "$NOW", date))
	assert.Equal(t, "0922", render("Asia/Tokyo", "$NOW", date))
	assert.Equal(t, instant.In(time.Local).Format("0102150405"), render("", "$NOW", datetime), "the default is the machine's zone")
	assert.Equal(t, instant.In(time.Local).Format("0102150405"), render("local", "$NOW", datetime))

	assert.Equal(t, "260922000001", render("Asia/Tokyo", "$RRN", 37), "the date of the RRN follows the zone")
	assert.Equal(t, "260921000001", render("GMT", "$RRN", 37))
}

func TestGenerator_ATimezoneThatDoesNotResolveFallsBackToLocal(t *testing.T) {
	assert.Equal(t, time.Local, zoneOf("Mars/Olympus"), "Init refuses it before a generator is built")
}

// DE 14 is the expiry of the card, YYMM, and it is generated ahead of now. It was written as
// MMDD, so every generated authorization carried an expiry that is not a date, and a rule
// such as field(14) < 2501 declined all of them as expired.
func TestGenerator_TheExpiryIsYYMMAndAheadOfNow(t *testing.T) {
	at := func(y int, m time.Month, d int) func() time.Time {
		return func() time.Time { return time.Date(y, m, d, 23, 30, 5, 0, time.UTC) }
	}
	newGen := func(months int, clock func() time.Time) *Generator {
		gen := newMacroGenerator(t, 1)
		gen.loc = time.UTC
		gen.clock = clock
		gen.cfg.ExpiryMonths = months
		return gen
	}
	const date, expiry = 13, 14

	assert.Equal(t, "2809", newGen(24, at(2026, 9, 21)).synthesizeField(expiry, nil))
	assert.Equal(t, "2709", newGen(12, at(2026, 9, 21)).synthesizeField(expiry, nil))
	assert.Equal(t, "2609", newGen(0, at(2026, 9, 21)).synthesizeField(expiry, nil), "zero months is the current month")
	assert.Equal(t, "2702", newGen(3, at(2026, 11, 30)).synthesizeField(expiry, nil), "the year turns over")
	assert.Equal(t, "2702", newGen(6, at(2026, 8, 31)).synthesizeField(expiry, nil), "the last day of a month does not skip a month")

	gen := newGen(24, at(2026, 9, 21))
	assert.Equal(t, "0921", gen.synthesizeField(date, nil), "a date that is not an expiry is not moved")
	assert.Equal(t, "2609", gen.expandMacro("$NOW", expiry, nil), "$NOW is the current time, in the layout of the field")

	// Through a whole message.
	values := genAllFields(t, newGen(24, at(2026, 9, 21)))
	require.Contains(t, values, expiry)
	assert.Equal(t, "2809", values[expiry])
}

func TestConfig_TheExpiryDefaultsToTwentyFourMonths(t *testing.T) {
	assert.Equal(t, 24, DefaultConfig().ExpiryMonths)
}

// A scenario that wants declines for an expired card asks for a share of expired
// expiries. They come from the seed, so a run replays exactly.
func TestGenerator_ASharedOfTheExpiriesIsAlreadyExpired(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC) }
	const now = 2609 // September 2026, as YYMM
	draw := func(percent, count int) (expired int, all []string) {
		gen := newMacroGenerator(t, 7)
		gen.loc = time.UTC
		gen.clock = clock
		gen.cfg.ExpiryMonths = 24
		gen.cfg.ExpiredPercent = percent
		for i := 0; i < count; i++ {
			v := gen.synthesizeField(14, nil)
			all = append(all, v)
			if parseInt(t, v) < now {
				expired++
			}
		}
		return expired, all
	}

	none, _ := draw(0, 200)
	assert.Zero(t, none, "by default no expiry is expired")

	all, values := draw(100, 200)
	assert.Equal(t, 200, all, "every expiry is expired at 100 percent")
	for _, v := range values {
		n := parseInt(t, v)
		assert.GreaterOrEqual(t, n, 2409, "at most ExpiryMonths months back: %s", v)
		assert.Less(t, n, now, "at least one month back: %s", v)
	}

	half, _ := draw(50, 1000)
	assert.InDelta(t, 500, half, 100, "about half are expired at 50 percent")

	_, first := draw(30, 50)
	_, second := draw(30, 50)
	assert.Equal(t, first, second, "the same seed draws the same expiries")
}
