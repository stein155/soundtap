// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash/flashtest"
)

// The real table, as printed by a stock ST10.
const procMTD = `dev:    size   erasesize  name
mtd0: 00020000 00020000 "NAND.SPL"
mtd1: 00020000 00020000 "NAND.SPL.backup1"
mtd2: 00020000 00020000 "NAND.SPL.backup2"
mtd3: 00020000 00020000 "NAND.SPL.backup3"
mtd4: 001e0000 00020000 "NAND.U-Boot"
mtd5: 00020000 00020000 "NAND.U-Boot-Env"
mtd6: 00500000 00020000 "NAND.Kernel-Ping"
mtd7: 00500000 00020000 "NAND.Kernel-Pong"
mtd8: 06000000 00020000 "NAND.File-System-Ping"
mtd9: 06000000 00020000 "NAND.File-System-Pong"
mtd10: 02600000 00020000 "NAND.Persistent-Storage"
mtd11: 00b80000 00020000 "NAND.Update-Staging"
mtd12: 00040000 00020000 "NAND.Manufacturing-Data"
`

func mtd(t *testing.T) map[string]Partition {
	t.Helper()
	m, err := ParseProcMTD(strings.NewReader(procMTD))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseProcMTDNamesEveryPartition(t *testing.T) {
	m := mtd(t)
	for name, want := range map[string]int{
		"NAND.Kernel-Ping": 6, "NAND.Kernel-Pong": 7,
		"NAND.File-System-Ping": 8, "NAND.File-System-Pong": 9,
		"NAND.Persistent-Storage": 10,
	} {
		if m[name].Index != want {
			t.Errorf("%s = mtd%d, want mtd%d", name, m[name].Index, want)
		}
	}
	// Sizes come off the same line and are what an install is measured against.
	if got := m["NAND.Kernel-Pong"].Size; got != 0x500000 {
		t.Errorf("Kernel-Pong is %d bytes, want %d", got, 0x500000)
	}
	if got := m["NAND.File-System-Pong"].Size; got != 0x6000000 {
		t.Errorf("File-System-Pong is %d bytes, want %d", got, 0x6000000)
	}
}

// The whole safety story rests on this: an install goes to the slot that is not
// running, and the running slot is read from the kernel command line the same
// way ARMUpdate reads it.
func TestInstallAlwaysTargetsTheOtherSlot(t *testing.T) {
	for _, c := range []struct{ cmdline, running, target string }{
		{"console=ttyS3,115200n8 root=ubi0:rootfs ubi.mtd=8,2048 rootwait", "PING", "PONG"},
		{"console=ttyS3,115200n8 root=ubi0:rootfs ubi.mtd=9,2048 rootwait", "PONG", "PING"},
	} {
		got, err := Plan(c.cmdline, mtd(t))
		if err != nil {
			t.Fatalf("%s: %v", c.running, err)
		}
		if got.Running.Name != c.running || got.Alternate.Name != c.target {
			t.Fatalf("running=%s target=%s, want %s → %s",
				got.Running.Name, got.Alternate.Name, c.running, c.target)
		}
		if got.Alternate.KernelMTD == got.Running.KernelMTD || got.Alternate.RootfsMTD == got.Running.RootfsMTD {
			t.Fatalf("target overlaps the running slot: %+v", got)
		}
	}
}

// No command line, an unfamiliar one, or a USB/dev boot: refuse. Guessing here
// writes the slot the speaker is running from.
func TestUnknownSlotRefusesRatherThanGuessing(t *testing.T) {
	for _, cmdline := range []string{
		"", "console=ttyS3", "root=/dev/mmcblk0p2 rootwait", "ubi.mtd=11,2048",
		"foo=ubi.mtd=8", "ubi.mtd=8garbage", "ubi.mtd=8,nope",
		"ubi.mtd=8,2048 ubi.mtd=9,2048",
	} {
		if _, err := Plan(cmdline, mtd(t)); err == nil {
			t.Errorf("cmdline %q was accepted", cmdline)
		}
	}
}

func TestRecoveryPlanTargetsPong(t *testing.T) {
	table := map[string]Partition{
		"NAND.SPL":              {Index: 0, Size: 0x20000, EraseSize: 0x20000},
		"NAND.SPL.backup1":      {Index: 1, Size: 0x20000, EraseSize: 0x20000},
		"NAND.SPL.backup2":      {Index: 2, Size: 0x20000, EraseSize: 0x20000},
		"NAND.SPL.backup3":      {Index: 3, Size: 0x20000, EraseSize: 0x20000},
		"NAND.U-Boot":           {Index: 4, Size: 0x1e0000, EraseSize: 0x20000},
		"NAND.Kernel-Ping":      {Index: 6, Size: 0x500000},
		"NAND.Kernel-Pong":      {Index: 7, Size: 0x500000},
		"NAND.File-System-Ping": {Index: 8, Size: 0x6000000},
		"NAND.File-System-Pong": {Index: 9, Size: 0x6000000},
	}
	target, err := RecoveryPlan(table, "")
	if err != nil {
		t.Fatal(err)
	}
	if target.Alternate.Name != "PONG" {
		t.Fatalf("recovery target = %s, want PONG", target.Alternate.Name)
	}
	// An environment that boots PONG keeps it: recovery writes PING instead.
	if target, err = RecoveryPlan(table, "PONG"); err != nil || target.Alternate.Name != "PING" {
		t.Fatalf("recovery target with PONG selected = %s (%v), want PING", target.Alternate.Name, err)
	}
}

// A layout that is not the layout we think it is must stop the install, not
// erase whatever happens to be at mtd7.
func TestUnexpectedPartitionTableIsRefused(t *testing.T) {
	m := mtd(t)
	m["NAND.Kernel-Pong"] = Partition{Index: 5, Size: 0x20000} // where the U-Boot environment lives
	if _, err := Plan("ubi.mtd=8,2048", m); err == nil {
		t.Fatal("a shifted partition table was accepted")
	}
	delete(m, "NAND.Kernel-Pong")
	if _, err := Plan("ubi.mtd=8,2048", m); err == nil {
		t.Fatal("a missing partition was accepted")
	}
	m = mtd(t)
	m["NAND.File-System-Pong"] = Partition{Index: 9}
	if _, err := Plan("ubi.mtd=8,2048", m); err == nil {
		t.Fatal("a partition with no capacity was accepted")
	}
}

func TestWLABootloaderGeometry(t *testing.T) {
	target := mustPlan(t, "ubi.mtd=8,2048")
	if !target.WLABootloaderGeometry() {
		t.Fatal("stock WLA bootloader geometry was rejected")
	}
	target.BootloaderSizes[4] = 0x20000
	if target.WLABootloaderGeometry() {
		t.Fatal("unexpected U-Boot geometry was accepted")
	}
	target = mustPlan(t, "ubi.mtd=8,2048")
	target.BootloaderEraseSizes[4] = 0x10000
	if target.WLABootloaderGeometry() {
		t.Fatal("unexpected U-Boot erase geometry was accepted")
	}
}

// The order is the recovery story: the alternate kernel is erased before
// anything else is written, so an install that dies halfway leaves a slot that
// cannot be booted — and the speaker comes back on the slot it was running.
func TestKernelIsErasedFirstAndWrittenLast(t *testing.T) {
	var log []string
	in := &Installer{
		Target: mustPlan(t, "ubi.mtd=8,2048"),
		Run: func(stdin io.Reader, name string, args ...string) error {
			log = append(log, name+" "+strings.Join(args, " "))
			if stdin != nil {
				n, _ := io.Copy(io.Discard, stdin)
				log[len(log)-1] += fmt.Sprintf(" <%d bytes", n)
			}
			return nil
		},
	}
	if err := in.EraseAlternateKernel(); err != nil {
		t.Fatal(err)
	}
	w, wait := in.StartRootfs(10)
	if _, err := io.WriteString(w, "UBI#rootfs"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(t.TempDir(), "uImage")
	if err := os.WriteFile(img, []byte("kernel-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	readGood := func(string, ...string) ([]byte, error) { return []byte("kernel-bytes"), nil }
	if err := in.WriteKernel(readGood, img); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"flash_erase /dev/mtd7 0 0",
		"ubiformat /dev/mtd9 -f - -S 10 -s 2048 -y <10 bytes",
		"flash_erase /dev/mtd7 0 0",
		"nandwrite -p /dev/mtd7 " + img,
	}
	if len(log) != len(want) {
		t.Fatalf("commands = %v", log)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Errorf("step %d = %q, want %q", i, log[i], want[i])
		}
	}
	// A kernel that did not land must not be reported as installed: the slot would
	// be selected and the board would jump into a partial image.
	badRead := func(string, ...string) ([]byte, error) { return []byte("kernel-byteX"), nil }
	if err := in.WriteKernel(badRead, img); err == nil {
		t.Error("a kernel readback mismatch must fail the write")
	}
	log = log[:4]

	// Nothing may touch the running slot, the bootloader, its environment or
	// persistent storage — mtd0-6 and mtd8 here, plus mtd10 and up.
	for _, cmd := range log {
		for _, forbidden := range []string{"/dev/mtd0", "/dev/mtd1", "/dev/mtd2", "/dev/mtd3",
			"/dev/mtd4", "/dev/mtd5", "/dev/mtd6", "/dev/mtd8", "/dev/mtd10", "/dev/mtd11", "/dev/mtd12"} {
			if strings.Contains(cmd, forbidden+" ") || strings.HasSuffix(cmd, forbidden) {
				t.Errorf("%q touches %s", cmd, forbidden)
			}
		}
	}
}

func mustPlan(t *testing.T, cmdline string) Target {
	t.Helper()
	target, err := Plan(cmdline, mtd(t))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// A payload that does not fit, or whose capacity is unknown, must be caught
// before anything is erased.
func TestFits(t *testing.T) {
	for _, c := range []struct {
		n, size int64
		want    bool
	}{
		{100, 200, true}, {200, 200, true}, {201, 200, false}, {1 << 30, 0, false},
	} {
		if got := Fits(c.n, c.size); got != c.want {
			t.Errorf("Fits(%d, %d) = %v, want %v", c.n, c.size, got, c.want)
		}
	}
}

func TestBootableSignatures(t *testing.T) {
	head := make([]byte, 64)
	copy(head[32:], "Linub")
	if got := KernelSignature(head); got != "Linub" {
		t.Fatalf("signature = %q", got)
	}
	for sig, want := range map[string]bool{"Linux": true, "Linub": true, "Linem": false, "Linpv": false, "": false} {
		if Bootable(sig) != want {
			t.Errorf("Bootable(%q) = %v, want %v", sig, !want, want)
		}
	}
}

func TestValidateKernel(t *testing.T) {
	build := func() []byte {
		data := []byte("kernel payload")
		image := make([]byte, 64+len(data)+20)
		for i := 64 + len(data); i < len(image); i++ {
			image[i] = 0xff
		}
		binary.BigEndian.PutUint32(image, 0x27051956)
		binary.BigEndian.PutUint32(image[12:], uint32(len(data)))
		binary.BigEndian.PutUint32(image[16:], 0x80008000)
		binary.BigEndian.PutUint32(image[20:], 0x80008000)
		binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(data))
		image[28], image[29], image[30], image[31] = 5, 2, 2, 0
		copy(image[32:], "Linux-6.12 SoundTap")
		copy(image[64:], data)
		header := append([]byte(nil), image[:64]...)
		binary.BigEndian.PutUint32(header[4:], 0)
		binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(header))
		copy(image[32:37], "Linub")
		return image
	}
	if err := ValidateKernel(build()); err != nil {
		t.Fatalf("valid kernel rejected: %v", err)
	}
	stock := build()
	clear(stock[len(stock)-20:]) // official stock ST10 carries zero tail bytes
	if err := ValidateKernel(stock); err != nil {
		t.Fatalf("official-style kernel tail rejected: %v", err)
	}
	for name, corrupt := range map[string]func([]byte){
		"magic":    func(b []byte) { b[0] = 0 },
		"size":     func(b []byte) { binary.BigEndian.PutUint32(b[12:], uint32(len(b))) },
		"data crc": func(b []byte) { b[64] ^= 1 },
		"arch":     func(b []byte) { b[29] = 3 },
		"entry":    func(b []byte) { binary.BigEndian.PutUint32(b[20:], 0) },
		"header":   func(b []byte) { b[8] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			image := build()
			corrupt(image)
			if err := ValidateKernel(image); err == nil {
				t.Fatal("corrupt kernel accepted")
			}
		})
	}
}

// The command line this board actually boots with, copied from /proc/cmdline. It
// carries two ubi.mtd= parameters: the root slot as ubi0, and the persistent
// volume as ubi1 because the image has no mtd-utils to attach it afterwards.
// Refusing on the second one meant the speaker could not say which slot it was
// running, so every install through /api/firmware was turned away -- visible in
// /api/debug as "ubi.mtd=NAND.Persistent-Storage is neither slot".
const boardCmdline = "console=ttyGS0,115200n8 root=ubi0:rootfs ro rootfstype=ubifs " +
	"ubi.mtd=9,2048 rootwait init=/init mem=128M cma=8M panic=10 omap_wdt.early_enable=1 " +
	"variant=binky module_type=sm2 country=GB region=GB soundtap.nandroot=1 " +
	"ubi.mtd=NAND.Persistent-Storage,2048"

func TestSlotFromTheCommandLineThisBoardBootsWith(t *testing.T) {
	slot, err := SlotFromCmdline(boardCmdline)
	if err != nil {
		t.Fatalf("the board's own command line was refused: %v", err)
	}
	if slot.Name != "PONG" {
		t.Errorf("running slot = %q, want PONG", slot.Name)
	}
}

func TestSlotFromCmdlineIgnoresVolumesAttachedAfterTheRoot(t *testing.T) {
	for _, cmdline := range []string{
		"root=ubi0:rootfs ubi.mtd=8,2048 ubi.mtd=NAND.Persistent-Storage,2048",
		"root=ubi0:rootfs ubi.mtd=8,2048 ubi.mtd=NAND.Persistant-Storage,2048",
		"root=ubi0:rootfs ubi.mtd=8,2048 ubi.mtd=10",
	} {
		slot, err := SlotFromCmdline(cmdline)
		if err != nil || slot.Name != "PING" {
			t.Errorf("%q = %v, %v; want PING", cmdline, slot.Name, err)
		}
	}
}

// What must still be refused, because each one could hide a wrong answer about
// which slot is running -- and writing the running slot is the one unrecoverable
// mistake this package can make.
func TestSlotFromCmdlineStillRefusesTheAmbiguousCases(t *testing.T) {
	for name, cmdline := range map[string]string{
		"two boot slots":             "ubi.mtd=8,2048 ubi.mtd=9,2048",
		"a boot slot after the root": "ubi.mtd=NAND.Persistent-Storage,2048 ubi.mtd=9,2048",
		"the root is not a slot":     "ubi.mtd=NAND.Update-Staging,2048",
		"nothing attached":           "root=/dev/mmcblk0p2 rootwait",
		"a bad offset":               "ubi.mtd=9,not-a-number",
	} {
		if _, err := SlotFromCmdline(cmdline); !errors.Is(err, ErrUnknownSlot) {
			t.Errorf("%s (%q) = %v, want ErrUnknownSlot", name, cmdline, err)
		}
	}
}

// Naming the partition rather than numbering it survives a renumbered table.
func TestSlotFromCmdlineAcceptsTheRootSlotByName(t *testing.T) {
	slot, err := SlotFromCmdline("root=ubi0:rootfs ubi.mtd=NAND.File-System-Pong,2048")
	if err != nil || slot.Name != "PONG" {
		t.Fatalf("named root slot = %v, %v", slot.Name, err)
	}
}

func TestVerifyRootfsReadsTheBootFilesBack(t *testing.T) {
	files := map[string][]byte{}
	image := flashtest.UBIImage(testPEB, testVolume())
	in, cmds := writtenSlot(t, image, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(flashtest.UBIVolume(image, testPEB))), nil
	})
	read := func(name string, args ...string) ([]byte, error) {
		if b, ok := files[args[0]]; ok {
			return b, nil
		}
		return flashtest.BootFile(args[0]), nil
	}
	if err := in.VerifyRootfs(read, "rhino"); err != nil {
		t.Fatalf("good slot refused: %v", err)
	}
	if got := strings.Join(*cmds, " "); got != "ubiformat ubiattach open:/dev/ubi7_0 mkdir mount test umount ubidetach" {
		t.Fatalf("commands %q", got)
	}
	bad := flashtest.BootFile("uImage")
	bad[len(bad)-1] ^= 1
	files["/run/soundtap/verify-rootfs/boot/uImage-rhino"] = bad
	if err := in.VerifyRootfs(read, "rhino"); err == nil || !strings.Contains(err.Error(), "data CRC") {
		t.Fatalf("corrupt kernel: err = %v", err)
	}
	delete(files, "/run/soundtap/verify-rootfs/boot/uImage-rhino")
	files["/run/soundtap/verify-rootfs/boot/rhino_sm2.dtb"] = []byte{0xd0, 0x0d}
	if err := in.VerifyRootfs(read, "rhino"); err == nil {
		t.Fatal("truncated device tree accepted")
	}
}
