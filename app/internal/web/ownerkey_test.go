// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/release"
	"github.com/stein155/soundtap/app/internal/ubootenv"
	"github.com/stein155/soundtap/app/internal/ubootenv/ubootenvtest"
	"github.com/stein155/soundtap/app/internal/web"
)

// ownerKeyNAND is a speaker's U-Boot environment in memory, both copies, holding owner as
// the owner key ("" for none), and the server's install lane pointed at it.
func ownerKeyNAND(t *testing.T, srv *web.Server, owner string) *ubootenvtest.NAND {
	t.Helper()
	target, err := flash.Plan("root=ubi0:rootfs ubi.mtd=9,2048 "+flash.EnvRedundantMarker, map[string]flash.Partition{
		"NAND.U-Boot":           {Index: 4, Size: 0x1e0000, EraseSize: 0x20000},
		"NAND.U-Boot-Env":       {Index: 5, Size: 0x20000, EraseSize: 0x20000},
		"NAND.Kernel-Ping":      {Index: 6, Size: 0x500000, EraseSize: 0x20000},
		"NAND.Kernel-Pong":      {Index: 7, Size: 0x500000, EraseSize: 0x20000},
		"NAND.File-System-Ping": {Index: 8, Size: 0x6000000, EraseSize: 0x20000},
		"NAND.File-System-Pong": {Index: 9, Size: 0x6000000, EraseSize: 0x20000},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := ubootenv.NewEnv()
	env.Set("bootcmd", "run nand_boot2;")
	if owner != "" {
		env.Set(release.OwnerKeyEnv, owner)
	}
	nand := &ubootenvtest.NAND{Devices: map[string][]byte{}}
	if err := ubootenvtest.Image(nand.Devices, "/dev/mtd5", "/dev/mtd4", env); err != nil {
		t.Fatal(err)
	}
	srv.SetNANDForTest(func() (flash.Target, error) { return target, nil }, nand.Run, nand.Read)
	return nand
}

// ownerKeyIn is the owner key in each environment copy.
func ownerKeyIn(t *testing.T, nand *ubootenvtest.NAND) [2]string {
	t.Helper()
	var out [2]string
	for i, raw := range [][]byte{
		nand.Devices["/dev/mtd5"][:ubootenv.Size],
		nand.Devices["/dev/mtd4"][ubootenv.RedundantOffset : ubootenv.RedundantOffset+ubootenv.Size],
	} {
		env, _, err := ubootenv.ParseCopy(raw)
		if err != nil {
			t.Fatalf("copy %d: %v", i, err)
		}
		out[i], _ = env.Get(release.OwnerKeyEnv)
	}
	return out
}

func newOwnerKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub)
}

// A backup holds no owner key and a restore cannot set or clear one, whatever the file
// says (ADR 0048): the key is the root of trust for what installs, and a restore needs
// only the file. Nor can a settings write carry one.
func TestRestoreNeverTouchesTheOwnerKey(t *testing.T) {
	for _, set := range []string{"", newOwnerKey(t)} {
		srv, _, _ := newServerSrv(t)
		nand := ownerKeyNAND(t, srv, set)
		h := srv.Handler()
		backup := do(t, h, "GET", "/api/backup", "")
		if backup.Code != 200 || strings.Contains(strings.ToLower(backup.Body.String()), "owner") {
			t.Fatalf("export: %d %s", backup.Code, backup.Body)
		}
		var file map[string]any
		if err := json.Unmarshal(backup.Body.Bytes(), &file); err != nil {
			t.Fatal(err)
		}
		forged := newOwnerKey(t)
		file["ownerKey"], file[release.OwnerKeyEnv] = forged, forged
		file["uboot"] = map[string]string{release.OwnerKeyEnv: forged}
		file["settings"].(map[string]any)["ownerKey"] = forged
		file["settings"].(map[string]any)[release.OwnerKeyEnv] = forged
		if set != "" {
			file["ownerKey"] = "" // a file that asks for the key to go
		}
		body, err := json.Marshal(file)
		if err != nil {
			t.Fatal(err)
		}
		if rec := do(t, h, "POST", "/api/restore", string(body)); rec.Code != 200 {
			t.Fatalf("restore: %d %s", rec.Code, rec.Body)
		}
		for _, patch := range []string{`{"ownerKey":"` + forged + `"}`, `{"` + release.OwnerKeyEnv + `":"` + forged + `"}`} {
			if rec := do(t, h, "PUT", "/api/settings", patch); rec.Code != 400 {
				t.Errorf("PUT /api/settings %s: %d, want 400", patch, rec.Code)
			}
		}
		if got := ownerKeyIn(t, nand); got != [2]string{set, set} {
			t.Errorf("owner key %q: after the restore the copies hold %q", set, got)
		}
		for _, c := range nand.Cmds {
			if strings.HasPrefix(c, "fw_setenv") {
				t.Errorf("owner key %q: the restore wrote the environment: %s", set, c)
			}
		}
	}
}

// The factory reset removes the owner key from both copies: a speaker that changes hands
// trusts only the release keys again.
func TestFactoryResetRemovesTheOwnerKey(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	key := newOwnerKey(t)
	nand := ownerKeyNAND(t, srv, key)
	h := srv.Handler()
	var k struct {
		Active      bool   `json:"active"`
		Fingerprint string `json:"fingerprint"`
	}
	decodeBody(t, do(t, h, "GET", "/api/owner-key", ""), &k)
	if !k.Active || k.Fingerprint != release.Fingerprint(key) {
		t.Fatalf("before the reset: %+v", k)
	}
	if rec := do(t, h, "POST", "/api/factory-reset", `{"confirm":true}`); rec.Code != 200 {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	if got := ownerKeyIn(t, nand); got != [2]string{"", ""} {
		t.Fatalf("after the reset the copies hold %q", got)
	}
	decodeBody(t, do(t, h, "GET", "/api/owner-key", ""), &k)
	if k.Active {
		t.Fatal("the owner key is still active after the reset")
	}
	// ClearOwnerKey is what the front-panel chord calls: off a speaker it has nothing to do.
	srv2, _, _ := newServerSrv(t)
	if err := srv2.ClearOwnerKey(); err != nil {
		t.Fatalf("off a speaker: %v", err)
	}
}
