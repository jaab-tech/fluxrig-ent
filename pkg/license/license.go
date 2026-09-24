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

// Package license signs and verifies the licence file a commercial customer
// runs alongside fluxrig-ent. See check.go for how a binary uses it, and
// https://fluxrig.org/docs/enterprise/licensing for the terms it certifies.
package license

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"time"
)

// Grant is the content a licence file certifies: who it is issued to, and
// which builds it covers. Coverage is identified by build date, the value
// the engine's pkg/version.BuildDate reports, never by the wall clock at
// verification time: see check.go for why that is the point.
type Grant struct {
	Licensee          string    `json:"licensee"`
	IssuedAt          time.Time `json:"issued_at"`
	CoversBuildsFrom  time.Time `json:"covers_builds_from"`
	CoversBuildsUntil time.Time `json:"covers_builds_until"`
}

// Covers reports whether a build made on buildDate falls inside the grant's
// window. The comparison is inclusive at both ends.
func (g Grant) Covers(buildDate time.Time) bool {
	return !buildDate.Before(g.CoversBuildsFrom) && !buildDate.After(g.CoversBuildsUntil)
}

// File is the signed licence file: the exact JSON bytes of the Grant that
// were signed, plus the signature over those bytes. Signing the serialized
// bytes, rather than the struct, means the verifier never has to reproduce
// the signer's encoding to check it: it hashes exactly what was signed.
//
// Payload is a plain []byte, not a json.RawMessage, on purpose: encoding/json
// base64-encodes a []byte into an opaque string, so writing a File with
// json.MarshalIndent (or anything else that reformats whitespace) can never
// touch the bytes that were actually signed. A json.RawMessage inlines the
// payload as literal JSON instead, and MarshalIndent then reindents it along
// with everything else, changing its bytes after the signature was already
// computed over the original ones.
type File struct {
	Payload   []byte `json:"payload"`
	Signature []byte `json:"signature"`
}

// Sign serializes grant and signs it with priv, producing a File ready to
// write to disk as a licence file.
func Sign(priv ed25519.PrivateKey, grant Grant) (*File, error) {
	payload, err := json.Marshal(grant)
	if err != nil {
		return nil, fmt.Errorf("license: marshal grant: %w", err)
	}
	return &File{
		Payload:   payload,
		Signature: ed25519.Sign(priv, payload),
	}, nil
}

// Verify checks the file's signature against pub and, only once the
// signature holds, decodes and returns the grant it certifies.
func (f *File) Verify(pub ed25519.PublicKey) (*Grant, error) {
	if !ed25519.Verify(pub, f.Payload, f.Signature) {
		return nil, fmt.Errorf("license: signature does not verify; the file is corrupt or was not issued by JAAB Tech")
	}
	var grant Grant
	if err := json.Unmarshal(f.Payload, &grant); err != nil {
		return nil, fmt.Errorf("license: decode grant: %w", err)
	}
	return &grant, nil
}
