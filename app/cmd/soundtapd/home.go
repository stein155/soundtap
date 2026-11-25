// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"path/filepath"

	"github.com/stein155/soundtap/app/internal/lpm"
)

// defaultHome is soundtapd's state directory on a speaker; -home moves it.
const defaultHome = "/mnt/nv/soundtap"

// homeLayout is where the persistent paths outside what -home's own users join themselves
// lie for one home: under home itself, and on the volume that holds it (filepath.Dir(home):
// /mnt/nv on a speaker, where stock's files and the key maps live).
type homeLayout struct {
	// hostnameFile is the DHCP hostname init reads before soundtapd runs.
	hostnameFile string
	// soundbarHome keeps the source the ST300's bar last played across a restart of the
	// module, and each soundbar choice's last value (internal/lpm).
	soundbarHome string
	// snapserverKeepDir is the snapserver state that survives a reboot.
	snapserverKeepDir string
	// bcoImageMarker records the BCO image last installed (bco.AutoInstall).
	bcoImageMarker string
	// stockConfigPath is stock's own system configuration. An install from Bose's software
	// leaves it on the persistent volume, and it holds the name the owner gave the speaker.
	stockConfigPath string
	// stockPresetsPath holds the presets stock left behind, next to its name.
	stockPresetsPath string
	// stockProfilesPath holds the networks stock saved, like stockConfigPath.
	stockProfilesPath string
	// stockClockPath holds the ST20 standby clock's settings stock kept; stockLocaltimePath
	// is its timezone as a link into /usr/share/zoneinfo.
	stockClockPath, stockLocaltimePath string
	// spotifyDir is librespot's cache and credentials; spotifySystemDir its system cache.
	spotifyDir, spotifySystemDir string
	// irUserMap and buttonsUserMap are an owner's own key maps, replacing the built-in ones.
	irUserMap, buttonsUserMap string
}

// layoutFor is the layout of home.
func layoutFor(home string) homeLayout {
	volume := filepath.Dir(home)
	stock := filepath.Join(volume, "BoseApp-Persistence", "1")
	spotify := filepath.Join(volume, "spotify")

	return homeLayout{
		hostnameFile:       filepath.Join(home, "hostname"),
		soundbarHome:       home,
		snapserverKeepDir:  filepath.Join(home, "snapserver"),
		bcoImageMarker:     filepath.Join(home, "bco-image"),
		stockConfigPath:    filepath.Join(stock, "SystemConfigurationDB.xml"),
		stockPresetsPath:   filepath.Join(stock, "Presets.xml"),
		stockProfilesPath:  filepath.Join(stock, "NetworkProfiles.xml"),
		stockClockPath:     filepath.Join(stock, "ClockDisplay.xml"),
		stockLocaltimePath: filepath.Join(volume, "localtime"),
		spotifyDir:         spotify,
		spotifySystemDir:   filepath.Join(spotify, "system"),
		irUserMap:          filepath.Join(volume, "ir.map"),
		buttonsUserMap:     filepath.Join(volume, "buttons.map"),
	}
}

// The persistent paths of the current home (homeLayout says what each is), and soundbar,
// the SoundTouch 300's link to its soundbar processor, keeping its state in soundbarHome.
// They start at defaultHome; setHome moves them all from one home value, so a test or the
// simulator runs against a temp dir and nothing reaches for the speaker's /mnt/nv.
//
//nolint:gochecknoglobals // a test seam: -home and tests point every path at a temp dir
var (
	hostnameFile       = layoutFor(defaultHome).hostnameFile
	soundbarHome       = layoutFor(defaultHome).soundbarHome
	soundbar           = lpm.New(soundbarHome, version)
	snapserverKeepDir  = layoutFor(defaultHome).snapserverKeepDir
	bcoImageMarker     = layoutFor(defaultHome).bcoImageMarker
	stockConfigPath    = layoutFor(defaultHome).stockConfigPath
	stockPresetsPath   = layoutFor(defaultHome).stockPresetsPath
	stockProfilesPath  = layoutFor(defaultHome).stockProfilesPath
	stockClockPath     = layoutFor(defaultHome).stockClockPath
	stockLocaltimePath = layoutFor(defaultHome).stockLocaltimePath
	spotifyDir         = layoutFor(defaultHome).spotifyDir
	spotifySystemDir   = layoutFor(defaultHome).spotifySystemDir
	irUserMap          = layoutFor(defaultHome).irUserMap
	buttonsUserMap     = layoutFor(defaultHome).buttonsUserMap
)

// setHome points every path above at home. main calls it with -home before anything
// reads them.
func setHome(home string) {
	layout := layoutFor(home)
	hostnameFile = layout.hostnameFile
	soundbarHome = layout.soundbarHome
	soundbar = lpm.New(soundbarHome, version)
	snapserverKeepDir = layout.snapserverKeepDir
	bcoImageMarker = layout.bcoImageMarker
	stockConfigPath = layout.stockConfigPath
	stockPresetsPath = layout.stockPresetsPath
	stockProfilesPath = layout.stockProfilesPath
	stockClockPath = layout.stockClockPath
	stockLocaltimePath = layout.stockLocaltimePath
	spotifyDir = layout.spotifyDir
	spotifySystemDir = layout.spotifySystemDir
	irUserMap = layout.irUserMap
	buttonsUserMap = layout.buttonsUserMap
}
