// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/web"
	"github.com/stein155/soundtap/app/internal/wifi"
	"github.com/stein155/soundtap/app/internal/wpactrl"
)

// wifiKnownMode keeps the remembered networks' keys to soundtapd.
const wifiKnownMode = 0o600

// wpaStatusTimeout bounds the question to wpa_supplicant which network is joined.
const wpaStatusTimeout = 3 * time.Second

// The Wi-Fi manager keeps the networks that joined in wifi.conf.known, newest first, one per line:
// "<SSID_B64> <PSK_B64|-> <PSK_HEX|->". Only the first field leaves this file.

func readKnownWifi(conf string) []string {
	known, err := os.ReadFile(conf + ".known")
	if err != nil {
		return nil
	}

	var lines []string

	for l := range strings.SplitSeq(string(known), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}

	return lines
}

// savedWifiProfiles lists the remembered networks by name. A name that is not text
// shows as a placeholder; it can still be forgotten by its ID.
func savedWifiProfiles(conf string) ([]web.WifiProfile, error) {
	nets := []web.WifiProfile{}

	for _, l := range readKnownWifi(conf) {
		ssidB64 := strings.Fields(l)[0]

		raw, err := base64.StdEncoding.DecodeString(ssidB64)
		if err != nil || len(raw) == 0 {
			continue
		}

		name := string(raw)
		if !utf8.Valid(raw) {
			name = "?"
		}

		nets = append(nets, web.WifiProfile{ID: ssidB64, SSID: name})
	}

	return nets, nil
}

// forgetWifiProfile drops one remembered network; an unknown ID is already forgotten. Only the
// network the speaker is joined to now is in use and stays (joined reports it, false when that
// cannot be told: then wifi.conf's network counts as in use, as nothing better is known). A
// network the speaker fell back from, still named in wifi.conf, can be forgotten: wifi.conf then
// names the network joined now, or else the first one left, and wifi.conf.good no longer names
// the forgotten one, so no fallback brings it back (ADR 0063).
// unlocked read-modify-write; a join finishing in the same instant can undo it.
func forgetWifiProfile(conf, ssidB64 string, joined func() (string, bool)) error {
	current, _ := os.ReadFile(conf) // none: nothing in wifi.conf to keep
	saved := wifi.ParseConf(current)

	now, told := joined()
	if told && now != "" && base64.StdEncoding.EncodeToString([]byte(now)) == ssidB64 ||
		!told && saved.SSIDB64 == ssidB64 {
		return web.ErrWifiProfileCurrent
	}

	all := readKnownWifi(conf)

	keep := all[:0:0]
	for _, l := range all {
		if strings.Fields(l)[0] != ssidB64 {
			keep = append(keep, l)
		}
	}

	if len(keep) != len(all) {
		err := atomicjson.WriteFile(conf+".known", []byte(strings.Join(keep, "\n")+"\n"), wifiKnownMode)
		if err != nil {
			return fmt.Errorf("forget Wi-Fi network: %w", err)
		}
	}

	if saved.SSIDB64 != ssidB64 {
		return nil
	}

	return repointWifiConf(conf, ssidB64, keep, now)
}

// repointWifiConf makes wifi.conf name a network that is left after the one it named was
// forgotten: the one joined now when it is remembered, else the newest remembered one, else none
// (the setup AP at the next join). wifi.conf.good follows when it named the forgotten network.
func repointWifiConf(conf, forgotten string, known []string, joinedSSID string) error {
	recs := wifi.ParseKnown([]byte(strings.Join(known, "\n")))

	var next wifi.Record

	for _, rec := range recs {
		if joinedSSID != "" && rec.SSIDB64 == base64.StdEncoding.EncodeToString([]byte(joinedSSID)) {
			next = rec

			break
		}
	}

	if next.SSIDB64 == "" && len(recs) > 0 {
		next = recs[0]
	}

	err := wifi.WriteConf(conf, next)
	if err != nil {
		return fmt.Errorf("forget Wi-Fi network: %w", err)
	}

	good, _ := os.ReadFile(conf + ".good")
	if wifi.ParseConf(good).SSIDB64 != forgotten {
		return nil
	}

	if next.SSIDB64 == "" {
		_ = os.Remove(conf + ".good")

		return nil
	}

	err = wifi.WriteConf(conf+".good", next)
	if err != nil {
		return fmt.Errorf("forget Wi-Fi network: %w", err)
	}

	return nil
}

// joinedWifiSSID is the network wlan0 is joined to now, as wpa_supplicant's STATUS says: "" when
// it is joined to none, false when there is no wpa_supplicant to ask.
func joinedWifiSSID() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), wpaStatusTimeout)
	defer cancel()

	conn, err := wpactrl.Dial(filepath.Join(wpactrl.Dir, "wlan0"))
	if err != nil {
		return "", false
	}
	defer conn.Close()

	status, err := conn.Status(ctx)
	if err != nil {
		return "", false
	}

	if status["wpa_state"] != "COMPLETED" || status["mode"] != "station" {
		return "", true
	}

	return status["ssid"], true
}
