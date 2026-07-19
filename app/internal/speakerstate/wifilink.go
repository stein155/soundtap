// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/wpactrl"
)

// Live Wi-Fi association state.
//
// NetworkInfo used to report s.wifiSSID, which is only ever written when someone joins
// a network through the UI. A speaker that associated at boot from the baked-in
// /etc/soundtap-wifi.conf therefore reported the seeded placeholder forever, and a failed
// join through the UI still reported success — both are the UI stating a network the
// speaker is not on. Ask wpa_supplicant over its control socket what wlan0 is actually
// associated with instead, and keep the stored value only as the fallback for hosts
// without a supplicant (tests, sim).

// wifiLinkTTL bounds how often we ask: /api/status is polled every 2.5 s per open
// browser, and an association does not change faster than this.
const (
	wifiLinkTTL  = 3 * time.Second
	stationIface = "wlan0"
)

type wifiLink struct {
	ssid string // "" when not associated
	wifiSignal
}

// wifiSignal is what SIGNAL_POLL says about the link; zero values are unknown.
type wifiSignal struct {
	signal string // UI token ("excellent".."poor")
	dbm    int    // RSSI
	mbps   int    // link speed
	mhz    int    // channel frequency
}

// liveWifiLink returns the current association, cached for wifiLinkTTL. ok is false when
// the link could not be read at all (no supplicant, no control socket) — distinct from "read
// fine, not associated", which is ok with an empty ssid.
func (s *Speaker) liveWifiLink(ctx context.Context) (wifiLink, bool) {
	s.lock()
	if !s.wifiLinkAt.IsZero() && time.Since(s.wifiLinkAt) < wifiLinkTTL {
		l, ok := s.wifiLinkCache, s.wifiLinkOK
		s.unlock()
		return l, ok
	}
	s.unlock()

	l, ok := readWifiLink(ctx)

	s.lock()
	s.wifiLinkCache, s.wifiLinkOK, s.wifiLinkAt = l, ok, time.Now()
	s.unlock()
	return l, ok
}

func readWifiLink(ctx context.Context) (wifiLink, bool) {
	if os.Getenv("SOUNDTAP_WIFI_MODE") == wifiModeBCO {
		ssid, err := bco.CurrentSSID(ctx, os.Getenv("SOUNDTAP_BCO_TTY"))
		return wifiLink{ssid: ssid}, err == nil
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, err := wpactrl.Dial(wpaCtrlPath)
	if err != nil {
		return wifiLink{}, false
	}
	defer c.Close()
	out, err := c.Request(cctx, "STATUS")
	if err != nil {
		return wifiLink{}, false
	}
	ssid, associated := parseWpaStatus(out)
	if !associated {
		return wifiLink{}, true
	}
	return wifiLink{ssid: ssid, wifiSignal: readWifiSignal(cctx, c)}, true
}

// wpaCtrlPath is wpa_supplicant's control socket for the station; a test points it at a
// fake.
var wpaCtrlPath = filepath.Join(wpactrl.Dir, stationIface)

// parseWpaStatus pulls the associated SSID out of a station's STATUS.
// Anything short of wpa_state=COMPLETED is reported as not associated.
func parseWpaStatus(out string) (string, bool) {
	var ssid, mode, state string
	for _, line := range strings.Split(out, "\n") {
		k, v, found := strings.Cut(strings.TrimLeft(strings.TrimRight(line, "\r"), " \t"), "=") // keep SSID spaces
		if !found {
			continue
		}
		switch k {
		case "ssid":
			ssid = v
		case "mode":
			mode = v
		case "wpa_state":
			state = v
		}
	}
	if mode != "station" || state != "COMPLETED" || ssid == "" {
		return "", false
	}
	return ssid, true
}

// readWifiSignal reads RSSI for the associated BSS. Best-effort: an unknown signal is
// reported as unknown rather than guessed, and never blocks the SSID from being shown.
func readWifiSignal(ctx context.Context, c *wpactrl.Conn) wifiSignal {
	out, err := c.Request(ctx, "SIGNAL_POLL")
	if err != nil {
		return wifiSignal{}
	}
	return parseWifiSignal(out)
}

// parseWifiSignal reads signal_poll. An RSSI that is not a plausible dBm reading leaves the
// whole signal unknown; speed and frequency only count when positive.
func parseWifiSignal(out string) wifiSignal {
	var sig wifiSignal
	for _, line := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			continue
		}
		switch k {
		case "RSSI":
			if sig.signal = dbmToken(n); sig.signal != "" {
				sig.dbm = n
			}
		case "LINKSPEED":
			sig.mbps = max(n, 0)
		case "FREQUENCY":
			sig.mhz = max(n, 0)
		}
	}
	if sig.signal == "" {
		return wifiSignal{}
	}
	return sig
}
