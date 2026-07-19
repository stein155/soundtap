// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package player

import (
	"context"
	"encoding/json"
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
		return CachedStreams{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[loc]
	return e, ok
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
	c.m[loc] = CachedStreams{urls, time.Now()}
	if c.path == "" || had && slices.Equal(old.URLs, urls) {
		return
	}
	for k, e := range c.m {
		if time.Since(e.At) > streamsKeep {
			delete(c.m, k)
		}
	}
	_ = atomicjson.Write(c.path, c.m, 0o600) // a failed write keeps the list in RAM only
}

func (c *StreamCache) drop(loc string) {
	if c != nil {
		c.mu.Lock()
		delete(c.m, loc)
		c.mu.Unlock()
	}
}

// Resolver is the slice of tunein.Client that streamCandidates needs, so the walk
// can be tested without reaching TuneIn.
type Resolver interface {
	StreamURLs(ctx context.Context, stationID string) ([]string, error)
}

// url returns the stream to try for loc. Pass retry=true after an attempt that produced
// no audio, to move past whatever just failed.
func (s *streamCandidates) url(ctx context.Context, tc Resolver, loc string, retry bool) (string, error) {
	sid := loc[strings.LastIndex(loc, "/")+1:]
	switch {
	case loc != s.loc || len(s.urls) == 0:
		if c, ok := s.cache.get(loc); ok && time.Since(c.At) < streamCacheTTL && !retry {
			s.loc, s.urls, s.i, s.cached = loc, c.URLs, 0, true
		} else if err := s.resolve(ctx, tc, loc, sid); err != nil {
			return "", err
		}
	case retry:
		if s.i++; s.cached || s.i >= len(s.urls) {
			if err := s.resolve(ctx, tc, loc, sid); err != nil {
				return "", err
			}
		}
	}
	return s.urls[s.i], nil
}

func (s *streamCandidates) resolve(ctx context.Context, tc Resolver, loc, sid string) error {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var urls []string
	var err error
	if speaker.OwnStream(loc) {
		urls, err = ownStreamURLs(rctx, loc)
	} else {
		urls, err = tc.StreamURLs(rctx, sid)
	}
	if err != nil {
		// TuneIn did not answer: walk the last list this station had, however old, rather
		// than go silent. Its streams usually outlive the directory's answer.
		if c, ok := s.cache.get(loc); ok && !speaker.OwnStream(loc) && len(c.URLs) > 0 {
			// A retry already moved past the stream that failed; go on from there.
			if s.loc != loc || !slices.Equal(s.urls, c.URLs) || s.i >= len(c.URLs) {
				s.i = 0
			}
			s.loc, s.urls, s.cached = loc, c.URLs, false
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
func Prewarm(ctx context.Context, tc Resolver, presets func(context.Context) ([]speaker.Preset, error), cache *StreamCache, standby func() bool, log *slog.Logger) {
	t := time.NewTicker(prewarmEvery)
	defer t.Stop()
	for {
		refresh := prewarmRefresh
		if standby() {
			refresh = prewarmRefreshStandby
		}
		prewarmOnce(ctx, tc, presets, cache, refresh, log)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func prewarmOnce(ctx context.Context, tc Resolver, presets func(context.Context) ([]speaker.Preset, error), cache *StreamCache, refresh time.Duration, log *slog.Logger) {
	ps, err := presets(ctx)
	if err != nil {
		return
	}
	for _, p := range ps {
		if p.Source != sourceTuneIn || p.Location == "" || speaker.OwnStream(p.Location) {
			continue
		}
		if e, ok := cache.get(p.Location); ok && time.Since(e.At) < refresh {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		urls, err := tc.StreamURLs(rctx, p.Location[strings.LastIndex(p.Location, "/")+1:])
		cancel()
		if err != nil {
			log.Debug("preset prewarm failed", "slot", p.Slot, "err", err)
			continue
		}
		cache.put(p.Location, urls)
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
		return nil, fmt.Errorf("not a media URL: %s", loc)
	}
	listed, err := tunein.PlaylistStreams(ctx, playlistClient, loc)
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, u := range listed {
		if upnp.MediaURL(ctx, u) {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no media URL in %s", loc)
	}
	return urls, nil
}

// playlistClient fetches a playlist of the user's own, which may live on the home network:
// not safehttp, which refuses that, but held to MediaURL on every hop and dial.
var playlistClient = upnp.MediaClient(8 * time.Second)
