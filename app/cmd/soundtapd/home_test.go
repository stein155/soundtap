// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// One -home moves every persistent path, so a test or the simulator never touches /mnt/nv.
//
//nolint:paralleltest // swaps package variables other tests read: bcoImageMarker, buttonsUserMap, hostnameFile and more
func TestSetHomeMovesEveryPersistentPath(t *testing.T) {
	t.Cleanup(func() { setHome(defaultHome) })

	volume := t.TempDir()
	home := filepath.Join(volume, "soundtap")
	setHome(home)

	paths := map[string]string{
		"hostnameFile": hostnameFile, "soundbarHome": soundbarHome,
		"snapserverKeepDir": snapserverKeepDir, "bcoImageMarker": bcoImageMarker,
		"stockConfigPath": stockConfigPath, "stockPresetsPath": stockPresetsPath,
		"stockProfilesPath": stockProfilesPath, "stockClockPath": stockClockPath,
		"stockLocaltimePath": stockLocaltimePath, "spotifyDir": spotifyDir,
		"spotifySystemDir": spotifySystemDir, "irUserMap": irUserMap, "buttonsUserMap": buttonsUserMap,
	}
	for name, path := range paths {
		if !strings.HasPrefix(path, volume+string(filepath.Separator)) {
			t.Errorf("%s = %q, not under %s", name, path, volume)
		}
	}
	// The speaker's layout is unchanged: state under home, stock's files and the key maps
	// on the volume above it.
	setHome(defaultHome)

	for got, want := range map[string]string{
		hostnameFile:       "/mnt/nv/soundtap/hostname",
		soundbarHome:       "/mnt/nv/soundtap",
		snapserverKeepDir:  "/mnt/nv/soundtap/snapserver",
		bcoImageMarker:     "/mnt/nv/soundtap/bco-image",
		stockConfigPath:    "/mnt/nv/BoseApp-Persistence/1/SystemConfigurationDB.xml",
		stockPresetsPath:   "/mnt/nv/BoseApp-Persistence/1/Presets.xml",
		stockProfilesPath:  "/mnt/nv/BoseApp-Persistence/1/NetworkProfiles.xml",
		stockClockPath:     "/mnt/nv/BoseApp-Persistence/1/ClockDisplay.xml",
		stockLocaltimePath: "/mnt/nv/localtime",
		spotifySystemDir:   "/mnt/nv/spotify/system",
		irUserMap:          "/mnt/nv/ir.map",
		buttonsUserMap:     "/mnt/nv/buttons.map",
	} {
		if got != want {
			t.Errorf("default path %q, want %q", got, want)
		}
	}
}
