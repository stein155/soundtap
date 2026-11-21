// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package rootd

import (
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// table is the speakers' partition table (CONTRIBUTING.md, NAND and boot contract).
func table() map[string]flash.Partition {
	return map[string]flash.Partition{
		"NAND.SPL":                {Index: 0, Size: 0x20000, EraseSize: 0x20000},
		"NAND.SPL.backup1":        {Index: 1, Size: 0x20000, EraseSize: 0x20000},
		"NAND.SPL.backup2":        {Index: 2, Size: 0x20000, EraseSize: 0x20000},
		"NAND.SPL.backup3":        {Index: 3, Size: 0x20000, EraseSize: 0x20000},
		"NAND.U-Boot":             {Index: 4, Size: 0x1e0000, EraseSize: 0x20000},
		"NAND.U-Boot-Env":         {Index: 5, Size: 0x20000, EraseSize: 0x20000},
		"NAND.Kernel-Ping":        {Index: 6, Size: 0x500000, EraseSize: 0x20000},
		"NAND.Kernel-Pong":        {Index: 7, Size: 0x500000, EraseSize: 0x20000},
		"NAND.File-System-Ping":   {Index: 8, Size: 0x6000000, EraseSize: 0x20000},
		"NAND.File-System-Pong":   {Index: 9, Size: 0x6000000, EraseSize: 0x20000},
		"NAND.Persistent-Storage": {Index: 10, Size: 0x2600000, EraseSize: 0x20000},
		"NAND.Update-Staging":     {Index: 11, Size: 0xb80000, EraseSize: 0x20000},
		"NAND.Manufacturing-Data": {Index: 12, Size: 0x40000, EraseSize: 0x20000},
	}
}

const pingBoot = "console=ttyGS0 root=ubi0:rootfs ro ubi.mtd=8,2048 soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048"

// The running slot, the persistent volume, update staging and manufacturing data are
// never written, whatever the request; the slot that is not running is.
func TestLayoutHoldsTheNANDContract(t *testing.T) {
	l := NewLayout(pingBoot, table())
	for _, mtd := range []int{6, 8, 10, 11, 12, 13} {
		if err := l.checkErase(Request{MTD: mtd}); err == nil {
			t.Errorf("erase of mtd%d allowed", mtd)
		}
		if err := l.checkWrite(Request{MTD: mtd}, 0x1000); err == nil {
			t.Errorf("write to mtd%d allowed", mtd)
		}
		if err := l.checkFormat(Request{MTD: mtd, Size: 0x1000}); err == nil {
			t.Errorf("ubiformat of mtd%d allowed", mtd)
		}
		if err := l.checkAttach(Request{MTD: mtd}); err == nil {
			t.Errorf("ubiattach of mtd%d allowed", mtd)
		}
	}
	for _, mtd := range []int{0, 1, 2, 3, 4, 5, 7} {
		if err := l.checkErase(Request{MTD: mtd}); err != nil {
			t.Errorf("erase of mtd%d refused: %v", mtd, err)
		}
		if err := l.checkWrite(Request{MTD: mtd, Pad: true}, 0x1000); err != nil {
			t.Errorf("write to mtd%d refused: %v", mtd, err)
		}
	}
	if err := l.checkFormat(Request{MTD: 9, Size: 0x100000}); err != nil {
		t.Errorf("ubiformat of the other rootfs refused: %v", err)
	}
	if err := l.checkFormat(Request{MTD: 7, Size: 0x100000}); err == nil {
		t.Error("ubiformat of a kernel partition allowed")
	}
	if err := l.checkAttach(Request{MTD: 9}); err != nil {
		t.Errorf("attaching the written rootfs refused: %v", err)
	}
}

// A NAND boot whose slot cannot be told writes neither; a RAM boot runs neither, and the
// recovery plan may write either.
func TestLayoutWithoutARunningSlot(t *testing.T) {
	unknown := NewLayout("root=ubi0:rootfs soundtap.nandroot=1", table())
	for _, mtd := range []int{6, 7} {
		if err := unknown.checkErase(Request{MTD: mtd}); err == nil {
			t.Errorf("unknown slot: erase of mtd%d allowed", mtd)
		}
	}
	if err := unknown.checkErase(Request{MTD: 5}); err != nil {
		t.Errorf("unknown slot: the environment refused: %v", err)
	}
	ram := NewLayout("console=ttyS3 root=/dev/ram0", table())
	for _, mtd := range []int{6, 7} {
		if err := ram.checkErase(Request{MTD: mtd}); err != nil {
			t.Errorf("RAM boot: erase of mtd%d refused: %v", mtd, err)
		}
	}
	if err := ram.checkErase(Request{MTD: 10}); err == nil {
		t.Error("RAM boot: erase of the persistent volume allowed")
	}
}

func TestLayoutHoldsRangesToThePartition(t *testing.T) {
	l := NewLayout(pingBoot, table())
	for _, r := range []Request{
		{MTD: 5, Offset: 0x1000},              // not a block
		{MTD: 5, Offset: 0x20000},             // past the end
		{MTD: 5, Blocks: 2},                   // runs past
		{MTD: 4, Offset: -0x20000},            // negative
		{MTD: 4, Offset: 0x1c0000, Blocks: 2}, // past the end
		{MTD: 7, Offset: 0x500000 - 0x20000, Blocks: 2},
	} {
		if err := l.checkErase(r); err == nil {
			t.Errorf("erase %+v allowed", r)
		}
	}
	if err := l.checkErase(Request{MTD: 4, Offset: 0x1c0000, Blocks: 1}); err != nil {
		t.Errorf("the redundant environment's block refused: %v", err)
	}
	if err := l.checkWrite(Request{MTD: 7}, 0x500001); err == nil {
		t.Error("an image larger than the kernel partition allowed")
	}
	if err := l.checkWrite(Request{MTD: 7}, 0); err == nil {
		t.Error("an empty write allowed")
	}
	if err := l.checkBlock(Request{MTD: 4, Offset: 0x100000, Skip: 1, Size: 0x20000}, 0x40000); err != nil {
		t.Errorf("a U-Boot block refused: %v", err)
	}
	for _, r := range []Request{
		{MTD: 4, Offset: 0x100000, Skip: 2, Size: 0x20000}, // not in the input
		{MTD: 4, Offset: 0x100000, Size: 0x800},            // not the erase block
		{MTD: 4, Offset: 0x1e0000, Size: 0x20000},          // past the end
		{MTD: 7, Size: 0x20000},                            // dd into a kernel
		{MTD: 5, Size: 0x20000},                            // dd into the environment
	} {
		if err := l.checkBlock(r, 0x40000); err == nil {
			t.Errorf("block write %+v allowed", r)
		}
	}
}

func TestEnvCopiesAreTheEnvironmentsPlaces(t *testing.T) {
	l := NewLayout(pingBoot, table())
	ok := [][]EnvCopy{
		{{MTD: 5, Size: ubootenv.LegacySize, EraseSize: 0x20000}},
		{{MTD: 5, Size: ubootenv.Size, EraseSize: 0x20000}, {MTD: 4, Offset: ubootenv.RedundantOffset, Size: ubootenv.Size, EraseSize: 0x20000}},
	}
	for _, c := range ok {
		if err := l.checkEnvCopies(c); err != nil {
			t.Errorf("%+v refused: %v", c, err)
		}
	}
	bad := [][]EnvCopy{
		nil,
		{{MTD: 8, Size: ubootenv.Size, EraseSize: 0x20000}},                  // the running rootfs
		{{MTD: 7, Size: ubootenv.Size, EraseSize: 0x20000}},                  // a kernel
		{{MTD: 4, Size: ubootenv.Size, EraseSize: 0x20000}},                  // U-Boot itself
		{{MTD: 5, Offset: 0x20000, Size: ubootenv.Size, EraseSize: 0x20000}}, // outside
		{{MTD: 5, Size: 0x4000, EraseSize: 0x20000}},                         // no such size
		{{MTD: 5, Size: ubootenv.Size, EraseSize: 0x800}},                    // wrong block
		{{MTD: 5, Size: ubootenv.Size, EraseSize: 0x20000}, {MTD: 5, Size: ubootenv.Size, EraseSize: 0x20000}, {MTD: 5, Size: ubootenv.Size, EraseSize: 0x20000}},
	}
	for _, c := range bad {
		if err := l.checkEnvCopies(c); err == nil {
			t.Errorf("%+v allowed", c)
		}
	}
}

func TestEnvScriptKeys(t *testing.T) {
	for _, ok := range []string{
		"bootcmd run nand_boot2;\nupgrade_available 1\nbootcount 0\n",
		"altbootcmd\nbootlimit\n", // deletions
		"soundtap_owner_key AAAA\n",
		"bootcmd=run nand_boot;\nupgrade_available=\n", // libubootenv's name=value form
	} {
		if err := CheckEnvScript([]byte(ok)); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"",
		"bootcmd run nand_boot2;\nbootargs init=/bin/sh\n",
		"ethaddr 02:00:00:00:00:01\n",
		"bootargs=init=/bin/sh\n",
		"bootcmd=x\npreboot=reset\n",
		"\tpreboot reset\n",
		"bootcmd x\x00y\n",
	} {
		if err := CheckEnvScript([]byte(bad)); err == nil {
			t.Errorf("%q allowed", bad)
		}
	}
}

func TestHostnameIsOneLabel(t *testing.T) {
	for _, ok := range []string{"kitchen", "soundtap-a1b2c3", "x"} {
		if err := CheckHostname(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "x-", "Kitchen", "a.b", "a b", strings.Repeat("a", 64), "x\n"} {
		if err := CheckHostname(bad); err == nil {
			t.Errorf("%q allowed", bad)
		}
	}
}
