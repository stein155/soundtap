// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package nowplaying enriches the speaker's now-playing with the live song,
// artist and cover art. The speaker no longer receives track metadata (it came
// from the retired Bose cloud), so the current track is read from the standard
// ICY metadata on the station's own stream (internal/icy), falling back to
// TuneIn's Describe, with cover art looked up generically (internal/artwork).
// Both the web UI and the MQTT bridge consume the same Enricher, so
// the stream is read once per station, not once per consumer.
package nowplaying

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/artwork"
	"github.com/stein155/soundtap/app/internal/icy"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/safehttp"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/tunein"
)

// ttl is how long a now-playing lookup stays fresh. Songs change every few
// minutes, so a short cache keeps the line current without per-poll stream reads.
// 45s, not 15s: each refresh opens a *second* connection to the station (separate from
// the player's ffmpeg), so a tight TTL doubles stream bandwidth and can trip per-IP CDN
// connection limits. Radio tracks run minutes, so 45s stays fresh enough.
const ttl = 45 * time.Second

// shownFor is how old a lookup may be and still be shown. A station that plays stays well
// inside it, since every poll refreshes after ttl. One tuned back to after a while gets
// the station alone until its refresh lands, not the song it played back then.
const shownFor = 2 * ttl

// streamURLTTL is how long a resolved stream URL is reused before re-resolving.
const streamURLTTL = 5 * time.Minute

// clientTimeout bounds every artwork lookup and stream read.
const clientTimeout = 12 * time.Second

// Each lookup of a refresh gets its own budget. One shared 12 s context let a slow ICY
// read use it all up, so the TuneIn fallback never ran.
const (
	icyBudget     = 6 * time.Second
	tuneinBudget  = 4 * time.Second
	artworkBudget = 4 * time.Second
)

// maxCached is how many stations the now-playing cache holds before expired entries
// are dropped: about the stations currently on screen.
const maxCached = 64

// entry is a now-playing lookup cached briefly so the UI's poll (every few
// seconds) doesn't re-read the stream for the same station. fetching guards
// against launching a second background refresh while one is already in flight.
type entry struct {
	song, artist, art string
	at                time.Time
	fetching          bool
}

// urlEntry caches the stream URL resolved from TuneIn so each poll doesn't
// re-hit Tune.ashx; the URL (and any embedded token) is stable for a while.
type urlEntry struct {
	url string
	at  time.Time
}

// Enricher fills in live track metadata for playing stations. Create with New;
// safe for concurrent use.
type Enricher struct {
	speaker nowPlayer
	tunein  *tunein.Client
	proxy   *http.Client // artwork lookups (public hosts only)
	stream  *http.Client // reads ICY metadata off the audio stream

	mu         sync.Mutex
	cache      map[string]entry    // now-playing, keyed by station id
	streamURLs map[string]urlEntry // resolved stream URL, keyed by station id
}

// nowPlayer is the speaker's now-playing source.
type nowPlayer interface {
	NowPlaying(ctx context.Context) (*speaker.NowPlaying, error)
}

// New builds an Enricher. The outbound clients only dial public addresses
// (safehttp.SafeTransport), since stream and artwork URLs come from third parties.
func New(source interface {
	NowPlaying(ctx context.Context) (*speaker.NowPlaying, error)
}, client *tunein.Client,
) *Enricher {
	return &Enricher{
		speaker:    source,
		tunein:     client,
		proxy:      &http.Client{Timeout: clientTimeout, Transport: safehttp.SafeTransport()},
		stream:     &http.Client{Timeout: clientTimeout, Transport: safehttp.SafeTransport()},
		mu:         sync.Mutex{},
		cache:      map[string]entry{},
		streamURLs: map[string]urlEntry{},
	}
}

// NowPlaying reads the speaker's now-playing and enriches it — the same view
// the web UI shows. The MQTT bridge uses it so clients see the track,
// not just the station name.
func (e *Enricher) NowPlaying(ctx context.Context) (*speaker.NowPlaying, error) {
	playing, err := e.speaker.NowPlaying(ctx)
	if err != nil {
		return nil, fmt.Errorf("nowplaying: %w", err)
	}

	e.Enrich(playing) //nolint:contextcheck // the metadata refresh runs on its own, past this request

	return playing, nil
}

// Track returns the live "now playing" line for a TuneIn station id — "Artist -
// Song", or just "Song" when the artist is unknown or merely repeats the station
// — and whether one is known. Like Enrich it applies the cache and kicks off a
// background refresh when stale, so it stays fast; it returns ("", false) until
// the first lookup lands. Used to drive the track onto the speaker's own display
// via marge (the firmware only shows what the cloud playback doc carries).
func (e *Enricher) Track(stationID string) (string, bool) {
	if !strings.HasPrefix(stationID, "s") {
		return "", false
	}

	ent, ok := e.cached(stationID)
	if !ok || ent.song == "" || time.Since(ent.at) >= shownFor {
		return "", false
	}

	artist := strings.TrimSpace(ent.artist)
	if artist != "" && !strings.EqualFold(artist, strings.TrimSpace(ent.song)) {
		return artist + " - " + ent.song, true
	}

	return ent.song, true
}

// Enrich fills in the song/artist/cover for a playing station. The read happens
// in the background so the caller's poll stays fast; this call only applies
// whatever is cached and kicks off a refresh when the entry is stale.
// Best-effort: with nothing cached yet the UI just shows the station until the
// next poll fills it in.
func (e *Enricher) Enrich(playing *speaker.NowPlaying) {
	if playing == nil {
		return
	}

	if playing.Art == speaker.InternetRadioIcon {
		playing.Art = ""
	}

	if !strings.HasPrefix(playing.StationID, "s") && !speaker.OwnStream(playing.StationID) {
		return
	}

	if playing.PlayStatus != "" && playing.PlayStatus != "PLAY_STATE" && playing.PlayStatus != "BUFFERING_STATE" {
		return // nothing is playing — don't show a stale song
	}

	ent, ok := e.cached(playing.StationID)
	if !ok || time.Since(ent.at) >= shownFor {
		return
	}

	apply(playing, &ent)
}

// cached returns the cache entry for a station and whether there is one, and starts a
// background refresh when the entry is stale and none is in flight.
func (e *Enricher) cached(stationID string) (entry, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ent, ok := e.cache[stationID]
	if (!ok || time.Since(ent.at) >= ttl) && !ent.fetching {
		ent.fetching = true
		e.cache[stationID] = ent

		safego.Go("nowplaying.refresh", func() { e.refresh(stationID) })
	}

	return ent, ok
}

// apply puts a cached lookup on the speaker's now-playing.
func apply(playing *speaker.NowPlaying, ent *entry) {
	// The speaker fills Track with the station name as a placeholder and never
	// knows the artist, so the stream's live song wins whenever it has one.
	if ent.song == "" {
		if playing.Art == "" && ent.art != "" {
			playing.Art = ent.art
		}

		return
	}

	playing.Track = ent.song
	playing.Artist = ent.artist
	// Some streams (e.g. NPO) put the programme name where the artist goes;
	// when that just repeats the station, drop it rather than show it twice.
	if strings.EqualFold(strings.TrimSpace(playing.Artist), strings.TrimSpace(playing.Station)) {
		playing.Artist = ""
	}
	// The speaker's own art is the generic station logo; our track cover is
	// more specific, so it takes priority when we have one.
	if ent.art != "" {
		playing.Art = ent.art
	}
}

// step is the budget of one lookup of a refresh.
func step(budget time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), budget)
}

// refresh reads the current track off the station's stream and looks up cover
// art, then stores the result. Runs in its own goroutine off a fresh context
// (the request that triggered it has already returned). Any failure caches an
// empty entry so the poll doesn't retry until the TTL lapses.
func (e *Enricher) refresh(stationID string) {
	// This goroutine parses untrusted stream/ICY/artwork data. If any of that
	// panics, clear the entry's `fetching` flag so the station isn't wedged
	// forever — the Enrich/Track guard skips a station while fetching is true, so
	// a leaked flag would stop it ever refreshing again (and exempt it from
	// eviction). Best-effort empty entry, retried after the TTL.
	defer func() {
		if r := recover(); r != nil {
			e.mu.Lock()
			e.cache[stationID] = entry{song: "", artist: "", art: "", at: time.Now(), fetching: false}
			e.mu.Unlock()
		}
	}()

	// Primary: the standard ICY metadata in the stream itself.
	ent := e.streamTrack(stationID)
	// TuneIn's Describe fills in when the stream carried no track metadata, and
	// settles the order when it did: "Artist - Title" is only a convention, and
	// some stations (Qmusic) send "Title - ARTIST".
	if !speaker.OwnStream(stationID) {
		e.tuneinTrack(stationID, &ent)
	}
	// Cover art for whatever we found: look it up generically when we don't
	// already have one (ICY carries none; TuneIn sometimes does).
	if ent.song != "" && ent.art == "" {
		term := strings.TrimSpace(ent.artist + " " + ent.song)

		ctx, cancel := step(artworkBudget)

		art, err := artwork.Search(ctx, e.proxy, term)
		if err == nil {
			ent.art = art
		}

		cancel()
	}

	ent.at = time.Now()

	e.store(stationID, &ent)
}

// streamTrack reads the artist and song off the station's own stream.
func (e *Enricher) streamTrack(stationID string) entry {
	var ent entry

	ctx, cancel := step(icyBudget)
	defer cancel()

	streamURL := e.streamURL(ctx, stationID)
	if streamURL == "" {
		return ent
	}

	title, err := icy.StreamTitle(ctx, e.stream, streamURL)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			e.invalidateStreamURL(stationID) // stale URL / expired token — re-resolve next time
		}

		return ent
	}

	ent.artist, ent.song = icy.SplitArtistTitle(title)

	return ent
}

// tuneinTrack fills ent from TuneIn's Describe when the stream named no song, and
// orders the stream's pair like TuneIn's when it did.
func (e *Enricher) tuneinTrack(stationID string, ent *entry) {
	ctx, cancel := step(tuneinBudget)
	track, err := e.tunein.NowPlaying(ctx, stationID)

	cancel()

	if err != nil || track.Song == "" {
		return
	}

	if ent.song == "" {
		ent.song, ent.artist, ent.art = track.Song, track.Artist, track.Art
	} else {
		ent.artist, ent.song = orderLike(ent.artist, ent.song, &track)
	}
}

// store caches a station's lookup.
func (e *Enricher) store(stationID string, ent *entry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// The cache only needs the stations currently on screen; drop expired
	// entries once it grows past that so it can't accumulate for months.
	if len(e.cache) > maxCached {
		for k, v := range e.cache {
			if k != stationID && time.Since(v.at) >= ttl && !v.fetching {
				delete(e.cache, k)
			}
		}
	}

	e.cache[stationID] = *ent
}

// orderLike swaps an ICY artist/title pair when TuneIn names the same track the
// other way round.
func orderLike(artist, song string, track *tunein.Track) (string, string) {
	if strings.EqualFold(artist, track.Song) && strings.EqualFold(song, track.Artist) {
		return song, artist
	}

	return artist, song
}

// streamURL returns the station's playable stream URL, resolved via TuneIn and
// cached so each poll doesn't re-resolve. Empty on failure.
func (e *Enricher) streamURL(ctx context.Context, stationID string) string {
	if speaker.OwnStream(stationID) {
		return stationID
	}

	e.mu.Lock()
	if cachedURL, ok := e.streamURLs[stationID]; ok && time.Since(cachedURL.at) < streamURLTTL {
		e.mu.Unlock()

		return cachedURL.url
	}
	e.mu.Unlock()

	urls, err := e.tunein.Resolve(ctx, stationID)
	if err != nil {
		return ""
	}

	playable := tunein.PlayableURL(urls)
	if playable == "" {
		return ""
	}

	e.mu.Lock()
	e.pruneStreamURLsLocked()
	e.streamURLs[stationID] = urlEntry{url: playable, at: time.Now()}
	e.mu.Unlock()

	return playable
}

// pruneStreamURLsLocked drops expired stream URLs as new ones come in, so station churn
// over months does not keep every resolved (possibly signed) URL in memory. What
// is left is what was resolved within one TTL. Call with e.mu held.
func (e *Enricher) pruneStreamURLsLocked() {
	for k, v := range e.streamURLs {
		if time.Since(v.at) >= streamURLTTL {
			delete(e.streamURLs, k)
		}
	}
}

// invalidateStreamURL drops a cached stream URL so it is re-resolved next time.
func (e *Enricher) invalidateStreamURL(stationID string) {
	e.mu.Lock()
	delete(e.streamURLs, stationID)
	e.mu.Unlock()
}
