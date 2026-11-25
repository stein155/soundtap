// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom_test

import (
	"testing"

	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/snapcast"
)

// init.d/soundtap builds the snapclient --hostID as "soundtap-" plus the speaker identity,
// so a snapcast client id and a SoundTap device id are the same string with and without
// that prefix. Everything below is the one place that relationship is expressed.
func TestDeviceIDForClient(t *testing.T) {
	t.Parallel()

	if got := multiroom.DeviceIDFor("soundtap-SERIAL0000000030A"); got != "SERIAL0000000030A" {
		t.Fatalf("DeviceIDFor = %q, want the identity without the prefix", got)
	}
	// Somebody else's snapclient on the same server is a member of the group like any
	// other and has no device id to report. Inventing one would put a device in the list
	// that /api/devices has never heard of.
	for _, foreign := range []string{"living-room-pi", "", "soundtap-", "SoundTap-ABC"} {
		if got := multiroom.DeviceIDFor(foreign); got != "" {
			t.Errorf("multiroom.DeviceIDFor(%q) = %q, want empty", foreign, got)
		}
	}
}

// snapClient is a snapcast client with only its id and connection set.
func snapClient(id string, connected bool) snapcast.ClientInfo {
	var client snapcast.ClientInfo

	client.ID, client.Connected = id, connected

	return client
}

func statusWith(ids ...string) snapcast.Status {
	group := snapcast.Group{ID: "g1", Clients: nil}
	for _, id := range ids {
		group.Clients = append(group.Clients, snapClient(id, true))
	}

	return snapcast.Status{Groups: []snapcast.Group{group}}
}

// A controller holding a device list must be able to act on multiroom without learning
// how the audio layer spells the same box.
func TestClientIDForAcceptsEitherSpelling(t *testing.T) {
	t.Parallel()

	status := statusWith("soundtap-SN1", "living-room-pi")

	if got, ok := multiroom.ClientIDFor(status, "soundtap-SN1"); !ok || got != "soundtap-SN1" {
		t.Fatalf("by client id = %q, %v", got, ok)
	}

	if got, ok := multiroom.ClientIDFor(status, "SN1"); !ok || got != "soundtap-SN1" {
		t.Fatalf("by device id = %q, %v; want the client id", got, ok)
	}
	// A client that is not ours is still addressable by its own id, and only that.
	if got, ok := multiroom.ClientIDFor(status, "living-room-pi"); !ok || got != "living-room-pi" {
		t.Fatalf("foreign client = %q, %v", got, ok)
	}

	if _, ok := multiroom.ClientIDFor(status, "SN-nobody"); ok {
		t.Fatal("resolved an id the server does not know")
	}
}

// The exact client id wins over the prefixed one. A snapclient literally called "SN1"
// alongside our "soundtap-SN1" is far-fetched, but resolving the caller's exact word to a
// different speaker is the kind of surprise that costs a house its groups.
func TestClientIDForPrefersTheExactMatch(t *testing.T) {
	t.Parallel()

	status := statusWith("SN1", "soundtap-SN1")
	if got, ok := multiroom.ClientIDFor(status, "SN1"); !ok || got != "SN1" {
		t.Fatalf("ClientIDFor = %q, %v; want the exact match", got, ok)
	}
}

func TestFollowerInItsOwnGroupCountsAsListener(t *testing.T) {
	t.Parallel()

	self := snapClient("soundtap-leader", true)
	follower := snapClient("soundtap-follower", true)
	gone := snapClient("soundtap-gone", false)

	solo := snapcast.Status{Groups: []snapcast.Group{
		{ID: "a", Clients: []snapcast.ClientInfo{self}}, {ID: "b", Clients: []snapcast.ClientInfo{gone}},
	}}
	if multiroom.HasListener(solo, self.ID) {
		t.Fatal("a disconnected client switched the tap on")
	}

	solo.Groups = append(solo.Groups, snapcast.Group{ID: "c", Clients: []snapcast.ClientInfo{follower}})
	if !multiroom.HasListener(solo, self.ID) {
		t.Fatal("a follower in its own group was missed")
	}
}
