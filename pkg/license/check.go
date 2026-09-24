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
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"
)

// CoveredGears names the commercial gears a licence under this mechanism
// covers. WarnAtStartup's messages are scoped to these by name, not to the
// Mixer, the Rack, or the engine: none of those need a licence for anything,
// under any Status. Only running these two gears in production does.
var CoveredGears = []string{"sim_source", "sim_responder"}

func coveredGearsList() string {
	return strings.Join(CoveredGears, ", ")
}

// Status is the outcome of checking a licence file against a build.
type Status string

const (
	// StatusCovered means a valid, signed licence file covers this build.
	StatusCovered Status = "covered"
	// StatusMissing means no licence file is configured, or the configured
	// path does not exist. This is the default for anyone who has not
	// bought a commercial licence, and it is not itself an error: reading
	// the source and any non-production use need no licence file at all.
	StatusMissing Status = "missing"
	// StatusInvalid means a licence file exists but is not valid JSON, or
	// its signature does not verify against JAAB Tech's public key.
	StatusInvalid Status = "invalid"
	// StatusExpired means the licence file verifies, but its window does
	// not cover this build's date.
	StatusExpired Status = "expired"
	// StatusUnknownBuild means the binary carries no build date (a plain
	// `go build` outside the Makefile's -ldflags), so no licence file
	// could be checked against it either way.
	StatusUnknownBuild Status = "unknown_build"
)

// LicenseFileEnv names the environment variable a Rack or a Mixer reads for
// its licence file path. Unset, ResolveLicenseFilePath falls back to
// DefaultLicenseFileName before the check reports StatusMissing.
const LicenseFileEnv = "FLUXRIG_LICENSE_FILE"

// DefaultLicenseFileName is the file ResolveLicenseFilePath looks for in the
// current working directory when LicenseFileEnv is unset. This mirrors the
// Mixer's own Zero-Config default for fluxrig-mixer.toml (checked with
// os.Stat, used only if present, no error if it is not): an operator who
// drops the licence file next to where the binary runs needs to set nothing.
const DefaultLicenseFileName = "fluxrig-license.json"

// ResolveLicenseFilePath returns the path WarnAtStartup checks: LicenseFileEnv
// if it names a non-empty value, otherwise DefaultLicenseFileName if that
// file exists in the working directory, otherwise "" (StatusMissing).
func ResolveLicenseFilePath() string {
	if p := os.Getenv(LicenseFileEnv); p != "" {
		return p
	}
	if _, err := os.Stat(DefaultLicenseFileName); err == nil {
		return DefaultLicenseFileName
	}
	return ""
}

// buildDateLayout is the layout the engine's Makefile bakes into
// pkg/version.BuildDate: `date -u +"%Y-%m-%dT%H:%M:%SZ"`.
const buildDateLayout = time.RFC3339

// Check loads the licence file at path, if any, and reports whether it
// covers a binary built on buildDate (pkg/version.BuildDate's own format,
// including its "unknown" default for a build with no ldflags). It never
// returns an error: a licence problem is reported as a Status, not failed
// on, because a Rack or a Mixer must keep running either way.
func Check(path string, buildDate string, pub ed25519.PublicKey) (Status, *Grant) {
	built, err := time.Parse(buildDateLayout, buildDate)
	if err != nil {
		return StatusUnknownBuild, nil
	}

	if path == "" {
		return StatusMissing, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return StatusMissing, nil
	}

	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return StatusInvalid, nil
	}
	grant, err := file.Verify(pub)
	if err != nil {
		return StatusInvalid, nil
	}
	if !grant.Covers(built) {
		return StatusExpired, grant
	}
	return StatusCovered, grant
}

// WarnAtStartup runs Check against ResolveLicenseFilePath's result and
// JAAB Tech's embedded public key, and logs the result. It never blocks
// startup: a licence file is read and reported here, exactly as
// https://fluxrig.org/docs/enterprise/licensing describes, and enforced only
// by the terms in LICENSE itself. Call it once, early in main, in every
// binary that links this module.
func WarnAtStartup(logger *slog.Logger, buildDate string) {
	if logger == nil {
		logger = slog.Default()
	}
	status, grant := Check(ResolveLicenseFilePath(), buildDate, publicKey)
	switch status {
	case StatusCovered:
		logger.Info("licence check",
			"flux.license.status", string(status),
			"flux.license.licensee", grant.Licensee,
			"flux.license.covers_until", grant.CoversBuildsUntil)
	case StatusUnknownBuild:
		// A dev build carries no build date to check a licence file against;
		// nothing useful to report either way.
	case StatusMissing:
		logger.Warn("no licence file found; the commercial gears ("+coveredGearsList()+") are running in evaluation mode; production use needs a commercial licence, see https://fluxrig.org/docs/enterprise/licensing",
			"flux.license.status", string(status),
			"flux.license.gears", coveredGearsList())
	case StatusInvalid:
		logger.Warn("licence file is present but does not verify; the commercial gears ("+coveredGearsList()+") are running in evaluation mode",
			"flux.license.status", string(status),
			"flux.license.gears", coveredGearsList())
	case StatusExpired:
		logger.Warn("licence file does not cover this build; the commercial gears ("+coveredGearsList()+") are running in evaluation mode",
			"flux.license.status", string(status),
			"flux.license.gears", coveredGearsList(),
			"flux.license.licensee", grant.Licensee,
			"flux.license.covers_until", grant.CoversBuildsUntil,
			"flux.license.build_date", buildDate)
	}
}
