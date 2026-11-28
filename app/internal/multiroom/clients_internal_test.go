// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"testing"
)

// row is a /api/multiroom row with the fields dropGhosts and dropForgotten read.
func row(id, deviceID, host string, connected bool, lastSeenAgo int) Client {
	return Client{
		ID: id, Name: "", Host: host, IP: "", Percent: 0, Muted: false, GroupID: "",
		DeviceID: deviceID, Current: false, Member: false, Connected: connected, Latency: 0, LastSeenAgo: lastSeenAgo,
		Leader: "", LeaderDeviceID: "",
	}
}

// Changing a speaker's snapclient host id leaves the old one in snapcast's client list,
// disconnected and never coming back. Measured on the board after the identity change:
// soundtap-<mac> and soundtap-<serial> both appeared, one box claiming to be two.
func TestGhostOfARenamedSpeakerIsDropped(t *testing.T) {
	t.Parallel()

	rows := []Client{
		row("soundtap-020000a1b231", "020000a1b231", "", false, 0),
		row("soundtap-SN1", "SN1", "", true, 0),
		row("soundtap-020000a1b231", "SN1", "", false, 0),
	}

	got := dropGhosts(rows)
	if len(got) != 2 {
		t.Fatalf("kept %d rows, want 2: %+v", len(got), got)
	}
	// The one that shares a device id with a connected row goes; the one that does not is
	// a different speaker that really is offline, and saying so is the point of keeping it.
	for _, kept := range got {
		if kept.DeviceID == "SN1" && !kept.Connected {
			t.Fatalf("kept the ghost of a connected speaker: %+v", kept)
		}
	}

	if got[0].DeviceID != "020000a1b231" {
		t.Fatalf("dropped a genuinely offline speaker: %+v", got)
	}
}

// A snapclient that is not ours has no device id, and two of them being offline together
// must not collapse into one.
func TestForeignClientsAreNeverCollapsed(t *testing.T) {
	t.Parallel()

	rows := []Client{
		row("pi-a", "", "", false, 0),
		row("pi-b", "", "", false, 0),
		row("soundtap-SN1", "SN1", "", true, 0),
	}
	if got := dropGhosts(rows); len(got) != 3 {
		t.Fatalf("kept %d rows, want 3: %+v", len(got), got)
	}
}

// Nothing connected means nothing to be a ghost of.
func TestAllOfflineIsLeftAlone(t *testing.T) {
	t.Parallel()

	rows := []Client{
		row("soundtap-SN1", "SN1", "", false, 0),
		row("soundtap-SN2", "SN2", "", false, 0),
	}
	if got := dropGhosts(rows); len(got) != 2 {
		t.Fatalf("kept %d rows, want 2", len(got))
	}
}

// The WLA on 2026-10-01: its MAC-based id (device id 020000a1b231) and its serial-based
// id (SERIAL0000000030A) are different device ids, but both snapclients ran on the same
// host, and the old one had been gone for 12 days.
func TestGhostWithSameHostIsDropped(t *testing.T) {
	t.Parallel()

	rows := []Client{
		row("soundtap-020000a1b231", "020000a1b231", "soundtap-a1b231", false, 0),
		row("soundtap-SERIAL0000000030A", "SERIAL0000000030A", "soundtap-a1b231", true, 0),
		row("soundtap-SERIAL0000000020A", "SERIAL0000000020A", "(none)", false, 0),
		row("soundtap-0aaaaaaaaaaa", "0aaaaaaaaaaa", "(none)", false, 0),
	}

	got := dropGhosts(rows)
	if len(got) != 3 || got[0].DeviceID != "SERIAL0000000030A" {
		t.Fatalf("want the old WLA id dropped and both (none) hosts kept: %+v", got)
	}
}

// snapserver never forgets a client. Speakers gone for longer than an hour must not stay in
// /api/multiroom (the bench on 2026-11-28 showed members last seen 29 h ago); a speaker
// that is merely offline for a few minutes must.
func TestForgottenClientsAreDropped(t *testing.T) {
	t.Parallel()

	rows := []Client{
		row("soundtap-SN2", "SN2", "", false, 29*3600),
		row("soundtap-SN3", "SN3", "", false, 10*60),
		row("soundtap-SN4", "SN4", "", true, 0),
		row("soundtap-SN5", "SN5", "", false, 0),
		row("soundtap-SN6", "SN6", "", true, 0),
	}

	got := dropForgotten(rows)
	if len(got) != 4 || got[0].ID != "soundtap-SN3" || got[3].ID != "soundtap-SN6" {
		t.Fatalf("kept %+v, want SN3, SN4, SN5, SN6", got)
	}
}

// A row offline just past forgetAfter goes; one just inside it stays.
func TestForgetAfterIsTheBound(t *testing.T) {
	t.Parallel()

	limit := int(forgetAfter.Seconds())
	rows := []Client{
		row("soundtap-SN2", "SN2", "", false, limit+1),
		row("soundtap-SN3", "SN3", "", false, limit),
	}

	got := dropForgotten(rows)
	if len(got) != 1 || got[0].ID != "soundtap-SN3" {
		t.Fatalf("kept %+v, want only SN3", got)
	}
}

// A follower's own row names the leader it follows; no other row does, and a speaker that
// follows nobody names none.
func TestMarkLeaderSetsTheCurrentRow(t *testing.T) {
	t.Parallel()

	rows := []Client{row("soundtap-SN1", "SN1", "", true, 0), row("soundtap-SN2", "SN2", "", false, 60)}
	rows[0].Current = true

	MarkLeader(rows, "", "")

	if rows[0].Leader != "" || rows[0].LeaderDeviceID != "" {
		t.Fatalf("not following, yet leader set: %+v", rows[0])
	}

	MarkLeader(rows, "192.0.2.10", "SN9")

	if rows[0].Leader != "192.0.2.10" || rows[0].LeaderDeviceID != "SN9" {
		t.Fatalf("current row = %+v, want leader 192.0.2.10 / SN9", rows[0])
	}

	if rows[1].Leader != "" || rows[1].LeaderDeviceID != "" {
		t.Fatalf("another row got the leader: %+v", rows[1])
	}
}
