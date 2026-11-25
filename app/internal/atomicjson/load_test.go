// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package atomicjson_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

func TestLoadMissingFileKeepsDefaults(t *testing.T) {
	t.Parallel()

	value := []string{"default"}

	err := atomicjson.Load(filepath.Join(t.TempDir(), "absent.json"), &value)
	if err != nil {
		t.Fatalf("Load = %v, want nil for a missing file", err)
	}

	if len(value) != 1 || value[0] != "default" {
		t.Fatalf("value = %v, want the defaults untouched", value)
	}
}

func TestLoadReadsAFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")

	err := atomicjson.Write(path, []string{"a", "b"}, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var value []string

	err = atomicjson.Load(path, &value)
	if err != nil || len(value) != 2 || value[1] != "b" {
		t.Fatalf("Load = %v, %v; want [a b]", value, err)
	}
}

// A directory where the file should be reads as EISDIR: not "no file", so the
// caller must not start over on defaults it would then write.
func TestReadDirectoryIsUnreadable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")

	err := os.Mkdir(path, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	_, found, err := atomicjson.Read(path)
	if !errors.Is(err, atomicjson.ErrUnreadable) || found {
		t.Fatalf("Read = found %v, %v; want ErrUnreadable", found, err)
	}

	value := []string{"default"}

	err = atomicjson.Load(path, &value)
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Load = %v, want ErrUnreadable", err)
	}

	if len(value) != 1 || value[0] != "default" {
		t.Fatalf("value = %v, want the defaults untouched", value)
	}

	_, err = os.Stat(path + ".corrupt")
	if !os.IsNotExist(err) {
		t.Fatalf("an unreadable file was moved aside: %v", err)
	}
}

func TestReadWithoutPermissionIsUnreadable(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}

	path := filepath.Join(t.TempDir(), "state.json")

	err := os.WriteFile(path, []byte(`["kept"]`), 0o000)
	if err != nil {
		t.Fatal(err)
	}

	var value []string

	err = atomicjson.Load(path, &value)
	if !errors.Is(err, atomicjson.ErrUnreadable) || errors.Is(err, atomicjson.ErrCorrupt) {
		t.Fatalf("Load = %v, want ErrUnreadable only", err)
	}
}

func TestLoadQuarantinesCorruptFileAndKeepsDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	for _, bad := range []string{"first", `["second"`, `{"third":1}`} {
		err := os.WriteFile(path, []byte(bad), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		value := []string{"default"}

		err = atomicjson.Load(path, &value)
		if !errors.Is(err, atomicjson.ErrCorrupt) || errors.Is(err, atomicjson.ErrUnreadable) {
			t.Fatalf("Load(%q) = %v, want ErrCorrupt only", bad, err)
		}

		if len(value) != 1 || value[0] != "default" {
			t.Fatalf("Load(%q) left value = %v, want the defaults", bad, value)
		}
	}

	_, err := os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("corrupt file left in place: %v", err)
	}

	for file, want := range map[string]string{
		path + ".corrupt":   `{"third":1}`,
		path + ".corrupt.1": `["second"`,
	} {
		got, rerr := os.ReadFile(file)
		if rerr != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", file, got, rerr, want)
		}
	}

	err = atomicjson.RemoveQuarantine(path)
	if err != nil {
		t.Fatal(err)
	}

	if left, _ := filepath.Glob(path + ".corrupt*"); len(left) != 0 {
		t.Fatalf("RemoveQuarantine left %v", left)
	}
}

// A corrupt file that cannot be moved aside is still the only copy: the next write
// would destroy it, so the caller is told to refuse writes.
func TestCorruptFileThatCannotBeMovedIsUnreadable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")

	err := os.WriteFile(path, []byte("{broken"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// Both quarantine names are non-empty directories, so neither rename can succeed.
	for _, dir := range []string{path + ".corrupt", path + ".corrupt.1"} {
		mkErr := os.MkdirAll(filepath.Join(dir, "x"), 0o700)
		if mkErr != nil {
			t.Fatal(mkErr)
		}
	}

	var value []string

	err = atomicjson.Load(path, &value)
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Load = %v, want ErrUnreadable", err)
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "{broken" {
		t.Fatalf("file = %q, %v; want it in place", got, err)
	}
}
