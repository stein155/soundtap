// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestSourceRoute(t *testing.T) {
	if got := sourceRoute("AUX"); got != "aux" {
		t.Fatalf("AUX route = %q, want aux", got)
	}
	for _, source := range []string{"STANDBY", "TUNEIN", "BLUETOOTH", "AIRPLAY", "SPOTIFY"} {
		if got := sourceRoute(source); got != "wifi" {
			t.Fatalf("%s route = %q, want wifi", source, got)
		}
	}
}

func TestST20DoesNotGuessBluetoothUART(t *testing.T) {
	if got := model.Named(model.ST20).BTUart; got != "" {
		t.Fatalf("ST20 Bluetooth UART = %q, want disabled until proven", got)
	}
}

// A notification is the one thing that has to be audible whatever is playing. On AUX
// the codec is routed to the analogue input in hardware, so a chime reached the sink
// with every counter moving and was never connected to the amplifier.
func TestAnnouncementTakesTheRouteBackFromAUX(t *testing.T) {
	t.Cleanup(func() { announcementsInFlight.Store(0) })
	announcementsInFlight.Add(1)
	if got := sourceRoute("AUX"); got != "wifi" {
		t.Fatalf("AUX route during an announcement = %q, want wifi", got)
	}
	announcementsInFlight.Add(-1)
	if got := sourceRoute("AUX"); got != "aux" {
		t.Fatalf("AUX route after the announcement = %q, want aux", got)
	}
}

// An announcement over AUX changes no speaker state, so the routing loop has to be
// woken by the announcement count itself, both when the clip starts and when it ends.
func TestRoutingFollowsAnAnnouncementOverAUX(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	saved := shelbySource
	shelbySource = path
	t.Cleanup(func() { shelbySource = saved; announcementsInFlight.Store(0) })
	sp := speakerstate.New()
	if err := sp.SetSource(context.Background(), "AUX"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runSourceRouting(ctx, sp, slog.New(slog.DiscardHandler))
	waitRoute := func(want string) {
		t.Helper()
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			if b, _ := os.ReadFile(path); strings.TrimSpace(string(b)) == want {
				return
			}
		}
		b, _ := os.ReadFile(path)
		t.Fatalf("route = %q, want %q", b, want)
	}
	waitRoute("aux")
	announcementDemand(1)
	waitRoute("wifi")
	announcementDemand(-1)
	waitRoute("aux")
}
