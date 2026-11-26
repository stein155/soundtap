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
	status, err := c.ctlStatus(ctx)
	if err != nil || status.Username == "" {
		return "", ErrNotConnected
	}

	return url.PathEscape(status.Username), nil
}

// following is the answer of user-profile-view's following list.
type following struct {
	Profiles []followedProfile `json:"profiles"`
}

// followedProfile is one profile the user follows.
type followedProfile struct {
	URI      string `json:"uri"`
	Name     string `json:"name"`
	ImageURL string `json:"image_url"`
}

// recentlyPlayed is the answer of recently-played.
type recentlyPlayed struct {
	PlayContexts []playContext `json:"playContexts"`
}

// playContext is one context the user played.
type playContext struct {
	URI string `json:"uri"`
}

// FollowedArtists returns the artists the user follows.
func (c *Client) FollowedArtists(ctx context.Context) ([]Artist, error) {
	user, err := c.username(ctx)
	if err != nil {
		return nil, err
	}

	var followed following

	err = c.spclient(ctx, "/user-profile-view/v3/profile/"+user+"/following?market=from_token", &followed)
	if err != nil {
		return nil, err
	}

	out := make([]Artist, 0, len(followed.Profiles))
	for _, profile := range followed.Profiles {
		if kind, _, ok := ParseURI(profile.URI); ok && kind == kindArtist {
			out = append(out, Artist{URI: profile.URI, Name: profile.Name, Artwork: artworkFromURL(profile.ImageURL)})
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

	var played recentlyPlayed

	path := fmt.Sprintf("/recently-played/v3/user/%s/recently-played?format=json&offset=0&limit=%d&filter=default",
		user, recentSize)

	err = c.spclient(ctx, path, &played)
	if err != nil {
		return nil, err
	}

	uris := make([]string, 0, len(played.PlayContexts))
	for _, entry := range played.PlayContexts {
		uris = append(uris, entry.URI)
	}

	return each(ctx, uris, c.recent)
}

func (c *Client) recent(ctx context.Context, uri string) (Recent, error) {
	kind, itemID, _ := ParseURI(uri)

	out := Recent{Kind: kind, URI: uri, Name: "", Artwork: ""}
	switch kind {
	case kindAlbum:
		meta, err := c.albumMeta(ctx, itemID)
		if err != nil {
			return Recent{}, err
		}

		album := meta.norm()
		out.Name, out.Artwork = album.Name, album.Artwork
	case kindArtist:
		var meta mArtist

		err := c.spclient(ctx, "/metadata/4/artist/"+hexID(itemID), &meta)
		if err != nil {
			return Recent{}, err
		}

		out.Name, out.Artwork = meta.Name, meta.Portrait.artwork()
	case kindPlaylist:
		var meta plMeta
		// One track is the smallest answer that still carries the name and picture.
		err := c.spclient(ctx,
			"/playlist/v2/playlist/"+itemID+"?decorate=attributes,length,owner&from=0&length=1", &meta)
		if err != nil {
			return Recent{}, err
		}

		playlist := meta.norm(uri)

		out.Name, out.Artwork = playlist.Name, playlist.Artwork
		if out.Artwork == "" {
			out.Artwork = c.firstCover(ctx, itemID)
		}
	default:
		return Recent{}, ErrUnavailable
	}

	return out, nil
}
