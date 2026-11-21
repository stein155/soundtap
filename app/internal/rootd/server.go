// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package rootd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/safego"
)

// Command is one program the helper runs: its own binaries at fixed names, arguments it
// built itself, and the descriptors that came with the request.
type Command struct {
	Name  string
	Args  []string
	Stdin *os.File
	Extra []*os.File
	// Detach starts the program in its own session and returns without waiting.
	Detach bool
}

func (c Command) String() string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// Server answers soundtapd's requests. NewServer fills in the speaker's defaults; a test
// replaces what reaches hardware.
type Server struct {
	Log *slog.Logger
	// UID and GID are soundtapd's: the user that may ask, besides root, and the owner
	// given what the helper hands over.
	UID, GID int

	Run         func(ctx context.Context, c Command) (string, error)
	Layout      func() (Layout, error)
	RTC         flash.RTC
	BCOPower    func(on bool) error
	SetHostname func(name string) error
	PeerUID     func(c *net.UnixConn) (int, error)
	Chown       func(path string, uid, gid int) error
	Lookup      func(user string) (uid, gid int, err error)

	DevDir, GPIORoot, MountDir, StateDir, NV string
	InitScript, BTCtl                        string

	nand sync.Mutex
}

// NewServer is the helper as the speaker runs it, for soundtapd's uid and gid.
func NewServer(log *slog.Logger, uid, gid int) *Server {
	return &Server{
		Log: log, UID: uid, GID: gid,
		Run:         runCommand,
		Layout:      liveLayout,
		RTC:         flash.DevRTC{},
		BCOPower:    bcoPower,
		SetHostname: sethostname,
		PeerUID:     peerUID,
		Chown:       os.Lchown,
		Lookup:      lookupUser,
		DevDir:      "/dev", GPIORoot: "/sys/class/gpio", MountDir: MountDir, StateDir: Dir, NV: "/mnt/nv",
		InitScript: "/etc/init.d/soundtap", BTCtl: "/opt/soundtap/bt-ctl.sh",
	}
}

func liveLayout() (Layout, error) {
	cmdline, err := flash.ReadCmdline()
	if err != nil {
		return Layout{}, err
	}
	mtd, err := flash.ReadMTD()
	if err != nil {
		return Layout{}, err
	}
	return NewLayout(cmdline, mtd), nil
}

func bcoPower(on bool) error {
	power, unmap, err := bco.MemPower()
	if err != nil {
		return err
	}
	defer unmap()
	return power(on)
}

// runCommand runs c to its end and returns its output, or starts it detached.
func runCommand(ctx context.Context, c Command) (string, error) {
	if c.Detach {
		// Not ctx: a detached program outlives the request.
		cmd := exec.CommandContext(context.WithoutCancel(ctx), c.Name, c.Args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return "", err
		}
		safego.Go("rootd.reap", func() { _ = cmd.Wait() })
		return "", nil
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	if c.Stdin != nil {
		cmd.Stdin = c.Stdin
	}
	cmd.ExtraFiles = c.Extra
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(out), &ExitError{Code: exit.ExitCode()}
		}
		return string(out), err
	}
	return string(out), nil
}

// Listen opens the socket at path: only root and the group gid may connect.
func Listen(path string, gid int) (*net.UnixListener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := errors.Join(os.Chown(path, 0, gid), os.Chmod(path, 0o660)); err != nil { //nolint:gosec // the group is the point: soundtapd connects through it
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// Serve answers connections on l until ctx ends.
func (s *Server) Serve(ctx context.Context, l *net.UnixListener) error {
	safego.Go("rootd.close", func() {
		<-ctx.Done()
		_ = l.Close()
	})
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // the listener was closed because ctx ended: a clean stop
			}
			return err
		}
		safego.Go("rootd.handle", func() { s.handle(ctx, c) })
	}
}

// handle answers one request on c.
func (s *Server) handle(ctx context.Context, c *net.UnixConn) {
	defer c.Close()
	uid, err := s.PeerUID(c)
	if err != nil || (uid != s.UID && uid != 0) {
		s.Log.Warn("refused a peer", "uid", uid, "err", err)
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var r Request
	files, err := readMessage(c, &r)
	defer closeAll(files)
	var resp Response
	if err == nil {
		resp, err = s.Do(ctx, r, files)
	}
	if err != nil {
		resp.Error = err.Error()
		s.Log.Warn("refused", "op", r.Op, "req", describe(r), "err", err)
	} else {
		s.Log.Info("done", "op", r.Op, "req", describe(r), "exit", resp.Exit)
	}
	// ubiformat's progress alone can pass the message limit: the end says what happened.
	resp.Out, resp.Error = tail(resp.Out), tail(resp.Error)
	_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := writeMessage(c, resp); err != nil {
		s.Log.Warn("answer lost", "op", r.Op, "err", err)
	}
}

// maxOut is how much of a command's output an answer carries.
const maxOut = 8 << 10

// tail is the last maxOut bytes of s.
func tail(s string) string {
	if len(s) <= maxOut {
		return s
	}
	return "..." + s[len(s)-maxOut:]
}

// describe is a request for the log, without the arguments every request has.
func describe(r Request) string {
	var b strings.Builder
	for _, kv := range []struct {
		k   string
		v   any
		set bool
	}{
		{"mtd", r.MTD, r.MTD != 0}, {"offset", fmt.Sprintf("%#x", r.Offset), r.Offset != 0},
		{"blocks", r.Blocks, r.Blocks != 0}, {"skip", r.Skip, r.Skip != 0}, {"pad", r.Pad, r.Pad},
		{"size", r.Size, r.Size != 0}, {"copies", r.Copies, len(r.Copies) > 0}, {"on", r.On, r.Op == OpBCOPower},
		{"name", r.Name, r.Name != ""}, {"gpio", r.GPIO, r.GPIO != 0},
	} {
		if kv.set {
			fmt.Fprintf(&b, "%s=%v ", kv.k, kv.v)
		}
	}
	return strings.TrimSpace(b.String())
}

// nandOps are the operations that write NAND or the environment, one at a time.
var nandOps = map[string]bool{
	OpNANDErase: true, OpNANDWrite: true, OpNANDBlock: true, OpUBIFormat: true, OpUBIAttach: true,
	OpUBIDetach: true, OpSlotMount: true, OpSlotUmount: true, OpEnvSet: true,
}

// inputOps are the operations that read the request's input descriptor.
var inputOps = map[string]bool{OpNANDWrite: true, OpNANDBlock: true, OpUBIFormat: true, OpEnvSet: true}

// Do validates r and carries it out. files are the descriptors that came with it, which
// Do neither keeps nor closes.
func (s *Server) Do(ctx context.Context, r Request, files []*os.File) (Response, error) {
	input, extra, err := descriptors(r, files)
	if err != nil {
		return Response{}, err
	}
	if nandOps[r.Op] {
		s.nand.Lock()
		defer s.nand.Unlock()
		ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		defer cancel()
		return s.doNAND(ctx, r, input, extra)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return s.doOther(ctx, r)
}

// descriptors sorts files into the input and the lock the request names, refusing a count
// that differs or an input for an operation that reads none.
func descriptors(r Request, files []*os.File) (input *os.File, extra []*os.File, err error) {
	want := 0
	if r.Input {
		want++
	}
	if r.Lock {
		want++
	}
	if len(files) != want {
		return nil, nil, fmt.Errorf("%d descriptors for a request that names %d", len(files), want)
	}
	if r.Input {
		input = files[0]
	}
	if r.Lock {
		extra = []*os.File{files[len(files)-1]}
	}
	if inputOps[r.Op] != (input != nil) {
		return nil, nil, fmt.Errorf("%s: input descriptor %v, want %v", r.Op, input != nil, inputOps[r.Op])
	}
	return input, extra, nil
}

// doOther carries out every request that is not a NAND one.
func (s *Server) doOther(ctx context.Context, r Request) (Response, error) {
	switch r.Op {
	case OpReboot:
		out, err := s.Run(ctx, Command{Name: "reboot"})
		return Response{Out: out}, err
	case OpBootCountRead:
		v, err := s.RTC.ReadScratch2()
		return Response{Value: v}, err
	case OpBootCountClear:
		return Response{}, s.RTC.ClearScratch2()
	case OpBCOPower:
		return Response{}, s.BCOPower(r.On)
	case OpHostname:
		if err := CheckHostname(r.Name); err != nil {
			return Response{}, err
		}
		return Response{}, s.SetHostname(r.Name)
	case OpGPIOExport:
		return Response{}, s.exportGPIO(r.GPIO)
	case OpClockSync:
		_, err := s.Run(ctx, Command{Name: s.InitScript, Args: []string{"sync-clock"}, Detach: true})
		return Response{}, err
	case OpBTCtl:
		return s.btCtl(ctx, r.Name)
	case OpNVReset:
		return Response{}, s.resetForeignState()
	}
	return Response{}, fmt.Errorf("unknown operation %q", r.Op)
}

// btCtl runs bt-ctl.sh verb; its exit status is the answer, not an error.
func (s *Server) btCtl(ctx context.Context, verb string) (Response, error) {
	if !slices.Contains(BTVerbs, verb) {
		return Response{}, fmt.Errorf("bt-ctl.sh has no command %q", verb)
	}
	// A transport key must not hang on a wedged bluetoothd; calling the phones takes up to
	// five bounded tries.
	limit := 5 * time.Second
	if verb == "reconnect" || verb == "trim" || verb == "disconnect" {
		limit = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := s.Run(ctx, Command{Name: "sh", Args: []string{s.BTCtl, verb}})
	var exit *ExitError
	if errors.As(err, &exit) {
		return Response{Out: out, Exit: exit.Code}, nil
	}
	return Response{Out: out}, err
}

// doNAND runs one NAND, UBI or environment operation after holding it to the layout.
func (s *Server) doNAND(ctx context.Context, r Request, input *os.File, extra []*os.File) (Response, error) {
	layout, err := s.Layout()
	if err != nil {
		return Response{}, fmt.Errorf("reading the partition table: %w", err)
	}
	if r.Op == OpEnvSet {
		return s.setEnv(ctx, layout, r, input, extra)
	}
	c, err := s.nandCommand(layout, r, input)
	if err != nil {
		return Response{}, err
	}
	c.Extra = extra
	out, err := s.Run(ctx, c)
	if err != nil {
		return Response{Out: out}, fmt.Errorf("%s: %w: %s", c.Name, err, strings.TrimSpace(out))
	}
	if r.Op == OpUBIAttach {
		s.grantVolumes()
	}
	return Response{Out: out}, nil
}

// nandCommand is the command one NAND or UBI request runs, once layout allows it.
func (s *Server) nandCommand(layout Layout, r Request, input *os.File) (Command, error) {
	dev := filepath.Join(s.DevDir, "mtd"+strconv.Itoa(r.MTD))
	itoa := func(n int64) string { return strconv.FormatInt(n, 10) }
	switch r.Op {
	case OpNANDErase:
		return Command{Name: "flash_erase", Args: []string{dev, itoa(r.Offset), itoa(r.Blocks)}}, layout.checkErase(r)
	case OpNANDWrite:
		size, err := regularSize(input)
		if err == nil {
			err = layout.checkWrite(r, size)
		}
		var args []string
		if r.Pad {
			args = append(args, "-p")
		}
		if r.Offset > 0 {
			args = append(args, "-s", itoa(r.Offset))
		}
		// The passed file, opened again through its descriptor: nandwrite reads a regular
		// file of known size, as it did from soundtapd's own path.
		return Command{Name: "nandwrite", Args: append(args, dev, "/proc/self/fd/0"), Stdin: input}, err
	case OpNANDBlock:
		size, err := regularSize(input)
		if err == nil {
			err = layout.checkBlock(r, size)
		}
		bs := layout.blockSize(r.MTD)
		if bs <= 0 {
			bs = 1
		}
		return Command{Name: "dd", Args: []string{"if=/proc/self/fd/0", "of=" + dev, "bs=" + itoa(bs),
			"count=1", "skip=" + itoa(r.Skip), "seek=" + itoa(r.Offset/bs), "conv=notrunc"}, Stdin: input}, err
	case OpUBIFormat:
		return Command{Name: "ubiformat", Args: []string{dev, "-f", "-", "-S", itoa(r.Size), "-s", "2048", "-y"}, Stdin: input},
			layout.checkFormat(r)
	case OpUBIAttach:
		return Command{Name: cmdUBIAttach, Args: []string{"-m", strconv.Itoa(r.MTD), "-d", verifyUBI, "-O", "2048"}}, layout.checkAttach(r)
	case OpUBIDetach:
		return Command{Name: "ubidetach", Args: []string{"-d", verifyUBI}}, nil
	case OpSlotMount:
		return Command{Name: "mount", Args: []string{"-t", "ubifs", "-o", "ro", "ubi" + verifyUBI + ":rootfs", s.MountDir}},
			os.MkdirAll(s.MountDir, 0o750)
	case OpSlotUmount:
		return Command{Name: "umount", Args: []string{s.MountDir}}, nil
	}
	return Command{}, fmt.Errorf("unknown operation %q", r.Op)
}

// verifyUBI is the UBI device the written slot is attached as (flash's verifyUBI).
const verifyUBI = "7"

// cmdUBIAttach is mtd-utils' attach, as internal/flash runs it and the helper does.
const cmdUBIAttach = "ubiattach"

// regularSize is the size of a passed regular file; anything else is refused.
func regularSize(f *os.File) (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if !fi.Mode().IsRegular() {
		return 0, errors.New("the input is not a regular file")
	}
	return fi.Size(), nil
}

// grantVolumes lets soundtapd's group read the attached slot's UBI volumes back, the way
// VerifyRootfs streams them; it never writes them.
func (s *Server) grantVolumes() {
	nodes, _ := filepath.Glob(filepath.Join(s.DevDir, "ubi"+verifyUBI+"_*"))
	for _, n := range nodes {
		if err := errors.Join(s.Chown(n, 0, s.GID), os.Chmod(n, 0o640)); err != nil { //nolint:gosec // read-only for soundtapd's readback
			s.Log.Warn("volume not readable for soundtapd", "node", n, "err", err)
		}
	}
}

// setEnv runs fw_setenv over a configuration the helper writes itself from the checked
// copies, with the checked script.
func (s *Server) setEnv(ctx context.Context, layout Layout, r Request, input *os.File, extra []*os.File) (Response, error) {
	if err := layout.checkEnvCopies(r.Copies); err != nil {
		return Response{}, err
	}
	script, err := io.ReadAll(io.LimitReader(input, MaxMessage+1))
	if err != nil {
		return Response{}, err
	}
	if len(script) > MaxMessage {
		return Response{}, fmt.Errorf("environment script over %d bytes", MaxMessage)
	}
	if err := CheckEnvScript(script); err != nil {
		return Response{}, err
	}
	var cfg strings.Builder
	for _, c := range r.Copies {
		fmt.Fprintf(&cfg, "%s %#x %#x %#x 1\n", filepath.Join(s.DevDir, "mtd"+strconv.Itoa(c.MTD)), c.Offset, c.Size, c.EraseSize)
	}
	cfgPath, err := writeTemp(s.StateDir, "fw_env.config-", []byte(cfg.String()))
	if err != nil {
		return Response{}, err
	}
	defer os.Remove(cfgPath)
	scriptPath, err := writeTemp(s.StateDir, "env-script-", script)
	if err != nil {
		return Response{}, err
	}
	defer os.Remove(scriptPath)
	c := Command{Name: "fw_setenv", Args: []string{"-c", cfgPath, "-s", scriptPath}, Extra: extra}
	out, err := s.Run(ctx, c)
	if err != nil {
		return Response{Out: out}, fmt.Errorf("fw_setenv: %w: %s", err, strings.TrimSpace(out))
	}
	return Response{Out: out}, nil
}

func writeTemp(dir, pattern string, b []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	_, werr := f.Write(b)
	if err := errors.Join(werr, f.Close()); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// exportGPIO exports one line of exportableGPIO and hands its attributes to soundtapd.
func (s *Server) exportGPIO(n int) error {
	bank, line, err := s.gpioLineOf(n)
	if err != nil {
		return err
	}
	if !slices.Contains(exportableGPIO, gpioLine{bank, line}) {
		return fmt.Errorf("gpio%d (bank %s line %d) is not one soundtapd drives", n, bank, line)
	}
	dir := filepath.Join(s.GPIORoot, "gpio"+strconv.Itoa(n))
	if _, err := os.Stat(dir); err != nil {
		if err := os.WriteFile(filepath.Join(s.GPIORoot, "export"), []byte(strconv.Itoa(n)), 0o200); err != nil &&
			!errors.Is(err, syscall.EBUSY) {
			return fmt.Errorf("export gpio%d: %w", n, err)
		}
	}
	// The attributes appear a moment after the directory.
	for range 50 {
		if _, err := os.Stat(filepath.Join(dir, "value")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var errs []error
	for _, attr := range []string{"value", "direction", "edge", "active_low"} {
		p := filepath.Join(dir, attr)
		if _, err := os.Stat(p); err != nil {
			continue // edge is absent on a line without interrupts
		}
		errs = append(errs, s.Chown(p, s.UID, s.GID))
	}
	return errors.Join(errs...)
}

// gpioLineOf finds the bank and line of the sysfs GPIO number n.
func (s *Server) gpioLineOf(n int) (bank string, line int, err error) {
	chips, _ := filepath.Glob(filepath.Join(s.GPIORoot, "gpiochip*"))
	for _, chip := range chips {
		base, err1 := readInt(filepath.Join(chip, "base"))
		ngpio, err2 := readInt(filepath.Join(chip, "ngpio"))
		if err1 != nil || err2 != nil || n < base || n >= base+ngpio {
			continue
		}
		target, err := filepath.EvalSymlinks(chip)
		if err != nil {
			return "", 0, err
		}
		for _, b := range gpioBanks {
			if strings.Contains(target, b) {
				return b, n - base, nil
			}
		}
		return "", 0, fmt.Errorf("gpio%d is not on a SoC GPIO bank", n)
	}
	return "", 0, fmt.Errorf("no GPIO chip has gpio%d", n)
}

func readInt(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// resetForeignState is the part of a factory reset soundtapd's user cannot do: Spotify's
// credentials and cache (librespot's user) and BlueZ's and bluealsa's state (root). They
// go, and come back empty with their owners.
func (s *Server) resetForeignState() error {
	var errs []error
	for _, d := range []string{"spotify", "bluetooth", "bluealsa"} {
		errs = append(errs, os.RemoveAll(filepath.Join(s.NV, d)))
	}
	for _, d := range []string{"bluetooth", "bluealsa"} {
		errs = append(errs, freshDir(filepath.Join(s.NV, d)))
	}
	uid, gid, err := s.Lookup("spotify")
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, d := range []string{"spotify", "spotify/cache", "spotify/system"} {
		p := filepath.Join(s.NV, d)
		if err := freshDir(p); err != nil {
			return errors.Join(append(errs, err)...)
		}
		errs = append(errs, s.Chown(p, uid, gid))
	}
	return errors.Join(errs...)
}

// freshDir makes dir, root's and 0700, and refuses whatever else stands in its place: the
// group may create names in /mnt/nv, and a link put there between the removal and this
// must not be followed.
func freshDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}
