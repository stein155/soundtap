// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"bytes"
	"fmt"
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
	}, cmds: nil, cutAfter: 0, bad: nil}
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
		data, err := os.ReadFile(args[len(args)-1])
		if err != nil {
			return fmt.Errorf("nandwrite: %w", err)
		}

		if r.isBad(args[len(args)-2], intArg(args, "-s")) {
			return io.ErrShortWrite
		}

		copy(r.nand[args[len(args)-2]][intArg(args, "-s"):], data)
	case "fw_setenv":
		if r.copyOnBadBlock(args) {
			return io.ErrUnexpectedEOF // libubootenv: -EIO
		}

		err := ubootenvtest.SetEnv(r.nand, args)
		if err != nil {
			return fmt.Errorf("fw_setenv: %w", err)
		}
	default: // the other commands change nothing the fake holds
	}

	return nil
}

func (r *rawNAND) read(name string, args ...string) ([]byte, error) {
	if name == "fw_printenv" {
		if r.copyOnBadBlock(args) {
			return nil, io.ErrUnexpectedEOF // libubootenv: -EIO
		}

		out, err := ubootenvtest.PrintEnv(r.nand, args)
		if err != nil {
			return nil, fmt.Errorf("fw_printenv: %w", err)
		}

		return out, nil
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
		Target: Target{
			Running: pong, Alternate: ping, EnvMTD: 5, EnvSize: 0x20000, EnvEraseSize: 0x20000,
			EnvRedundant: redundant, BootloaderSizes: map[int]int64{4: 0x1e0000},
			BootloaderEraseSizes: nil, PersistentName: "", BootCount: false,
		},
		Run: r.run, Open: nil, written: nil,
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
	t.Parallel()

	for _, redundant := range []bool{false, true} {
		t.Run("redundant="+strconv.FormatBool(redundant), func(t *testing.T) {
			t.Parallel()

			fake := newRawNAND()
			copy(fake.nand["/dev/mtd5"], bytes.Repeat([]byte{0x5a}, 0x8000)) // garbage, no CRC

			flasher := fake.installer(redundant)

			wrote, err := flasher.RepairEnv(fake.read, "binky")
			if err != nil || !wrote {
				t.Fatalf("repair: wrote %v, err %v", wrote, err)
			}

			env := fake.env(t)
			if got := SelectedSlot(env); got != "PONG" {
				t.Fatalf("selected %q, want the running PONG", got)
			}

			want, _ := MainlineRecipes("binky")
			if got, _ := env.Get("slot_boot"); "slot_boot="+got != want[2] {
				t.Errorf("slot_boot = %q", got)
			}

			primary := fake.nand["/dev/mtd5"][:ubootenv.Size]

			redundantCopy := fake.nand["/dev/mtd4"][ubootenv.RedundantOffset : ubootenv.RedundantOffset+ubootenv.Size]

			_, flag, err := ubootenv.ParseCopy(redundantCopy)
			if redundant != (err == nil) || (redundant && flag != ubootenv.FreshRedundantFlag) {
				t.Errorf("redundant copy: flag %d, err %v", flag, err)
			}

			_, flag, err = ubootenv.ParseCopy(primary)
			if redundant != (err == nil) || (redundant && flag != ubootenv.FreshPrimaryFlag) {
				t.Errorf("primary copy: flag %d, err %v", flag, err)
			}

			redundantErase := slices.Index(fake.cmds, "flash_erase /dev/mtd4 1835008 1")
			if redundant && redundantErase > slices.Index(fake.cmds, "flash_erase /dev/mtd5 0 1") {
				t.Errorf("the primary was erased before the redundant copy was written: %q", fake.cmds)
			}

			writes := len(fake.cmds)

			wrote, err = flasher.RepairEnv(fake.read, "binky")
			if err != nil || wrote || len(fake.cmds) != writes {
				t.Fatalf("second repair: wrote %v, err %v, commands %q", wrote, err, fake.cmds[writes:])
			}
		})
	}
}

// Ours from before the redundant environment, or Bose's, left the single copy: it is
// converted with every entry, not rebuilt, and nothing is written once it is.
func TestRepairEnvConvertsTheSingleCopy(t *testing.T) {
	t.Parallel()

	fake := newRawNAND()
	old := factoryEnv(t)
	old.Set("country_code", "NL")
	fake.putLegacy(t, old)

	flasher := fake.installer(true)

	wrote, err := flasher.RepairEnv(fake.read, "binky")
	if err != nil || !wrote {
		t.Fatalf("repair: wrote %v, err %v", wrote, err)
	}

	if env := fake.env(t); !env.Equal(old) {
		t.Errorf("converted environment differs: %q", env.Text())
	}
	// One copy torn after that: libubootenv reads the other, so nothing is rebuilt.
	copy(fake.nand["/dev/mtd5"][100:], "torn")

	n := len(fake.cmds)

	wrote, err = flasher.RepairEnv(fake.read, "binky")
	if err != nil || wrote || len(fake.cmds) != n {
		t.Fatalf("repair with one good copy: wrote %v, err %v", wrote, err)
	}

	env, err := flasher.Env(fake.read)
	if err != nil || !env.Equal(old) {
		t.Fatalf("fw_printenv over one good copy: %v", err)
	}
	// And the next write goes to the torn copy, with the newer flag.
	err = flasher.SelectBootSlot(fake.read, ping, SoundTapMarkers(""))
	if err != nil {
		t.Fatal(err)
	}

	_, flag, err := ubootenv.ParseCopy(fake.nand["/dev/mtd5"])
	if err != nil || flag != ubootenv.FreshRedundantFlag+1 {
		t.Errorf("the torn primary was rewritten with flag %d, err %v", flag, err)
	}
}

// Under a U-Boot that reads the single copy, a redundant copy that checks out is left over
// from one that read it, and the next U-Boot that reads it would boot it.
func TestRepairEnvErasesAStaleRedundantCopy(t *testing.T) {
	t.Parallel()

	fake := newRawNAND()
	stale := factoryEnv(t)
	stale.Set("bootcmd", bootcmdPong)

	err := ubootenvtest.Image(fake.nand, "/dev/mtd5", "/dev/mtd4", stale)
	if err != nil {
		t.Fatal(err)
	}

	fake.putLegacy(t, factoryEnv(t))

	wrote, err := fake.installer(false).RepairEnv(fake.read, "binky")
	if err != nil || !wrote {
		t.Fatalf("repair: wrote %v, err %v", wrote, err)
	}

	if !bytes.Equal(fake.nand["/dev/mtd4"][ubootenv.RedundantOffset:], bytes.Repeat([]byte{0xff}, 0x20000)) {
		t.Error("the stale redundant copy is still there")
	}

	if got := SelectedSlot(fake.env(t)); got != "PING" {
		t.Errorf("selected %q, want the single copy's PING", got)
	}
}

// The way back to stock: the single copy Bose's U-Boot reads, every entry kept, the
// redundant copy erased, later edits in that format; at every step a copy boots that
// holds the same entries.
func TestUseLegacyEnvKeepsEveryEntry(t *testing.T) {
	t.Parallel()

	want := factoryEnv(t)

	for cut := 1; ; cut++ {
		fake := newRawNAND()

		err := ubootenvtest.Image(fake.nand, "/dev/mtd5", "/dev/mtd4", want)
		if err != nil {
			t.Fatal(err)
		}

		fake.cutAfter = cut
		flasher := fake.installer(true)

		err = flasher.UseLegacyEnv(fake.read)
		if got := fake.env(t); !got.Equal(want) {
			t.Fatalf("a cut after %d commands boots %q", cut, got.Text())
		}

		if err != nil {
			if !flasher.Target.EnvRedundant {
				t.Fatal("a failed conversion switched to the single copy")
			}

			continue
		}

		if flasher.Target.EnvRedundant {
			t.Fatal("the installer still writes the redundant environment")
		}

		_, err = ubootenv.ParseLegacy(fake.nand["/dev/mtd5"])
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(fake.nand["/dev/mtd4"][ubootenv.RedundantOffset:], bytes.Repeat([]byte{0xff}, 0x20000)) {
			t.Error("the redundant copy is still there")
		}

		err = flasher.InstallStockRecipes(fake.read)
		if err != nil {
			t.Fatal(err)
		}

		_, err = ubootenv.ParseLegacy(fake.nand["/dev/mtd5"])
		if err != nil {
			t.Fatalf("the stock recipes were not written as the single copy: %v", err)
		}

		return
	}
}

// An environment that does not fit Bose's 8 KiB is refused before anything is written.
func TestUseLegacyEnvRefusesWhatDoesNotFit(t *testing.T) {
	t.Parallel()

	fake := newRawNAND()
	big := factoryEnv(t)
	big.Set("owner_key", strings.Repeat("k", ubootenv.LegacySize))

	err := ubootenvtest.Image(fake.nand, "/dev/mtd5", "/dev/mtd4", big)
	if err != nil {
		t.Fatal(err)
	}

	in := fake.installer(true)

	err = in.UseLegacyEnv(fake.read)
	if err == nil {
		t.Fatal("an environment larger than 8 KiB was converted")
	}

	for _, c := range fake.cmds {
		if !strings.HasPrefix(c, "fw_printenv") {
			t.Errorf("ran %q", c)
		}
	}
}
