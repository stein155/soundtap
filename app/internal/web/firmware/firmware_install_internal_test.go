// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/flash/flashtest"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/stu"

	"github.com/stein155/soundtap/app/internal/ubootenv"
	"github.com/stein155/soundtap/app/internal/ubootenv/ubootenvtest"
)

func TestFirmwareUploadIsSingleFlight(t *testing.T) {
	t.Parallel()

	post := func(handler *Handler, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader("anything"))
		req.Header.Set("X-SoundTap-Confirm", confirmInstall)

		rec := httptest.NewRecorder()
		handler.postFirmware(rec, req)

		return rec
	}
	busy := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == 409 && strings.Contains(rec.Body.String(), "in progress")
	}

	handler := New(nil, "")
	handler.firmware.mu.Lock()
	if rec := post(handler, "/api/firmware?install=1"); !busy(rec) {
		t.Errorf("install during an install = %d %q, want 409 with recovery reason", rec.Code, rec.Body.String())
	}
	// A probe only scans: it neither waits for an install nor holds one off.
	if rec := post(handler, "/api/firmware"); busy(rec) {
		t.Errorf("probe during an install = %d, want it scanned", rec.Code)
	}
	handler.firmware.mu.Unlock()

	// A probe that trickles in must not make the UI's next check a 409.
	pr, pipeWriter := io.Pipe()
	defer pipeWriter.Close()

	go func() {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/firmware", pr)
		handler.postFirmware(httptest.NewRecorder(), req)
	}()

	_, _ = pipeWriter.Write([]byte{0}) // returns once the first probe is reading, so it is in flight

	if rec := post(handler, "/api/firmware"); busy(rec) {
		t.Errorf("probe during a slow probe = %d %q, want it scanned", rec.Code, rec.Body.String())
	}
}

// A fake NAND: what got run, and what reached the writers.
type fakeFlash struct {
	cmds   []string
	bytes  int64
	fail   string
	writes map[string][]byte
	// ubi is what ubiformat was given per MTD device, attached which device each
	// ubiattach -d number stands for: what open reads a written volume back from.
	ubi           map[string][]byte
	attached      map[string]string
	corruptVolume bool
}

// newFakeFlash is an empty fake NAND that fails nothing.
func newFakeFlash() *fakeFlash {
	return &fakeFlash{
		cmds: nil, bytes: 0, fail: "", writes: nil, ubi: nil, attached: nil, corruptVolume: false,
	}
}

// fakePEB is the eraseblock size of the fake NAND.
const fakePEB = 0x20000

var (
	// errInjected is the failure fakeFlash.fail injects.
	errInjected = errors.New("injected flash failure")
	// errNoDevice is a UBI volume the fake NAND never attached.
	errNoDevice = errors.New("no such device")
)

func (f *fakeFlash) run(stdin io.Reader, name string, args ...string) error {
	f.cmds = append(f.cmds, name+" "+strings.Join(args, " "))
	if f.fail != "" && strings.HasSuffix(f.cmds[len(f.cmds)-1], f.fail) {
		return errInjected
	}

	if name == "fw_setenv" {
		if f.writes == nil {
			f.writes = map[string][]byte{}
		}

		err := ubootenvtest.SetEnv(f.writes, args)
		if err != nil {
			return fmt.Errorf("fw_setenv: %w", err)
		}
	}

	if name == "nandwrite" || name == "dd" {
		src, dev, off := args[len(args)-1], args[len(args)-2], flagValue(args, "-s")

		var skip, count int

		if name == "dd" { // dd if= of= bs= count= skip= seek=, in blocks of bs
			values := map[string]int{}

			for _, a := range args[2:] {
				k, n, _ := strings.Cut(a, "=")
				values[k], _ = strconv.Atoi(n)
			}

			src, dev = strings.TrimPrefix(args[0], "if="), strings.TrimPrefix(args[1], "of=")
			off, skip, count = values["seek"]*values["bs"], values["skip"]*values["bs"], values["count"]*values["bs"]
		}

		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}

		if count > 0 {
			data = data[min(skip, len(data)):min(skip+count, len(data))]
		}

		if f.writes == nil {
			f.writes = map[string][]byte{}
		}
		// The write lands inside what the last erase left.
		old := f.writes[dev]
		if len(old) < off+len(data) {
			old = append(old, bytes.Repeat([]byte{0xff}, off+len(data)-len(old))...)
		}

		copy(old[off:], data)
		f.writes[dev] = old
	}

	if name == "flash_erase" && len(args) == 3 && f.writes != nil {
		// flash_erase dev start blocks; 0 blocks is to the end. 128 KiB blocks.
		dev := args[0]
		start, _ := strconv.Atoi(args[1])
		blocks, _ := strconv.Atoi(args[2])

		old := f.writes[dev]
		if blocks == 0 {
			f.writes[dev] = old[:min(start, len(old))]
		} else {
			for i := start; i < min(start+blocks*0x20000, len(old)); i++ {
				old[i] = 0xff
			}
		}
	}

	if name == "ubiattach" { // ubiattach -m mtd -d ubi ...
		if f.attached == nil {
			f.attached = map[string]string{}
		}

		f.attached[args[3]] = "/dev/mtd" + args[1]
	}

	if stdin != nil {
		var sink bytes.Buffer

		n, err := io.Copy(&sink, stdin)

		f.bytes += n
		if name == "ubiformat" {
			if f.ubi == nil {
				f.ubi = map[string][]byte{}
			}

			f.ubi[args[0]] = sink.Bytes()
		}

		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

// open reads volume 0 of an attached UBI device back, /dev/ubi7_0, from the image ubiformat
// was given; corruptVolume flips a bit in it first.
func (f *fakeFlash) open(name string) (io.ReadCloser, error) {
	ubi, vol, ok := strings.Cut(strings.TrimPrefix(name, "/dev/ubi"), "_")

	mtd := f.attached[ubi]
	if !ok || vol != "0" || f.ubi[mtd] == nil {
		return nil, fmt.Errorf("open %s: %w", name, errNoDevice)
	}

	volume := flashtest.UBIVolume(f.ubi[mtd], fakePEB)
	if f.corruptVolume {
		volume[len(volume)/2] ^= 1
	}

	return io.NopCloser(bytes.NewReader(volume)), nil
}

// read is fw_printenv, or nanddump, over the fake: -s and -l honoured, so a readback of one half of a
// partition sees that half.
func (f *fakeFlash) read(name string, args ...string) ([]byte, error) {
	if name == "fw_printenv" {
		env, err := ubootenvtest.PrintEnv(f.writes, args)
		if err != nil {
			return nil, fmt.Errorf("fw_printenv: %w", err)
		}

		return env, nil
	}

	if name == "cat" { // VerifyRootfs reading the written slot's /boot back
		if b, ok := f.writes[args[0]]; ok {
			return b, nil
		}

		return flashtest.BootFile(args[0]), nil
	}

	data := f.writes[args[len(args)-1]]
	if off := flagValue(args, "-s"); off > 0 {
		data = data[min(off, len(data)):]
	}

	if n := flagValue(args, "-l"); n > 0 && n < len(data) {
		data = data[:n]
	} else if n > len(data) && strings.HasPrefix(args[len(args)-1], "/dev/mtd") && name == "nanddump" {
		// Flash nothing was written to reads erased, as far as it is asked to.
		data = append(append([]byte(nil), data...), bytes.Repeat([]byte{0xff}, n-len(data))...)
	}

	return data, nil
}

// envOf is the environment U-Boot boots from the fake: the redundant one when a copy
// checks out, the single 8 KiB copy otherwise.
func envOf(t *testing.T, f *fakeFlash) *ubootenv.Env {
	t.Helper()

	env, err := ubootenvtest.Load(f.writes, "/dev/mtd5", "/dev/mtd4")
	if err != nil {
		t.Fatal(err)
	}

	return env
}

// writesTo reports whether the commands in joined write dev: nandwrite, or dd for U-Boot.
func writesTo(joined, dev string) bool {
	return strings.Contains(joined, "nandwrite -p "+dev) || strings.Contains(joined, "of="+dev+" ")
}

func flagValue(args []string, flag string) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			n, _ := strconv.Atoi(args[i+1])

			return n
		}
	}

	return 0
}

func installServer(t *testing.T, nand *fakeFlash) *Handler {
	t.Helper()

	target, err := flash.Plan("root=ubi0:rootfs ubi.mtd=8,2048", map[string]flash.Partition{
		"NAND.SPL":              {Index: 0, Size: 0x20000, EraseSize: 0x20000},
		"NAND.SPL.backup1":      {Index: 1, Size: 0x20000, EraseSize: 0x20000},
		"NAND.SPL.backup2":      {Index: 2, Size: 0x20000, EraseSize: 0x20000},
		"NAND.SPL.backup3":      {Index: 3, Size: 0x20000, EraseSize: 0x20000},
		"NAND.U-Boot":           {Index: 4, Size: 0x1e0000, EraseSize: 0x20000},
		"NAND.Kernel-Ping":      {Index: 6, Size: 0x500000, EraseSize: 0},
		"NAND.Kernel-Pong":      {Index: 7, Size: 0x500000, EraseSize: 0},
		"NAND.File-System-Ping": {Index: 8, Size: 0x6000000, EraseSize: fakePEB},
		"NAND.File-System-Pong": {Index: 9, Size: 0x6000000, EraseSize: fakePEB},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := New(slog.New(slog.DiscardHandler), "")
	handler.flashPlan = func() (flash.Target, error) { return target, nil }
	handler.flashTools = func() []string { return nil }
	handler.flashRunner = nand.run
	handler.flashOpen = nand.open
	handler.flashReader = func(name string, args ...string) ([]byte, error) {
		return nand.read(name, args...)
	}
	handler.firmwareModel = func() (string, error) { return "st10", nil }

	return handler
}

func post(t *testing.T, handler *Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, installPath, strings.NewReader(string(body)))
	req.Header.Set("X-SoundTap-Confirm", "install")

	rec := httptest.NewRecorder()
	handler.postFirmware(rec, req)

	return rec
}

// A package that is not a package must not reach NAND at all. It never gets as
// far as its rootfs module, and nothing before that point is destructive, so the
// alternate slot is left exactly as it was — still a bootable fallback.
func TestRejectedPackageTouchesNothing(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()

	rec := post(t, installServer(t, nand), []byte(strings.Repeat("not firmware", 200)))
	if rec.Code < 400 {
		t.Fatalf("rubbish accepted: %d %s", rec.Code, rec.Body)
	}

	if len(nand.cmds) != 0 {
		t.Fatalf("a rejected package still wrote to NAND: %v", nand.cmds)
	}
}

// An install that started and failed is reported once, after the active flag is
// cleared, so the panel can show it after the progress screen.
//
//nolint:paralleltest // it counts the process-wide counters.InstallFailures, which any install moves
func TestFailedInstallIsReported(t *testing.T) {
	handler := installServer(t, newFakeFlash())

	var events []string

	handler.SetActiveFn(func(active bool) { events = append(events, fmt.Sprint("active=", active)) })
	handler.SetFailedFn(func() { events = append(events, "failed") })

	before := counters.InstallFailures.Load()

	if rec := post(t, handler, []byte(strings.Repeat("not firmware", 200))); rec.Code < 400 {
		t.Fatalf("rubbish accepted: %d", rec.Code)
	}

	if got := strings.Join(events, " "); got != "active=true active=false failed" {
		t.Errorf("events = %q", got)
	}
	// /api/health counts it too (ADR 0127).
	if n := counters.InstallFailures.Load() - before; n != 1 {
		t.Errorf("%d install failures counted, want 1", n)
	}
}

// Off-device there is no NAND. Simulating an install would be worse than saying so.
// An interrupted install used to leave its staged kernel behind, and the next one then
// failed with "no space left on device" -- 4.7 MB of kernel into an 8 MiB tmpfs that also
// holds the log. Staging goes to the home directory, and what an earlier attempt left is
// cleared before the next one starts.
func TestStagingClearsWhatAnInterruptedInstallLeft(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	stale := filepath.Join(dir, "soundtap-kernel-oldandlarge")

	err := os.WriteFile(stale, []byte("left behind"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	keep := filepath.Join(dir, "firmware-pending.json")

	err = os.WriteFile(keep, []byte("{}"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := New(nil, dir)
	if got := s.stagingDir(); got != dir {
		t.Fatalf("stagingDir = %q, want the home directory", got)
	}

	install.RemoveStaleStaging(s.stagingDir())

	_, err = os.Stat(stale)
	if !os.IsNotExist(err) {
		t.Errorf("the stale staged kernel survived: %v", err)
	}

	_, err = os.Stat(keep)
	if err != nil {
		t.Errorf("cleanup removed a file that is not staging: %v", err)
	}
}

func TestInstallRefusedWithoutNAND(t *testing.T) {
	t.Parallel()

	s := New(nil, "")

	rec := post(t, s, []byte("anything"))
	if rec.Code != 501 {
		t.Fatalf("install off-device = %d, want 501", rec.Code)
	}
}

// A speaker that cannot tell which slot it is running must refuse: the only way
// to be wrong is to erase the slot it is running from.
func TestInstallRefusedWhenTheSlotIsUnknown(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()
	s := installServer(t, nand)
	s.flashPlan = func() (flash.Target, error) { return flash.Target{}, flash.ErrUnknownSlot }

	rec := post(t, s, []byte("anything"))
	if rec.Code != 412 {
		t.Fatalf("unknown slot = %d, want 412", rec.Code)
	}

	if len(nand.cmds) != 0 {
		t.Fatalf("it flashed anyway: %v", nand.cmds)
	}
}

func TestFirmwareInstallRequiresMatchingKnownModel(t *testing.T) {
	t.Parallel()

	shared := stu.Module{Name: "ubi.img", Size: 1 << 20, Target: "", Targets: []string{"st10", "wla"}, Native: false}
	st20 := stu.Module{Name: "ubi.img", Size: 1 << 20, Target: "st20", Targets: []string{"st20"}, Native: false}
	st300 := stu.Module{Name: "ubi.img", Size: 1 << 20, Target: "st300", Targets: []string{"st300"}, Native: false}

	err := install.ModelRefusal("st20", st20)
	if err != nil {
		t.Errorf("an ST20 refused its own firmware: %v", err)
	}

	err = install.ModelRefusal("st10", shared)
	if err != nil {
		t.Errorf("an ST10 refused firmware: %v", err)
	}

	err = install.ModelRefusal("st10", st20)
	if err == nil {
		t.Error("cross-model package was accepted")
	}

	err = install.ModelRefusal("unknown", shared)
	if err == nil {
		t.Error("unknown hardware accepted a firmware install")
	}
	// The way back to Bose: its processor modules are skipped, not refused.
	err = install.ModelRefusal("st300", st300)
	if err != nil {
		t.Errorf("an ST300 refused Bose's own package: %v", err)
	}

	st300.Native = true

	err = install.ModelRefusal("st300", st300)
	if err != nil {
		t.Errorf("an ST300 refused our own package: %v", err)
	}
	// The Wireless Link is now installable through this endpoint: it has no
	// retained stock updater, so refusing here would leave it with no way back.
	err = install.ModelRefusal("wla", shared)
	if err != nil {
		t.Errorf("a Wireless Link refused firmware: %v", err)
	}

	err = install.ModelRefusal("wla", st20)
	if err == nil {
		t.Error("an ST20 package was accepted on a Wireless Link")
	}
	// Each model keeps its own ceiling, even though they share a module chain.
	big := stu.Module{
		Name: "ubi.img", Size: stu.RootfsLimit("wla"), Target: "", Targets: []string{"st10", "wla"}, Native: false,
	}

	err = install.ModelRefusal("st10", big)
	if err == nil {
		t.Error("an ST10 accepted a Wireless-Link-sized rootfs")
	}

	err = install.ModelRefusal("wla", big)
	if err != nil {
		t.Errorf("a Wireless Link refused its own stock rootfs size: %v", err)
	}
}

func TestWLANativeFourModuleChainMapsToInstallPayloads(t *testing.T) {
	t.Parallel()

	data := buildSTUChain(t, stu.ExpectedWLANative, nil)
	copy(data[4:8], "RTCH")
	// Header edits change package and first-module CRCs.
	binary.BigEndian.PutUint32(data[stuPkgCRCOff:], crc32.ChecksumIEEE(data[:stuPkgCRCOff]))
	binary.BigEndian.PutUint32(data[stuHeaderCRCOff:], crc32.ChecksumIEEE(data[stuOverlap:stuHeaderCRCOff]))

	pkg, err := stu.Scan(bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}

	if !pkg.Native || pkg.Target != "wla" || len(pkg.Modules) != 4 {
		t.Fatalf("native package = %+v", pkg)
	}

	for _, name := range []string{"soundtap-kernel", "soundtap-rootfs", "soundtap-spl", "soundtap-uboot"} {
		if _, ok := pkg.Module(name); !ok {
			t.Errorf("missing %s", name)
		}
	}

	err = install.PackageRefusal("wla", pkg)
	if err != nil {
		t.Fatalf("native WLA package refused: %v", err)
	}
}

// Keep the pre-write contract of the WLA install in one table. The successful write order and
// readback path are covered by TestWLAInstallsStockPackageIntoInactiveSlot and
// the bootloader-specific tests below.
func TestWLAInstallContractGates(t *testing.T) {
	t.Parallel()

	native := buildSTUChain(t, stu.ExpectedWLANative, nil)
	copy(native[4:8], "RTCH")
	binary.BigEndian.PutUint32(native[stuPkgCRCOff:], crc32.ChecksumIEEE(native[:stuPkgCRCOff]))
	binary.BigEndian.PutUint32(native[stuHeaderCRCOff:], crc32.ChecksumIEEE(native[stuOverlap:stuHeaderCRCOff]))

	for _, tcase := range []struct {
		name  string
		path  string
		body  []byte
		code  int
		check func(t *testing.T, nand *fakeFlash, body string)
	}{
		{
			name: "report only", path: "/api/firmware", body: native, code: http.StatusOK,
			check: func(t *testing.T, nand *fakeFlash, body string) {
				t.Helper()

				if len(nand.cmds) != 0 || !strings.Contains(body, `"installable":true`) {
					t.Fatalf("report wrote=%v body=%s", nand.cmds, body)
				}
			},
		},
		{
			name: "a package that is not one", path: installPath,
			body: []byte("ignored"), code: http.StatusUnsupportedMediaType, check: nil,
		},
	} {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			nand := newFakeFlash()
			handler := installServer(t, nand)
			handler.firmwareModel = func() (string, error) { return "wla", nil }

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tcase.path, bytes.NewReader(tcase.body))
			if strings.Contains(tcase.path, "install=1") {
				req.Header.Set("X-SoundTap-Confirm", "install")
			}

			rec := httptest.NewRecorder()
			handler.postFirmware(rec, req)

			if rec.Code != tcase.code {
				t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body, tcase.code)
			}

			if len(nand.cmds) != 0 {
				t.Fatalf("gate reached NAND: %v", nand.cmds)
			}

			if tcase.check != nil {
				tcase.check(t, nand, rec.Body.String())
			}
		})
	}

	pkg := &stu.Package{
		Size: 0, Target: "", Targets: nil, Mainline: true, KernelTarget: "st10", Native: false,
		Modules: []stu.Module{{Name: "soundtap-rootfs", Size: 1 << 20, Target: "wla", Targets: nil, Native: false}},
	}

	err := install.PackageRefusal("wla", pkg)
	if err == nil || !strings.Contains(err.Error(), "st10 kernel") {
		t.Fatalf("kernel target mismatch = %v", err)
	}
}

// The duo package's one kernel is named after the ST10 inside its uImage; its
// soundtap-kernel.binky and .binky bootloaders are what say it is also the WLA's.
func TestWLAAcceptsTheUniversalPackage(t *testing.T) {
	t.Parallel()

	mods := make([]stu.Module, 0, len(stu.ExpectedSoundTapDuo))
	for _, name := range stu.ExpectedSoundTapDuo {
		mods = append(mods, stu.Module{Name: name, Size: 1 << 16, Target: "wla", Targets: nil, Native: false})
	}

	pkg := &stu.Package{
		Size: 0, Modules: mods, Target: "", Targets: nil, Mainline: true, KernelTarget: "st10", Native: false,
	}

	err := install.PackageRefusal("wla", pkg)
	if err != nil {
		t.Errorf("wla refused the duo package: %v", err)
	}

	spl, uboot, ok := install.BootloaderModules(pkg, "wla")
	if !ok || spl.Name != "soundtap-spl.binky" || uboot.Name != "soundtap-uboot.binky" {
		t.Errorf("bootloaders = %q %q %v", spl.Name, uboot.Name, ok)
	}

	if got := install.SuffixedKernelModels(pkg); strings.Join(got, ",") != "ST10,WLA,ST300" {
		t.Errorf("models = %v", got)
	}

	err = install.PackageRefusal("st20", pkg)
	if err == nil {
		t.Error("st20 took a package with no kernel for it")
	}
}

// The tri package's kernel identity is spotty's, the last kernel module scanned; the
// suffixed kernels are what make it the ST10's and the WLA's too.
func TestST10AndWLAAcceptTheTriPackage(t *testing.T) {
	t.Parallel()

	mods := make([]stu.Module, 0, len(stu.ExpectedSoundTapTri))
	for _, name := range stu.ExpectedSoundTapTri {
		mods = append(mods, stu.Module{Name: name, Size: 1 << 16, Target: "wla", Targets: nil, Native: false})
	}

	pkg := &stu.Package{
		Size: 0, Modules: mods, Target: "", Targets: nil, Mainline: true, KernelTarget: "st20", Native: false,
	}

	err := install.PackageRefusal("wla", pkg)
	if err != nil {
		t.Errorf("wla refused the tri package: %v", err)
	}

	if got := install.SuffixedKernelModels(pkg); strings.Join(got, ",") != "ST10,WLA,ST300,ST20" {
		t.Errorf("models = %v", got)
	}

	for i := range mods {
		mods[i].Target = "st10"
	}

	err = install.PackageRefusal("st10", pkg)
	if err != nil {
		t.Errorf("st10 refused the tri package: %v", err)
	}
}

func TestST300DoesNotReachNAND(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()
	s := installServer(t, nand)
	s.firmwareModel = func() (string, error) { return "st300", nil }

	rec := post(t, s, buildSTUChain(t, stu.ExpectedST300, nil))
	if rec.Code < 400 {
		t.Fatalf("ST300 package accepted: %d %s", rec.Code, rec.Body)
	}

	if len(nand.cmds) != 0 {
		t.Fatalf("ST300 package reached NAND: %v", nand.cmds)
	}
}

func TestST300KernelCannotUseWLAStockException(t *testing.T) {
	t.Parallel()

	data := buildSTUChain(t, stu.Expected, nil)
	off := 0

	for _, name := range stu.Expected {
		if name == "linuxPatched" {
			break
		}

		off += stuHeaderSize + 0x200 - stuOverlap
	}

	header := data[off : off+stuHeaderSize]
	image := data[off+stuHeaderSize : off+stuHeaderSize+0x200]
	binary.BigEndian.PutUint32(image, 0x27051956)
	binary.BigEndian.PutUint32(image[12:], uint32(len(image)-64))
	binary.BigEndian.PutUint32(image[16:], 0x80008000)
	binary.BigEndian.PutUint32(image[20:], 0x80008000)
	binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(image[64:]))
	image[28], image[29], image[30], image[31] = 5, 2, 2, 0
	copy(image[32:], "Linux-6.13 SoundTap st300")
	binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
	copy(image[32:37], "Linub")
	binary.BigEndian.PutUint32(header[stuImageCRCOff:], crc32.ChecksumIEEE(image))
	binary.BigEndian.PutUint32(header[stuHeaderCRCOff:], crc32.ChecksumIEEE(header[stuOverlap:stuHeaderCRCOff]))

	nand := newFakeFlash()
	s := installServer(t, nand)
	s.firmwareModel = func() (string, error) { return "wla", nil }

	rec := post(t, s, data)
	if rec.Code < 400 {
		t.Fatalf("ST300 kernel accepted on WLA: %d %s", rec.Code, rec.Body)
	}

	if len(nand.cmds) != 0 {
		t.Fatalf("ST300 kernel reached WLA NAND: %v", nand.cmds)
	}
}

func TestCrossModelSTUDoesNotReachNAND(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()
	s := installServer(t, nand)

	rec := post(t, s, buildSTUChain(t, stu.ExpectedST20, nil))
	if rec.Code < 400 {
		t.Fatalf("cross-model STU accepted: %d %s", rec.Code, rec.Body)
	}

	if len(nand.cmds) != 0 {
		t.Fatalf("cross-model STU wrote NAND: %v", nand.cmds)
	}
}

func TestExactST300STUDoesNotReachSiblingNAND(t *testing.T) {
	t.Parallel()

	for _, model := range []string{"st10", "wla"} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()

			nand := newFakeFlash()
			s := installServer(t, nand)
			s.firmwareModel = func() (string, error) { return model, nil }

			rec := post(t, s, buildSTUChain(t, stu.ExpectedST300, nil))
			if rec.Code < 400 {
				t.Fatalf("ST300 package accepted on %s: %d %s", model, rec.Code, rec.Body)
			}

			if len(nand.cmds) != 0 {
				t.Fatalf("ST300 package reached %s NAND: %v", model, nand.cmds)
			}
		})
	}
}

func TestFirmwareVariantMatchingIsExact(t *testing.T) {
	t.Parallel()

	for variant, want := range map[string]string{
		"rhino": "st10", "lisa": "st20", "spotty": "st20", "binky": "wla", "ginger": "st300",
		"lisax": "", "notvariant=lisa": "",
	} {
		if got := install.ModelForVariant(variant); got != want {
			t.Errorf("install.ModelForVariant(%q) = %q, want %q", variant, got, want)
		}
	}
}

func TestFirmwareModelVariantsFailClosed(t *testing.T) {
	t.Parallel()

	for name, variants := range map[string][]string{
		"unknown plus known": {"future", "rhino"},
		"conflicting":        {"rhino", "spotty"},
		"missing":            nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			model, err := install.ModelFromVariants(variants...)
			if err == nil {
				t.Fatalf("variants %v resolved to %q", variants, model)
			}
		})
	}

	model, err := install.ModelFromVariants("lisa", "spotty")
	if err != nil || model != "st20" {
		t.Fatalf("equivalent ST20 variants resolved to %q, %v", model, err)
	}
}

func TestST20KernelMustMatchPinnedStockBytes(t *testing.T) {
	t.Parallel()

	payload := []byte("valid but not the approved ST20 kernel")
	image := make([]byte, 64+len(payload))
	binary.BigEndian.PutUint32(image, 0x27051956)
	binary.BigEndian.PutUint32(image[12:], uint32(len(payload)))
	binary.BigEndian.PutUint32(image[16:], 0x80008000)
	binary.BigEndian.PutUint32(image[20:], 0x80008000)
	binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(payload))
	image[28], image[29], image[30], image[31] = 5, 2, 2, 0
	copy(image[32:], "Linux-6.12 SoundTap st10")
	copy(image[64:], payload)
	binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
	copy(image[32:37], "Linub")

	path := t.TempDir() + "/uImage"

	err := os.WriteFile(path, image, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = install.CheckKernelForTarget(path, "st10", false)
	if err != nil {
		t.Fatalf("structurally valid ST10 kernel rejected: %v", err)
	}

	err = install.CheckKernelForTarget(path, "st20", false)
	if err == nil || !strings.Contains(err.Error(), "not st20") {
		t.Fatalf("cross-model ST20 kernel error = %v", err)
	}

	// A stock Bose kernel names no model. Every model has to be able to go back to
	// one: once a speaker runs our boot chain, nothing else on it can install firmware.
	// The header CRC is the one fixsig leaves behind, so it is computed over the
	// "Linux" name and only then written back as "Linub", exactly as the stock
	// installer produces it.
	stock := append([]byte(nil), image...)
	copy(stock[32:64], make([]byte, 32))
	copy(stock[32:], "Linux-3.14.43+")
	binary.BigEndian.PutUint32(stock[4:], 0)
	binary.BigEndian.PutUint32(stock[4:], crc32.ChecksumIEEE(stock[:64]))
	copy(stock[32:37], "Linub")

	stockPath := t.TempDir() + "/uImage-stock"

	err = os.WriteFile(stockPath, stock, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, model := range []string{"wla", "st10", "st20"} {
		err := install.CheckKernelForTarget(stockPath, model, false)
		if err != nil {
			t.Fatalf("%s refused a stock kernel: %v", model, err)
		}
	}
}

func TestOversizedKernelCannotBeForgottenAtRootfs(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()
	handler := installServer(t, nand)

	target, err := flash.Plan("ubi.mtd=8,2048", map[string]flash.Partition{
		"NAND.Kernel-Ping":      {Index: 6, Size: 0x1ff, EraseSize: 0},
		"NAND.Kernel-Pong":      {Index: 7, Size: 0x1ff, EraseSize: 0},
		"NAND.File-System-Ping": {Index: 8, Size: 0x6000000, EraseSize: 0},
		"NAND.File-System-Pong": {Index: 9, Size: 0x6000000, EraseSize: 0},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler.flashPlan = func() (flash.Target, error) { return target, nil }

	rec := post(t, handler, buildSTU(t)) // default linuxPatched is 0x200 bytes
	if rec.Code < 400 {
		t.Fatalf("oversized kernel accepted: %d %s", rec.Code, rec.Body)
	}

	if len(nand.cmds) != 0 {
		t.Fatalf("known oversized kernel still touched NAND: %v", nand.cmds)
	}
}

func TestInvalidKernelTouchesNothing(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()

	rec := post(t, installServer(t, nand), buildSTU(t)) // fixture kernel is not a uImage
	if rec.Code < 400 {
		t.Fatalf("invalid kernel accepted: %d %s", rec.Code, rec.Body)
	}

	if len(nand.cmds) != 0 {
		t.Fatalf("invalid kernel touched NAND before validation: %v", nand.cmds)
	}
}

func TestReconstructedModuleMustFitBeforeErase(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()
	handler := installServer(t, nand)

	target, err := flash.Plan("ubi.mtd=8,2048", map[string]flash.Partition{
		"NAND.Kernel-Ping":      {Index: 6, Size: 0x500000, EraseSize: 0},
		"NAND.Kernel-Pong":      {Index: 7, Size: 0x500000, EraseSize: 0},
		"NAND.File-System-Ping": {Index: 8, Size: 0x1ff, EraseSize: 0},
		"NAND.File-System-Pong": {Index: 9, Size: 0x1ff, EraseSize: 0},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler.flashPlan = func() (flash.Target, error) { return target, nil }

	rec := post(t, handler, buildSTU(t)) // default ubi.img is 0x200 bytes including overlap
	if rec.Code < 400 {
		t.Fatalf("oversized reconstructed module accepted: %d %s", rec.Code, rec.Body)
	}

	if len(nand.cmds) != 0 {
		t.Fatalf("capacity failure touched NAND: %v", nand.cmds)
	}
}

// Rebooting is how an install becomes a running firmware, so the endpoint has to
// exist on a speaker — and refuse everywhere else rather than pretend.
func TestRebootNeedsConfirmationAndAHook(t *testing.T) {
	t.Parallel()

	post := func(handler *Handler, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/reboot", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.reboot(rec, req)

		return rec
	}
	if rec := post(New(nil, ""), `{"confirm":true}`); rec.Code != 501 {
		t.Errorf("reboot off-device = %d, want 501", rec.Code)
	}

	called := make(chan struct{}, 1)

	handler := New(nil, "")
	handler.SetReboot(func() error {
		called <- struct{}{}

		return nil
	})

	if rec := post(handler, `{"confirm":false}`); rec.Code != 400 {
		t.Errorf("unconfirmed reboot = %d, want 400", rec.Code)
	}

	handler.firmware.mu.Lock()
	if rec := post(handler, `{"confirm":true}`); rec.Code != http.StatusConflict {
		t.Errorf("reboot during firmware operation = %d, want 409", rec.Code)
	}
	handler.firmware.mu.Unlock()

	select {
	case <-called:
		t.Fatal("an unconfirmed request rebooted the speaker")
	default:
	}

	if rec := post(handler, `{"confirm":true}`); rec.Code != 200 {
		t.Fatalf("confirmed reboot = %d, want 200", rec.Code)
	}

	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("a confirmed reboot never called the hook")
	}
}

// Only init.d/soundtap confirms an uploaded app, after 60 s up with its listener bound;
// a reboot inside that minute proves nothing about a hang that comes later, so the
// route leaves the upload on trial and the next boot drops it.
func TestRebootLeavesAnUploadOnTrial(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	handler := New(nil, home)
	handler.EnableSoundTapdUpdate(filepath.Join(home, "run"), filepath.Join(home, "base"), home, "app-v1.0.0",
		func() {})

	override := filepath.Join(home, "soundtapd.override")
	for _, suffix := range []string{".gz", ".base", ".trial"} {
		err := os.WriteFile(override+suffix, []byte("x"), 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	called := make(chan struct{}, 1)

	handler.SetReboot(func() error {
		called <- struct{}{}

		return nil
	})
	handler.SetBootGuard(func(uint32, func(bool) error) error { return nil })

	rec := httptest.NewRecorder()
	handler.reboot(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/reboot",
		strings.NewReader(`{"confirm":true}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("reboot = %d %s", rec.Code, rec.Body)
	}

	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("the reboot hook never ran")
	}

	for _, suffix := range []string{".gz", ".base", ".trial"} {
		_, err := os.Stat(override + suffix)
		if err != nil {
			t.Errorf("reboot changed the upload's %s: %v", suffix, err)
		}
	}
}

func TestFirmwareInstallNeedsExplicitConfirmation(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, installPath, strings.NewReader("not touched"))
	rec := httptest.NewRecorder()
	New(nil, "").postFirmware(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed firmware install = %d, want 400", rec.Code)
	}
}

func TestWLAStockChainNeedsNoUpdaterManifestIdentity(t *testing.T) {
	t.Parallel()

	sizes := map[string]int{}
	for _, name := range stu.Expected[:4] {
		sizes[name] = len("soundtap-wla-updater-manifest-v1\nname="+name+"\naction=native-endpoint\n") + stuOverlap
	}

	data := buildSTUChain(t, stu.Expected, sizes)
	moduleSize := func(name string) int {
		if size := sizes[name]; size != 0 {
			return size
		}

		return 0x200
	}

	off := 0
	for i, name := range stu.Expected {
		image := data[off+stuHeaderSize : off+stuHeaderSize+moduleSize(name)]
		if i < 4 {
			copy(image, "soundtap-wla-updater-manifest-v1\nname="+name+"\naction=native-endpoint\n")
		}

		if name == "linuxPatched" {
			binary.BigEndian.PutUint32(image, 0x27051956)
			binary.BigEndian.PutUint32(image[12:], uint32(len(image)-64))
			binary.BigEndian.PutUint32(image[16:], 0x80008000)
			binary.BigEndian.PutUint32(image[20:], 0x80008000)
			image[28], image[29], image[30], image[31] = 5, 2, 2, 0
			copy(image[32:64], make([]byte, 32))
			copy(image[32:], "Linux-6.12 SoundTap binky")

			reconstructed := append([]byte(nil), image...)
			for i := len(reconstructed) - stuOverlap; i < len(reconstructed); i++ {
				reconstructed[i] = 0xff
			}

			binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(reconstructed[64:]))
			binary.BigEndian.PutUint32(image[4:], 0)
			binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
			copy(image[32:37], "Linub")
		}

		if name == "MLO" {
			binary.LittleEndian.PutUint32(image, 0xea00000f)
		}

		if name == "u-boot.img" {
			binary.BigEndian.PutUint32(image, 0x27051956)
			binary.BigEndian.PutUint32(image[12:], uint32(len(image)-64))
			binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(image[64:]))
			binary.BigEndian.PutUint32(image[4:], 0)
			binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))
		}

		off += stuHeaderSize + moduleSize(name) - stuOverlap
	}

	off = 0
	for _, name := range stu.Expected {
		header := data[off : off+stuHeaderSize]
		image := data[off+stuHeaderSize : off+stuHeaderSize+moduleSize(name)]
		binary.BigEndian.PutUint32(header[stuImageCRCOff:], crc32.ChecksumIEEE(image))
		binary.BigEndian.PutUint32(header[stuHeaderCRCOff:], crc32.ChecksumIEEE(header[stuOverlap:stuHeaderCRCOff]))
		off += stuHeaderSize + moduleSize(name) - stuOverlap
	}

	binary.BigEndian.PutUint32(data[stuPkgCRCOff:], crc32.ChecksumIEEE(data[:stuPkgCRCOff]))
	binary.BigEndian.PutUint32(data[stuHeaderCRCOff:], crc32.ChecksumIEEE(data[stuOverlap:stuHeaderCRCOff]))

	pkg, err := stu.Scan(strings.NewReader(string(data)), nil)
	if err != nil || pkg.Native {
		t.Fatalf("stock-chain scan = %+v, %v", pkg, err)
	}
}

func TestWLABootloaderWriteStopsBeforeAnySPL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	mlo, uboot := dir+"/MLO", dir+"/u-boot.img"

	err := os.WriteFile(mlo, []byte("MLO"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(uboot, []byte("u-boot"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	write := " of=/dev/mtd4 bs=131072 count=1 skip=0 seek=8 conv=notrunc"

	nand := newFakeFlash()
	nand.fail = write

	err = install.WriteBootloaders(nand.run, func(name string, args ...string) ([]byte, error) {
		return nand.read(name, args...)
	}, mlo, uboot, true, nil)
	if err == nil {
		t.Fatal("bootloader write accepted injected failure")
	}
	// The spare's half is blocks 8-13: block 14 holds the redundant environment.
	if len(nand.cmds) != 2 || nand.cmds[0] != "flash_erase /dev/mtd4 1048576 6" ||
		!strings.HasSuffix(nand.cmds[1], write) {
		t.Fatalf("bootloader failure order = %v", nand.cmds)
	}
}

func TestWLABootloaderReadbackFailureStopsBeforeAnySPL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	mlo, uboot := dir+"/MLO", dir+"/u-boot.img"

	err := os.WriteFile(mlo, []byte("MLO"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(uboot, []byte("u-boot"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	nand := newFakeFlash()
	wrong := func(string, ...string) ([]byte, error) { return []byte("wrong"), nil }

	err = install.WriteBootloaders(nand.run, wrong, mlo, uboot, true, nil)
	if err == nil || !strings.Contains(err.Error(), "mtd4 readback") {
		t.Fatalf("readback failure = %v", err)
	}

	for _, cmd := range nand.cmds {
		if !strings.Contains(cmd, "/dev/mtd4 ") ||
			!strings.Contains(cmd, "1048576") && !strings.Contains(cmd, "seek=8 ") {
			t.Fatalf("readback failure continued: %v", nand.cmds)
		}
	}
}

// One kernel serves the ST10 and the Wireless Link, so the model inside its uImage name is
// whichever build drove it -- and a package that names its kernels per model has already
// said whose each one is, in the same field stock's installer reads. The name check then
// only has to prove it is a SoundTap mainline image.
func TestASuffixedKernelIsNotJudgedByTheModelInItsName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "uImage")
	payload := []byte("a SoundTap mainline kernel")
	image := make([]byte, 64+len(payload))
	binary.BigEndian.PutUint32(image, 0x27051956)
	binary.BigEndian.PutUint32(image[12:], uint32(len(payload)))
	binary.BigEndian.PutUint32(image[16:], 0x80008000)
	binary.BigEndian.PutUint32(image[20:], 0x80008000)
	binary.BigEndian.PutUint32(image[24:], crc32.ChecksumIEEE(payload))
	image[28], image[29], image[30], image[31] = 5, 2, 2, 0
	copy(image[32:], "Linux-6.12 SoundTap st10")
	copy(image[64:], payload)
	binary.BigEndian.PutUint32(image[4:], crc32.ChecksumIEEE(image[:64]))

	err := os.WriteFile(path, image, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = install.CheckKernelForTarget(path, "wla", true)
	if err != nil {
		t.Errorf("suffixed kernel refused on the WLA: %v", err)
	}

	err = install.CheckKernelForTarget(path, "wla", false)
	if err == nil {
		t.Error("an unsuffixed kernel naming st10 must still be refused on the WLA")
	}
}

// While the other slot holds the only copy of the codec script, a SoundTap rootfs
// may not overwrite it; Bose's package, which carries the script, still may.
func TestRootfsRefusedWhileTheCodecScriptIsOnlyInTheOtherSlot(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "codec-only-in-other-slot")

	err := codecOnlyInOtherSlot(marker)
	if err != nil {
		t.Fatalf("refused without the marker: %v", err)
	}

	err = os.WriteFile(marker, []byte("mtd9\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	handler := installServer(t, newFakeFlash())
	handler.codecMarker = marker

	var request installRequest

	request.model = modelST10
	job := handler.newInstallJob(&request, stagedFiles{kernel: nil, mlo: nil, uboot: nil})

	module := stu.Module{Name: "soundtap-rootfs", Size: 1 << 16, Target: "", Targets: nil, Native: false}
	if sink := job.writerFor(module); sink != nil || job.refusal == nil ||
		!strings.Contains(job.refusal.Error(), "only copy") {
		t.Fatalf("writer=%v refusal=%v", sink, job.refusal)
	}
}
