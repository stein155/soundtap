// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Valid-record headers of BCO app 9423 (stockLayout9423), as they stand on flash.
const (
	kSSID0, kSec0, kPass0 = 0x50ec, 0x50cc, 0x50bc
	kSSID1, kSec1, kPass1 = 0x51bc, 0x519c, 0x518c
	kCipher1              = 0x517c
	kSSID2, kSec2, kPass2 = 0x528c, 0x526c, 0x525c
	kSSID4, kSec4, kPass4 = 0x544c, 0x540c, 0x53fc
	kActive               = 0x549c
	kDeviceName           = 0x4fbc
	kCountry              = 0x54ec
)

// The two settings sectors, the first proven on hardware.
const (
	sectorA = 0x40030000
	sectorB = 0x407e0000
)

// errNoShellReply is a BCO shell that does not answer.
var errNoShellReply = errors.New("BCO shell: no reply")

// superseded turns a valid record's header into the one stock leaves once a newer record
// of the same setting is written.
func superseded(header int) int { return header&^0xf | 0x8 }

// settingsLog builds a synthetic sector as Bose's BCO app lays it out: the sector header,
// then header/string records, each padded to an even length, then erased flash.
func settingsLog(state byte, records ...any) []byte {
	out := []byte{state, 0x00}
	for i := 0; i < len(records); i += 2 {
		out = binary.LittleEndian.AppendUint16(out, uint16(records[i].(int)))
		out = append(out, records[i+1].(string)...)

		out = append(out, 0)
		if len(out)%2 == 1 {
			out = append(out, 0) // stock writes the pad byte as 0, as the owner's dump shows
		}
	}

	for len(out) < 8192 {
		out = append(out, 0xff)
	}

	return out
}

// rdOutput prints data as the sdsShell's rd does, with the echo and prompt around it, and
// noise (a log line) after the line with index noiseAt, when noiseAt >= 0.
func rdOutput(cmd string, base uint32, data []byte, noiseAt int) string {
	var dump strings.Builder
	dump.WriteString(cmd + "\r\n")

	for o, line := 0, 0; o < len(data); o, line = o+16, line+1 {
		fmt.Fprintf(&dump, "0x%08x: ", base+uint32(o))

		for w := o; w < o+16 && w+4 <= len(data); w += 4 {
			fmt.Fprintf(&dump, "0x%08x ", binary.LittleEndian.Uint32(data[w:]))
		}

		dump.WriteString("\r\n")

		if line == noiseAt {
			dump.WriteString("[wlan] link quality 63/70\r\n")
		}
	}

	dump.WriteString("\r\nsds://>")

	return dump.String()
}

// stockShell answers rd from a synthetic flash map through the real dump parser, as the shell
// would print it. Addresses it does not hold get no reply.
type stockShell struct {
	flash    map[uint32][]byte
	reads    []uint32
	maxWords int // the most words one rd prints; 0: no limit
	noise    bool
}

// newStockShell is a shell on flash that prints whole replies without noise.
func newStockShell(flash map[uint32][]byte) *stockShell {
	return &stockShell{flash: flash, reads: nil, maxWords: 0, noise: false}
}

func (f *stockShell) rd(_ context.Context, addr uint32, words int) ([]byte, error) {
	f.reads = append(f.reads, addr)

	for base, data := range f.flash {
		if addr < base || addr >= base+uint32(len(data)) {
			continue
		}

		if f.maxWords > 0 && words > f.maxWords {
			words = f.maxWords
		}

		end := min(int(addr-base)+4*words, len(data))
		cmd := fmt.Sprintf("rd 0x%08x %d", addr, words)

		noiseAt := -1
		if f.noise {
			noiseAt = 1
		}

		reply := cleanShellOutput(rdOutput(cmd, addr, data[addr-base:end], noiseAt), cmd)

		return parseMemoryDump(reply, addr)
	}

	return nil, errNoShellReply
}

func readFake(t *testing.T, f *stockShell) (StockProfile, error) {
	t.Helper()

	return readStockProfileWith(t.Context(), f.rd)
}

func TestStockProfileTakesTheActiveProfile(t *testing.T) {
	t.Parallel()

	home := StockProfile{SSID: "Home net", Passphrase: "correct horse"}
	for name, tcase := range map[string]struct {
		flash    map[uint32][]byte
		want     StockProfile
		maxWords int
	}{
		"profile 1 in a row": {
			flash: map[uint32][]byte{sectorA: settingsLog(0x08, kDeviceName, "Keuken", kCountry, "EU",
				kSSID1, "Home net", kSec1, "WPA2PSK", kPass1, "correct horse")},
			want:     home,
			maxWords: 0,
		},
		"records out of order, others between them": {
			flash: map[uint32][]byte{sectorA: settingsLog(0x08, kPass1, "correct horse", kDeviceName, "Keuken",
				kSSID1, "Home net", kCountry, "EU", kSec1, "WPA2PSK")},
			want:     home,
			maxWords: 0,
		},
		"superseded records lose to the last valid one": {
			flash: map[uint32][]byte{sectorA: settingsLog(0x08,
				superseded(kSSID1), "Old net", superseded(kPass1), "old passphrase",
				kSSID1, "Home net", kPass1, "correct horse")},
			want:     home,
			maxWords: 0,
		},
		"a record being written is not taken": {
			flash: map[uint32][]byte{sectorA: settingsLog(0x08, kSSID1, "Home net", kPass1, "correct horse",
				kPass1&^0xf|0xe, "half written")},
			want:     home,
			maxWords: 0,
		},
		"second sector active": {
			flash: map[uint32][]byte{
				sectorA: settingsLog(0xfe, kSSID1, "Stale net", kPass1, "stale passphrase"),
				sectorB: settingsLog(0x08, kSSID1, "Home net", kPass1, "correct horse"),
			},
			want:     home,
			maxWords: 0,
		},
		"first sector being written, second unreadable": {
			flash:    map[uint32][]byte{sectorA: settingsLog(0xfc, kSSID1, "Home net", kPass1, "correct horse")},
			want:     home,
			maxWords: 0,
		},
		"profile 2 active": {
			flash: map[uint32][]byte{sectorA: settingsLog(0x08, kSSID1, "Other net", kPass1, "other passphrase",
				kSSID2, "Home net", kSec2, "WPAPSK", kPass2, "correct horse", superseded(kActive), "1", kActive, "2")},
			want:     home,
			maxWords: 0,
		},
		"profile 1 without a security record takes its default": {
			flash: map[uint32][]byte{sectorA: settingsLog(0x08, kSSID1, "Home net", kPass1, "correct horse",
				kCipher1, "AES")},
			want:     home,
			maxWords: 0,
		},
		"WPAWPA2PSK and a raw key": {
			flash: map[uint32][]byte{sectorA: settingsLog(0x08, kSSID0, "Home net", kSec0, "WPAWPA2PSK",
				kPass0, strings.Repeat("Ab", 32), kActive, "0")},
			want:     StockProfile{SSID: "Home net", Passphrase: strings.Repeat("Ab", 32)},
			maxWords: 0,
		},
		"an open network, an old passphrase left in it": {
			flash: map[uint32][]byte{sectorA: settingsLog(0x08, kSSID1, "Cafe", kSec1, "NONE",
				kPass1, "correct horse")},
			want:     StockProfile{SSID: "Cafe", Passphrase: ""},
			maxWords: 0,
		},
		"a log longer than one rd, with a short rd": {
			flash: map[uint32][]byte{sectorA: settingsLog(0x08, append(fillers(300),
				kSSID1, "Home net", kPass1, "correct horse")...)},
			want:     home,
			maxWords: 100,
		},
	} {
		shell := &stockShell{flash: tcase.flash, reads: nil, maxWords: tcase.maxWords, noise: true}

		got, err := readFake(t, shell)
		if err != nil || got != tcase.want {
			t.Errorf("%s: got %+v, %v; want %+v", name, got, err, tcase.want)
		}
	}
}

// fillers makes n superseded device-name records, enough to push a profile past one rd.
func fillers(n int) []any {
	out := make([]any, 0, 2*n)
	for i := range n {
		out = append(out, superseded(kDeviceName), fmt.Sprintf("Speaker %d", i))
	}

	return out
}

func TestStockProfileReadsOnlyTheProvenSectorWhenItIsActive(t *testing.T) {
	t.Parallel()

	shell := newStockShell(map[uint32][]byte{
		sectorA: settingsLog(0x08, kSSID1, "Home net", kPass1, "correct horse"),
	})

	_, err := readFake(t, shell)
	if err != nil {
		t.Fatal(err)
	}

	for _, addr := range shell.reads {
		if addr >= sectorB {
			t.Fatalf("read the unproven sector at 0x%08x", addr)
		}
	}
	// The log ends inside the first chunk: one header read, one chunk.
	if len(shell.reads) != 2 {
		t.Fatalf("reads = %x", shell.reads)
	}
}

func TestStockProfileNotImportable(t *testing.T) {
	t.Parallel()

	for name, sector := range map[string][]byte{
		"nothing saved":         settingsLog(0x08, kDeviceName, "Keuken", kCountry, "EU"),
		"both sectors unused":   settingsLog(0xff),
		"wep":                   settingsLog(0x08, kSSID1, "Home net", kSec1, "WEP", kPass1, "abcde"),
		"short passphrase":      settingsLog(0x08, kSSID1, "Home net", kSec1, "WPA2PSK", kPass1, "short"),
		"no passphrase":         settingsLog(0x08, kSSID1, "Home net", kSec1, "WPA2PSK"),
		"empty name":            settingsLog(0x08, kSSID1, "", kPass1, "correct horse"),
		"long name":             settingsLog(0x08, kSSID1, strings.Repeat("n", 33), kPass1, "correct horse"),
		"setup profile active":  settingsLog(0x08, kSSID4, "Bose SoundTouch", kSec4, "NONE", kPass4, "", kActive, "4"),
		"profile out of range":  settingsLog(0x08, kSSID1, "Home net", kPass1, "correct horse", kActive, "7"),
		"profile not a number":  settingsLog(0x08, kSSID1, "Home net", kPass1, "correct horse", kActive, "x"),
		"only superseded":       settingsLog(0x08, superseded(kSSID1), "Home net", superseded(kPass1), "correct horse"),
		"profile 2, no default": settingsLog(0x08, kSSID2, "Home net", kPass2, "correct horse", kActive, "2"),
	} {
		flash := map[uint32][]byte{sectorA: sector}
		if sector[0] == 0xff {
			flash[sectorB] = settingsLog(0xff)
		}

		profile, err := readFake(t, newStockShell(flash))
		if !errors.Is(err, ErrStockProfileNotImportable) {
			t.Errorf("%s: got %+v, %v; want not importable", name, profile, err)
		}
	}
}

// Errors that may pass on a second read are never reported as definitive.
func TestStockProfileTransientErrors(t *testing.T) {
	t.Parallel()

	for name, shell := range map[string]*stockShell{
		"no reply":             newStockShell(map[uint32][]byte{}),
		"second sector silent": newStockShell(map[uint32][]byte{sectorA: settingsLog(0xfe, kSSID1, "Home net")}),
		"no end in 64 KiB": newStockShell(map[uint32][]byte{sectorA: append([]byte{0x08, 0x00, 0xbc, 0x51},
			strings.Repeat("a", stockLogMax)...)}),
		"string into erased flash": newStockShell(map[uint32][]byte{sectorA: append([]byte{0x08, 0x00, 0xbc, 0x51},
			[]byte("Home\xff\xff\xff\xff")...)}),
		"unknown record state": newStockShell(map[uint32][]byte{sectorA: settingsLog(0x08, 0x51b4, "Home net")}),
	} {
		_, err := readFake(t, shell)
		if err == nil || errors.Is(err, ErrStockProfileNotImportable) {
			t.Errorf("%s: got %v; want a retryable error", name, err)
		}
	}
}

func TestStockLogEndsAtTheEndMarker(t *testing.T) {
	t.Parallel()

	// A record after the erased end marker is never read: the log ends at the first 0xf.
	sector := settingsLog(0x08, kSSID1, "Home net", kPass1, "correct horse")
	tail := settingsLog(0x08, kSSID1, "Ghost net")[2:14]
	copy(sector[64:], tail)

	settings, done, err := walkStockLog(sector)
	if err != nil || !done || settings[0x51b] != "Home net" {
		t.Fatalf("settings = %v, done %v, %v", settings, done, err)
	}
	// A log cut off before its end asks for more.
	_, done, err = walkStockLog(sector[:12])
	if done || err != nil {
		t.Fatalf("a cut-off log: done %v, %v", done, err)
	}
}

func TestMemoryDumpRefusesGapsAndNoise(t *testing.T) {
	t.Parallel()

	for name, reply := range map[string]string{
		"empty":      "sds://>",
		"gap":        "0x40030000: 0x00000000 0x00000000 0x00000000 0x00000000\n0x40030020: 0x00000000",
		"wrong base": "0x40010000: 0x00000000 0x00000000 0x00000000 0x00000000",
		"bad word":   "0x40030000: 0x0000 0x00000000\n0x40030008: 0x00000000",
	} {
		_, err := parseMemoryDump(reply, sectorA)
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A log line between two dump lines is skipped, not an error.
	got, err := parseMemoryDump("0x40030000: 0x04030201\nwlan: link up\n0x40030004: 0x08070605", sectorA)
	if err != nil || string(got) != "\x01\x02\x03\x04\x05\x06\x07\x08" {
		t.Fatalf("got %x, %v", got, err)
	}
	// A last line cut off mid-word ends the data before that word.
	got, err = parseMemoryDump("0x40030000: 0x04030201 0x0807\nsds://>", sectorA)
	if err != nil || string(got) != "\x01\x02\x03\x04" {
		t.Fatalf("cut-off line: got %x, %v", got, err)
	}
}
