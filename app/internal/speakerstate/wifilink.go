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
	wifiSignal

	ssid string // "" when not associated
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

	link, found := readWifiLink(ctx)

	s.lock()
	s.wifiLinkCache, s.wifiLinkOK, s.wifiLinkAt = link, found, time.Now()
	s.unlock()

	return link, found
}

func readWifiLink(ctx context.Context) (wifiLink, bool) {
	if os.Getenv("SOUNDTAP_WIFI_MODE") == wifiModeBCO {
		ssid, err := bco.CurrentSSID(ctx, os.Getenv("SOUNDTAP_BCO_TTY"))

		return wifiLink{ssid: ssid, wifiSignal: wifiSignal{dbm: 0, mbps: 0, mhz: 0, signal: ""}}, err == nil
	}

	cctx, cancel := context.WithTimeout(ctx, wpaTimeout)
	defer cancel()

	client, err := wpactrl.Dial(wpaCtrlPath)
	if err != nil {
		return wifiLink{ssid: "", wifiSignal: wifiSignal{dbm: 0, mbps: 0, mhz: 0, signal: ""}}, false
	}
	defer client.Close()

	out, err := client.Request(cctx, "STATUS")
	if err != nil {
		return wifiLink{ssid: "", wifiSignal: wifiSignal{dbm: 0, mbps: 0, mhz: 0, signal: ""}}, false
	}

	ssid, associated := parseWpaStatus(out)
	if !associated {
		return wifiLink{ssid: "", wifiSignal: wifiSignal{dbm: 0, mbps: 0, mhz: 0, signal: ""}}, true
	}

	return wifiLink{ssid: ssid, wifiSignal: readWifiSignal(cctx, client)}, true
}

// wpaCtrlPath is wpa_supplicant's control socket for the station; a test points it at a
// fake.
//
//nolint:gochecknoglobals // see above.
var wpaCtrlPath = filepath.Join(wpactrl.Dir, stationIface)

// parseWpaStatus pulls the associated SSID out of a station's STATUS.
// Anything short of wpa_state=COMPLETED is reported as not associated.
func parseWpaStatus(out string) (string, bool) {
	var ssid, mode, state string

	for line := range strings.SplitSeq(out, "\n") {
		k, value, found := strings.Cut(strings.TrimLeft(strings.TrimRight(line, "\r"), " \t"), "=") // keep SSID spaces
		if !found {
			continue
		}

		switch k {
		case "ssid":
			ssid = value
		case "mode":
			mode = value
		case "wpa_state":
			state = value
		default: // the other STATUS lines say nothing this reads
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
		return wifiSignal{signal: "", dbm: 0, mbps: 0, mhz: 0}
	}

	return parseWifiSignal(out)
}

// parseWifiSignal reads signal_poll. An RSSI that is not a plausible dBm reading leaves the
// whole signal unknown; speed and frequency only count when positive.
func parseWifiSignal(out string) wifiSignal {
	var sig wifiSignal

	for line := range strings.SplitSeq(out, "\n") {
		key, v, _ := strings.Cut(strings.TrimSpace(line), "=")

		count, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			continue
		}

		switch key {
		case "RSSI":
			if sig.signal = dbmToken(count); sig.signal != "" {
				sig.dbm = count
			}
		case "LINKSPEED":
			sig.mbps = max(count, 0)
		case "FREQUENCY":
			sig.mhz = max(count, 0)
		default: // the other SIGNAL_POLL lines say nothing this reads
		}
	}

	if sig.signal == "" {
		return wifiSignal{signal: "", dbm: 0, mbps: 0, mhz: 0}
	}

	return sig
}
