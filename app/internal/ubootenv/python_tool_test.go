// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package ubootenv

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/ubootenv/envfixture"
)

// parseToolOutput reads what a recovery tool wrote: the primary copy at out and the
// redundant one at out.redundant, the fresh pair this package writes for the same entries.
func parseToolOutput(t *testing.T, out string, primary []byte) (*Env, error) {
	t.Helper()
	redundant, err := os.ReadFile(out + ".redundant")
	if err != nil {
		return nil, err
	}
	env, flag, err := ParseCopy(primary)
	if err != nil {
		return nil, err
	}
	if flag != FreshPrimaryFlag {
		t.Errorf("primary flag %d, want %d", flag, FreshPrimaryFlag)
	}
	wantPrimary, err := env.EncodeCopy(FreshPrimaryFlag)
	if err != nil {
		return nil, err
	}
	wantRedundant, err := env.EncodeCopy(FreshRedundantFlag)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(primary, wantPrimary) || !bytes.Equal(redundant, wantRedundant) {
		t.Error("the tool's copies differ from the ones this package writes for the same entries")
	}
	return env, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// os/support/uboot_recipes.py replaces what nand_boot and nand_boot2 mean, so that
// this board's slot selector keeps working under a U-Boot that is not Bose's. Bose
// defines those two with `nboot.e` and `fixsig`, which exist only in their 2014.07 fork;
// ours use plain ubi/ubifsload/bootm. Nothing else in the environment may move, because
// bootselect.go rewrites only bootcmd and expects to find the rest where it was.
func TestTheMainlineBootToolOnlyRedefinesTheTwoSlotCommands(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	tool, err := filepath.Abs("../../../os/support/uboot_recipes.py")
	if err != nil || !exists(tool) {
		t.Skip("os/support/uboot_recipes.py not found")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "factory.bin")
	if err := os.WriteFile(in, envfixture.Image(), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "env-mainline.bin")
	cmd := exec.CommandContext(t.Context(), python, tool, in, out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", tool, err, output)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	env, err := parseToolOutput(t, out, written)
	if err != nil {
		t.Fatalf("this package rejects what the mainline boot tool wrote: %v", err)
	}
	// One shared recipe, entered with three variables per slot. Two copies of it did
	// not fit in the 8 KiB environment once every prerequisite was gated.
	shared, ok := env.Get("slot_boot")
	if !ok {
		t.Fatal("slot_boot is missing")
	}
	for _, slot := range []struct{ key, name, rmtd, kernel string }{
		{"nand_boot", "Ping", "8", "0x280000"},
		{"nand_boot2", "Pong", "9", "0x780000"},
	} {
		got, ok := env.Get(slot.key)
		if !ok {
			t.Fatalf("%s is missing", slot.key)
		}
		for _, want := range []string{
			"setenv slot " + slot.name,
			"setenv rmtd " + slot.rmtd,
			"setenv kaddr " + slot.kernel,
			"run slot_boot",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s does not %q: %q", slot.key, want, got)
			}
		}
	}
	if strings.Contains(shared, "nboot.e") {
		t.Error("slot_boot still uses Bose's nboot.e, which our U-Boot does not have")
	}
	// Each slot has to boot either layout: ours keeps its kernel in the root filesystem,
	// the vendor keeps it in the kernel partition behind a boot marker only fixsig
	// clears. One that can do just one of the two is how the speaker loses its way back.
	for _, want := range []string{"/boot/uImage-binky", "nand read 0x80007fc0 ${kaddr}"} {
		if !strings.Contains(shared, want) {
			t.Errorf("slot_boot lacks %q: %q", want, shared)
		}
	}
	// Every prerequisite gates the next. Semicolons in hush ignore failure, so an
	// unchecked chain lets a failed mount fall through to the layout test, and hands
	// bootm whatever an earlier attempt left at the device-tree address. A slot that
	// will not mount is broken, not stock, and must not be booted as stock.
	for _, guard := range []string{
		"if ubi part NAND.File-System-${slot} 2048; then",
		"if ubifsmount ubi0:rootfs; then",
		"if ubifsload 0x83000000 /boot/binky_sm2.dtb; then",
		"if nand read 0x80007fc0 ${kaddr} 0x500000; then",
		"if fixsig 0x80007fc0; then bootm",
	} {
		if !strings.Contains(shared, guard) {
			t.Errorf("slot_boot does not gate on %q: %q", guard, shared)
		}
	}
	// Which layout a slot holds is decided by looking at what was loaded, never by
	// whether the load reported failure. Measured on hardware 2026-09-17: branching on
	// `if ubifsload ... uImage-binky` never took its else arm with the file absent, so
	// bootm was handed an address holding nothing and the board sat there -- and a slot
	// holding vendor firmware, which has no such file by definition, could never reach
	// the kernel partition that would boot it. mw poisons the address first so a leftover
	// image from an earlier attempt cannot answer for this one.
	for _, want := range []string{
		"mw.l 0x82000000 0 1",
		"itest.l *0x82000000 == 0x56190527",
	} {
		if !strings.Contains(shared, want) {
			t.Errorf("slot_boot does not test the loaded image with %q: %q", want, shared)
		}
	}
	// And no else arm anywhere: the vendor path is what execution reaches when the our-
	// kernel bootm does not happen or does not return, so there is nothing to mis-bind.
	if strings.Contains(shared, "else") {
		t.Errorf("slot_boot branches with else again; the fallback is the absence of a branch: %q", shared)
	}
	original, err := ParseLegacy(factoryEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range original.Keys() {
		if key == "nand_boot" || key == "nand_boot2" {
			continue
		}
		before, _ := original.Get(key)
		after, ok := env.Get(key)
		if !ok || after != before {
			t.Errorf("%s: %q became %q (present=%v)", key, before, after, ok)
		}
	}
}
