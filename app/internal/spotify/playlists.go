// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package spotify

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

type plContents struct {
	Pos       int  `json:"pos"`
	Truncated bool `json:"truncated"`
	Items     []struct {
		URI string `json:"uri"`
	} `json:"items"`
	MetaItems []plMeta `json:"metaItems"`
}

type plAttributes struct {
	Name        string `json:"name"`
	Picture     string `json:"picture"`
	PictureSize []struct {
		TargetName string `json:"targetName"`
		URL        string `json:"url"`
	} `json:"pictureSize"`
}

type plMeta struct {
	Length        int          `json:"length"`
	Attributes    plAttributes `json:"attributes"`
	OwnerUsername string       `json:"ownerUsername"`
}

func (a plAttributes) artwork() string {
	for _, p := range a.PictureSize {
		if p.TargetName == "default" {
			if art := artworkFromURL(p.URL); art != "" {
				return art
			}
		}
	}
	if raw, err := base64.StdEncoding.DecodeString(a.Picture); err == nil && len(raw) == 20 {
		return artworkPath(hex.EncodeToString(raw))
	}
	return ""
}

func (m plMeta) norm(uri string) Playlist {
	return Playlist{URI: uri, Name: strings.TrimSpace(m.Attributes.Name), Owner: m.OwnerUsername,
		Artwork: m.Attributes.artwork(), Total: m.Length}
}

func nextOf(offset, got, total int) int {
	if offset+got < total && got > 0 {
		return offset + got
	}
	return 0
}

// Playlist returns a playlist and one page of its tracks, and the offset of the next page.
func (c *Client) Playlist(ctx context.Context, id string, offset int) (Playlist, int, error) {
	if !ValidID(id) || offset < 0 {
		return Playlist{}, 0, ErrInvalid
	}
	var w struct {
		plMeta
		Contents plContents `json:"contents"`
	}
	path := fmt.Sprintf("/playlist/v2/playlist/%s?decorate=attributes,length,owner&from=%d&length=%d", id, offset, pageSize)
	if err := c.spclient(ctx, path, &w); err != nil {
		return Playlist{}, 0, err
	}
	out := w.norm("spotify:playlist:" + id)
	uris := make([]string, 0, len(w.Contents.Items))
	for _, it := range w.Contents.Items {
		uris = append(uris, it.URI)
	}
	tracks, err := c.tracks(ctx, uris)
	if err != nil {
		return Playlist{}, 0, err
	}
	out.Tracks = tracks
	if out.Artwork == "" && len(tracks) > 0 {
		out.Artwork = tracks[0].Artwork
	}
	return out, nextOf(offset, len(w.Contents.Items), w.Length), nil
}

// firstCover is the album cover of a playlist's first track: the mixes and radios Spotify
// makes per listener carry no picture of their own.
func (c *Client) firstCover(ctx context.Context, id string) string {
	if v, ok := c.cache.get("c:" + id); ok {
		if cover, ok := v.(string); ok {
			return cover
		}
	}
	var w struct {
		Contents plContents `json:"contents"`
	}
	if err := c.spclient(ctx, "/playlist/v2/playlist/"+id+"?decorate=length&from=0&length=1", &w); err != nil || len(w.Contents.Items) == 0 {
		return ""
	}
	kind, track, _ := ParseURI(w.Contents.Items[0].URI)
	if kind != kindTrack {
		return ""
	}
	t, err := c.track(ctx, track)
	if err != nil {
		return ""
	}
	c.cache.put("c:"+id, t.Artwork)
	return t.Artwork
}

// withCovers fills in the playlists that have no picture.
func (c *Client) withCovers(ctx context.Context, lists []Playlist) {
	var missing []int
	var ids []string
	for i, p := range lists {
		if _, id, ok := ParseURI(p.URI); ok && p.Artwork == "" {
			missing = append(missing, i)
			ids = append(ids, id)
		}
	}
	covers, _ := each(ctx, ids, func(ctx context.Context, id string) (string, error) {
		return c.firstCover(ctx, id), nil
	})
	for k, cover := range covers {
		lists[missing[k]].Artwork = cover
	}
}

// MyPlaylists returns one page of the user's own playlist list (folders are flattened out).
func (c *Client) MyPlaylists(ctx context.Context, offset int) (Page[Playlist], error) {
	if offset < 0 {
		return Page[Playlist]{}, ErrInvalid
	}
	st, err := c.ctlStatus(ctx)
	if err != nil || st.Username == "" {
		return Page[Playlist]{}, ErrNotConnected
	}
	var w struct {
		Length   int        `json:"length"`
		Contents plContents `json:"contents"`
	}
	path := fmt.Sprintf("/playlist/v2/user/%s/rootlist?decorate=revision,length,attributes,timestamp,owner&from=%d&length=%d",
		url.PathEscape(st.Username), offset, pageSize)
	if err := c.spclient(ctx, path, &w); err != nil {
		return Page[Playlist]{}, err
	}
	out := Page[Playlist]{Items: []Playlist{}, Total: w.Length, Next: nextOf(offset, len(w.Contents.Items), w.Length)}
	for i, it := range w.Contents.Items {
		if kind, _, ok := ParseURI(it.URI); !ok || kind != kindPlaylist || i >= len(w.Contents.MetaItems) {
			continue // folder markers and anything we cannot name
		}
		out.Items = append(out.Items, w.Contents.MetaItems[i].norm(it.URI))
	}
	c.withCovers(ctx, out.Items)
	return out, nil
}

// LikedTracks returns one page of the user's Liked Songs.
func (c *Client) LikedTracks(ctx context.Context, offset int) (Page[Track], error) {
	if offset < 0 {
		return Page[Track]{}, ErrInvalid
	}
	st, err := c.ctlStatus(ctx)
	if err != nil || st.Username == "" {
		return Page[Track]{}, ErrNotConnected
	}
	var w struct {
		Pages []struct {
			Tracks []struct {
				URI string `json:"uri"`
			} `json:"tracks"`
		} `json:"pages"`
	}
	if err := c.spclient(ctx, "/context-resolve/v1/spotify:user:"+st.Username+":collection", &w); err != nil {
		return Page[Track]{}, err
	}
	var uris []string
	for _, p := range w.Pages {
		for _, t := range p.Tracks {
			uris = append(uris, t.URI)
		}
	}
	total := len(uris)
	if offset > total {
		offset = total
	}
	end := min(offset+pageSize, total)
	tracks, err := c.tracks(ctx, uris[offset:end])
	if err != nil {
		return Page[Track]{}, err
	}
	return Page[Track]{Items: tracks, Total: total, Next: nextOf(offset, end-offset, total)}, nil
}
