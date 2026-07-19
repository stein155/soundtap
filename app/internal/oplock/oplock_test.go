// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oplock

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLocksExcludeEachOtherThroughTheFile(t *testing.T) {
	Path = filepath.Join(t.TempDir(), "run", "operation.lock")
	defer func() { Path = "" }()
	var a, b Lock
	if !a.TryLock() {
		t.Fatal("first TryLock failed")
	}
	if b.TryLock() {
		t.Fatal("second Lock got the file while the first held it")
	}
	a.Unlock()
	if !b.TryLock() {
		t.Fatal("TryLock failed after Unlock")
	}
	b.Unlock()
}

func TestChildKeepsTheLockAfterUnlock(t *testing.T) {
	Path = filepath.Join(t.TempDir(), "operation.lock")
	defer func() { Path = "" }()
	var a, b Lock
	if !a.TryLock() {
		t.Fatal("TryLock failed")
	}
	// A child holding the inherited descriptor stands in for a nandwrite that outlives soundtapd.
	cmd := exec.Command("sleep", "1")
	cmd.ExtraFiles = append(cmd.ExtraFiles, a.File())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	a.Unlock()
	if b.TryLock() {
		t.Fatal("lock free while the child still holds it")
	}
	_ = cmd.Wait()
	if !b.TryLock() {
		t.Fatal("lock still taken after the child exited")
	}
	b.Unlock()
}

// A lock file that cannot be opened must not drop the exclusion: separate Locks share
// the process-wide fallback instead.
func TestLocksExcludeEachOtherWhenTheFileCannotBeOpened(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	Path = filepath.Join(notADir, "operation.lock")
	defer func() { Path = "" }()
	var a, b Lock
	if !a.TryLock() {
		t.Fatal("first TryLock failed")
	}
	if b.TryLock() {
		t.Fatal("second Lock got in while the first held the fallback")
	}
	a.Unlock()
	if !b.TryLock() {
		t.Fatal("TryLock failed after Unlock")
	}
	b.Unlock()
}
