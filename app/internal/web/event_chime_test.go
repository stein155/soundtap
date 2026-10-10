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

func TestFollowAndBluetoothConnectChime(t *testing.T) {
	// Without this the test dials a real LAN address, and a dial still running after the
	// test ends reads peerIPAllowed while a later test writes it.
	defer func(f func(context.Context, string) string) { peerLeader = f }(peerLeader)
	peerLeader = func(context.Context, string) string { return "" }
	ch := make(chan string, 4)
	s := &Server{
		homeDir:      t.TempDir(),
		eventChime:   func(n string) { ch <- n },
		selectSource: func(context.Context, string) error { return nil },
	}
	post := func(host string) {
		w := httptest.NewRecorder()
		s.postMultiroomFollow(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/multiroom/follow",
			strings.NewReader(`{"host":"`+host+`"}`)))
		if w.Code != http.StatusOK {
			t.Fatalf("follow %q: %d", host, w.Code)
		}
	}
	post("10.0.1.126")
	if got := chimeOf(ch); got != "grouped.wav" {
		t.Fatalf("joining a group chimed %q", got)
	}
	post("10.0.1.126") // unchanged
	post("")           // leaving
	if got := chimeOf(ch); got != "" {
		t.Fatalf("unexpected chime %q", got)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/bluetooth/connected", nil)
	r.RemoteAddr = "192.168.1.9:5555"
	s.bluetoothConnected(w, r)
	if w.Code != http.StatusForbidden || chimeOf(ch) != "" {
		t.Fatalf("a LAN caller got through: %d", w.Code)
	}
	r.RemoteAddr = "127.0.0.1:5555"
	s.bluetoothConnected(httptest.NewRecorder(), r)
	if got := chimeOf(ch); got != "bt-connected.wav" {
		t.Fatalf("connect chimed %q", got)
	}
}
