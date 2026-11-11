// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha1" //nolint:gosec // WPA's PSK is PBKDF2-SHA1 by the standard (IEEE 802.11i)
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// The saved networks on /mnt/nv (docs/contributors/nv-state.md; ADR 0120 keeps these formats):
//
//	wifi.conf        SSID_B64=<base64>, PSK_B64=<base64> (empty: open), optional PSK_HEX=<64 hex>
//	wifi.conf.good   a copy of the last wifi.conf that gave the speaker an address
//	wifi.conf.known  up to KnownMax lines, newest first: <SSID_B64> <PSK_B64|-> <PSK_HEX|->
//
// wifi.conf is the network tried first; the known ones follow at lower priority, so
// wpa_supplicant joins whichever is in range.

// KnownMax is how many joined networks wifi.conf.known keeps.
const KnownMax = 5

// Record is one saved network as stored: base64 name and passphrase, and the raw key in
// hex when there is one. Empty fields are absent.
type Record struct {
	SSIDB64 string
	PSKB64  string
	HexPSK  string
}

// Line is r as a wifi.conf.known line.
func (r Record) Line() string {
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	return r.SSIDB64 + " " + dash(r.PSKB64) + " " + dash(r.HexPSK)
}

// ConfBytes is r as wifi.conf.
func ConfBytes(r Record) []byte {
	b := "SSID_B64=" + r.SSIDB64 + "\nPSK_B64=" + r.PSKB64 + "\n"
	if r.HexPSK != "" {
		b += "PSK_HEX=" + r.HexPSK + "\n"
	}
	return []byte(b)
}

// NewRecord stores a network the way the setup page hands it over: a 64-digit hex key is
// the key itself, anything else a passphrase.
func NewRecord(ssid, psk string) Record {
	r := Record{SSIDB64: base64.StdEncoding.EncodeToString([]byte(ssid))}
	if IsHexPSK(psk) {
		r.HexPSK = psk
	} else if psk != "" {
		r.PSKB64 = base64.StdEncoding.EncodeToString([]byte(psk))
	}
	return r
}

// WriteConf replaces path with r, atomically and readable by root only.
func WriteConf(path string, r Record) error {
	return atomicjson.WriteFile(path, ConfBytes(r), 0o600)
}

// ParseConf reads wifi.conf. The first line of each key counts.
func ParseConf(b []byte) Record {
	var r Record
	seen := map[string]bool{}
	for line := range strings.Lines(string(b)) {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "=")
		if !ok || seen[k] {
			continue
		}
		seen[k] = true
		switch k {
		case "SSID_B64":
			r.SSIDB64 = v
		case "PSK_B64":
			r.PSKB64 = v
		case "PSK_HEX":
			r.HexPSK = v
		}
	}
	return r
}

// ParseKnown reads wifi.conf.known; a missing field reads as absent.
func ParseKnown(b []byte) []Record {
	var out []Record
	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		for len(f) < 3 {
			f = append(f, "-")
		}
		un := func(s string) string {
			if s == "-" {
				return ""
			}
			return s
		}
		out = append(out, Record{SSIDB64: f[0], PSKB64: un(f[1]), HexPSK: un(f[2])})
	}
	return out
}

// remember puts r in front of the known list in path, drops an older line for the same
// network and keeps the newest KnownMax.
func remember(path string, r Record) error {
	b, _ := os.ReadFile(path) // no list yet: r starts one
	lines := []string{r.Line()}
	for line := range strings.Lines(string(b)) {
		line = strings.TrimRight(line, "\n")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, r.SSIDB64+" ") {
			continue
		}
		lines = append(lines, line)
	}
	lines = lines[:min(len(lines), KnownMax)]
	return atomicjson.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// sameFile reports whether a and b both exist with the same bytes, as cmp -s.
func sameFile(a, b string) bool {
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// copyFile replaces dst with src's bytes, atomically.
func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return atomicjson.WriteFile(dst, b, 0o600)
}

// IsHexPSK reports a raw WPA key: 64 hex digits.
func IsHexPSK(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// network is one block handed to wpa_supplicant: the name and the 32-byte key in hex, or
// no key for an open network.
type network struct {
	rec    Record
	ssid   string
	keyHex string
}

var (
	errBadConf = errors.New("invalid wifi.conf")
	errBadHex  = errors.New("invalid WPA PSK")
	errBadPass = errors.New("invalid WPA credentials")
)

// decode turns a stored network into what wpa_supplicant gets. A passphrase becomes its
// key here (PBKDF2-SHA1 over the name, as wpa_passphrase), so no passphrase reaches the
// supplicant, a file or a log.
func decode(r Record) (network, error) {
	ssid, err := base64.StdEncoding.DecodeString(r.SSIDB64)
	if err != nil || len(ssid) == 0 {
		return network{}, errBadConf
	}
	pass, err := base64.StdEncoding.DecodeString(r.PSKB64)
	if err != nil {
		return network{}, errBadConf
	}
	n := network{rec: r, ssid: string(ssid)}
	switch {
	case r.HexPSK != "":
		if !IsHexPSK(r.HexPSK) {
			return network{}, errBadHex
		}
		n.keyHex = strings.ToLower(r.HexPSK)
	case len(pass) > 0:
		if len(pass) < 8 || len(pass) > 63 {
			return network{}, errBadPass
		}
		key, err := pbkdf2.Key(sha1.New, string(pass), ssid, 4096, 32)
		if err != nil {
			return network{}, fmt.Errorf("%w: %w", errBadPass, err)
		}
		n.keyHex = hex.EncodeToString(key)
	}
	return n, nil
}
