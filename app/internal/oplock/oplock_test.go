// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oplock_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/oplock"
)

func TestLocksExcludeEachOtherThroughTheFile(t *testing.T) { //nolint:paralleltest // swaps oplock.Path
	oplock.Path = filepath.Join(t.TempDir(), "run", "operation.lock")
	defer func() { oplock.Path = "" }()

	var first, second oplock.Lock
	if !first.TryLock() {
		t.Fatal("first TryLock failed")
	}
	if second.TryLock() {
		t.Fatal("second Lock got the file while the first held it")
	}
	first.Unlock()

	if !second.TryLock() {
		t.Fatal("TryLock failed after Unlock")
	}
	second.Unlock()
}

func TestChildKeepsTheLockAfterUnlock(t *testing.T) { //nolint:paralleltest // swaps oplock.Path
	oplock.Path = filepath.Join(t.TempDir(), "operation.lock")
	defer func() { oplock.Path = "" }()

	var first, second oplock.Lock
	if !first.TryLock() {
		t.Fatal("TryLock failed")
	}
	// A child holding the inherited descriptor stands in for a nandwrite that outlives soundtapd.
	cmd := exec.CommandContext(t.Context(), "sleep", "1")

	cmd.ExtraFiles = append(cmd.ExtraFiles, first.File())

	err := cmd.Start()
	if err != nil {
		t.Fatal(err)
	}
	first.Unlock()

	if second.TryLock() {
		t.Fatal("lock free while the child still holds it")
	}
	_ = cmd.Wait()

	if !second.TryLock() {
		t.Fatal("lock still taken after the child exited")
	}
	second.Unlock()
}

// A lock file that cannot be opened must not drop the exclusion: separate Locks share
// the process-wide fallback instead.
//
//nolint:paralleltest // swaps oplock.Path
func TestLocksExcludeEachOtherWhenTheFileCannotBeOpened(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")

	err := os.WriteFile(notADir, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	oplock.Path = filepath.Join(notADir, "operation.lock")
	defer func() { oplock.Path = "" }()

	var first, second oplock.Lock
	if !first.TryLock() {
		t.Fatal("first TryLock failed")
	}
	if second.TryLock() {
		t.Fatal("second Lock got in while the first held the fallback")
	}
	first.Unlock()

	if !second.TryLock() {
		t.Fatal("TryLock failed after Unlock")
	}
	second.Unlock()
}
