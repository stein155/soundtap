// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package rootd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/safego"
)

// Client is soundtapd's side of the socket.
type Client struct {
	Path string
	// LocalRun runs what the helper need not: test -x on the mounted slot. nil is exec.
	LocalRun func(name string, args ...string) error
}

// NewClient talks to the helper at path.
func NewClient(path string) *Client { return &Client{Path: path, LocalRun: nil} }

// How long the client waits for an answer: a NAND operation may erase and write a whole
// partition.
const (
	nandWait  = 25 * time.Minute
	otherWait = 3 * time.Minute
)

// helperError is the helper's own refusal, as its answer words it.
type helperError string

func (e helperError) Error() string { return string(e) }

// The refusals on this side, before anything is sent.
var (
	errNotRun       = errors.New("soundtap-root does not run")
	errNotMTD       = errors.New("is not an MTD device")
	errNandwriteUse = errors.New("soundtap-root: nandwrite needs a device and a file")
	errConfigLine   = errors.New("fw_env.config line")
)

// The shapes of the commands the client translates: flash_erase's three arguments, dd's
// seven key=value pairs, a fw_env.config line's device and three numbers, nandwrite's
// device and file, ubiattach's six and ubiformat's eight arguments.
const (
	eraseArgs     = 3
	ddPairs       = 7
	configFields  = 4
	nandwriteTail = 2
	ubiattachArgs = 6
	ubiformatArgs = 8
	envConfigNums = 3
)

// FromEnv is the helper init.d/soundtap started, or nil when there is none (EnvVar unset).
func FromEnv() *Client {
	if p := os.Getenv(EnvVar); p != "" {
		return NewClient(p)
	}

	return nil
}

// Do sends r with files and waits for the answer: up to 25 minutes for a NAND operation,
// which may erase and write a whole partition, 3 for anything else.
func (c *Client) Do(req *Request, files ...*os.File) (Response, error) {
	limit := otherWait
	if nandOp(req.Op) {
		limit = nandWait
	}

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: c.Path, Net: "unix"})
	if err != nil {
		return Response{}, fmt.Errorf("soundtap-root: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(limit))

	err = writeMessage(conn, req, files...)
	if err != nil {
		return Response{}, fmt.Errorf("soundtap-root: %s: %w", req.Op, err)
	}

	var resp Response

	got, err := readMessage(conn, &resp)
	closeAll(got)

	if err != nil {
		return Response{}, fmt.Errorf("soundtap-root: %s: %w", req.Op, err)
	}

	if resp.Error != "" {
		return resp, fmt.Errorf("soundtap-root: %s: %w", req.Op, helperError(resp.Error))
	}

	return resp, nil
}

// Reboot reboots the speaker.
func (c *Client) Reboot() error { return c.simple(request(OpReboot)) }

// ReadScratch2 is U-Boot's boot count register (flash.RTC).
func (c *Client) ReadScratch2() (uint32, error) {
	resp, err := c.Do(request(OpBootCountRead))

	return resp.Value, err
}

// ClearScratch2 clears it and makes the next reboot cold (flash.RTC).
func (c *Client) ClearScratch2() error { return c.simple(request(OpBootCountClear)) }

// BCOPower switches the ST20 BCO's supply.
func (c *Client) BCOPower(powered bool) error {
	r := request(OpBCOPower)
	r.On = powered

	return c.simple(r)
}

// SetHostname names the host.
func (c *Client) SetHostname(name string) error {
	r := request(OpHostname)
	r.Name = name

	return c.simple(r)
}

// ExportGPIO exports sysfs GPIO gpio, when it is a line soundtapd drives, and makes its
// attributes soundtapd's.
func (c *Client) ExportGPIO(gpio int) error {
	r := request(OpGPIOExport)
	r.GPIO = gpio

	return c.simple(r)
}

// SyncClock starts init.d/soundtap sync-clock.
func (c *Client) SyncClock() error { return c.simple(request(OpClockSync)) }

// ResetForeignState removes and recreates Spotify's and BlueZ's state (a factory reset).
func (c *Client) ResetForeignState() error { return c.simple(request(OpNVReset)) }

// BTCtl runs bt-ctl.sh verb; a non-zero exit is an *ExitError.
func (c *Client) BTCtl(verb string) error {
	r := request(OpBTCtl)
	r.Name = verb

	resp, err := c.Do(r)
	if err != nil {
		return err
	}

	if resp.Exit != 0 {
		return &ExitError{Code: resp.Exit}
	}

	return nil
}

// Run sends one of internal/flash's and internal/install's NAND commands, in the shape a
// flash.Command gets it, to the helper as its typed request. lock is the operations lock
// to pass along, or nil. A command it does not know is refused, never run.
func (c *Client) Run(lock *os.File, stdin io.Reader, name string, args ...string) error {
	switch name {
	case "mkdir", "test":
		return c.local(name, args)
	case cmdUBIAttach, "ubidetach", "mount", "umount":
		return c.slot(lock, name, args)
	case "flash_erase":
		return c.erase(lock, args)
	case "nandwrite":
		return c.nandwrite(lock, args)
	case "dd":
		return c.dd(lock, args)
	case "ubiformat":
		return c.ubiformat(lock, stdin, args)
	case "fw_setenv":
		if len(args) == 4 && args[0] == "-c" && args[2] == "-s" {
			return c.setenv(lock, args[1], args[3])
		}
	default: // refused below
	}

	return refused(name, args)
}

func (c *Client) simple(r *Request) error {
	_, err := c.Do(r)

	return err
}

// request is a Request for op with no arguments.
func request(op string) *Request {
	return &Request{
		Op: op, MTD: 0, Offset: 0, Blocks: 0, Skip: 0, Pad: false, Size: 0, Copies: nil,
		On: false, Name: "", GPIO: 0, Input: false, Lock: false,
	}
}

func refused(name string, args []string) error {
	return fmt.Errorf("%w %s %s", errNotRun, name, strings.Join(args, " "))
}

// local runs what needs no root here: VerifyRootfs's mkdir of its mount point (the helper
// makes its own) and its test -x on the mounted slot.
func (c *Client) local(name string, args []string) error {
	switch {
	case name == "mkdir" && len(args) == 2 && args[0] == "-p" && args[1] == MountDir && flash.VerifyDir == MountDir:
		return nil
	case name == "test" && len(args) == 2 && args[0] == "-x" && strings.HasPrefix(args[1], MountDir+"/"):
		if c.LocalRun != nil {
			return c.LocalRun(name, args...)
		}

		err := exec.CommandContext(context.Background(), "test", args...).Run()
		if err != nil {
			return fmt.Errorf("test %s: %w", strings.Join(args, " "), err)
		}

		return nil
	}

	return refused(name, args)
}

// slot is VerifyRootfs's attach, mount, umount and detach of the written slot.
func (c *Client) slot(lock *os.File, name string, args []string) error {
	switch strings.Join(append([]string{name}, args...), " ") {
	case "ubidetach -d " + verifyUBI:
		return c.send(request(OpUBIDetach), lock, nil)
	case "mount -t ubifs -o ro ubi" + verifyUBI + ":rootfs " + MountDir:
		return c.send(request(OpSlotMount), lock, nil)
	case "umount " + MountDir:
		return c.send(request(OpSlotUmount), lock, nil)
	}

	if name == cmdUBIAttach && len(args) == ubiattachArgs && args[0] == "-m" && args[2] == "-d" &&
		args[3] == verifyUBI && args[4] == "-O" && args[5] == "2048" {
		mtd, err := strconv.Atoi(args[1])
		if err == nil {
			r := request(OpUBIAttach)
			r.MTD = mtd

			return c.send(r, lock, nil)
		}
	}

	return refused(name, args)
}

// erase is `flash_erase /dev/mtdN OFFSET BLOCKS`.
func (c *Client) erase(lock *os.File, args []string) error {
	if len(args) != eraseArgs {
		return refused("flash_erase", args)
	}

	mtd, err1 := mtdOf(args[0])
	off, err2 := strconv.ParseInt(args[1], 10, 64)

	blocks, err3 := strconv.ParseInt(args[2], 10, 64)

	err := errors.Join(err1, err2, err3)
	if err != nil {
		return err
	}

	r := request(OpNANDErase)
	r.MTD, r.Offset, r.Blocks = mtd, off, blocks

	return c.send(r, lock, nil)
}

// ubiformat is `ubiformat /dev/mtdN -f - -S SIZE -s 2048 -y` with the image on stdin.
func (c *Client) ubiformat(lock *os.File, stdin io.Reader, args []string) error {
	if len(args) != ubiformatArgs || args[1] != "-f" || args[2] != "-" || args[3] != "-S" || args[5] != "-s" ||
		args[6] != "2048" || args[7] != "-y" || stdin == nil {
		return refused("ubiformat", args)
	}

	mtd, err1 := mtdOf(args[0])

	size, err2 := strconv.ParseInt(args[4], 10, 64)

	err := errors.Join(err1, err2)
	if err != nil {
		return err
	}

	r := request(OpUBIFormat)
	r.MTD, r.Size = mtd, size

	return c.sendStream(r, lock, stdin)
}

// mtdOf is N of /dev/mtdN.
func mtdOf(dev string) (int, error) {
	number, ok := strings.CutPrefix(dev, "/dev/mtd")
	if !ok {
		return 0, fmt.Errorf("%s %w", dev, errNotMTD)
	}

	mtd, err := strconv.Atoi(number)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", dev, err)
	}

	return mtd, nil
}

// nandwrite is `nandwrite [-p] [-s OFFSET] /dev/mtdN FILE`.
func (c *Client) nandwrite(lock *os.File, args []string) error {
	req := request(OpNANDWrite)

	for len(args) > nandwriteTail {
		switch {
		case args[0] == "-p":
			req.Pad, args = true, args[1:]
		case args[0] == "-s" && len(args) > 3:
			off, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("nandwrite -s: %w", err)
			}

			req.Offset, args = off, args[2:]
		default:
			return refused("nandwrite", args)
		}
	}

	if len(args) != nandwriteTail {
		return errNandwriteUse
	}

	mtd, err := mtdOf(args[0])
	if err != nil {
		return err
	}

	req.MTD = mtd

	return c.sendFile(req, lock, args[1])
}

// dd is install's one-block U-Boot write:
// `dd if=FILE of=/dev/mtdN bs=N count=1 skip=S seek=K conv=notrunc`.
func (c *Client) dd(lock *os.File, args []string) error {
	pairs := map[string]string{}

	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return refused("dd", args)
		}

		pairs[key] = value
	}

	if len(pairs) != ddPairs || pairs["count"] != "1" || pairs["conv"] != "notrunc" {
		return refused("dd", args)
	}

	mtd, err := mtdOf(pairs["of"])
	if err != nil {
		return err
	}

	block, err1 := strconv.ParseInt(pairs["bs"], 10, 64)
	skip, err2 := strconv.ParseInt(pairs["skip"], 10, 64)
	seek, err3 := strconv.ParseInt(pairs["seek"], 10, 64)

	err = errors.Join(err1, err2, err3)
	if err != nil {
		return err
	}

	r := request(OpNANDBlock)
	r.MTD, r.Offset, r.Skip, r.Size = mtd, seek*block, skip, block

	return c.sendFile(r, lock, pairs["if"])
}

// setenv reads the fw_env.config internal/flash wrote into copies and passes the script.
func (c *Client) setenv(lock *os.File, cfg, script string) error {
	config, err := os.Open(cfg)
	if err != nil {
		return fmt.Errorf("fw_env.config: %w", err)
	}
	defer config.Close()

	var copies []EnvCopy

	lines := bufio.NewScanner(config)
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}

		if len(fields) < configFields {
			return fmt.Errorf("%w %q", errConfigLine, lines.Text())
		}

		mtd, mtdErr := mtdOf(fields[0])
		if mtdErr != nil {
			return mtdErr
		}

		var nums [envConfigNums]int64
		for i := range nums {
			nums[i], err = strconv.ParseInt(fields[i+1], 0, 64)
			if err != nil {
				return fmt.Errorf("fw_env.config: %w", err)
			}
		}

		copies = append(copies, EnvCopy{MTD: mtd, Offset: nums[0], Size: nums[1], EraseSize: nums[2]})
	}

	err = lines.Err()
	if err != nil {
		return fmt.Errorf("fw_env.config: %w", err)
	}

	r := request(OpEnvSet)
	r.Copies = copies

	return c.sendFile(r, lock, script)
}

// sendFile passes the file at path, opened read-only here, as the input.
func (c *Client) sendFile(req *Request, lock *os.File, path string) error {
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("the input: %w", err)
	}
	defer input.Close()

	return c.send(req, lock, input)
}

// sendStream passes stdin as the input: as it is when it is a file, through a pipe fed
// from here otherwise.
func (c *Client) sendStream(req *Request, lock *os.File, stdin io.Reader) error {
	if file, ok := stdin.(*os.File); ok {
		return c.send(req, lock, file)
	}

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("the input pipe: %w", err)
	}

	safego.Go("rootd.stream", func() {
		_, _ = io.Copy(writeEnd, stdin)
		_ = writeEnd.Close()
	})

	err = c.send(req, lock, readEnd)
	// The helper holds its own copy of the read end; once it is done, a write here fails
	// rather than blocking, and the copy ends with the caller's stream or when the caller
	// closes it (flash.StartRootfs does once Run returns).
	_ = readEnd.Close()
	if err != nil {
		_ = writeEnd.Close()
	}

	return err
}

func (c *Client) send(req *Request, lock, input *os.File) error {
	var files []*os.File

	if input != nil {
		req.Input = true

		files = append(files, input)
	}

	if lock != nil {
		req.Lock = true

		files = append(files, lock)
	}

	_, err := c.Do(req, files...)

	return err
}

// Interface checks.
var _ flash.RTC = (*Client)(nil)
