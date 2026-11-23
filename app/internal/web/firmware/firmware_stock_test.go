// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/stu"
	"github.com/stein155/soundtap/app/internal/ubootenv"
	"github.com/stein155/soundtap/app/internal/ubootenv/envfixture"
	"github.com/stein155/soundtap/app/internal/ubootenv/ubootenvtest"
)

// stockWLAPackage builds a complete, structurally valid stock Bose chain: a
// kernel U-Boot will accept, an SPL with the NAND configuration header and reset
// vector the ROM needs, and a u-boot.img with a legacy uImage header that checks
// out. This is what a real .stu looks like to this endpoint.
func stockWLAPackage(t *testing.T) []byte {
	t.Helper()
	return stockPackage(t, stu.Expected)
}

// stockPackage builds a Bose package with this module chain and valid images.
func stockPackage(t *testing.T, chain []string) []byte {
	t.Helper()
	sizes := map[string]int{"MLO": 0x400, "u-boot.img": 0x400}
	data := buildSTUChain(t, chain, sizes)
	size := func(name string) int {
		if s := sizes[name]; s != 0 {
			return s
		}
		return 0x200
	}
	off := 0
	for i, name := range chain {
		image := data[off+stuHeaderSize : off+stuHeaderSize+size(name)]
		switch name {
		case "linuxPatched":
			// Real kernels end well before the module does -- the approved stock
			// package has 22 KiB of slack, ours has 1.8 KiB -- so the uImage payload
			// stops short of the overlapping tail.
			payload := len(image) - 64 - stuOverlap
			binary.BigEndian.PutUint32(image, 0x27051956)
			binary.BigEndian.PutUint32(image[12:], uint32(payload))
			binary.BigEndian.PutUint32(image[16:], 0x80008000)
			binary.BigEndian.PutUint32(image[20:], 0x80008000)
			image[28], image[29], image[30], image[31] = 5, 2, 2, 0
			copy(image[32:64], make([]byte, 32))
			copy(image[32:], "Linux-3.14.43+")
			binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(image[64:64+payload]))
			binary.BigEndian.PutUint32(image[4:], 0)
			binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
			copy(image[32:37], "Linub")
		case "MLO":
			// The TI image the mask ROM loads: configuration header, GP header of
			// image size and SRAM load address, then a branch to reset. Bose's own
			// SPL uses bl, so use bl here too.
			binary.LittleEndian.PutUint32(image, 0x40)
			binary.LittleEndian.PutUint32(image[0x200:], uint32(len(image)-0x208))
			binary.LittleEndian.PutUint32(image[0x204:], 0x402f0400)
			binary.LittleEndian.PutUint32(image[0x208:], 0xeb000042)
		case "u-boot.img":
			// Last in the WLA package, so nothing overlaps it: its uImage ends exactly
			// at the declared module size. In the ST300's the .bos follows it.
			payload := len(image) - 64
			if i < len(chain)-1 {
				payload -= stuOverlap
			}
			binary.BigEndian.PutUint32(image, 0x27051956)
			binary.BigEndian.PutUint32(image[12:], uint32(payload))
			binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(image[64:64+payload]))
			binary.BigEndian.PutUint32(image[4:], 0)
			binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
		}
		off += stuHeaderSize + size(name) - stuOverlap
	}
	off = 0
	for _, name := range chain {
		header := data[off : off+stuHeaderSize]
		image := data[off+stuHeaderSize : off+stuHeaderSize+size(name)]
		binary.BigEndian.PutUint32(header[stuImageCRCOff:], crc32.ChecksumIEEE(image))
		binary.BigEndian.PutUint32(header[stuHeaderCRCOff:], crc32.ChecksumIEEE(header[stuOverlap:stuHeaderCRCOff]))
		off += stuHeaderSize + size(name) - stuOverlap
	}
	binary.BigEndian.PutUint32(data[stuPkgCRCOff:], crc32.ChecksumIEEE(data[:stuPkgCRCOff]))
	binary.BigEndian.PutUint32(data[stuHeaderCRCOff:], crc32.ChecksumIEEE(data[stuOverlap:stuHeaderCRCOff]))
	return data
}

// nativeWLAPackage builds a complete, structurally valid SoundTap RTCH chain: a
// mainline kernel that names this model, a rootfs, and the boot chain we ship.
func nativeWLAPackage(t *testing.T) []byte {
	t.Helper()
	sizes := map[string]int{"soundtap-spl": 0x400, "soundtap-uboot": 0x400, "soundtap-rootfs": rootfsModuleSize}
	data := buildSTUChain(t, stu.ExpectedWLANative, sizes)
	copy(data[4:8], "RTCH")
	size := func(name string) int {
		if s := sizes[name]; s != 0 {
			return s
		}
		return 0x200
	}
	off := 0
	for _, name := range stu.ExpectedWLANative {
		image := data[off+stuHeaderSize : off+stuHeaderSize+size(name)]
		switch name {
		case "soundtap-kernel":
			payload := len(image) - 64 - stuOverlap
			binary.BigEndian.PutUint32(image, 0x27051956)
			binary.BigEndian.PutUint32(image[12:], uint32(payload))
			binary.BigEndian.PutUint32(image[16:], 0x80008000)
			binary.BigEndian.PutUint32(image[20:], 0x80008000)
			image[28], image[29], image[30], image[31] = 5, 2, 2, 0
			copy(image[32:64], make([]byte, 32))
			copy(image[32:], "Linux-6.12 SoundTap binky")
			binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(image[64:64+payload]))
			binary.BigEndian.PutUint32(image[4:], 0)
			binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
			copy(image[32:37], "Linub")
		case "soundtap-spl":
			binary.LittleEndian.PutUint32(image, 0x40)
			binary.LittleEndian.PutUint32(image[0x200:], uint32(len(image)-0x208))
			binary.LittleEndian.PutUint32(image[0x204:], 0x402f0400)
			binary.LittleEndian.PutUint32(image[0x208:], 0xea00000f)
		case "soundtap-uboot":
			binary.BigEndian.PutUint32(image, 0x27051956)
			binary.BigEndian.PutUint32(image[12:], uint32(len(image)-64))
			binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(image[64:]))
			binary.BigEndian.PutUint32(image[4:], 0)
			binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
		}
		off += stuHeaderSize + size(name) - stuOverlap
	}
	off = 0
	for _, name := range stu.ExpectedWLANative {
		header := data[off : off+stuHeaderSize]
		image := data[off+stuHeaderSize : off+stuHeaderSize+size(name)]
		binary.BigEndian.PutUint32(header[stuImageCRCOff:], crc32.ChecksumIEEE(image))
		binary.BigEndian.PutUint32(header[stuHeaderCRCOff:], crc32.ChecksumIEEE(header[stuOverlap:stuHeaderCRCOff]))
		off += stuHeaderSize + size(name) - stuOverlap
	}
	binary.BigEndian.PutUint32(data[stuPkgCRCOff:], crc32.ChecksumIEEE(data[:stuPkgCRCOff]))
	binary.BigEndian.PutUint32(data[stuHeaderCRCOff:], crc32.ChecksumIEEE(data[stuOverlap:stuHeaderCRCOff]))
	return data
}

// universalST10Package is the one package every speaker installs, aimed at an ST10:
// our five module names under the BOSE magic stock authenticates, and a mainline
// kernel that names this model.
func universalST10Package(t *testing.T) []byte {
	t.Helper()
	return universalPackage(t, stu.ExpectedSoundTap)
}

// universalPackage builds a valid package for any universal chain: payloads are filled by
// their base name, so soundtap-spl.binky gets the same well-formed SPL as soundtap-spl.
func universalPackage(t *testing.T, chain []string) []byte {
	t.Helper()
	return universalPackageSized(t, chain, nil)
}

// universalPackageSized is universalPackage with some modules given their own size.
func universalPackageSized(t *testing.T, chain []string, override map[string]int) []byte {
	t.Helper()
	sizes := map[string]int{}
	for _, name := range chain {
		switch stu.BaseModuleName(name) {
		case "soundtap-spl", "soundtap-uboot":
			sizes[name] = 0x400
		case "soundtap-rootfs":
			sizes[name] = rootfsModuleSize
		}
		if n := override[name]; n != 0 {
			sizes[name] = n
		}
	}
	data := buildSTUChain(t, chain, sizes)
	size := func(name string) int {
		if s := sizes[name]; s != 0 {
			return s
		}
		return 0x200
	}
	off := 0
	for _, name := range chain {
		image := data[off+stuHeaderSize : off+stuHeaderSize+size(name)]
		switch stu.BaseModuleName(name) {
		case "soundtap-kernel":
			payload := len(image) - 64 - stuOverlap
			binary.BigEndian.PutUint32(image, 0x27051956)
			binary.BigEndian.PutUint32(image[12:], uint32(payload))
			binary.BigEndian.PutUint32(image[16:], 0x80008000)
			binary.BigEndian.PutUint32(image[20:], 0x80008000)
			image[28], image[29], image[30], image[31] = 5, 2, 2, 0
			copy(image[32:64], make([]byte, 32))
			copy(image[32:], "Linux-6.12 SoundTap st10")
			binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(image[64:64+payload]))
			binary.BigEndian.PutUint32(image[4:], 0)
			binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
			copy(image[32:37], "Linub")
		case "soundtap-spl":
			binary.LittleEndian.PutUint32(image, 0x40)
			binary.LittleEndian.PutUint32(image[0x200:], uint32(len(image)-0x208))
			binary.LittleEndian.PutUint32(image[0x204:], 0x402f0400)
			binary.LittleEndian.PutUint32(image[0x208:], 0xea00000f)
		case "soundtap-uboot":
			binary.BigEndian.PutUint32(image, 0x27051956)
			binary.BigEndian.PutUint32(image[12:], uint32(len(image)-64))
			binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(image[64:]))
			binary.BigEndian.PutUint32(image[4:], 0)
			binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
		}
		off += stuHeaderSize + size(name) - stuOverlap
	}
	off = 0
	for _, name := range chain {
		header := data[off : off+stuHeaderSize]
		image := data[off+stuHeaderSize : off+stuHeaderSize+size(name)]
		binary.BigEndian.PutUint32(header[stuImageCRCOff:], crc32.ChecksumIEEE(image))
		binary.BigEndian.PutUint32(header[stuHeaderCRCOff:], crc32.ChecksumIEEE(header[stuOverlap:stuHeaderCRCOff]))
		off += stuHeaderSize + size(name) - stuOverlap
	}
	binary.BigEndian.PutUint32(data[stuPkgCRCOff:], crc32.ChecksumIEEE(data[:stuPkgCRCOff]))
	binary.BigEndian.PutUint32(data[stuHeaderCRCOff:], crc32.ChecksumIEEE(data[stuOverlap:stuHeaderCRCOff]))
	return data
}

// An ST10 used to choose its slot from the name in the kernel header, so writing the
// slot was the whole install. Once a SoundTap install has rewritten the environment,
// bootcmd decides instead -- and an install that leaves bootcmd alone writes a slot
// nobody boots. Measured on the speaker: PING written, reboot, still ubi.mtd=9.
func TestST10InstallSelectsTheSlotItWrote(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	s.firmwareModel = func() (string, error) { return "st10", nil }
	req := httptest.NewRequest("POST", "/api/firmware?install=1",
		strings.NewReader(string(universalST10Package(t))))
	req.Header.Set("X-SoundTap-Confirm", "install")
	rec := httptest.NewRecorder()
	s.postFirmware(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("ST10 install = %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(strings.Join(f.cmds, "\n"), "fw_setenv -c") {
		t.Fatalf("the environment was never rewritten:\n%s", strings.Join(f.cmds, "\n"))
	}
	env := envOf(t, f)
	if got, _ := env.Get("bootcmd"); got != "run nand_boot;" {
		t.Errorf("bootcmd = %q, want the slot this install wrote", got)
	}
}

// The ST300 takes the universal package as the Wireless Link does, under its own
// device tree: the binky kernel and boot chain, and ginger's recipes.
func TestST300InstallsTheUniversalPackage(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	s.firmwareModel = func() (string, error) { return "st300", nil }
	rec := installWLA(t, s, universalPackage(t, stu.ExpectedSoundTapTri))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("ST300 install = %d %s", rec.Code, rec.Body)
	}
	env := envOf(t, f)
	if got, _ := env.Get("bootcmd"); got != "run nand_boot;" {
		t.Errorf("bootcmd = %q, want the slot this install wrote", got)
	}
	if got, _ := env.Get("soundtap_args"); !strings.Contains(got, "variant=ginger ") {
		t.Errorf("soundtap_args = %q, want ginger's", got)
	}
}

// A rootfs whose volume does not read back as it was streamed in is refused before the
// kernel, the boot chain or the environment are touched: the slot stays unbootable and
// the speaker keeps booting the one it runs.
func TestInstallRefusesARootfsThatDoesNotReadBack(t *testing.T) {
	f := &fakeFlash{corruptVolume: true}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	rec := installWLA(t, s, nativeWLAPackage(t))
	if rec.Code == 200 || !strings.Contains(rec.Body.String(), "writing the root filesystem failed") {
		t.Fatalf("install of a slot that reads back wrong = %d %s", rec.Code, rec.Body)
	}
	joined := strings.Join(f.cmds, "\n")
	if !strings.Contains(joined, "ubiformat /dev/mtd8") {
		t.Fatalf("the rootfs was never written: %v", f.cmds)
	}
	// Refused at the volume readback, before the boot files were looked at.
	if strings.Contains(joined, "mount -t ubifs") {
		t.Errorf("a slot that read back wrong was mounted: %v", f.cmds)
	}
	for _, dev := range []string{"/dev/mtd0", "/dev/mtd4", "/dev/mtd6"} {
		if writesTo(joined, dev) {
			t.Errorf("%s was written after the rootfs failed its readback", dev)
		}
	}
	env := envOf(t, f)
	if got, _ := env.Get("bootcmd"); got != "run nand_boot2;" {
		t.Errorf("bootcmd = %q, want the running slot still selected", got)
	}
}

// soundtapEnv is the environment a SoundTap install leaves behind: PONG
// selected, and SoundTap's markers on that slot's bootargs entry.
func soundtapEnv(t *testing.T) *ubootenv.Env {
	t.Helper()
	env, err := ubootenv.ParseLegacy(factoryEnvImage(t))
	if err != nil {
		t.Fatal(err)
	}
	args, _ := env.Get("nand_args2")
	env.Set("bootcmd", "run nand_boot2;")
	env.Set("nand_args2", args+" soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048")
	return env
}

func factoryEnvImage(t *testing.T) []byte {
	t.Helper()
	return envfixture.Image()
}

// wlaInstallServer is a Wireless Link: the full partition table this board
// reports, including the environment partition that decides which slot boots,
// seeded with the factory environment.
func wlaInstallServer(t *testing.T, f *fakeFlash, cmdline string) *Handler {
	t.Helper()
	// Our U-Boot, which reads the redundant environment and says so on the command line.
	target, err := flash.Plan(cmdline+" "+flash.EnvRedundantMarker, map[string]flash.Partition{
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
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.writes == nil {
		f.writes = map[string][]byte{}
	}
	if err := ubootenvtest.Image(f.writes, "/dev/mtd5", "/dev/mtd4", soundtapEnv(t)); err != nil {
		t.Fatal(err)
	}
	return &Handler{
		homeDir:     t.TempDir(),
		flashPlan:   func() (flash.Target, error) { return target, nil },
		flashTools:  func() []string { return nil },
		flashRunner: f.run,
		flashOpen:   f.open,
		flashReader: func(name string, args ...string) ([]byte, error) {
			return f.read(name, args...)
		},
		firmwareModel: func() (string, error) { return "wla", nil },
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func installWLA(t *testing.T, s *Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/firmware?install=1", strings.NewReader(string(body)))
	req.Header.Set("X-SoundTap-Confirm", "install")
	rec := httptest.NewRecorder()
	s.postFirmware(rec, req)
	return rec
}

// The point of the whole exercise: a Wireless Link running SoundTap can be sent
// back to stock from SoundTap itself. It has no retained Bose updater, so before
// this the only way back was a mask-ROM RAM boot with a laptop attached.
func TestWLAInstallsAStockPackageAndSelectsItsSlot(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	var logbuf strings.Builder
	s.log = slog.New(slog.NewTextHandler(&logbuf, nil))
	rec := installWLA(t, s, stockWLAPackage(t))
	if rec.Code != 200 {
		t.Fatalf("stock install = %d %s\n%s", rec.Code, rec.Body, logbuf.String())
	}
	if !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("body = %s", rec.Body)
	}

	joined := strings.Join(f.cmds, "\n")
	for _, want := range []string{
		"flash_erase /dev/mtd6 0 0", // the alternate kernel, invalidated first
		"ubiformat /dev/mtd8",       // the alternate rootfs
		"nandwrite -p /dev/mtd6",    // the kernel, last, which makes PING bootable
		"fw_setenv -c",              // and the environment, which selects it
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q from:\n%s", want, joined)
		}
	}
	// The running slot must never be touched.
	for _, forbidden := range []string{"/dev/mtd7", "/dev/mtd9"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("the install wrote the running slot %s:\n%s", forbidden, joined)
		}
	}

	env := envOf(t, f)
	if got, _ := env.Get("bootcmd"); got != "run nand_boot;" {
		t.Errorf("bootcmd = %q, want the stock package's slot selected", got)
	}
	// Back on stock, nothing of ours may remain on the kernel command line.
	for _, key := range []string{"nand_args", "nand_args2"} {
		if value, _ := env.Get(key); strings.Contains(value, "soundtap") {
			t.Errorf("%s still carries SoundTap markers: %q", key, value)
		}
	}
	// Bose's U-Boot reads one 8 KiB copy: that, and no redundant copy for a later U-Boot of
	// ours to prefer over it.
	if !bytes.Equal(f.writes["/dev/mtd5"][:ubootenv.LegacySize], factoryEnvImage(t)) {
		t.Error("going back to stock did not restore the factory environment byte for byte")
	}
	if redundant := f.writes["/dev/mtd4"][ubootenv.RedundantOffset:]; !bytes.Equal(redundant, bytes.Repeat([]byte{0xff}, len(redundant))) {
		t.Error("the redundant environment copy survived the way back to stock")
	}
	// The single copy went in before Bose's U-Boot did.
	envAt := strings.Index(joined, "flash_erase /dev/mtd4 1835008 1")
	if ubootAt := strings.Index(joined, "flash_erase /dev/mtd4 0 8"); envAt < 0 || ubootAt < envAt {
		t.Errorf("Bose's U-Boot was written before the environment went back to its format:\n%s", joined)
	}
}

// Bootloaders the board already has must not be erased to put back what is
// already there. On a board whose SPL was never replaced, going back to stock
// writes the same bytes over the same partitions -- all risk, no change.
func TestGoingBackToStockSkipsBootloadersThatAlreadyMatch(t *testing.T) {
	data := stockWLAPackage(t)
	var mlo, uboot bytes.Buffer
	if _, err := stu.Scan(bytes.NewReader(data), func(m stu.Module) io.Writer {
		switch m.Name {
		case "MLO":
			return &mlo
		case "u-boot.img":
			return &uboot
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f := &fakeFlash{writes: map[string][]byte{
		"/dev/mtd0": mlo.Bytes(), "/dev/mtd1": mlo.Bytes(),
		"/dev/mtd2": mlo.Bytes(), "/dev/mtd3": mlo.Bytes(),
		"/dev/mtd4": twoUBoots(uboot.Bytes()),
	}}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	if rec := installWLA(t, s, data); rec.Code != 200 {
		t.Fatalf("stock install = %d %s", rec.Code, rec.Body)
	}
	for _, dev := range []string{"/dev/mtd0", "/dev/mtd1", "/dev/mtd2", "/dev/mtd3", "/dev/mtd4"} {
		if strings.Contains(strings.Join(f.cmds, "\n"), "flash_erase "+dev) {
			t.Errorf("%s was erased even though it already held these bytes", dev)
		}
	}
}

// An environment whose CRC does not check out is rebuilt before anything else is
// erased: libubootenv refuses it, so without the repair no install or confirm ever
// writes it again, and U-Boot's built-in default boots one fixed slot and device tree
// . The running slot just proved it boots, so the rebuilt environment selects it.
func TestInstallRebuildsAnEnvironmentWithABadCRC(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	// Both copies torn, and no single copy either.
	for _, copy := range [][]byte{f.writes["/dev/mtd5"][:ubootenv.Size],
		f.writes["/dev/mtd4"][ubootenv.RedundantOffset : ubootenv.RedundantOffset+ubootenv.Size]} {
		copy[64] ^= 0xff
	}

	rec := installWLA(t, s, stockWLAPackage(t))
	if rec.Code != 200 {
		t.Fatalf("install with a corrupt environment = %d %s", rec.Code, rec.Body)
	}
	cmds := strings.Join(f.cmds, "\n")
	if !strings.HasPrefix(cmds, "flash_erase /dev/mtd4 1835008 1\nnandwrite -p -s 1835008 /dev/mtd4 ") ||
		!strings.Contains(cmds, "\nflash_erase /dev/mtd5 0 1\nnandwrite -p /dev/mtd5 ") {
		t.Errorf("the environment was not rebuilt before anything else, redundant copy first: %v", f.cmds)
	}
}

// A stock package whose SPL is not an SPL must not become the boot chain. The
// alternate slot has already been overwritten by then -- it is the slot that was
// going to be replaced either way -- but the kernel write is what makes a slot
// bootable and the environment write is what selects it, and neither may happen.
func TestWLARefusesAStockPackageWithAnInvalidSPL(t *testing.T) {
	data := stockWLAPackage(t)
	off := 0
	for _, name := range stu.Expected {
		size := 0x200
		if name == "MLO" || name == "u-boot.img" {
			size = 0x400
		}
		if name == "MLO" {
			image := data[off+stuHeaderSize : off+stuHeaderSize+size]
			binary.LittleEndian.PutUint32(image[0x208:], 0) // not the SPL reset vector
			header := data[off : off+stuHeaderSize]
			binary.BigEndian.PutUint32(header[stuImageCRCOff:], crc32.ChecksumIEEE(image))
			binary.BigEndian.PutUint32(header[stuHeaderCRCOff:], crc32.ChecksumIEEE(header[stuOverlap:stuHeaderCRCOff]))
			break
		}
		off += stuHeaderSize + size - stuOverlap
	}
	binary.BigEndian.PutUint32(data[stuPkgCRCOff:], crc32.ChecksumIEEE(data[:stuPkgCRCOff]))
	binary.BigEndian.PutUint32(data[stuHeaderCRCOff:], crc32.ChecksumIEEE(data[stuOverlap:stuHeaderCRCOff]))

	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	before := append([]byte(nil), f.writes["/dev/mtd5"]...)
	rec := installWLA(t, s, data)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "the package was not installed") {
		t.Fatalf("invalid SPL = %d %s", rec.Code, rec.Body)
	}
	joined := strings.Join(f.cmds, "\n")
	if strings.Contains(joined, "nandwrite -p /dev/mtd6") {
		t.Errorf("an unusable package made the alternate slot bootable:\n%s", joined)
	}
	for _, dev := range []string{"/dev/mtd0", "/dev/mtd1", "/dev/mtd2", "/dev/mtd3", "/dev/mtd4", "/dev/mtd5"} {
		if strings.Contains(joined, "flash_erase "+dev) {
			t.Errorf("%s was written despite an invalid SPL:\n%s", dev, joined)
		}
	}
	if !bytes.Equal(f.writes["/dev/mtd5"], before) {
		t.Error("the environment changed despite a refused package")
	}
}

// install.PackageRefusal is the last line: whatever chain arrives, a Wireless Link may
// not be left running one project's SPL against the other's kernel.
func TestWLAPackageMustCarryABootChain(t *testing.T) {
	err := install.PackageRefusal("wla", &stu.Package{
		Modules: []stu.Module{{Name: "ubi.img", Size: 0x200, Targets: []string{"st10", "wla"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "SPL and U-Boot") {
		t.Fatalf("a package with no bootloaders = %v", err)
	}
}

// Every model goes back to stock through this endpoint now, so a stock package is
// accepted on the ST10 and ST20 too -- but only with the boot chain that Bose's kernel
// needs under it.
func TestStockPackageOnEveryModelNeedsItsBootChain(t *testing.T) {
	for _, tc := range []struct{ model, target string }{{"st10", "st10"}, {"st20", "st20"}} {
		rootfs := stu.Module{Name: "ubi.img", Size: 0x200, Targets: []string{tc.target}}
		bare := &stu.Package{Modules: []stu.Module{rootfs}}
		if err := install.PackageRefusal(tc.model, bare); err == nil || !strings.Contains(err.Error(), "SPL and U-Boot") {
			t.Errorf("%s stock package with no bootloaders = %v", tc.model, err)
		}
		chained := &stu.Package{Modules: []stu.Module{rootfs,
			{Name: "MLO", Size: 0x200, Targets: []string{tc.target}},
			{Name: "u-boot.img", Size: 0x200, Targets: []string{tc.target}}}}
		if err := install.PackageRefusal(tc.model, chained); err != nil {
			t.Errorf("%s refused a stock package with its boot chain: %v", tc.model, err)
		}
	}
}

// Both SPLs this board can legitimately run must pass: Bose's, which begins with
// bl, and our mainline one, which begins with b. Pinning the exact instruction was
// what refused the approved stock package.
func TestAM335xSPLAcceptsBothBranchForms(t *testing.T) {
	// As mkimage writes it: the size counts the 8-byte GP header, nothing pads the end.
	spl := func(first uint32, imageSize int) []byte {
		b := make([]byte, 0x208+imageSize)
		binary.LittleEndian.PutUint32(b, 0x40)
		binary.LittleEndian.PutUint32(b[0x200:], uint32(imageSize+8))
		binary.LittleEndian.PutUint32(b[0x204:], 0x402f0400)
		binary.LittleEndian.PutUint32(b[0x208:], first)
		return b
	}
	for name, first := range map[string]uint32{"Bose bl": 0xeb000042, "mainline b": 0xea00000f} {
		if err := install.ValidateAM335xSPL(spl(first, 0x7428)); err != nil {
			t.Errorf("%s SPL refused: %v", name, err)
		}
	}
	for name, b := range map[string][]byte{
		"not a branch": spl(0xe1a00000, 0x100),
		"wrong load address": func() []byte {
			b := spl(0xea00000f, 0x100)
			binary.LittleEndian.PutUint32(b[0x204:], 0x80008000)
			return b
		}(),
		"no config header":   func() []byte { b := spl(0xea00000f, 0x100); binary.LittleEndian.PutUint32(b, 0xea00000f); return b }(),
		"image past the end": func() []byte { b := spl(0xea00000f, 0x100); binary.LittleEndian.PutUint32(b[0x200:], 0x9999); return b }(),
		"one byte short":     spl(0xea00000f, 0x100)[:0x307],
		"too short":          spl(0xea00000f, 0x100)[:0x100],
	} {
		if err := install.ValidateAM335xSPL(b); err == nil {
			t.Errorf("%s was accepted as an SPL", name)
		}
	}
}

// An ordinary update reinstalls the boot chain the board already runs, so nothing
// at mtd0-mtd4 changes, and erasing a partition to put back the bytes it already
// holds is pure risk.
func TestNativeInstallLeavesAnUnchangedBootChainAlone(t *testing.T) {
	data := nativeWLAPackage(t)
	var mlo, uboot bytes.Buffer
	if _, err := stu.Scan(bytes.NewReader(data), func(m stu.Module) io.Writer {
		switch m.Name {
		case "soundtap-spl":
			return &mlo
		case "soundtap-uboot":
			return &uboot
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f := &fakeFlash{writes: map[string][]byte{
		"/dev/mtd0": mlo.Bytes(), "/dev/mtd1": mlo.Bytes(),
		"/dev/mtd2": mlo.Bytes(), "/dev/mtd3": mlo.Bytes(),
		"/dev/mtd4": twoUBoots(uboot.Bytes()),
	}}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	rec := installWLA(t, s, data)
	if rec.Code != 200 {
		t.Fatalf("native install with an unchanged boot chain = %d %s", rec.Code, rec.Body)
	}
	for _, dev := range []string{"/dev/mtd0", "/dev/mtd1", "/dev/mtd2", "/dev/mtd3", "/dev/mtd4"} {
		if strings.Contains(strings.Join(f.cmds, "\n"), "flash_erase "+dev) {
			t.Errorf("%s was erased although it already held these bytes", dev)
		}
	}
}

// A changing chain needs nothing beyond the two acknowledgements: no proof file
// that a new SPL would make stale.
func TestNativeInstallWritesAChangingChainOnceAcknowledged(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	rec := installWLA(t, s, nativeWLAPackage(t))
	if rec.Code != 200 {
		t.Fatalf("native install with a changing chain = %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(strings.Join(f.cmds, "\n"), "nandwrite -p /dev/mtd0") {
		t.Errorf("the primary SPL was not written: %v", f.cmds)
	}
}

// The package decides what the boot chain becomes. An ordinary update carries the chain
// the board already runs, and then mtd0-mtd4 are not touched.
func TestInstallWritesTheBootChainOnlyWhenItDiffers(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	install := func() string {
		t.Helper()
		f.cmds = nil
		rec := installWLA(t, s, nativeWLAPackage(t))
		if rec.Code != 200 {
			t.Fatalf("install = %d %s", rec.Code, rec.Body)
		}
		return strings.Join(f.cmds, "\n")
	}
	devices := []string{"/dev/mtd0", "/dev/mtd1", "/dev/mtd2", "/dev/mtd3", "/dev/mtd4"}

	joined := install()
	for _, dev := range devices {
		if !writesTo(joined, dev) {
			t.Errorf("a differing chain did not reach %s:\n%s", dev, joined)
		}
	}
	joined = install()
	for _, dev := range devices {
		if writesTo(joined, dev) || strings.Contains(joined, "flash_erase "+dev) {
			t.Errorf("an unchanged chain was rewritten on %s:\n%s", dev, joined)
		}
	}
	if !strings.Contains(joined, "nandwrite -p /dev/mtd6") {
		t.Errorf("the kernel was not written:\n%s", joined)
	}
}

// A writer that dies closes the pipe, so the scan always reports the same
// "read/write on closed pipe" and says nothing about why. The command's own output
// is in writeErr, and reporting the pipe error instead threw it away -- which is
// what made a failed stock install on the board look like it had no cause at all.
func TestAFailedFlashWriteReportsWhatTheCommandSaid(t *testing.T) {
	f := &fakeFlash{fail: "ubiformat /dev/mtd8 -f - -S 512 -s 2048 -y"}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048")
	var logbuf strings.Builder
	s.log = slog.New(slog.NewTextHandler(&logbuf, nil))

	rec := installWLA(t, s, stockWLAPackage(t))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "writing to flash failed") {
		t.Fatalf("install = %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(logbuf.String(), "injected flash failure") {
		t.Errorf("the log lost the cause:\n%s", logbuf.String())
	}
	if strings.Contains(logbuf.String(), "closed pipe") {
		t.Errorf("the log reported the symptom instead of the cause:\n%s", logbuf.String())
	}
}

// A boot U-Boot reached by falling back leaves the failed slot selected. The confirm has
// to select the slot that is running, or every later boot pays the boot limit again.
func TestFallbackBootSelectsTheRunningSlot(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=8,2048")
	fallback := false
	s.bootGuardFn = func(_ uint32, settle func(bool) error) error { return settle(fallback) }
	if err := s.settleBootGuard(true); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("an ordinary boot confirm wrote NAND: %v", f.cmds)
	}
	fallback = true
	if err := s.settleBootGuard(true); err != nil {
		t.Fatal(err)
	}
	env := envOf(t, f)
	if got := flash.SelectedSlot(env); got != "PING" {
		t.Errorf("selected %q after a fallback into PING", got)
	}
}

// An install's selection carries upgrade_available, and U-Boot keeps that slot's boot
// count in the environment: the guard is handed that count, and the confirm clears both,
// in one write, and only then the RTC.
func TestBootConfirmClearsTheEnvironmentCount(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG, selected
	env := soundtapEnv(t)
	env.Set("upgrade_available", "1")
	env.Set("bootcount", "2")
	if err := ubootenvtest.Image(f.writes, "/dev/mtd5", "/dev/mtd4", env); err != nil {
		t.Fatal(err)
	}
	var handed uint32
	s.bootGuardFn = func(envCount uint32, settle func(bool) error) error {
		handed = envCount
		return settle(false)
	}
	if err := s.settleBootGuard(true); err != nil {
		t.Fatal(err)
	}
	if handed != 2 {
		t.Errorf("the guard was handed count %d, want the environment's 2", handed)
	}
	after := envOf(t, f)
	for _, key := range []string{"upgrade_available", "bootcount"} {
		if v, ok := after.Get(key); ok {
			t.Errorf("%s=%q survived the confirm", key, v)
		}
	}
	if got := flash.SelectedSlot(after); got != "PONG" {
		t.Errorf("selected %q, want the running PONG", got)
	}
	// Confirmed already: nothing more to write.
	n := len(f.cmds)
	if err := s.settleBootGuard(true); err != nil || len(f.cmds) != n {
		t.Fatalf("a second confirm: %v, wrote %v", err, f.cmds[n:])
	}
	// An environment nothing can read holds no count: the RTC's still confirms the boot.
	for _, dev := range []string{"/dev/mtd4", "/dev/mtd5"} {
		for i := range f.writes[dev] {
			f.writes[dev][i] = 0xa5
		}
	}
	confirmed := false
	s.bootGuardFn = func(envCount uint32, settle func(bool) error) error {
		if err := settle(false); err != nil {
			return err
		}
		confirmed = envCount == 0
		return nil
	}
	if err := s.settleBootGuard(true); err != nil || !confirmed {
		t.Fatalf("a confirm over an unreadable environment: %v, confirmed %v", err, confirmed)
	}
}

// An install selects the other slot and leaves it unconfirmed; POST /api/reboot follows.
// The reboot's settle confirms the boot that is running, not the selection that has yet
// to boot, so the new slot counts its trial boots in the environment (ADR 0010).
func TestRebootAfterAnInstallKeepsTheNewSlotUnconfirmed(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=8,2048") // running PING
	env := soundtapEnv(t)                                          // selects PONG
	env.Set("upgrade_available", "1")
	env.Set("altbootcmd", "run nand_boot;")
	env.Set("bootlimit", "4")
	if err := ubootenvtest.Image(f.writes, "/dev/mtd5", "/dev/mtd4", env); err != nil {
		t.Fatal(err)
	}
	settled := false
	s.bootGuardFn = func(_ uint32, settle func(bool) error) error {
		settled = true
		return settle(false)
	}
	called := make(chan struct{}, 1)
	s.rebootFn = func() error { called <- struct{}{}; return nil }
	rec := httptest.NewRecorder()
	s.reboot(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/reboot", strings.NewReader(`{"confirm":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("reboot = %d %s", rec.Code, rec.Body)
	}
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("the reboot hook never ran")
	}
	if !settled {
		t.Fatal("the reboot did not settle the boot guard")
	}
	after := envOf(t, f)
	if v, _ := after.Get("upgrade_available"); v != "1" {
		t.Errorf("upgrade_available = %q after the reboot, want the install's 1", v)
	}
	if got := flash.SelectedSlot(after); got != "PONG" {
		t.Errorf("selected %q, want the installed PONG", got)
	}
}

// A restart from the page in the first minute of a new version is no proof that it works:
// the slot stays on trial, and only the count that restart would have added goes.
func TestRebootDuringATrialBootKeepsTheTrial(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	env := soundtapEnv(t)                                          // selects PONG
	env.Set("upgrade_available", "1")
	env.Set("altbootcmd", "run nand_boot;")
	env.Set("bootlimit", "4")
	env.Set("bootcount", "2")
	if err := ubootenvtest.Image(f.writes, "/dev/mtd5", "/dev/mtd4", env); err != nil {
		t.Fatal(err)
	}
	s.bootGuardFn = func(_ uint32, settle func(bool) error) error { return settle(false) }
	called := make(chan struct{}, 1)
	s.rebootFn = func() error { called <- struct{}{}; return nil }
	reboot := func() {
		t.Helper()
		rec := httptest.NewRecorder()
		s.reboot(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/reboot", strings.NewReader(`{"confirm":true}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("reboot = %d %s", rec.Code, rec.Body)
		}
		select {
		case <-called:
		case <-time.After(5 * time.Second):
			t.Fatal("the reboot hook never ran")
		}
	}
	reboot()
	after := envOf(t, f)
	if v, _ := after.Get("upgrade_available"); v != "1" {
		t.Errorf("upgrade_available = %q after a restart on trial, want 1", v)
	}
	if v, ok := after.Get("bootcount"); ok {
		t.Errorf("bootcount = %q after a deliberate restart, want it gone", v)
	}
	// The confirm, once this boot has proven itself, ends the trial. (A reboot that went
	// out holds the lane until the speaker is gone, so the confirm runs on its own here.)
	if err := s.settleBootGuard(true); err != nil {
		t.Fatal(err)
	}
	if v, ok := envOf(t, f).Get("upgrade_available"); ok {
		t.Errorf("upgrade_available = %q after the confirm", v)
	}
}

// GET /api/health says whether the boot is confirmed (tools/hil/ waits for it on a
// production image): only a confirm that went through counts.
func TestBootConfirmedOnlyAfterTheConfirm(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=8,2048")
	failed := errors.New("RTC module stays idle")
	s.bootGuardFn = func(uint32, func(bool) error) error { return failed }
	if err := s.ConfirmBoot(); !errors.Is(err, failed) {
		t.Fatalf("ConfirmBoot = %v, want the guard's error", err)
	}
	if s.BootConfirmed() {
		t.Fatal("a boot whose count was not reset reports confirmed")
	}
	s.bootGuardFn = func(uint32, func(bool) error) error { return nil }
	if s.BootConfirmed() {
		t.Fatal("confirmed before ConfirmBoot ran")
	}
	if err := s.ConfirmBoot(); err != nil {
		t.Fatal(err)
	}
	if !s.BootConfirmed() {
		t.Error("a confirmed boot reports unconfirmed")
	}
}

// A first install selected PONG and the speaker was never rebooted into it, so it still
// runs PING. A second install is then cut off mid-package: the environment must be back
// on PING, the slot that is whole, and it must have been put there before PONG was erased.
func TestInterruptedReinstallLeavesTheRunningSlotSelected(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=8,2048") // running PING
	// soundtapEnv selects PONG: the unrebooted first install.
	pkg := stockWLAPackage(t)
	rec := installWLA(t, s, pkg[:len(pkg)-16]) // cut off inside its last module
	if rec.Code == 200 {
		t.Fatalf("a truncated package installed: %s", rec.Body)
	}
	env := envOf(t, f)
	if got := flash.SelectedSlot(env); got != "PING" {
		t.Fatalf("environment selects %q after the failed install, want PING; %s\n%s", got, rec.Body, strings.Join(f.cmds, "\n"))
	}
	joined := strings.Join(f.cmds, "\n")
	envAt, eraseAt := strings.Index(joined, "fw_setenv -c"), strings.Index(joined, "flash_erase /dev/mtd7")
	if envAt < 0 || eraseAt < 0 || envAt > eraseAt {
		t.Fatalf("PING was not selected before PONG was erased:\n%s", joined)
	}
}

// The ST300's way back to Bose: Bose's package carries STP_Update and the .bos
// for the soundbar processor next to the SM2 module's four images. Only those four are
// written; the processor modules are read for their CRC and skipped.
func TestST300InstallsBosePackageWithoutTouchingTheProcessor(t *testing.T) {
	f := &fakeFlash{}
	s := wlaInstallServer(t, f, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
	s.firmwareModel = func() (string, error) { return "st300", nil }
	var logbuf strings.Builder
	s.log = slog.New(slog.NewTextHandler(&logbuf, nil))
	rec := installWLA(t, s, stockPackage(t, stu.ExpectedST300))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("Bose's ST300 package = %d %s\n%s", rec.Code, rec.Body, logbuf.String())
	}
	joined := strings.Join(f.cmds, "\n")
	for _, want := range []string{"ubiformat /dev/mtd8", "nandwrite -p /dev/mtd6", "fw_setenv -c"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q from:\n%s", want, joined)
		}
	}
	for _, forbidden := range []string{"/dev/mtd7", "/dev/mtd9", "STP_Update", ".bos"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("the install touched %s:\n%s", forbidden, joined)
		}
	}
}
