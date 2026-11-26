// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package tunein

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// withServer returns a Client whose API root is a test server, and the function that
// stops it. The handler receives every request the client makes.
func withServer(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()

	srv := httptest.NewServer(handler)
	client := New()
	client.http = &http.Client{} // the test server is on loopback, which SafeTransport refuses
	client.root = srv.URL

	return client, srv.Close
}

func TestSearch(t *testing.T) {
	t.Parallel()

	const body = `{
	  "head": {"status": "200"},
	  "body": [
	    {"element":"outline","type":"audio","item":"station","text":"BBC Radio 1","guide_id":"s24939",
	     "image":"http://img/r1.png","bitrate":"128","subtext":"Now: A Song"},
	    {"element":"outline","type":"link","item":"link","text":"More Stations","guide_id":"c57943"},
	    {"element":"outline","type":"audio","item":"station","text":"Bad GuideID","guide_id":"p999",
	     "image":"","bitrate":"","subtext":""},
	    {"element":"outline","type":"audio","item":"station","text":"NPO Radio 2","guide_id":"s6712",
	     "image":"http://img/r2.png","bitrate":"320","subtext":""}
	  ]
	}`

	var gotPath, gotRawQuery string

	client, done := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(body))
	})
	defer done()

	got, err := client.Search(t.Context(), "radio & jazz?")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	want := []Station{
		{ID: "s24939", Name: "BBC Radio 1", Logo: "http://img/r1.png", Bitrate: "128", NowPlaying: "Now: A Song"},
		{ID: "s6712", Name: "NPO Radio 2", Logo: "http://img/r2.png", Bitrate: "320", NowPlaying: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Search stations = %#v, want %#v", got, want)
	}

	// URL construction: correct endpoint and escaped query.
	if gotPath != "/Search.ashx" {
		t.Errorf("path = %q, want /Search.ashx", gotPath)
	}
	// Raw query is url.QueryEscape'd (control characters and ';' too).
	if !strings.Contains(gotRawQuery, "query=radio+%26+jazz%3F") {
		t.Errorf("raw query = %q, want escaped query=radio+%%26+jazz%%3F", gotRawQuery)
	}
	// Server-side parsed form should round-trip the escapes back to the original.
	if q := mustParseQuery(t, gotRawQuery).Get("query"); q != "radio & jazz?" {
		t.Errorf("decoded query = %q, want %q", q, "radio & jazz?")
	}

	if f := mustParseQuery(t, gotRawQuery).Get("formats"); f != formats {
		t.Errorf("formats = %q, want %q", f, formats)
	}

	if ty := mustParseQuery(t, gotRawQuery).Get("types"); ty != "station" {
		t.Errorf("types = %q, want station", ty)
	}

	if rn := mustParseQuery(t, gotRawQuery).Get("render"); rn != "json" {
		t.Errorf("render = %q, want json", rn)
	}
}

func TestSearchErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
		wantLen int
	}{
		{name: "bad json", status: 200, body: `not json at all`, wantErr: true, wantLen: 0},
		{name: "non-200", status: 500, body: `{}`, wantErr: true, wantLen: 0},
		{name: "empty body array", status: 200, body: `{"body":[]}`, wantErr: false, wantLen: 0},
		{
			name: "no station items", status: 200, body: `{"body":[{"item":"link","guide_id":"c1"}]}`,
			wantErr: false, wantLen: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client, done := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			defer done()

			got, err := client.Search(t.Context(), "x")
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%#v)", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(got) != test.wantLen {
				t.Errorf("len = %d, want %d", len(got), test.wantLen)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	t.Parallel()

	const body = `{"body":[{"name":"BBC Radio 1","logo":"http://img/r1.png"}]}`

	var gotPath, gotQuery string

	client, done := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(body))
	})
	defer done()

	name, logo := client.Describe(t.Context(), "s24939")
	if name != "BBC Radio 1" || logo != "http://img/r1.png" {
		t.Errorf("Describe = (%q, %q), want (BBC Radio 1, http://img/r1.png)", name, logo)
	}

	if gotPath != "/Describe.ashx" {
		t.Errorf("path = %q, want /Describe.ashx", gotPath)
	}

	if id := mustParseQuery(t, gotQuery).Get("id"); id != "s24939" {
		t.Errorf("id = %q, want s24939", id)
	}
}

func TestDescribeBestEffort(t *testing.T) {
	t.Parallel()

	// Describe swallows all failures and returns empty strings (no error path).
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non-200", status: 500, body: `{}`},
		{name: "bad json", status: 200, body: `}{`},
		{name: "empty body", status: 200, body: `{"body":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client, done := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			defer done()

			name, logo := client.Describe(t.Context(), "s1")
			if name != "" || logo != "" {
				t.Errorf("Describe = (%q, %q), want empty", name, logo)
			}
		})
	}
}

func TestNowPlaying(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		status int
		want   Track
	}{
		{
			name: "album art, is_music as bool true",
			body: `{"body":[{"is_music":true,"has_song":"false","logo":"http://img/l.png",
			        "current_song":" Song ","current_artist":" Artist ","current_album":" Album ",
			        "current_album_art":"http://img/album.png","current_artist_art":"http://img/artist.png"}]}`,
			status: 200,
			want: Track{
				Song: "Song", Artist: "Artist", Album: "Album",
				Art: "http://img/album.png", Logo: "http://img/l.png", IsLive: true,
			},
		},
		{
			name: "falls back to artist art, has_song quoted true",
			body: `{"body":[{"is_music":"false","has_song":"true","logo":"http://img/l.png",
			        "current_song":"S","current_artist":"A","current_album":"",
			        "current_album_art":"","current_artist_art":"http://img/artist.png"}]}`,
			status: 200,
			want: Track{
				Song: "S", Artist: "A", Album: "",
				Art: "http://img/artist.png", Logo: "http://img/l.png", IsLive: true,
			},
		},
		{
			name:   "not live: both flags false",
			body:   `{"body":[{"is_music":"false","has_song":false,"current_song":"S","current_artist":"A"}]}`,
			status: 200,
			want:   Track{Song: "S", Artist: "A", Album: "", Art: "", Logo: "", IsLive: false},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client, done := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			defer done()

			got, err := client.NowPlaying(t.Context(), "s1")
			if err != nil {
				t.Fatalf("NowPlaying: %v", err)
			}

			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("NowPlaying = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNowPlayingBestEffort(t *testing.T) {
	t.Parallel()

	// Bad JSON / empty body -> zero Track, no error. Transport error -> error.
	t.Run("bad json is zero track no error", func(t *testing.T) {
		t.Parallel()

		client, done := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`}{`))
		})
		defer done()

		got, err := client.NowPlaying(t.Context(), "s1")
		if err != nil || got != (Track{Song: "", Artist: "", Album: "", Art: "", Logo: "", IsLive: false}) {
			t.Errorf("got (%#v, %v), want (zero, nil)", got, err)
		}
	})
	t.Run("non-200 returns error", func(t *testing.T) {
		t.Parallel()

		client, done := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		defer done()

		_, err := client.NowPlaying(t.Context(), "s1")
		if !errors.Is(err, errStatus) {
			t.Errorf("err = %v, want errStatus on non-200", err)
		}
	})
}

func TestResolve(t *testing.T) {
	t.Parallel()

	const body = "http://stream.example/1.mp3\r\n" +
		"#comment line\n" +
		"  https://stream.example/2.aac  \n" +
		"ftp://ignored\n" +
		"\n"

	var gotPath, gotQuery string

	client, done := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(body))
	})
	defer done()

	urls, err := client.Resolve(t.Context(), "s6712")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := []string{"http://stream.example/1.mp3", "https://stream.example/2.aac"}
	if !reflect.DeepEqual(urls, want) {
		t.Errorf("Resolve = %#v, want %#v", urls, want)
	}

	if gotPath != "/Tune.ashx" {
		t.Errorf("path = %q, want /Tune.ashx", gotPath)
	}

	query := mustParseQuery(t, gotQuery)
	if query.Get("id") != "s6712" {
		t.Errorf("id = %q, want s6712", query.Get("id"))
	}

	if query.Get("formats") != formats {
		t.Errorf("formats = %q, want %q", query.Get("formats"), formats)
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "no stream lines", status: 200, body: "#just a comment\nnot a url\n"},
		{name: "empty body", status: 200, body: ""},
		{name: "non-200", status: 404, body: "http://x/y.mp3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client, done := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			defer done()

			_, err := client.Resolve(t.Context(), "s1")
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestPlayableURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want string
	}{
		{name: "prefers http over https", in: []string{"https://a", "http://b", "http://c"}, want: "http://b"},
		{name: "falls back to first when no http", in: []string{"https://a", "https://b"}, want: "https://a"},
		{name: "empty", in: nil, want: ""},
		{name: "single https", in: []string{"https://only"}, want: "https://only"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := PlayableURL(test.in); got != test.want {
				t.Errorf("PlayableURL(%v) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestGetRejectsNonTuneInHost(t *testing.T) {
	t.Parallel()

	// The get() guard must refuse any URL not under the (test) base.
	client, done := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	defer done()

	_, err := client.get(t.Context(), "http://evil.example/Search.ashx")
	if !errors.Is(err, errForeignHost) || !strings.Contains(err.Error(), "refusing non-TuneIn host") {
		t.Errorf("get(evil host) err = %v, want refusing non-TuneIn host", err)
	}
}

func mustParseQuery(t *testing.T, raw string) url.Values {
	t.Helper()

	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", raw, err)
	}

	return v
}

// PlayableURLs must keep every candidate, not just the preferred one: the whole point is
// that a station's first stream is often dead and the caller needs somewhere to fall back
// to. MP3 comes first because its decoder starts much faster on the ST10.
func TestPlayableURLsKeepsEveryCandidateMP3First(t *testing.T) {
	t.Parallel()

	resolved := []string{
		"https://cdn.example/live.aac",
		"https://secure.example/live.mp3",
		"http://primary.example/live.mp3",
		"http://backup.example/live.aac",
	}
	got := PlayableURLs(resolved)

	want := []string{
		"http://primary.example/live.mp3",
		"https://secure.example/live.mp3",
		"http://backup.example/live.aac",
		"https://cdn.example/live.aac",
	}
	if len(got) != len(want) {
		t.Fatalf("PlayableURLs(%v) = %v, want %v", resolved, got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PlayableURLs(%v) = %v, want %v", resolved, got, want)
		}
	}
}

func TestPlayableURLsOnEmpty(t *testing.T) {
	t.Parallel()

	if got := PlayableURLs(nil); len(got) != 0 {
		t.Fatalf("PlayableURLs(nil) = %v, want empty", got)
	}
}

func TestResolveSaysWhenAStationHasNoStream(t *testing.T) {
	t.Parallel()

	client, done := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("#STATUS: 400\n"))
	})
	defer done()

	_, err := client.Resolve(t.Context(), "s7910")
	if !errors.Is(err, ErrNoStreams) {
		t.Fatalf("err = %v, want ErrNoStreams", err)
	}
}
