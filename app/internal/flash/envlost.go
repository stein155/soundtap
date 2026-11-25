// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"errors"
	"fmt"

	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// errBadBlock marks an environment copy whose erase block is bad. Each copy owns the last
// (or only) erase block of its partition, so nanddump --bb=skipbad skips it and reads
// nothing.
var errBadBlock = errors.New("the erase block is bad")

// errBothBad means neither environment copy has a good erase block to be written to.
var errBothBad = errors.New("sit on bad erase blocks")

// envCopy is where one copy of the redundant environment lives.
type envCopy struct{ mtd, offset int }

func (in *Installer) envCopy(which int) envCopy {
	if which == ubootenv.Primary {
		return envCopy{in.Target.EnvMTD, 0}
	}

	return envCopy{ubootMTD, ubootenv.RedundantOffset}
}

// lostCopy is the copy of the redundant environment that sits on a bad erase block, or -1
// when both read. A bad block under either copy loses that copy, not the environment
// (ADR 0010): U-Boot boots the other copy, and a save that fails on the lost one writes the
// other. libubootenv refuses the whole environment when one copy cannot be read, so on such
// a board soundtapd reads and writes the copy left itself. Both lost is an error: nothing
// is left for U-Boot but its built-in bootcmd.
func (in *Installer) lostCopy(read Reader) (int, error) {
	if !in.Target.EnvRedundant || in.Target.EnvMTD < 0 {
		return -1, nil
	}

	_, lost, err := in.readCopies(read)

	return lost, err
}

// readCopies dumps both copies of the redundant environment, indexed by ubootenv.Primary
// and ubootenv.Redundant, and names the one lost to a bad block (-1 for none), which stays
// empty and so checks out as no copy.
func (in *Installer) readCopies(read Reader) ([2][]byte, int, error) {
	var copies [2][]byte

	lost := -1

	for which := range copies {
		c := in.envCopy(which)

		raw, err := in.dumpEnv(read, c.mtd, c.offset, ubootenv.Size)
		switch {
		case errors.Is(err, errBadBlock) && lost >= 0:
			return copies, -1, fmt.Errorf("both copies of %s %w", envPart, errBothBad)
		case errors.Is(err, errBadBlock):
			lost = which
		case err != nil:
			return copies, -1, err
		default: // the copy was read
		}

		copies[which] = raw
	}

	return copies, lost, nil
}

// lostBehind is the copy lost to a bad block when that is why fw_printenv refused the
// environment (readErr), and -1 otherwise: a board with both copies reads through
// libubootenv alone.
func (in *Installer) lostBehind(read Reader, readErr error) int {
	if readErr == nil {
		return -1
	}

	lost, err := in.lostCopy(read)
	if err != nil {
		return -1
	}

	return lost
}

// remainingEnv reads the copy left when the other is lost, and its flag. Before the first
// write converts it, the primary may still be the single 8 KiB copy, which our U-Boot reads
// when no redundant copy checks out (CONFIG_ENV_REDUNDANT_UPGRADE); its flag is then 0.
func (in *Installer) remainingEnv(read Reader, lost int) (*ubootenv.Env, byte, error) {
	left := 1 - lost // Primary and Redundant are 0 and 1
	c := in.envCopy(left)

	raw, err := in.dumpEnv(read, c.mtd, c.offset, ubootenv.Size)
	if err != nil {
		return nil, 0, err
	}

	env, flag, err := ubootenv.ParseCopy(raw)
	if err == nil {
		return env, flag, nil
	}

	if left == ubootenv.Primary {
		single, singleErr := ubootenv.ParseLegacy(raw)
		if singleErr == nil {
			return single, 0, nil
		}
	}

	return nil, 0, fmt.Errorf("reading %s, one copy lost to a bad block: %w", envPart, err)
}

// rewriteRemaining is rewriteEnv on a board with one copy lost: it writes the copy left
// whole, with the next flag, in the redundant format, and reads it back. It writes nothing
// when edit changes nothing. With one copy there is no older one to fall back on: a cut
// between the erase and the write leaves U-Boot its built-in bootcmd, which boots either
// slot, until RepairEnv writes the copy again.
func (in *Installer) rewriteRemaining(read Reader, lost int, edit func(*ubootenv.Env) (bool, error)) error {
	current, flag, err := in.remainingEnv(read, lost)
	if err != nil {
		return err
	}

	env := current.Clone()

	changed, err := edit(env)
	if err != nil || !changed || env.Equal(current) {
		return err
	}

	image, err := env.EncodeCopy(flag + 1)
	if err != nil {
		return fmt.Errorf("encoding the copy left: %w", err)
	}

	left := in.envCopy(1 - lost)

	return in.writeEnvCopy(read, left.mtd, left.offset, image)
}
