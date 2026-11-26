// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package player

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// The answers of a TuneIn that does not help and a lookup that ran too long.
var (
	errTuneInFailed  = errors.New("tunein status 500")
	errTuneInRefused = errors.New("tunein status 403")
	errNotCancelled  = errors.New("lookup was not cancelled")
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

// answering is a resolver that lists urls.
func answering(urls ...string) *fakeResolver {
	return &fakeResolver{urls: urls, err: nil, calls: 0, sids: nil}
}

// failing is a resolver that answers err.
func failing(err error) *fakeResolver {
	return &fakeResolver{urls: nil, err: err, calls: 0, sids: nil}
}

// candidates is a fresh walk over cache (nil: no cache).
func candidates(cache *StreamCache) *streamCandidates {
	return &streamCandidates{loc: "", urls: nil, i: 0, cached: false, cache: cache}
}

// newCache is an empty stream cache that keeps nothing on flash.
func newCache() *StreamCache {
	return &StreamCache{mu: sync.Mutex{}, m: nil, path: ""}
}

// A retry must move on to the next stream. This is the whole point of the change: a
// station whose primary stream is dead used to be retried against that same primary
// forever, spending a full bufferDeadline per attempt and never playing.
func TestRetryWalksTheFallbackChain(t *testing.T) {
	t.Parallel()

	resolver := answering(
		"http://primary.example/live.mp3",
		"http://backup.example/live.mp3",
		"https://cdn.example/live.aac",
	)

	walk := candidates(nil)
	ctx := t.Context()

	const loc = "/stations/s12345"

	got := make([]string, 0, len(resolver.urls))

	for attempt, pick := range []picker{walk.url, walk.retry, walk.retry} {
		stream, err := pick(ctx, resolver, loc)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}

		for _, prev := range got {
			if stream == prev {
				t.Fatalf("attempt %d reused %q, which already failed", attempt, stream)
			}
		}

		got = append(got, stream)
	}

	if got[0] != "http://primary.example/live.mp3" {
		t.Fatalf("first attempt used %q, want the preferred stream", got[0])
	}
	// One resolve covered all three attempts. Re-resolving per attempt is exactly what
	// handed back the same dead entry every time.
	if resolver.calls != 1 {
		t.Fatalf("resolved %d times for three attempts, want 1", resolver.calls)
	}

	if resolver.sids[0] != "s12345" {
		t.Fatalf("resolved station %q, want s12345", resolver.sids[0])
	}
}

// Once the chain is exhausted, ask TuneIn again rather than giving up: the stream set
// does change over time, and by then another round trip has earned its keep.
func TestChainExhaustionReresolves(t *testing.T) {
	t.Parallel()

	resolver := answering("http://only.example/live.mp3")
	walk := candidates(nil)
	ctx := t.Context()

	const loc = "/stations/s12345"

	_, err := walk.url(ctx, resolver, loc)
	if err != nil {
		t.Fatal(err)
	}

	_, err = walk.retry(ctx, resolver, loc)
	if err != nil {
		t.Fatal(err)
	}

	if resolver.calls != 2 {
		t.Fatalf("resolved %d times, want 2 (one per exhaustion)", resolver.calls)
	}
}

// Selecting a different station must start that station's own chain at its best stream,
// not continue wherever the previous station had got to.
func TestNewStationRestartsTheChain(t *testing.T) {
	t.Parallel()

	resolver := answering("http://a.example/live.mp3", "http://b.example/live.mp3")
	walk := candidates(nil)
	ctx := t.Context()

	_, err := walk.url(ctx, resolver, "/stations/s1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = walk.retry(ctx, resolver, "/stations/s1")
	if err != nil {
		t.Fatal(err)
	}

	got, err := walk.url(ctx, resolver, "/stations/s2")
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
	t.Parallel()

	walk := candidates(nil)

	_, err := walk.url(t.Context(), failing(errTuneInFailed), "/stations/s1")
	if !errors.Is(err, errTuneInFailed) {
		t.Fatalf("err = %v, want the resolve error", err)
	}

	if walk.loc != "" || len(walk.urls) != 0 || walk.i != 0 {
		t.Fatalf("failed resolve left state behind: %+v", walk)
	}
}

// A station's last list is kept on flash and still plays after a restart when TuneIn
// refuses every lookup.
func TestKeptListPlaysWhenTuneInFails(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "tunein-streams.json")
	before := newCache()
	before.Load(path)

	working := answering("http://a.example/live.mp3", "http://b.example/live.mp3")

	_, err := candidates(before).url(t.Context(), working, "/stations/s1")
	if err != nil {
		t.Fatal(err)
	}

	after := newCache()
	after.Load(path)

	down := failing(errTuneInRefused)

	walk := candidates(after)
	for attempt, want := range []string{
		"http://a.example/live.mp3", "http://b.example/live.mp3", "http://a.example/live.mp3",
	} {
		pick := walk.url
		if attempt > 0 {
			pick = walk.retry
		}

		got, err := pick(t.Context(), down, "/stations/s1")
		if err != nil || got != want {
			t.Fatalf("attempt %d: %q, %v; want %q", attempt, got, err, want)
		}
	}
}

// A station resolved once is served from the cache when it is selected again, but a retry
// after a failed attempt resolves fresh instead of walking the possibly stale cached list.
func TestCachedStationSkipsResolveUntilRetry(t *testing.T) {
	t.Parallel()

	resolver := answering("http://a.example/live.mp3", "http://b.example/live.mp3")
	walk := candidates(newCache())

	ctx := t.Context()
	for _, loc := range []string{"/stations/s1", "/stations/s2", "/stations/s1"} {
		_, err := walk.url(ctx, resolver, loc)
		if err != nil {
			t.Fatal(err)
		}
	}

	if resolver.calls != 2 {
		t.Fatalf("resolved %d times, want 2 (s1 and s2 once each)", resolver.calls)
	}

	got, err := walk.retry(ctx, resolver, "/stations/s1")
	if err != nil || resolver.calls != 3 || got != "http://a.example/live.mp3" {
		t.Fatalf("retry on cached list: %q, %v, calls=%d; want a fresh resolve", got, err, resolver.calls)
	}

	walk.cache.m["/stations/s2"] = CachedStreams{URLs: resolver.urls, At: time.Now().Add(-streamCacheTTL - time.Second)}

	_, err = walk.url(ctx, resolver, "/stations/s2")
	if err != nil || resolver.calls != 4 {
		t.Fatalf("expired entry was not re-resolved: %v calls=%d", err, resolver.calls)
	}
}

// A selection change during a slow lookup cancels it at once and drops its result.
func TestLookupStopsWhenTheSelectionChanges(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex

	sig := make(chan struct{})
	selected := true
	changed := func() <-chan struct{} {
		mu.Lock()
		defer mu.Unlock()

		return sig
	}
	wanted := func() bool {
		mu.Lock()
		defer mu.Unlock()

		return selected
	}

	started := make(chan struct{})
	go func() {
		<-started
		mu.Lock()
		selected = false

		close(sig)
		sig = make(chan struct{})
		mu.Unlock()
	}()

	begin := time.Now()

	url, current, _ := lookupWhileWanted(t.Context(), changed, wanted, func(ctx context.Context) (string, error) {
		close(started)

		select {
		case <-ctx.Done():
			return "http://old.example/stream", nil // a lookup that finishes anyway
		case <-time.After(10 * time.Second):
			return "", errNotCancelled
		}
	})
	if current || url != "" {
		t.Fatalf("got %q current=%v; the old station's result must be dropped", url, current)
	}

	if took := time.Since(begin); took > 2*time.Second {
		t.Fatalf("cancelling took %v", took)
	}
}

func TestLookupKeepsTheResultWhileSelected(t *testing.T) {
	t.Parallel()

	never := make(chan struct{})

	url, current, err := lookupWhileWanted(t.Context(), func() <-chan struct{} { return never },
		func() bool { return true }, func(context.Context) (string, error) { return "http://a/s", nil })
	if !current || err != nil || url != "http://a/s" {
		t.Fatalf("got %q current=%v err=%v", url, current, err)
	}
}

// A stream URL of the user's own is its own candidate and never reaches TuneIn, and it
// passes the check a UPnP location passes: this URL ends up in ffmpeg as root.
func TestOwnStreamSkipsTuneIn(t *testing.T) {
	t.Parallel()

	resolver := answering("http://tunein.example/live.mp3")
	walk := candidates(nil)

	const own = "http://192.0.2.10:8000/live.mp3"

	got, err := walk.url(t.Context(), resolver, own)
	if err != nil || got != own {
		t.Fatalf("url = %q, %v", got, err)
	}

	if resolver.calls != 0 {
		t.Fatalf("TuneIn was asked %d times", resolver.calls)
	}

	_, err = walk.url(t.Context(), resolver, "http://127.0.0.1/api/reboot")
	if err == nil {
		t.Fatal("a loopback URL was accepted")
	}
}

// preset is a saved preset with the fields the prewarm reads.
func preset(slot int, source, location string) speaker.Preset {
	return speaker.Preset{
		Slot: slot, Name: "", StationID: "", Source: source, Type: "", Location: location, Logo: "",
	}
}

// The prewarm resolves each TuneIn preset once (own streams and other sources never), skips
// entries that are fresh, renews entries nearing the TTL, and a cached press then needs no resolve.
func TestPrewarmFillsAndRefreshesCache(t *testing.T) {
	t.Parallel()

	resolver := answering("http://a.example/live.mp3")
	cache := newCache()
	presets := []speaker.Preset{
		preset(1, sourceTuneIn, "/stations/s1"),
		preset(2, sourceTuneIn, "http://192.0.2.10:8000/live.mp3"),
		preset(3, "SPOTIFY", "/x/y"),
	}
	list := func(context.Context) ([]speaker.Preset, error) { return presets, nil }
	ctx := t.Context()
	log := slog.New(slog.DiscardHandler)
	prewarmOnce(ctx, resolver, list, cache, prewarmRefresh, log)
	prewarmOnce(ctx, resolver, list, cache, prewarmRefresh, log)

	if resolver.calls != 1 {
		t.Fatalf("resolved %d times, want 1 (second pass is fresh, others are skipped)", resolver.calls)
	}

	_, err := candidates(cache).url(ctx, resolver, "/stations/s1")
	if err != nil || resolver.calls != 1 {
		t.Fatalf("press after prewarm resolved again: %v calls=%d", err, resolver.calls)
	}

	cache.m["/stations/s1"] = CachedStreams{URLs: resolver.urls, At: time.Now().Add(-prewarmRefresh - time.Minute)}
	prewarmOnce(ctx, resolver, list, cache, prewarmRefresh, log)

	if resolver.calls != 2 {
		t.Fatalf("aged entry was not renewed, calls=%d", resolver.calls)
	}
	// In standby the same age is left alone until a day has passed.
	cache.m["/stations/s1"] = CachedStreams{URLs: resolver.urls, At: time.Now().Add(-prewarmRefresh - time.Minute)}
	prewarmOnce(ctx, resolver, list, cache, prewarmRefreshStandby, log)

	if resolver.calls != 2 {
		t.Fatalf("standby renewed a list younger than a day, calls=%d", resolver.calls)
	}
}
