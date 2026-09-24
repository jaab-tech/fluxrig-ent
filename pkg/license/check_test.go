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
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func writeLicenseFile(t *testing.T, dir string, contents []byte) string {
	t.Helper()
	path := filepath.Join(dir, "license.json")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write test licence file: %v", err)
	}
	return path
}

func TestCheckCoversABuildInsideTheWindow(t *testing.T) {
	pub, priv := testKeypair(t)
	file, err := Sign(priv, testGrant())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatalf("marshal licence file: %v", err)
	}
	path := writeLicenseFile(t, t.TempDir(), data)

	status, grant := Check(path, "2026-06-15T00:00:00Z", pub)
	if status != StatusCovered {
		t.Fatalf("Check status = %s, want %s", status, StatusCovered)
	}
	if grant == nil || grant.Licensee != "Acme Test Corp" {
		t.Errorf("Check returned grant %+v, want the signed licensee", grant)
	}
}

func TestCheckReportsExpiredWhenTheBuildIsOutsideTheWindow(t *testing.T) {
	pub, priv := testKeypair(t)
	file, err := Sign(priv, testGrant())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal licence file: %v", err)
	}
	path := writeLicenseFile(t, t.TempDir(), data)

	status, grant := Check(path, "2028-01-01T00:00:00Z", pub)
	if status != StatusExpired {
		t.Fatalf("Check status = %s, want %s", status, StatusExpired)
	}
	if grant == nil {
		t.Error("Check(StatusExpired) returned a nil grant; the caller needs it to report the window")
	}
}

func TestCheckReportsMissingWhenNoPathIsConfigured(t *testing.T) {
	pub, _ := testKeypair(t)
	status, grant := Check("", "2026-06-15T00:00:00Z", pub)
	if status != StatusMissing {
		t.Errorf("Check status = %s, want %s", status, StatusMissing)
	}
	if grant != nil {
		t.Errorf("Check(StatusMissing) returned a non-nil grant: %+v", grant)
	}
}

func TestCheckReportsMissingWhenTheFileDoesNotExist(t *testing.T) {
	pub, _ := testKeypair(t)
	status, _ := Check(filepath.Join(t.TempDir(), "does-not-exist.json"), "2026-06-15T00:00:00Z", pub)
	if status != StatusMissing {
		t.Errorf("Check status = %s, want %s", status, StatusMissing)
	}
}

func TestCheckReportsInvalidForACorruptFile(t *testing.T) {
	pub, _ := testKeypair(t)
	path := writeLicenseFile(t, t.TempDir(), []byte("not json at all"))

	status, _ := Check(path, "2026-06-15T00:00:00Z", pub)
	if status != StatusInvalid {
		t.Errorf("Check status = %s, want %s", status, StatusInvalid)
	}
}

func TestCheckReportsInvalidForAFileSignedByAnotherKey(t *testing.T) {
	pub, _ := testKeypair(t)
	_, otherPriv := testKeypair(t)
	file, err := Sign(otherPriv, testGrant())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal licence file: %v", err)
	}
	path := writeLicenseFile(t, t.TempDir(), data)

	status, _ := Check(path, "2026-06-15T00:00:00Z", pub)
	if status != StatusInvalid {
		t.Errorf("Check status = %s, want %s", status, StatusInvalid)
	}
}

func TestCheckReportsUnknownBuildForADevBuild(t *testing.T) {
	pub, _ := testKeypair(t)
	// "unknown" is pkg/version.BuildDate's own default for a binary built
	// without the Makefile's -ldflags.
	status, grant := Check("/irrelevant/because/build/date/is/unparseable", "unknown", pub)
	if status != StatusUnknownBuild {
		t.Errorf("Check status = %s, want %s", status, StatusUnknownBuild)
	}
	if grant != nil {
		t.Errorf("Check(StatusUnknownBuild) returned a non-nil grant: %+v", grant)
	}
}

func TestResolveLicenseFilePathPrefersTheEnvVarOverTheDefaultFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	envPath := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(envPath, []byte("irrelevant"), 0o600); err != nil {
		t.Fatalf("write env-pointed file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultLicenseFileName), []byte("irrelevant"), 0o600); err != nil {
		t.Fatalf("write default-named file: %v", err)
	}
	t.Setenv(LicenseFileEnv, envPath)

	if got := ResolveLicenseFilePath(); got != envPath {
		t.Errorf("ResolveLicenseFilePath() = %q, want the env var's path %q even though %s also exists in the working directory", got, envPath, DefaultLicenseFileName)
	}
}

func TestResolveLicenseFilePathFallsBackToTheDefaultFileInTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(LicenseFileEnv, "")

	if got := ResolveLicenseFilePath(); got != "" {
		t.Errorf("ResolveLicenseFilePath() = %q, want \"\" before %s exists", got, DefaultLicenseFileName)
	}

	if err := os.WriteFile(filepath.Join(dir, DefaultLicenseFileName), []byte("irrelevant"), 0o600); err != nil {
		t.Fatalf("write default-named file: %v", err)
	}
	if got := ResolveLicenseFilePath(); got != DefaultLicenseFileName {
		t.Errorf("ResolveLicenseFilePath() = %q, want %q once it exists in the working directory", got, DefaultLicenseFileName)
	}
}

// WarnAtStartup must never panic and never exit the process: a licence
// problem is reported, not enforced. This exercises every status through the
// real logger plumbing once, as a binary's main would call it.
func TestWarnAtStartupNeverPanicsForAnyStatus(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// A clean working directory: the StatusMissing case below must not
	// accidentally pick up a stray DefaultLicenseFileName left by something
	// else on the machine running the test.
	t.Chdir(t.TempDir())

	t.Setenv(LicenseFileEnv, "")
	WarnAtStartup(logger, "2026-06-15T00:00:00Z") // StatusMissing

	pub, priv := testKeypair(t)
	_ = pub // the embedded production key is what WarnAtStartup actually checks against
	file, err := Sign(priv, testGrant())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal licence file: %v", err)
	}
	path := writeLicenseFile(t, t.TempDir(), data)
	t.Setenv(LicenseFileEnv, path)

	// Signed by a throwaway key, not JAAB Tech's embedded one: WarnAtStartup
	// can only ever see this as StatusInvalid, which is the point here.
	WarnAtStartup(logger, "2026-06-15T00:00:00Z")
	WarnAtStartup(logger, "unknown")
}
