// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/release"
)

// signed is the .sig sign.sh writes for a package of kind that hashes to sum, with a
// throwaway key.
func signed(t *testing.T, priv ed25519.PrivateKey, kind, profile, version string, sum []byte) string {
	t.Helper()
	sig, err := release.Sign(priv, release.Statement{Kind: kind, Profile: profile, Version: version, Digest: sum})
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// ADR 0048: an upload runs only with a valid signature, or with allow-unsigned set.
func TestSoundTapdUploadNeedsAValidSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	oldKey, oldAllow := releaseKeys, allowUnsigned
	releaseKeys, allowUnsigned = []string{base64.StdEncoding.EncodeToString(pub)}, strictAllowUnsigned
	t.Cleanup(func() { releaseKeys, allowUnsigned = oldKey, oldAllow })

	s, _, _, bin := soundtapdServer(t)
	upload := append(append([]byte{}, bin...), 0)
	sum := sha256.Sum256(upload)
	post := func(sig string) int {
		req := httptest.NewRequest("POST", soundtapdUpdatePath, bytes.NewReader(upload))
		req.Header.Set("X-SoundTap-Confirm", "soundtapd")
		if sig != "" {
			req.Header.Set(signatureHeader, sig)
		}
		rec := httptest.NewRecorder()
		s.postSoundTapd(rec, req)
		return rec.Code
	}
	if code := post(""); code != 403 {
		t.Errorf("unsigned: status %d, want 403", code)
	}
	other := sha256.Sum256([]byte("another file"))
	if code := post(signed(t, priv, release.KindApp, release.ProfileProduction, "0.1.0", other[:])); code != 403 {
		t.Errorf("signature of another file: status %d, want 403", code)
	}
	if code := post(signed(t, priv, release.KindCore, release.ProfileProduction, "0.1.0", sum[:])); code != 403 {
		t.Errorf("signed as a Core package: status %d, want 403", code)
	}
	if code := post(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:]))); code != 403 {
		t.Errorf("digest-only signature: status %d, want 403", code)
	}
	if code := post(signed(t, priv, release.KindApp, release.ProfileProduction, "0.1.0", sum[:])); code != 200 {
		t.Errorf("signed: status %d, want 200", code)
	}
}

func TestAllowUnsignedNeedsTheFileOnAlpha(t *testing.T) {
	home := t.TempDir()
	old := profileFile
	profileFile = filepath.Join(home, "profile")
	t.Cleanup(func() { profileFile = old })
	profile := func(p string) {
		if err := os.WriteFile(profileFile, []byte(p+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	profile("alpha")
	if strictAllowUnsigned(home) || strictAllowUnsigned("") {
		t.Fatal("unsigned builds allowed without allow-unsigned")
	}
	if err := os.WriteFile(filepath.Join(home, "allow-unsigned"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !strictAllowUnsigned(home) {
		t.Error("allow-unsigned is ignored on alpha")
	}
	// A production image ignores the file, and so does one that names no profile.
	profile("production")
	if strictAllowUnsigned(home) {
		t.Error("allow-unsigned honoured on production")
	}
	_ = os.Remove(profileFile)
	if strictAllowUnsigned(home) {
		t.Error("allow-unsigned honoured with no profile")
	}
}

// untouchedBody fails the test if the install reads a byte of the upload.
type untouchedBody struct{ t *testing.T }

func (b untouchedBody) Read([]byte) (int, error) {
	b.t.Error("the upload was read before its signature was checked")
	return 0, io.EOF
}

// The rootfs streams into the fallback slot, so a signature that does not verify
// must stop the install before the first byte is read, let alone written.
func TestFirmwareSignatureIsCheckedBeforeAnyWrite(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	_, stranger, _ := ed25519.GenerateKey(nil)
	oldKey, oldAllow := releaseKeys, allowUnsigned
	releaseKeys, allowUnsigned = []string{base64.StdEncoding.EncodeToString(pub)}, strictAllowUnsigned
	t.Cleanup(func() { releaseKeys, allowUnsigned = oldKey, oldAllow })

	sum := sha256.Sum256([]byte("package"))
	digestOnly := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:]))
	good := signed(t, priv, release.KindCore, release.ProfileProduction, "0.1.0", sum[:])
	for name, sig := range map[string]string{
		"unsigned":          "",
		"digest only":       digestOnly,
		"digest and hex":    digestOnly + " " + hex.EncodeToString(sum[:]),
		"truncated":         good[:strings.LastIndex(good, " sig=")],
		"another key":       signed(t, stranger, release.KindCore, release.ProfileProduction, "0.1.0", sum[:]),
		"signed as the app": signed(t, priv, release.KindApp, release.ProfileProduction, "0.1.0", sum[:]),
		"alpha profile":     signed(t, priv, release.KindCore, release.ProfileAlpha, "0.1.0", sum[:]),
		"edited version":    strings.Replace(good, "version=0.1.0", "version=0.2.0", 1),
	} {
		f := &fakeFlash{}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/firmware?install=1", untouchedBody{t})
		req.Header.Set("X-SoundTap-Confirm", "install")
		if sig != "" {
			req.Header.Set(signatureHeader, sig)
		}
		rec := httptest.NewRecorder()
		installServer(t, f).postFirmware(rec, req)
		if rec.Code != 403 || len(f.cmds) != 0 {
			t.Errorf("%s: status %d, NAND %v; want 403 and nothing written", name, rec.Code, f.cmds)
		}
	}
}

// An older app installs only with the downgrade confirmation.
func TestSoundTapdUploadOlderNeedsDowngrade(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	oldKey, oldAllow := releaseKeys, allowUnsigned
	releaseKeys, allowUnsigned = []string{base64.StdEncoding.EncodeToString(pub)}, strictAllowUnsigned
	t.Cleanup(func() { releaseKeys, allowUnsigned = oldKey, oldAllow })

	upload := func(t *testing.T) (*Handler, []byte) {
		t.Helper()
		s, u, _, bin := soundtapdServer(t)
		u.version = "0.2.0"
		return s, append(append([]byte{}, bin...), 0)
	}
	sigFor := func(body []byte, version string) string {
		sum := sha256.Sum256(body)
		return signed(t, priv, release.KindApp, release.ProfileProduction, version, sum[:])
	}
	post := func(s *Handler, body []byte, sig, confirm string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, soundtapdUpdatePath, bytes.NewReader(body))
		req.Header.Set("X-SoundTap-Confirm", confirm)
		req.Header.Set(signatureHeader, sig)
		rec := httptest.NewRecorder()
		s.postSoundTapd(rec, req)
		return rec.Code
	}
	for _, c := range []struct {
		version, confirm string
		want             int
	}{
		{"0.1.0", "soundtapd", http.StatusPreconditionRequired},
		{"0.1.0", "downgrade", http.StatusOK},
		{"0.2.0", "soundtapd", http.StatusOK},
		{"0.3.0", "downgrade", http.StatusBadRequest},
	} {
		s, body := upload(t)
		if got := post(s, body, sigFor(body, c.version), c.confirm); got != c.want {
			t.Errorf("signed %q, confirm %s: status %d, want %d", c.version, c.confirm, got, c.want)
		}
	}
}
