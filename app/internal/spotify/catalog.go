// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package spotify

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/stein155/soundtap/app/internal/safego"
)

// The catalogue comes from Spotify's own client endpoints (spclient), fetched by
// librespot on its session. The public Web API is no option: it answers 429 for
// librespot's client identity, and so does nothing else we could use without a
// developer app.

// ---- metadata wire format (only the fields we use) ------------------------------------

type mImage struct {
	FileID string `json:"file_id"`
	Size   string `json:"size"`
}

type mImageGroup struct {
	Image []mImage `json:"image"`
}

// artwork picks the ~300 px image; the speaker has no use for the 640 px one.
func (g *mImageGroup) artwork() string {
	if g == nil || len(g.Image) == 0 {
		return ""
	}

	for _, im := range g.Image {
		if im.Size == "DEFAULT" {
			return artworkPath(im.FileID)
		}
	}

	return artworkPath(g.Image[0].FileID)
}

type mRef struct {
	GID  string `json:"gid"`
	Name string `json:"name"`
}

type mTrack struct {
	GID      string  `json:"gid"`
	Name     string  `json:"name"`
	Artist   []mRef  `json:"artist"`
	Duration int     `json:"duration"`
	Album    *mAlbum `json:"album"`
}

type mAlbum struct {
	GID    string       `json:"gid"`
	Name   string       `json:"name"`
	Artist []mRef       `json:"artist"`
	Cover  *mImageGroup `json:"cover_group"`
	Date   *struct {
		Year int `json:"year"`
	} `json:"date"`
	Disc []struct {
		Track []mRef `json:"track"`
	} `json:"disc"`
}

type mArtist struct {
	GID      string       `json:"gid"`
	Name     string       `json:"name"`
	Portrait *mImageGroup `json:"portrait_group"`
	TopTrack []struct {
		Country string `json:"country"`
		Track   []mRef `json:"track"`
	} `json:"top_track"`
	AlbumGroup  []struct{ Album []mRef } `json:"album_group"`
	SingleGroup []struct{ Album []mRef } `json:"single_group"`
	Compilation []struct{ Album []mRef } `json:"compilation_group"`
	AppearsOn   []struct{ Album []mRef } `json:"appears_on_group"`
}

func mRefs(kind string, in []mRef) []Ref {
	out := make([]Ref, 0, len(in))
	for _, r := range in {
		out = append(out, Ref{URI: uriOf(kind, r.GID), Name: r.Name})
	}

	return out
}

func (t mTrack) norm() Track {
	out := Track{URI: uriOf("track", t.GID), Name: t.Name, Artists: mRefs("artist", t.Artist), DurationMs: t.Duration}
	if t.Album != nil {
		out.Album = &Ref{URI: uriOf("album", t.Album.GID), Name: t.Album.Name}
		out.Artwork = t.Album.Cover.artwork()
	}

	return out
}

func (a mAlbum) norm() Album {
	out := Album{URI: uriOf("album", a.GID), Name: a.Name, Artists: mRefs("artist", a.Artist), Artwork: a.Cover.artwork()}
	if a.Date != nil && a.Date.Year > 0 {
		out.ReleaseDate = strconv.Itoa(a.Date.Year)
	}

	return out
}

// ---- fetching -----------------------------------------------------------------------

// spclient GETs a path on Spotify's client API through librespot and decodes the JSON.
func (c *Client) spclient(ctx context.Context, path string, v any) error {
	body, err := c.ctl(ctx, http.MethodGet, "/spclient", url.Values{"path": {path}})
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("spclient %s: %w", path, err)
	}

	return nil
}

// metaCache keeps normalised tracks and albums: metadata barely changes, and every
// list view would otherwise refetch the same items one request each.
type metaCache struct {
	mu    sync.Mutex
	order *list.List
	byKey map[string]*list.Element
}

type cacheEntry struct {
	key string
	val any
}

const metaCacheEntries = 2000

func (m *metaCache) get(key string) (any, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if el, ok := m.byKey[key]; ok {
		m.order.MoveToFront(el)

		return el.Value.(cacheEntry).val, true
	}

	return nil, false
}

func (m *metaCache) put(key string, val any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.byKey == nil {
		m.byKey, m.order = map[string]*list.Element{}, list.New()
	}

	if el, ok := m.byKey[key]; ok {
		el.Value = cacheEntry{key, val}
		m.order.MoveToFront(el)

		return
	}

	m.byKey[key] = m.order.PushFront(cacheEntry{key, val})
	for m.order.Len() > metaCacheEntries {
		old := m.order.Back()
		m.order.Remove(old)
		delete(m.byKey, old.Value.(cacheEntry).key)
	}
}

func (c *Client) track(ctx context.Context, id string) (Track, error) {
	if v, ok := c.cache.get("t:" + id); ok {
		return v.(Track), nil
	}

	var m mTrack

	err := c.spclient(ctx, "/metadata/4/track/"+hexID(id), &m)
	if err != nil {
		return Track{}, err
	}

	t := m.norm()
	c.cache.put("t:"+id, t)

	return t, nil
}

func (c *Client) albumMeta(ctx context.Context, id string) (mAlbum, error) {
	if v, ok := c.cache.get("a:" + id); ok {
		return v.(mAlbum), nil
	}

	var m mAlbum

	err := c.spclient(ctx, "/metadata/4/album/"+hexID(id), &m)
	if err != nil {
		return mAlbum{}, err
	}

	c.cache.put("a:"+id, m)

	return m, nil
}

// fetchParallel is how many metadata requests run at once: enough to fill a list in
// a second or two, few enough for a 128 MB speaker.
const fetchParallel = 8

// each runs fn for every id, fetchParallel at a time, and keeps the results in order.
// Items that fail are left out: one track Spotify withdrew must not blank a playlist.
func each[T any](ctx context.Context, ids []string, fn func(context.Context, string) (T, error)) ([]T, error) {
	type res struct {
		v  T
		ok bool
	}

	out := make([]res, len(ids))
	sem := make(chan struct{}, fetchParallel)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)

	for i, id := range ids {
		wg.Add(1)

		sem <- struct{}{}

		safego.Go("spotify.catalogFetch", func() {
			defer wg.Done()
			defer func() { <-sem }()

			v, err := fn(ctx, id)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()

				return
			}

			out[i] = res{v, true}
		})
	}

	wg.Wait()

	list := make([]T, 0, len(ids))

	for _, r := range out {
		if r.ok {
			list = append(list, r.v)
		}
	}
	// Only a total failure is an error: then the session is likely the problem.
	if len(list) == 0 && firstErr != nil && !errors.Is(firstErr, ErrUnavailable) {
		return nil, firstErr
	}

	return list, nil
}

// trackIDs keeps the track ids from a list of URIs; episodes and local files drop out.
func trackIDs(uris []string) []string {
	ids := make([]string, 0, len(uris))
	for _, u := range uris {
		if kind, id, ok := ParseURI(u); ok && kind == "track" {
			ids = append(ids, id)
		}
	}

	return ids
}

func (c *Client) tracks(ctx context.Context, uris []string) ([]Track, error) {
	return each(ctx, trackIDs(uris), c.track)
}

// ---- catalogue ------------------------------------------------------------------------

// pageSize is how many tracks one list request returns.
const pageSize = 50

// Album returns an album and one page of its tracks, and the offset of the next page.
func (c *Client) Album(ctx context.Context, id string, offset int) (Album, int, error) {
	if !ValidID(id) || offset < 0 {
		return Album{}, 0, ErrInvalid
	}

	m, err := c.albumMeta(ctx, id)
	if err != nil {
		return Album{}, 0, err
	}

	out := m.norm()

	var uris []string

	for _, d := range m.Disc {
		for _, t := range d.Track {
			uris = append(uris, uriOf("track", t.GID))
		}
	}

	out.Total = len(uris)
	// Clamp before adding: offset comes from the query string, and MaxInt+pageSize
	// wraps to a negative slice bound.
	offset = min(offset, len(uris))

	page := uris[offset:min(offset+pageSize, len(uris))]
	if out.Tracks, err = c.tracks(ctx, page); err != nil {
		return Album{}, 0, err
	}

	return out, nextOf(offset, len(page), len(uris)), nil
}

// Artist returns an artist, their top tracks and their releases, each marked with its group.
func (c *Client) Artist(ctx context.Context, id string) (ArtistPage, error) {
	if !ValidID(id) {
		return ArtistPage{}, ErrInvalid
	}

	st, _ := c.ctlStatus(ctx)
	country := st.Country

	var m mArtist

	err := c.spclient(ctx, "/metadata/4/artist/"+hexID(id), &m)
	if err != nil {
		return ArtistPage{}, err
	}

	page := ArtistPage{
		Artist:    Artist{URI: uriOf("artist", m.GID), Name: m.Name, Artwork: m.Portrait.artwork()},
		TopTracks: []Track{},
		Albums:    []Album{},
	}

	var top []string
	for _, tt := range m.TopTrack {
		if tt.Country == country || (top == nil && len(tt.Track) > 0) {
			top = top[:0]
			for _, t := range tt.Track {
				top = append(top, uriOf("track", t.GID))
			}

			if tt.Country == country {
				break
			}
		}
	}

	if len(top) > 10 {
		top = top[:10]
	}

	var albumIDs []string

	group := map[string]string{}

	for _, g := range []struct {
		name   string
		limit  int
		groups []struct{ Album []mRef }
	}{
		{kindAlbum, 20, m.AlbumGroup},
		{"single", 10, m.SingleGroup},
		{"compilation", 5, m.Compilation},
		{"appears_on", 5, m.AppearsOn},
	} {
		for i, rel := range g.groups {
			// A group lists one release in its regional variants; the first will do.
			if i < g.limit && len(rel.Album) > 0 {
				albumIDs = append(albumIDs, b62ID(rel.Album[0].GID))
				group[uriOf("album", rel.Album[0].GID)] = g.name
			}
		}
	}
	// The lists are extras: an artist page without them is still an artist page.
	if t, err := c.tracks(ctx, top); err == nil {
		page.TopTracks = t
	}

	if albums, err := each(ctx, albumIDs, func(ctx context.Context, id string) (Album, error) {
		m, err := c.albumMeta(ctx, id)

		return m.norm(), err
	}); err == nil {
		for i := range albums {
			albums[i].Group = group[albums[i].URI]
		}

		page.Albums = albums
	}

	return page, nil
}
