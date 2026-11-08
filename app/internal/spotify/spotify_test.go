// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCtl stands in for librespot's control API and records what it was asked.
type fakeCtl struct {
	mu       sync.Mutex
	state    string
	user     string
	calls    []string
	spclient map[string]string // path -> JSON body
	mercury  string            // body for any hm:// GET
}

func (f *fakeCtl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/status" {
		json.NewEncoder(w).Encode(map[string]string{"state": f.state, "username": f.user, "country": "NL"})
		return
	}
	if f.state != "connected" {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "not_connected", "state": f.state})
		return
	}
	switch r.URL.Path {
	case "/spclient":
		body, ok := f.spclient[r.URL.Query().Get("path")]
		if !ok {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":"spclient_failed","kind":"NotFound","message":"404"}`))
			return
		}
		w.Write([]byte(body))
		return
	case "/mercury":
		f.calls = append(f.calls, "GET /mercury "+r.URL.Query().Get("uri"))
		w.Write([]byte(f.mercury))
		return
	}
	f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	w.Write([]byte("{}"))
}

func newTest(t *testing.T, ctl *fakeCtl) *Client {
	t.Helper()
	cs := httptest.NewServer(ctl)
	t.Cleanup(cs.Close)
	c := New()
	c.Ctl = cs.URL
	return c
}

func TestStatusMapsReceiverStates(t *testing.T) {
	cases := map[string]Status{
		"unpaired":      {Available: true, State: "unpaired"},
		"connecting":    {Available: true, Authenticated: true, State: "connecting"},
		"reconnecting":  {Available: true, Authenticated: true, State: "reconnecting"},
		"connected":     {Available: true, Authenticated: true, Connected: true, State: "connected"},
		"auth_required": {Available: true, ReauthRequired: true, State: "auth_required"},
	}
	for state, want := range cases {
		c := newTest(t, &fakeCtl{state: state})
		if got := c.Status(context.Background()); got != want {
			t.Errorf("%s: got %+v, want %+v", state, got, want)
		}
	}
	c := New()
	c.Ctl = "http://127.0.0.1:1" // nothing listens
	if got := c.Status(context.Background()); got.State != "unavailable" || got.Available {
		t.Errorf("no receiver: got %+v", got)
	}
}

func TestErrorsFollowTheSessionState(t *testing.T) {
	for state, want := range map[string]error{
		"unpaired":      ErrNotAuthenticated,
		"auth_required": ErrNotAuthenticated,
		"reconnecting":  ErrNotConnected,
	} {
		c := newTest(t, &fakeCtl{state: state})
		if err := c.Command(context.Background(), "next"); !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", state, err, want)
		}
		if _, err := c.Search(context.Background(), "x"); !errors.Is(err, want) {
			t.Errorf("%s search: got %v, want %v", state, err, want)
		}
	}
}

func TestPlayMapsToLoad(t *testing.T) {
	const track = "spotify:track:4uLU6hMCjMI75M1A2tKUQC"
	const album = "spotify:album:2noRn2Aes5aoNVsU6iWThc"
	ctl := &fakeCtl{state: "connected", user: "listener"}
	c := newTest(t, ctl)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.Play(ctx, track, ""))
	must(c.Play(ctx, track, album))
	must(c.Play(ctx, "", LikedContext))
	must(c.Command(ctx, "prev"))
	must(c.Seek(ctx, 30000))
	want := []string{
		"POST /load?uri=spotify%3Atrack%3A4uLU6hMCjMI75M1A2tKUQC",
		"POST /load?context=spotify%3Aalbum%3A2noRn2Aes5aoNVsU6iWThc&uri=spotify%3Atrack%3A4uLU6hMCjMI75M1A2tKUQC",
		"POST /load?context=spotify%3Auser%3Alistener%3Acollection",
		"POST /prev?",
		"POST /seek?ms=30000",
	}
	if strings.Join(ctl.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(ctl.calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestLikedOnlyAcceptsTracks(t *testing.T) {
	ctl := &fakeCtl{state: "connected"}
	c := newTest(t, ctl)
	ctx := context.Background()
	const track = "spotify:track:abc"
	if err := c.SetLiked(ctx, track, true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLiked(ctx, track, false); err != nil {
		t.Fatal(err)
	}
	want := "POST /liked?on=1&uri=spotify%3Atrack%3Aabc\nPOST /liked?on=0&uri=spotify%3Atrack%3Aabc"
	if got := strings.Join(ctl.calls, "\n"); got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}
	if err := c.SetLiked(ctx, "spotify:episode:abc", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("SetLiked(episode) = %v, want ErrInvalid", err)
	}
	if _, err := c.Liked(ctx, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("Liked(empty) = %v, want ErrInvalid", err)
	}
}

func TestShuffleAndRepeatReachTheReceiver(t *testing.T) {
	ctl := &fakeCtl{state: "connected"}
	c := newTest(t, ctl)
	ctx := context.Background()
	for _, err := range []error{
		c.SetShuffle(ctx, true), c.SetShuffle(ctx, false),
		c.SetRepeat(ctx, "track"), c.SetRepeat(ctx, "off"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := "POST /shuffle?on=1\nPOST /shuffle?on=0\nPOST /repeat?mode=track\nPOST /repeat?mode=off"
	if got := strings.Join(ctl.calls, "\n"); got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}
	if err := c.SetRepeat(ctx, "album"); !errors.Is(err, ErrInvalid) {
		t.Errorf("SetRepeat(album) = %v, want ErrInvalid", err)
	}
	if m := c.PlayModes(ctx); m.Shuffle || m.Repeat != "off" {
		t.Errorf("a receiver without modes reads as %+v, want off", m)
	}
}

func TestPlayRejectsBadInput(t *testing.T) {
	ctl := &fakeCtl{state: "connected"}
	c := newTest(t, ctl)
	ctx := context.Background()
	for _, tc := range [][2]string{
		{"", ""},
		{"spotify:album:2noRn2Aes5aoNVsU6iWThc", ""},                  // not a track
		{"spotify:track:short", ""},                                   // bad id
		{"", "spotify:track:4uLU6hMCjMI75M1A2tKUQC"},                  // a track is no context
		{"", "spotify:user:someone:collection"},                       // someone else's library
		{"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC", ""}, // not a URI
		{"spotify:track:4uLU6hMCjMI75M1A2tKUQC&x=1", ""},
	} {
		if err := c.Play(ctx, tc[0], tc[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("Play(%q, %q) = %v, want ErrInvalid", tc[0], tc[1], err)
		}
	}
	if err := c.Command(ctx, "shutdown"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown command: %v", err)
	}
	if err := c.Seek(ctx, -1); !errors.Is(err, ErrInvalid) {
		t.Errorf("negative seek: %v", err)
	}
	if len(ctl.calls) != 0 {
		t.Fatalf("invalid input reached the receiver: %v", ctl.calls)
	}
}

func TestIDsAndImages(t *testing.T) {
	for _, id := range []string{"", "abc", "2noRn2Aes5aoNVsU6iWThc/../x", "2noRn2Aes5aoNVsU6iWTh!"} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
	}
	if _, _, err := (&Client{}).Album(context.Background(), "../me", 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("path traversal id: %v", err)
	}
	if _, ok := ImageURL("ab67616d0000b273" + strings.Repeat("0", 24)); !ok {
		t.Error("real image id refused")
	}
	for _, id := range []string{"x", "../../etc/passwd", strings.Repeat("g", 40)} {
		if _, ok := ImageURL(id); ok {
			t.Errorf("ImageURL(%q) accepted", id)
		}
	}
}

func TestFollowVolumeSendsTheLatestPercent(t *testing.T) {
	ctl := &fakeCtl{state: "connected"}
	c := newTest(t, ctl)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	follow := c.FollowVolume(ctx)
	for level := 10; level <= 30; level++ {
		follow(level)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctl.mu.Lock()
		calls := append([]string(nil), ctl.calls...)
		ctl.mu.Unlock()
		if n := len(calls); n > 0 && calls[n-1] == "POST /volume?percent=30" {
			if n > 21 {
				t.Errorf("%d calls for 21 levels", n)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("calls = %v, want the last one to set 30", calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A level Spotify reported itself is not sent back; the next button press is.
func TestFollowVolumeSkipsSpotifysOwnLevel(t *testing.T) {
	ctl := &fakeCtl{state: "connected"}
	c := newTest(t, ctl)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	follow := c.FollowVolume(ctx)
	c.NoteVolume(40)
	follow(40)
	follow(41)
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctl.mu.Lock()
		calls := append([]string(nil), ctl.calls...)
		ctl.mu.Unlock()
		if n := len(calls); n > 0 && calls[n-1] == "POST /volume?percent=41" {
			for _, call := range calls {
				if call == "POST /volume?percent=40" {
					t.Fatalf("Spotify's own level was echoed: %v", calls)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("calls = %v, want 41 sent", calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPlayingContextKeepsWhatAPresetCanHold(t *testing.T) {
	cases := []struct {
		context, want string
		known         bool
	}{
		{"spotify:album:4aawyAB9vmqN3uQ7FjRGTy", "spotify:album:4aawyAB9vmqN3uQ7FjRGTy", true},
		{"spotify:user:someone:collection", LikedContext, true},
		{"spotify:station:track:4aawyAB9vmqN3uQ7FjRGTy", "", true},
		{"", "", false},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"state": "connected", "context": tc.context})
		}))
		c := &Client{Ctl: srv.URL, HTTP: srv.Client()}
		uri, known := c.PlayingContext(context.Background())
		srv.Close()
		if uri != tc.want || known != tc.known {
			t.Errorf("%q: got %q, %v", tc.context, uri, known)
		}
	}
}
