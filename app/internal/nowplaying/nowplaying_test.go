// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package nowplaying

import (
	"fmt"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/tunein"
)

// seed puts a fresh cache entry in place so Enrich applies it without kicking
// off a background refresh (at is now, so the entry is not stale).
func seed(e *Enricher, id string, ent entry) {
	ent.at = time.Now()

	e.mu.Lock()
	e.cache[id] = ent
	e.mu.Unlock()
}

func TestEnrichAppliesCachedTrack(t *testing.T) {
	e := New(speakerstate.New(), tunein.New())
	seed(e, "s123", entry{song: "Song", artist: "Artist", art: "http://cover"})

	np := &speaker.NowPlaying{StationID: "s123", Station: "Radio X", Track: "Radio X", PlayStatus: "PLAY_STATE"}
	e.Enrich(np)

	if np.Track != "Song" || np.Artist != "Artist" || np.Art != "http://cover" {
		t.Fatalf("enriched = %q/%q/%q, want Song/Artist/http://cover", np.Track, np.Artist, np.Art)
	}
}

// Tuning back to a station must not show the song it played yesterday.
func TestEnrichIgnoresAnOldEntry(t *testing.T) {
	e := New(nil, nil)
	e.cache["s1"] = entry{song: "Yesterday", art: "old.jpg", at: time.Now().Add(-shownFor), fetching: true}
	np := &speaker.NowPlaying{StationID: "s1", Track: "Station", PlayStatus: "PLAY_STATE"}
	e.Enrich(np)

	if np.Track != "Station" || np.Art != "" {
		t.Fatalf("old entry applied: %+v", np)
	}

	if line, ok := e.Track("s1"); ok {
		t.Fatalf("old entry on the display: %q", line)
	}
}

func TestEnrichDropsArtistEqualToStation(t *testing.T) {
	// Some streams put the programme/station name in the artist slot; Enrich
	// must not show the station twice.
	e := New(speakerstate.New(), tunein.New())
	seed(e, "s1", entry{song: "Song", artist: " radio x "})

	np := &speaker.NowPlaying{StationID: "s1", Station: "Radio X", PlayStatus: "PLAY_STATE"}
	e.Enrich(np)

	if np.Artist != "" {
		t.Fatalf("artist = %q, want dropped (repeats station)", np.Artist)
	}
}

func TestTrackLine(t *testing.T) {
	e := New(speakerstate.New(), tunein.New())

	// Artist + song -> "Artist - Song".
	seed(e, "s1", entry{song: "Song", artist: "Artist"})

	if got, ok := e.Track("s1"); !ok || got != "Artist - Song" {
		t.Errorf("Track = %q,%v want \"Artist - Song\",true", got, ok)
	}

	// Artist repeating the song -> just the song.
	seed(e, "s2", entry{song: "Song", artist: " song "})

	if got, ok := e.Track("s2"); !ok || got != "Song" {
		t.Errorf("Track = %q,%v want \"Song\",true", got, ok)
	}

	// No song cached, or non-TuneIn id -> not known.
	seed(e, "s3", entry{})

	if _, ok := e.Track("s3"); ok {
		t.Error("Track ok for empty song, want false")
	}

	if _, ok := e.Track("p100"); ok {
		t.Error("Track ok for non-TuneIn id, want false")
	}
}

func TestEnrichSkipsWhenNotPlaying(t *testing.T) {
	e := New(speakerstate.New(), tunein.New())
	seed(e, "s1", entry{song: "Song"})

	np := &speaker.NowPlaying{StationID: "s1", Station: "Radio X", Track: "Radio X", PlayStatus: "STOP_STATE"}
	e.Enrich(np)

	if np.Track != "Radio X" {
		t.Fatalf("track = %q, want untouched while stopped", np.Track)
	}

	// Non-TuneIn station ids (no "s" prefix) are never enriched.
	np = &speaker.NowPlaying{StationID: "p100", Track: "Local FM", PlayStatus: "PLAY_STATE"}
	e.Enrich(np)

	if np.Track != "Local FM" {
		t.Fatalf("track = %q, want untouched for non-TuneIn id", np.Track)
	}
}

func TestOrderLikeSwapsReversedICY(t *testing.T) {
	tr := tunein.Track{Song: "Just the Way You Are", Artist: "Bruno Mars"}
	if a, s := orderLike("Just The Way You Are", "BRUNO MARS", tr); a != "BRUNO MARS" || s != "Just The Way You Are" {
		t.Fatalf("reversed pair not swapped: %q / %q", a, s)
	}

	if a, s := orderLike("Bruno Mars", "Just The Way You Are", tr); a != "Bruno Mars" || s != "Just The Way You Are" {
		t.Fatalf("correct pair changed: %q / %q", a, s)
	}

	if a, s := orderLike("X", "Y", tunein.Track{}); a != "X" || s != "Y" {
		t.Fatalf("empty TuneIn changed pair: %q / %q", a, s)
	}
}

// Expired stream URLs go when new ones arrive, so the map stays bounded.
func TestStreamURLCacheDropsExpiredEntries(t *testing.T) {
	e := New(speakerstate.New(), tunein.New())
	old := time.Now().Add(-2 * streamURLTTL)

	e.mu.Lock()
	for i := range 1000 {
		e.streamURLs[fmt.Sprintf("s%d", i)] = urlEntry{url: "http://old", at: old}
	}

	e.streamURLs["fresh"] = urlEntry{url: "http://fresh", at: time.Now()}
	e.pruneStreamURLsLocked()
	n := len(e.streamURLs)
	_, kept := e.streamURLs["fresh"]
	e.mu.Unlock()

	if n != 1 || !kept {
		t.Fatalf("after pruning: %d entries, fresh kept=%v; want only the fresh one", n, kept)
	}
}
