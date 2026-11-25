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

	err := SelectSlot(env, pong, SoundTapMarkers(""))
	if err != nil {
		t.Fatal(err)
	}

	return env
}

func untouched(t *testing.T, fake *rawNAND, dev string, offset int) {
	t.Helper()

	if !bytes.Equal(fake.nand[dev][offset:offset+0x20000], bytes.Repeat([]byte{0xff}, 0x20000)) {
		t.Errorf("the bad block at %s+%#x was written", dev, offset)
	}

	for _, c := range fake.cmds {
		if (strings.HasPrefix(c, "nandwrite") || strings.HasPrefix(c, "fw_setenv")) && strings.Contains(c, dev) &&
			(offset == 0 || strings.Contains(c, "-s 1835008")) {
			t.Errorf("a write aimed at the bad block: %q", c)
		}
	}
}

func TestLostRedundantCopyLeavesThePrimary(t *testing.T) {
	t.Parallel()

	fake := newRawNAND()
	fake.bad = map[string]int{"/dev/mtd4": redundantAt}
	fake.putCopy(t, "/dev/mtd5", 0, runningEnv(t), 7)
	flasher := fake.installer(true)

	wrote, err := flasher.RepairEnv(fake.read, "binky")
	if err != nil || wrote {
		t.Fatalf("repair of a primary that checks out: wrote %v, err %v", wrote, err)
	}

	env, err := flasher.Env(fake.read)
	if err != nil {
		t.Fatal(err)
	}

	if got := SelectedSlot(env); got != "PONG" {
		t.Fatalf("read %q, want the running PONG", got)
	}

	err = flasher.SelectBootSlot(fake.read, ping, SoundTapMarkers(""))
	if err != nil {
		t.Fatal(err)
	}

	got, flag := fake.copyAt(t, "/dev/mtd5", 0)
	if SelectedSlot(got) != "PING" || flag != 8 {
		t.Errorf("primary selects %q with flag %d, want PING with 8", SelectedSlot(got), flag)
	}

	err = flasher.SelectBootSlot(fake.read, pong, SoundTapMarkers(""))
	if err != nil {
		t.Fatal(err)
	}

	if got, flag := fake.copyAt(t, "/dev/mtd5", 0); flag != 9 {
		t.Errorf("flag %d after the second selection, want 9", flag)
	} else if SelectedSlot(got) != "PONG" {
		t.Errorf("primary selects %q, want PONG", SelectedSlot(got))
	}

	writes := len(fake.cmds)

	err = flasher.SelectBootSlot(fake.read, pong, SoundTapMarkers(""))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range fake.cmds[writes:] {
		if strings.HasPrefix(c, "flash_erase") || strings.HasPrefix(c, "nandwrite") {
			t.Errorf("a selection that changes nothing wrote: %q", c)
		}
	}

	untouched(t, fake, "/dev/mtd4", redundantAt)
}

// The single 8 KiB copy of a speaker before our first write, with the redundant block bad:
// our U-Boot reads it (CONFIG_ENV_REDUNDANT_UPGRADE), and so does soundtapd; the repair
// converts it into the one redundant copy left.
func TestLostRedundantCopyConvertsTheSingleCopy(t *testing.T) {
	t.Parallel()

	fake := newRawNAND()
	fake.bad = map[string]int{"/dev/mtd4": redundantAt}
	fake.putLegacy(t, runningEnv(t))
	flasher := fake.installer(true)

	env, err := flasher.Env(fake.read)
	if err != nil || SelectedSlot(env) != "PONG" {
		t.Fatalf("single copy: %v, err %v", env, err)
	}

	wrote, err := flasher.RepairEnv(fake.read, "binky")
	if err != nil || !wrote {
		t.Fatalf("conversion: wrote %v, err %v", wrote, err)
	}

	if got, flag := fake.copyAt(t, "/dev/mtd5", 0); SelectedSlot(got) != "PONG" || flag != ubootenv.FreshPrimaryFlag {
		t.Errorf("primary selects %q with flag %d", SelectedSlot(got), flag)
	}

	untouched(t, fake, "/dev/mtd4", redundantAt)
}

// With the primary block bad, the redundant copy carries the environment, and the way
// back to stock refuses before it writes: Bose's U-Boot reads the primary alone.
func TestLostPrimaryCopy(t *testing.T) {
	t.Parallel()

	fake := newRawNAND()
	fake.bad = map[string]int{"/dev/mtd5": 0}
	fake.putCopy(t, "/dev/mtd4", redundantAt, runningEnv(t), 3)
	flasher := fake.installer(true)

	err := flasher.SelectBootSlot(fake.read, ping, SoundTapMarkers(""))
	if err != nil {
		t.Fatal(err)
	}

	if got, flag := fake.copyAt(t, "/dev/mtd4", redundantAt); SelectedSlot(got) != "PING" || flag != 4 {
		t.Errorf("redundant copy selects %q with flag %d", SelectedSlot(got), flag)
	}

	writes := len(fake.cmds)

	err = flasher.UseLegacyEnv(fake.read)
	if err == nil {
		t.Fatal("the way back to stock wrote a legacy copy onto a bad block")
	}

	for _, c := range fake.cmds[writes:] {
		if strings.HasPrefix(c, "flash_erase") || strings.HasPrefix(c, "nandwrite") {
			t.Errorf("the refused way back wrote: %q", c)
		}
	}

	untouched(t, fake, "/dev/mtd5", 0)
}

// The way back to stock with the redundant block bad writes the single copy and leaves the
// bad block alone.
func TestUseLegacyEnvWithLostRedundantCopy(t *testing.T) {
	t.Parallel()

	fake := newRawNAND()
	fake.bad = map[string]int{"/dev/mtd4": redundantAt}
	fake.putCopy(t, "/dev/mtd5", 0, runningEnv(t), 5)

	in := fake.installer(true)

	err := in.UseLegacyEnv(fake.read)
	if err != nil {
		t.Fatal(err)
	}

	env, err := ubootenv.ParseLegacy(fake.nand["/dev/mtd5"])
	if err != nil || SelectedSlot(env) != "PONG" {
		t.Fatalf("single copy: %v, err %v", env, err)
	}

	untouched(t, fake, "/dev/mtd4", redundantAt)
}

func TestBothCopiesLost(t *testing.T) {
	t.Parallel()

	fake := newRawNAND()
	fake.bad = map[string]int{"/dev/mtd4": redundantAt, "/dev/mtd5": 0}

	flasher := fake.installer(true)

	_, err := flasher.RepairEnv(fake.read, "binky")
	if err == nil || !strings.Contains(err.Error(), "both copies") {
		t.Fatalf("repair: %v", err)
	}

	_, err = flasher.Env(fake.read)
	if err == nil {
		t.Fatal("read an environment with both copies on bad blocks")
	}
}
