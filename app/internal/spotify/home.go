// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package spotify

import (
	"context"
	"fmt"
	"net/url"
)

const (
	kindAlbum    = "album"
	kindArtist   = "artist"
	kindPlaylist = "playlist"
	kindTrack    = "track"

	// recentSize is how many recent plays the home screen asks for.
	recentSize = 10
)

// Recent is something the user played lately, ready to open.
type Recent struct {
	Kind    string `json:"kind"` // album, artist or playlist
	URI     string `json:"uri"`
	Name    string `json:"name"`
	Artwork string `json:"artwork,omitempty"`
}

func (c *Client) username(ctx context.Context) (string, error) {
	st, err := c.ctlStatus(ctx)
	if err != nil || st.Username == "" {
		return "", ErrNotConnected
	}

	return url.PathEscape(st.Username), nil
}

// FollowedArtists returns the artists the user follows.
func (c *Client) FollowedArtists(ctx context.Context) ([]Artist, error) {
	user, err := c.username(ctx)
	if err != nil {
		return nil, err
	}

	var w struct {
		Profiles []struct {
			URI      string `json:"uri"`
			Name     string `json:"name"`
			ImageURL string `json:"image_url"`
		} `json:"profiles"`
	}
	if err := c.spclient(ctx, "/user-profile-view/v3/profile/"+user+"/following?market=from_token", &w); err != nil {
		return nil, err
	}

	out := make([]Artist, 0, len(w.Profiles))
	for _, p := range w.Profiles {
		if kind, _, ok := ParseURI(p.URI); ok && kind == kindArtist {
			out = append(out, Artist{URI: p.URI, Name: p.Name, Artwork: artworkFromURL(p.ImageURL)})
		}
	}

	return out, nil
}

// RecentlyPlayed returns what the user played lately, newest first. Tracks and the Liked
// Songs context drop out: only what has a page of its own can be opened again.
func (c *Client) RecentlyPlayed(ctx context.Context) ([]Recent, error) {
	user, err := c.username(ctx)
	if err != nil {
		return nil, err
	}

	var w struct {
		PlayContexts []struct {
			URI string `json:"uri"`
		} `json:"playContexts"`
	}

	path := fmt.Sprintf("/recently-played/v3/user/%s/recently-played?format=json&offset=0&limit=%d&filter=default", user, recentSize)
	if err := c.spclient(ctx, path, &w); err != nil {
		return nil, err
	}

	uris := make([]string, 0, len(w.PlayContexts))
	for _, p := range w.PlayContexts {
		uris = append(uris, p.URI)
	}

	return each(ctx, uris, c.recent)
}

func (c *Client) recent(ctx context.Context, uri string) (Recent, error) {
	kind, id, _ := ParseURI(uri)

	out := Recent{Kind: kind, URI: uri}
	switch kind {
	case kindAlbum:
		m, err := c.albumMeta(ctx, id)
		if err != nil {
			return Recent{}, err
		}

		a := m.norm()
		out.Name, out.Artwork = a.Name, a.Artwork
	case kindArtist:
		var m mArtist

		err := c.spclient(ctx, "/metadata/4/artist/"+hexID(id), &m)
		if err != nil {
			return Recent{}, err
		}

		out.Name, out.Artwork = m.Name, m.Portrait.artwork()
	case kindPlaylist:
		var m plMeta
		// One track is the smallest answer that still carries the name and picture.
		err := c.spclient(ctx, "/playlist/v2/playlist/"+id+"?decorate=attributes,length,owner&from=0&length=1", &m)
		if err != nil {
			return Recent{}, err
		}

		p := m.norm(uri)

		out.Name, out.Artwork = p.Name, p.Artwork
		if out.Artwork == "" {
			out.Artwork = c.firstCover(ctx, id)
		}
	default:
		return Recent{}, ErrUnavailable
	}

	return out, nil
}
