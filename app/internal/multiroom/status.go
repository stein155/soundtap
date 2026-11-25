// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package multiroom is the speaker's side of a multiroom group: who is in it, whom this
// speaker follows, and the calls between leader and followers. See ADR 0122.
package multiroom

import (
	"strings"

	"github.com/stein155/soundtap/app/internal/snapcast"
)

// snapcastHostPrefix is where the two namespaces meet. init.d/soundtap builds the
// snapclient --hostID as this prefix followed by the speaker identity, so a snapcast
// client id and a SoundTap device id are the same string with and without it. Keeping that
// in one named place is the difference between a mapping and a convention nobody wrote
// down; see cmd/soundtapd/identity.go.
const snapcastHostPrefix = "soundtap-"

// DeviceIDFor returns the SoundTap device id behind a snapcast client id, or "" when the
// client is not one of ours -- somebody else's snapclient on the same server is a member
// of the group like any other, and has no device id to report.
func DeviceIDFor(clientID string) string {
	rest, ok := strings.CutPrefix(clientID, snapcastHostPrefix)
	if !ok || rest == "" {
		return ""
	}

	return rest
}

// ClientIDFor resolves what a caller named -- a snapcast client id or a SoundTap device
// id -- to the snapcast client id, and reports whether the server knows it. Accepting
// both is what lets a controller work from /api/devices without learning the prefix.
func ClientIDFor(status snapcast.Status, name string) (string, bool) {
	if _, ok := FindClient(status, name); ok {
		return name, true
	}

	prefixed := snapcastHostPrefix + name
	if _, ok := FindClient(status, prefixed); ok {
		return prefixed, true
	}

	return name, false
}

// FindClient returns the id of the group that client id is in.
func FindClient(status snapcast.Status, id string) (string, bool) {
	for _, group := range status.Groups {
		for _, client := range group.Clients {
			if client.ID == id {
				return group.ID, true
			}
		}
	}

	return "", false
}

// FindClientVolume returns client id's volume.
func FindClientVolume(status snapcast.Status, id string) (snapcast.Volume, bool) {
	for _, group := range status.Groups {
		for _, client := range group.Clients {
			if client.ID == id {
				return client.Config.Volume, true
			}
		}
	}

	return snapcast.Volume{Percent: 0, Muted: false}, false
}

// HasListener reports whether any other connected client listens to this speaker's
// server, which is when the tap has to feed Snapcast and the DAC has to wait for it. Any
// group counts: the server has one stream, and a speaker that follows lands in a group of
// its own, so asking only about this speaker's group missed every follower and left the
// leader's DAC undelayed -- the lag between the WLA and the ST10 (2026-09-29).
func HasListener(status snapcast.Status, localID string) bool {
	for _, group := range status.Groups {
		for _, client := range group.Clients {
			if client.Connected && client.ID != localID {
				return true
			}
		}
	}

	return false
}
