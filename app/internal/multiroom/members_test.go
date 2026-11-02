// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multiroom-enabled")
	if err := PersistActive(path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := PersistActive(path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker still exists: %v", err)
	}
}
