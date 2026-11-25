// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package atomicjson_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

var errUploadCutOff = errors.New("upload cut off")

func TestWriteRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")

	want := map[string]int{"a": 1, "b": 2}

	err := atomicjson.Write(path, want, 0o600)
	if err != nil {
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

	err = json.Unmarshal(data, &got)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["a"] != 1 || got["b"] != 2 {
		t.Errorf("round-trip = %v, want a:1 b:2", got)
	}
}

func TestWriteDoesNotFollowTempSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	victim := filepath.Join(dir, "victim")

	err := os.WriteFile(victim, []byte("keep"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(victim, path+".tmp")
	if err != nil {
		t.Fatal(err)
	}

	err = atomicjson.Write(path, map[string]int{"safe": 1}, 0o600)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := os.ReadFile(victim)
	if err != nil || string(got) != "keep" {
		t.Fatalf("victim = %q, %v; want unchanged", got, err)
	}

	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("target = %v, %v; want regular file", info, err)
	}
}

// A stream that fails leaves the old file and no temporary one; one that works
// replaces it with the given mode.
func TestWriteStreamReplacesOnlyOnSuccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	path := filepath.Join(dir, "app.gz")

	err := os.WriteFile(path, []byte("old"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = atomicjson.WriteStream(path, 0o644, func(w io.Writer) error {
		_, _ = io.WriteString(w, "half")

		return errUploadCutOff
	})
	if !errors.Is(err, errUploadCutOff) {
		t.Fatalf("WriteStream = %v", err)
	}

	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Fatalf("a failed stream left %q", got)
	}

	if left, _ := filepath.Glob(filepath.Join(dir, ".app.gz.tmp-*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}

	err = atomicjson.WriteStream(path, 0o755, func(w io.Writer) error {
		_, werr := io.WriteString(w, "new")
		if werr != nil {
			return fmt.Errorf("write: %w", werr)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if got, _ := os.ReadFile(path); string(got) != "new" || err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("content %q, mode %v, err %v", got, info.Mode(), err)
	}
}
