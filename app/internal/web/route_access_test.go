// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/web"
)

// Who may call each route (ADR 0055). There is no household token: a route is
// open to the home network, needs a press on the speaker, or answers only the speaker
// itself. A new route fails TestEveryRouteHasAnAccessClass until it is listed here, so
// adding one is a decision, not an accident.
const (
	open     = "open"     // anyone on the home network or the setup hotspot
	press    = "press"    // waits for a key press on the speaker (needsPress)
	loopback = "loopback" // refused unless it comes from 127.0.0.1
)

func routeAccess() map[string]string {
	return map[string]string{
		"GET /": open,

		// Reading state. Nothing here returns a password: /api/backup leaves them out, and
		// /api/wifi/saved lists names only.
		"GET /api/info": open, "GET /api/docs": open, "GET /api/openapi.json": open,
		"GET /api/devices": open, "GET /api/presets": open, "GET /api/recent": open,
		"GET /api/favorites": open, "GET /api/sleep": open, "GET /api/user-sources": open,
		"GET /api/now": open, "GET /api/position": open, "GET /api/events": open,
		"GET /api/household/events": open, "GET /api/volume": open, "GET /api/multiroom": open,
		"GET /api/soundbar/tone": open, "GET /api/soundbar/audio-mode": open, "GET /api/soundbar/cec-mode": open,
		"GET /api/soundbar/hdmi-input": open, "GET /api/soundbar/lip-sync": open, "GET /api/channel": open,
		"GET /api/multiroom/downstream": open, "GET /api/multiroom/leader": open,
		"GET /api/settings": open, "GET /api/mqtt/status": open, "GET /api/bluetooth": open,
		"GET /api/wifi/saved": open, "GET /api/version": open, "GET /api/releases": open,
		"GET /api/health": open, "GET /api/backup": open,
		// A crash report, only once the owner turned them on, redacted as the support bundle is
		// and without its logs; recording one as reported or dismissed only ends the offer
		// (ADR 0126).
		"GET /api/crash-report": open, "POST /api/crash-report": open,
		"GET /api/upnp/servers": open, "GET /api/upnp/browse": open,

		// Fetches on the owner's behalf. The logo and TuneIn proxies refuse non-public hosts.
		"GET /api/search": open, "GET /api/tunein/": open, "GET /api/logo": open,
		"POST /api/upnp/search": open,

		// The device proxy forwards to a speaker that announced itself; each of those
		// applies its own classes to the forwarded request.
		"GET /api/devices/{id}/{rest}": open, "POST /api/devices/{id}/{rest}": open,
		"PUT /api/devices/{id}/{rest}": open, "DELETE /api/devices/{id}/{rest}": open,

		// Playing and everyday settings: the Sonos model (ADR 0055).
		"PUT /api/presets/{slot}": open, "DELETE /api/presets/{slot}": open,
		"POST /api/presets/move": open, "DELETE /api/recent": open,
		"POST /api/favorites": open, "DELETE /api/favorites": open,
		"POST /api/play/{slot}": open, "POST /api/play": open, "POST /api/key": open,
		"POST /api/stop": open, "POST /api/power": open, "POST /api/sleep": open,
		"POST /api/user-source": open, "POST /api/volume": open, "POST /api/mute": open,
		"PUT /api/soundbar/tone": open, "PUT /api/soundbar/audio-mode": open, "PUT /api/soundbar/cec-mode": open,
		"PUT /api/soundbar/hdmi-input": open, "PUT /api/soundbar/lip-sync": open, "POST /api/soundbar/adaptiq": open,
		"PUT /api/channel": open, "PUT /api/settings": open,
		"PUT /api/multiroom/clients/{id}/volume":     open,
		"PUT /api/multiroom/clients/{id}/membership": open,
		"POST /api/multiroom/follow":                 open, "POST /api/multiroom/invite": open,
		"POST /api/multiroom/listen": open,
		"POST /api/bluetooth/clear":  press, "POST /api/bluetooth/forget": press, "POST /api/bluetooth/pair": open,
		"POST /api/wifi/scan": open, "POST /api/wifi/forget": open, "POST /api/wifi/fallback/dismiss": open,
		"POST /api/speaker/notify": open, "POST /api/upnp/play": open,
		"GET /api/upnp/modes": open, "POST /api/upnp/modes": open,
		"GET /api/spotify/status": open, "GET /api/spotify/search": open,
		"GET /api/spotify/artists/{id}": open, "GET /api/spotify/albums/{id}": open,
		"GET /api/spotify/playlists/{id}": open, "GET /api/spotify/me/playlists": open,
		"GET /api/spotify/me/tracks": open, "GET /api/spotify/me/recent": open,
		"GET /api/spotify/me/following": open, "GET /api/spotify/player": open,
		"GET /api/spotify/liked": open, "POST /api/spotify/liked": open,
		"GET /api/spotify/images/{id}": open, "POST /api/spotify/play": open,
		"POST /api/spotify/pause": open, "POST /api/spotify/resume": open,
		"POST /api/spotify/next": open, "POST /api/spotify/previous": open,
		"POST /api/spotify/seek": open, "POST /api/seek": open, "POST /api/spotify/disconnect": open,
		"POST /api/spotify/shuffle": open, "POST /api/spotify/repeat": open,

		// What runs on the speaker, wiping it, taking it off the network, reading its memory.
		"POST /api/wifi": press, "POST /api/setup": press, "POST /api/reboot": press,
		"POST /api/restore": press, "POST /api/factory-reset": press,
		// The Update button installs the latest signed release, a picked older one too.
		// A fetched release is signed: no press (ADR 0048). Every upload waits for one, signed
		// or not.
		"POST /api/update": open, "POST /api/core-update": open,
		"POST /api/firmware": press, "POST /api/soundtapd": press, "DELETE /api/soundtapd": press,
		"GET /api/debug": press,

		// Hooks for the player, the receivers and bt.sh on the speaker itself.
		"POST /api/source": loopback, "POST /api/nowplaying": loopback, "POST /api/position": loopback,
		"POST /api/bluetooth/connected": loopback, "GET /api/speaker/audio/{name}": loopback,
	}
}

func TestEveryRouteHasAnAccessClass(t *testing.T) {
	t.Parallel()

	reg := registeredRoutes(t)
	access := routeAccess()

	var problems []string

	for r := range reg {
		if access[r] == "" {
			problems = append(problems, "route has no access class in routeAccess: "+r)
		}
	}

	for r := range access {
		if !reg[r] {
			problems = append(problems, "routeAccess names a route that is not registered: "+r)
		}
	}

	slices.Sort(problems)

	for _, p := range problems {
		t.Error(p)
	}
}

// The classes are what the code does, not just a list: press routes wait for the key,
// the others do not, and loopback routes refuse a LAN caller.
func TestAccessClassesHold(t *testing.T) {
	t.Parallel()

	handler, _ := newServer(t)

	for route, class := range routeAccess() {
		method, path, _ := strings.Cut(route, " ")

		target := strings.NewReplacer("{id}", "x", "{rest}", "x", "{slot}", "1", "{name}", "x").Replace(path)
		if target == "/api/firmware" && class == press {
			target += "?install=1"
		}

		if got := web.NeedsPressForTest(method, target); got != (class == press) {
			t.Errorf("%s: class %s, but needsPress=%v", route, class, got)
		}

		if method == http.MethodGet && class == press && !web.NeedsPressForTest(http.MethodHead, target) {
			t.Errorf("HEAD %s skips the press its GET needs", path)
		}

		if class != loopback {
			continue
		}

		req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader("{}"))
		req.Host = "192.168.1.20"
		req.RemoteAddr = "192.168.1.30:40000"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from the LAN: status %d, want 403", route, rec.Code)
		}
	}
}
