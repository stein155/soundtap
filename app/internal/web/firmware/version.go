// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/stein155/soundtap/app/internal/release"
)

// The install confirmations. An install names the one its package needs, so going
// back to an older release or to Bose's firmware is never a side effect of "install".
const (
	confirmInstall   = "install"
	confirmDowngrade = "downgrade"
	confirmStock     = "stock"
)

// stockVersion is the version sign.sh is given for a Bose package, the way back to stock.
const stockVersion = release.StockVersion

// defaultOSVersionFile holds the running SoundTap version; post-build.sh writes it (ADR 0035).
const defaultOSVersionFile = "/etc/soundtap/os-version"

// runningVersion is the SoundTap version this speaker runs, or "" off a speaker.
func (s *Handler) runningVersion() string {
	path := s.osVersionFile
	if path == "" {
		path = defaultOSVersionFile
	}

	text, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(text))
}

// versionConfirm is the confirmation installing a package signed as version needs on a
// speaker running running: install for the same or a newer SoundTap, stock for Bose's
// firmware, and downgrade for an older SoundTap or a version it cannot order. Versions
// order as release.Version does, so an alpha is older than the release of its number.
func versionConfirm(version, running string) string {
	if version == stockVersion {
		return confirmStock
	}

	have, parsed := release.ParseVersion(running)
	if !parsed {
		return confirmInstall // nothing to compare with: a development machine
	}

	want, parsed := release.ParseVersion(version)
	if !parsed || want.Compare(have) < 0 {
		return confirmDowngrade
	}

	return confirmInstall
}

// packageVersion is what the upload's signed statement says about its version, and the
// confirmation installing it needs here. An unsigned upload has no version to judge and
// needs the plain one; the press on the speaker is what installs it (ADR 0048).
func (s *Handler) packageVersion(r *http.Request) (string, string, error) {
	if r.Header.Get(signatureHeader) == "" {
		return "", confirmInstall, nil
	}

	st, err := s.verifyStatement(r, release.KindCore)
	if err != nil {
		return "", "", err
	}

	return st.Version, versionConfirm(st.Version, s.runningVersion()), nil
}

// confirmError is an install whose confirmation is not the one its version needs:
// confirm is the one it needs, version the package's and running the speaker's.
type confirmError struct {
	version, running, confirm string
}

func (e confirmError) Error() string {
	switch e.confirm {
	case confirmStock:
		return "this is Bose's firmware: confirm the return to stock with X-SoundTap-Confirm: " + confirmStock
	case confirmDowngrade:
		return fmt.Sprintf("this package is %s, older than the running %s: "+
			"confirm the downgrade with X-SoundTap-Confirm: %s", e.version, e.running, confirmDowngrade)
	default:
		return "confirm the install with X-SoundTap-Confirm: " + confirmInstall
	}
}

// versionRefusal explains an install whose confirmation is not the one its version needs.
func versionRefusal(version, running, confirm string) error {
	return confirmError{version: version, running: running, confirm: confirm}
}
