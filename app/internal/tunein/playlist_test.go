// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package tunein

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestPlaylistStreams(t *testing.T) {
	bodies := map[string]string{
		"/a.pls": "[playlist]\nNumberOfEntries=2\nFile1=http://one.example/live\nTitle1=One\nFile2=https://two.example/live\n",
		"/b.M3U": "#EXTM3U\n#EXTINF:-1,One\nhttp://one.example/live\n",
		"/c.m3u": "#EXTM3U\n#EXT-X-TARGETDURATION:6\nsegment1.ts\n",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name, url string
		want      []string
	}{
		{"a stream is itself", srv.URL + "/live.mp3", []string{srv.URL + "/live.mp3"}},
		{"pls", srv.URL + "/a.pls", []string{"http://one.example/live", "https://two.example/live"}},
		{"m3u", srv.URL + "/b.M3U", []string{"http://one.example/live"}},
		{"hls under an m3u name", srv.URL + "/c.m3u", []string{srv.URL + "/c.m3u"}},
	} {
		got, err := PlaylistStreams(context.Background(), srv.Client(), tc.url)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v %v, want %v", tc.name, got, err, tc.want)
		}
	}

	if _, err := PlaylistStreams(context.Background(), srv.Client(), srv.URL+"/gone.pls"); err == nil {
		t.Error("a playlist that is gone must be an error")
	}
}
