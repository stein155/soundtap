// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package rootd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/ubootenv"
	"github.com/stein155/soundtap/app/internal/ubootenv/ubootenvtest"
)

// ran is one command the fake helper ran, with what came on its input.
type ran struct {
	cmd   string
	input []byte
	lock  bool
}

// fakeHelper serves a Server over a real socket with everything that reaches hardware
// replaced, and returns a client for it.
type fakeHelper struct {
	srv    *Server
	client *Client
	mu     sync.Mutex
	ran    []ran
	peer   int
	rtc    uint32
	hosts  []string
	chowns []string
	power  []bool
}

func newFakeHelper(t *testing.T) *fakeHelper {
	t.Helper()
	// A short directory: a socket path is limited to about 100 bytes, which t.TempDir's
	// long names can pass.
	dir, err := os.MkdirTemp("", "rd") //nolint:usetesting // see above.
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	helper := &fakeHelper{
		srv: nil, client: nil, mu: sync.Mutex{}, ran: nil, peer: 1000, rtc: 0, hosts: nil, chowns: nil, power: nil,
	}
	server := &Server{
		nand: sync.Mutex{}, Run: nil,
		Log: slog.New(slog.DiscardHandler), UID: 1000, GID: 1000,
		Layout:   func() (Layout, error) { return NewLayout(pingBoot, table()), nil },
		DevDir:   filepath.Join(dir, "dev"),
		GPIORoot: filepath.Join(dir, "gpio"), MountDir: filepath.Join(dir, "mnt"), StateDir: dir,
		NV: filepath.Join(dir, "nv"), InitScript: "/etc/init.d/soundtap", BTCtl: "/opt/soundtap/bt-ctl.sh",
		PeerUID: func(*net.UnixConn) (int, error) {
			var p int

			helper.with(func() { p = helper.peer })

			return p, nil
		},
		SetHostname: func(n string) error {
			helper.with(func() { helper.hosts = append(helper.hosts, n) })

			return nil
		},
		BCOPower: func(on bool) error {
			helper.with(func() { helper.power = append(helper.power, on) })

			return nil
		},
		RTC: fakeRTC{helper},
		Chown: func(p string, _, _ int) error {
			helper.with(func() { helper.chowns = append(helper.chowns, strings.TrimPrefix(p, dir)) })

			return nil
		},
		Lookup: func(string) (int, int, error) { return 1001, 29, nil },
	}
	server.Run = func(_ context.Context, command *Command) (string, error) {
		record := ran{cmd: command.String(), input: nil, lock: len(command.Extra) > 0}
		if command.Stdin != nil {
			record.input, _ = io.ReadAll(command.Stdin)
		}

		if command.Name == "fw_setenv" {
			record.input, _ = os.ReadFile(command.Args[3])
			record.cmd = "fw_setenv " + mustRead(t, command.Args[1])
		}

		helper.mu.Lock()
		helper.ran = append(helper.ran, record)
		helper.mu.Unlock()

		if command.Name == "ubidetach" {
			return strings.Repeat("ubiformat: formatting eraseblock 1 -- 1 % complete\n", 4000), nil // 200 KB
		}

		if command.Name == "sh" && command.Args[1] == "reconnect" {
			return "", &ExitError{Code: 3}
		}

		return "out", nil
	}
	helper.srv = server
	sock := filepath.Join(dir, "sock")

	line, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = server.Serve(ctx, line) }()

	helper.client = NewClient(sock)
	helper.client.LocalRun = func(name string, args ...string) error {
		helper.mu.Lock()
		defer helper.mu.Unlock()

		local := ran{cmd: "local " + name + " " + strings.Join(args, " "), input: nil, lock: false}
		helper.ran = append(helper.ran, local)

		return nil
	}

	return helper
}

func mustRead(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Error(err)
	}

	return strings.TrimSpace(string(b))
}

// with runs fn holding the fake's lock: the server answers on its own goroutines.
func (f *fakeHelper) with(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fn()
}

func rtcOf(f *fakeHelper) uint32 {
	return f.snapshot().rtc
}

// recorded is a copy of what the fake recorded.
type recorded struct {
	rtc           uint32
	hosts, chowns []string
	power         []bool
}

// snapshot copies what the fake recorded.
func (f *fakeHelper) snapshot() recorded {
	var out recorded

	f.with(func() {
		out = recorded{
			rtc: f.rtc, hosts: slices.Clone(f.hosts), chowns: slices.Clone(f.chowns), power: slices.Clone(f.power),
		}
	})

	return out
}

type fakeRTC struct{ f *fakeHelper }

func (r fakeRTC) ReadScratch2() (uint32, error) {
	var v uint32

	r.f.with(func() { v = r.f.rtc })

	return v, nil
}

func (r fakeRTC) ClearScratch2() error {
	r.f.with(func() { r.f.rtc = 0 })

	return nil
}

func (f *fakeHelper) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]string, 0, len(f.ran))
	for _, r := range f.ran {
		out = append(out, r.cmd)
	}

	return out
}

func staged(t *testing.T, b []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "staged")

	err := os.WriteFile(path, b, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

// The commands internal/flash and internal/install run reach the helper as typed requests
// and come out as the same commands, with the file contents passed along, not the paths.
//
//nolint:paralleltest // it sets flash.VerifyDir, which the client reads.
func TestFlashCommandsGoThroughTheHelper(t *testing.T) {
	helper := newFakeHelper(t)
	kernel := bytes.Repeat([]byte{0xab}, 4096)

	lock, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	run := func(stdin io.Reader, name string, args ...string) {
		t.Helper()

		err := helper.client.Run(lock, stdin, name, args...)
		if err != nil {
			t.Fatalf("%s %v: %v", name, args, err)
		}
	}
	dev := helper.srv.DevDir

	run(nil, "flash_erase", "/dev/mtd7", "0", "0")
	run(nil, "nandwrite", "-p", "/dev/mtd7", staged(t, kernel))
	run(nil, "nandwrite", "-p", "-s", "1835008", "/dev/mtd4", staged(t, kernel))
	run(nil, "dd", "if="+staged(t, make([]byte, 0x40000)), "of=/dev/mtd4", "bs=131072", "count=1", "skip=1",
		"seek=9", "conv=notrunc")

	image := bytes.Repeat([]byte("ubi!"), 50000)
	run(io.MultiReader(bytes.NewReader(image)), "ubiformat", "/dev/mtd9", "-f", "-", "-S", "200000", "-s", "2048", "-y")

	flash.VerifyDir = MountDir
	defer func() { flash.VerifyDir = "/run/soundtap/verify-rootfs" }()

	run(nil, "ubiattach", "-m", "9", "-d", "7", "-O", "2048")
	run(nil, "mkdir", "-p", MountDir)
	run(nil, "mount", "-t", "ubifs", "-o", "ro", "ubi7:rootfs", MountDir)
	run(nil, "test", "-x", MountDir+"/init")
	run(nil, "umount", MountDir)
	run(nil, "ubidetach", "-d", "7")

	cfg := staged(t, []byte("/dev/mtd5 0x0 0x8000 0x20000 1\n/dev/mtd4 0x1c0000 0x8000 0x20000 1\n"))
	run(nil, "fw_setenv", "-c", cfg, "-s", staged(t, []byte("bootcmd run nand_boot2;\n")))

	want := []string{
		"flash_erase " + dev + "/mtd7 0 0",
		"nandwrite -p " + dev + "/mtd7 /proc/self/fd/0",
		"nandwrite -p -s 1835008 " + dev + "/mtd4 /proc/self/fd/0",
		"dd if=/proc/self/fd/0 of=" + dev + "/mtd4 bs=131072 count=1 skip=1 seek=9 conv=notrunc",
		"ubiformat " + dev + "/mtd9 -f - -S 200000 -s 2048 -y",
		"ubiattach -m 9 -d 7 -O 2048",
		"mount -t ubifs -o ro ubi7:rootfs " + helper.srv.MountDir,
		"local test -x " + MountDir + "/init",
		"umount " + helper.srv.MountDir,
		"ubidetach -d 7",
		"fw_setenv " + dev + "/mtd5 0x0 0x8000 0x20000 1\n" + dev + "/mtd4 0x1c0000 0x8000 0x20000 1",
	}
	if got := helper.commands(); !slices.Equal(got, want) {
		t.Fatalf("ran\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	var runs []ran

	helper.with(func() { runs = slices.Clone(helper.ran) })

	if !bytes.Equal(runs[1].input, kernel) || !bytes.Equal(runs[4].input, image) {
		t.Error("the helper did not get the staged bytes")
	}

	if string(runs[10].input) != "bootcmd run nand_boot2;\n" {
		t.Errorf("script %q", runs[10].input)
	}

	for i, r := range runs {
		if !r.lock && !strings.HasPrefix(r.cmd, "local") {
			t.Errorf("%s ran without the operations lock (%d)", r.cmd, i)
		}
	}
}

// What the NAND contract forbids is refused by the helper, whatever the client sends,
// and nothing runs.
func TestTheHelperRefusesWhatTheContractForbids(t *testing.T) {
	t.Parallel()

	helper := newFakeHelper(t)

	file, err := os.Open(staged(t, make([]byte, 4096)))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	script, err := os.Open(staged(t, []byte("bootargs init=/bin/sh\n")))
	if err != nil {
		t.Fatal(err)
	}
	defer script.Close()

	for _, test := range []struct {
		r     Request
		files []*os.File
	}{
		{*req(OpNANDErase, withMTD(8)), nil},                                           // the running rootfs
		{*req(OpNANDErase, withMTD(6)), nil},                                           // the running kernel
		{*req(OpNANDErase, withMTD(10)), nil},                                          // /mnt/nv
		{*req(OpNANDErase, withMTD(12)), nil},                                          // manufacturing data
		{*req(OpNANDWrite, withMTD(6), withInput()), []*os.File{file}},                 // the running kernel
		{*req(OpNANDWrite, withMTD(7)), nil},                                           // no input
		{*req(OpUBIFormat, withMTD(8), withSize(4096), withInput()), []*os.File{file}}, // the running rootfs
		{*req(OpUBIAttach, withMTD(10)), nil},
		{
			*req(OpEnvSet, withInput(),
				withCopies([]EnvCopy{{MTD: 5, Offset: 0, Size: ubootenv.Size, EraseSize: 0x20000}})),
			[]*os.File{script},
		},
		{
			*req(OpEnvSet, withInput(),
				withCopies([]EnvCopy{{MTD: 8, Offset: 0, Size: ubootenv.Size, EraseSize: 0x20000}})),
			[]*os.File{file},
		},
		{*req(OpHostname, withName("a;reboot")), nil},
		{*req(OpBTCtl, withName("../../bin/sh")), nil},
		{*req("shell"), nil},
		{*req(OpReboot, withInput()), []*os.File{file}},   // a descriptor for nothing
		{*req(OpNANDErase, withMTD(7), withInput()), nil}, // fewer descriptors than named
	} {
		_, err := helper.client.Do(&test.r, test.files...)
		if err == nil {
			t.Errorf("%+v answered", test.r)
		}
	}

	if got := helper.commands(); len(got) != 0 {
		t.Errorf("ran %q", got)
	}
}

func TestTheHelperAnswersOnlySoundtapdAndRoot(t *testing.T) {
	t.Parallel()

	helper := newFakeHelper(t)
	for _, uid := range []int{1000, 0} {
		helper.with(func() { helper.peer = uid })

		err := helper.client.Reboot()
		if err != nil {
			t.Errorf("uid %d refused: %v", uid, err)
		}
	}

	helper.with(func() { helper.peer = 1001 }) // spotify

	err := helper.client.Reboot()
	if err == nil {
		t.Error("another user's request answered")
	}

	if got := helper.commands(); len(got) != 2 {
		t.Errorf("ran %q", got)
	}
}

// A message over the limit, or with fields the protocol does not have, is refused.
func TestTheHelperRefusesMalformedMessages(t *testing.T) {
	t.Parallel()

	helper := newFakeHelper(t)

	_, err := helper.client.Do(req(OpHostname, withName(strings.Repeat("a", MaxMessage))))
	if err == nil {
		t.Error("an oversize request answered")
	}

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: helper.client.Path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	err = writeMessage(conn, map[string]any{"op": OpReboot, "path": "/etc/shadow"})
	if err != nil {
		t.Fatal(err)
	}

	var resp Response

	_, err = readMessage(conn, &resp)
	if err != nil || !strings.Contains(resp.Error, "unknown field") {
		t.Errorf("unknown field: %+v %v", resp, err)
	}

	if got := helper.commands(); len(got) != 0 {
		t.Errorf("ran %q", got)
	}
}

func TestTheHelperDoesTheSmallSteps(t *testing.T) {
	t.Parallel()

	helper := newFakeHelper(t)
	helper.with(func() { helper.rtc = 0x050001B0 })

	v, err := helper.client.ReadScratch2()
	if err != nil || v != 0x050001B0 {
		t.Errorf("SCRATCH2 = %#x, %v", v, err)
	}

	err = helper.client.ClearScratch2()
	if err != nil || rtcOf(helper) != 0 {
		t.Errorf("clear: %v, SCRATCH2 %#x", err, rtcOf(helper))
	}

	err = errors.Join(helper.client.SetHostname("kitchen"), helper.client.BCOPower(false), helper.client.BCOPower(true))
	if err != nil {
		t.Fatal(err)
	}

	got := helper.snapshot()
	hosts, power := got.hosts, got.power

	if !slices.Equal(hosts, []string{"kitchen"}) || !slices.Equal(power, []bool{false, true}) {
		t.Errorf("hosts %q power %v", hosts, power)
	}

	err = helper.client.SyncClock()
	if err != nil {
		t.Fatal(err)
	}

	err = helper.client.BTCtl("play")
	if err != nil {
		t.Fatal(err)
	}

	var exit *ExitError

	err = helper.client.BTCtl("reconnect")
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("reconnect = %v, want exit 3", err)
	}

	want := []string{
		"/etc/init.d/soundtap sync-clock", "sh /opt/soundtap/bt-ctl.sh play", "sh /opt/soundtap/bt-ctl.sh reconnect",
	}
	if got := helper.commands(); !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// ConfirmBootCountVia reads the count, settles and only then clears it, through the helper.
func TestBootCountThroughTheHelper(t *testing.T) {
	t.Parallel()

	helper := newFakeHelper(t)
	helper.with(func() { helper.rtc = 0x050001B0 }) // count 5: past bootlimit
	confirm := flash.ConfirmBootCountVia(helper.client)

	failed := errSaveFailed

	err := confirm(0, func(fallback bool) error {
		if !fallback {
			t.Error("count 5 is not a fallback")
		}

		return failed
	})
	if !errors.Is(err, failed) && (err == nil || !strings.Contains(err.Error(), "save failed")) {
		t.Errorf("err = %v", err)
	}

	if rtcOf(helper) == 0 {
		t.Error("SCRATCH2 cleared before the save")
	}

	err = confirm(0, func(bool) error { return nil })
	if err != nil || rtcOf(helper) != 0 {
		t.Errorf("err %v, SCRATCH2 %#x", err, rtcOf(helper))
	}
}

// A GPIO line soundtapd drives is exported and handed over; any other line is refused.
func TestGPIOExportHoldsToTheLines(t *testing.T) {
	t.Parallel()

	helper := newFakeHelper(t)

	root := helper.srv.GPIORoot
	for _, chip := range []struct{ name, addr, base string }{
		{"gpiochip0", "44e07000.gpio", "0"}, {"gpiochip32", "4804c000.gpio", "32"},
	} {
		dev := filepath.Join(filepath.Dir(root), "devices", chip.addr, "gpiochip")

		err := os.MkdirAll(dev, 0o755)
		if err != nil {
			t.Fatal(err)
		}

		_ = os.WriteFile(filepath.Join(dev, "base"), []byte(chip.base+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dev, "ngpio"), []byte("32\n"), 0o644)

		_ = os.MkdirAll(root, 0o755)

		err = os.Symlink(dev, filepath.Join(root, chip.name))
		if err != nil {
			t.Fatal(err)
		}
	}
	// gpio1_24 (the ST20 amplifier fault), already exported.
	line := filepath.Join(root, "gpio56")

	_ = os.MkdirAll(line, 0o755)
	for _, a := range []string{"value", "direction", "active_low"} {
		_ = os.WriteFile(filepath.Join(line, a), nil, 0o644)
	}

	err := helper.client.ExportGPIO(56)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"/gpio/gpio56/value", "/gpio/gpio56/direction", "/gpio/gpio56/active_low"}
	if chowns := helper.snapshot().chowns; !slices.Equal(chowns, want) {
		t.Errorf("chowned %q, want %q", chowns, want)
	}

	for _, n := range []int{14 /* gpio0_14, the BCO's supply */, 33, 64 /* no chip */} {
		err := helper.client.ExportGPIO(n)
		if err == nil {
			t.Errorf("gpio%d exported", n)
		}
	}
}

// The factory reset of state other users own removes it and gives it back empty: Spotify's
// to librespot's user, BlueZ's to root.
func TestResetForeignState(t *testing.T) {
	t.Parallel()

	helper := newFakeHelper(t)

	nvDir := helper.srv.NV
	for _, p := range []string{
		"spotify/system/credentials.json", "bluetooth/00:11/info", "bluealsa/x", "soundtap/presets.json",
	} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(nvDir, p)), 0o755)
		_ = os.WriteFile(filepath.Join(nvDir, p), []byte("x"), 0o600)
	}

	err := helper.client.ResetForeignState()
	if err != nil {
		t.Fatal(err)
	}

	for _, gone := range []string{"spotify/system/credentials.json", "bluetooth/00:11", "bluealsa/x"} {
		_, err := os.Lstat(filepath.Join(nvDir, gone))
		if err == nil {
			t.Errorf("%s left", gone)
		}
	}

	for _, kept := range []string{"spotify/cache", "spotify/system", "bluetooth", "bluealsa", "soundtap/presets.json"} {
		_, err := os.Stat(filepath.Join(nvDir, kept))
		if err != nil {
			t.Errorf("%s: %v", kept, err)
		}
	}

	if chowns := helper.snapshot().chowns; len(chowns) != 3 {
		t.Errorf("chowned %q", chowns)
	}
}

// Every fw_setenv script internal/flash writes, through every way it changes the
// environment, passes CheckEnvScript: the allow-list is flash.EnvKeys, and this keeps the
// two together.
func TestEnvKeysCoverEveryScriptFlashWrites(t *testing.T) {
	t.Parallel()

	mtd := map[string]flash.Partition{}
	maps.Copy(mtd, table())

	target, err := flash.Plan(pingBoot+" "+flash.EnvRedundantMarker, mtd)
	if err != nil {
		t.Fatal(err)
	}

	env := ubootenv.NewEnv()
	for _, pair := range [][2]string{
		{"bootcmd", "run nand_boot;"},
		{"nand_boot", "x"},
		{"nand_boot2", "y"},
		{"nand_args", "setenv bootargs ${bootargs} ubi.mtd=8,2048"},
		{"nand_args2", "setenv bootargs ${bootargs} ubi.mtd=9,2048"},
		// An owner key from a build before 0.1.0, which the selection removes.
		{"soundtap_owner_key", "AAAA"},
	} {
		env.Set(pair[0], pair[1])
	}

	nand := &ubootenvtest.NAND{Devices: map[string][]byte{}, Cmds: nil}

	err = ubootenvtest.Image(nand.Devices, "/dev/mtd5", "/dev/mtd4", env)
	if err != nil {
		t.Fatal(err)
	}

	scripts := 0
	run := func(stdin io.Reader, name string, args ...string) error {
		if name == "fw_setenv" {
			scripts++

			data, err := os.ReadFile(args[3])
			if err != nil {
				return fmt.Errorf("the script: %w", err)
			}

			err = CheckEnvScript(data)
			if err != nil {
				t.Errorf("%v in\n%s", err, data)
			}
		}

		return nand.Run(stdin, name, args...)
	}
	installer := &flash.Installer{Target: target, Run: run, Open: nil}
	markers := flash.SoundTapMarkers(target.PersistentName)

	for name, step := range map[string]func() error{
		"recipes": func() error { return installer.InstallMainlineRecipes(nand.Read, "ginger") },
		"select":  func() error { return installer.SelectBootSlot(nand.Read, target.Alternate, markers) },
		"hold":    func() error { return installer.HoldRunningSlot(nand.Read) },
		"settle":  func() error { return installer.SettleRunningSlot(nand.Read, markers) },
		"confirm": func() error { return installer.ConfirmPendingBoot(nand.Read) },
		"stock":   func() error { return installer.InstallStockRecipes(nand.Read) },
	} {
		err := step()
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	if scripts < 4 {
		t.Errorf("only %d scripts written; the steps above should write more", scripts)
	}
}

// The socket is root's and the group's alone: soundtapd's user reaches it through its group,
// no other user at all.
func TestListenMakesTheSocketTheGroups(t *testing.T) {
	t.Parallel()

	// A short directory, as in newFakeHelper.
	dir, err := os.MkdirTemp("", "rd") //nolint:usetesting // a socket path must stay short.
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "sock")

	err = os.WriteFile(path, nil, 0o600) // a stale one from a crash
	if err != nil {
		t.Fatal(err)
	}

	line, err := Listen(path, os.Getgid())
	if err != nil {
		if os.Geteuid() != 0 {
			t.Skipf("chown to root needs root: %v", err)
		}

		t.Fatal(err)
	}
	defer line.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o660 {
		t.Errorf("socket mode %v, want a socket 0660", info.Mode())
	}

	if st, ok := info.Sys().(*syscall.Stat_t); ok && (st.Uid != 0 || int(st.Gid) != os.Getgid()) {
		t.Errorf("socket owner %d:%d, want 0:%d", st.Uid, st.Gid, os.Getgid())
	}
}

// A command that says a lot (ubiformat's progress) still gets its answer through: the
// answer carries the end of the output.
func TestALongOutputStillAnswers(t *testing.T) {
	t.Parallel()

	helper := newFakeHelper(t)

	resp, err := helper.client.Do(req(OpUBIDetach))
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.Out) > maxOut+3 || !strings.HasSuffix(resp.Out, "complete\n") {
		t.Errorf("answer of %d bytes ending %q", len(resp.Out), resp.Out[max(0, len(resp.Out)-20):])
	}
}

// errSaveFailed is the failure a test's settle step reports.
var errSaveFailed = errors.New("save failed")

// A command killed at its timeout whose child still holds the output pipe ends the request
// within waitDelay, instead of waiting for that child (and, for a NAND one, holding the
// NAND lock meanwhile).
func TestRunCommandDoesNotWaitForAChildHoldingThePipe(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err := runCommand(ctx, program("/bin/sh", "-c", "/bin/sleep 30 & wait"))
	if err == nil {
		t.Fatal("a killed command reported success")
	}

	// Without the bound it waits the child's full 30 s; a busy host may be slow, never that slow.
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("runCommand returned after %v, want about %v", took, waitDelay)
	}
}
