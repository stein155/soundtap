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
	URI      string    `json:"uri"`
	Name     string    `json:"name"`
	Image    string    `json:"image"`
	Duration int       `json:"duration"`
	Artists  []Ref     `json:"artists"`
	Album    *Ref      `json:"album"`
	Author   *svAuthor `json:"author"`
}

type svAuthor struct {
	Name string `json:"name"`
}

type svResult struct {
	Results svResults `json:"results"`
}

type svResults struct {
	Tracks    svHits `json:"tracks"`
	Artists   svHits `json:"artists"`
	Albums    svHits `json:"albums"`
	Playlists svHits `json:"playlists"`
}

// svHits is one kind's hits. Its key has no tag of its own on the wire: encoding/json
// matches "Hits" without regard to case.
type svHits struct {
	Hits []svHit `json:"Hits"`
}

// maxQuery is the longest search query passed on.
const maxQuery = 200

func kindOf(uri string) string {
	kind, _, ok := ParseURI(uri)
	if !ok {
		return ""
	}

	return kind
}

func normSearch(result *svResult) SearchResult {
	return SearchResult{
		Tracks:    searchTracks(result.Results.Tracks.Hits),
		Artists:   searchArtists(result.Results.Artists.Hits),
		Albums:    searchAlbums(result.Results.Albums.Hits),
		Playlists: searchPlaylists(result.Results.Playlists.Hits),
	}
}

func searchTracks(hits []svHit) []Track {
	out := []Track{}

	for i := range hits {
		hit := &hits[i]
		if kindOf(hit.URI) != kindTrack {
			continue
		}

		track := Track{
			URI: hit.URI, Name: hit.Name, Artists: hit.Artists, Album: nil,
			Artwork: artworkFromURL(hit.Image), DurationMs: hit.Duration,
		}
		if track.Artists == nil {
			track.Artists = []Ref{}
		}

		if hit.Album != nil && kindOf(hit.Album.URI) == kindAlbum {
			track.Album = hit.Album
		}

		out = append(out, track)
	}

	return out
}

func searchArtists(hits []svHit) []Artist {
	out := []Artist{}

	for i := range hits {
		hit := &hits[i]
		if kindOf(hit.URI) == kindArtist {
			out = append(out, Artist{URI: hit.URI, Name: hit.Name, Artwork: artworkFromURL(hit.Image)})
		}
	}

	return out
}

func searchAlbums(hits []svHit) []Album {
	out := []Album{}

	for i := range hits {
		hit := &hits[i]
		if kindOf(hit.URI) != kindAlbum {
			continue
		}

		album := Album{
			URI: hit.URI, Name: hit.Name, Artists: hit.Artists, Artwork: artworkFromURL(hit.Image),
			ReleaseDate: "", Group: "", Total: 0, Tracks: nil,
		}
		if album.Artists == nil {
			album.Artists = []Ref{}
		}

		out = append(out, album)
	}

	return out
}

func searchPlaylists(hits []svHit) []Playlist {
	out := []Playlist{}

	for i := range hits {
		hit := &hits[i]
		// searchview still spells some playlists spotify:user:<owner>:playlist:<id>.
		uri := hit.URI
		if at := strings.Index(uri, ":playlist:"); at > 0 {
			uri = "spotify" + uri[at:]
		}

		if kindOf(uri) != kindPlaylist {
			continue
		}

		playlist := Playlist{
			URI: uri, Name: hit.Name, Owner: "", Artwork: artworkFromURL(hit.Image), Total: 0, Tracks: nil,
		}
		if hit.Author != nil {
			playlist.Owner = hit.Author.Name
		}

		out = append(out, playlist)
	}

	return out
}

// Search returns up to 10 of each kind.
func (c *Client) Search(ctx context.Context, query string) (SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > maxQuery {
		return SearchResult{}, ErrInvalid
	}

	status, err := c.ctlStatus(ctx)
	if err != nil {
		return SearchResult{}, ErrNotConnected
	}

	if status.State != "connected" {
		return SearchResult{}, errFromState(status.State)
	}
	// No username: librespot logs a failing Mercury URI in full, and the log ends up in
	// /tmp/soundtap.log and debug bundles. Searchview answered the same with and without it.
	params := url.Values{
		"entityVersion": {"2"}, "limit": {"10"}, "imageSize": {"large"}, "catalogue": {""},
		"country": {status.Country}, "locale": {"en"},
	}
	uri := "hm://searchview/km/v4/search/" + url.PathEscape(query) + "?" + params.Encode()

	body, err := c.ctl(ctx, http.MethodGet, "/mercury", url.Values{"uri": {uri}})
	if err != nil {
		if errors.Is(err, ErrNotConnected) || errors.Is(err, ErrNotAuthenticated) {
			return SearchResult{}, err
		}

		return SearchResult{}, errors.Join(ErrSearchUnavailable, err)
	}

	var result svResult

	err = json.Unmarshal(body, &result)
	if err != nil {
		return SearchResult{}, errors.Join(ErrSearchUnavailable, err)
	}

	return normSearch(&result), nil
}
