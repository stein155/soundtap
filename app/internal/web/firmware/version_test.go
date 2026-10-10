// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/release"
)

func TestVersionConfirm(t *testing.T) {
	for _, c := range []struct{ version, running, want string }{
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
		if got := versionConfirm(c.version, c.running); got != c.want {
			t.Errorf("versionConfirm(%q, %q) = %q, want %q", c.version, c.running, got, c.want)
		}
	}
}

// An older release or Bose's firmware installs only with the confirmation that
// names it, and the refusal comes before the first byte is read.
func TestDowngradeAndStockNeedTheirOwnConfirmation(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	oldKey, oldAllow, oldVersion := releaseKeys, allowUnsigned, osVersionFile
	releaseKeys, allowUnsigned = []string{base64.StdEncoding.EncodeToString(pub)}, strictAllowUnsigned
	osVersionFile = filepath.Join(t.TempDir(), "os-version")
	t.Cleanup(func() { releaseKeys, allowUnsigned, osVersionFile = oldKey, oldAllow, oldVersion })
	if err := os.WriteFile(osVersionFile, []byte("0.2.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte("package"))
	sig := func(version string) string {
		return signed(t, priv, release.KindCore, release.ProfileProduction, version, sum[:])
	}
	for _, c := range []struct {
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
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/firmware?install=1", untouchedBody{t})
		req.Header.Set("X-SoundTap-Confirm", c.confirm)
		req.Header.Set(signatureHeader, c.sig)
		rec := httptest.NewRecorder()
		_, ok := installServer(t, &fakeFlash{}).admitInstall(rec, req)
		if ok != c.admitted {
			t.Errorf("%s: admitted = %v (status %d, %s), want %v", c.name, ok, rec.Code, rec.Body, c.admitted)
		}
		if !ok && !c.admitted && rec.Code != http.StatusPreconditionRequired {
			t.Errorf("%s: status %d, want 428", c.name, rec.Code)
		}
	}
}

// A package signed with profile alpha installs on an alpha image only; a
// production image takes only production packages.
func TestAlphaProfileOnlyOnAlpha(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	oldKey, oldAllow, oldVersion, oldProfile := releaseKeys, allowUnsigned, osVersionFile, profileFile
	releaseKeys, allowUnsigned = []string{base64.StdEncoding.EncodeToString(pub)}, strictAllowUnsigned
	dir := t.TempDir()
	osVersionFile, profileFile = filepath.Join(dir, "os-version"), filepath.Join(dir, "profile")
	t.Cleanup(func() {
		releaseKeys, allowUnsigned, osVersionFile, profileFile = oldKey, oldAllow, oldVersion, oldProfile
	})
	if err := os.WriteFile(osVersionFile, []byte("0.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("package"))
	for _, c := range []struct{ image, profile string }{
		{"production", release.ProfileAlpha},
		{"production", release.ProfileProduction},
		{"alpha", release.ProfileAlpha},
		{"alpha", release.ProfileProduction},
		{"", release.ProfileAlpha}, // an image that names no profile is not alpha
	} {
		if err := os.WriteFile(profileFile, []byte(c.image+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/firmware?install=1", untouchedBody{t})
		req.Header.Set("X-SoundTap-Confirm", confirmInstall)
		req.Header.Set(signatureHeader, signed(t, priv, release.KindCore, c.profile, "0.1.0", sum[:]))
		rec := httptest.NewRecorder()
		_, ok := installServer(t, &fakeFlash{}).admitInstall(rec, req)
		want := c.image == "alpha" || c.profile == release.ProfileProduction
		if ok != want {
			t.Errorf("%s image, %s package: admitted = %v (status %d, %s), want %v", c.image, c.profile, ok, rec.Code, rec.Body, want)
		}
		if !want && rec.Code != http.StatusForbidden {
			t.Errorf("%s image, %s package: status %d, want 403", c.image, c.profile, rec.Code)
		}
	}
}

// The app upload holds the same rules: kind app, the image's profile, and an older
// version only with the downgrade confirmation. soundtapdConfirm decides before the
// body is read, so this runs where the ELF upload tests cannot.
func TestSoundTapdConfirm(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	oldKey, oldProfile := releaseKeys, profileFile
	releaseKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	profileFile = filepath.Join(t.TempDir(), "profile")
	t.Cleanup(func() { releaseKeys, profileFile = oldKey, oldProfile })
	if err := os.WriteFile(profileFile, []byte("production\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("soundtapd"))
	app := func(version string) string {
		return signed(t, priv, release.KindApp, release.ProfileProduction, version, sum[:])
	}
	good := app("0.2.0")
	for _, c := range []struct {
		name, sig, confirm, want string
		code                     int
	}{
		{"newer", app("0.3.0"), "soundtapd", "soundtapd", 0},
		{"same", good, "soundtapd", "soundtapd", 0},
		{"older", app("0.1.0"), "soundtapd", "", http.StatusPreconditionRequired},
		{"older confirmed", app("0.1.0"), "downgrade", "downgrade", 0},
		{"signed as Core", signed(t, priv, release.KindCore, release.ProfileProduction, "0.3.0", sum[:]), "soundtapd", "", http.StatusForbidden},
		{"alpha on production", signed(t, priv, release.KindApp, release.ProfileAlpha, "0.3.0", sum[:]), "soundtapd", "", http.StatusForbidden},
		{"truncated", good[:strings.LastIndex(good, " sig=")], "soundtapd", "", http.StatusForbidden},
		{"digest only", base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:])), "soundtapd", "", http.StatusForbidden},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, soundtapdUpdatePath, nil)
		req.Header.Set("X-SoundTap-Confirm", c.confirm)
		req.Header.Set(signatureHeader, c.sig)
		got, code, err := (&Handler{}).soundtapdConfirm(req, "0.2.0")
		if got != c.want || code != c.code || (c.code != 0) != (err != nil) {
			t.Errorf("%s: soundtapdConfirm = %q, %d, %v; want %q, %d", c.name, got, code, err, c.want, c.code)
		}
	}
}
