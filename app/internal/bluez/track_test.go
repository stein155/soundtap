// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bluez

import (
	"maps"
	"testing"

	"github.com/godbus/dbus/v5"
)

type object struct {
	path   dbus.ObjectPath
	ifaces map[string]map[string]dbus.Variant
}

func objects(objs ...object) managedObjects {
	out := managedObjects{}
	for _, o := range objs {
		if out[o.path] == nil {
			out[o.path] = map[string]map[string]dbus.Variant{}
		}
		maps.Copy(out[o.path], o.ifaces)
	}
	return out
}

func player(path, status, title string) object {
	track := map[string]dbus.Variant{
		"Title":    dbus.MakeVariant(title),
		"Artist":   dbus.MakeVariant("Artist of " + title),
		"Album":    dbus.MakeVariant("Album of " + title),
		"Duration": dbus.MakeVariant(uint32(1000)),
	}
	return object{adapterPath + dbus.ObjectPath(path), map[string]map[string]dbus.Variant{playerIface: {
		"Status": dbus.MakeVariant(status),
		"Track":  dbus.MakeVariant(track),
	}}}
}

func TestPlayingTrackPrefersThePlayerThatPlays(t *testing.T) {
	want := Track{"B", "Artist of B", "Album of B"}
	if got := playingTrack(objects(player("/dev_1/player0", "paused", "A"), player("/dev_2/player0", "playing", "B"))); got != want {
		t.Errorf("got %+v", got)
	}
}

func TestPlayingTrackFallsBackToAnIdlePlayer(t *testing.T) {
	if got := playingTrack(objects(player("/dev_1/player0", "paused", "A"))); got.Title != "A" {
		t.Errorf("got %+v", got)
	}
}

func TestPlayingTrackIsEmptyWithoutAPlayer(t *testing.T) {
	device := object{adapterPath + "/dev_1", map[string]map[string]dbus.Variant{deviceIface: {}}}
	other := object{"/somewhere/else", map[string]map[string]dbus.Variant{playerIface: {}}}
	if got := playingTrack(objects(device, other)); got != (Track{}) {
		t.Errorf("got %+v", got)
	}
}

func phone(path, alias string, connected bool) object {
	return object{adapterPath + dbus.ObjectPath(path), map[string]map[string]dbus.Variant{deviceIface: {
		"Alias":     dbus.MakeVariant(alias),
		"Connected": dbus.MakeVariant(connected),
	}}}
}

func TestConnectedPhone(t *testing.T) {
	for _, c := range []struct {
		name string
		objs []object
		want string
	}{
		{"nothing connected", []object{phone("/dev_1", "Paired only", false)}, ""},
		{"one connected", []object{phone("/dev_1", "Paired only", false), phone("/dev_2", "Anna", true)}, "Anna"},
		{"the one that plays", []object{
			phone("/dev_1", "Idle", true), phone("/dev_2", "Anna", true),
			player("/dev_1/player0", "paused", "A"), player("/dev_2/player0", "playing", "B"),
		}, "Anna"},
		{"two connected, none playing: the first by path", []object{
			phone("/dev_2", "Bert", true), phone("/dev_1", "Anna", true),
		}, "Anna"},
	} {
		for range 20 { // map order must not decide
			if got := connectedPhone(objects(c.objs...)); got != c.want {
				t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
			}
		}
	}
}
