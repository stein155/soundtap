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
func seed(enricher *Enricher, stationID, song, artist, art string) {
	enricher.mu.Lock()
	enricher.cache[stationID] = entry{song: song, artist: artist, art: art, at: time.Now(), fetching: false}
	enricher.mu.Unlock()
}

// playing is a station's now-playing as the speaker reports it.
func playing(stationID, station, track, status string) *speaker.NowPlaying {
	return &speaker.NowPlaying{
		Source: "", Track: track, Artist: "", Album: "", Station: station, StationID: stationID,
		Location: "", Art: "", PlayStatus: status, CanSkip: false, Failed: false, Unheard: false,
	}
}

// track is what TuneIn reports for a station.
func track(song, artist string) *tunein.Track {
	return &tunein.Track{Song: song, Artist: artist, Album: "", Art: "", Logo: "", IsLive: false}
}

func TestEnrichAppliesCachedTrack(t *testing.T) {
	t.Parallel()

	enricher := New(speakerstate.New(), tunein.New())
	seed(enricher, "s123", "Song", "Artist", "http://cover")

	now := playing("s123", "Radio X", "Radio X", "PLAY_STATE")
	enricher.Enrich(now)

	if now.Track != "Song" || now.Artist != "Artist" || now.Art != "http://cover" {
		t.Fatalf("enriched = %q/%q/%q, want Song/Artist/http://cover", now.Track, now.Artist, now.Art)
	}
}

// Tuning back to a station must not show the song it played yesterday.
func TestEnrichIgnoresAnOldEntry(t *testing.T) {
	t.Parallel()

	enricher := New(nil, nil)
	enricher.cache["s1"] = entry{
		song: "Yesterday", artist: "", art: "old.jpg", at: time.Now().Add(-shownFor), fetching: true,
	}
	now := playing("s1", "", "Station", "PLAY_STATE")
	enricher.Enrich(now)

	if now.Track != "Station" || now.Art != "" {
		t.Fatalf("old entry applied: %+v", now)
	}

	if line, ok := enricher.Track("s1"); ok {
		t.Fatalf("old entry on the display: %q", line)
	}
}

func TestEnrichDropsArtistEqualToStation(t *testing.T) {
	t.Parallel()

	// Some streams put the programme/station name in the artist slot; Enrich
	// must not show the station twice.
	enricher := New(speakerstate.New(), tunein.New())
	seed(enricher, "s1", "Song", " radio x ", "")

	now := playing("s1", "Radio X", "", "PLAY_STATE")
	enricher.Enrich(now)

	if now.Artist != "" {
		t.Fatalf("artist = %q, want dropped (repeats station)", now.Artist)
	}
}

func TestTrackLine(t *testing.T) {
	t.Parallel()

	enricher := New(speakerstate.New(), tunein.New())

	// Artist + song -> "Artist - Song".
	seed(enricher, "s1", "Song", "Artist", "")

	if got, ok := enricher.Track("s1"); !ok || got != "Artist - Song" {
		t.Errorf("Track = %q,%v want \"Artist - Song\",true", got, ok)
	}

	// Artist repeating the song -> just the song.
	seed(enricher, "s2", "Song", " song ", "")

	if got, ok := enricher.Track("s2"); !ok || got != "Song" {
		t.Errorf("Track = %q,%v want \"Song\",true", got, ok)
	}

	// No song cached, or non-TuneIn id -> not known.
	seed(enricher, "s3", "", "", "")

	if _, ok := enricher.Track("s3"); ok {
		t.Error("Track ok for empty song, want false")
	}

	if _, ok := enricher.Track("p100"); ok {
		t.Error("Track ok for non-TuneIn id, want false")
	}
}

func TestEnrichSkipsWhenNotPlaying(t *testing.T) {
	t.Parallel()

	enricher := New(speakerstate.New(), tunein.New())
	seed(enricher, "s1", "Song", "", "")

	now := playing("s1", "Radio X", "Radio X", "STOP_STATE")
	enricher.Enrich(now)

	if now.Track != "Radio X" {
		t.Fatalf("track = %q, want untouched while stopped", now.Track)
	}

	// Non-TuneIn station ids (no "s" prefix) are never enriched.
	now = playing("p100", "", "Local FM", "PLAY_STATE")
	enricher.Enrich(now)

	if now.Track != "Local FM" {
		t.Fatalf("track = %q, want untouched for non-TuneIn id", now.Track)
	}
}

func TestOrderLikeSwapsReversedICY(t *testing.T) {
	t.Parallel()

	tuned := track("Just the Way You Are", "Bruno Mars")

	artist, song := orderLike("Just The Way You Are", "BRUNO MARS", tuned)
	if artist != "BRUNO MARS" || song != "Just The Way You Are" {
		t.Fatalf("reversed pair not swapped: %q / %q", artist, song)
	}

	artist, song = orderLike("Bruno Mars", "Just The Way You Are", tuned)
	if artist != "Bruno Mars" || song != "Just The Way You Are" {
		t.Fatalf("correct pair changed: %q / %q", artist, song)
	}

	artist, song = orderLike("X", "Y", track("", ""))
	if artist != "X" || song != "Y" {
		t.Fatalf("empty TuneIn changed pair: %q / %q", artist, song)
	}
}

// Expired stream URLs go when new ones arrive, so the map stays bounded.
func TestStreamURLCacheDropsExpiredEntries(t *testing.T) {
	t.Parallel()

	enricher := New(speakerstate.New(), tunein.New())
	old := time.Now().Add(-2 * streamURLTTL)

	enricher.mu.Lock()
	for i := range 1000 {
		enricher.streamURLs[fmt.Sprintf("s%d", i)] = urlEntry{url: "http://old", at: old}
	}

	enricher.streamURLs["fresh"] = urlEntry{url: "http://fresh", at: time.Now()}
	enricher.pruneStreamURLsLocked()
	left := len(enricher.streamURLs)
	_, kept := enricher.streamURLs["fresh"]
	enricher.mu.Unlock()

	if left != 1 || !kept {
		t.Fatalf("after pruning: %d entries, fresh kept=%v; want only the fresh one", left, kept)
	}
}
