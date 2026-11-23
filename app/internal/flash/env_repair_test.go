// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"bytes"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/ubootenv"
	"github.com/stein155/soundtap/app/internal/ubootenv/ubootenvtest"
)

// rawNAND is mtd4 and mtd5 with the mtd-utils and libubootenv the environment code runs:
// flash_erase by block, nandwrite and nanddump with -s and -l, fw_printenv and fw_setenv.
// cutAfter, when set, fails every command once that many have run, as a power cut would
// leave the flash. bad holds bad erase blocks, by device and offset, as the tools meet them:
// nanddump --bb=skipbad reads nothing there, nandwrite fails, flash_erase skips it and
// libubootenv refuses an environment with a copy in one.
type rawNAND struct {
	nand     map[string][]byte
	cmds     []string
	cutAfter int
	bad      map[string]int
}

func (r *rawNAND) isBad(dev string, offset int) bool {
	at, ok := r.bad[dev]
	return ok && offset >= at && offset < at+0x20000
}

func (r *rawNAND) copyOnBadBlock(args []string) bool {
	copies, err := ubootenvtest.Config(args)
	if err != nil {
		return false
	}
	for _, c := range copies {
		if r.isBad(c.Dev, c.Offset) {
			return true
		}
	}
	return false
}

func newRawNAND() *rawNAND {
	return &rawNAND{nand: map[string][]byte{
		"/dev/mtd4": bytes.Repeat([]byte{0xff}, 0x1e0000),
		"/dev/mtd5": bytes.Repeat([]byte{0xff}, 0x20000),
	}}
}

func intArg(args []string, flag string) int {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		n, _ := strconv.Atoi(args[i+1])
		return n
	}
	return 0
}

func (r *rawNAND) run(_ io.Reader, name string, args ...string) error {
	if r.cutAfter > 0 && len(r.cmds) >= r.cutAfter {
		return io.ErrUnexpectedEOF
	}
	r.cmds = append(r.cmds, name+" "+strings.Join(args, " "))
	switch name {
	case "flash_erase":
		dev := r.nand[args[0]]
		start, _ := strconv.Atoi(args[1])
		blocks, _ := strconv.Atoi(args[2])
		if r.isBad(args[0], start) {
			return nil // "Skipping bad block"
		}
		copy(dev[start:start+blocks*0x20000], bytes.Repeat([]byte{0xff}, blocks*0x20000))
	case "nandwrite":
		b, err := os.ReadFile(args[len(args)-1])
		if err != nil {
			return err
		}
		if r.isBad(args[len(args)-2], intArg(args, "-s")) {
			return io.ErrShortWrite
		}
		copy(r.nand[args[len(args)-2]][intArg(args, "-s"):], b)
	case "fw_setenv":
		if r.copyOnBadBlock(args) {
			return io.ErrUnexpectedEOF // libubootenv: -EIO
		}
		return ubootenvtest.SetEnv(r.nand, args)
	}
	return nil
}

func (r *rawNAND) read(name string, args ...string) ([]byte, error) {
	if name == "fw_printenv" {
		if r.copyOnBadBlock(args) {
			return nil, io.ErrUnexpectedEOF // libubootenv: -EIO
		}
		return ubootenvtest.PrintEnv(r.nand, args)
	}
	dev := r.nand[args[len(args)-1]]
	off := intArg(args, "-s")
	if r.isBad(args[len(args)-1], off) {
		return nil, nil // --bb=skipbad, and no block after it in the partition
	}
	return append([]byte(nil), dev[off:off+intArg(args, "-l")]...), nil
}

func (r *rawNAND) installer(redundant bool) *Installer {
	return &Installer{
		Target: Target{Running: pong, Alternate: ping, EnvMTD: 5, EnvSize: 0x20000, EnvEraseSize: 0x20000,
			EnvRedundant: redundant, BootloaderSizes: map[int]int64{4: 0x1e0000}},
		Run: r.run,
	}
}

func (r *rawNAND) env(t *testing.T) *ubootenv.Env {
	t.Helper()
	env, err := ubootenvtest.Load(r.nand, "/dev/mtd5", "/dev/mtd4")
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func (r *rawNAND) putLegacy(t *testing.T, env *ubootenv.Env) {
	t.Helper()
	image, err := env.EncodeLegacy()
	if err != nil {
		t.Fatal(err)
	}
	copy(r.nand["/dev/mtd5"], image)
}

// A torn write left nothing that checks out: the repair writes our recipes and the running
// slot, after which the environment parses and selects it. A good one is left alone. Both
// U-Boots: the one that reads the single 8 KiB copy gets that, ours gets both copies.
func TestRepairEnvRebuildsABadCRC(t *testing.T) {
	for _, redundant := range []bool{false, true} {
		t.Run("redundant="+strconv.FormatBool(redundant), func(t *testing.T) {
			r := newRawNAND()
			copy(r.nand["/dev/mtd5"], bytes.Repeat([]byte{0x5a}, 0x8000)) // garbage, no CRC
			in := r.installer(redundant)
			if wrote, err := in.RepairEnv(r.read, "binky"); err != nil || !wrote {
				t.Fatalf("repair: wrote %v, err %v", wrote, err)
			}
			env := r.env(t)
			if got := SelectedSlot(env); got != "PONG" {
				t.Fatalf("selected %q, want the running PONG", got)
			}
			want, _ := MainlineRecipes("binky")
			if got, _ := env.Get("slot_boot"); "slot_boot="+got != want[2] {
				t.Errorf("slot_boot = %q", got)
			}
			primary := r.nand["/dev/mtd5"][:ubootenv.Size]
			redundantCopy := r.nand["/dev/mtd4"][ubootenv.RedundantOffset : ubootenv.RedundantOffset+ubootenv.Size]
			if _, flag, err := ubootenv.ParseCopy(redundantCopy); redundant != (err == nil) || (redundant && flag != ubootenv.FreshRedundantFlag) {
				t.Errorf("redundant copy: flag %d, err %v", flag, err)
			}
			if _, flag, err := ubootenv.ParseCopy(primary); redundant != (err == nil) || (redundant && flag != ubootenv.FreshPrimaryFlag) {
				t.Errorf("primary copy: flag %d, err %v", flag, err)
			}
			if redundant && slices.Index(r.cmds, "flash_erase /dev/mtd4 1835008 1") > slices.Index(r.cmds, "flash_erase /dev/mtd5 0 1") {
				t.Errorf("the primary was erased before the redundant copy was written: %q", r.cmds)
			}
			writes := len(r.cmds)
			if wrote, err := in.RepairEnv(r.read, "binky"); err != nil || wrote || len(r.cmds) != writes {
				t.Fatalf("second repair: wrote %v, err %v, commands %q", wrote, err, r.cmds[writes:])
			}
		})
	}
}

// Ours from before the redundant environment, or Bose's, left the single copy: it is
// converted with every entry, not rebuilt, and nothing is written once it is.
func TestRepairEnvConvertsTheSingleCopy(t *testing.T) {
	r := newRawNAND()
	old := factoryEnv(t)
	old.Set("country_code", "NL")
	r.putLegacy(t, old)
	in := r.installer(true)
	if wrote, err := in.RepairEnv(r.read, "binky"); err != nil || !wrote {
		t.Fatalf("repair: wrote %v, err %v", wrote, err)
	}
	if env := r.env(t); !env.Equal(old) {
		t.Errorf("converted environment differs: %q", env.Text())
	}
	// One copy torn after that: libubootenv reads the other, so nothing is rebuilt.
	copy(r.nand["/dev/mtd5"][100:], []byte("torn"))
	n := len(r.cmds)
	if wrote, err := in.RepairEnv(r.read, "binky"); err != nil || wrote || len(r.cmds) != n {
		t.Fatalf("repair with one good copy: wrote %v, err %v", wrote, err)
	}
	if env, err := in.Env(r.read); err != nil || !env.Equal(old) {
		t.Fatalf("fw_printenv over one good copy: %v", err)
	}
	// And the next write goes to the torn copy, with the newer flag.
	if err := in.SelectBootSlot(r.read, ping, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	if _, flag, err := ubootenv.ParseCopy(r.nand["/dev/mtd5"]); err != nil || flag != ubootenv.FreshRedundantFlag+1 {
		t.Errorf("the torn primary was rewritten with flag %d, err %v", flag, err)
	}
}

// Under a U-Boot that reads the single copy, a redundant copy that checks out is left over
// from one that read it, and the next U-Boot that reads it would boot it.
func TestRepairEnvErasesAStaleRedundantCopy(t *testing.T) {
	r := newRawNAND()
	stale := factoryEnv(t)
	stale.Set("bootcmd", bootcmdPong)
	if err := ubootenvtest.Image(r.nand, "/dev/mtd5", "/dev/mtd4", stale); err != nil {
		t.Fatal(err)
	}
	r.putLegacy(t, factoryEnv(t))
	if wrote, err := r.installer(false).RepairEnv(r.read, "binky"); err != nil || !wrote {
		t.Fatalf("repair: wrote %v, err %v", wrote, err)
	}
	if !bytes.Equal(r.nand["/dev/mtd4"][ubootenv.RedundantOffset:], bytes.Repeat([]byte{0xff}, 0x20000)) {
		t.Error("the stale redundant copy is still there")
	}
	if got := SelectedSlot(r.env(t)); got != "PING" {
		t.Errorf("selected %q, want the single copy's PING", got)
	}
}

// The way back to stock: the single copy Bose's U-Boot reads, every entry kept, the
// redundant copy erased, later edits in that format; at every step a copy boots that
// holds the same entries.
func TestUseLegacyEnvKeepsEveryEntry(t *testing.T) {
	want := factoryEnv(t)
	for cut := 1; ; cut++ {
		r := newRawNAND()
		if err := ubootenvtest.Image(r.nand, "/dev/mtd5", "/dev/mtd4", want); err != nil {
			t.Fatal(err)
		}
		r.cutAfter = cut
		in := r.installer(true)
		err := in.UseLegacyEnv(r.read)
		if got := r.env(t); !got.Equal(want) {
			t.Fatalf("a cut after %d commands boots %q", cut, got.Text())
		}
		if err != nil {
			if !in.Target.EnvRedundant {
				t.Fatal("a failed conversion switched to the single copy")
			}
			continue
		}
		if in.Target.EnvRedundant {
			t.Fatal("the installer still writes the redundant environment")
		}
		if _, err := ubootenv.ParseLegacy(r.nand["/dev/mtd5"]); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(r.nand["/dev/mtd4"][ubootenv.RedundantOffset:], bytes.Repeat([]byte{0xff}, 0x20000)) {
			t.Error("the redundant copy is still there")
		}
		if err := in.InstallStockRecipes(r.read); err != nil {
			t.Fatal(err)
		}
		if _, err := ubootenv.ParseLegacy(r.nand["/dev/mtd5"]); err != nil {
			t.Fatalf("the stock recipes were not written as the single copy: %v", err)
		}
		return
	}
}

// An environment that does not fit Bose's 8 KiB is refused before anything is written.
func TestUseLegacyEnvRefusesWhatDoesNotFit(t *testing.T) {
	r := newRawNAND()
	big := factoryEnv(t)
	big.Set("owner_key", strings.Repeat("k", ubootenv.LegacySize))
	if err := ubootenvtest.Image(r.nand, "/dev/mtd5", "/dev/mtd4", big); err != nil {
		t.Fatal(err)
	}
	in := r.installer(true)
	if err := in.UseLegacyEnv(r.read); err == nil {
		t.Fatal("an environment larger than 8 KiB was converted")
	}
	for _, c := range r.cmds {
		if !strings.HasPrefix(c, "fw_printenv") {
			t.Errorf("ran %q", c)
		}
	}
}
