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
	t.Parallel()

	speaker := speakerstate.New()

	ctx := t.Context()

	go runDevPlayer(ctx, speaker)

	err := speaker.Select(context.Background(), "TUNEIN", "", "/station/s47309", "NPO Radio 2", "", "")
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * devPlayDelay)
	for time.Now().Before(deadline) {
		if speaker.StatusSnapshot().PlayStatus == "PLAY_STATE" {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("playStatus = %q, want PLAY_STATE after the dev player's delay", speaker.StatusSnapshot().PlayStatus)
}

// A development run takes the computer's address for /api/info but announces nothing:
// no responder is made, so the nil web server here is never handed one.
//
//nolint:paralleltest // changes package-wide state other tests read: devMode
func TestDevModeAnnouncesNothing(t *testing.T) {
	devMode.Store(true)
	t.Cleanup(func() { devMode.Store(false) })

	speaker := speakerstate.New()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	maintainIPWith(ctx, speaker, nil, "test", devTestService(), quietLog(), time.Millisecond,
		func() string { return "192.0.2.10" })

	if got := speaker.StatusSnapshot().IP; got != "192.0.2.10" {
		t.Fatalf("IP = %q, want the host's address", got)
	}
}

// devTestService is a development run's service: an ID and nothing else to announce.
func devTestService() mdns.Service {
	return mdns.Service{ID: "X", Model: "", Firmware: "", API: 0, Port: 0}
}
