// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package recent_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/recent"
)

func TestRecordAndAll(t *testing.T) {
	s := recent.Open(filepath.Join(t.TempDir(), "recent.json"))

	s.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 1", Location: "/v1/playback/station/s1", Art: "http://art1"})
	s.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 2", Location: "/v1/playback/station/s2", Art: ""})

	all := s.All()
	if len(all) != 2 {
		t.Fatalf("want 2 entries, got %d", len(all))
	}
	if all[0].Name != "Radio 2" {
		t.Errorf("want newest first, got %q", all[0].Name)
	}
}

func TestDeduplication(t *testing.T) {
	s := recent.Open(filepath.Join(t.TempDir(), "recent.json"))

	s.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 1", Location: "/v1/playback/station/s1"})
	s.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 2", Location: "/v1/playback/station/s2"})
	// Playing Radio 1 again should move it to the front, not duplicate.
	s.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 1", Location: "/v1/playback/station/s1"})

	all := s.All()
	if len(all) != 2 {
		t.Fatalf("want 2 after dedup, got %d", len(all))
	}
	if all[0].Location != "/v1/playback/station/s1" {
		t.Errorf("want Radio 1 first after replay, got %q", all[0].Name)
	}
}

func TestBoundedAt50(t *testing.T) {
	s := recent.Open(filepath.Join(t.TempDir(), "recent.json"))
	for i := 0; i < 55; i++ {
		s.Record(recent.Entry{Source: "TUNEIN", Name: "s", Location: string(rune('A' + i))})
	}
	if len(s.All()) != 50 {
		t.Fatalf("want 50, got %d", len(s.All()))
	}
}

func TestIgnoresStandby(t *testing.T) {
	s := recent.Open(filepath.Join(t.TempDir(), "recent.json"))
	s.Record(recent.Entry{Source: "STANDBY"})
	s.Record(recent.Entry{Source: ""})
	if len(s.All()) != 0 {
		t.Fatal("should not record STANDBY or empty source")
	}
}

func TestPersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recent.json")

	s1 := recent.Open(path)
	s1.Record(recent.Entry{Source: "AIRPLAY", Name: "Song", Location: "", StoppedAt: time.Now()})

	s2 := recent.Open(path)
	all := s2.All()
	if len(all) != 1 || all[0].Source != "AIRPLAY" {
		t.Fatalf("reload failed, got %v", all)
	}
}

func TestCorruptFileStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.json")
	_ = os.WriteFile(path, []byte("not json"), 0o644)
	s := recent.Open(path)
	if len(s.All()) != 0 {
		t.Fatal("corrupt file should start fresh")
	}
}

func TestFailedRecordDoesNotMutateMemory(t *testing.T) {
	s := recent.Open(filepath.Join(t.TempDir(), "missing", "recent.json"))
	if err := s.Record(recent.Entry{Source: "TUNEIN", Location: "station"}); err == nil {
		t.Fatal("Record succeeded with missing parent directory")
	}
	if got := s.All(); len(got) != 0 {
		t.Fatalf("entries after failed write = %v, want empty", got)
	}
}

func TestLastStationSkipsExternalSources(t *testing.T) {
	s := recent.Open(filepath.Join(t.TempDir(), "recent.json"))
	if _, ok := s.LastStation(); ok {
		t.Fatal("empty list has no station")
	}
	s.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 1", Location: "/v1/playback/station/s1"})
	s.Record(recent.Entry{Source: "UPNP", Name: "quiet", Location: "http://nas/quiet.mp3"})
	s.Record(recent.Entry{Source: "MULTIROOM", Name: "MULTIROOM"})
	if e, ok := s.LastStation(); !ok || e.Name != "Radio 1" {
		t.Fatalf("LastStation = %+v, %v; want Radio 1", e, ok)
	}
}

func TestRemoveSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.json")
	s := recent.Open(path)
	s.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 1", Location: "/v1/playback/station/s1"})
	s.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 2", Location: "/v1/playback/station/s2"})

	if err := s.Remove("TUNEIN", "/v1/playback/station/s1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("TUNEIN", "/v1/playback/station/none"); err != nil {
		t.Fatal(err)
	}
	all := recent.Open(path).All()
	if len(all) != 1 || all[0].Name != "Radio 2" {
		t.Fatalf("after remove and reopen = %+v, want only Radio 2", all)
	}
}

func TestSetArtFillsOnlyAnEmptyEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.json")
	s := recent.Open(path)
	for _, e := range []recent.Entry{
		{Source: "TUNEIN", Name: "Old", Location: "/v1/playback/station/s1", Art: "http://kept"},
		{Source: "TUNEIN", Name: "New", Location: "/v1/playback/station/s2"},
	} {
		if err := s.Record(e); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.SetArt("TUNEIN", "/v1/playback/station/s2", "https://logo2"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetArt("TUNEIN", "/v1/playback/station/s1", "https://other"); err != nil {
		t.Fatal(err)
	}
	all := recent.Open(path).All() // read back from disk
	if all[0].Art != "https://logo2" || all[1].Art != "http://kept" {
		t.Errorf("art = %q, %q; want https://logo2, http://kept", all[0].Art, all[1].Art)
	}
}

func TestReplayWithoutArtKeepsTheLogo(t *testing.T) {
	s := recent.Open(filepath.Join(t.TempDir(), "recent.json"))
	loc := "/v1/playback/station/s1"
	for _, art := range []string{"https://logo/s1.png", ""} {
		if err := s.Record(recent.Entry{Source: "TUNEIN", Name: "Radio 1", Location: loc, Art: art}); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.All()[0].Art; got != "https://logo/s1.png" {
		t.Fatalf("art = %q, want the earlier logo", got)
	}
}
