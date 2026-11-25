// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ErrSearchUnavailable means Spotify refused every search route this session has.
var ErrSearchUnavailable = errors.New("spotify_search_unavailable")

// Search results come from searchview over the session's Mercury channel, the route
// Spotify's own older clients use; no route the public API offers accepts
// librespot's identity.

type svHit struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	Image    string `json:"image"`
	Duration int    `json:"duration"`
	Artists  []Ref  `json:"artists"`
	Album    *Ref   `json:"album"`
	Author   *struct {
		Name string `json:"name"`
	} `json:"author"`
}

type svResult struct {
	Results struct {
		Tracks    struct{ Hits []svHit } `json:"tracks"`
		Artists   struct{ Hits []svHit } `json:"artists"`
		Albums    struct{ Hits []svHit } `json:"albums"`
		Playlists struct{ Hits []svHit } `json:"playlists"`
	} `json:"results"`
}

func kindOf(uri string) string {
	kind, _, ok := ParseURI(uri)
	if !ok {
		return ""
	}

	return kind
}

func normSearch(w svResult) SearchResult {
	out := SearchResult{Tracks: []Track{}, Artists: []Artist{}, Albums: []Album{}, Playlists: []Playlist{}}
	for _, h := range w.Results.Tracks.Hits {
		if kindOf(h.URI) != "track" {
			continue
		}

		t := Track{URI: h.URI, Name: h.Name, Artists: h.Artists, DurationMs: h.Duration, Artwork: artworkFromURL(h.Image)}
		if t.Artists == nil {
			t.Artists = []Ref{}
		}

		if h.Album != nil && kindOf(h.Album.URI) == "album" {
			t.Album = h.Album
		}

		out.Tracks = append(out.Tracks, t)
	}

	for _, h := range w.Results.Artists.Hits {
		if kindOf(h.URI) == "artist" {
			out.Artists = append(out.Artists, Artist{URI: h.URI, Name: h.Name, Artwork: artworkFromURL(h.Image)})
		}
	}

	for _, h := range w.Results.Albums.Hits {
		if kindOf(h.URI) == "album" {
			a := Album{URI: h.URI, Name: h.Name, Artists: h.Artists, Artwork: artworkFromURL(h.Image)}
			if a.Artists == nil {
				a.Artists = []Ref{}
			}

			out.Albums = append(out.Albums, a)
		}
	}

	for _, h := range w.Results.Playlists.Hits {
		// searchview still spells some playlists spotify:user:<owner>:playlist:<id>.
		uri := h.URI
		if i := strings.Index(uri, ":playlist:"); i > 0 {
			uri = "spotify" + uri[i:]
		}

		if kindOf(uri) == "playlist" {
			p := Playlist{URI: uri, Name: h.Name, Artwork: artworkFromURL(h.Image)}
			if h.Author != nil {
				p.Owner = h.Author.Name
			}

			out.Playlists = append(out.Playlists, p)
		}
	}

	return out
}

// Search returns up to 10 of each kind.
func (c *Client) Search(ctx context.Context, query string) (SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 200 {
		return SearchResult{}, ErrInvalid
	}

	st, err := c.ctlStatus(ctx)
	if err != nil {
		return SearchResult{}, ErrNotConnected
	}

	if st.State != "connected" {
		return SearchResult{}, errFromState(st.State)
	}
	// No username: librespot logs a failing Mercury URI in full, and the log ends up in
	// /tmp/soundtap.log and debug bundles. Searchview answered the same with and without it.
	q := url.Values{
		"entityVersion": {"2"}, "limit": {"10"}, "imageSize": {"large"}, "catalogue": {""},
		"country": {st.Country}, "locale": {"en"},
	}
	uri := "hm://searchview/km/v4/search/" + url.PathEscape(query) + "?" + q.Encode()

	body, err := c.ctl(ctx, http.MethodGet, "/mercury", url.Values{"uri": {uri}})
	if err != nil {
		if errors.Is(err, ErrNotConnected) || errors.Is(err, ErrNotAuthenticated) {
			return SearchResult{}, err
		}

		return SearchResult{}, errors.Join(ErrSearchUnavailable, err)
	}

	var w svResult
	if err := json.Unmarshal(body, &w); err != nil {
		return SearchResult{}, errors.Join(ErrSearchUnavailable, err)
	}

	return normSearch(w), nil
}
