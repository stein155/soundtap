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

func (c *Command) String() string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// program is a Command with no descriptors that runs to its end.
func program(name string, args ...string) *Command {
	return &Command{Name: name, Args: args, Stdin: nil, Extra: nil, Detach: false}
}

// answer is a Response with only the command's output.
func answer(out string) Response { return Response{Out: out, Value: 0, Exit: 0, Error: ""} }

// The refusals of a request. A message with values reads them around its sentinel, as it
// always has: soundtapd shows it.
var (
	errDescriptorCount = errors.New("descriptors for a request that names")
	errInputDescriptor = errors.New("input descriptor")
	errUnknownOp       = errors.New("unknown operation")
	errNoBTCommand     = errors.New("bt-ctl.sh has no command")
	errNotRegular      = errors.New("the input is not a regular file")
	errScriptTooLong   = errors.New("environment script over")
	errNotOurGPIO      = errors.New("is not one soundtapd drives")
	errNotSoCBank      = errors.New("is not on a SoC GPIO bank")
	errNoGPIOChip      = errors.New("no GPIO chip has")
	errNotDirectory    = errors.New("is not a directory")
)

// Timeouts: a request has requestTimeout to arrive and its answer as long to leave; a NAND
// operation may run nandTimeout, any other otherTimeout; a bt-ctl.sh verb btTimeout, or
// btLongTimeout for one that calls the phones.
const (
	requestTimeout = 10 * time.Second
	nandTimeout    = 20 * time.Minute
	otherTimeout   = 2 * time.Minute
	btTimeout      = 5 * time.Second
	btLongTimeout  = time.Minute
)

// GPIO export: how often, and how long apart, the attributes are looked for after the
// export.
const (
	gpioWaitTries = 50
	gpioWaitStep  = 10 * time.Millisecond
)

// The modes the helper gives what it makes: the export file is written (0o200 is what a
// write needs), the mount point is root's and the group's, a fresh state directory root's.
const (
	exportMode   = 0o200
	mountDirMode = 0o750
	stateDirMode = 0o700
	volumeMode   = 0o640
	socketMode   = 0o660
)

// Server answers soundtapd's requests. NewServer fills in the speaker's defaults; a test
// replaces what reaches hardware.
type Server struct {
	Log *slog.Logger
	// UID and GID are soundtapd's: the user that may ask, besides root, and the owner
	// given what the helper hands over.
	UID, GID int

	Run         func(ctx context.Context, c *Command) (string, error)
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
		nand: sync.Mutex{},
		Log:  log, UID: uid, GID: gid,
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
		return Layout{}, fmt.Errorf("layout: %w", err)
	}

	mtd, err := flash.ReadMTD()
	if err != nil {
		return Layout{}, fmt.Errorf("layout: %w", err)
	}

	return NewLayout(cmdline, mtd), nil
}

func bcoPower(powered bool) error {
	power, unmap, err := bco.MemPower()
	if err != nil {
		return fmt.Errorf("BCO power switch: %w", err)
	}
	defer unmap()

	return power(powered)
}

// runCommand runs c to its end and returns its output, or starts it detached.
func runCommand(ctx context.Context, command *Command) (string, error) {
	if command.Detach {
		// Not ctx: a detached program outlives the request.
		cmd := exec.CommandContext(context.WithoutCancel(ctx), command.Name, command.Args...)

		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

		err := cmd.Start()
		if err != nil {
			return "", fmt.Errorf("starting %s: %w", command.Name, err)
		}

		safego.Go("rootd.reap", func() { _ = cmd.Wait() })

		return "", nil
	}

	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	if command.Stdin != nil {
		cmd.Stdin = command.Stdin
	}

	cmd.ExtraFiles = command.Extra

	out, err := cmd.CombinedOutput()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			return string(out), &ExitError{Code: exit.ExitCode()}
		}

		return string(out), err
	}

	return string(out), nil
}

// Listen opens the socket at path: only root and the group gid may connect.
func Listen(path string, gid int) (*net.UnixListener, error) {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("removing the old socket: %w", err)
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listening: %w", err)
	}

	// The group is the point: soundtapd connects through it.
	err = errors.Join(os.Chown(path, 0, gid), os.Chmod(path, socketMode))
	if err != nil {
		_ = listener.Close()

		return nil, err
	}

	return listener, nil
}

// Serve answers connections on l until ctx ends.
func (s *Server) Serve(ctx context.Context, listener *net.UnixListener) error {
	safego.Go("rootd.close", func() {
		<-ctx.Done()

		_ = listener.Close()
	})

	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // the listener was closed because ctx ended: a clean stop
			}

			return fmt.Errorf("accepting: %w", err)
		}

		safego.Go("rootd.handle", func() { s.handle(ctx, conn) })
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
func describe(req *Request) string {
	var out strings.Builder

	for _, field := range []struct {
		k   string
		v   any
		set bool
	}{
		{"mtd", req.MTD, req.MTD != 0},
		{"offset", fmt.Sprintf("%#x", req.Offset), req.Offset != 0},
		{"blocks", req.Blocks, req.Blocks != 0},
		{"skip", req.Skip, req.Skip != 0},
		{"pad", req.Pad, req.Pad},
		{"size", req.Size, req.Size != 0},
		{"copies", req.Copies, len(req.Copies) > 0},
		{"on", req.On, req.Op == OpBCOPower},
		{"name", req.Name, req.Name != ""},
		{"gpio", req.GPIO, req.GPIO != 0},
	} {
		if field.set {
			fmt.Fprintf(&out, "%s=%v ", field.k, field.v)
		}
	}

	return strings.TrimSpace(out.String())
}

// nandOp reports the operations that write NAND or the environment, one at a time.
func nandOp(op string) bool {
	switch op {
	case OpNANDErase, OpNANDWrite, OpNANDBlock, OpUBIFormat, OpUBIAttach,
		OpUBIDetach, OpSlotMount, OpSlotUmount, OpEnvSet:
		return true
	}

	return false
}

// inputOp reports the operations that read the request's input descriptor.
func inputOp(op string) bool {
	switch op {
	case OpNANDWrite, OpNANDBlock, OpUBIFormat, OpEnvSet:
		return true
	}

	return false
}

// Do validates r and carries it out. files are the descriptors that came with it, which
// Do neither keeps nor closes.
func (s *Server) Do(ctx context.Context, req *Request, files []*os.File) (Response, error) {
	input, extra, err := descriptors(req, files)
	if err != nil {
		return Response{}, err
	}

	if nandOp(req.Op) {
		s.nand.Lock()
		defer s.nand.Unlock()

		nandCtx, cancel := context.WithTimeout(ctx, nandTimeout)
		defer cancel()

		return s.doNAND(nandCtx, req, input, extra)
	}

	otherCtx, cancel := context.WithTimeout(ctx, otherTimeout)
	defer cancel()

	return s.doOther(otherCtx, req)
}

// handle answers one request on c.
func (s *Server) handle(ctx context.Context, conn *net.UnixConn) {
	defer conn.Close()

	uid, err := s.PeerUID(conn)
	if err != nil || (uid != s.UID && uid != 0) {
		s.Log.Warn("refused a peer", "uid", uid, "err", err)

		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(requestTimeout))

	var request Request

	files, err := readMessage(conn, &request)
	defer closeAll(files)

	var resp Response
	if err == nil {
		resp, err = s.Do(ctx, &request, files)
	}

	if err != nil {
		resp.Error = err.Error()
		s.Log.Warn("refused", "op", request.Op, "req", describe(&request), "err", err)
	} else {
		s.Log.Info("done", "op", request.Op, "req", describe(&request), "exit", resp.Exit)
	}
	// ubiformat's progress alone can pass the message limit: the end says what happened.
	resp.Out, resp.Error = tail(resp.Out), tail(resp.Error)

	_ = conn.SetWriteDeadline(time.Now().Add(requestTimeout))

	err = writeMessage(conn, resp)
	if err != nil {
		s.Log.Warn("answer lost", "op", request.Op, "err", err)
	}
}

// descriptors sorts files into the input and the lock the request names, refusing a count
// that differs or an input for an operation that reads none.
func descriptors(req *Request, files []*os.File) (*os.File, []*os.File, error) {
	want := 0
	if req.Input {
		want++
	}

	if req.Lock {
		want++
	}

	if len(files) != want {
		return nil, nil, fmt.Errorf("%d %w %d", len(files), errDescriptorCount, want)
	}

	var (
		input *os.File
		extra []*os.File
	)

	if req.Input {
		input = files[0]
	}

	if req.Lock {
		extra = []*os.File{files[len(files)-1]}
	}

	if inputOp(req.Op) != (input != nil) {
		return nil, nil, fmt.Errorf("%s: %w %v, want %v", req.Op, errInputDescriptor, input != nil, inputOp(req.Op))
	}

	return input, extra, nil
}

// doOther carries out every request that is not a NAND one.
func (s *Server) doOther(ctx context.Context, req *Request) (Response, error) {
	switch req.Op {
	case OpReboot:
		out, err := s.Run(ctx, program("reboot"))

		return answer(out), err
	case OpBootCountRead:
		value, err := s.RTC.ReadScratch2()
		if err != nil {
			return Response{Out: "", Value: value, Exit: 0, Error: ""}, fmt.Errorf("reading SCRATCH2: %w", err)
		}

		return Response{Out: "", Value: value, Exit: 0, Error: ""}, nil
	case OpBootCountClear:
		err := s.RTC.ClearScratch2()
		if err != nil {
			return Response{}, fmt.Errorf("clearing SCRATCH2: %w", err)
		}

		return answer(""), nil
	case OpBCOPower:
		return Response{}, s.BCOPower(req.On)
	case OpHostname:
		err := CheckHostname(req.Name)
		if err != nil {
			return Response{}, err
		}

		return Response{}, s.SetHostname(req.Name)
	case OpGPIOExport:
		return Response{}, s.exportGPIO(req.GPIO)
	case OpClockSync:
		syncClock := program(s.InitScript, "sync-clock")
		syncClock.Detach = true

		_, err := s.Run(ctx, syncClock)

		return Response{}, err
	case OpBTCtl:
		return s.btCtl(ctx, req.Name)
	case OpNVReset:
		return Response{}, s.resetForeignState()
	}

	return Response{}, fmt.Errorf("%w %q", errUnknownOp, req.Op)
}

// btCtl runs bt-ctl.sh verb; its exit status is the answer, not an error.
func (s *Server) btCtl(ctx context.Context, verb string) (Response, error) {
	if !slices.Contains(BTVerbs(), verb) {
		return Response{}, fmt.Errorf("%w %q", errNoBTCommand, verb)
	}
	// A transport key must not hang on a wedged bluetoothd; calling the phones takes up to
	// five bounded tries.
	limit := btTimeout
	if verb == "reconnect" || verb == "trim" || verb == "disconnect" {
		limit = btLongTimeout
	}

	btCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	out, err := s.Run(btCtx, program("sh", s.BTCtl, verb))

	if exit, ok := errors.AsType[*ExitError](err); ok {
		return Response{Out: out, Value: 0, Exit: exit.Code, Error: ""}, nil
	}

	return answer(out), err
}

// doNAND runs one NAND, UBI or environment operation after holding it to the layout.
func (s *Server) doNAND(ctx context.Context, req *Request, input *os.File, extra []*os.File) (Response, error) {
	layout, err := s.Layout()
	if err != nil {
		return Response{}, fmt.Errorf("reading the partition table: %w", err)
	}

	if req.Op == OpEnvSet {
		return s.setEnv(ctx, layout, req, input, extra)
	}

	cmd, err := s.nandCommand(layout, req, input)
	if err != nil {
		return Response{}, err
	}

	cmd.Extra = extra

	out, err := s.Run(ctx, cmd)
	if err != nil {
		return answer(out), fmt.Errorf("%s: %w: %s", cmd.Name, err, strings.TrimSpace(out))
	}

	if req.Op == OpUBIAttach {
		s.grantVolumes()
	}

	return answer(out), nil
}

// nandCommand is the command one NAND or UBI request runs, once layout allows it.
func (s *Server) nandCommand(layout Layout, req *Request, input *os.File) (*Command, error) {
	dev := filepath.Join(s.DevDir, "mtd"+strconv.Itoa(req.MTD))
	itoa := func(n int64) string { return strconv.FormatInt(n, 10) }

	switch req.Op {
	case OpNANDErase:
		return program("flash_erase", dev, itoa(req.Offset), itoa(req.Blocks)), layout.checkErase(req)
	case OpNANDWrite:
		size, err := regularSize(input)
		if err == nil {
			err = layout.checkWrite(req, size)
		}

		var args []string
		if req.Pad {
			args = append(args, "-p")
		}

		if req.Offset > 0 {
			args = append(args, "-s", itoa(req.Offset))
		}
		// The passed file, opened again through its descriptor: nandwrite reads a regular
		// file of known size, as it did from soundtapd's own path.
		cmd := program("nandwrite", append(args, dev, "/proc/self/fd/0")...)
		cmd.Stdin = input

		return cmd, err
	case OpNANDBlock:
		size, err := regularSize(input)
		if err == nil {
			err = layout.checkBlock(req, size)
		}

		block := layout.blockSize(req.MTD)
		if block <= 0 {
			block = 1
		}

		cmd := program("dd",
			"if=/proc/self/fd/0", "of="+dev, "bs="+itoa(block),
			"count=1", "skip="+itoa(req.Skip), "seek="+itoa(req.Offset/block), "conv=notrunc",
		)
		cmd.Stdin = input

		return cmd, err
	case OpUBIFormat:
		cmd := program("ubiformat", dev, "-f", "-", "-S", itoa(req.Size), "-s", "2048", "-y")
		cmd.Stdin = input

		return cmd, layout.checkFormat(req)
	case OpUBIAttach:
		return program(cmdUBIAttach, "-m", strconv.Itoa(req.MTD), "-d", verifyUBI, "-O", "2048"),
			layout.checkAttach(req)
	case OpUBIDetach:
		return program("ubidetach", "-d", verifyUBI), nil
	case OpSlotMount:
		return program("mount", "-t", "ubifs", "-o", "ro", "ubi"+verifyUBI+":rootfs", s.MountDir),
			os.MkdirAll(s.MountDir, mountDirMode)
	case OpSlotUmount:
		return program("umount", s.MountDir), nil
	}

	return program(""), fmt.Errorf("%w %q", errUnknownOp, req.Op)
}

// verifyUBI is the UBI device the written slot is attached as (flash's verifyUBI).
const verifyUBI = "7"

// cmdUBIAttach is mtd-utils' attach, as internal/flash runs it and the helper does.
const cmdUBIAttach = "ubiattach"

// regularSize is the size of a passed regular file; anything else is refused.
func regularSize(input *os.File) (int64, error) {
	info, err := input.Stat()
	if err != nil {
		return 0, fmt.Errorf("the input: %w", err)
	}

	if !info.Mode().IsRegular() {
		return 0, errNotRegular
	}

	return info.Size(), nil
}

// grantVolumes lets soundtapd's group read the attached slot's UBI volumes back, the way
// VerifyRootfs streams them; it never writes them.
func (s *Server) grantVolumes() {
	nodes, _ := filepath.Glob(filepath.Join(s.DevDir, "ubi"+verifyUBI+"_*"))
	for _, node := range nodes {
		err := errors.Join(s.Chown(node, 0, s.GID), os.Chmod(node, volumeMode))
		if err != nil {
			s.Log.Warn("volume not readable for soundtapd", "node", node, "err", err)
		}
	}
}

// setEnv runs fw_setenv over a configuration the helper writes itself from the checked
// copies, with the checked script.
func (s *Server) setEnv(
	ctx context.Context, layout Layout, req *Request, input *os.File, extra []*os.File,
) (Response, error) {
	err := layout.checkEnvCopies(req.Copies)
	if err != nil {
		return Response{}, err
	}

	script, err := io.ReadAll(io.LimitReader(input, MaxMessage+1))
	if err != nil {
		return Response{}, fmt.Errorf("reading the environment script: %w", err)
	}

	if len(script) > MaxMessage {
		return Response{}, fmt.Errorf("%w %d bytes", errScriptTooLong, MaxMessage)
	}

	err = CheckEnvScript(script)
	if err != nil {
		return Response{}, err
	}

	var cfg strings.Builder
	for _, envCopy := range req.Copies {
		fmt.Fprintf(&cfg, "%s %#x %#x %#x 1\n",
			filepath.Join(s.DevDir, "mtd"+strconv.Itoa(envCopy.MTD)), envCopy.Offset, envCopy.Size, envCopy.EraseSize)
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

	cmd := program("fw_setenv", "-c", cfgPath, "-s", scriptPath)
	cmd.Extra = extra

	out, err := s.Run(ctx, cmd)
	if err != nil {
		return answer(out), fmt.Errorf("fw_setenv: %w: %s", err, strings.TrimSpace(out))
	}

	return answer(out), nil
}

func writeTemp(dir, pattern string, data []byte) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("writing %s: %w", pattern, err)
	}

	_, werr := file.Write(data)

	err = errors.Join(werr, file.Close())
	if err != nil {
		_ = os.Remove(file.Name())

		return "", err
	}

	return file.Name(), nil
}

// exportGPIO exports one line of exportableGPIO and hands its attributes to soundtapd.
func (s *Server) exportGPIO(gpio int) error {
	line, err := s.gpioLineOf(gpio)
	if err != nil {
		return err
	}

	if !slices.Contains(exportableGPIO(), line) {
		return fmt.Errorf("gpio%d (bank %s line %d) %w", gpio, line.bank, line.line, errNotOurGPIO)
	}

	dir := filepath.Join(s.GPIORoot, "gpio"+strconv.Itoa(gpio))

	_, err = os.Stat(dir)
	if err != nil {
		err = os.WriteFile(filepath.Join(s.GPIORoot, "export"), []byte(strconv.Itoa(gpio)), exportMode)
		if err != nil &&
			!errors.Is(err, syscall.EBUSY) {
			return fmt.Errorf("export gpio%d: %w", gpio, err)
		}
	}
	// The attributes appear a moment after the directory.
	for range gpioWaitTries {
		_, statErr := os.Stat(filepath.Join(dir, "value"))
		if statErr == nil {
			break
		}

		time.Sleep(gpioWaitStep)
	}

	var errs []error

	for _, attr := range []string{"value", "direction", "edge", "active_low"} {
		path := filepath.Join(dir, attr)

		_, statErr := os.Stat(path)
		if statErr != nil {
			continue // edge is absent on a line without interrupts
		}

		errs = append(errs, s.Chown(path, s.UID, s.GID))
	}

	return errors.Join(errs...)
}

// gpioLineOf finds the bank and line of the sysfs GPIO number gpio.
func (s *Server) gpioLineOf(gpio int) (gpioLine, error) {
	chips, _ := filepath.Glob(filepath.Join(s.GPIORoot, "gpiochip*"))
	for _, chip := range chips {
		base, err1 := readInt(filepath.Join(chip, "base"))

		ngpio, err2 := readInt(filepath.Join(chip, "ngpio"))
		if err1 != nil || err2 != nil || gpio < base || gpio >= base+ngpio {
			continue
		}

		target, err := filepath.EvalSymlinks(chip)
		if err != nil {
			return gpioLine{bank: "", line: 0}, fmt.Errorf("gpio%d: %w", gpio, err)
		}

		for _, bank := range gpioBanks() {
			if strings.Contains(target, bank) {
				return gpioLine{bank: bank, line: gpio - base}, nil
			}
		}

		return gpioLine{bank: "", line: 0}, fmt.Errorf("gpio%d %w", gpio, errNotSoCBank)
	}

	return gpioLine{bank: "", line: 0}, fmt.Errorf("%w gpio%d", errNoGPIOChip, gpio)
}

func readInt(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}

	value, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}

	return value, nil
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
		path := filepath.Join(s.NV, d)

		err = freshDir(path)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}

		errs = append(errs, s.Chown(path, uid, gid))
	}

	return errors.Join(errs...)
}

// freshDir makes dir, root's and 0700, and refuses whatever else stands in its place: the
// group may create names in /mnt/nv, and a link put there between the removal and this
// must not be followed.
func freshDir(dir string) error {
	err := os.Mkdir(dir, stateDirMode)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("making %s: %w", dir, err)
	}

	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("checking %s: %w", dir, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%s %w", dir, errNotDirectory)
	}

	return nil
}
