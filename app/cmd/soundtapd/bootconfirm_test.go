// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/counters"
)

type confirmRecorder struct{ n atomic.Int32 }

func (c *confirmRecorder) ConfirmBoot() error {
	c.n.Add(1)
	return nil
}

func quickConfirm(t *testing.T) {
	t.Helper()
	after, poll := bootConfirmAfter, bootConfirmPoll
	bootConfirmAfter, bootConfirmPoll = time.Millisecond, time.Millisecond
	t.Cleanup(func() { bootConfirmAfter, bootConfirmPoll = after, poll })
}

// runConfirm runs confirmBoot until it returns or d passed, and says how often it confirmed.
func runConfirm(t *testing.T, g *bootGate, checks []bootCheck, d time.Duration) int32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var rec confirmRecorder
	confirmBoot(ctx, &rec, g, checks, quietLog())
	return rec.n.Load()
}

func TestConfirmBootOnlyWhenTheAPIAnswers(t *testing.T) {
	quickConfirm(t)
	up := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer up.Close()
	g := newBootGate()
	if n := runConfirm(t, g, g.checks(up.URL), 5*time.Second); n != 1 {
		t.Fatalf("answering API: confirmed %d times", n)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	g = newBootGate()
	if n := runConfirm(t, g, g.checks(down.URL), 200*time.Millisecond); n != 0 {
		t.Fatalf("failing API: confirmed %d times", n)
	}
}

// One slow answer used to leave a good boot unconfirmed for its whole uptime, and the next
// reboots counted towards a rollback. The API is asked again until it answers.
func TestConfirmBootAsksTheAPIAgain(t *testing.T) {
	quickConfirm(t)
	var asked atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if asked.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer api.Close()
	g := newBootGate()
	counters.Reset()
	t.Cleanup(counters.Reset)
	if n := runConfirm(t, g, g.checks(api.URL), 5*time.Second); n != 1 || asked.Load() != 3 {
		t.Fatalf("confirmed %d times after %d probes, want once after 3", n, asked.Load())
	}
	// /api/health counts the two looks that found the boot not good yet (ADR 0127).
	if n := counters.BootConfirmRetries.Load(); n != 2 {
		t.Errorf("%d boot confirm retries, want 2", n)
	}
}

func TestConfirmBootWaitsTheMinute(t *testing.T) {
	after := bootConfirmAfter
	bootConfirmAfter = time.Hour
	t.Cleanup(func() { bootConfirmAfter = after })
	met := func(context.Context) string { return "" }
	if n := runConfirm(t, newBootGate(), []bootCheck{met}, 50*time.Millisecond); n != 0 {
		t.Fatalf("confirmed %d times before bootConfirmAfter", n)
	}
}

// Every model but the ST20 waits for the API alone: no network, no audio. A speaker with no
// router, or with its audio broken, still keeps a good image.
func TestConfirmBootChecksNoNetwork(t *testing.T) {
	g := newBootGate()
	if n := len(g.checks("http://127.0.0.1:1/api/version")); n != 1 {
		t.Fatalf("%d checks, want the API probe alone", n)
	}
}

// The ST20 confirms only once its BCO install step is over and our BCO app's link is up: a
// slot whose BCO image does not come up rolls back, and the other slot puts its own back.
func TestConfirmBootOnTheST20WaitsForTheBCO(t *testing.T) {
	quickConfirm(t)
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer api.Close()
	netDir := t.TempDir()
	newST20 := func() *bootGate {
		g := newBootGate()
		g.netDir = netDir
		g.waitForBCO()
		return g
	}
	link := filepath.Join(netDir, "bcolink0")

	// Flash still running, link up (the old app): not yet.
	if err := os.WriteFile(link, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	g := newST20()
	if n := runConfirm(t, g, g.checks(api.URL), 100*time.Millisecond); n != 0 {
		t.Fatalf("confirmed %d times during the BCO install", n)
	}
	// Install over, no link: our app does not run, so not either.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	g = newST20()
	g.bcoDone()
	if n := runConfirm(t, g, g.checks(api.URL), 100*time.Millisecond); n != 0 {
		t.Fatalf("confirmed %d times without bcolink0", n)
	}
	// The install ends and the link comes up while confirmBoot waits: confirmed then.
	g = newST20()
	go func() {
		time.Sleep(20 * time.Millisecond)
		g.bcoDone()
		_ = os.WriteFile(link, nil, 0o644)
	}()
	if n := runConfirm(t, g, g.checks(api.URL), 5*time.Second); n != 1 {
		t.Fatalf("BCO installed and linked: confirmed %d times", n)
	}
	if !g.awaitAttempt(context.Background(), time.Second) {
		t.Fatal("the attempt was not reported to the BCO reboot")
	}
}

// The reboot after a first install of our BCO app waits for the confirmation, bounded.
func TestAwaitAttemptIsBounded(t *testing.T) {
	g := newBootGate()
	start := time.Now()
	if g.awaitAttempt(context.Background(), 20*time.Millisecond) {
		t.Fatal("an attempt that never came was reported")
	}
	if time.Since(start) > time.Second {
		t.Fatal("awaitAttempt did not keep to its bound")
	}
	g.bcoDone() // no BCO step on this model: a no-op
}

// fakeBCO is the BCO install step's world for keepBCOInstalled.
type fakeBCO struct {
	own     bool
	results []error // one per attempt; past the end, success
	busy    int     // how many lock attempts find it busy
	tries   int
	waits   []time.Duration
}

func (f *fakeBCO) lock() (func(), bool) {
	if f.busy > 0 {
		f.busy--
		return nil, false
	}
	return func() {}, true
}

func (f *fakeBCO) attempt(context.Context) (bool, error) {
	f.tries++
	if f.tries <= len(f.results) && f.results[f.tries-1] != nil {
		return false, f.results[f.tries-1]
	}
	f.own = true
	return true, nil
}

func (f *fakeBCO) wait(_ context.Context, d time.Duration) bool {
	f.waits = append(f.waits, d)
	return true
}

func (f *fakeBCO) run() bool {
	return keepBCOInstalled(context.Background(), f.lock, f.attempt, func() bool { return f.own }, f.wait, quietLog())
}

func TestKeepBCOInstalledRetriesWithoutOwnApp(t *testing.T) {
	cut := errors.New("cut off")
	f := &fakeBCO{results: []error{cut, cut, cut, cut}}
	if !f.run() || f.tries != 5 {
		t.Fatalf("flashed after %d tries, want the fifth", f.tries)
	}
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute}
	if len(f.waits) != len(want) {
		t.Fatalf("waits %v, want %v", f.waits, want)
	}
	for i := range want {
		if f.waits[i] != want[i] {
			t.Fatalf("waits %v, want %v", f.waits, want)
		}
	}
}

func TestKeepBCOInstalledLeavesOwnAppAlone(t *testing.T) {
	// A failed flash that left our own app running is not retried: the LAN runs through it.
	f := &fakeBCO{own: true, results: []error{errors.New("tftp")}}
	if f.run() || f.tries != 1 || len(f.waits) != 0 {
		t.Fatalf("tries=%d waits=%v", f.tries, f.waits)
	}
	// Busy with our app on the BCO: next boot.
	f = &fakeBCO{own: true, busy: 1}
	if f.run() || f.tries != 0 {
		t.Fatalf("busy lock: tries=%d", f.tries)
	}
	// Busy without it: waited out, then installed.
	f = &fakeBCO{busy: 2}
	if !f.run() || f.tries != 1 || len(f.waits) != 2 {
		t.Fatalf("busy without own app: tries=%d waits=%v", f.tries, f.waits)
	}
}

func TestKeepBCOInstalledStopsWithTheDaemon(t *testing.T) {
	f := &fakeBCO{results: []error{errors.New("cut off")}}
	stopped := func(context.Context, time.Duration) bool { return false }
	if keepBCOInstalled(context.Background(), f.lock, f.attempt, func() bool { return f.own }, stopped, quietLog()) || f.tries != 1 {
		t.Fatalf("tries=%d after the daemon stopped", f.tries)
	}
}
