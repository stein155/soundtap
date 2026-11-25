// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/store"
)

func TestSetGetAll(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "presets.json")

	presets, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if got := presets.All(); len(got) != 0 {
		t.Fatalf("fresh store All() = %v, want empty", got)
	}

	if _, ok := presets.Get(1); ok {
		t.Fatal("Get(1) on empty store returned ok=true")
	}

	want := []store.Preset{
		preset(3, "s31", "Radio 3"),
		presetWithLogo(1, "s10", "Radio 1", "l1.png"),
		preset(6, "s60", "Radio 6"),
	}
	for _, entry := range want {
		err = presets.Set(entry)
		if err != nil {
			t.Fatalf("Set(%+v): %v", entry, err)
		}
	}

	got, ok := presets.Get(1)
	if !ok || got != want[1] {
		t.Fatalf("Get(1) = %+v ok=%v, want %+v", got, ok, want[1])
	}

	all := presets.All()
	if len(all) != 3 {
		t.Fatalf("All() len = %d, want 3", len(all))
	}

	for i := 1; i < len(all); i++ {
		if all[i-1].Slot > all[i].Slot {
			t.Fatalf("All() not sorted by slot: %v", all)
		}
	}
}

func TestSetSlotValidation(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "presets.json")

	presets, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	cases := []struct {
		slot    int
		wantErr bool
	}{
		{0, true},
		{1, false},
		{6, false},
		{7, true},
		{-1, true},
	}
	for _, c := range cases {
		err := presets.Set(preset(c.slot, "x", "n"))
		if (err != nil) != c.wantErr {
			t.Errorf("Set(slot=%d) err=%v, wantErr=%v", c.slot, err, c.wantErr)
		}
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "presets.json")

	presets, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	err = presets.Set(preset(2, "a", "A"))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	err = presets.Remove(2)
	if err != nil {
		t.Fatalf("Remove(2): %v", err)
	}

	if _, ok := presets.Get(2); ok {
		t.Fatal("Get(2) after Remove returned ok=true")
	}
	// Removing an absent slot is a no-op (no error).
	err = presets.Remove(2)
	if err != nil {
		t.Fatalf("Remove(absent): %v", err)
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "presets.json")

	first, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	saved := []store.Preset{
		presetWithLogo(1, "s1", "One", "one.png"),
		preset(4, "s4", "Four"),
	}
	for _, entry := range saved {
		err = first.Set(entry)
		if err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	second, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}

	got := second.All()
	if len(got) != 2 {
		t.Fatalf("reopened All() len = %d, want 2", len(got))
	}

	if got[0] != saved[0] || got[1] != saved[1] {
		t.Fatalf("reopened state = %+v, want %+v", got, saved)
	}
}

func TestOpenMissingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	presets, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open(missing) err = %v, want nil", err)
	}

	if got := presets.All(); len(got) != 0 {
		t.Fatalf("missing-file store All() = %v, want empty", got)
	}
}

func TestOpenCorruptFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "corrupt.json")

	err := os.WriteFile(path, []byte("{not valid json"), 0o644)
	if err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	_, err = store.Open(path)
	if err == nil {
		t.Fatal("Open(corrupt) err = nil, want error")
	}
}

func TestOpenSkipsOutOfRangeSlots(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "presets.json")
	// Valid JSON, but with slots outside 1..6 that Open must drop.
	data := `[{"slot":0,"stationId":"a","name":"A"},
	          {"slot":2,"stationId":"b","name":"B"},
	          {"slot":99,"stationId":"c","name":"C"}]`

	err := os.WriteFile(path, []byte(data), 0o644)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	presets, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	got := presets.All()
	if len(got) != 1 || got[0].Slot != 2 {
		t.Fatalf("All() = %+v, want only slot 2", got)
	}
}

func TestFailedWriteDoesNotMutateMemory(t *testing.T) {
	t.Parallel()

	presets, err := store.Open(filepath.Join(t.TempDir(), "missing", "presets.json"))
	if err != nil {
		t.Fatal(err)
	}

	err = presets.Set(preset(1, "s1", "One"))
	if err == nil {
		t.Fatal("Set succeeded with missing parent")
	}

	if _, ok := presets.Get(1); ok {
		t.Fatal("failed Set mutated in-memory state")
	}
}

// A corrupt presets file must not stop soundtapd: it is moved aside and the store starts empty.
func TestOpenQuarantinesCorruptFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "presets.json")

	err := os.WriteFile(path, []byte("[{\"slot\":1,"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	presets, err := store.Open(path)
	if err == nil || presets == nil || len(presets.All()) != 0 {
		t.Fatalf("Open = %v, %v; want an empty store and an error", presets, err)
	}

	kept, err := os.ReadFile(path + ".corrupt")
	if err != nil || string(kept) != "[{\"slot\":1," {
		t.Fatalf("corrupt file not preserved: %q %v", kept, err)
	}

	err = presets.Set(preset(2, "s1", "x"))
	if err != nil {
		t.Fatalf("store not usable after quarantine: %v", err)
	}
}

// preset is a station on a slot with only the fields the tests compare.
func preset(slot int, stationID, name string) store.Preset {
	return store.Preset{Slot: slot, StationID: stationID, Source: "", Type: "", Location: "", Name: name, Logo: ""}
}

func presetWithLogo(slot int, stationID, name, logo string) store.Preset {
	return store.Preset{Slot: slot, StationID: stationID, Source: "", Type: "", Location: "", Name: name, Logo: logo}
}
