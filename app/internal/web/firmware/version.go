// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"errors"
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

// errBenchOnly refuses a package signed with profile alpha on an image that is not alpha.
var errBenchOnly = errors.New("this package is signed for development speakers only; install a release instead")

// osVersionFile holds the running SoundTap version; post-build.sh writes it (ADR 0035).
var osVersionFile = "/etc/soundtap/os-version"

func runningVersion() string {
	b, err := os.ReadFile(osVersionFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// versionConfirm is the confirmation installing a package signed as version needs on a
// speaker running running: install for the same or a newer SoundTap, stock for Bose's
// firmware, and downgrade for an older SoundTap or a version it cannot order. Versions
// order as release.Version does, so an alpha is older than the release of its number.
func versionConfirm(version, running string) string {
	if version == stockVersion {
		return confirmStock
	}
	have, ok := release.ParseVersion(running)
	if !ok {
		return confirmInstall // nothing to compare with: a development machine
	}
	want, ok := release.ParseVersion(version)
	if !ok || want.Compare(have) < 0 {
		return confirmDowngrade
	}
	return confirmInstall
}

// packageVersion is what the upload's signed statement says about its version, and the
// confirmation installing it needs here. An unsigned upload, which only an alpha speaker
// with allow-unsigned takes, has no version to judge and needs the plain one.
func (s *Handler) packageVersion(r *http.Request) (version, confirm string, err error) {
	if r.Header.Get(signatureHeader) == "" {
		return "", confirmInstall, nil
	}
	st, err := s.verifyStatement(r, release.KindCore)
	if err != nil {
		return "", "", err
	}
	return st.Version, versionConfirm(st.Version, runningVersion()), nil
}

// versionRefusal explains an install whose confirmation is not the one its version needs.
func versionRefusal(version, running, confirm string) error {
	switch confirm {
	case confirmStock:
		return fmt.Errorf("this is Bose's firmware: confirm the return to stock with X-SoundTap-Confirm: %s", confirmStock)
	case confirmDowngrade:
		return fmt.Errorf("this package is %s, older than the running %s: confirm the downgrade with X-SoundTap-Confirm: %s", version, running, confirmDowngrade)
	}
	return fmt.Errorf("confirm the install with X-SoundTap-Confirm: %s", confirmInstall)
}
