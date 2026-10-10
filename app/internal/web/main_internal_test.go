// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/stein155/soundtap/app/internal/clocksync"
)

// Fake peers in these tests are httptest servers on loopback.
var strictPeerIP = peerIPAllowed

func TestMain(m *testing.M) {
	// An ask left running past its test would race the next test's package state.
	listenInBackground = func(string) func(context.Context) { return func(context.Context) {} }
	peerIPAllowed = func(ip net.IP) bool { return ip.IsLoopback() || strictPeerIP(ip) }
	// The alarm tests run on a clock NTP has set; one test takes the marker away.
	synced, err := os.CreateTemp("", "clock-synced")
	if err != nil {
		panic(err)
	}
	_ = synced.Close()
	clocksync.Path = synced.Name()
	code := m.Run()
	_ = os.Remove(synced.Name())
	os.Exit(code)
}
