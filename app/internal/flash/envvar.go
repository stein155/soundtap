// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"errors"
	"fmt"
	"os"

	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// EnvValue reads one entry of the environment through fw_printenv: its value, and ""
// when the entry is not there.
func (in *Installer) EnvValue(read Reader, key string) (string, error) {
	env, err := in.Env(read)
	if err != nil {
		return "", err
	}
	v, _ := env.Get(key)
	return v, nil
}

// SetEnvEverywhere sets key to value, or removes key when value is "", in every copy of
// the environment, through fw_setenv as every other write (ADR 0010), and checks each copy
// read back from NAND.
//
// fw_setenv writes the redundant environment one copy at a time, the one that is not
// current, so one write leaves the older copy as it was. U-Boot boots that older copy when
// the newer one is torn: an entry set once would be gone then, and an entry removed once
// would come back. For an entry whose absence and presence both matter, the owner key
// (ADR 0048), the same change is written a second time, which lands in the other copy, and
// both copies are then read with nanddump and must say the same.
func (in *Installer) SetEnvEverywhere(read Reader, key, value string) error {
	edit := func(env *ubootenv.Env) (bool, error) {
		old, had := env.Get(key)
		if value == "" {
			env.Delete(key)
			return had, nil
		}
		env.Set(key, value)
		return !had || old != value, nil
	}
	if err := in.rewriteEnv(read, edit); err != nil {
		return err
	}
	if !in.Target.EnvRedundant {
		return nil // one copy, which rewriteEnv read back
	}
	agree, err := in.copiesSay(read, key, value)
	if err != nil || agree {
		return err
	}
	if err := in.storeAgain(read, key, value); err != nil {
		return err
	}
	if agree, err = in.copiesSay(read, key, value); err == nil && !agree {
		err = fmt.Errorf("%s: the two environment copies still disagree on %s", envPart, key)
	}
	return err
}

// copiesSay reports whether both copies of the redundant environment check out and hold
// key at value ("" for absent). A copy lost to a bad block holds nothing to disagree:
// rewriteEnv wrote the one left and read it back.
func (in *Installer) copiesSay(read Reader, key, value string) (bool, error) {
	for _, which := range []int{ubootenv.Primary, ubootenv.Redundant} {
		c := in.envCopy(which)
		raw, err := in.dumpEnv(read, c.mtd, c.offset, ubootenv.Size)
		if errors.Is(err, errBadBlock) {
			continue
		}
		if err != nil || !copyHolds(raw, key, value) {
			return false, err
		}
	}
	return true, nil
}

// copyHolds reports whether raw is a copy that checks out with key at value. A copy that
// does not check out yet does not: the next write makes it.
func copyHolds(raw []byte, key, value string) bool {
	env, _, err := ubootenv.ParseCopy(raw)
	if err != nil {
		return false
	}
	v, _ := env.Get(key)
	return v == value
}

// storeAgain runs fw_setenv with key's line once more. libubootenv stores the whole
// environment on every run, into the copy that is not current, whether or not a value
// changed; rewriteEnv skips a write that changes nothing, which is right everywhere else.
func (in *Installer) storeAgain(read Reader, key, value string) error {
	cfg, err := in.envConfig()
	if err != nil {
		return err
	}
	defer os.Remove(cfg)
	staged, err := os.CreateTemp("", "soundtap-env-")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	if _, err := fmt.Fprintf(staged, "%s=%s\n", key, value); err != nil {
		return err
	}
	if err := staged.Sync(); err != nil {
		return err
	}
	if err := in.Run(nil, "fw_setenv", "-c", cfg, "-s", staged.Name()); err != nil {
		return err
	}
	back, err := in.readEnv(read, cfg)
	if err != nil {
		return fmt.Errorf("reading %s back: %w", envPart, err)
	}
	if v, _ := back.Get(key); v != value {
		return fmt.Errorf("%s readback verification failed", envPart)
	}
	return nil
}
