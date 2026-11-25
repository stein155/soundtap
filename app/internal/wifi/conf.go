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

// The WPA key's sizes: a raw key in hex, a passphrase's bounds, and the PBKDF2 that turns a
// passphrase into the key (IEEE 802.11i).
const (
	hexPSKLen        = 64
	minPassphrase    = 8
	maxPassphrase    = 63
	pbkdf2Iterations = 4096
	pskBytes         = 32
)

// Record is one saved network as stored: base64 name and passphrase, and the raw key in
// hex when there is one. Empty fields are absent.
type Record struct {
	SSIDB64 string
	PSKB64  string
	HexPSK  string
}

// NewRecord stores a network the way the setup page hands it over: a 64-digit hex key is
// the key itself, anything else a passphrase.
func NewRecord(ssid, psk string) Record {
	rec := Record{SSIDB64: base64.StdEncoding.EncodeToString([]byte(ssid)), PSKB64: "", HexPSK: ""}
	if IsHexPSK(psk) {
		rec.HexPSK = psk
	} else if psk != "" {
		rec.PSKB64 = base64.StdEncoding.EncodeToString([]byte(psk))
	}

	return rec
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

// WriteConf replaces path with r, atomically and readable by root only.
func WriteConf(path string, r Record) error {
	err := atomicjson.WriteFile(path, ConfBytes(r), privateFileMode)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}

// ParseConf reads wifi.conf. The first line of each key counts.
func ParseConf(b []byte) Record {
	var rec Record

	seen := map[string]bool{}

	for line := range strings.Lines(string(b)) {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "=")
		if !ok || seen[key] {
			continue
		}

		seen[key] = true
		switch key {
		case "SSID_B64":
			rec.SSIDB64 = value
		case "PSK_B64":
			rec.PSKB64 = value
		case "PSK_HEX":
			rec.HexPSK = value
		default:
			// Not a key of wifi.conf.
		}
	}

	return rec
}

// ParseKnown reads wifi.conf.known; a missing field reads as absent.
func ParseKnown(b []byte) []Record {
	var out []Record

	for line := range strings.Lines(string(b)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		for len(fields) < 3 {
			fields = append(fields, "-")
		}

		orNone := func(s string) string {
			if s == "-" {
				return ""
			}

			return s
		}
		out = append(out, Record{SSIDB64: fields[0], PSKB64: orNone(fields[1]), HexPSK: orNone(fields[2])})
	}

	return out
}

// remember puts r in front of the known list in path, drops an older line for the same
// network and keeps the newest KnownMax.
func remember(path string, rec Record) error {
	b, _ := os.ReadFile(path) // no list yet: rec starts one
	lines := []string{rec.Line()}

	for line := range strings.Lines(string(b)) {
		line = strings.TrimRight(line, "\n")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, rec.SSIDB64+" ") {
			continue
		}

		lines = append(lines, line)
	}

	lines = lines[:min(len(lines), KnownMax)]

	err := atomicjson.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), privateFileMode)
	if err != nil {
		return fmt.Errorf("known networks: %w", err)
	}

	return nil
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
		return fmt.Errorf("copy %s: %w", src, err)
	}

	err = atomicjson.WriteFile(dst, b, privateFileMode)
	if err != nil {
		return fmt.Errorf("copy to %s: %w", dst, err)
	}

	return nil
}

// IsHexPSK reports a raw WPA key: 64 hex digits.
func IsHexPSK(s string) bool {
	if len(s) != hexPSKLen {
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
func decode(rec Record) (network, error) {
	var none network

	ssid, err := base64.StdEncoding.DecodeString(rec.SSIDB64)
	if err != nil || len(ssid) == 0 {
		return none, errBadConf
	}

	pass, err := base64.StdEncoding.DecodeString(rec.PSKB64)
	if err != nil {
		return none, errBadConf
	}

	decoded := network{rec: rec, ssid: string(ssid), keyHex: ""}
	switch {
	case rec.HexPSK != "":
		if !IsHexPSK(rec.HexPSK) {
			return none, errBadHex
		}

		decoded.keyHex = strings.ToLower(rec.HexPSK)
	case len(pass) > 0:
		if len(pass) < minPassphrase || len(pass) > maxPassphrase {
			return none, errBadPass
		}

		key, err := pbkdf2.Key(sha1.New, string(pass), ssid, pbkdf2Iterations, pskBytes)
		if err != nil {
			return none, fmt.Errorf("%w: %w", errBadPass, err)
		}

		decoded.keyHex = hex.EncodeToString(key)
	default:
		// An open network: no key.
	}

	return decoded, nil
}
