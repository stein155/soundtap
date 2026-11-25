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

// eraseBlock is the 128 KiB NAND erase block each copy of the environment lives in.
const eraseBlock = 0x20000

// configFields is a fw_env.config line's device, offset, size and sector.
const configFields = 4

// The refusals of the fake tools, as their messages read.
var (
	errNoConfig   = errors.New("no -c config")
	errConfigLine = errors.New("has no device, offset, size and sector")
	errCopyCount  = errors.New("copies")
	errNotFaked   = errors.New("not faked")
)

// Copy is one line of a fw_env.config: the device, where the copy starts in it, its size
// and the erase block it lives in.
type Copy struct {
	Dev                  string
	Offset, Size, Sector int
}

// Config reads the -c config passed to fw_printenv or fw_setenv.
func Config(args []string) ([]Copy, error) {
	cfg := flagValue(args, "-c")
	if cfg == "" {
		return nil, errNoConfig
	}

	raw, err := os.ReadFile(cfg)
	if err != nil {
		return nil, fmt.Errorf("reading the config: %w", err)
	}

	var copies []Copy

	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < configFields {
			return nil, fmt.Errorf("config line %q %w", line, errConfigLine)
		}

		place := Copy{Dev: fields[0], Offset: 0, Size: 0, Sector: 0}
		for i, field := range []*int{&place.Offset, &place.Size, &place.Sector} {
			n, err := strconv.ParseInt(fields[i+1], 0, 64)
			if err != nil {
				return nil, fmt.Errorf("config line %q: %w", line, err)
			}

			*field = int(n)
		}

		copies = append(copies, place)
	}

	if len(copies) == 0 || len(copies) > 2 {
		return nil, fmt.Errorf("config %q holds %d %w", raw, len(copies), errCopyCount)
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
func read(nand map[string][]byte, place Copy) []byte {
	out := bytes.Repeat([]byte{0xff}, place.Size)
	if dev := nand[place.Dev]; place.Offset < len(dev) {
		copy(out, dev[place.Offset:min(len(dev), place.Offset+place.Size)])
	}

	return out
}

// write erases the copy's sector and programs image at its start.
func write(nand map[string][]byte, place Copy, image []byte) {
	dev := nand[place.Dev]
	if end := place.Offset + place.Sector; len(dev) < end {
		dev = append(dev, bytes.Repeat([]byte{0xff}, end-len(dev))...)
	}

	copy(dev[place.Offset:place.Offset+place.Sector], bytes.Repeat([]byte{0xff}, place.Sector))
	copy(dev[place.Offset:], image)
	nand[place.Dev] = dev
}

// loaded is what libuboot_load finds: the environment and, for the redundant one, which
// copy is current and its flag.
type loaded struct {
	env     *ubootenv.Env
	current int
	flag    byte
}

// loadCopies is libuboot_load.
func loadCopies(nand map[string][]byte, copies []Copy) (loaded, error) {
	if len(copies) == 1 {
		env, err := ubootenv.ParseLegacy(read(nand, copies[0]))
		if err != nil {
			return loaded{env: nil, current: 0, flag: 0}, fmt.Errorf("the single copy: %w", err)
		}

		return loaded{env: env, current: 0, flag: 0}, nil
	}

	env, current, flag, err := ubootenv.Current(read(nand, copies[0]), read(nand, copies[1]))
	if err != nil {
		return loaded{env: nil, current: 0, flag: 0}, fmt.Errorf("the redundant environment: %w", err)
	}

	return loaded{env: env, current: current, flag: flag}, nil
}

// PrintEnv is fw_printenv over nand: it fails, as the real one does without a default
// environment file, when no copy checks out.
func PrintEnv(nand map[string][]byte, args []string) ([]byte, error) {
	copies, err := Config(args)
	if err != nil {
		return nil, err
	}

	found, err := loadCopies(nand, copies)
	if err != nil {
		return nil, err
	}

	return found.env.Text(), nil
}

// SetEnv is fw_setenv -s over nand.
func SetEnv(nand map[string][]byte, args []string) error {
	copies, err := Config(args)
	if err != nil {
		return err
	}

	found, err := loadCopies(nand, copies)
	if err != nil {
		return err
	}

	script, err := os.ReadFile(flagValue(args, "-s"))
	if err != nil {
		return fmt.Errorf("reading the script: %w", err)
	}

	found.env.Apply(script)

	if len(copies) == 1 {
		image, encodeErr := found.env.EncodeLegacy()
		if encodeErr != nil {
			return fmt.Errorf("encoding the single copy: %w", encodeErr)
		}

		write(nand, copies[0], image)

		return nil
	}

	image, err := found.env.EncodeCopy(found.flag + 1)
	if err != nil {
		return fmt.Errorf("encoding a copy: %w", err)
	}

	write(nand, copies[1-found.current], image)

	return nil
}

// Image places env in nand as a whole redundant environment, both copies, as a fresh write
// leaves it: dev5 is NAND.U-Boot-Env and dev4 NAND.U-Boot.
func Image(nand map[string][]byte, dev5, dev4 string, env *ubootenv.Env) error {
	redundant, err := env.EncodeCopy(ubootenv.FreshRedundantFlag)
	if err != nil {
		return fmt.Errorf("encoding the redundant copy: %w", err)
	}

	primary, err := env.EncodeCopy(ubootenv.FreshPrimaryFlag)
	if err != nil {
		return fmt.Errorf("encoding the primary copy: %w", err)
	}

	write(nand, Copy{Dev: dev4, Offset: ubootenv.RedundantOffset, Size: ubootenv.Size, Sector: eraseBlock}, redundant)
	write(nand, Copy{Dev: dev5, Offset: 0, Size: ubootenv.Size, Sector: eraseBlock}, primary)

	return nil
}

// Load reads the environment U-Boot would boot from nand: the redundant one when a copy
// checks out, the single legacy copy otherwise.
func Load(nand map[string][]byte, dev5, dev4 string) (*ubootenv.Env, error) {
	env, _, _, err := ubootenv.Current(read(nand, Copy{Dev: dev5, Offset: 0, Size: ubootenv.Size, Sector: 0}),
		read(nand, Copy{Dev: dev4, Offset: ubootenv.RedundantOffset, Size: ubootenv.Size, Sector: 0}))
	if err == nil {
		return env, nil
	}

	env, err = ubootenv.ParseLegacy(read(nand, Copy{Dev: dev5, Offset: 0, Size: ubootenv.LegacySize, Sector: 0}))
	if err != nil {
		return nil, fmt.Errorf("neither the redundant environment nor the single copy: %w", err)
	}

	return env, nil
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
		return nil, fmt.Errorf("%s: %w", name, errNotFaked)
	}

	off, _ := strconv.Atoi(flagValue(args, "-s"))
	size, _ := strconv.Atoi(flagValue(args, "-l"))

	return read(n.Devices, Copy{Dev: args[len(args)-1], Offset: off, Size: size, Sector: 0}), nil
}

func flagValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}

	return ""
}
