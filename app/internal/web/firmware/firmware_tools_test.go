// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/stu"
)

func wlaNativePackage(t *testing.T) []byte {
	t.Helper()
	native := buildSTUChain(t, stu.ExpectedWLANative, nil)
	copy(native[4:8], "RTCH")
	binary.BigEndian.PutUint32(native[stuPkgCRCOff:], crc32.ChecksumIEEE(native[:stuPkgCRCOff]))
	binary.BigEndian.PutUint32(native[stuHeaderCRCOff:], crc32.ChecksumIEEE(native[stuOverlap:stuHeaderCRCOff]))
	return native
}

// The WLA NAND image was built without the mtd package, so it had none of the
// commands internal/flash shells out to. /api/firmware still answered
// "installable": true, and an install would have streamed 96 MB before dying on
// "flash_erase: not found". Refuse up front and say which tools are missing.
func TestFirmwareReportRefusesWhenTheImageHasNoMTDTools(t *testing.T) {
	s := installServer(t, &fakeFlash{})
	s.firmwareModel = func() (string, error) { return "wla", nil }
	s.flashTools = func() []string { return []string{"flash_erase", "ubiformat"} }

	req := httptest.NewRequest("POST", "/api/firmware", strings.NewReader(string(wlaNativePackage(t))))
	rec := httptest.NewRecorder()
	s.postFirmware(rec, req)
	var rep Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("report is not JSON: %v (%s)", err, rec.Body)
	}
	if rep.Installable {
		t.Fatal("report says installable on an image with no flash tools")
	}
	if !strings.Contains(rep.Reason, "flash_erase") || !strings.Contains(rep.Reason, "ubiformat") {
		t.Errorf("reason = %q, want it to name the missing tools", rep.Reason)
	}
}

func TestFirmwareInstallRefusedBeforeUploadWhenToolsAreMissing(t *testing.T) {
	s := installServer(t, &fakeFlash{})
	s.firmwareModel = func() (string, error) { return "wla", nil }
	s.flashTools = func() []string { return []string{"nandwrite"} }

	req := httptest.NewRequest("POST", "/api/firmware?install=1", strings.NewReader("not read"))
	req.Header.Set("X-SoundTap-Confirm", "install")
	rec := httptest.NewRecorder()
	s.postFirmware(rec, req)
	if rec.Code != 501 {
		t.Fatalf("install status = %d, want 501", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "nandwrite") {
		t.Errorf("body = %q, want it to name nandwrite", rec.Body)
	}
}

// A speaker that cannot tell which slot it is running must say so in the report,
// not after a 99 MB upload. On this board two ubi.mtd= parameters on the kernel
// command line made exactly that happen.
func TestFirmwareReportSaysWhenTheSlotIsUnknown(t *testing.T) {
	s := installServer(t, &fakeFlash{})
	s.firmwareModel = func() (string, error) { return "wla", nil }
	s.flashPlan = func() (flash.Target, error) { return flash.Target{}, flash.ErrUnknownSlot }

	req := httptest.NewRequest("POST", "/api/firmware", strings.NewReader(string(wlaNativePackage(t))))
	rec := httptest.NewRecorder()
	s.postFirmware(rec, req)

	var rep Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("report is not JSON: %v (%s)", err, rec.Body)
	}
	if rep.Installable {
		t.Fatal("a speaker that cannot name its running slot reported the package as installable")
	}
	if !strings.Contains(rep.Reason, "slot") {
		t.Errorf("reason = %q, want it to say the slot is the problem", rep.Reason)
	}
}
