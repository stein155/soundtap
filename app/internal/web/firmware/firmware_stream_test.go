// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/release"
)

// A Core release the speaker downloads itself goes through the upload's install: the
// signature before the first write, the version against the running one, the slot.
func TestInstallStreamIsTheUploadInstall(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	_, stranger, _ := ed25519.GenerateKey(nil)
	oldKey, oldAllow, oldVersion := releaseKeys, allowUnsigned, osVersionFile
	releaseKeys, allowUnsigned = []string{base64.StdEncoding.EncodeToString(pub)}, strictAllowUnsigned
	osVersionFile = filepath.Join(t.TempDir(), "os-version")
	t.Cleanup(func() { releaseKeys, allowUnsigned, osVersionFile = oldKey, oldAllow, oldVersion })
	if err := os.WriteFile(osVersionFile, []byte("0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := universalST10Package(t)
	sum := sha256.Sum256(pkg)
	sign := func(k ed25519.PrivateKey, version string) string {
		return signed(t, k, release.KindCore, release.ProfileProduction, version, sum[:])
	}
	install := func(sig string) (*httptest.ResponseRecorder, *fakeFlash) {
		f := &fakeFlash{}
		s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
		s.firmwareModel = func() (string, error) { return "st10", nil }
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/core-update", nil)
		err := s.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) {
			return io.NopCloser(bytes.NewReader(pkg)), int64(len(pkg)), sig, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return rec, f
	}

	rec, f := install(sign(priv, "0.2.0"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("newer signed release = %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(strings.Join(f.cmds, "\n"), "fw_setenv -c") {
		t.Fatal("the installed slot was not selected")
	}
	for name, sig := range map[string]string{
		"signed as older":  sign(priv, "0.0.9"),
		"signed as stock":  sign(priv, "stock"),
		"another key":      sign(stranger, "0.2.0"),
		"signed as alpha":  signed(t, priv, release.KindCore, release.ProfileAlpha, "0.2.0", sum[:]),
		"signed as an app": signed(t, priv, release.KindApp, release.ProfileProduction, "0.2.0", sum[:]),
	} {
		if rec, f := install(sig); rec.Code < 400 || len(f.cmds) != 0 {
			t.Errorf("%s: status %d, NAND %v; want a refusal and nothing written", name, rec.Code, f.cmds)
		}
	}
}

func TestInstallStreamAnswersNothingItDidNotStart(t *testing.T) {
	s := installServer(t, &fakeFlash{})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/core-update", nil)
	upstream := errors.New("github down")
	rec := httptest.NewRecorder()
	err := s.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) { return nil, 0, "", upstream })
	if !errors.Is(err, upstream) || rec.Body.Len() != 0 {
		t.Fatalf("open failed: err %v, body %q", err, rec.Body)
	}
	if !s.TryLock() {
		t.Fatal("the install lock was kept after a failed open")
	}
	defer s.Unlock()
	opened := false
	err = s.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) {
		opened = true
		return nil, 0, "", nil
	})
	if !errors.Is(err, ErrInstallBusy) || opened {
		t.Fatalf("while locked: err %v, opened %v; want ErrInstallBusy and no download", err, opened)
	}
}
