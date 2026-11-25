// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/release"
	"github.com/stein155/soundtap/app/internal/stu"
)

// A production speaker goes back to Bose's firmware from Bose's own file, with no
// SoundTap signature: the stock confirmation and the press on the speaker are the gate
// (ADR 0048). An unsigned SoundTap build installs on the press as well, with the plain
// confirmation, on every profile.
func strictSigning(t *testing.T, handler *Handler) {
	t.Helper()

	pub, _, _ := ed25519.GenerateKey(nil)
	handler.releaseKeys = []string{base64.StdEncoding.EncodeToString(pub)}
}

// postUnsigned checks body without a signature, or installs it when target is the install's.
func postUnsigned(t *testing.T, handler *Handler, body []byte, target, confirm string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, bytes.NewReader(body))
	if confirm != "" {
		req.Header.Set("X-SoundTap-Confirm", confirm)
	}

	rec := httptest.NewRecorder()
	handler.postFirmware(rec, req)

	return rec
}

// The upload routes postUnsigned sends to.
const (
	checkPath   = "/api/firmware"
	installPath = "/api/firmware?install=1"
)

func nandWrites(f *fakeFlash) []string {
	var writes []string

	for _, cmd := range f.cmds {
		if strings.Contains(cmd, "flash_erase") || strings.Contains(cmd, "nandwrite") ||
			strings.Contains(cmd, "ubiformat") || strings.Contains(cmd, "fw_setenv") {
			writes = append(writes, cmd)
		}
	}

	return writes
}

func TestUnsignedStockInstallsWithTheStockConfirmation(t *testing.T) {
	t.Parallel()

	f := newFakeFlash()
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	strictSigning(t, s)

	rec := postUnsigned(t, s, stockWLAPackage(t), installPath, confirmStock)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("unsigned stock with the stock confirmation = %d %s", rec.Code, rec.Body)
	}
}

// An own build, unsigned, installs with the plain confirmation, whatever the image's
// profile: the press on the speaker that the route waits for is what lets it in.
func TestUnsignedBuildInstalls(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()
	s := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	strictSigning(t, s)

	rec := postUnsigned(t, s, universalPackage(t, stu.ExpectedSoundTapDuo), installPath, confirmInstall)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("unsigned build with the plain confirmation = %d %s", rec.Code, rec.Body)
	}

	if !strings.Contains(strings.Join(nand.cmds, "\n"), "fw_setenv -c") {
		t.Fatal("the installed slot was not selected")
	}
}

func TestUnsignedStockNeedsTheStockConfirmation(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()
	s := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048")
	strictSigning(t, s)

	rec := postUnsigned(t, s, stockWLAPackage(t), installPath, confirmInstall)
	if rec.Code < 400 || len(nandWrites(nand)) != 0 {
		t.Fatalf("unsigned stock with the plain confirmation = %d %s, NAND %v", rec.Code, rec.Body, nandWrites(nand))
	}
}

// A SoundTap package sent as "stock" without a signature never reaches NAND: its first
// module is ours, and it is refused before anything is erased or written.
func TestUnsignedSoundTapPackageIsNotStock(t *testing.T) {
	t.Parallel()

	for name, chain := range map[string][]string{
		"universal": stu.ExpectedSoundTapDuo,
		"native":    stu.ExpectedWLANative,
	} {
		nand := newFakeFlash()
		s := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048")
		strictSigning(t, s)

		rec := postUnsigned(t, s, universalPackage(t, chain), installPath, confirmStock)
		if rec.Code < 400 || len(nandWrites(nand)) != 0 {
			t.Errorf("%s: unsigned SoundTap as stock = %d %s, NAND %v", name, rec.Code, rec.Body, nandWrites(nand))
		}
	}
}

// The check says what the install needs, so the page shows the way-back warning and
// sends the stock confirmation; an unsigned SoundTap package is not called stock.
func TestUnsignedCheckReportsStock(t *testing.T) {
	t.Parallel()

	handler := wlaInstallServer(t, newFakeFlash(), "root=ubi0:rootfs ubi.mtd=9,2048")
	strictSigning(t, handler)

	for name, tcase := range map[string]struct {
		body []byte
		want string
	}{
		"bose":     {stockWLAPackage(t), confirmStock},
		"soundtap": {universalPackage(t, stu.ExpectedSoundTapDuo), confirmInstall},
	} {
		rec := postUnsigned(t, handler, tcase.body, checkPath, "")

		var rep Report

		err := json.NewDecoder(rec.Body).Decode(&rep)
		if err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: check = %d %v", name, rec.Code, err)
		}

		if rep.Confirm != tcase.want {
			t.Errorf("%s: confirm %q, want %q", name, rep.Confirm, tcase.want)
		}
	}
}

// An alpha release arrives with its .sig: it installs as signed, on every image (ADR 0048).
func TestAnAlphaWithItsSigInstallsAsSigned(t *testing.T) {
	t.Parallel()

	pub, priv, _ := ed25519.GenerateKey(nil)
	body := universalPackage(t, stu.ExpectedSoundTapDuo)
	sum := sha256.Sum256(body)
	handler := wlaInstallServer(t, newFakeFlash(), "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	handler.releaseKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, installPath, bytes.NewReader(body))
	req.Header.Set("X-SoundTap-Confirm", confirmInstall)
	req.Header.Set(signatureHeader,
		signed(t, priv, release.KindCore, release.ProfileProduction, "0.1.0-alpha.4", sum[:]))

	rec := httptest.NewRecorder()
	handler.postFirmware(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("alpha with its .sig = %d %s", rec.Code, rec.Body)
	}
}
