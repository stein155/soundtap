// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"os"
	"path/filepath"
	"testing"
)

// A reported MemAvailable of 0 is kept: only a kernel without the line falls back to
// MemFree.
func TestReadMeminfoKeepsZeroAvailable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(dir, name)

		err := os.WriteFile(path, []byte(text), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		return path
	}

	zero := readMeminfo(write("zero", "MemTotal: 131072 kB\nMemFree: 12288 kB\nMemAvailable: 0 kB\n"))
	if zero.AvailableBytes != 0 {
		t.Fatalf("MemAvailable 0 became %d", zero.AvailableBytes)
	}

	old := readMeminfo(write("old", "MemTotal: 131072 kB\nMemFree: 12288 kB\n"))
	if old.AvailableBytes != 12288<<10 {
		t.Fatalf("without MemAvailable want MemFree, got %d", old.AvailableBytes)
	}
}
