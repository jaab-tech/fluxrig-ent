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
	"strings"
	"time"
)

// layoutTokens turns the layout of a date or time field in a spec into a Go layout. The
// tokens are YYYY, YY, MM (the month), DD, hh, mm (the minute) and ss. The replacer tries
// the old strings in the order given, so YYYY wins over YY.
var layoutTokens = strings.NewReplacer("YYYY", "2006", "YY", "06", "MM", "01", "DD", "02", "hh", "15", "mm", "04", "ss", "05")

// hasToken reports whether a layout names any part of a date or a time.
func hasToken(layout string) bool {
	return layoutTokens.Replace(layout) != layout
}

// Render writes an instant in the layout a spec declares for a field, such as MMDD,
// hhmmss, MMDDhhmmss or YYMM. It returns false when the layout names no part of a date or
// a time (it is empty, or it says something else), and the caller falls back to the kind
// of the field.
func Render(t time.Time, layout string) (string, bool) {
	if !hasToken(layout) {
		return "", false
	}
	return t.Format(layoutTokens.Replace(layout)), true
}

// IsExpiry reports whether a layout is that of an expiry date: a year and a month and
// nothing finer, such as YYMM.
func IsExpiry(layout string) bool {
	has := func(token string) bool { return strings.Contains(layout, token) }
	return (has("YY") || has("YYYY")) && has("MM") && !has("DD") && !has("hh") && !has("mm") && !has("ss")
}

// MonthsAhead is the first day of the month n months after the one t is in, at the
// zone of t. Adding the months to a day near the end of a month would overflow into the
// month after: the last day of August plus six months is the third of March.
func MonthsAhead(t time.Time, n int) time.Time {
	return time.Date(t.Year(), t.Month()+time.Month(n), 1, 0, 0, 0, 0, t.Location())
}
