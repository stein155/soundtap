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

// testKeys is a throwaway release key pair: the public half as Handler.releaseKeys holds it.
func testKeys(t *testing.T) ([]string, ed25519.PrivateKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	return []string{base64.StdEncoding.EncodeToString(pub)}, priv
}

// runningVersionFile is an os-version file that says the speaker runs version.
func runningVersionFile(t *testing.T, version string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "os-version")

	err := os.WriteFile(path, []byte(version+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

// ADR 0048: a signed upload runs only when its signature verifies; an unsigned one runs on
// the press its route waits for, on any profile.
//
//nolint:paralleltest // it runs an uploaded binary: a parallel fork can hold the file open (ETXTBSY)
func TestSoundTapdUploadSignatureMustVerify(t *testing.T) {
	keys, priv := testKeys(t)
	app := soundtapdServer(t)
	handler := app.handler
	handler.releaseKeys = keys

	upload := append(append([]byte{}, app.bin...), 0)
	sum := sha256.Sum256(upload)
	post := func(sig string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, soundtapdUpdatePath,
			bytes.NewReader(upload))
		req.Header.Set("X-SoundTap-Confirm", "soundtapd")

		if sig != "" {
			req.Header.Set(signatureHeader, sig)
		}

		rec := httptest.NewRecorder()
		handler.postSoundTapd(rec, req)

		return rec.Code
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

	handler = soundtapdServer(t).handler
	handler.releaseKeys = keys

	if code := post(""); code != 200 {
		t.Errorf("unsigned: status %d, want 200", code)
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
	t.Parallel()

	keys, priv := testKeys(t)
	_, stranger := testKeys(t)
	sum := sha256.Sum256([]byte("package"))
	digestOnly := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:]))

	good := signed(t, priv, release.KindCore, release.ProfileProduction, "0.1.0", sum[:])
	for name, sig := range map[string]string{
		"digest only":       digestOnly,
		"digest and hex":    digestOnly + " " + hex.EncodeToString(sum[:]),
		"truncated":         good[:strings.LastIndex(good, " sig=")],
		"another key":       signed(t, stranger, release.KindCore, release.ProfileProduction, "0.1.0", sum[:]),
		"signed as the app": signed(t, priv, release.KindApp, release.ProfileProduction, "0.1.0", sum[:]),
		"edited version":    strings.Replace(good, "version=0.1.0", "version=0.2.0", 1),
	} {
		nand := newFakeFlash()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, installPath, untouchedBody{t})
		req.Header.Set("X-SoundTap-Confirm", "install")

		if sig != "" {
			req.Header.Set(signatureHeader, sig)
		}

		rec := httptest.NewRecorder()
		handler := installServer(t, nand)
		handler.releaseKeys = keys
		handler.postFirmware(rec, req)

		if rec.Code != 403 || len(nand.cmds) != 0 {
			t.Errorf("%s: status %d, NAND %v; want 403 and nothing written", name, rec.Code, nand.cmds)
		}
	}
}

// The check (POST /api/firmware without install=1) refuses a .sig that does not verify
// before it reads the package: otherwise the install would wait for the press as for an
// unsigned package, with no prompt in the page, and only then refuse it (ADR 0048).
func TestFirmwareCheckRefusesASigThatDoesNotVerify(t *testing.T) {
	t.Parallel()

	keys, priv := testKeys(t)
	sum := sha256.Sum256([]byte("package"))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, checkPath, untouchedBody{t})
	req.Header.Set(signatureHeader, signed(t, priv, release.KindApp, release.ProfileProduction, "0.1.0", sum[:]))

	rec := httptest.NewRecorder()
	handler := installServer(t, newFakeFlash())
	handler.releaseKeys = keys
	handler.postFirmware(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403 for an app .sig on the firmware check", rec.Code)
	}
}

// An older app installs only with the downgrade confirmation.
//
//nolint:paralleltest // it runs an uploaded binary: a parallel fork can hold the file open (ETXTBSY)
func TestSoundTapdUploadOlderNeedsDowngrade(t *testing.T) {
	keys, priv := testKeys(t)
	upload := func(t *testing.T) (*Handler, []byte) {
		t.Helper()

		app := soundtapdServer(t)
		app.handler.releaseKeys = keys
		app.update.version = "0.2.0"

		return app.handler, append(append([]byte{}, app.bin...), 0)
	}
	sigFor := func(body []byte, version string) string {
		sum := sha256.Sum256(body)

		return signed(t, priv, release.KindApp, release.ProfileProduction, version, sum[:])
	}

	post := func(handler *Handler, body []byte, sig, confirm string) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, soundtapdUpdatePath, bytes.NewReader(body))
		req.Header.Set("X-SoundTap-Confirm", confirm)
		req.Header.Set(signatureHeader, sig)

		rec := httptest.NewRecorder()
		handler.postSoundTapd(rec, req)

		return rec.Code
	}
	for _, tcase := range []struct {
		version, confirm string
		want             int
	}{
		{"0.1.0", "soundtapd", http.StatusPreconditionRequired},
		{"0.1.0", "downgrade", http.StatusOK},
		{"0.2.0", "soundtapd", http.StatusOK},
		{"0.3.0", "downgrade", http.StatusBadRequest},
	} {
		handler, body := upload(t)
		if got := post(handler, body, sigFor(body, tcase.version), tcase.confirm); got != tcase.want {
			t.Errorf("signed %q, confirm %s: status %d, want %d", tcase.version, tcase.confirm, got, tcase.want)
		}
	}
}

// A statement that verifies but names another file's digest is found out only once the
// whole package went by, after its root filesystem was written. That filesystem carries a
// kernel and would boot under a lost environment's built-in bootcmd, so it is erased.
func TestCoreThatMissesItsDigestLeavesNoRootfs(t *testing.T) {
	t.Parallel()

	keys, priv := testKeys(t)
	pkg := universalST10Package(t)
	other := sha256.Sum256(append([]byte("not "), pkg...))
	sig := signed(t, priv, release.KindCore, release.ProfileProduction, "0.2.0", other[:])

	nand := newFakeFlash()
	handler := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	handler.firmwareModel = func() (string, error) { return "st10", nil }
	handler.releaseKeys, handler.osVersionFile = keys, runningVersionFile(t, "0.1.0")
	rec := httptest.NewRecorder()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/core-update", nil)

	err := handler.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) {
		return io.NopCloser(bytes.NewReader(pkg)), int64(len(pkg)), sig, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("installed a package that misses its digest: %s", rec.Body)
	}

	formatted, erased := -1, -1

	for index, cmd := range nand.cmds {
		if strings.HasPrefix(cmd, "ubiformat /dev/mtd8") {
			formatted = index
		}

		if cmd == "flash_erase /dev/mtd8 0 0" {
			erased = index
		}
	}

	if formatted < 0 || erased < formatted {
		t.Errorf("PING's root filesystem written at %d, erased at %d: %q", formatted, erased, nand.cmds)
	}
}

// A stream cut after the root filesystem, in the boot chain that follows it, fails the scan
// before the digest is checked; the native rootfs it wrote is erased all the same.
func TestCoreCutAfterItsRootfsLeavesNoRootfs(t *testing.T) {
	t.Parallel()

	keys, priv := testKeys(t)
	pkg := universalST10Package(t)
	sum := sha256.Sum256(pkg)
	sig := signed(t, priv, release.KindCore, release.ProfileProduction, "0.2.0", sum[:])
	cut := pkg[:len(pkg)-16] // inside the last module, after soundtap-rootfs

	nand := newFakeFlash()
	handler := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	handler.firmwareModel = func() (string, error) { return "st10", nil }
	handler.releaseKeys, handler.osVersionFile = keys, runningVersionFile(t, "0.1.0")
	rec := httptest.NewRecorder()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/core-update", nil)

	err := handler.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) {
		return io.NopCloser(bytes.NewReader(cut)), int64(len(cut)), sig, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("installed a cut package: %s", rec.Body)
	}

	formatted, erased := -1, -1

	for index, cmd := range nand.cmds {
		if strings.HasPrefix(cmd, "ubiformat /dev/mtd8") {
			formatted = index
		}

		if cmd == "flash_erase /dev/mtd8 0 0" {
			erased = index
		}
	}

	if formatted < 0 || erased < formatted {
		t.Errorf("PING's root filesystem written at %d, erased at %d: %q", formatted, erased, nand.cmds)
	}
}
