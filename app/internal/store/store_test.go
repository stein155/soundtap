// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetGetAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if got := s.All(); len(got) != 0 {
		t.Fatalf("fresh store All() = %v, want empty", got)
	}
	if _, ok := s.Get(1); ok {
		t.Fatalf("Get(1) on empty store returned ok=true")
	}

	want := []Preset{
		{Slot: 3, StationID: "s31", Name: "Radio 3"},
		{Slot: 1, StationID: "s10", Name: "Radio 1", Logo: "l1.png"},
		{Slot: 6, StationID: "s60", Name: "Radio 6"},
	}
	for _, p := range want {
		if err := s.Set(p); err != nil {
			t.Fatalf("Set(%+v): %v", p, err)
		}
	}

	got, ok := s.Get(1)
	if !ok || got != want[1] {
		t.Fatalf("Get(1) = %+v ok=%v, want %+v", got, ok, want[1])
	}

	all := s.All()
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
	path := filepath.Join(t.TempDir(), "presets.json")
	s, err := Open(path)
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
		err := s.Set(Preset{Slot: c.slot, StationID: "x", Name: "n"})
		if (err != nil) != c.wantErr {
			t.Errorf("Set(slot=%d) err=%v, wantErr=%v", c.slot, err, c.wantErr)
		}
	}
}

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Set(Preset{Slot: 2, StationID: "a", Name: "A"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Remove(2); err != nil {
		t.Fatalf("Remove(2): %v", err)
	}
	if _, ok := s.Get(2); ok {
		t.Fatalf("Get(2) after Remove returned ok=true")
	}
	// Removing an absent slot is a no-op (no error).
	if err := s.Remove(2); err != nil {
		t.Fatalf("Remove(absent): %v", err)
	}
}

func TestPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	in := []Preset{
		{Slot: 1, StationID: "s1", Name: "One", Logo: "one.png"},
		{Slot: 4, StationID: "s4", Name: "Four"},
	}
	for _, p := range in {
		if err := s1.Set(p); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}
	got := s2.All()
	if len(got) != 2 {
		t.Fatalf("reopened All() len = %d, want 2", len(got))
	}
	if got[0] != in[0] || got[1] != in[1] {
		t.Fatalf("reopened state = %+v, want %+v", got, in)
	}
}

func TestOpenMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(missing) err = %v, want nil", err)
	}
	if got := s.All(); len(got) != 0 {
		t.Fatalf("missing-file store All() = %v, want empty", got)
	}
}

func TestOpenCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, err := Open(path); err == nil {
		t.Fatalf("Open(corrupt) err = nil, want error")
	}
}

func TestOpenSkipsOutOfRangeSlots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	// Valid JSON, but with slots outside 1..6 that Open must drop.
	data := `[{"slot":0,"stationId":"a","name":"A"},
	          {"slot":2,"stationId":"b","name":"B"},
	          {"slot":99,"stationId":"c","name":"C"}]`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got := s.All()
	if len(got) != 1 || got[0].Slot != 2 {
		t.Fatalf("All() = %+v, want only slot 2", got)
	}
}

func TestFailedWriteDoesNotMutateMemory(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "missing", "presets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(Preset{Slot: 1, StationID: "s1", Name: "One"}); err == nil {
		t.Fatal("Set succeeded with missing parent")
	}
	if _, ok := s.Get(1); ok {
		t.Fatal("failed Set mutated in-memory state")
	}
}

// A corrupt presets file must not stop soundtapd: it is moved aside and the store starts empty.
func TestOpenQuarantinesCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	if err := os.WriteFile(path, []byte("[{\"slot\":1,"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err == nil || s == nil || len(s.All()) != 0 {
		t.Fatalf("Open = %v, %v; want an empty store and an error", s, err)
	}
	if b, rerr := os.ReadFile(path + ".corrupt"); rerr != nil || string(b) != "[{\"slot\":1," {
		t.Fatalf("corrupt file not preserved: %q %v", b, rerr)
	}
	if err := s.Set(Preset{Slot: 2, StationID: "s1", Name: "x"}); err != nil {
		t.Fatalf("store not usable after quarantine: %v", err)
	}
}
