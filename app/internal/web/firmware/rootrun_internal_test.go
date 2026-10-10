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

// With soundtapd as its own user every NAND command of the lane goes to soundtap-root, with
// the operations lock the command would have held here (ADR 0066), and its refusal is the
// command's error.
func TestFlashRunGoesThroughTheRootHelper(t *testing.T) {
	s := New(nil, t.TempDir())
	if !s.firmware.mu.TryLock() {
		t.Fatal("lock taken")
	}
	defer s.firmware.mu.Unlock()
	var got []string
	var lock *os.File
	refuse := errors.New("refused")
	s.SetRootRun(func(l *os.File, _ io.Reader, name string, args ...string) error {
		lock = l
		got = append(got, name+" "+strings.Join(args, " "))
		if name == "nandwrite" {
			return refuse
		}
		return nil
	})
	if err := s.flashRun(nil, "flash_erase", "/dev/mtd7", "0", "0"); err != nil {
		t.Fatal(err)
	}
	if err := s.flashRun(nil, "nandwrite", "-p", "/dev/mtd8", "/tmp/k"); !errors.Is(err, refuse) {
		t.Errorf("err = %v, want the helper's refusal", err)
	}
	if len(got) != 2 || got[0] != "flash_erase /dev/mtd7 0 0" {
		t.Errorf("ran %q", got)
	}
	if lock != s.firmware.mu.File() {
		t.Error("the operations lock was not passed along")
	}
}
