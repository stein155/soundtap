// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"testing"
)

// Changing a speaker's snapclient host id leaves the old one in snapcast's client list,
// disconnected and never coming back. Measured on the board after the identity change:
// soundtap-<mac> and soundtap-<serial> both appeared, one box claiming to be two.
func TestGhostOfARenamedSpeakerIsDropped(t *testing.T) {
	rows := []Client{
		{ID: "soundtap-020000a1b231", DeviceID: "020000a1b231", Connected: false},
		{ID: "soundtap-SN1", DeviceID: "SN1", Connected: true},
		{ID: "soundtap-020000a1b231", DeviceID: "SN1", Connected: false},
	}
	got := dropGhosts(rows)
	if len(got) != 2 {
		t.Fatalf("kept %d rows, want 2: %+v", len(got), got)
	}
	// The one that shares a device id with a connected row goes; the one that does not is
	// a different speaker that really is offline, and saying so is the point of keeping it.
	for _, row := range got {
		if row.DeviceID == "SN1" && !row.Connected {
			t.Fatalf("kept the ghost of a connected speaker: %+v", row)
		}
	}
	if got[0].DeviceID != "020000a1b231" {
		t.Fatalf("dropped a genuinely offline speaker: %+v", got)
	}
}

// A snapclient that is not ours has no device id, and two of them being offline together
// must not collapse into one.
func TestForeignClientsAreNeverCollapsed(t *testing.T) {
	rows := []Client{
		{ID: "pi-a", Connected: false},
		{ID: "pi-b", Connected: false},
		{ID: "soundtap-SN1", DeviceID: "SN1", Connected: true},
	}
	if got := dropGhosts(rows); len(got) != 3 {
		t.Fatalf("kept %d rows, want 3: %+v", len(got), got)
	}
}

// Nothing connected means nothing to be a ghost of.
func TestAllOfflineIsLeftAlone(t *testing.T) {
	rows := []Client{
		{ID: "soundtap-SN1", DeviceID: "SN1", Connected: false},
		{ID: "soundtap-SN2", DeviceID: "SN2", Connected: false},
	}
	if got := dropGhosts(rows); len(got) != 2 {
		t.Fatalf("kept %d rows, want 2", len(got))
	}
}

// The WLA on 2026-10-01: its MAC-based id (device id 020000a1b231) and its serial-based
// id (SERIAL0000000030A) are different device ids, but both snapclients ran on the same
// host, and the old one had been gone for 12 days.
func TestGhostWithSameHostIsDropped(t *testing.T) {
	rows := []Client{
		{ID: "soundtap-020000a1b231", DeviceID: "020000a1b231", Host: "soundtap-a1b231", Connected: false},
		{ID: "soundtap-SERIAL0000000030A", DeviceID: "SERIAL0000000030A", Host: "soundtap-a1b231", Connected: true},
		{ID: "soundtap-SERIAL0000000020A", DeviceID: "SERIAL0000000020A", Host: "(none)", Connected: false},
		{ID: "soundtap-0aaaaaaaaaaa", DeviceID: "0aaaaaaaaaaa", Host: "(none)", Connected: false},
	}
	got := dropGhosts(rows)
	if len(got) != 3 || got[0].DeviceID != "SERIAL0000000030A" {
		t.Fatalf("want the old WLA id dropped and both (none) hosts kept: %+v", got)
	}
}

// snapserver never forgets a client. Speakers gone for over a week must not stay in
// /api/multiroom; a speaker that is merely offline for a while must.
func TestForgottenClientsAreDropped(t *testing.T) {
	rows := []Client{
		{ID: "soundtap-SN2", DeviceID: "SN2", Connected: false, LastSeenAgo: 8 * 24 * 3600},
		{ID: "soundtap-SN3", DeviceID: "SN3", Connected: false, LastSeenAgo: 3600},
		{ID: "soundtap-SN4", DeviceID: "SN4", Connected: true},
		{ID: "soundtap-SN5", DeviceID: "SN5", Connected: false},
		{ID: "soundtap-SN6", DeviceID: "SN6", Connected: true},
	}
	got := dropForgotten(rows)
	if len(got) != 4 || got[0].ID != "soundtap-SN3" || got[3].ID != "soundtap-SN6" {
		t.Fatalf("kept %+v, want SN3, SN4, SN5, SN6", got)
	}
}
