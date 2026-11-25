// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package spotify

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

const (
	trackB62 = "0DiWol3AO6WpXZgp0goxAV"
	trackHex = "14d4763313df47a1b5878c513b47c9f5"
	albumHex = "4e1d738f069c4f6890aad47f4bde4996"
	cover    = "ab67616d00001e021e81bff9807a9e629fce5ade"
)

func TestIDConversion(t *testing.T) {
	if got := hexID(trackB62); got != trackHex {
		t.Fatalf("hexID = %s, want %s", got, trackHex)
	}

	if got := b62ID(trackHex); got != trackB62 {
		t.Fatalf("b62ID = %s, want %s", got, trackB62)
	}

	if got := b62ID("00000000000000000000000000000001"); len(got) != 22 {
		t.Fatalf("b62ID keeps leading zeros: %q", got)
	}
}

func trackJSON(gid, name string) string {
	return `{"gid":"` + gid + `","name":"` + name + `","duration":320357,
	  "artist":[{"gid":"9352616e1b6a4a5fbe8764a1648b51ac","name":"Daft Punk"}],
	  "album":{"gid":"` + albumHex + `","name":"Discovery",
	    "cover_group":{"image":[{"file_id":"ab67616d000048511e81bff9807a9e629fce5ade","size":"SMALL"},
	                            {"file_id":"` + cover + `","size":"DEFAULT"}]}}}`
}

func catalogCtl() *fakeCtl {
	return &fakeCtl{state: "connected", user: "someone", spclient: map[string]string{
		"/metadata/4/track/" + trackHex: trackJSON(trackHex, "One More Time"),
		"/metadata/4/album/" + albumHex: `{"gid":"` + albumHex + `","name":"Discovery","date":{"year":2001},
		  "artist":[{"gid":"9352616e1b6a4a5fbe8764a1648b51ac","name":"Daft Punk"}],
		  "cover_group":{"image":[{"file_id":"` + cover + `","size":"DEFAULT"}]},
		  "disc":[{"track":[{"gid":"` + trackHex + `"},{"gid":"ffffffffffffffffffffffffffffffff"}]}]}`,
		"/playlist/v2/user/someone/rootlist?decorate=revision,length,attributes,timestamp,owner&from=0&length=50": `{
		  "length":3,"contents":{"items":[
		    {"uri":"spotify:start-group:abc:Folder"},
		    {"uri":"spotify:playlist:37i9dQZF1DX4UtSsGT1Sbe"},
		    {"uri":"spotify:playlist:7cK1eoApNSJusbpZgFQXP2"}],
		  "metaItems":[{},
		    {"length":150,"ownerUsername":"spotify","attributes":{"name":"All Out 80s",
		      "pictureSize":[{"targetName":"default","url":"https://i.scdn.co/image/ab67706f00000002fb8e01f2c022ab1a32e2c5a0"}]}},
		    {"length":20,"ownerUsername":"me","attributes":{"name":"Mine ","picture":"q2dwbAAA2oQcxoKQ5d3KTGsf2NY="}}]}}`,
		"/context-resolve/v1/spotify:user:someone:collection": `{"pages":[{"tracks":[
		  {"uri":"spotify:track:` + trackB62 + `"},{"uri":"spotify:local:::x:1"},{"uri":"spotify:episode:` + trackB62 + `"}]}]}`,
	}}
}

func TestAlbumFromMetadata(t *testing.T) {
	c := newTest(t, catalogCtl())

	a, next, err := c.Album(context.Background(), "2noRn2Aes5aoNVsU6iWThc", 0)
	if err != nil || next != 0 || a.Total != 2 {
		t.Fatal(next, a.Total, err)
	}

	if a.Name != "Discovery" || a.ReleaseDate != "2001" || a.Artwork != "/api/spotify/images/"+cover {
		t.Fatalf("album = %+v", a)
	}
	// The second track is withdrawn (404): the album still lists the rest.
	if len(a.Tracks) != 1 || a.Tracks[0].URI != "spotify:track:"+trackB62 || a.Tracks[0].Artists[0].Name != "Daft Punk" {
		t.Fatalf("tracks = %+v", a.Tracks)
	}
}

// An album longer than one page says where the next page starts.
func TestAlbumPagesPastFifty(t *testing.T) {
	ctl := catalogCtl()

	var tracks []string
	for range pageSize + 1 {
		tracks = append(tracks, `{"gid":"`+trackHex+`"}`)
	}

	ctl.spclient["/metadata/4/album/"+albumHex] = `{"gid":"` + albumHex + `","name":"Box",
	  "disc":[{"track":[` + strings.Join(tracks[:30], ",") + `]},{"track":[` + strings.Join(tracks[30:], ",") + `]}]}`
	c := newTest(t, ctl)

	a, next, err := c.Album(context.Background(), "2noRn2Aes5aoNVsU6iWThc", 0)
	if err != nil || len(a.Tracks) != pageSize || next != pageSize || a.Total != pageSize+1 {
		t.Fatalf("first page: %d tracks, next %d, total %d, %v", len(a.Tracks), next, a.Total, err)
	}

	a, next, err = c.Album(context.Background(), "2noRn2Aes5aoNVsU6iWThc", next)
	if err != nil || len(a.Tracks) != 1 || next != 0 {
		t.Fatalf("second page: %d tracks, next %d, %v", len(a.Tracks), next, err)
	}
}

// An offset from the query string near MaxInt is an empty page, not a panic.
func TestAlbumOffsetPastTheEndIsEmpty(t *testing.T) {
	c := newTest(t, catalogCtl())

	a, next, err := c.Album(context.Background(), "2noRn2Aes5aoNVsU6iWThc", math.MaxInt)
	if err != nil || len(a.Tracks) != 0 || next != 0 {
		t.Fatalf("offset MaxInt: %d tracks, next %d, %v", len(a.Tracks), next, err)
	}
}

func TestMyPlaylistsSkipsFoldersAndFindsArtwork(t *testing.T) {
	c := newTest(t, catalogCtl())

	p, err := c.MyPlaylists(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Items) != 2 || p.Total != 3 {
		t.Fatalf("playlists = %+v", p)
	}

	if p.Items[0].Name != "All Out 80s" || p.Items[0].Artwork != "/api/spotify/images/ab67706f00000002fb8e01f2c022ab1a32e2c5a0" {
		t.Fatalf("editorial playlist = %+v", p.Items[0])
	}

	if p.Items[1].Name != "Mine" || p.Items[1].Artwork != "/api/spotify/images/ab67706c0000da841cc68290e5ddca4c6b1fd8d6" {
		t.Fatalf("own playlist = %+v", p.Items[1])
	}
}

func TestPlaylistWithoutPictureTakesFirstCover(t *testing.T) {
	ctl := catalogCtl()

	const playlist = "37i9dQZF1E4oAaaaaaaaaa"

	ctl.spclient["/playlist/v2/playlist/"+playlist+"?decorate=length&from=0&length=1"] = `{"contents":{"items":[{"uri":"spotify:track:` + trackB62 + `"}]}}`
	c := newTest(t, ctl)

	want, err := c.track(context.Background(), trackB62)
	if err != nil || want.Artwork == "" {
		t.Fatalf("track = %+v, %v", want, err)
	}

	if got := c.firstCover(context.Background(), playlist); got != want.Artwork {
		t.Fatalf("cover = %q, want %q", got, want.Artwork)
	}

	lists := []Playlist{{URI: "spotify:playlist:" + playlist}, {URI: "spotify:playlist:37i9dQZF1E4oAbbbbbbbbb"}}
	c.withCovers(context.Background(), lists)

	if lists[0].Artwork != want.Artwork || lists[1].Artwork != "" {
		t.Fatalf("lists = %+v", lists)
	}
}

func TestLikedTracksPlayableOnly(t *testing.T) {
	c := newTest(t, catalogCtl())

	p, err := c.LikedTracks(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Items) != 1 || p.Items[0].Name != "One More Time" || p.Next != 0 {
		t.Fatalf("liked = %+v", p)
	}
}

func TestMissingContentIsUnavailable(t *testing.T) {
	c := newTest(t, catalogCtl())
	if _, _, err := c.Album(context.Background(), "0000000000000000000001", 0); err == nil || !strings.Contains(err.Error(), "spotify_content_unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchNormalizes(t *testing.T) {
	ctl := catalogCtl()
	ctl.mercury = `{"results":{
	  "tracks":{"hits":[
	    {"uri":"spotify:track:` + trackB62 + `","name":"One More Time","duration":320357,"image":"https://i.scdn.co/image/` + cover + `",
	     "artists":[{"uri":"spotify:artist:4tZwfgrHOc3mvqYlEYSvVi","name":"Daft Punk"}],
	     "album":{"uri":"spotify:album:2noRn2Aes5aoNVsU6iWThc","name":"Discovery"}},
	    {"uri":"spotify:episode:` + trackB62 + `","name":"a podcast"}]},
	  "artists":{"hits":[{"uri":"spotify:artist:4tZwfgrHOc3mvqYlEYSvVi","name":"Daft Punk","image":"https://evil.example/x.jpg"}]},
	  "albums":{"hits":[]},
	  "playlists":{"hits":[{"uri":"spotify:user:spotify:playlist:37i9dQZF1DX4UtSsGT1Sbe","name":"Mix","author":{"name":"Spotify"}}]}}}`
	c := newTest(t, ctl)

	res, err := c.Search(context.Background(), "  daft punk ")
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Tracks) != 1 || res.Tracks[0].Artwork != "/api/spotify/images/"+cover || res.Tracks[0].Album.Name != "Discovery" {
		t.Fatalf("tracks = %+v", res.Tracks)
	}

	if res.Artists[0].Artwork != "" {
		t.Fatalf("non-Spotify image URL passed through: %+v", res.Artists[0])
	}

	if len(res.Playlists) != 1 || res.Playlists[0].URI != "spotify:playlist:37i9dQZF1DX4UtSsGT1Sbe" || res.Playlists[0].Owner != "Spotify" {
		t.Fatalf("playlists = %+v", res.Playlists)
	}

	if !strings.Contains(ctl.calls[0], "hm://searchview/km/v4/search/daft%20punk?") || !strings.Contains(ctl.calls[0], "country=NL") {
		t.Fatalf("mercury uri = %s", ctl.calls[0])
	}

	out, _ := json.Marshal(res)
	if strings.Contains(string(out), "scdn") || strings.Contains(string(out), "evil") {
		t.Fatalf("normalized output leaks an upstream URL: %s", out)
	}
}

func TestFollowedArtists(t *testing.T) {
	ctl := catalogCtl()
	ctl.spclient["/user-profile-view/v3/profile/someone/following?market=from_token"] = `{"profiles":[
	  {"uri":"spotify:artist:` + trackB62 + `","name":"Daft Punk","image_url":"https://i.scdn.co/image/` + cover + `"},
	  {"uri":"spotify:user:someone","name":"A friend"}]}`

	got, err := newTest(t, ctl).FollowedArtists(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Name != "Daft Punk" || got[0].Artwork == "" {
		t.Fatalf("followed = %+v", got)
	}
}

func TestRecentlyPlayed(t *testing.T) {
	ctl := catalogCtl()

	const playlist = "37i9dQZF1DX4UtSsGT1Sbe"

	ctl.spclient["/recently-played/v3/user/someone/recently-played?format=json&offset=0&limit=10&filter=default"] = `{"playContexts":[
	  {"uri":"spotify:user:someone:collection"},
	  {"uri":"spotify:playlist:` + playlist + `"},
	  {"uri":"spotify:track:` + trackB62 + `"},
	  {"uri":"spotify:album:` + b62ID(albumHex) + `"}]}`
	ctl.spclient["/playlist/v2/playlist/"+playlist+"?decorate=attributes,length,owner&from=0&length=1"] = `{"attributes":{"name":"All Out 80s"}}`

	got, err := newTest(t, ctl).RecentlyPlayed(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].Name != "All Out 80s" || got[0].Kind != "playlist" ||
		got[1].Name != "Discovery" || got[1].Kind != "album" {
		t.Fatalf("recent = %+v", got)
	}
}
