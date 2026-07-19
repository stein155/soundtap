// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// fakeResolver answers with a fixed stream list and counts how often it was asked.
type fakeResolver struct {
	urls  []string
	err   error
	calls int
	sids  []string
}

func (f *fakeResolver) StreamURLs(_ context.Context, sid string) ([]string, error) {
	f.calls++
	f.sids = append(f.sids, sid)
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.urls...), nil
}

// A retry must move on to the next stream. This is the whole point of the change: a
// station whose primary stream is dead used to be retried against that same primary
// forever, spending a full bufferDeadline per attempt and never playing.
func TestRetryWalksTheFallbackChain(t *testing.T) {
	r := &fakeResolver{urls: []string{
		"http://primary.example/live.mp3",
		"http://backup.example/live.mp3",
		"https://cdn.example/live.aac",
	}}
	var c streamCandidates
	ctx := context.Background()
	const loc = "/stations/s12345"

	var got []string
	for i, retry := range []bool{false, true, true} {
		u, err := c.url(ctx, r, loc, retry)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		for _, prev := range got {
			if u == prev {
				t.Fatalf("attempt %d reused %q, which already failed", i, u)
			}
		}
		got = append(got, u)
	}
	if got[0] != "http://primary.example/live.mp3" {
		t.Fatalf("first attempt used %q, want the preferred stream", got[0])
	}
	// One resolve covered all three attempts. Re-resolving per attempt is exactly what
	// handed back the same dead entry every time.
	if r.calls != 1 {
		t.Fatalf("resolved %d times for three attempts, want 1", r.calls)
	}
	if r.sids[0] != "s12345" {
		t.Fatalf("resolved station %q, want s12345", r.sids[0])
	}
}

// Once the chain is exhausted, ask TuneIn again rather than giving up: the stream set
// does change over time, and by then another round trip has earned its keep.
func TestChainExhaustionReresolves(t *testing.T) {
	r := &fakeResolver{urls: []string{"http://only.example/live.mp3"}}
	var c streamCandidates
	ctx := context.Background()
	const loc = "/stations/s12345"

	if _, err := c.url(ctx, r, loc, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.url(ctx, r, loc, true); err != nil {
		t.Fatal(err)
	}
	if r.calls != 2 {
		t.Fatalf("resolved %d times, want 2 (one per exhaustion)", r.calls)
	}
}

// Selecting a different station must start that station's own chain at its best stream,
// not continue wherever the previous station had got to.
func TestNewStationRestartsTheChain(t *testing.T) {
	r := &fakeResolver{urls: []string{"http://a.example/live.mp3", "http://b.example/live.mp3"}}
	var c streamCandidates
	ctx := context.Background()

	if _, err := c.url(ctx, r, "/stations/s1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.url(ctx, r, "/stations/s1", true); err != nil {
		t.Fatal(err)
	}
	got, err := c.url(ctx, r, "/stations/s2", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://a.example/live.mp3" {
		t.Fatalf("new station started at %q, want its first candidate", got)
	}
}

// A failed resolve must not leave a stale chain behind for the next retry to walk as if
// it were current.
func TestFailedResolveCachesNothing(t *testing.T) {
	r := &fakeResolver{err: fmt.Errorf("tunein status 500")}
	var c streamCandidates

	if _, err := c.url(context.Background(), r, "/stations/s1", false); err == nil {
		t.Fatal("expected a resolve error")
	}
	if c.loc != "" || len(c.urls) != 0 || c.i != 0 {
		t.Fatalf("failed resolve left state behind: %+v", c)
	}
}

// A station's last list is kept on flash and still plays after a restart when TuneIn
// refuses every lookup.
func TestKeptListPlaysWhenTuneInFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunein-streams.json")
	before := &StreamCache{}
	before.Load(path)
	ok := &fakeResolver{urls: []string{"http://a.example/live.mp3", "http://b.example/live.mp3"}}
	if _, err := (&streamCandidates{cache: before}).url(context.Background(), ok, "/stations/s1", false); err != nil {
		t.Fatal(err)
	}

	after := &StreamCache{}
	after.Load(path)
	down := &fakeResolver{err: fmt.Errorf("tunein status 403")}
	c := streamCandidates{cache: after}
	for i, want := range []string{"http://a.example/live.mp3", "http://b.example/live.mp3", "http://a.example/live.mp3"} {
		got, err := c.url(context.Background(), down, "/stations/s1", i > 0)
		if err != nil || got != want {
			t.Fatalf("attempt %d: %q, %v; want %q", i, got, err, want)
		}
	}
}

// A station resolved once is served from the cache when it is selected again, but a retry
// after a failed attempt resolves fresh instead of walking the possibly stale cached list.
func TestCachedStationSkipsResolveUntilRetry(t *testing.T) {
	r := &fakeResolver{urls: []string{"http://a.example/live.mp3", "http://b.example/live.mp3"}}
	c := streamCandidates{cache: &StreamCache{}}
	ctx := context.Background()
	for _, loc := range []string{"/stations/s1", "/stations/s2", "/stations/s1"} {
		if _, err := c.url(ctx, r, loc, false); err != nil {
			t.Fatal(err)
		}
	}
	if r.calls != 2 {
		t.Fatalf("resolved %d times, want 2 (s1 and s2 once each)", r.calls)
	}
	got, err := c.url(ctx, r, "/stations/s1", true)
	if err != nil || r.calls != 3 || got != "http://a.example/live.mp3" {
		t.Fatalf("retry on cached list: %q, %v, calls=%d; want a fresh resolve", got, err, r.calls)
	}
	c.cache.m["/stations/s2"] = CachedStreams{r.urls, time.Now().Add(-streamCacheTTL - time.Second)}
	if _, err := c.url(ctx, r, "/stations/s2", false); err != nil || r.calls != 4 {
		t.Fatalf("expired entry was not re-resolved: %v calls=%d", err, r.calls)
	}
}

// A selection change during a slow lookup cancels it at once and drops its result.
func TestLookupStopsWhenTheSelectionChanges(t *testing.T) {
	var mu sync.Mutex
	sig := make(chan struct{})
	selected := true
	changed := func() <-chan struct{} { mu.Lock(); defer mu.Unlock(); return sig }
	wanted := func() bool { mu.Lock(); defer mu.Unlock(); return selected }
	started := make(chan struct{})
	go func() {
		<-started
		mu.Lock()
		selected = false
		close(sig)
		sig = make(chan struct{})
		mu.Unlock()
	}()
	t0 := time.Now()
	url, current, _ := lookupWhileWanted(context.Background(), changed, wanted, func(ctx context.Context) (string, error) {
		close(started)
		select {
		case <-ctx.Done():
			return "http://old.example/stream", nil // a lookup that finishes anyway
		case <-time.After(10 * time.Second):
			return "", errors.New("lookup was not cancelled")
		}
	})
	if current || url != "" {
		t.Fatalf("got %q current=%v; the old station's result must be dropped", url, current)
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("cancelling took %v", d)
	}
}

func TestLookupKeepsTheResultWhileSelected(t *testing.T) {
	never := make(chan struct{})
	url, current, err := lookupWhileWanted(context.Background(), func() <-chan struct{} { return never },
		func() bool { return true }, func(context.Context) (string, error) { return "http://a/s", nil })
	if !current || err != nil || url != "http://a/s" {
		t.Fatalf("got %q current=%v err=%v", url, current, err)
	}
}

// A stream URL of the user's own is its own candidate and never reaches TuneIn, and it
// passes the check a UPnP location passes: this URL ends up in ffmpeg as root.
func TestOwnStreamSkipsTuneIn(t *testing.T) {
	r := &fakeResolver{urls: []string{"http://tunein.example/live.mp3"}}
	var c streamCandidates
	const own = "http://192.0.2.10:8000/live.mp3"
	got, err := c.url(context.Background(), r, own, false)
	if err != nil || got != own {
		t.Fatalf("url = %q, %v", got, err)
	}
	if r.calls != 0 {
		t.Fatalf("TuneIn was asked %d times", r.calls)
	}
	if _, err := c.url(context.Background(), r, "http://127.0.0.1/api/reboot", false); err == nil {
		t.Fatal("a loopback URL was accepted")
	}
}

// The prewarm resolves each TuneIn preset once (own streams and other sources never), skips
// entries that are fresh, renews entries nearing the TTL, and a cached press then needs no resolve.
func TestPrewarmFillsAndRefreshesCache(t *testing.T) {
	r := &fakeResolver{urls: []string{"http://a.example/live.mp3"}}
	cache := &StreamCache{}
	ps := []speaker.Preset{
		{Slot: 1, Source: sourceTuneIn, Location: "/stations/s1"},
		{Slot: 2, Source: sourceTuneIn, Location: "http://192.0.2.10:8000/live.mp3"},
		{Slot: 3, Source: "SPOTIFY", Location: "/x/y"},
	}
	list := func(context.Context) ([]speaker.Preset, error) { return ps, nil }
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	prewarmOnce(ctx, r, list, cache, prewarmRefresh, log)
	prewarmOnce(ctx, r, list, cache, prewarmRefresh, log)
	if r.calls != 1 {
		t.Fatalf("resolved %d times, want 1 (second pass is fresh, others are skipped)", r.calls)
	}
	c := streamCandidates{cache: cache}
	if _, err := c.url(ctx, r, "/stations/s1", false); err != nil || r.calls != 1 {
		t.Fatalf("press after prewarm resolved again: %v calls=%d", err, r.calls)
	}
	cache.m["/stations/s1"] = CachedStreams{r.urls, time.Now().Add(-prewarmRefresh - time.Minute)}
	prewarmOnce(ctx, r, list, cache, prewarmRefresh, log)
	if r.calls != 2 {
		t.Fatalf("aged entry was not renewed, calls=%d", r.calls)
	}
	// In standby the same age is left alone until a day has passed.
	cache.m["/stations/s1"] = CachedStreams{r.urls, time.Now().Add(-prewarmRefresh - time.Minute)}
	prewarmOnce(ctx, r, list, cache, prewarmRefreshStandby, log)
	if r.calls != 2 {
		t.Fatalf("standby renewed a list younger than a day, calls=%d", r.calls)
	}
}
