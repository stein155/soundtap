// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package ubootenvtest stands in for libubootenv's fw_printenv and fw_setenv in tests: it
// runs them against NAND held in memory, one byte slice per MTD device, the way the real
// tools use the fw_env.config they are given. One config line is the single 8 KiB copy
// Bose's U-Boot reads; two are our redundant environment, where fw_setenv writes the copy
// that is not current with the next flag, as libubootenv does.
package ubootenvtest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// Copy is one line of a fw_env.config: the device, where the copy starts in it, its size
// and the erase block it lives in.
type Copy struct {
	Dev                  string
	Offset, Size, Sector int
}

// Config reads the -c config passed to fw_printenv or fw_setenv.
func Config(args []string) ([]Copy, error) {
	cfg := flag(args, "-c")
	if cfg == "" {
		return nil, errors.New("no -c config")
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		return nil, err
	}
	var copies []Copy
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return nil, fmt.Errorf("config line %q has no device, offset, size and sector", line)
		}
		c := Copy{Dev: fields[0]}
		for i, v := range []*int{&c.Offset, &c.Size, &c.Sector} {
			n, err := strconv.ParseInt(fields[i+1], 0, 64)
			if err != nil {
				return nil, fmt.Errorf("config line %q: %w", line, err)
			}
			*v = int(n)
		}
		copies = append(copies, c)
	}
	if len(copies) == 0 || len(copies) > 2 {
		return nil, fmt.Errorf("config %q holds %d copies", b, len(copies))
	}
	return copies, nil
}

// Device is the MTD device of the config's first copy.
func Device(args []string) (string, error) {
	copies, err := Config(args)
	if err != nil {
		return "", err
	}
	return copies[0].Dev, nil
}

// read returns the copy's bytes, erased flash past what the device holds.
func read(nand map[string][]byte, c Copy) []byte {
	out := bytes.Repeat([]byte{0xff}, c.Size)
	if dev := nand[c.Dev]; c.Offset < len(dev) {
		copy(out, dev[c.Offset:min(len(dev), c.Offset+c.Size)])
	}
	return out
}

// write erases the copy's sector and programs image at its start.
func write(nand map[string][]byte, c Copy, image []byte) {
	dev := nand[c.Dev]
	if end := c.Offset + c.Sector; len(dev) < end {
		dev = append(dev, bytes.Repeat([]byte{0xff}, end-len(dev))...)
	}
	copy(dev[c.Offset:c.Offset+c.Sector], bytes.Repeat([]byte{0xff}, c.Sector))
	copy(dev[c.Offset:], image)
	nand[c.Dev] = dev
}

// load is libuboot_load: the environment and, for the redundant one, which copy is current.
func load(nand map[string][]byte, copies []Copy) (*ubootenv.Env, int, byte, error) {
	if len(copies) == 1 {
		env, err := ubootenv.ParseLegacy(read(nand, copies[0]))
		return env, 0, 0, err
	}
	return ubootenv.Current(read(nand, copies[0]), read(nand, copies[1]))
}

// PrintEnv is fw_printenv over nand: it fails, as the real one does without a default
// environment file, when no copy checks out.
func PrintEnv(nand map[string][]byte, args []string) ([]byte, error) {
	copies, err := Config(args)
	if err != nil {
		return nil, err
	}
	env, _, _, err := load(nand, copies)
	if err != nil {
		return nil, err
	}
	return env.Text(), nil
}

// SetEnv is fw_setenv -s over nand.
func SetEnv(nand map[string][]byte, args []string) error {
	copies, err := Config(args)
	if err != nil {
		return err
	}
	env, current, flag, err := load(nand, copies)
	if err != nil {
		return err
	}
	script, err := os.ReadFile(flagValue(args, "-s"))
	if err != nil {
		return err
	}
	env.Apply(script)
	if len(copies) == 1 {
		image, err := env.EncodeLegacy()
		if err != nil {
			return err
		}
		write(nand, copies[0], image)
		return nil
	}
	image, err := env.EncodeCopy(flag + 1)
	if err != nil {
		return err
	}
	write(nand, copies[1-current], image)
	return nil
}

// Image places env in nand as a whole redundant environment, both copies, as a fresh write
// leaves it: dev5 is NAND.U-Boot-Env and dev4 NAND.U-Boot.
func Image(nand map[string][]byte, dev5, dev4 string, env *ubootenv.Env) error {
	redundant, err := env.EncodeCopy(ubootenv.FreshRedundantFlag)
	if err != nil {
		return err
	}
	primary, err := env.EncodeCopy(ubootenv.FreshPrimaryFlag)
	if err != nil {
		return err
	}
	write(nand, Copy{Dev: dev4, Offset: ubootenv.RedundantOffset, Size: ubootenv.Size, Sector: 0x20000}, redundant)
	write(nand, Copy{Dev: dev5, Size: ubootenv.Size, Sector: 0x20000}, primary)
	return nil
}

// Load reads the environment U-Boot would boot from nand: the redundant one when a copy
// checks out, the single legacy copy otherwise.
func Load(nand map[string][]byte, dev5, dev4 string) (*ubootenv.Env, error) {
	env, _, _, err := ubootenv.Current(read(nand, Copy{Dev: dev5, Size: ubootenv.Size}),
		read(nand, Copy{Dev: dev4, Offset: ubootenv.RedundantOffset, Size: ubootenv.Size}))
	if err == nil {
		return env, nil
	}
	return ubootenv.ParseLegacy(read(nand, Copy{Dev: dev5, Size: ubootenv.LegacySize}))
}

// NAND is MTD devices in memory with the commands soundtapd's environment code runs over
// them: fw_printenv and fw_setenv, and nanddump with -s and -l. Run and Read have the
// shapes of flash.Command and flash.Reader; Cmds is what Run ran.
type NAND struct {
	Devices map[string][]byte
	Cmds    []string
}

// Run is fw_setenv; any other command is recorded and does nothing.
func (n *NAND) Run(_ io.Reader, name string, args ...string) error {
	n.Cmds = append(n.Cmds, name+" "+strings.Join(args, " "))
	if name == "fw_setenv" {
		return SetEnv(n.Devices, args)
	}
	return nil
}

// Read is fw_printenv or nanddump; flash nothing was written to reads erased.
func (n *NAND) Read(name string, args ...string) ([]byte, error) {
	if name == "fw_printenv" {
		return PrintEnv(n.Devices, args)
	}
	if name != "nanddump" || len(args) == 0 {
		return nil, fmt.Errorf("%s: not faked", name)
	}
	off, _ := strconv.Atoi(flag(args, "-s"))
	size, _ := strconv.Atoi(flag(args, "-l"))
	return read(n.Devices, Copy{Dev: args[len(args)-1], Offset: off, Size: size}), nil
}

func flag(args []string, name string) string { return flagValue(args, name) }

func flagValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}
