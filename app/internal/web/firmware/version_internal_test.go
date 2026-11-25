// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/release"
)

func TestVersionConfirm(t *testing.T) {
	t.Parallel()

	for _, tcase := range []struct{ version, running, want string }{
		{"0.2.0", "0.2.0", confirmInstall},
		{"0.3.0", "0.2.0", confirmInstall},
		{"0.10.0", "0.9.1", confirmInstall},
		{"0.1.9", "0.2.0", confirmDowngrade},
		{"1.0.0", "1.0.1", confirmDowngrade},
		{"", "0.2.0", confirmDowngrade},
		{"0.2", "0.2.0", confirmDowngrade},
		{"stock", "0.2.0", confirmStock},
		{"stock", "", confirmStock},
		{"0.1.0", "", confirmInstall},
		// An alpha comes before the release of its number (ADR 0035).
		{"0.1.0-alpha.2", "0.1.0-alpha.1", confirmInstall},
		{"0.1.0-alpha.1", "0.1.0-alpha.1", confirmInstall},
		{"0.1.0", "0.1.0-alpha.2", confirmInstall},
		{"0.1.0-alpha.1", "0.1.0-alpha.2", confirmDowngrade},
		{"0.1.0-alpha.9", "0.1.0", confirmDowngrade},
		{"0.2.0-alpha.1", "0.1.0", confirmInstall},
		{"0.1.0-beta.1", "0.1.0", confirmDowngrade},
	} {
		if got := versionConfirm(tcase.version, tcase.running); got != tcase.want {
			t.Errorf("versionConfirm(%q, %q) = %q, want %q", tcase.version, tcase.running, got, tcase.want)
		}
	}
}

// An older release or Bose's firmware installs only with the confirmation that
// names it, and the refusal comes before the first byte is read.
func TestDowngradeAndStockNeedTheirOwnConfirmation(t *testing.T) {
	t.Parallel()

	keys, priv := testKeys(t)
	versionFile := runningVersionFile(t, "0.2.0")
	sum := sha256.Sum256([]byte("package"))

	sig := func(version string) string {
		return signed(t, priv, release.KindCore, release.ProfileProduction, version, sum[:])
	}
	for _, tcase := range []struct {
		name, sig, confirm string
		admitted           bool
	}{
		{"newer", sig("0.3.0"), confirmInstall, true},
		{"same", sig("0.2.0"), confirmInstall, true},
		{"older as install", sig("0.1.0"), confirmInstall, false},
		{"older confirmed", sig("0.1.0"), confirmDowngrade, true},
		{"stock as install", sig("stock"), confirmInstall, false},
		{"stock as downgrade", sig("stock"), confirmDowngrade, false},
		{"stock confirmed", sig("stock"), confirmStock, true},
		{"newer as downgrade", sig("0.3.0"), confirmDowngrade, false},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, installPath, untouchedBody{t})
		req.Header.Set("X-SoundTap-Confirm", tcase.confirm)
		req.Header.Set(signatureHeader, tcase.sig)

		rec := httptest.NewRecorder()
		handler := installServer(t, newFakeFlash())
		handler.releaseKeys, handler.osVersionFile = keys, versionFile

		_, admitted := handler.admitInstall(rec, req)
		if admitted != tcase.admitted {
			t.Errorf("%s: admitted = %v (status %d, %s), want %v",
				tcase.name, admitted, rec.Code, rec.Body, tcase.admitted)
		}

		if !admitted && !tcase.admitted && rec.Code != http.StatusPreconditionRequired {
			t.Errorf("%s: status %d, want 428", tcase.name, rec.Code)
		}
	}
}

// ADR 0048: a statement that verifies is a signature, whatever its profile or version and
// whatever the image's profile: an alpha signed by a release key is admitted as signed.
func TestAlphaStatementIsASignatureOnEveryImage(t *testing.T) {
	t.Parallel()

	keys, priv := testKeys(t)
	versionFile := runningVersionFile(t, "0.1.0")
	sum := sha256.Sum256([]byte("package"))

	for _, tcase := range []struct{ profile, version string }{
		{release.ProfileAlpha, "0.2.0"},
		{release.ProfileProduction, "0.2.0-alpha.1"},
		{release.ProfileProduction, "0.2.0"},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, installPath, untouchedBody{t})
		req.Header.Set("X-SoundTap-Confirm", confirmInstall)
		req.Header.Set(signatureHeader, signed(t, priv, release.KindCore, tcase.profile, tcase.version, sum[:]))

		rec := httptest.NewRecorder()
		handler := installServer(t, newFakeFlash())
		handler.releaseKeys, handler.osVersionFile = keys, versionFile

		got, admitted := handler.admitInstall(rec, req)
		if !admitted || got.unsigned {
			t.Errorf("%s %s: admitted %v (status %d, %s), unsigned %v; want admitted as signed",
				tcase.profile, tcase.version, admitted, rec.Code, rec.Body, got.unsigned)
		}
	}
}

// The app upload holds the same rules: kind app, any profile, and an older
// version only with the downgrade confirmation. soundtapdConfirm decides before the
// body is read, so this runs where the ELF upload tests cannot.
func TestSoundTapdConfirm(t *testing.T) {
	t.Parallel()

	keys, priv := testKeys(t)
	sum := sha256.Sum256([]byte("soundtapd"))
	app := func(version string) string {
		return signed(t, priv, release.KindApp, release.ProfileProduction, version, sum[:])
	}

	good := app("0.2.0")
	for _, tcase := range []struct {
		name, sig, confirm, want string
		code                     int
	}{
		{"newer", app("0.3.0"), "soundtapd", "soundtapd", 0},
		{"same", good, "soundtapd", "soundtapd", 0},
		{"older", app("0.1.0"), "soundtapd", "", http.StatusPreconditionRequired},
		{"older confirmed", app("0.1.0"), "downgrade", "downgrade", 0},
		{
			"signed as Core", signed(t, priv, release.KindCore, release.ProfileProduction, "0.3.0", sum[:]),
			"soundtapd", "", http.StatusForbidden,
		},
		{
			"alpha profile, signed", signed(t, priv, release.KindApp, release.ProfileAlpha, "0.3.0", sum[:]),
			"soundtapd", "soundtapd", 0,
		},
		{"an older alpha release", app("0.1.0-alpha.1"), "soundtapd", "", http.StatusPreconditionRequired},
		{"truncated", good[:strings.LastIndex(good, " sig=")], "soundtapd", "", http.StatusForbidden},
		{
			"digest only", base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:])),
			"soundtapd", "", http.StatusForbidden,
		},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, soundtapdUpdatePath, nil)
		req.Header.Set("X-SoundTap-Confirm", tcase.confirm)
		req.Header.Set(signatureHeader, tcase.sig)

		handler := New(nil, "")
		handler.releaseKeys = keys

		got, code, err := handler.soundtapdConfirm(req, "0.2.0")
		if got != tcase.want || code != tcase.code || (tcase.code != 0) != (err != nil) {
			t.Errorf("%s: soundtapdConfirm = %q, %d, %v; want %q, %d",
				tcase.name, got, code, err, tcase.want, tcase.code)
		}
	}
}
