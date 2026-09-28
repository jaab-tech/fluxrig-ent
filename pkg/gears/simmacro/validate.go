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
	"fmt"
	"strconv"
	"strings"
)

// Name returns the name of a macro template such as $SEQ(name, 5), or "" when the
// template is not a macro at all (a literal or an enum reference).
func Name(template string) string {
	if !strings.HasPrefix(template, "$") {
		return ""
	}
	name, _, _ := strings.Cut(template[1:], "(")
	return name
}

// ValidateArgs checks the arguments of the macros whose behavior depends on them
// ($RAND, $SEQ), which would otherwise silently fall back to a default rather than
// fail at apply time. name is the macro name without its leading $, as Name returns
// it; template is the macro as written in the scenario, e.g. "$RAND(1, 100)".
func ValidateArgs(name, template string) error {
	_, args, _ := strings.Cut(template, "(")
	parts := Args(strings.TrimSuffix(args, ")"))
	switch name {
	case "RAND":
		if len(parts) > 2 {
			return fmt.Errorf("$RAND takes at most two arguments, min and max")
		}
		for _, p := range parts {
			if _, err := strconv.Atoi(p); err != nil {
				return fmt.Errorf("$RAND: %q is not an integer", p)
			}
		}
	case "SEQ":
		if len(parts) > 2 {
			return fmt.Errorf("$SEQ takes at most two arguments, name and start")
		}
		if len(parts) == 2 {
			if _, err := strconv.ParseUint(parts[1], 10, 64); err != nil {
				return fmt.Errorf("$SEQ: the start %q is not a non-negative integer", parts[1])
			}
		}
	}
	return nil
}
