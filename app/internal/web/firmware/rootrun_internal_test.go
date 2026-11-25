// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// errRefused is soundtap-root refusing a command.
var errRefused = errors.New("refused")

// With soundtapd as its own user every NAND command of the lane goes to soundtap-root, with
// the operations lock the command would have held here (ADR 0066), and its refusal is the
// command's error.
func TestFlashRunGoesThroughTheRootHelper(t *testing.T) {
	t.Parallel()

	handler := New(nil, t.TempDir())
	if !handler.firmware.mu.TryLock() {
		t.Fatal("lock taken")
	}
	defer handler.firmware.mu.Unlock()

	var (
		got  []string
		lock *os.File
	)

	refuse := errRefused

	handler.SetRootRun(func(l *os.File, _ io.Reader, name string, args ...string) error {
		lock = l

		got = append(got, name+" "+strings.Join(args, " "))
		if name == "nandwrite" {
			return refuse
		}

		return nil
	})

	err := handler.flashRun(nil, "flash_erase", "/dev/mtd7", "0", "0")
	if err != nil {
		t.Fatal(err)
	}

	err = handler.flashRun(nil, "nandwrite", "-p", "/dev/mtd8", "/tmp/k")
	if !errors.Is(err, refuse) {
		t.Errorf("err = %v, want the helper's refusal", err)
	}

	if len(got) != 2 || got[0] != "flash_erase /dev/mtd7 0 0" {
		t.Errorf("ran %q", got)
	}

	if lock != handler.firmware.mu.File() {
		t.Error("the operations lock was not passed along")
	}
}
