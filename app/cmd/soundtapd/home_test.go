// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// One -home moves every persistent path, so a test or the simulator never touches /mnt/nv.
func TestSetHomeMovesEveryPersistentPath(t *testing.T) {
	t.Cleanup(func() { setHome(defaultHome) })
	nv := t.TempDir()
	home := filepath.Join(nv, "soundtap")
	setHome(home)
	paths := map[string]string{
		"hostnameFile": hostnameFile, "soundbarHome": soundbarHome,
		"snapserverKeepDir": snapserverKeepDir, "bcoImageMarker": bcoImageMarker,
		"stockConfigPath": stockConfigPath, "stockPresetsPath": stockPresetsPath,
		"stockProfilesPath": stockProfilesPath, "spotifyDir": spotifyDir,
		"spotifySystemDir": spotifySystemDir, "irUserMap": irUserMap, "buttonsUserMap": buttonsUserMap,
	}
	for name, path := range paths {
		if !strings.HasPrefix(path, nv+string(filepath.Separator)) {
			t.Errorf("%s = %q, not under %s", name, path, nv)
		}
	}
	// The speaker's layout is unchanged: state under home, stock's files and the key maps
	// on the volume above it.
	setHome(defaultHome)
	for got, want := range map[string]string{
		hostnameFile:      "/mnt/nv/soundtap/hostname",
		soundbarHome:      "/mnt/nv/soundtap",
		snapserverKeepDir: "/mnt/nv/soundtap/snapserver",
		bcoImageMarker:    "/mnt/nv/soundtap/bco-image",
		stockConfigPath:   "/mnt/nv/BoseApp-Persistence/1/SystemConfigurationDB.xml",
		stockPresetsPath:  "/mnt/nv/BoseApp-Persistence/1/Presets.xml",
		stockProfilesPath: "/mnt/nv/BoseApp-Persistence/1/NetworkProfiles.xml",
		spotifySystemDir:  "/mnt/nv/spotify/system",
		irUserMap:         "/mnt/nv/ir.map",
		buttonsUserMap:    "/mnt/nv/buttons.map",
	} {
		if got != want {
			t.Errorf("default path %q, want %q", got, want)
		}
	}
}
