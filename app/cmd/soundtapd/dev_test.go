// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/mdns"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// Under -dev a selected station plays, silently, so the UI can be worked on; with plain
// -no-audio it would fall back to STOP_STATE.
func TestDevPlayerPlaysWhatBuffers(t *testing.T) {
	sp := speakerstate.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runDevPlayer(ctx, sp)

	if err := sp.Select(context.Background(), "TUNEIN", "", "/station/s47309", "NPO Radio 2", "", ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * devPlayDelay)
	for time.Now().Before(deadline) {
		if sp.StatusSnapshot().PlayStatus == "PLAY_STATE" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("playStatus = %q, want PLAY_STATE after the dev player's delay", sp.StatusSnapshot().PlayStatus)
}

// A development run takes the computer's address for /api/info but announces nothing:
// no responder is made, so the nil web server here is never handed one.
func TestDevModeAnnouncesNothing(t *testing.T) {
	devMode.Store(true)
	t.Cleanup(func() { devMode.Store(false) })
	sp := speakerstate.New()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	maintainIPWith(ctx, sp, nil, "test", mdns.Service{ID: "X"}, quietLog(), time.Millisecond,
		func() string { return "192.0.2.10" })
	if got := sp.StatusSnapshot().IP; got != "192.0.2.10" {
		t.Fatalf("IP = %q, want the host's address", got)
	}
}
