// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"encoding/base64"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/web"
)

// The Wi-Fi manager keeps the networks that joined in wifi.conf.known, newest first, one per line:
// "<SSID_B64> <PSK_B64|-> <PSK_HEX|->". Only the first field leaves this file.

func readKnownWifi(conf string) []string {
	b, err := os.ReadFile(conf + ".known")
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
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
		id := strings.Fields(l)[0]
		raw, err := base64.StdEncoding.DecodeString(id)
		if err != nil || len(raw) == 0 {
			continue
		}
		name := string(raw)
		if !utf8.Valid(raw) {
			name = "?"
		}
		nets = append(nets, web.WifiProfile{ID: id, SSID: name})
	}
	return nets, nil
}

// forgetWifiProfile drops one remembered network; an unknown ID is already forgotten.
// The one in wifi.conf is in use and stays.
// unlocked read-modify-write; a join finishing in the same instant can undo it.
func forgetWifiProfile(conf, id string) error {
	if b, err := os.ReadFile(conf); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l == "SSID_B64="+id {
				return web.ErrWifiProfileCurrent
			}
		}
	}
	all := readKnownWifi(conf)
	keep := all[:0:0]
	for _, l := range all {
		if strings.Fields(l)[0] != id {
			keep = append(keep, l)
		}
	}
	if len(keep) == len(all) {
		return nil
	}
	return atomicjson.WriteFile(conf+".known", []byte(strings.Join(keep, "\n")+"\n"), 0o600)
}
