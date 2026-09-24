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

package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"
)

func testKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func testGrant() Grant {
	return Grant{
		Licensee:          "Acme Test Corp",
		IssuedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CoversBuildsFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CoversBuildsUntil: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestSignThenVerifyReturnsTheSameGrant(t *testing.T) {
	pub, priv := testKeypair(t)
	want := testGrant()

	file, err := Sign(priv, want)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := file.Verify(pub)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Licensee != want.Licensee || !got.CoversBuildsFrom.Equal(want.CoversBuildsFrom) || !got.CoversBuildsUntil.Equal(want.CoversBuildsUntil) {
		t.Errorf("Verify returned %+v, want %+v", got, want)
	}
}

// A licence file is written and read back through JSON, typically with
// indentation (fluxrig-license issue pretty-prints it). Verification must
// survive that round trip: a File whose Payload is inlined as literal JSON
// gets its whitespace rewritten by json.MarshalIndent, which breaks the
// signature it carries. This is a regression test for exactly that bug.
func TestFileSurvivesAnIndentedJSONRoundTrip(t *testing.T) {
	pub, priv := testKeypair(t)
	file, err := Sign(priv, testGrant())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}

	var roundTripped File
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, err := roundTripped.Verify(pub); err != nil {
		t.Fatalf("Verify after an indented round trip: %v", err)
	}
}

func TestVerifyRejectsTheWrongPublicKey(t *testing.T) {
	_, priv := testKeypair(t)
	otherPub, _ := testKeypair(t)

	file, err := Sign(priv, testGrant())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := file.Verify(otherPub); err == nil {
		t.Error("Verify accepted a signature under a key that never signed it")
	}
}

func TestVerifyRejectsATamperedPayload(t *testing.T) {
	pub, priv := testKeypair(t)
	file, err := Sign(priv, testGrant())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	tampered := *file
	tampered.Payload = append([]byte(nil), file.Payload...)
	tampered.Payload[0] ^= 0xFF

	if _, err := tampered.Verify(pub); err == nil {
		t.Error("Verify accepted a payload that was modified after signing")
	}
}

func TestGrantCoversIsInclusiveAtBothEnds(t *testing.T) {
	g := testGrant()

	cases := []struct {
		name  string
		built time.Time
		want  bool
	}{
		{"exactly at the start", g.CoversBuildsFrom, true},
		{"exactly at the end", g.CoversBuildsUntil, true},
		{"well inside the window", g.CoversBuildsFrom.Add(30 * 24 * time.Hour), true},
		{"one second before the window opens", g.CoversBuildsFrom.Add(-time.Second), false},
		{"one second after the window closes", g.CoversBuildsUntil.Add(time.Second), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := g.Covers(c.built); got != c.want {
				t.Errorf("Covers(%s) = %v, want %v", c.built, got, c.want)
			}
		})
	}
}
