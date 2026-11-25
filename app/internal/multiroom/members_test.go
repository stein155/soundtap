// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/multiroom"
)

func TestPersistActive(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "multiroom-enabled")

	err := multiroom.PersistActive(path, true)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	err = multiroom.PersistActive(path, false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("marker still exists: %v", err)
	}
}
