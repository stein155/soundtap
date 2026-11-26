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

	for _, image := range g.Image {
		if image.Size == "DEFAULT" {
			return artworkPath(image.FileID)
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
	Date   *mDate       `json:"date"`
	Disc   []mDisc      `json:"disc"`
}

type mDate struct {
	Year int `json:"year"`
}

type mDisc struct {
	Track []mRef `json:"track"`
}

type mArtist struct {
	GID         string       `json:"gid"`
	Name        string       `json:"name"`
	Portrait    *mImageGroup `json:"portrait_group"`
	TopTrack    []mTopTrack  `json:"top_track"`
	AlbumGroup  []mRelease   `json:"album_group"`
	SingleGroup []mRelease   `json:"single_group"`
	Compilation []mRelease   `json:"compilation_group"`
	AppearsOn   []mRelease   `json:"appears_on_group"`
}

type mTopTrack struct {
	Country string `json:"country"`
	Track   []mRef `json:"track"`
}

// mRelease is one release of an artist, in its regional variants. Its key has no tag of
// its own on the wire: encoding/json matches "Album" without regard to case.
type mRelease struct {
	Album []mRef `json:"Album"`
}

func mRefs(kind string, in []mRef) []Ref {
	out := make([]Ref, 0, len(in))
	for _, ref := range in {
		out = append(out, Ref{URI: uriOf(kind, ref.GID), Name: ref.Name})
	}

	return out
}

func (t *mTrack) norm() Track {
	out := Track{
		URI: uriOf(kindTrack, t.GID), Name: t.Name, Artists: mRefs(kindArtist, t.Artist),
		Album: nil, Artwork: "", DurationMs: t.Duration,
	}
	if t.Album != nil {
		out.Album = &Ref{URI: uriOf(kindAlbum, t.Album.GID), Name: t.Album.Name}
		out.Artwork = t.Album.Cover.artwork()
	}

	return out
}

func (a *mAlbum) norm() Album {
	out := Album{
		URI: uriOf(kindAlbum, a.GID), Name: a.Name, Artists: mRefs(kindArtist, a.Artist), Artwork: a.Cover.artwork(),
		ReleaseDate: "", Group: "", Total: 0, Tracks: nil,
	}
	if a.Date != nil && a.Date.Year > 0 {
		out.ReleaseDate = strconv.Itoa(a.Date.Year)
	}

	return out
}

// ---- fetching -----------------------------------------------------------------------

// spclient GETs a path on Spotify's client API through librespot and decodes the JSON.
func (c *Client) spclient(ctx context.Context, path string, into any) error {
	body, err := c.ctl(ctx, http.MethodGet, "/spclient", url.Values{"path": {path}})
	if err != nil {
		return err
	}

	err = json.Unmarshal(body, into)
	if err != nil {
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

	element, ok := m.byKey[key]
	if !ok {
		return nil, false
	}

	m.order.MoveToFront(element)

	entry, ok := element.Value.(cacheEntry)

	return entry.val, ok
}

func (m *metaCache) put(key string, val any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.byKey == nil {
		m.byKey, m.order = map[string]*list.Element{}, list.New()
	}

	if element, ok := m.byKey[key]; ok {
		element.Value = cacheEntry{key: key, val: val}
		m.order.MoveToFront(element)

		return
	}

	m.byKey[key] = m.order.PushFront(cacheEntry{key: key, val: val})
	for m.order.Len() > metaCacheEntries {
		old := m.order.Back()
		m.order.Remove(old)

		if entry, ok := old.Value.(cacheEntry); ok {
			delete(m.byKey, entry.key)
		}
	}
}

func (c *Client) track(ctx context.Context, trackID string) (Track, error) {
	if cached, ok := c.cache.get("t:" + trackID); ok {
		if track, isTrack := cached.(Track); isTrack {
			return track, nil
		}
	}

	var meta mTrack

	err := c.spclient(ctx, "/metadata/4/track/"+hexID(trackID), &meta)
	if err != nil {
		return Track{}, err
	}

	track := meta.norm()
	c.cache.put("t:"+trackID, track)

	return track, nil
}

func (c *Client) albumMeta(ctx context.Context, albumID string) (mAlbum, error) {
	if cached, ok := c.cache.get("a:" + albumID); ok {
		if meta, isAlbum := cached.(mAlbum); isAlbum {
			return meta, nil
		}
	}

	var meta mAlbum

	err := c.spclient(ctx, "/metadata/4/album/"+hexID(albumID), &meta)
	if err != nil {
		return mAlbum{}, err
	}

	c.cache.put("a:"+albumID, meta)

	return meta, nil
}

// fetchParallel is how many metadata requests run at once: enough to fill a list in
// a second or two, few enough for a 128 MB speaker.
const fetchParallel = 8

// each runs fn for every id, fetchParallel at a time, and keeps the results in order.
// Items that fail are left out: one track Spotify withdrew must not blank a playlist.
func each[T any](ctx context.Context, ids []string, fetch func(context.Context, string) (T, error)) ([]T, error) {
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

	for index, itemID := range ids {
		wg.Add(1)

		sem <- struct{}{}

		safego.Go("spotify.catalogFetch", func() {
			defer wg.Done()
			defer func() { <-sem }()

			value, err := fetch(ctx, itemID)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()

				return
			}

			out[index] = res{v: value, ok: true}
		})
	}

	wg.Wait()

	kept := make([]T, 0, len(ids))

	for _, r := range out {
		if r.ok {
			kept = append(kept, r.v)
		}
	}
	// Only a total failure is an error: then the session is likely the problem.
	if len(kept) == 0 && firstErr != nil && !errors.Is(firstErr, ErrUnavailable) {
		return nil, firstErr
	}

	return kept, nil
}

// trackIDs keeps the track ids from a list of URIs; episodes and local files drop out.
func trackIDs(uris []string) []string {
	ids := make([]string, 0, len(uris))
	for _, uri := range uris {
		if kind, id, ok := ParseURI(uri); ok && kind == kindTrack {
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
func (c *Client) Album(ctx context.Context, albumID string, offset int) (Album, int, error) {
	if !ValidID(albumID) || offset < 0 {
		return Album{}, 0, ErrInvalid
	}

	meta, err := c.albumMeta(ctx, albumID)
	if err != nil {
		return Album{}, 0, err
	}

	out := meta.norm()

	var uris []string

	for _, disc := range meta.Disc {
		for _, track := range disc.Track {
			uris = append(uris, uriOf(kindTrack, track.GID))
		}
	}

	out.Total = len(uris)
	// Clamp before adding: offset comes from the query string, and MaxInt+pageSize
	// wraps to a negative slice bound.
	offset = min(offset, len(uris))

	page := uris[offset:min(offset+pageSize, len(uris))]

	out.Tracks, err = c.tracks(ctx, page)
	if err != nil {
		return Album{}, 0, err
	}

	return out, nextOf(offset, len(page), len(uris)), nil
}

// How many of an artist's top tracks and of each kind of release the page shows.
const (
	maxTopTracks      = 10
	albumsShown       = 20
	singlesShown      = 10
	compilationsShown = 5
	appearancesShown  = 5
)

// Artist returns an artist, their top tracks and their releases, each marked with its group.
func (c *Client) Artist(ctx context.Context, artistID string) (ArtistPage, error) {
	if !ValidID(artistID) {
		return ArtistPage{}, ErrInvalid
	}

	status, _ := c.ctlStatus(ctx)

	var meta mArtist

	err := c.spclient(ctx, "/metadata/4/artist/"+hexID(artistID), &meta)
	if err != nil {
		return ArtistPage{}, err
	}

	page := ArtistPage{
		Artist:    Artist{URI: uriOf(kindArtist, meta.GID), Name: meta.Name, Artwork: meta.Portrait.artwork()},
		TopTracks: []Track{},
		Albums:    []Album{},
	}

	top := topTrackURIs(meta.TopTrack, status.Country)
	albumIDs, group := releases(&meta)
	// The lists are extras: an artist page without them is still an artist page.
	tracks, err := c.tracks(ctx, top)
	if err == nil {
		page.TopTracks = tracks
	}

	albums, err := each(ctx, albumIDs, func(ctx context.Context, albumID string) (Album, error) {
		album, fetchErr := c.albumMeta(ctx, albumID)

		return album.norm(), fetchErr
	})
	if err == nil {
		for i := range albums {
			albums[i].Group = group[albums[i].URI]
		}

		page.Albums = albums
	}

	return page, nil
}

// topTrackURIs picks the top tracks for country, or the first non-empty list when the
// artist has none for it, at most maxTopTracks.
func topTrackURIs(lists []mTopTrack, country string) []string {
	var top []string

	for _, ranked := range lists {
		if ranked.Country == country || (top == nil && len(ranked.Track) > 0) {
			top = top[:0]
			for _, track := range ranked.Track {
				top = append(top, uriOf(kindTrack, track.GID))
			}

			if ranked.Country == country {
				break
			}
		}
	}

	if len(top) > maxTopTracks {
		top = top[:maxTopTracks]
	}

	return top
}

// releaseGroup is one kind of release on an artist page, and how many of it to show.
type releaseGroup struct {
	name     string
	limit    int
	releases []mRelease
}

// releases lists the ids of the releases an artist page shows, and each one's group by
// album URI.
func releases(meta *mArtist) ([]string, map[string]string) {
	var albumIDs []string

	group := map[string]string{}

	for _, kind := range []releaseGroup{
		{name: kindAlbum, limit: albumsShown, releases: meta.AlbumGroup},
		{name: "single", limit: singlesShown, releases: meta.SingleGroup},
		{name: "compilation", limit: compilationsShown, releases: meta.Compilation},
		{name: "appears_on", limit: appearancesShown, releases: meta.AppearsOn},
	} {
		for i, release := range kind.releases {
			// A group lists one release in its regional variants; the first will do.
			if i < kind.limit && len(release.Album) > 0 {
				albumIDs = append(albumIDs, b62ID(release.Album[0].GID))
				group[uriOf(kindAlbum, release.Album[0].GID)] = kind.name
			}
		}
	}

	return albumIDs, group
}
