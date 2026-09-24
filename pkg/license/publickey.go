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
	"encoding/hex"
)

// publicKeyHex is JAAB Tech's licence-signing public key. The matching
// private key never leaves JAAB Tech's custody; publishing the public half
// here is what lets every binary verify a licence file offline, with no call
// home. Rotating it invalidates every licence file issued under the old key,
// so it is expected to change rarely, if ever.
const publicKeyHex = "2c110a6a09a2d20b52e89c3e2d67078625a001512381311a47dd23914fd4f257"

var publicKey = mustDecodePublicKey(publicKeyHex)

// PublicKey returns JAAB Tech's licence-signing public key, for tools that
// verify a licence file without generating one themselves.
func PublicKey() ed25519.PublicKey {
	return publicKey
}

func mustDecodePublicKey(h string) ed25519.PublicKey {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != ed25519.PublicKeySize {
		panic("license: embedded public key is not a valid ed25519 key")
	}
	return ed25519.PublicKey(b)
}
