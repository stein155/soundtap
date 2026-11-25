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

const pingBoot = "console=ttyGS0 root=ubi0:rootfs ro ubi.mtd=8,2048 soundtap.nandroot=1 " +
	"ubi.mtd=NAND.Persistent-Storage,2048"

// The running slot, the persistent volume, update staging and manufacturing data are
// never written, whatever the request; the slot that is not running is.
func TestLayoutHoldsTheNANDContract(t *testing.T) {
	t.Parallel()

	line := NewLayout(pingBoot, table())
	for _, mtd := range []int{6, 8, 10, 11, 12, 13} {
		err := line.checkErase(req("", withMTD(mtd)))
		if err == nil {
			t.Errorf("erase of mtd%d allowed", mtd)
		}

		err = line.checkWrite(req("", withMTD(mtd)), 0x1000)
		if err == nil {
			t.Errorf("write to mtd%d allowed", mtd)
		}

		err = line.checkFormat(req("", withMTD(mtd), withSize(0x1000)))
		if err == nil {
			t.Errorf("ubiformat of mtd%d allowed", mtd)
		}

		err = line.checkAttach(req("", withMTD(mtd)))
		if err == nil {
			t.Errorf("ubiattach of mtd%d allowed", mtd)
		}
	}

	for _, mtd := range []int{0, 1, 2, 3, 4, 5, 7} {
		err := line.checkErase(req("", withMTD(mtd)))
		if err != nil {
			t.Errorf("erase of mtd%d refused: %v", mtd, err)
		}

		err = line.checkWrite(req("", withMTD(mtd), withPad()), 0x1000)
		if err != nil {
			t.Errorf("write to mtd%d refused: %v", mtd, err)
		}
	}

	err := line.checkFormat(req("", withMTD(9), withSize(0x100000)))
	if err != nil {
		t.Errorf("ubiformat of the other rootfs refused: %v", err)
	}

	err = line.checkFormat(req("", withMTD(7), withSize(0x100000)))
	if err == nil {
		t.Error("ubiformat of a kernel partition allowed")
	}

	err = line.checkAttach(req("", withMTD(9)))
	if err != nil {
		t.Errorf("attaching the written rootfs refused: %v", err)
	}
}

// A NAND boot whose slot cannot be told writes neither; a RAM boot runs neither, and the
// recovery plan may write either.
func TestLayoutWithoutARunningSlot(t *testing.T) {
	t.Parallel()

	unknown := NewLayout("root=ubi0:rootfs soundtap.nandroot=1", table())
	for _, mtd := range []int{6, 7} {
		err := unknown.checkErase(req("", withMTD(mtd)))
		if err == nil {
			t.Errorf("unknown slot: erase of mtd%d allowed", mtd)
		}
	}

	err := unknown.checkErase(req("", withMTD(5)))
	if err != nil {
		t.Errorf("unknown slot: the environment refused: %v", err)
	}

	ram := NewLayout("console=ttyS3 root=/dev/ram0", table())
	for _, mtd := range []int{6, 7} {
		err = ram.checkErase(req("", withMTD(mtd)))
		if err != nil {
			t.Errorf("RAM boot: erase of mtd%d refused: %v", mtd, err)
		}
	}

	err = ram.checkErase(req("", withMTD(10)))
	if err == nil {
		t.Error("RAM boot: erase of the persistent volume allowed")
	}
}

func TestLayoutHoldsRangesToThePartition(t *testing.T) {
	t.Parallel()

	line := NewLayout(pingBoot, table())
	for _, req := range []Request{
		*req("", withMTD(5), withOffset(0x1000)),                  // not a block
		*req("", withMTD(5), withOffset(0x20000)),                 // past the end
		*req("", withMTD(5), withBlocks(2)),                       // runs past
		*req("", withMTD(4), withOffset(-0x20000)),                // negative
		*req("", withMTD(4), withOffset(0x1c0000), withBlocks(2)), // past the end
		*req("", withMTD(7), withOffset(0x500000-0x20000), withBlocks(2)),
	} {
		err := line.checkErase(&req)
		if err == nil {
			t.Errorf("erase %+v allowed", req)
		}
	}

	err := line.checkErase(req("", withMTD(4), withOffset(0x1c0000), withBlocks(1)))
	if err != nil {
		t.Errorf("the redundant environment's block refused: %v", err)
	}

	err = line.checkWrite(req("", withMTD(7)), 0x500001)
	if err == nil {
		t.Error("an image larger than the kernel partition allowed")
	}

	err = line.checkWrite(req("", withMTD(7)), 0)
	if err == nil {
		t.Error("an empty write allowed")
	}

	err = line.checkBlock(req("", withMTD(4), withOffset(0x100000), withSkip(1), withSize(0x20000)), 0x40000)
	if err != nil {
		t.Errorf("a U-Boot block refused: %v", err)
	}

	for _, request := range []Request{
		*req("", withMTD(4), withOffset(0x100000), withSkip(2), withSize(0x20000)), // not in the input
		*req("", withMTD(4), withOffset(0x100000), withSize(0x800)),                // not the erase block
		*req("", withMTD(4), withOffset(0x1e0000), withSize(0x20000)),              // past the end
		*req("", withMTD(7), withSize(0x20000)),                                    // dd into a kernel
		*req("", withMTD(5), withSize(0x20000)),                                    // dd into the environment
	} {
		err := line.checkBlock(&request, 0x40000)
		if err == nil {
			t.Errorf("block write %+v allowed", request)
		}
	}
}

func TestEnvCopiesAreTheEnvironmentsPlaces(t *testing.T) {
	t.Parallel()

	line := NewLayout(pingBoot, table())

	accepted := [][]EnvCopy{
		{{MTD: 5, Size: ubootenv.LegacySize, EraseSize: 0x20000, Offset: 0}},
		{
			{MTD: 5, Size: ubootenv.Size, EraseSize: 0x20000, Offset: 0},
			{MTD: 4, Offset: ubootenv.RedundantOffset, Size: ubootenv.Size, EraseSize: 0x20000},
		},
	}
	for _, c := range accepted {
		err := line.checkEnvCopies(c)
		if err != nil {
			t.Errorf("%+v refused: %v", c, err)
		}
	}

	bad := [][]EnvCopy{
		nil,
		{{MTD: 8, Size: ubootenv.Size, EraseSize: 0x20000, Offset: 0}},       // the running rootfs
		{{MTD: 7, Size: ubootenv.Size, EraseSize: 0x20000, Offset: 0}},       // a kernel
		{{MTD: 4, Size: ubootenv.Size, EraseSize: 0x20000, Offset: 0}},       // U-Boot itself
		{{MTD: 5, Offset: 0x20000, Size: ubootenv.Size, EraseSize: 0x20000}}, // outside
		{{MTD: 5, Size: 0x4000, EraseSize: 0x20000, Offset: 0}},              // no such size
		{{MTD: 5, Size: ubootenv.Size, EraseSize: 0x800, Offset: 0}},         // wrong block
		{
			{MTD: 5, Size: ubootenv.Size, EraseSize: 0x20000, Offset: 0},
			{MTD: 5, Size: ubootenv.Size, EraseSize: 0x20000, Offset: 0},
			{MTD: 5, Size: ubootenv.Size, EraseSize: 0x20000, Offset: 0},
		},
	}
	for _, c := range bad {
		err := line.checkEnvCopies(c)
		if err == nil {
			t.Errorf("%+v allowed", c)
		}
	}
}

func TestEnvScriptKeys(t *testing.T) {
	t.Parallel()

	for _, found := range []string{
		"bootcmd run nand_boot2;\nupgrade_available 1\nbootcount 0\n",
		"altbootcmd\nbootlimit\n",                      // deletions
		"soundtap_owner_key\n",                         // the retired owner key, removed
		"bootcmd=run nand_boot;\nupgrade_available=\n", // libubootenv's name=value form
	} {
		err := CheckEnvScript([]byte(found))
		if err != nil {
			t.Errorf("%q refused: %v", found, err)
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
		err := CheckEnvScript([]byte(bad))
		if err == nil {
			t.Errorf("%q allowed", bad)
		}
	}
}

func TestHostnameIsOneLabel(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"kitchen", "soundtap-a1b2c3", "x"} {
		err := CheckHostname(ok)
		if err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}

	for _, bad := range []string{"", "-x", "x-", "Kitchen", "a.b", "a b", strings.Repeat("a", 64), "x\n"} {
		err := CheckHostname(bad)
		if err == nil {
			t.Errorf("%q allowed", bad)
		}
	}
}

// req is a Request for op with what opts set; every other field is zero.
func req(op string, opts ...func(*Request)) *Request {
	request := &Request{
		Op: op, MTD: 0, Offset: 0, Blocks: 0, Skip: 0, Pad: false, Size: 0, Copies: nil,
		On: false, Name: "", GPIO: 0, Input: false, Lock: false,
	}
	for _, opt := range opts {
		opt(request)
	}

	return request
}

func withMTD(mtd int) func(*Request)         { return func(r *Request) { r.MTD = mtd } }
func withOffset(offset int64) func(*Request) { return func(r *Request) { r.Offset = offset } }
func withBlocks(blocks int64) func(*Request) { return func(r *Request) { r.Blocks = blocks } }
func withSkip(skip int64) func(*Request)     { return func(r *Request) { r.Skip = skip } }
func withPad() func(*Request)                { return func(r *Request) { r.Pad = true } }
func withSize(size int64) func(*Request)     { return func(r *Request) { r.Size = size } }
func withCopies(c []EnvCopy) func(*Request)  { return func(r *Request) { r.Copies = c } }
func withName(name string) func(*Request)    { return func(r *Request) { r.Name = name } }
func withInput() func(*Request)              { return func(r *Request) { r.Input = true } }
