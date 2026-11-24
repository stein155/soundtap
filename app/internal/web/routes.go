// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import "net/http"

// routeMux is the ServeMux the API registers on. It keeps each pattern, so a test can
// hold the routes the server really serves against the API reference.
type routeMux struct {
	*http.ServeMux
	patterns []string
}

func (m *routeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.patterns = append(m.patterns, pattern)
	m.ServeMux.HandleFunc(pattern, handler)
}

// Handler returns the HTTP mux.
func (s *Server) Handler() http.Handler { //nolint:funlen // route table, moved unchanged
	mux := &routeMux{ServeMux: http.NewServeMux()}
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/docs", s.docs)
	mux.HandleFunc("GET /api/openapi.json", s.openapi)
	mux.HandleFunc("GET /api/devices", s.devicesHandler)
	// Control another speaker through this one; see deviceproxy.go for why this is a
	// proxy rather than a CORS header.
	// Per method rather than one method-less pattern: a pattern with no method
	// conflicts with the "GET /" that serves the UI.
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		mux.HandleFunc(method+" /api/devices/{id}/{rest...}", s.deviceProxy)
	}
	mux.HandleFunc("GET /api/tunein/", s.tuneinProxy)
	mux.HandleFunc("GET /api/logo", s.logoProxy)
	mux.HandleFunc("GET /api/presets", s.presets)
	mux.HandleFunc("PUT /api/presets/{slot}", s.setPreset)
	mux.HandleFunc("GET /api/recent", s.getRecent)
	mux.HandleFunc("DELETE /api/recent", s.delRecent)
	mux.HandleFunc("GET /api/favorites", s.getFavorites)
	mux.HandleFunc("POST /api/favorites", s.addFavorite)
	mux.HandleFunc("DELETE /api/favorites", s.delFavorite)
	mux.HandleFunc("DELETE /api/presets/{slot}", s.delPreset)
	mux.HandleFunc("POST /api/presets/move", s.movePreset)
	mux.HandleFunc("POST /api/play/{slot}", s.playPreset)
	mux.HandleFunc("POST /api/play", s.playStation)
	mux.HandleFunc("POST /api/key", s.postKey) // transport keys a grouped follower forwards
	mux.HandleFunc("POST /api/stop", s.stop)
	mux.HandleFunc("POST /api/power", s.power)
	mux.HandleFunc("GET /api/sleep", s.getSleep)
	mux.HandleFunc("POST /api/sleep", s.postSleep)
	mux.HandleFunc("GET /api/user-sources", s.getUserSources)
	mux.HandleFunc("POST /api/user-source", s.setUserSource)
	mux.HandleFunc("POST /api/source", s.setSource)         // AirPlay/BT signal source start/stop
	mux.HandleFunc("POST /api/nowplaying", s.setNowPlaying) // Spotify (librespot) pushes track metadata
	mux.HandleFunc("GET /api/now", s.now)
	mux.HandleFunc("POST /api/position", s.setPosition) // Spotify (librespot) says where it is
	mux.HandleFunc("GET /api/position", s.position)
	mux.HandleFunc("POST /api/seek", s.seek)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/household/events", s.householdEvents)
	mux.HandleFunc("GET /api/volume", s.getVolume)
	mux.HandleFunc("POST /api/volume", s.setVolume)
	mux.HandleFunc("GET /api/multiroom", s.getMultiroom)
	mux.HandleFunc("GET /api/soundbar/tone", s.getTone)
	mux.HandleFunc("PUT /api/soundbar/tone", s.putTone)
	mux.HandleFunc("POST /api/soundbar/adaptiq", s.startADAPTiQ)
	mux.HandleFunc("GET /api/soundbar/audio-mode", s.getChoice("audio-mode"))
	mux.HandleFunc("PUT /api/soundbar/audio-mode", s.putChoice("audio-mode"))
	mux.HandleFunc("GET /api/soundbar/cec-mode", s.getChoice("cec-mode"))
	mux.HandleFunc("PUT /api/soundbar/cec-mode", s.putChoice("cec-mode"))
	mux.HandleFunc("GET /api/soundbar/hdmi-input", s.getChoice("hdmi-input"))
	mux.HandleFunc("PUT /api/soundbar/hdmi-input", s.putChoice("hdmi-input"))
	mux.HandleFunc("GET /api/soundbar/lip-sync", s.getLipSync)
	mux.HandleFunc("PUT /api/soundbar/lip-sync", s.putLipSync)
	mux.HandleFunc("GET /api/channel", s.getChannel)
	mux.HandleFunc("PUT /api/channel", s.putChannel)
	mux.HandleFunc("PUT /api/multiroom/clients/{id}/volume", s.putMultiroomVolume)
	mux.HandleFunc("PUT /api/multiroom/clients/{id}/membership", s.putMultiroomMembership)
	mux.HandleFunc("POST /api/multiroom/follow", s.postMultiroomFollow)
	mux.HandleFunc("POST /api/multiroom/invite", s.postMultiroomInvite)
	mux.HandleFunc("POST /api/multiroom/listen", s.postMultiroomListen)
	mux.HandleFunc("GET /api/multiroom/downstream", s.getMultiroomDownstream)
	mux.HandleFunc("GET /api/multiroom/leader", s.getMultiroomLeader)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("GET /api/mqtt/status", s.mqttStatus)
	mux.HandleFunc("GET /api/bluetooth", s.getBluetooth)
	mux.HandleFunc("POST /api/bluetooth/clear", s.clearBluetooth)
	mux.HandleFunc("POST /api/bluetooth/pair", s.pairBluetooth)
	mux.HandleFunc("POST /api/bluetooth/connected", s.bluetoothConnected)
	mux.HandleFunc("POST /api/wifi/scan", s.wifiScan)
	mux.HandleFunc("POST /api/wifi", s.setWifi)
	mux.HandleFunc("GET /api/wifi/saved", s.savedWifi)
	mux.HandleFunc("POST /api/wifi/forget", s.forgetSavedWifi)
	mux.HandleFunc("POST /api/wifi/fallback/dismiss", s.dismissWifiFallback)
	mux.HandleFunc("POST /api/setup", s.enterSetup)
	mux.HandleFunc("GET /api/version", s.versionInfo)
	mux.HandleFunc("GET /api/releases", s.releases)
	mux.HandleFunc("GET /api/debug", s.debugBundle)
	mux.HandleFunc("GET /api/health", s.healthInfo)
	mux.HandleFunc("GET /api/crash-report", s.getCrashReport)
	mux.HandleFunc("POST /api/crash-report", s.postCrashReport)
	mux.HandleFunc("GET /api/backup", s.exportBackup)
	mux.HandleFunc("POST "+restorePath, s.importBackup)
	mux.HandleFunc("POST /api/factory-reset", s.factoryReset)
	mux.HandleFunc("POST /api/update", s.updateApp)
	mux.HandleFunc("POST "+coreUpdatePath, s.updateCore)
	s.fw.Register(mux)

	// Audio notifications play a clip over the current source via the firmware's
	// /speaker endpoint. It is open to the LAN (smart-home hubs post doorbells to it), so
	// a URL must be http(s); notify accepts either a URL (JSON) or an uploaded clip (binary body); audio
	// serves an uploaded clip back to the co-located firmware over loopback.
	mux.HandleFunc("POST /api/speaker/notify", s.speakerNotify)
	mux.HandleFunc("GET /api/speaker/audio/{name}", s.serveAudio)
	s.spotifyRoutes(mux)
	mux.HandleFunc("GET /api/upnp/servers", s.upnpServers)
	mux.HandleFunc("POST /api/upnp/search", s.upnpSearch)
	mux.HandleFunc("GET /api/upnp/browse", s.upnpBrowse)
	mux.HandleFunc("POST /api/upnp/play", s.upnpPlay)
	mux.HandleFunc("GET /api/upnp/modes", s.upnpModes)
	mux.HandleFunc("POST /api/upnp/modes", s.upnpSetModes)
	// Everything else is the embedded single-page UI. More specific /api/...
	// patterns above win; this serves index.html, assets and icons.
	mux.HandleFunc("GET /", s.serveUI)
	s.routes = mux.patterns
	guarded := s.guard(s.replays.replay(s.onDevice(jsonRouterErrors(mux))))
	s.local = guarded
	captive := http.NewServeMux()
	for _, path := range []string{"/hotspot-detect.html", "/generate_204", "/gen_204", "/connecttest.txt", "/ncsi.txt", "/canonical.html"} {
		captive.HandleFunc("GET "+path, captivePortal)
	}
	captive.Handle("/", guarded)
	return captive
}
