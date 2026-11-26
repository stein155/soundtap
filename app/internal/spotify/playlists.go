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
	Pos       int      `json:"pos"`
	Truncated bool     `json:"truncated"`
	Items     []plItem `json:"items"`
	MetaItems []plMeta `json:"metaItems"`
}

type plItem struct {
	URI string `json:"uri"`
}

type plAttributes struct {
	Name        string          `json:"name"`
	Picture     string          `json:"picture"`
	PictureSize []plPictureSize `json:"pictureSize"`
}

type plPictureSize struct {
	TargetName string `json:"targetName"`
	URL        string `json:"url"`
}

// plPage is a playlist with one page of its contents.
type plPage struct {
	plMeta

	Contents plContents `json:"contents"`
}

// plFirst is the contents of a playlist without its attributes.
type plFirst struct {
	Contents plContents `json:"contents"`
}

// plRootlist is the user's own playlist list.
type plRootlist struct {
	Length   int        `json:"length"`
	Contents plContents `json:"contents"`
}

// plCollection is the user's Liked Songs, as context-resolve answers.
type plCollection struct {
	Pages []plCollectionPage `json:"pages"`
}

// plCollectionPage is one page of Liked Songs.
type plCollectionPage struct {
	Tracks []plItem `json:"tracks"`
}

// pictureBytes is the length of a playlist picture's id, before hex encoding.
const pictureBytes = 20

type plMeta struct {
	Length        int          `json:"length"`
	Attributes    plAttributes `json:"attributes"`
	OwnerUsername string       `json:"ownerUsername"`
}

func (a plAttributes) artwork() string {
	for _, size := range a.PictureSize {
		if size.TargetName == "default" {
			if art := artworkFromURL(size.URL); art != "" {
				return art
			}
		}
	}

	raw, err := base64.StdEncoding.DecodeString(a.Picture)
	if err != nil || len(raw) != pictureBytes {
		return ""
	}

	return artworkPath(hex.EncodeToString(raw))
}

func (m *plMeta) norm(uri string) Playlist {
	return Playlist{
		URI: uri, Name: strings.TrimSpace(m.Attributes.Name), Owner: m.OwnerUsername,
		Artwork: m.Attributes.artwork(), Total: m.Length, Tracks: nil,
	}
}

func nextOf(offset, got, total int) int {
	if offset+got < total && got > 0 {
		return offset + got
	}

	return 0
}

// Playlist returns a playlist and one page of its tracks, and the offset of the next page.
func (c *Client) Playlist(ctx context.Context, playlistID string, offset int) (Playlist, int, error) {
	if !ValidID(playlistID) || offset < 0 {
		return Playlist{}, 0, ErrInvalid
	}

	var page plPage

	path := fmt.Sprintf("/playlist/v2/playlist/%s?decorate=attributes,length,owner&from=%d&length=%d",
		playlistID, offset, pageSize)

	err := c.spclient(ctx, path, &page)
	if err != nil {
		return Playlist{}, 0, err
	}

	out := page.norm("spotify:playlist:" + playlistID)

	uris := make([]string, 0, len(page.Contents.Items))
	for _, item := range page.Contents.Items {
		uris = append(uris, item.URI)
	}

	tracks, err := c.tracks(ctx, uris)
	if err != nil {
		return Playlist{}, 0, err
	}

	out.Tracks = tracks
	if out.Artwork == "" && len(tracks) > 0 {
		out.Artwork = tracks[0].Artwork
	}

	return out, nextOf(offset, len(page.Contents.Items), page.Length), nil
}

// firstCover is the album cover of a playlist's first track: the mixes and radios Spotify
// makes per listener carry no picture of their own.
func (c *Client) firstCover(ctx context.Context, playlistID string) string {
	if cached, ok := c.cache.get("c:" + playlistID); ok {
		if cover, ok := cached.(string); ok {
			return cover
		}
	}

	var first plFirst

	err := c.spclient(ctx, "/playlist/v2/playlist/"+playlistID+"?decorate=length&from=0&length=1", &first)
	if err != nil || len(first.Contents.Items) == 0 {
		return ""
	}

	kind, trackID, _ := ParseURI(first.Contents.Items[0].URI)
	if kind != kindTrack {
		return ""
	}

	track, err := c.track(ctx, trackID)
	if err != nil {
		return ""
	}

	c.cache.put("c:"+playlistID, track.Artwork)

	return track.Artwork
}

// withCovers fills in the playlists that have no picture.
func (c *Client) withCovers(ctx context.Context, lists []Playlist) {
	var (
		missing []int
		ids     []string
	)

	for i := range lists {
		if _, id, ok := ParseURI(lists[i].URI); ok && lists[i].Artwork == "" {
			missing = append(missing, i)
			ids = append(ids, id)
		}
	}

	covers, _ := each(ctx, ids, func(ctx context.Context, playlistID string) (string, error) {
		return c.firstCover(ctx, playlistID), nil
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

	status, err := c.ctlStatus(ctx)
	if err != nil || status.Username == "" {
		return Page[Playlist]{}, ErrNotConnected
	}

	var root plRootlist

	path := fmt.Sprintf(
		"/playlist/v2/user/%s/rootlist?decorate=revision,length,attributes,timestamp,owner&from=%d&length=%d",
		url.PathEscape(status.Username), offset, pageSize)

	err = c.spclient(ctx, path, &root)
	if err != nil {
		return Page[Playlist]{}, err
	}

	out := Page[Playlist]{
		Items: []Playlist{}, Total: root.Length, Next: nextOf(offset, len(root.Contents.Items), root.Length),
	}
	for i, item := range root.Contents.Items {
		if kind, _, ok := ParseURI(item.URI); !ok || kind != kindPlaylist || i >= len(root.Contents.MetaItems) {
			continue // folder markers and anything we cannot name
		}

		out.Items = append(out.Items, root.Contents.MetaItems[i].norm(item.URI))
	}

	c.withCovers(ctx, out.Items)

	return out, nil
}

// LikedTracks returns one page of the user's Liked Songs.
func (c *Client) LikedTracks(ctx context.Context, offset int) (Page[Track], error) {
	if offset < 0 {
		return Page[Track]{}, ErrInvalid
	}

	status, err := c.ctlStatus(ctx)
	if err != nil || status.Username == "" {
		return Page[Track]{}, ErrNotConnected
	}

	var liked plCollection

	err = c.spclient(ctx, "/context-resolve/v1/spotify:user:"+status.Username+":collection", &liked)
	if err != nil {
		return Page[Track]{}, err
	}

	var uris []string

	for _, page := range liked.Pages {
		for _, track := range page.Tracks {
			uris = append(uris, track.URI)
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
