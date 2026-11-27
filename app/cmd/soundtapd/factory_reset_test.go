// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// A power cut between an atomic write's temporary file and its rename leaves that file on
// /mnt/nv for good. The next start removes every such file in the directories soundtapd
// writes, and leaves the previous release's state as it was (ADR 0120), its dot files too.
func TestInterruptedWritesGoAtStart(t *testing.T) {
	t.Parallel()

	set := filepath.Join(nvFixtures, nvCurrent)
	nvDir := t.TempDir()
	must(t, os.CopyFS(nvDir, os.DirFS(set)))
	home := filepath.Join(nvDir, "soundtap")

	must(t, os.MkdirAll(filepath.Join(home, "snapserver"), 0o700))
	must(t, os.MkdirAll(filepath.Join(home, "codec"), 0o755))

	orphans := []string{
		filepath.Join(home, ".soundtapd.override.gz.tmp-2718281828"),
		filepath.Join(home, ".presets.json.settings.tmp-123456"),
		filepath.Join(nvDir, ".wifi.conf.known.tmp-42"),
		filepath.Join(home, "snapserver", ".server.json.tmp-7"),
		filepath.Join(home, "codec", ".codec.tmp-9"),
	}
	for _, orphan := range orphans {
		must(t, os.WriteFile(orphan, []byte("interrupted"), 0o600))
	}

	dotFile := filepath.Join(home, ".notes")
	must(t, os.WriteFile(dotFile, nil, 0o600))

	cleanAtomicTemps(home, slog.New(slog.DiscardHandler))

	for _, orphan := range orphans {
		_, err := os.Stat(orphan)
		if !os.IsNotExist(err) {
			t.Errorf("%s remains: %v", orphan, err)
		}
	}

	_, err := os.Stat(dotFile)
	if err != nil {
		t.Errorf("a dot file that is no temporary file went: %v", err)
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
