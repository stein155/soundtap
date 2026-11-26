// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/readahead"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/upnp"
)

// streamCandidates walks one station's fallback chain.
//
// TuneIn answers a Tune.ashx with several stream URLs in priority order, and the first
// one is regularly not alive — a station's primary can be dead, geo-blocked, or simply
// black-holed. The player used to ask for a single URL and get that same first entry on
// every retry, so a station with a dead primary could never start: each attempt spent up
// to rw_timeout connecting, hit bufferDeadline, backed off, and resolved its way back to
// the identical dead URL. That is the "it sometimes sits on buffering for ages" case, and
// why it is station-dependent rather than random.
//
// So resolve once per station and walk the list, re-resolving only once it is exhausted
// (the stream set does change over time, and by then it is worth another round trip).
//
// Resolving costs ~2.4 s on the speaker, which is most of a preset press, so TuneIn lists
// are also remembered per station for streamCacheTTL. A cached list is trusted for the
// first attempt only: any retry resolves fresh, so a stale entry costs one failed attempt.
type streamCandidates struct {
	loc    string
	urls   []string
	i      int
	cached bool         // urls came from the cache, not from a fresh resolve
	cache  *StreamCache // shared with prewarmStreams; nil disables caching
}

const (
	streamCacheTTL = 26 * time.Hour
	// prewarmEvery is how often prewarmStreams looks for presets that are new or about to
	// expire; prewarmRefresh is the age at which an entry is renewed, well before the TTL.
	// In standby that is once a day: every installed speaker asks TuneIn from the
	// same User-Agent, and a speaker nobody listens to need not ask twice a day. A press
	// from standby still finds a list younger than the TTL.
	prewarmEvery          = time.Minute
	prewarmRefresh        = 10 * time.Hour
	prewarmRefreshStandby = 24 * time.Hour

	// resolveTimeout bounds one resolve of a station's streams.
	resolveTimeout = 10 * time.Second

	// streamsFileMode keeps the stream lists the owner's: they say what was listened to.
	streamsFileMode = 0o600

	// playlistTimeout bounds a fetch of the user's own playlist.
	playlistTimeout = 8 * time.Second
)

// Errors of a stream URL the user gave; the text after them names the URL.
var (
	errStreamNotMedia = errors.New("not a media URL")
	errNoMediaIn      = errors.New("no media URL in")
)

// CachedStreams is one station's resolved stream list and when it was resolved.
type CachedStreams struct {
	URLs []string  `json:"urls"`
	At   time.Time `json:"at"`
}

// StreamCache holds resolved TuneIn stream lists by station location. A nil cache is
// valid and remembers nothing. With a path it also keeps them on flash: when TuneIn does
// not answer (blocked, rate-limited, down), the last list a station had still plays,
// after a reboot too. It is written only when a station's list changes.
type StreamCache struct {
	mu   sync.Mutex
	m    map[string]CachedStreams
	path string
}

// streamsKeep is how long a station nobody resolved stays in the file.
const streamsKeep = 60 * 24 * time.Hour

// Load reads the lists kept on flash; a missing or unreadable file starts empty.
func (c *StreamCache) Load(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.path = path

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var m map[string]CachedStreams
	if json.Unmarshal(data, &m) == nil {
		c.m = m
	}
}

// All is a copy of every list the cache holds, by station location.
func (c *StreamCache) All() map[string]CachedStreams {
	c.mu.Lock()
	defer c.mu.Unlock()

	return maps.Clone(c.m)
}

func (c *StreamCache) get(loc string) (CachedStreams, bool) {
	if c == nil {
		return CachedStreams{URLs: nil, At: time.Time{}}, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	cached, ok := c.m[loc]

	return cached, ok
}

func (c *StreamCache) put(loc string, urls []string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.m == nil {
		c.m = map[string]CachedStreams{}
	}

	old, had := c.m[loc]

	c.m[loc] = CachedStreams{URLs: urls, At: time.Now()}
	if c.path == "" || had && slices.Equal(old.URLs, urls) {
		return
	}

	for k, e := range c.m {
		if time.Since(e.At) > streamsKeep {
			delete(c.m, k)
		}
	}

	_ = atomicjson.Write(c.path, c.m, streamsFileMode) // a failed write keeps the list in RAM only
}

// Resolver is the slice of tunein.Client that streamCandidates needs, so the walk
// can be tested without reaching TuneIn.
type Resolver interface {
	StreamURLs(ctx context.Context, stationID string) ([]string, error)
}

// url returns the stream to try first for loc: the one the walk is at, a list the cache
// keeps fresh, or the first of a new resolve.
func (s *streamCandidates) url(ctx context.Context, resolver Resolver, loc string) (string, error) {
	if loc != s.loc || len(s.urls) == 0 {
		cached, ok := s.cache.get(loc)
		if ok && time.Since(cached.At) < streamCacheTTL {
			s.loc, s.urls, s.i, s.cached = loc, cached.URLs, 0, true
		} else {
			err := s.resolve(ctx, resolver, loc, stationID(loc))
			if err != nil {
				return "", err
			}
		}
	}

	return s.urls[s.i], nil
}

// retry returns the stream to try for loc after an attempt that produced no audio: it
// moves past whatever just failed, and resolves afresh when the walk began from the cache
// or has run out.
func (s *streamCandidates) retry(ctx context.Context, resolver Resolver, loc string) (string, error) {
	if loc != s.loc || len(s.urls) == 0 {
		err := s.resolve(ctx, resolver, loc, stationID(loc))
		if err != nil {
			return "", err
		}

		return s.urls[s.i], nil
	}

	s.i++
	if s.cached || s.i >= len(s.urls) {
		err := s.resolve(ctx, resolver, loc, stationID(loc))
		if err != nil {
			return "", err
		}
	}

	return s.urls[s.i], nil
}

// stationID is the TuneIn guide id at the end of a station location.
func stationID(loc string) string {
	return loc[strings.LastIndex(loc, "/")+1:]
}

func (s *streamCandidates) resolve(ctx context.Context, resolver Resolver, loc, sid string) error {
	resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	var (
		urls []string
		err  error
	)
	if speaker.OwnStream(loc) {
		urls, err = ownStreamURLs(resolveCtx, loc)
	} else {
		urls, err = resolver.StreamURLs(resolveCtx, sid)
		if err != nil {
			err = fmt.Errorf("resolve %s: %w", sid, err)
		}
	}

	if err != nil {
		// TuneIn did not answer: walk the last list this station had, however old, rather
		// than go silent. Its streams usually outlive the directory's answer.
		cached, ok := s.cache.get(loc)
		if ok && !speaker.OwnStream(loc) && len(cached.URLs) > 0 {
			// A retry already moved past the stream that failed; go on from there.
			if s.loc != loc || !slices.Equal(s.urls, cached.URLs) || s.i >= len(cached.URLs) {
				s.i = 0
			}

			s.loc, s.urls, s.cached = loc, cached.URLs, false

			return nil
		}

		s.loc, s.urls, s.i, s.cached = "", nil, 0, false

		return err
	}

	s.loc, s.urls, s.i, s.cached = loc, urls, 0, false
	if !speaker.OwnStream(loc) { // own streams are re-checked against MediaURL every time
		s.cache.put(loc, urls)
	}

	return nil
}

// Prewarm keeps the cache filled for the TuneIn presets, so a press is warm even
// right after boot or a long standby. It runs one resolve at a time; a preset saved
// meanwhile is picked up on the next tick. Failures (network not up yet) just retry then.
func Prewarm(ctx context.Context, resolver Resolver, presets func(context.Context) ([]speaker.Preset, error),
	cache *StreamCache, standby func() bool, log *slog.Logger,
) {
	ticker := time.NewTicker(prewarmEvery)
	defer ticker.Stop()

	for {
		refresh := prewarmRefresh
		if standby() {
			refresh = prewarmRefreshStandby
		}

		prewarmOnce(ctx, resolver, presets, cache, refresh, log)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func prewarmOnce(ctx context.Context, resolver Resolver, presets func(context.Context) ([]speaker.Preset, error),
	cache *StreamCache, refresh time.Duration, log *slog.Logger,
) {
	saved, err := presets(ctx)
	if err != nil {
		return
	}

	for index := range saved {
		preset := &saved[index]
		if preset.Source != sourceTuneIn || preset.Location == "" || speaker.OwnStream(preset.Location) {
			continue
		}

		if cached, ok := cache.get(preset.Location); ok && time.Since(cached.At) < refresh {
			continue
		}

		resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
		urls, err := resolver.StreamURLs(resolveCtx, stationID(preset.Location))

		cancel()

		if err != nil {
			log.Debug("preset prewarm failed", "slot", preset.Slot, "err", err)

			continue
		}

		cache.put(preset.Location, urls)

		if len(urls) > 0 { // seeds the TLS session the press will resume
			readahead.Warm(ctx, urls[0])
		}
	}
}

// ownStreamURLs is what to open for a stream URL the user gave. Like a UPnP location it
// ends up in ffmpeg as root, so it passes the same check, and so does whatever a playlist
// names.
func ownStreamURLs(ctx context.Context, loc string) ([]string, error) {
	if !upnp.MediaURL(ctx, loc) {
		return nil, fmt.Errorf("%w: %s", errStreamNotMedia, loc)
	}

	listed, err := tunein.PlaylistStreams(ctx, playlistClient, loc)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", loc, err)
	}

	var urls []string

	for _, listedURL := range listed {
		if upnp.MediaURL(ctx, listedURL) {
			urls = append(urls, listedURL)
		}
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("%w %s", errNoMediaIn, loc)
	}

	return urls, nil
}

// playlistClient fetches a playlist of the user's own, which may live on the home network:
// not safehttp, which refuses that, but held to MediaURL on every hop and dial.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var playlistClient = upnp.MediaClient(playlistTimeout)
