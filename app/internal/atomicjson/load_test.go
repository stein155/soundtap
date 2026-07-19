// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package atomicjson

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileKeepsDefaults(t *testing.T) {
	v := []string{"default"}
	if err := Load(filepath.Join(t.TempDir(), "absent.json"), &v); err != nil {
		t.Fatalf("Load = %v, want nil for a missing file", err)
	}
	if len(v) != 1 || v[0] != "default" {
		t.Fatalf("v = %v, want the defaults untouched", v)
	}
}

func TestLoadReadsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, []string{"a", "b"}, 0o600); err != nil {
		t.Fatal(err)
	}
	var v []string
	if err := Load(path, &v); err != nil || len(v) != 2 || v[1] != "b" {
		t.Fatalf("Load = %v, %v; want [a b]", v, err)
	}
}

// A directory where the file should be reads as EISDIR: not "no file", so the
// caller must not start over on defaults it would then write.
func TestReadDirectoryIsUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Read(path); !errors.Is(err, ErrUnreadable) || found {
		t.Fatalf("Read = found %v, %v; want ErrUnreadable", found, err)
	}
	v := []string{"default"}
	if err := Load(path, &v); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("Load = %v, want ErrUnreadable", err)
	}
	if len(v) != 1 || v[0] != "default" {
		t.Fatalf("v = %v, want the defaults untouched", v)
	}
	if _, err := os.Stat(path + ".corrupt"); !os.IsNotExist(err) {
		t.Fatalf("an unreadable file was moved aside: %v", err)
	}
}

func TestReadWithoutPermissionIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`["kept"]`), 0o000); err != nil {
		t.Fatal(err)
	}
	var v []string
	if err := Load(path, &v); !errors.Is(err, ErrUnreadable) || errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load = %v, want ErrUnreadable only", err)
	}
}

func TestLoadQuarantinesCorruptFileAndKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	for _, bad := range []string{"first", `["second"`, `{"third":1}`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		v := []string{"default"}
		err := Load(path, &v)
		if !errors.Is(err, ErrCorrupt) || errors.Is(err, ErrUnreadable) {
			t.Fatalf("Load(%q) = %v, want ErrCorrupt only", bad, err)
		}
		if len(v) != 1 || v[0] != "default" {
			t.Fatalf("Load(%q) left v = %v, want the defaults", bad, v)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt file left in place: %v", err)
	}
	for file, want := range map[string]string{
		path + ".corrupt":   `{"third":1}`,
		path + ".corrupt.1": `["second"`,
	} {
		if got, err := os.ReadFile(file); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", file, got, err, want)
		}
	}
	if err := RemoveQuarantine(path); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(path + ".corrupt*"); len(left) != 0 {
		t.Fatalf("RemoveQuarantine left %v", left)
	}
}

// A corrupt file that cannot be moved aside is still the only copy: the next write
// would destroy it, so the caller is told to refuse writes.
func TestCorruptFileThatCannotBeMovedIsUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Both quarantine names are non-empty directories, so neither rename can succeed.
	for _, dir := range []string{path + ".corrupt", path + ".corrupt.1"} {
		if err := os.MkdirAll(filepath.Join(dir, "x"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var v []string
	if err := Load(path, &v); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("Load = %v, want ErrUnreadable", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "{broken" {
		t.Fatalf("file = %q, %v; want it in place", got, err)
	}
}
