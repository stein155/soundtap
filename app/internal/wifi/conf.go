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
	"strconv"
	"strings"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// The saved networks on /mnt/nv (docs/contributors/nv-state.md; ADR 0120 keeps these formats):
//
//	wifi.conf        SSID_B64=<base64>, PSK_B64=<base64> (empty: open), optional PSK_HEX=<64 hex>,
//	                 optional SECURITY=wep (PSK_B64 then holds the WEP key as typed), and for a
//	                 WEP key at another index than 0 WEP_KEYIDX=<1-3>
//	wifi.conf.good   a copy of the last wifi.conf that gave the speaker an address
//	wifi.conf.known  up to KnownMax lines, newest first: <SSID_B64> <PSK_B64|-> <PSK_HEX|-> [wep [<1-3>]]
//
// A file of an earlier release has no SECURITY line and no fourth field: its networks are WPA or
// open, as they were; one without WEP_KEYIDX or a fifth field has its WEP key at index 0, as
// before (ADR 0120, ADR 0136). A reader that does not know WEP_KEYIDX or the fifth field skips it.
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

// SecurityWEP marks a saved WEP network (ADR 0136): its key is 5 or 13 characters, or 10 or 26
// hex digits.
const SecurityWEP = "wep"

// wepKeys is how many keys a WEP network has: indexes 0 to 3.
const wepKeys = 4

// The WEP key's sizes: 40 or 104 bits, as characters or as hex digits.
const (
	wep40Chars  = 5
	wep104Chars = 13
	wep40Hex    = 10
	wep104Hex   = 26
)

// Record is one saved network as stored: base64 name and passphrase, the raw key in hex
// when there is one, and SecurityWEP for a WEP network with the index of its key (0 to 3; the
// access point holds the key at the same index). Empty fields are absent.
type Record struct {
	SSIDB64     string
	PSKB64      string
	HexPSK      string
	Security    string
	WEPKeyIndex int
}

// NewRecord stores a network the way the setup page hands it over: a WEP network's key as
// typed; otherwise a 64-digit hex key is the key itself, anything else a passphrase.
func NewRecord(ssid, security, psk string) Record {
	rec := Record{
		SSIDB64: base64.StdEncoding.EncodeToString([]byte(ssid)), PSKB64: "", HexPSK: "", Security: "", WEPKeyIndex: 0,
	}

	switch {
	case security == SecurityWEP:
		rec.Security = SecurityWEP
		rec.PSKB64 = base64.StdEncoding.EncodeToString([]byte(psk))
	case IsHexPSK(psk):
		rec.HexPSK = psk
	case psk != "":
		rec.PSKB64 = base64.StdEncoding.EncodeToString([]byte(psk))
	default:
		// An open network: no key.
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

	line := r.SSIDB64 + " " + dash(r.PSKB64) + " " + dash(r.HexPSK)
	if r.Security == SecurityWEP {
		line += " " + SecurityWEP
		if r.WEPKeyIndex != 0 {
			line += " " + strconv.Itoa(r.WEPKeyIndex)
		}
	}

	return line
}

// ConfBytes is rec as wifi.conf.
func ConfBytes(rec Record) []byte {
	conf := "SSID_B64=" + rec.SSIDB64 + "\nPSK_B64=" + rec.PSKB64 + "\n"
	if rec.HexPSK != "" {
		conf += "PSK_HEX=" + rec.HexPSK + "\n"
	}

	if rec.Security == SecurityWEP {
		conf += "SECURITY=" + SecurityWEP + "\n"
		if rec.WEPKeyIndex != 0 {
			conf += "WEP_KEYIDX=" + strconv.Itoa(rec.WEPKeyIndex) + "\n"
		}
	}

	return []byte(conf)
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
		case "SECURITY":
			if value == SecurityWEP {
				rec.Security = SecurityWEP
			}
		case "WEP_KEYIDX":
			rec.WEPKeyIndex = parseWEPKeyIndex(value)
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
		rec := Record{
			SSIDB64: fields[0], PSKB64: orNone(fields[1]), HexPSK: orNone(fields[2]), Security: "", WEPKeyIndex: 0,
		}

		if len(fields) > 3 && fields[3] == SecurityWEP {
			rec.Security = SecurityWEP
		}

		if len(fields) > 4 && rec.Security == SecurityWEP {
			rec.WEPKeyIndex = parseWEPKeyIndex(fields[4])
		}

		out = append(out, rec)
	}

	return out
}

// parseWEPKeyIndex reads a stored WEP key index; anything but 1 to 3 reads as 0, the key
// every earlier file meant.
func parseWEPKeyIndex(value string) int {
	index, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || index < 0 || index >= wepKeys {
		return 0
	}

	return index
}

// Remember puts recs, in their order, in front of the known list in path: the first is the
// newest. A later record or an older line for a network already listed is dropped, and the
// newest KnownMax are kept.
func Remember(path string, recs ...Record) error {
	old, _ := os.ReadFile(path) // no list yet: recs start one
	lines := make([]string, 0, len(recs)+KnownMax)
	seen := map[string]bool{}

	for _, rec := range recs {
		if seen[rec.SSIDB64] {
			continue
		}

		seen[rec.SSIDB64] = true
		lines = append(lines, rec.Line())
	}

	for line := range strings.Lines(string(old)) {
		line = strings.TrimRight(line, "\n")

		ssid, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		if ssid == "" || seen[ssid] {
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

// IsWEPKey reports a WEP key as typed: 5 or 13 printable ASCII characters, or 10 or 26 hex
// digits.
func IsWEPKey(s string) bool {
	_, ok := wepKeyHex(s)

	return ok
}

// wepKeyHex is a WEP key as wpa_supplicant's wep_key0 to wep_key3 take it unquoted: hex digits.
func wepKeyHex(key string) (string, bool) {
	switch len(key) {
	case wep40Hex, wep104Hex:
		_, err := hex.DecodeString(key)
		if err == nil {
			return strings.ToLower(key), true
		}
	case wep40Chars, wep104Chars:
		for _, c := range []byte(key) {
			if c < ' ' || c > '~' {
				return "", false
			}
		}

		return hex.EncodeToString([]byte(key)), true
	default:
	}

	return "", false
}

// network is one block handed to wpa_supplicant: the name and the 32-byte key in hex, the
// WEP key in hex with its index, or no key for an open network. passphrase is kept for SAE,
// which needs the password itself (ADR 0136), when the record has one.
type network struct {
	rec        Record
	ssid       string
	keyHex     string
	wepHex     string
	wepIndex   int
	passphrase string
}

var (
	errBadConf = errors.New("invalid wifi.conf")
	errBadHex  = errors.New("invalid WPA PSK")
	errBadPass = errors.New("invalid WPA credentials")
	errBadWEP  = errors.New("invalid WEP key")
)

// decode turns a stored network into what wpa_supplicant gets. A passphrase becomes its
// key here (PBKDF2-SHA1 over the name, as wpa_passphrase); it reaches the supplicant only as
// SAE's password over the control socket (WPA3 needs the password itself, ADR 0136), never a
// file or a log.
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

	decoded := network{rec: rec, ssid: string(ssid), keyHex: "", wepHex: "", wepIndex: 0, passphrase: ""}
	switch {
	case rec.Security == SecurityWEP:
		key, ok := wepKeyHex(string(pass))
		if !ok {
			return none, errBadWEP
		}

		decoded.wepHex, decoded.wepIndex = key, rec.WEPKeyIndex
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
		decoded.passphrase = string(pass)
	default:
		// An open network: no key.
	}

	return decoded, nil
}
