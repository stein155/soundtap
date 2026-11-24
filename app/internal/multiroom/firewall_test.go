// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestAllowFollowerRunsNftForIPv4Only(t *testing.T) {
	forgetAllAllowed(t)
	orig := runNft
	t.Cleanup(func() { runNft = orig })
	var got []string
	runNft = func(_ context.Context, args ...string) error { got = args; return errors.New("no nft") }

	AllowFollower(context.Background(), "192.168.1.7:80")
	want := []string{"add", "element", "ip", "filter", "snap_followers", "{", "192.168.1.7", "timeout", "10m", "}"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv %v, want %v", got, want)
	}
	got = nil
	AllowFollower(context.Background(), "nonexistent.invalid")
	AllowFollower(context.Background(), "fe80::1")
	if got != nil {
		t.Fatalf("non-IPv4 ran nft: %v", got)
	}
}

// A repeated ask inside allowRefresh runs no nft; a failed run is retried.
func TestAllowFollowerRunsNftOncePerRefresh(t *testing.T) {
	forgetAllAllowed(t)
	orig := runNft
	t.Cleanup(func() { runNft = orig })
	runs := 0
	fail := true
	runNft = func(context.Context, ...string) error {
		runs++
		if fail {
			return errors.New("busy")
		}
		return nil
	}
	ip := "192.168.1.8"
	AllowFollower(context.Background(), ip)
	fail = false
	for range 50 {
		AllowFollower(context.Background(), ip)
	}
	if runs != 2 {
		t.Fatalf("nft ran %d times, want 2 (failed try, then one success)", runs)
	}
}

// forgetAllAllowed empties the refresh memory before and after a test, so a second run
// (go test -count) starts as the first did.
func forgetAllAllowed(t *testing.T) {
	t.Helper()
	reset := func() {
		allowMu.Lock()
		allowedAt = map[string]time.Time{}
		allowMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}
