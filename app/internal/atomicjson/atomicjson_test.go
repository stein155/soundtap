// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package atomicjson

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := map[string]int{"a": 1, "b": 2}
	if err := Write(path, want, 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["a"] != 1 || got["b"] != 2 {
		t.Errorf("round-trip = %v, want a:1 b:2", got)
	}
}

func TestWriteDoesNotFollowTempSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, map[string]int{"safe": 1}, 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "keep" {
		t.Fatalf("victim = %q, %v; want unchanged", got, err)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("target = %v, %v; want regular file", info, err)
	}
}

// A stream that fails leaves the old file and no temporary one; one that works
// replaces it with the given mode.
func TestWriteStreamReplacesOnlyOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.gz")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteStream(path, 0o644, func(w io.Writer) error {
		_, _ = io.WriteString(w, "half")
		return errors.New("upload cut off")
	})
	if err == nil || err.Error() != "upload cut off" {
		t.Fatalf("WriteStream = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatalf("a failed stream left %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".app.gz.tmp-*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
	if err := WriteStream(path, 0o755, func(w io.Writer) error {
		_, err := io.WriteString(w, "new")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if got, _ := os.ReadFile(path); string(got) != "new" || err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("content %q, mode %v, err %v", got, info.Mode(), err)
	}
}
