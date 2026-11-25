// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/web"
)

// wifiKnownMode keeps the remembered networks' keys to soundtapd.
const wifiKnownMode = 0o600

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

// forgetWifiProfile drops one remembered network; an unknown ID is already forgotten.
// The one in wifi.conf is in use and stays.
// unlocked read-modify-write; a join finishing in the same instant can undo it.
func forgetWifiProfile(conf, ssidB64 string) error {
	current, err := os.ReadFile(conf)
	if err == nil && slices.Contains(strings.Split(string(current), "\n"), "SSID_B64="+ssidB64) {
		return web.ErrWifiProfileCurrent
	}

	all := readKnownWifi(conf)

	keep := all[:0:0]
	for _, l := range all {
		if strings.Fields(l)[0] != ssidB64 {
			keep = append(keep, l)
		}
	}

	if len(keep) == len(all) {
		return nil
	}

	err = atomicjson.WriteFile(conf+".known", []byte(strings.Join(keep, "\n")+"\n"), wifiKnownMode)
	if err != nil {
		return fmt.Errorf("forget Wi-Fi network: %w", err)
	}

	return nil
}
