// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// A speaker that ran a build before 0.1.0 may hold allow-unsigned, which nothing reads any
// more (ADR 0048). The first start of this build removes it from the previous release's
// state and leaves every other file of it as it was (ADR 0120).
func TestRetiredFilesGoAndTheRestStays(t *testing.T) {
	t.Parallel()

	set := filepath.Join(nvFixtures, nvCurrent)
	nvDir := t.TempDir()
	must(t, os.CopyFS(nvDir, os.DirFS(set)))
	home := filepath.Join(nvDir, "soundtap")
	must(t, os.WriteFile(filepath.Join(home, "allow-unsigned"), nil, 0o600))

	must(t, removeRetiredFiles(home))
	must(t, removeRetiredFiles(home)) // the next start finds nothing to remove

	_, err := os.Stat(filepath.Join(home, "allow-unsigned"))
	if !os.IsNotExist(err) {
		t.Fatalf("allow-unsigned remains: %v", err)
	}

	must(t, filepath.WalkDir(set, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(set, path)
		want, _ := os.ReadFile(path)

		got, err := os.ReadFile(filepath.Join(nvDir, rel))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s changed: %v", rel, err)
		}

		return nil
	}))
}
