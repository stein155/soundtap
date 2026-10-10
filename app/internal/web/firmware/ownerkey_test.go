// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/release"
	"github.com/stein155/soundtap/app/internal/ubootenv"
	"github.com/stein155/soundtap/app/internal/ubootenv/ubootenvtest"
)

// ownerKeyCopies is the owner key in each copy of the fake's redundant environment: ""
// when absent, "bad" when the copy does not check out.
func ownerKeyCopies(f *fakeFlash) [2]string {
	var out [2]string
	pad := func(b []byte, off int) []byte {
		b = append([]byte(nil), b...)
		if len(b) < off+ubootenv.Size {
			b = append(b, bytes.Repeat([]byte{0xff}, off+ubootenv.Size-len(b))...)
		}
		return b[off : off+ubootenv.Size]
	}
	for i, raw := range [][]byte{pad(f.writes["/dev/mtd5"], 0), pad(f.writes["/dev/mtd4"], ubootenv.RedundantOffset)} {
		env, _, err := ubootenv.ParseCopy(raw)
		if err != nil {
			out[i] = "bad"
			continue
		}
		out[i], _ = env.Get(release.OwnerKeyEnv)
	}
	return out
}

// withOwnerKey puts value into the fake's environment as owner key, both copies.
func withOwnerKey(t *testing.T, f *fakeFlash, value string) {
	t.Helper()
	env := soundtapEnv(t)
	env.Set(release.OwnerKeyEnv, value)
	if err := ubootenvtest.Image(f.writes, "/dev/mtd5", "/dev/mtd4", env); err != nil {
		t.Fatal(err)
	}
}

func ownerKeyCall(t *testing.T, s *Handler, method, body string) (*httptest.ResponseRecorder, OwnerKey) {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	req := httptest.NewRequestWithContext(t.Context(), method, OwnerKeyPath, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var k OwnerKey
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &k); err != nil {
			t.Fatalf("%s: %v: %s", method, err, rec.Body)
		}
	}
	return rec, k
}

// The route sets the key in both environment copies, says so with its fingerprint, and
// removes it from both; a value that is not a key is refused before anything is written.
func TestOwnerKeyRoute(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	if _, k := ownerKeyCall(t, s, http.MethodGet, ""); !k.Available || k.Active || k.Key != "" {
		t.Fatalf("fresh speaker: %+v", k)
	}
	for _, bad := range []string{`{"key":""}`, `{"key":"AAAA"}`, `{"key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5"}`, `not json`} {
		if rec, _ := ownerKeyCall(t, s, http.MethodPut, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: %d, want 400", bad, rec.Code)
		}
	}
	if len(f.cmds) != 0 {
		t.Fatalf("a refused key wrote: %q", f.cmds)
	}

	pub, _, _ := ed25519.GenerateKey(nil)
	key := base64.StdEncoding.EncodeToString(pub)
	sum := sha256.Sum256(pub)
	rec, k := ownerKeyCall(t, s, http.MethodPut, `{"key":" `+key+`\n"}`)
	if rec.Code != http.StatusOK || !k.Active || k.Key != key || k.Fingerprint != hex.EncodeToString(sum[:8]) {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	if got := ownerKeyCopies(f); got != [2]string{key, key} {
		t.Fatalf("after PUT the copies hold %q", got)
	}
	if _, k := ownerKeyCall(t, s, http.MethodGet, ""); !k.Active || k.Key != key {
		t.Fatalf("GET after PUT: %+v", k)
	}
	// Nothing but the key changed.
	env := envOf(t, f)
	env.Delete(release.OwnerKeyEnv)
	if !env.Equal(soundtapEnv(t)) {
		t.Errorf("the PUT changed other entries: %s", env.Text())
	}

	if rec, k := ownerKeyCall(t, s, http.MethodDelete, ""); rec.Code != http.StatusOK || k.Active || !k.Available {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body)
	}
	if got := ownerKeyCopies(f); got != [2]string{"", ""} {
		t.Fatalf("after DELETE the copies hold %q", got)
	}
}

// While an install holds the operations lock the key is not written.
func TestOwnerKeyWaitsForTheInstall(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	s.firmware.mu.Lock()
	defer s.firmware.mu.Unlock()
	pub, _, _ := ed25519.GenerateKey(nil)
	for _, m := range []string{http.MethodPut, http.MethodDelete} {
		rec, _ := ownerKeyCall(t, s, m, `{"key":"`+base64.StdEncoding.EncodeToString(pub)+`"}`)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s during an install: %d, want 409", m, rec.Code)
		}
	}
	if len(f.cmds) != 0 {
		t.Fatalf("wrote during an install: %q", f.cmds)
	}
}

// Off a speaker there is no environment: nothing to show, nothing to set.
func TestOwnerKeyOffDevice(t *testing.T) {
	s := &Handler{flashPlan: func() (flash.Target, error) { return flash.Target{EnvMTD: -1}, nil }}
	if rec, k := ownerKeyCall(t, s, http.MethodGet, ""); rec.Code != http.StatusOK || k.Available || k.Active {
		t.Fatalf("GET: %d %+v", rec.Code, k)
	}
	pub, _, _ := ed25519.GenerateKey(nil)
	if rec, _ := ownerKeyCall(t, s, http.MethodPut, `{"key":"`+base64.StdEncoding.EncodeToString(pub)+`"}`); rec.Code != http.StatusNotImplemented {
		t.Fatalf("PUT: %d", rec.Code)
	}
	if err := s.ClearOwnerKey(); !errors.Is(err, ErrNoEnvironment) {
		t.Fatalf("ClearOwnerKey: %v", err)
	}
}

// The factory reset's part: the key goes from both copies, and a speaker without one
// writes nothing.
func TestClearOwnerKey(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	if err := s.ClearOwnerKey(); err != nil || len(f.cmds) != 0 {
		t.Fatalf("no key: %v, ran %q", err, f.cmds)
	}
	pub, _, _ := ed25519.GenerateKey(nil)
	withOwnerKey(t, f, base64.StdEncoding.EncodeToString(pub))
	if err := s.ClearOwnerKey(); err != nil {
		t.Fatal(err)
	}
	if got := ownerKeyCopies(f); got != [2]string{"", ""} {
		t.Fatalf("copies hold %q", got)
	}
}

// ADR 0048: a package signed with the owner's own key installs only while that key is
// set, under the rules a release key's package installs under: production on a production
// image, the version against the running one. A garbled key is ignored and leaves the
// release key working.
func TestOwnerSignedPackageInstallsOnlyWithTheKey(t *testing.T) {
	releasePub, releasePriv, _ := ed25519.GenerateKey(nil)
	ownerPub, ownerPriv, _ := ed25519.GenerateKey(nil)
	owner := base64.StdEncoding.EncodeToString(ownerPub)
	otherPub, _, _ := ed25519.GenerateKey(nil)
	oldKey, oldAllow, oldVersion, oldProfile := releaseKeys, allowUnsigned, osVersionFile, profileFile
	releaseKeys, allowUnsigned = []string{base64.StdEncoding.EncodeToString(releasePub)}, strictAllowUnsigned
	dir := t.TempDir()
	osVersionFile, profileFile = filepath.Join(dir, "os-version"), filepath.Join(dir, "profile")
	t.Cleanup(func() {
		releaseKeys, allowUnsigned, osVersionFile, profileFile = oldKey, oldAllow, oldVersion, oldProfile
	})
	for path, text := range map[string]string{osVersionFile: "0.1.0\n", profileFile: "production\n"} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := universalST10Package(t)
	sum := sha256.Sum256(pkg)
	sign := func(k ed25519.PrivateKey, profile, version string) string {
		return signed(t, k, release.KindCore, profile, version, sum[:])
	}
	install := func(env, sig string) (*httptest.ResponseRecorder, *fakeFlash) {
		f := &fakeFlash{}
		s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
		if env != "" {
			withOwnerKey(t, f, env)
		}
		s.firmwareModel = func() (string, error) { return "st10", nil }
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/core-update", nil)
		if err := s.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) {
			return io.NopCloser(bytes.NewReader(pkg)), int64(len(pkg)), sig, nil
		}); err != nil {
			t.Fatal(err)
		}
		return rec, f
	}
	installed := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), `"installed":true`)
	}

	for _, c := range []struct {
		name, env, sig string
		want           bool
	}{
		{"owner-signed, no owner key", "", sign(ownerPriv, release.ProfileProduction, "0.2.0"), false},
		{"owner-signed, owner key set", owner, sign(ownerPriv, release.ProfileProduction, "0.2.0"), true},
		{"owner-signed, the same version", owner, sign(ownerPriv, release.ProfileProduction, "0.1.0"), true},
		{"owner-signed older: the downgrade rule holds", owner, sign(ownerPriv, release.ProfileProduction, "0.0.9"), false},
		{"owner-signed alpha on a production image", owner, sign(ownerPriv, release.ProfileAlpha, "0.2.0"), false},
		{"owner-signed stock: the way-back rule holds", owner, sign(ownerPriv, release.ProfileProduction, release.StockVersion), false},
		{"owner-signed, another owner key set", base64.StdEncoding.EncodeToString(otherPub), sign(ownerPriv, release.ProfileProduction, "0.2.0"), false},
		{"owner-signed, garbled owner key", owner[:43], sign(ownerPriv, release.ProfileProduction, "0.2.0"), false},
		{"release-signed, garbled owner key", owner[:43] + " ", sign(releasePriv, release.ProfileProduction, "0.2.0"), true},
		{"release-signed, owner key set", owner, sign(releasePriv, release.ProfileProduction, "0.2.0"), true},
	} {
		rec, f := install(c.env, c.sig)
		if got := installed(rec); got != c.want {
			t.Errorf("%s: installed %v (%d %s), want %v", c.name, got, rec.Code, rec.Body, c.want)
		}
		if !c.want && len(f.cmds) != 0 {
			t.Errorf("%s: refused, but NAND saw %q", c.name, f.cmds)
		}
		// An install selects its slot without dropping the key from either copy.
		if c.want && c.env != "" {
			if got := ownerKeyCopies(f); got != [2]string{c.env, c.env} {
				t.Errorf("%s: the install lost the owner key: %q", c.name, got)
			}
		}
	}
}
