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
func NewClient(path string) *Client { return &Client{Path: path} }

// FromEnv is the helper init.d/soundtap started, or nil when there is none (EnvVar unset).
func FromEnv() *Client {
	if p := os.Getenv(EnvVar); p != "" {
		return NewClient(p)
	}
	return nil
}

// Do sends r with files and waits for the answer: up to 25 minutes for a NAND operation,
// which may erase and write a whole partition, 3 for anything else.
func (c *Client) Do(r Request, files ...*os.File) (Response, error) {
	limit := 3 * time.Minute
	switch r.Op {
	case OpNANDErase, OpNANDWrite, OpNANDBlock, OpUBIFormat, OpUBIAttach, OpUBIDetach, OpSlotMount, OpSlotUmount, OpEnvSet:
		limit = 25 * time.Minute
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: c.Path, Net: "unix"})
	if err != nil {
		return Response{}, fmt.Errorf("soundtap-root: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(limit))
	if err := writeMessage(conn, r, files...); err != nil {
		return Response{}, fmt.Errorf("soundtap-root: %s: %w", r.Op, err)
	}
	var resp Response
	got, err := readMessage(conn, &resp)
	closeAll(got)
	if err != nil {
		return Response{}, fmt.Errorf("soundtap-root: %s: %w", r.Op, err)
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("soundtap-root: %s: %s", r.Op, resp.Error)
	}
	return resp, nil
}

func (c *Client) simple(r Request) error {
	_, err := c.Do(r)
	return err
}

// Reboot reboots the speaker.
func (c *Client) Reboot() error { return c.simple(Request{Op: OpReboot}) }

// ReadScratch2 is U-Boot's boot count register (flash.RTC).
func (c *Client) ReadScratch2() (uint32, error) {
	resp, err := c.Do(Request{Op: OpBootCountRead})
	return resp.Value, err
}

// ClearScratch2 clears it and makes the next reboot cold (flash.RTC).
func (c *Client) ClearScratch2() error { return c.simple(Request{Op: OpBootCountClear}) }

// BCOPower switches the ST20 BCO's supply.
func (c *Client) BCOPower(on bool) error { return c.simple(Request{Op: OpBCOPower, On: on}) }

// SetHostname names the host.
func (c *Client) SetHostname(name string) error { return c.simple(Request{Op: OpHostname, Name: name}) }

// ExportGPIO exports sysfs GPIO n, when it is a line soundtapd drives, and makes its
// attributes soundtapd's.
func (c *Client) ExportGPIO(n int) error { return c.simple(Request{Op: OpGPIOExport, GPIO: n}) }

// SyncClock starts init.d/soundtap sync-clock.
func (c *Client) SyncClock() error { return c.simple(Request{Op: OpClockSync}) }

// ResetForeignState removes and recreates Spotify's and BlueZ's state (a factory reset).
func (c *Client) ResetForeignState() error { return c.simple(Request{Op: OpNVReset}) }

// BTCtl runs bt-ctl.sh verb; a non-zero exit is an *ExitError.
func (c *Client) BTCtl(verb string) error {
	resp, err := c.Do(Request{Op: OpBTCtl, Name: verb})
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
	}
	return refused(name, args)
}

func refused(name string, args []string) error {
	return fmt.Errorf("soundtap-root does not run %s %s", name, strings.Join(args, " "))
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
		return exec.CommandContext(context.Background(), "test", args...).Run()
	}
	return refused(name, args)
}

// slot is VerifyRootfs's attach, mount, umount and detach of the written slot.
func (c *Client) slot(lock *os.File, name string, args []string) error {
	switch strings.Join(append([]string{name}, args...), " ") {
	case "ubidetach -d " + verifyUBI:
		return c.send(Request{Op: OpUBIDetach}, lock, nil)
	case "mount -t ubifs -o ro ubi" + verifyUBI + ":rootfs " + MountDir:
		return c.send(Request{Op: OpSlotMount}, lock, nil)
	case "umount " + MountDir:
		return c.send(Request{Op: OpSlotUmount}, lock, nil)
	}
	if name == cmdUBIAttach && len(args) == 6 && args[0] == "-m" && args[2] == "-d" && args[3] == verifyUBI &&
		args[4] == "-O" && args[5] == "2048" {
		if mtd, err := strconv.Atoi(args[1]); err == nil {
			return c.send(Request{Op: OpUBIAttach, MTD: mtd}, lock, nil)
		}
	}
	return refused(name, args)
}

// erase is `flash_erase /dev/mtdN OFFSET BLOCKS`.
func (c *Client) erase(lock *os.File, args []string) error {
	if len(args) != 3 {
		return refused("flash_erase", args)
	}
	mtd, err1 := mtdOf(args[0])
	off, err2 := strconv.ParseInt(args[1], 10, 64)
	blocks, err3 := strconv.ParseInt(args[2], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return err
	}
	return c.send(Request{Op: OpNANDErase, MTD: mtd, Offset: off, Blocks: blocks}, lock, nil)
}

// ubiformat is `ubiformat /dev/mtdN -f - -S SIZE -s 2048 -y` with the image on stdin.
func (c *Client) ubiformat(lock *os.File, stdin io.Reader, args []string) error {
	if len(args) != 8 || args[1] != "-f" || args[2] != "-" || args[3] != "-S" || args[5] != "-s" ||
		args[6] != "2048" || args[7] != "-y" || stdin == nil {
		return refused("ubiformat", args)
	}
	mtd, err1 := mtdOf(args[0])
	size, err2 := strconv.ParseInt(args[4], 10, 64)
	if err := errors.Join(err1, err2); err != nil {
		return err
	}
	return c.sendStream(Request{Op: OpUBIFormat, MTD: mtd, Size: size}, lock, stdin)
}

// mtdOf is N of /dev/mtdN.
func mtdOf(dev string) (int, error) {
	n, ok := strings.CutPrefix(dev, "/dev/mtd")
	if !ok {
		return 0, fmt.Errorf("%s is not an MTD device", dev)
	}
	return strconv.Atoi(n)
}

// nandwrite is `nandwrite [-p] [-s OFFSET] /dev/mtdN FILE`.
func (c *Client) nandwrite(lock *os.File, args []string) error {
	r := Request{Op: OpNANDWrite}
	for len(args) > 2 {
		switch {
		case args[0] == "-p":
			r.Pad, args = true, args[1:]
		case args[0] == "-s" && len(args) > 3:
			off, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return err
			}
			r.Offset, args = off, args[2:]
		default:
			return refused("nandwrite", args)
		}
	}
	if len(args) != 2 {
		return errors.New("soundtap-root: nandwrite needs a device and a file")
	}
	mtd, err := mtdOf(args[0])
	if err != nil {
		return err
	}
	r.MTD = mtd
	return c.sendFile(r, lock, args[1])
}

// dd is install's one-block U-Boot write:
// `dd if=FILE of=/dev/mtdN bs=N count=1 skip=S seek=K conv=notrunc`.
func (c *Client) dd(lock *os.File, args []string) error {
	kv := map[string]string{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return refused("dd", args)
		}
		kv[k] = v
	}
	if len(kv) != 7 || kv["count"] != "1" || kv["conv"] != "notrunc" {
		return refused("dd", args)
	}
	mtd, err := mtdOf(kv["of"])
	if err != nil {
		return err
	}
	bs, err1 := strconv.ParseInt(kv["bs"], 10, 64)
	skip, err2 := strconv.ParseInt(kv["skip"], 10, 64)
	seek, err3 := strconv.ParseInt(kv["seek"], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return err
	}
	return c.sendFile(Request{Op: OpNANDBlock, MTD: mtd, Offset: seek * bs, Skip: skip, Size: bs}, lock, kv["if"])
}

// setenv reads the fw_env.config internal/flash wrote into copies and passes the script.
func (c *Client) setenv(lock *os.File, cfg, script string) error {
	f, err := os.Open(cfg)
	if err != nil {
		return err
	}
	defer f.Close()
	var copies []EnvCopy
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) < 4 {
			return fmt.Errorf("fw_env.config line %q", s.Text())
		}
		mtd, err := mtdOf(fields[0])
		if err != nil {
			return err
		}
		var nums [3]int64
		for i := range nums {
			if nums[i], err = strconv.ParseInt(fields[i+1], 0, 64); err != nil {
				return err
			}
		}
		copies = append(copies, EnvCopy{MTD: mtd, Offset: nums[0], Size: nums[1], EraseSize: nums[2]})
	}
	if err := s.Err(); err != nil {
		return err
	}
	return c.sendFile(Request{Op: OpEnvSet, Copies: copies}, lock, script)
}

// sendFile passes the file at path, opened read-only here, as the input.
func (c *Client) sendFile(r Request, lock *os.File, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return c.send(r, lock, f)
}

// sendStream passes stdin as the input: as it is when it is a file, through a pipe fed
// from here otherwise.
func (c *Client) sendStream(r Request, lock *os.File, stdin io.Reader) error {
	if f, ok := stdin.(*os.File); ok {
		return c.send(r, lock, f)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	safego.Go("rootd.stream", func() {
		_, _ = io.Copy(pw, stdin)
		_ = pw.Close()
	})
	err = c.send(r, lock, pr)
	// The helper holds its own copy of the read end; once it is done, a write here fails
	// rather than blocking, and the copy ends with the caller's stream or when the caller
	// closes it (flash.StartRootfs does once Run returns).
	_ = pr.Close()
	if err != nil {
		_ = pw.Close()
	}
	return err
}

func (c *Client) send(r Request, lock, input *os.File) error {
	var files []*os.File
	if input != nil {
		r.Input = true
		files = append(files, input)
	}
	if lock != nil {
		r.Lock = true
		files = append(files, lock)
	}
	_, err := c.Do(r, files...)
	return err
}

// Interface checks.
var _ flash.RTC = (*Client)(nil)
