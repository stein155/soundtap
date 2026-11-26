// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package spotify

import (
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
		writeJSON(w, map[string]string{"state": f.state, "username": f.user, "country": "NL"})

		return
	}

	if f.state != "connected" {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]string{"error": "not_connected", "state": f.state})

		return
	}

	switch r.URL.Path {
	case "/spclient":
		body, ok := f.spclient[r.URL.Query().Get("path")]
		if !ok {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"spclient_failed","kind":"NotFound","message":"404"}`))

			return
		}

		_, _ = w.Write([]byte(body))

		return
	case "/mercury":
		f.calls = append(f.calls, "GET /mercury "+r.URL.Query().Get("uri"))
		_, _ = w.Write([]byte(f.mercury))

		return
	}

	f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)

	_, _ = w.Write([]byte("{}"))
}

// writeJSON answers fields as a JSON object.
func writeJSON(w http.ResponseWriter, fields map[string]string) {
	body, err := json.Marshal(fields)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	_, _ = w.Write(body)
}

// newCtl is a control API in state for user, answering spclient paths with their bodies.
func newCtl(state, user string, spclient map[string]string) *fakeCtl {
	return &fakeCtl{mu: sync.Mutex{}, state: state, user: user, calls: nil, spclient: spclient, mercury: ""}
}

func newTest(t *testing.T, ctl *fakeCtl) *Client {
	t.Helper()

	srv := httptest.NewServer(ctl)
	t.Cleanup(srv.Close)

	client := New()
	client.Ctl = srv.URL

	return client
}

// status is a Status as the receiver's state maps to it.
func status(state string, authenticated, connected, reauth bool) Status {
	return Status{
		Available: true, Authenticated: authenticated, Connected: connected, ReauthRequired: reauth, State: state,
	}
}

func TestStatusMapsReceiverStates(t *testing.T) {
	t.Parallel()

	cases := map[string]Status{
		"unpaired":      status("unpaired", false, false, false),
		"connecting":    status("connecting", true, false, false),
		"reconnecting":  status("reconnecting", true, false, false),
		"connected":     status("connected", true, true, false),
		"auth_required": status("auth_required", false, false, true),
	}
	for state, want := range cases {
		client := newTest(t, newCtl(state, "", nil))
		if got := client.Status(t.Context()); got != want {
			t.Errorf("%s: got %+v, want %+v", state, got, want)
		}
	}

	client := New()

	client.Ctl = "http://127.0.0.1:1" // nothing listens
	if got := client.Status(t.Context()); got.State != "unavailable" || got.Available {
		t.Errorf("no receiver: got %+v", got)
	}
}

func TestErrorsFollowTheSessionState(t *testing.T) {
	t.Parallel()

	for state, want := range map[string]error{
		"unpaired":      ErrNotAuthenticated,
		"auth_required": ErrNotAuthenticated,
		"reconnecting":  ErrNotConnected,
	} {
		client := newTest(t, newCtl(state, "", nil))

		err := client.Command(t.Context(), "next")
		if !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", state, err, want)
		}

		_, err = client.Search(t.Context(), "x")
		if !errors.Is(err, want) {
			t.Errorf("%s search: got %v, want %v", state, err, want)
		}
	}
}

func TestPlayMapsToLoad(t *testing.T) {
	t.Parallel()

	const (
		track = "spotify:track:4uLU6hMCjMI75M1A2tKUQC"
		album = "spotify:album:2noRn2Aes5aoNVsU6iWThc"
	)

	ctl := newCtl("connected", "listener", nil)
	client := newTest(t, ctl)
	ctx := t.Context()
	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}
	must(client.Play(ctx, track, ""))
	must(client.Play(ctx, track, album))
	must(client.Play(ctx, "", LikedContext))
	must(client.Command(ctx, "prev"))
	must(client.Seek(ctx, 30000))

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
	t.Parallel()

	ctl := newCtl("connected", "", nil)
	client := newTest(t, ctl)
	ctx := t.Context()

	const track = "spotify:track:abc"

	err := client.SetLiked(ctx, track, true)
	if err != nil {
		t.Fatal(err)
	}

	err = client.SetLiked(ctx, track, false)
	if err != nil {
		t.Fatal(err)
	}

	want := "POST /liked?on=1&uri=spotify%3Atrack%3Aabc\nPOST /liked?on=0&uri=spotify%3Atrack%3Aabc"
	if got := strings.Join(ctl.calls, "\n"); got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}

	err = client.SetLiked(ctx, "spotify:episode:abc", true)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("SetLiked(episode) = %v, want ErrInvalid", err)
	}

	_, err = client.Liked(ctx, "")
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("Liked(empty) = %v, want ErrInvalid", err)
	}
}

func TestShuffleAndRepeatReachTheReceiver(t *testing.T) {
	t.Parallel()

	ctl := newCtl("connected", "", nil)
	client := newTest(t, ctl)

	ctx := t.Context()
	for _, err := range []error{
		client.SetShuffle(ctx, true), client.SetShuffle(ctx, false),
		client.SetRepeat(ctx, "track"), client.SetRepeat(ctx, "off"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	want := "POST /shuffle?on=1\nPOST /shuffle?on=0\nPOST /repeat?mode=track\nPOST /repeat?mode=off"
	if got := strings.Join(ctl.calls, "\n"); got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}

	err := client.SetRepeat(ctx, "album")
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("SetRepeat(album) = %v, want ErrInvalid", err)
	}

	if m := client.PlayModes(ctx); m.Shuffle || m.Repeat != "off" {
		t.Errorf("a receiver without modes reads as %+v, want off", m)
	}
}

func TestPlayRejectsBadInput(t *testing.T) {
	t.Parallel()

	ctl := newCtl("connected", "", nil)
	client := newTest(t, ctl)

	ctx := t.Context()
	for _, test := range [][2]string{
		{"", ""},
		{"spotify:album:2noRn2Aes5aoNVsU6iWThc", ""},                  // not a track
		{"spotify:track:short", ""},                                   // bad id
		{"", "spotify:track:4uLU6hMCjMI75M1A2tKUQC"},                  // a track is no context
		{"", "spotify:user:someone:collection"},                       // someone else's library
		{"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC", ""}, // not a URI
		{"spotify:track:4uLU6hMCjMI75M1A2tKUQC&x=1", ""},
	} {
		err := client.Play(ctx, test[0], test[1])
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("Play(%q, %q) = %v, want ErrInvalid", test[0], test[1], err)
		}
	}

	err := client.Command(ctx, "shutdown")
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown command: %v", err)
	}

	err = client.Seek(ctx, -1)
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("negative seek: %v", err)
	}

	if len(ctl.calls) != 0 {
		t.Fatalf("invalid input reached the receiver: %v", ctl.calls)
	}
}

func TestIDsAndImages(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "abc", "2noRn2Aes5aoNVsU6iWThc/../x", "2noRn2Aes5aoNVsU6iWTh!"} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true", id)
		}
	}

	_, _, err := New().Album(t.Context(), "../me", 0)
	if !errors.Is(err, ErrInvalid) {
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
	t.Parallel()

	ctl := newCtl("connected", "", nil)
	client := newTest(t, ctl)

	ctx := t.Context()

	follow := client.FollowVolume(ctx)
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
	t.Parallel()

	ctl := newCtl("connected", "", nil)
	client := newTest(t, ctl)

	ctx := t.Context()

	follow := client.FollowVolume(ctx)
	client.NoteVolume(40)
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
	t.Parallel()

	cases := []struct {
		context, want string
		known         bool
	}{
		{"spotify:album:4aawyAB9vmqN3uQ7FjRGTy", "spotify:album:4aawyAB9vmqN3uQ7FjRGTy", true},
		{"spotify:user:someone:collection", LikedContext, true},
		{"spotify:station:track:4aawyAB9vmqN3uQ7FjRGTy", "", true},
		{"", "", false},
	}
	for _, test := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"state": "connected", "context": test.context})
		}))
		client := New()
		client.Ctl, client.HTTP = srv.URL, srv.Client()
		uri, known := client.PlayingContext(t.Context())

		srv.Close()

		if uri != test.want || known != test.known {
			t.Errorf("%q: got %q, %v", test.context, uri, known)
		}
	}
}
