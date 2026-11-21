// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package rootd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

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
	// A short directory: a socket path is limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "rd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeHelper{peer: 1000}
	s := &Server{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), UID: 1000, GID: 1000,
		Layout:   func() (Layout, error) { return NewLayout(pingBoot, table()), nil },
		DevDir:   filepath.Join(dir, "dev"),
		GPIORoot: filepath.Join(dir, "gpio"), MountDir: filepath.Join(dir, "mnt"), StateDir: dir,
		NV: filepath.Join(dir, "nv"), InitScript: "/etc/init.d/soundtap", BTCtl: "/opt/soundtap/bt-ctl.sh",
		PeerUID:     func(*net.UnixConn) (int, error) { var p int; f.with(func() { p = f.peer }); return p, nil },
		SetHostname: func(n string) error { f.with(func() { f.hosts = append(f.hosts, n) }); return nil },
		BCOPower:    func(on bool) error { f.with(func() { f.power = append(f.power, on) }); return nil },
		RTC:         fakeRTC{f},
		Chown: func(p string, _, _ int) error {
			f.with(func() { f.chowns = append(f.chowns, strings.TrimPrefix(p, dir)) })
			return nil
		},
		Lookup: func(string) (int, int, error) { return 1001, 29, nil },
	}
	s.Run = func(_ context.Context, c Command) (string, error) {
		r := ran{cmd: c.String(), lock: len(c.Extra) > 0}
		if c.Stdin != nil {
			r.input, _ = io.ReadAll(c.Stdin)
		}
		if c.Name == "fw_setenv" {
			r.input, _ = os.ReadFile(c.Args[3])
			r.cmd = "fw_setenv " + mustRead(t, c.Args[1])
		}
		f.mu.Lock()
		f.ran = append(f.ran, r)
		f.mu.Unlock()
		if c.Name == "ubidetach" {
			return strings.Repeat("ubiformat: formatting eraseblock 1 -- 1 % complete\n", 4000), nil // 200 KB
		}
		if c.Name == "sh" && c.Args[1] == "reconnect" {
			return "", &ExitError{Code: 3}
		}
		return "out", nil
	}
	f.srv = s
	sock := filepath.Join(dir, "sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = s.Serve(ctx, l) }()
	f.client = NewClient(sock)
	f.client.LocalRun = func(name string, args ...string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.ran = append(f.ran, ran{cmd: "local " + name + " " + strings.Join(args, " ")})
		return nil
	}
	return f
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

func rtcOf(f *fakeHelper) uint32 { rtc, _, _, _ := f.snapshot(); return rtc }

// snapshot copies what the fake recorded.
func (f *fakeHelper) snapshot() (rtc uint32, hosts, chowns []string, power []bool) {
	f.with(func() {
		rtc, hosts, chowns, power = f.rtc, slices.Clone(f.hosts), slices.Clone(f.chowns), slices.Clone(f.power)
	})
	return
}

type fakeRTC struct{ f *fakeHelper }

func (r fakeRTC) ReadScratch2() (uint32, error) {
	var v uint32
	r.f.with(func() { v = r.f.rtc })
	return v, nil
}
func (r fakeRTC) ClearScratch2() error { r.f.with(func() { r.f.rtc = 0 }); return nil }

func (f *fakeHelper) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.ran {
		out = append(out, r.cmd)
	}
	return out
}

func staged(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "staged")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The commands internal/flash and internal/install run reach the helper as typed requests
// and come out as the same commands, with the file contents passed along, not the paths.
func TestFlashCommandsGoThroughTheHelper(t *testing.T) {
	f := newFakeHelper(t)
	kernel := bytes.Repeat([]byte{0xab}, 4096)
	lock, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	run := func(stdin io.Reader, name string, args ...string) {
		t.Helper()
		if err := f.client.Run(lock, stdin, name, args...); err != nil {
			t.Fatalf("%s %v: %v", name, args, err)
		}
	}
	dev := f.srv.DevDir
	run(nil, "flash_erase", "/dev/mtd7", "0", "0")
	run(nil, "nandwrite", "-p", "/dev/mtd7", staged(t, kernel))
	run(nil, "nandwrite", "-p", "-s", "1835008", "/dev/mtd4", staged(t, kernel))
	run(nil, "dd", "if="+staged(t, make([]byte, 0x40000)), "of=/dev/mtd4", "bs=131072", "count=1", "skip=1", "seek=9", "conv=notrunc")
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
		"mount -t ubifs -o ro ubi7:rootfs " + f.srv.MountDir,
		"local test -x " + MountDir + "/init",
		"umount " + f.srv.MountDir,
		"ubidetach -d 7",
		"fw_setenv " + dev + "/mtd5 0x0 0x8000 0x20000 1\n" + dev + "/mtd4 0x1c0000 0x8000 0x20000 1",
	}
	if got := f.commands(); !slices.Equal(got, want) {
		t.Fatalf("ran\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var runs []ran
	f.with(func() { runs = slices.Clone(f.ran) })
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
	f := newFakeHelper(t)
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
	for _, tc := range []struct {
		r     Request
		files []*os.File
	}{
		{Request{Op: OpNANDErase, MTD: 8}, nil},                                       // the running rootfs
		{Request{Op: OpNANDErase, MTD: 6}, nil},                                       // the running kernel
		{Request{Op: OpNANDErase, MTD: 10}, nil},                                      // /mnt/nv
		{Request{Op: OpNANDErase, MTD: 12}, nil},                                      // manufacturing data
		{Request{Op: OpNANDWrite, MTD: 6, Input: true}, []*os.File{file}},             // the running kernel
		{Request{Op: OpNANDWrite, MTD: 7}, nil},                                       // no input
		{Request{Op: OpUBIFormat, MTD: 8, Size: 4096, Input: true}, []*os.File{file}}, // the running rootfs
		{Request{Op: OpUBIAttach, MTD: 10}, nil},
		{Request{Op: OpEnvSet, Copies: []EnvCopy{{MTD: 5, Size: ubootenv.Size, EraseSize: 0x20000}}, Input: true}, []*os.File{script}},
		{Request{Op: OpEnvSet, Copies: []EnvCopy{{MTD: 8, Size: ubootenv.Size, EraseSize: 0x20000}}, Input: true}, []*os.File{file}},
		{Request{Op: OpHostname, Name: "a;reboot"}, nil},
		{Request{Op: OpBTCtl, Name: "../../bin/sh"}, nil},
		{Request{Op: "shell"}, nil},
		{Request{Op: OpReboot, Input: true}, []*os.File{file}}, // a descriptor for nothing
		{Request{Op: OpNANDErase, MTD: 7, Input: true}, nil},   // fewer descriptors than named
	} {
		if _, err := f.client.Do(tc.r, tc.files...); err == nil {
			t.Errorf("%+v answered", tc.r)
		}
	}
	if got := f.commands(); len(got) != 0 {
		t.Errorf("ran %q", got)
	}
}

func TestTheHelperAnswersOnlySoundtapdAndRoot(t *testing.T) {
	f := newFakeHelper(t)
	for _, uid := range []int{1000, 0} {
		f.with(func() { f.peer = uid })
		if err := f.client.Reboot(); err != nil {
			t.Errorf("uid %d refused: %v", uid, err)
		}
	}
	f.with(func() { f.peer = 1001 }) // spotify
	if err := f.client.Reboot(); err == nil {
		t.Error("another user's request answered")
	}
	if got := f.commands(); len(got) != 2 {
		t.Errorf("ran %q", got)
	}
}

// A message over the limit, or with fields the protocol does not have, is refused.
func TestTheHelperRefusesMalformedMessages(t *testing.T) {
	f := newFakeHelper(t)
	if _, err := f.client.Do(Request{Op: OpHostname, Name: strings.Repeat("a", MaxMessage)}); err == nil {
		t.Error("an oversize request answered")
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: f.client.Path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := writeMessage(conn, map[string]any{"op": OpReboot, "path": "/etc/shadow"}); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if _, err := readMessage(conn, &resp); err != nil || !strings.Contains(resp.Error, "unknown field") {
		t.Errorf("unknown field: %+v %v", resp, err)
	}
	if got := f.commands(); len(got) != 0 {
		t.Errorf("ran %q", got)
	}
}

func TestTheHelperDoesTheSmallSteps(t *testing.T) {
	f := newFakeHelper(t)
	f.with(func() { f.rtc = 0x050001B0 })
	if v, err := f.client.ReadScratch2(); err != nil || v != 0x050001B0 {
		t.Errorf("SCRATCH2 = %#x, %v", v, err)
	}
	if err := f.client.ClearScratch2(); err != nil || rtcOf(f) != 0 {
		t.Errorf("clear: %v, SCRATCH2 %#x", err, rtcOf(f))
	}
	if err := errors.Join(f.client.SetHostname("kitchen"), f.client.BCOPower(false), f.client.BCOPower(true)); err != nil {
		t.Fatal(err)
	}
	if _, hosts, _, power := f.snapshot(); !slices.Equal(hosts, []string{"kitchen"}) || !slices.Equal(power, []bool{false, true}) {
		t.Errorf("hosts %q power %v", hosts, power)
	}
	if err := f.client.SyncClock(); err != nil {
		t.Fatal(err)
	}
	if err := f.client.BTCtl("play"); err != nil {
		t.Fatal(err)
	}
	var exit *ExitError
	if err := f.client.BTCtl("reconnect"); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("reconnect = %v, want exit 3", err)
	}
	want := []string{"/etc/init.d/soundtap sync-clock", "sh /opt/soundtap/bt-ctl.sh play", "sh /opt/soundtap/bt-ctl.sh reconnect"}
	if got := f.commands(); !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// ConfirmBootCountVia reads the count, settles and only then clears it, through the helper.
func TestBootCountThroughTheHelper(t *testing.T) {
	f := newFakeHelper(t)
	f.with(func() { f.rtc = 0x050001B0 }) // count 5: past bootlimit
	confirm := flash.ConfirmBootCountVia(f.client)
	failed := errors.New("save failed")
	if err := confirm(0, func(fallback bool) error {
		if !fallback {
			t.Error("count 5 is not a fallback")
		}
		return failed
	}); !errors.Is(err, failed) && (err == nil || !strings.Contains(err.Error(), "save failed")) {
		t.Errorf("err = %v", err)
	}
	if rtcOf(f) == 0 {
		t.Error("SCRATCH2 cleared before the save")
	}
	if err := confirm(0, func(bool) error { return nil }); err != nil || rtcOf(f) != 0 {
		t.Errorf("err %v, SCRATCH2 %#x", err, rtcOf(f))
	}
}

// A GPIO line soundtapd drives is exported and handed over; any other line is refused.
func TestGPIOExportHoldsToTheLines(t *testing.T) {
	f := newFakeHelper(t)
	root := f.srv.GPIORoot
	for _, chip := range []struct{ name, addr, base string }{
		{"gpiochip0", "44e07000.gpio", "0"}, {"gpiochip32", "4804c000.gpio", "32"},
	} {
		dev := filepath.Join(filepath.Dir(root), "devices", chip.addr, "gpiochip")
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dev, "base"), []byte(chip.base+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dev, "ngpio"), []byte("32\n"), 0o644)
		_ = os.MkdirAll(root, 0o755)
		if err := os.Symlink(dev, filepath.Join(root, chip.name)); err != nil {
			t.Fatal(err)
		}
	}
	// gpio1_24 (the ST20 amplifier fault), already exported.
	line := filepath.Join(root, "gpio56")
	_ = os.MkdirAll(line, 0o755)
	for _, a := range []string{"value", "direction", "active_low"} {
		_ = os.WriteFile(filepath.Join(line, a), nil, 0o644)
	}
	if err := f.client.ExportGPIO(56); err != nil {
		t.Fatal(err)
	}
	want := []string{"/gpio/gpio56/value", "/gpio/gpio56/direction", "/gpio/gpio56/active_low"}
	if _, _, chowns, _ := f.snapshot(); !slices.Equal(chowns, want) {
		t.Errorf("chowned %q, want %q", chowns, want)
	}
	for _, n := range []int{14 /* gpio0_14, the BCO's supply */, 33, 64 /* no chip */} {
		if err := f.client.ExportGPIO(n); err == nil {
			t.Errorf("gpio%d exported", n)
		}
	}
}

// The factory reset of state other users own removes it and gives it back empty: Spotify's
// to librespot's user, BlueZ's to root.
func TestResetForeignState(t *testing.T) {
	f := newFakeHelper(t)
	nv := f.srv.NV
	for _, p := range []string{"spotify/system/credentials.json", "bluetooth/00:11/info", "bluealsa/x", "soundtap/presets.json"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(nv, p)), 0o755)
		_ = os.WriteFile(filepath.Join(nv, p), []byte("x"), 0o600)
	}
	if err := f.client.ResetForeignState(); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"spotify/system/credentials.json", "bluetooth/00:11", "bluealsa/x"} {
		if _, err := os.Lstat(filepath.Join(nv, gone)); err == nil {
			t.Errorf("%s left", gone)
		}
	}
	for _, kept := range []string{"spotify/cache", "spotify/system", "bluetooth", "bluealsa", "soundtap/presets.json"} {
		if _, err := os.Stat(filepath.Join(nv, kept)); err != nil {
			t.Errorf("%s: %v", kept, err)
		}
	}
	if _, _, chowns, _ := f.snapshot(); len(chowns) != 3 {
		t.Errorf("chowned %q", chowns)
	}
}

// Every fw_setenv script internal/flash writes, through every way it changes the
// environment, passes CheckEnvScript: the allow-list is flash.EnvKeys, and this keeps the
// two together.
func TestEnvKeysCoverEveryScriptFlashWrites(t *testing.T) {
	mtd := map[string]flash.Partition{}
	for k, v := range table() {
		mtd[k] = v
	}
	target, err := flash.Plan(pingBoot+" "+flash.EnvRedundantMarker, mtd)
	if err != nil {
		t.Fatal(err)
	}
	env := ubootenv.NewEnv()
	for _, kv := range [][2]string{
		{"bootcmd", "run nand_boot;"}, {"nand_boot", "x"}, {"nand_boot2", "y"},
		{"nand_args", "setenv bootargs ${bootargs} ubi.mtd=8,2048"}, {"nand_args2", "setenv bootargs ${bootargs} ubi.mtd=9,2048"},
	} {
		env.Set(kv[0], kv[1])
	}
	nand := &ubootenvtest.NAND{Devices: map[string][]byte{}}
	if err := ubootenvtest.Image(nand.Devices, "/dev/mtd5", "/dev/mtd4", env); err != nil {
		t.Fatal(err)
	}
	scripts := 0
	run := func(stdin io.Reader, name string, args ...string) error {
		if name == "fw_setenv" {
			scripts++
			b, err := os.ReadFile(args[3])
			if err != nil {
				return err
			}
			if err := CheckEnvScript(b); err != nil {
				t.Errorf("%v in\n%s", err, b)
			}
		}
		return nand.Run(stdin, name, args...)
	}
	in := &flash.Installer{Target: target, Run: run}
	markers := flash.SoundTapMarkers(target.PersistentName)
	for name, step := range map[string]func() error{
		"recipes":   func() error { return in.InstallMainlineRecipes(nand.Read, "ginger") },
		"select":    func() error { return in.SelectBootSlot(nand.Read, target.Alternate, markers) },
		"hold":      func() error { return in.HoldRunningSlot(nand.Read) },
		"settle":    func() error { return in.SettleRunningSlot(nand.Read, markers) },
		"confirm":   func() error { return in.ConfirmPendingBoot(nand.Read) },
		"owner key": func() error { return in.SetEnvEverywhere(nand.Read, "soundtap_owner_key", "AAAA") },
		"stock":     func() error { return in.InstallStockRecipes(nand.Read) },
	} {
		if err := step(); err != nil {
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
	dir, err := os.MkdirTemp("", "rd")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "sock")
	if err := os.WriteFile(path, nil, 0o600); err != nil { // a stale one from a crash
		t.Fatal(err)
	}
	l, err := Listen(path, os.Getgid())
	if err != nil {
		if os.Geteuid() != 0 {
			t.Skipf("chown to root needs root: %v", err)
		}
		t.Fatal(err)
	}
	defer l.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o660 {
		t.Errorf("socket mode %v, want a socket 0660", fi.Mode())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && (st.Uid != 0 || int(st.Gid) != os.Getgid()) {
		t.Errorf("socket owner %d:%d, want 0:%d", st.Uid, st.Gid, os.Getgid())
	}
}

// A command that says a lot (ubiformat's progress) still gets its answer through: the
// answer carries the end of the output.
func TestALongOutputStillAnswers(t *testing.T) {
	f := newFakeHelper(t)
	resp, err := f.client.Do(Request{Op: OpUBIDetach})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Out) > maxOut+3 || !strings.HasSuffix(resp.Out, "complete\n") {
		t.Errorf("answer of %d bytes ending %q", len(resp.Out), resp.Out[max(0, len(resp.Out)-20):])
	}
}
