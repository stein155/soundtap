// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/stu"
)

// A production speaker goes back to Bose's firmware from Bose's own file, with no
// SoundTap signature: the stock confirmation and the press on the speaker are the gate
// (ADR 0048). Nothing else installs unsigned.
func strictSigning(t *testing.T) {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(nil)
	oldKey, oldAllow := releaseKeys, allowUnsigned
	releaseKeys, allowUnsigned = []string{base64.StdEncoding.EncodeToString(pub)}, strictAllowUnsigned
	t.Cleanup(func() { releaseKeys, allowUnsigned = oldKey, oldAllow })
}

func postUnsigned(s *Handler, body []byte, install bool, confirm string) *httptest.ResponseRecorder {
	target := "/api/firmware"
	if install {
		target += "?install=1"
	}
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if confirm != "" {
		req.Header.Set("X-SoundTap-Confirm", confirm)
	}
	rec := httptest.NewRecorder()
	s.postFirmware(rec, req)
	return rec
}

func nandWrites(f *fakeFlash) []string {
	var w []string
	for _, c := range f.cmds {
		if strings.Contains(c, "flash_erase") || strings.Contains(c, "nandwrite") ||
			strings.Contains(c, "ubiformat") || strings.Contains(c, "fw_setenv") {
			w = append(w, c)
		}
	}
	return w
}

func TestUnsignedStockInstallsWithTheStockConfirmation(t *testing.T) {
	strictSigning(t)
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	rec := postUnsigned(s, stockWLAPackage(t), true, confirmStock)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("unsigned stock with the stock confirmation = %d %s", rec.Code, rec.Body)
	}
}

func TestUnsignedStockNeedsTheStockConfirmation(t *testing.T) {
	strictSigning(t)
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	rec := postUnsigned(s, stockWLAPackage(t), true, confirmInstall)
	if rec.Code < 400 || len(nandWrites(f)) != 0 {
		t.Fatalf("unsigned stock with the plain confirmation = %d %s, NAND %v", rec.Code, rec.Body, nandWrites(f))
	}
}

// A SoundTap package sent as "stock" without a signature never reaches NAND: its first
// module is ours, and it is refused before anything is erased or written.
func TestUnsignedSoundTapPackageIsNotStock(t *testing.T) {
	strictSigning(t)
	for name, chain := range map[string][]string{
		"universal": stu.ExpectedSoundTapDuo,
		"native":    stu.ExpectedWLANative,
	} {
		f := &fakeFlash{}
		s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
		rec := postUnsigned(s, universalPackage(t, chain), true, confirmStock)
		if rec.Code < 400 || len(nandWrites(f)) != 0 {
			t.Errorf("%s: unsigned SoundTap as stock = %d %s, NAND %v", name, rec.Code, rec.Body, nandWrites(f))
		}
	}
}

// The check says what the install needs, so the page shows the way-back warning and
// sends the stock confirmation; an unsigned SoundTap package is not called stock.
func TestUnsignedCheckReportsStock(t *testing.T) {
	strictSigning(t)
	s := wlaInstallServer(t, &fakeFlash{}, "root=ubi0:rootfs ubi.mtd=9,2048")
	for name, c := range map[string]struct {
		body []byte
		want string
	}{
		"bose":     {stockWLAPackage(t), confirmStock},
		"soundtap": {universalPackage(t, stu.ExpectedSoundTapDuo), confirmInstall},
	} {
		rec := postUnsigned(s, c.body, false, "")
		var rep Report
		if err := json.NewDecoder(rec.Body).Decode(&rep); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: check = %d %v", name, rec.Code, err)
		}
		if rep.Confirm != c.want {
			t.Errorf("%s: confirm %q, want %q", name, rep.Confirm, c.want)
		}
	}
}
