// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func chimeOf(ch chan string) string {
	select {
	case n := <-ch:
		return n
	case <-time.After(300 * time.Millisecond):
		return ""
	}
}

//nolint:paralleltest // it swaps the package-level leader lookup
func TestFollowAndBluetoothConnectChime(t *testing.T) {
	// Without this the test dials a real LAN address, and a dial still running after the
	// test ends reads peerIPAllowed while a later test writes it.
	defer func(f func(context.Context, string) string) { peerLeader = f }(peerLeader)

	peerLeader = func(context.Context, string) string { return "" }
	chimes := make(chan string, 4)
	srv := new(Server)
	srv.homeDir = t.TempDir()
	srv.eventChime = func(n string) { chimes <- n }
	srv.selectSource = func(context.Context, string) error { return nil }
	post := func(host string) {
		rec := httptest.NewRecorder()
		srv.postMultiroomFollow(
			rec,
			httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/multiroom/follow",
				strings.NewReader(`{"host":"`+host+`"}`)),
		)

		if rec.Code != http.StatusOK {
			t.Fatalf("follow %q: %d", host, rec.Code)
		}
	}
	post("10.0.1.126")

	if got := chimeOf(chimes); got != "grouped.wav" {
		t.Fatalf("joining a group chimed %q", got)
	}

	post("10.0.1.126") // unchanged
	post("")           // leaving

	if got := chimeOf(chimes); got != "" {
		t.Fatalf("unexpected chime %q", got)
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/bluetooth/connected", nil)
	r.RemoteAddr = "192.168.1.9:5555"
	srv.bluetoothConnected(rec, r)

	if rec.Code != http.StatusForbidden || chimeOf(chimes) != "" {
		t.Fatalf("a LAN caller got through: %d", rec.Code)
	}

	r.RemoteAddr = "127.0.0.1:5555"
	srv.bluetoothConnected(httptest.NewRecorder(), r)

	if got := chimeOf(chimes); got != "bt-connected.wav" {
		t.Fatalf("connect chimed %q", got)
	}
}
