// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bluez

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/sysbus"
)

const (
	playerIface = "org.bluez.MediaPlayer1"
	deviceIface = "org.bluez.Device1"
)

// Track is what a connected phone says is playing, over AVRCP. Zero when nothing says so.
type Track struct{ Title, Artist, Album string }

// OnTrack hands the connected phone's name and its track to set whenever they are read: on
// each change BlueZ signals and on the agent's tick, so a phone that was already playing shows
// too. want keeps the bus quiet while Bluetooth is not the source.
func (a *Agent) OnTrack(want func() bool, set func(phone string, t Track)) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.wantTrack, a.setTrack = want, set
}

func (a *Agent) followTrack(ctx context.Context, c *sysbus.Conn) {
	a.mu.Lock()
	want, set := a.wantTrack, a.setTrack
	a.mu.Unlock()

	if set == nil || !want() {
		return
	}

	objs, err := getManagedObjects(ctx, c)
	if err != nil {
		return // keeps what it has: the next tick tries again
	}

	set(connectedPhone(objs), playingTrack(objs))
}

// eachOnAdapter calls f, in path order, with the path and properties of every object of
// our adapter in a GetManagedObjects answer that has the interface.
func eachOnAdapter(objs managedObjects, iface string, f func(path dbus.ObjectPath, props map[string]dbus.Variant)) {
	for _, path := range slices.Sorted(maps.Keys(objs)) {
		if !strings.HasPrefix(string(path), string(adapterPath)+"/") {
			continue
		}

		if props, ok := objs[path][iface]; ok {
			f(path, props)
		}
	}
}

// connectedPhone names the connected phone, the one that plays when two are connected.
func connectedPhone(objs managedObjects) string {
	var playing dbus.ObjectPath

	eachOnAdapter(objs, playerIface, func(path dbus.ObjectPath, props map[string]dbus.Variant) {
		if props["Status"].Value() == "playing" {
			playing = path
		}
	})

	name := ""

	eachOnAdapter(objs, deviceIface, func(path dbus.ObjectPath, props map[string]dbus.Variant) {
		if on, _ := props["Connected"].Value().(bool); on && (name == "" || strings.HasPrefix(string(playing), string(path)+"/")) {
			name, _ = props["Alias"].Value().(string)
		}
	})

	return name
}

// playingTrack picks the track of the player that is playing, or else the first with one:
// two phones can be connected, one of them idle.
func playingTrack(objs managedObjects) Track {
	var first, playing Track

	eachOnAdapter(objs, playerIface, func(_ dbus.ObjectPath, props map[string]dbus.Variant) {
		t := trackOf(props)
		if props["Status"].Value() == "playing" && t != (Track{}) && playing == (Track{}) {
			playing = t
		}

		if first == (Track{}) {
			first = t
		}
	})

	if playing != (Track{}) {
		return playing
	}

	return first
}

func trackOf(player map[string]dbus.Variant) Track {
	fields, _ := player["Track"].Value().(map[string]dbus.Variant)
	title, _ := fields["Title"].Value().(string)
	artist, _ := fields["Artist"].Value().(string)
	album, _ := fields["Album"].Value().(string)

	return Track{title, artist, album}
}
