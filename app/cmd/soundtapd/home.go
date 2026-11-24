// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"path/filepath"

	"github.com/stein155/soundtap/app/internal/lpm"
)

// defaultHome is soundtapd's state directory on a speaker; -home moves it.
const defaultHome = "/mnt/nv/soundtap"

// Persistent paths outside what -home's own users join themselves. setHome sets them all
// from one home value, so a test or the simulator runs against a temp dir and nothing
// reaches for the speaker's /mnt/nv.
var (
	// hostnameFile is the DHCP hostname init reads before soundtapd runs.
	hostnameFile string
	// soundbarHome keeps the source the ST300's bar last played across a restart of the
	// module, and each soundbar choice's last value (internal/lpm).
	soundbarHome string
	// soundbar is the SoundTouch 300's link to its soundbar processor, keeping its state in
	// soundbarHome.
	soundbar *lpm.Link
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
)

func init() { setHome(defaultHome) }

// setHome points every path above at home and the volume that holds it
// (filepath.Dir(home): /mnt/nv on a speaker, where stock's files and the key maps live).
// main calls it with -home before anything reads them.
func setHome(home string) {
	nv := filepath.Dir(home)
	stock := filepath.Join(nv, "BoseApp-Persistence", "1")
	hostnameFile = filepath.Join(home, "hostname")
	soundbarHome = home
	soundbar = lpm.New(soundbarHome, version)
	snapserverKeepDir = filepath.Join(home, "snapserver")
	bcoImageMarker = filepath.Join(home, "bco-image")
	stockConfigPath = filepath.Join(stock, "SystemConfigurationDB.xml")
	stockPresetsPath = filepath.Join(stock, "Presets.xml")
	stockProfilesPath = filepath.Join(stock, "NetworkProfiles.xml")
	stockClockPath = filepath.Join(stock, "ClockDisplay.xml")
	stockLocaltimePath = filepath.Join(nv, "localtime")
	spotifyDir = filepath.Join(nv, "spotify")
	spotifySystemDir = filepath.Join(spotifyDir, "system")
	irUserMap = filepath.Join(nv, "ir.map")
	buttonsUserMap = filepath.Join(nv, "buttons.map")
}
