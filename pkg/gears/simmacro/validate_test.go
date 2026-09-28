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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestName(t *testing.T) {
	assert.Equal(t, "SEQ", Name("$SEQ(name, 5)"))
	assert.Equal(t, "AUTH", Name("$AUTH"))
	assert.Equal(t, "", Name("00"), "a literal is not a macro")
	assert.Equal(t, "", Name(""))
}

func TestValidateArgs(t *testing.T) {
	require.NoError(t, ValidateArgs("RAND", "$RAND"))
	require.NoError(t, ValidateArgs("RAND", "$RAND(1, 100)"))
	require.NoError(t, ValidateArgs("SEQ", "$SEQ"))
	require.NoError(t, ValidateArgs("SEQ", "$SEQ(fraud)"))
	require.NoError(t, ValidateArgs("SEQ", "$SEQ(fraud, 500)"))
	require.NoError(t, ValidateArgs("AUTH", "$AUTH(anything)"), "a macro with no declared args is never checked")

	err := ValidateArgs("RAND", "$RAND(1, 2, 3)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most two arguments")

	err = ValidateArgs("RAND", "$RAND(1, many)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an integer")

	err = ValidateArgs("SEQ", "$SEQ(fraud, x)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start")
}
