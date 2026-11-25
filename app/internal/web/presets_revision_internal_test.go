// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"testing"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// The SSE revision must change with any slot, so an open page re-reads presets made
// elsewhere, and stay put otherwise so idle clients are not woken.
func TestPresetsRevisionFollowsSlots(t *testing.T) {
	t.Parallel()

	first := []speaker.Preset{{
		Slot: 1, Name: "NPO Radio 2", StationID: "s9483", Source: "", Type: "",
		Location: "/v1/playback/station/s9483", Logo: "",
	}}
	second := []speaker.Preset{{
		Slot: 1, Name: "538", StationID: "s6712", Source: "", Type: "",
		Location: "/v1/playback/station/s6712", Logo: "",
	}}
	same := []speaker.Preset{first[0]}

	if presetsRevision(first) != presetsRevision(same) {
		t.Fatal("same presets, different revision")
	}

	if presetsRevision(first) == presetsRevision(second) {
		t.Fatal("changed preset, same revision")
	}

	logo := []speaker.Preset{first[0]}

	logo[0].Logo = "https://example.com/new.png"
	if presetsRevision(first) == presetsRevision(logo) {
		t.Fatal("changed logo, same revision")
	}

	if presetsRevision(first) == presetsRevision(nil) {
		t.Fatal("cleared presets, same revision")
	}
}
