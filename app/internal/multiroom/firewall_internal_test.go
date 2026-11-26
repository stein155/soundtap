// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// errNft is an nft run that failed.
var errNft = errors.New("no nft")

func TestAllowFollowerPutsEachFamilyInItsOwnSet(t *testing.T) {
	t.Parallel()

	var got [][]string

	list := newAllowList(func(_ context.Context, args ...string) error {
		got = append(got, args)

		return errNft
	})

	list.allow(context.Background(), "192.168.1.7:80")
	list.allow(context.Background(), "[fe80::1%wlan0]:51234")
	list.allow(context.Background(), "::ffff:192.168.1.9")
	list.allow(context.Background(), "2001:db8::7")

	want := [][]string{
		{"add", "element", "inet", "filter", "snap_followers", "{", "192.168.1.7", "timeout", "10m", "}"},
		{"add", "element", "inet", "filter", "snap_followers6", "{", "fe80::1", "timeout", "10m", "}"},
		{"add", "element", "inet", "filter", "snap_followers", "{", "192.168.1.9", "timeout", "10m", "}"},
		{"add", "element", "inet", "filter", "snap_followers6", "{", "2001:db8::7", "timeout", "10m", "}"},
	}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("argv %v, want %v", got, want)
	}

	got = nil

	list.allow(context.Background(), "nonexistent.invalid")
	list.allow(context.Background(), "::1")
	list.allow(context.Background(), "ff02::fb")
	list.allow(context.Background(), "0.0.0.0")

	if got != nil {
		t.Fatalf("an unresolvable, loopback, multicast or unspecified address ran nft: %v", got)
	}
}

// A repeated ask inside allowRefresh runs no nft; a failed run is retried.
func TestAllowFollowerRunsNftOncePerRefresh(t *testing.T) {
	t.Parallel()

	runs := 0
	fail := true
	list := newAllowList(func(context.Context, ...string) error {
		runs++

		if fail {
			return errNft
		}

		return nil
	})
	follower := "192.168.1.8"
	list.allow(context.Background(), follower)

	fail = false

	for range 50 {
		list.allow(context.Background(), follower)
	}

	if runs != 2 {
		t.Fatalf("nft ran %d times, want 2 (failed try, then one success)", runs)
	}
}
