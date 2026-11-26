// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// settingsSector builds a sector the way Bose's BCO app lays it out: a two-byte header,
// then key/string records, each padded to an even length.
func settingsSector(records ...any) []byte {
	out := []byte{0x08, 0x00}
	for i := 0; i < len(records); i += 2 {
		out = binary.LittleEndian.AppendUint16(out, uint16(records[i].(int)))
		out = append(out, records[i+1].(string)...)

		out = append(out, 0)
		if len(out)%2 == 1 {
			out = append(out, 0)
		}
	}

	for len(out) < 256 {
		out = append(out, 0xff)
	}

	return out
}

// rdOutput prints data as the sdsShell's rd does, with the echo and prompt around it.
func rdOutput(base uint32, data []byte) string {
	var dump strings.Builder
	dump.WriteString("rd 0x40030000 128\r\n")

	for o := 0; o < len(data); o += 16 {
		fmt.Fprintf(&dump, "0x%08x: ", base+uint32(o))

		for w := o; w < o+16; w += 4 {
			fmt.Fprintf(&dump, "0x%08x ", binary.LittleEndian.Uint32(data[w:]))
		}

		dump.WriteString("\r\n")
	}

	dump.WriteString("\r\nsds://>")

	return dump.String()
}

func TestStockProfileFromAnRdDump(t *testing.T) {
	t.Parallel()

	sector := settingsSector(0x4fbc, "Keuken", 0x4d4c, "0", 0x4e4c, "192.168.1.100", 0x54ec, "EU",
		stockKeySSID, "Home net", stockKeySecurity, "WPA2PSK", stockKeyPassphrase, "correct horse", 0x5e3c, "1")
	reply := cleanShellOutput(rdOutput(stockSettingsAddr, sector), "rd 0x40030000 128")

	got, err := parseMemoryDump(reply)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, sector) {
		t.Fatal("the dump does not read back as the sector")
	}

	p, err := parseStockProfile(got)
	if err != nil || p.SSID != "Home net" || p.Passphrase != "correct horse" {
		t.Fatalf("profile = %+v, %v", p, err)
	}
}

func TestStockProfileRefusesWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()

	// A WPA2 network's three records, with the name and passphrase given.
	wpa2 := func(name, pass string) []byte {
		return settingsSector(stockKeySSID, name, stockKeySecurity, "WPA2PSK", stockKeyPassphrase, pass)
	}
	for name, sector := range map[string][]byte{
		"erased":     settingsSector()[:2],
		"no network": settingsSector(0x4fbc, "Keuken", 0x54ec, "EU"),
		"other keys": settingsSector(0x1111, "Home net", 0x2222, "WPA2PSK", 0x3333, "correct horse"),
		"wep":        settingsSector(stockKeySSID, "Home net", stockKeySecurity, "WEP", stockKeyPassphrase, "abcde"),
		"short pass": wpa2("Home net", "short"),
		"no name":    wpa2("", "correct horse"),
		"long name":  wpa2(strings.Repeat("n", 33), "correct horse"),
		"cut off":    wpa2("Home net", "correct horse")[:30],
		"out of order": settingsSector(
			stockKeySecurity, "WPA2PSK", stockKeySSID, "Home net", stockKeyPassphrase, "correct horse"),
	} {
		profile, err := parseStockProfile(sector)
		if err == nil {
			t.Errorf("%s: accepted as %+v", name, profile)
		}
	}
}

func TestMemoryDumpRefusesGapsAndNoise(t *testing.T) {
	t.Parallel()

	for name, reply := range map[string]string{
		"empty":      "sds://>",
		"gap":        "0x40030000: 0x00000000 0x00000000 0x00000000 0x00000000\n0x40030020: 0x00000000",
		"wrong base": "0x40010000: 0x00000000 0x00000000 0x00000000 0x00000000",
		"bad word":   "0x40030000: 0x0000 0x00000000",
	} {
		_, err := parseMemoryDump(reply)
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A log line between two dump lines is skipped, not an error.
	got, err := parseMemoryDump("0x40030000: 0x04030201\nwlan: link up\n0x40030004: 0x08070605")
	if err != nil || string(got) != "\x01\x02\x03\x04\x05\x06\x07\x08" {
		t.Fatalf("got %x, %v", got, err)
	}
}
