// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// A bad erase block under one copy of the redundant environment loses that copy, not the
// environment (ADR 0010). libubootenv refuses both copies then, so soundtapd reads and
// writes the copy left; the lost block is never written.

const redundantAt = ubootenv.RedundantOffset

// putCopy writes env as one redundant copy with flag at offset of dev.
func (r *rawNAND) putCopy(t *testing.T, dev string, offset int, env *ubootenv.Env, flag byte) {
	t.Helper()
	image, err := env.EncodeCopy(flag)
	if err != nil {
		t.Fatal(err)
	}
	copy(r.nand[dev][offset:], image)
}

// copyAt parses the redundant copy at offset of dev.
func (r *rawNAND) copyAt(t *testing.T, dev string, offset int) (*ubootenv.Env, byte) {
	t.Helper()
	env, flag, err := ubootenv.ParseCopy(r.nand[dev][offset : offset+ubootenv.Size])
	if err != nil {
		t.Fatalf("%s+%#x: %v", dev, offset, err)
	}
	return env, flag
}

func runningEnv(t *testing.T) *ubootenv.Env {
	t.Helper()
	env := factoryEnv(t)
	if err := SelectSlot(env, pong, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	return env
}

func untouched(t *testing.T, r *rawNAND, dev string, offset int) {
	t.Helper()
	if !bytes.Equal(r.nand[dev][offset:offset+0x20000], bytes.Repeat([]byte{0xff}, 0x20000)) {
		t.Errorf("the bad block at %s+%#x was written", dev, offset)
	}
	for _, c := range r.cmds {
		if (strings.HasPrefix(c, "nandwrite") || strings.HasPrefix(c, "fw_setenv")) && strings.Contains(c, dev) &&
			(offset == 0 || strings.Contains(c, "-s 1835008")) {
			t.Errorf("a write aimed at the bad block: %q", c)
		}
	}
}

func TestLostRedundantCopyLeavesThePrimary(t *testing.T) {
	r := newRawNAND()
	r.bad = map[string]int{"/dev/mtd4": redundantAt}
	r.putCopy(t, "/dev/mtd5", 0, runningEnv(t), 7)
	in := r.installer(true)

	if wrote, err := in.RepairEnv(r.read, "binky"); err != nil || wrote {
		t.Fatalf("repair of a primary that checks out: wrote %v, err %v", wrote, err)
	}
	env, err := in.Env(r.read)
	if err != nil {
		t.Fatal(err)
	}
	if got := SelectedSlot(env); got != "PONG" {
		t.Fatalf("read %q, want the running PONG", got)
	}
	if err := in.SelectBootSlot(r.read, ping, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	got, flag := r.copyAt(t, "/dev/mtd5", 0)
	if SelectedSlot(got) != "PING" || flag != 8 {
		t.Errorf("primary selects %q with flag %d, want PING with 8", SelectedSlot(got), flag)
	}
	if err := in.SetEnvEverywhere(r.read, "soundtap_key", "abc"); err != nil {
		t.Fatal(err)
	}
	if got, flag := r.copyAt(t, "/dev/mtd5", 0); flag != 9 {
		t.Errorf("flag %d after the key, want 9", flag)
	} else if v, _ := got.Get("soundtap_key"); v != "abc" {
		t.Errorf("soundtap_key = %q", v)
	}
	writes := len(r.cmds)
	if err := in.SelectBootSlot(r.read, ping, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.cmds[writes:] {
		if strings.HasPrefix(c, "flash_erase") || strings.HasPrefix(c, "nandwrite") {
			t.Errorf("a selection that changes nothing wrote: %q", c)
		}
	}
	untouched(t, r, "/dev/mtd4", redundantAt)
}

// The single 8 KiB copy of a speaker before our first write, with the redundant block bad:
// our U-Boot reads it (CONFIG_ENV_REDUNDANT_UPGRADE), and so does soundtapd; the repair
// converts it into the one redundant copy left.
func TestLostRedundantCopyConvertsTheSingleCopy(t *testing.T) {
	r := newRawNAND()
	r.bad = map[string]int{"/dev/mtd4": redundantAt}
	r.putLegacy(t, runningEnv(t))
	in := r.installer(true)

	env, err := in.Env(r.read)
	if err != nil || SelectedSlot(env) != "PONG" {
		t.Fatalf("single copy: %v, err %v", env, err)
	}
	if wrote, err := in.RepairEnv(r.read, "binky"); err != nil || !wrote {
		t.Fatalf("conversion: wrote %v, err %v", wrote, err)
	}
	if got, flag := r.copyAt(t, "/dev/mtd5", 0); SelectedSlot(got) != "PONG" || flag != ubootenv.FreshPrimaryFlag {
		t.Errorf("primary selects %q with flag %d", SelectedSlot(got), flag)
	}
	untouched(t, r, "/dev/mtd4", redundantAt)
}

// With the primary block bad, the redundant copy carries the environment, and the way
// back to stock refuses before it writes: Bose's U-Boot reads the primary alone.
func TestLostPrimaryCopy(t *testing.T) {
	r := newRawNAND()
	r.bad = map[string]int{"/dev/mtd5": 0}
	r.putCopy(t, "/dev/mtd4", redundantAt, runningEnv(t), 3)
	in := r.installer(true)

	if err := in.SelectBootSlot(r.read, ping, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	if got, flag := r.copyAt(t, "/dev/mtd4", redundantAt); SelectedSlot(got) != "PING" || flag != 4 {
		t.Errorf("redundant copy selects %q with flag %d", SelectedSlot(got), flag)
	}
	writes := len(r.cmds)
	if err := in.UseLegacyEnv(r.read); err == nil {
		t.Fatal("the way back to stock wrote a legacy copy onto a bad block")
	}
	for _, c := range r.cmds[writes:] {
		if strings.HasPrefix(c, "flash_erase") || strings.HasPrefix(c, "nandwrite") {
			t.Errorf("the refused way back wrote: %q", c)
		}
	}
	untouched(t, r, "/dev/mtd5", 0)
}

// The way back to stock with the redundant block bad writes the single copy and leaves the
// bad block alone.
func TestUseLegacyEnvWithLostRedundantCopy(t *testing.T) {
	r := newRawNAND()
	r.bad = map[string]int{"/dev/mtd4": redundantAt}
	r.putCopy(t, "/dev/mtd5", 0, runningEnv(t), 5)
	in := r.installer(true)
	if err := in.UseLegacyEnv(r.read); err != nil {
		t.Fatal(err)
	}
	env, err := ubootenv.ParseLegacy(r.nand["/dev/mtd5"])
	if err != nil || SelectedSlot(env) != "PONG" {
		t.Fatalf("single copy: %v, err %v", env, err)
	}
	untouched(t, r, "/dev/mtd4", redundantAt)
}

func TestBothCopiesLost(t *testing.T) {
	r := newRawNAND()
	r.bad = map[string]int{"/dev/mtd4": redundantAt, "/dev/mtd5": 0}
	in := r.installer(true)
	if _, err := in.RepairEnv(r.read, "binky"); err == nil || !strings.Contains(err.Error(), "both copies") {
		t.Fatalf("repair: %v", err)
	}
	if _, err := in.Env(r.read); err == nil {
		t.Fatal("read an environment with both copies on bad blocks")
	}
}
