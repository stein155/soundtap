// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package recent_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/recent"
)

func TestRecordAndAll(t *testing.T) {
	t.Parallel()

	store := recent.Open(filepath.Join(t.TempDir(), "recent.json"))

	record(t, store, station("Radio 1", "/v1/playback/station/s1", "http://art1"))
	record(t, store, station("Radio 2", "/v1/playback/station/s2", ""))

	all := store.All()
	if len(all) != 2 {
		t.Fatalf("want 2 entries, got %d", len(all))
	}

	if all[0].Name != "Radio 2" {
		t.Errorf("want newest first, got %q", all[0].Name)
	}
}

func TestDeduplication(t *testing.T) {
	t.Parallel()

	store := recent.Open(filepath.Join(t.TempDir(), "recent.json"))

	record(t, store, station("Radio 1", "/v1/playback/station/s1", ""))
	record(t, store, station("Radio 2", "/v1/playback/station/s2", ""))
	// Playing Radio 1 again should move it to the front, not duplicate.
	record(t, store, station("Radio 1", "/v1/playback/station/s1", ""))

	all := store.All()
	if len(all) != 2 {
		t.Fatalf("want 2 after dedup, got %d", len(all))
	}

	if all[0].Location != "/v1/playback/station/s1" {
		t.Errorf("want Radio 1 first after replay, got %q", all[0].Name)
	}
}

func TestBoundedAt50(t *testing.T) {
	t.Parallel()

	store := recent.Open(filepath.Join(t.TempDir(), "recent.json"))
	for i := range 55 {
		record(t, store, station("s", string(rune('A'+i)), ""))
	}

	if len(store.All()) != 50 {
		t.Fatalf("want 50, got %d", len(store.All()))
	}
}

func TestIgnoresStandby(t *testing.T) {
	t.Parallel()

	store := recent.Open(filepath.Join(t.TempDir(), "recent.json"))
	record(t, store, &recent.Entry{Source: "STANDBY", Name: "", Location: "", Art: "", StoppedAt: time.Time{}})
	record(t, store, &recent.Entry{Source: "", Name: "", Location: "", Art: "", StoppedAt: time.Time{}})

	if len(store.All()) != 0 {
		t.Fatal("should not record STANDBY or empty source")
	}
}

func TestPersistAndReload(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "recent.json")

	first := recent.Open(path)
	record(t, first, &recent.Entry{Source: "AIRPLAY", Name: "Song", Location: "", Art: "", StoppedAt: time.Now()})

	second := recent.Open(path)

	all := second.All()
	if len(all) != 1 || all[0].Source != "AIRPLAY" {
		t.Fatalf("reload failed, got %v", all)
	}
}

func TestCorruptFileStartsFresh(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "recent.json")
	_ = os.WriteFile(path, []byte("not json"), 0o644)

	store := recent.Open(path)
	if len(store.All()) != 0 {
		t.Fatal("corrupt file should start fresh")
	}
}

func TestFailedRecordDoesNotMutateMemory(t *testing.T) {
	t.Parallel()

	store := recent.Open(filepath.Join(t.TempDir(), "missing", "recent.json"))

	err := store.Record(*station("", "station", ""))
	if err == nil {
		t.Fatal("Record succeeded with missing parent directory")
	}

	if got := store.All(); len(got) != 0 {
		t.Fatalf("entries after failed write = %v, want empty", got)
	}
}

func TestLastStationSkipsExternalSources(t *testing.T) {
	t.Parallel()

	store := recent.Open(filepath.Join(t.TempDir(), "recent.json"))
	if _, ok := store.LastStation(); ok {
		t.Fatal("empty list has no station")
	}

	record(t, store, station("Radio 1", "/v1/playback/station/s1", ""))
	record(t, store, &recent.Entry{
		Source: "UPNP", Name: "quiet", Location: "http://nas/quiet.mp3", Art: "", StoppedAt: time.Time{},
	})
	record(t, store, &recent.Entry{
		Source: "MULTIROOM", Name: "MULTIROOM", Location: "", Art: "", StoppedAt: time.Time{},
	})

	if e, ok := store.LastStation(); !ok || e.Name != "Radio 1" {
		t.Fatalf("LastStation = %+v, %v; want Radio 1", e, ok)
	}
}

func TestRemoveSurvivesReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "recent.json")
	store := recent.Open(path)
	record(t, store, station("Radio 1", "/v1/playback/station/s1", ""))
	record(t, store, station("Radio 2", "/v1/playback/station/s2", ""))

	err := store.Remove("TUNEIN", "/v1/playback/station/s1")
	if err != nil {
		t.Fatal(err)
	}

	err = store.Remove("TUNEIN", "/v1/playback/station/none")
	if err != nil {
		t.Fatal(err)
	}

	all := recent.Open(path).All()
	if len(all) != 1 || all[0].Name != "Radio 2" {
		t.Fatalf("after remove and reopen = %+v, want only Radio 2", all)
	}
}

func TestSetArtFillsOnlyAnEmptyEntry(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "recent.json")

	store := recent.Open(path)
	record(t, store, station("Old", "/v1/playback/station/s1", "http://kept"))
	record(t, store, station("New", "/v1/playback/station/s2", ""))

	err := store.SetArt("TUNEIN", "/v1/playback/station/s2", "https://logo2")
	if err != nil {
		t.Fatal(err)
	}

	err = store.SetArt("TUNEIN", "/v1/playback/station/s1", "https://other")
	if err != nil {
		t.Fatal(err)
	}

	all := recent.Open(path).All() // read back from disk
	if all[0].Art != "https://logo2" || all[1].Art != "http://kept" {
		t.Errorf("art = %q, %q; want https://logo2, http://kept", all[0].Art, all[1].Art)
	}
}

func TestReplayWithoutArtKeepsTheLogo(t *testing.T) {
	t.Parallel()

	store := recent.Open(filepath.Join(t.TempDir(), "recent.json"))

	loc := "/v1/playback/station/s1"
	for _, art := range []string{"https://logo/s1.png", ""} {
		err := store.Record(*station("Radio 1", loc, art))
		if err != nil {
			t.Fatal(err)
		}
	}

	if got := store.All()[0].Art; got != "https://logo/s1.png" {
		t.Fatalf("art = %q, want the earlier logo", got)
	}
}

// station is a TuneIn entry with no stop time.
func station(name, location, art string) *recent.Entry {
	return &recent.Entry{Source: "TUNEIN", Name: name, Location: location, Art: art, StoppedAt: time.Time{}}
}

// record records entry and fails the test when that does not persist.
func record(t *testing.T, store *recent.Store, entry *recent.Entry) {
	t.Helper()

	err := store.Record(*entry)
	if err != nil {
		t.Fatal(err)
	}
}

// A factory reset closes the list before it removes the file: a source change still in
// flight must not write the previous owner's history back.
func TestClosedStoreWritesNothing(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "recent.json")
	store := recent.Open(path)

	err := store.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 1", Location: "s1", Art: "", StoppedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	err = store.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}

	for name, write := range map[string]func() error{
		"Record": func() error {
			return store.Record(recent.Entry{
				Source: "TUNEIN", Name: "Radio 2", Location: "s2", Art: "", StoppedAt: time.Now(),
			})
		},
		"SetArt": func() error { return store.SetArt("TUNEIN", "s1", "http://192.0.2.1/a.png") },
		"Remove": func() error { return store.Remove("TUNEIN", "s1") },
	} {
		err = write()
		if !errors.Is(err, recent.ErrClosed) {
			t.Errorf("%s after Close = %v, want ErrClosed", name, err)
		}

		_, err = os.Stat(path)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s after Close wrote the file back: %v", name, err)
		}
	}
}
