// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

// The Wi-Fi network Bose's BCO app joined, read from the BCO's own flash.
//
// On the ST20 the BCO is the Wi-Fi station, and Bose's software keeps the network there
// and nowhere on the main processor: there is no NetworkProfiles.xml to take over. The
// BCO's settings sector at stockSettingsAddr (a copy lives at 0x407e0000) holds, after a
// two-byte header, records of
//
//	u16 key (little endian), NUL-terminated string, one pad byte if needed to stay even
//
// with the network's name, its security and its passphrase in three records in a row.
// Read on hardware 2026-11-11 with BCO app 9423; the keys are that app's. The sector is
// read through the sdsShell's `rd <address> <words>`, which prints
//
//	0x40030000: 0x4fbc0008 0x6b75654b 0x00006e65 0x304d4c00
//
// four little-endian words to a line. Only Bose's app has that shell, so this has to run
// before AutoInstall replaces it.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

const (
	stockSettingsAddr  = 0x40030000
	stockSettingsWords = 128 // 512 bytes: the three records end well inside them
	stockKeySSID       = 0x51bc
	stockKeySecurity   = 0x519c
	stockKeyPassphrase = 0x518c
	// stockHeaderLen is the sector's header before the first record; stockKeyLen a
	// record's key.
	stockHeaderLen = 2
	stockKeyLen    = 2
	// rdWordLen is one word as rd prints it: "0x" and eight hex digits.
	rdWordLen = 10
)

// The settings read's errors; each keeps the text it had before it was a sentinel.
var (
	errMemoryDump   = errors.New("BCO memory dump")
	errStockProfile = errors.New("BCO stock profile")
)

// StockProfile is the WPA network Bose's BCO app has saved. A Passphrase of 64 hex digits
// is a raw key (IsHexPSK).
type StockProfile struct {
	SSID, Passphrase string
}

// ReadStockProfile asks Bose's BCO app on device for its saved network.
func ReadStockProfile(ctx context.Context, device string) (StockProfile, error) {
	err := lockSerial(ctx)
	if err != nil {
		return StockProfile{}, err
	}

	defer unlockSerial()

	serialFD, err := openSerial(ctx, device)
	if err != nil {
		return StockProfile{}, err
	}
	defer syscall.Close(serialFD)

	reply, err := shellCommand(ctx, serialFD, fmt.Sprintf("rd 0x%08x %d", stockSettingsAddr, stockSettingsWords))
	if err != nil {
		return StockProfile{}, err
	}

	sector, err := parseMemoryDump(reply)
	if err != nil {
		return StockProfile{}, err
	}

	return parseStockProfile(sector)
}

// parseMemoryDump turns `rd` output into the bytes from stockSettingsAddr on. Lines must
// follow each other without a gap; anything else on the console (a log line, the prompt) is skipped.
func parseMemoryDump(reply string) ([]byte, error) {
	var out []byte

	for line := range strings.SplitSeq(reply, "\n") {
		addr, words, ok := strings.Cut(strings.TrimSpace(line), ":")

		lineAddr, err := strconv.ParseUint(strings.TrimPrefix(addr, "0x"), 16, 32)
		if !ok || err != nil || !strings.HasPrefix(addr, "0x") {
			continue
		}

		if due := uint64(stockSettingsAddr) + uint64(len(out)); lineAddr != due {
			return nil, fmt.Errorf("%w: line for 0x%08x where 0x%08x was due", errMemoryDump, lineAddr, due)
		}

		for w := range strings.FieldsSeq(words) {
			v, err := strconv.ParseUint(strings.TrimPrefix(w, "0x"), 16, 32)
			if err != nil || len(w) != rdWordLen {
				return nil, fmt.Errorf("%w: bad word at 0x%08x", errMemoryDump, lineAddr)
			}

			out = binary.LittleEndian.AppendUint32(out, uint32(v))
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no data in the reply", errMemoryDump)
	}

	return out, nil
}

// parseStockProfile finds the name, security and passphrase records in the settings sector.
// It takes a WPA network with a passphrase; anything it does not know (WEP, an open
// network, another app's keys, an erased sector) is an error, never a guess.
func parseStockProfile(sector []byte) (StockProfile, error) {
	type record struct {
		key   uint16
		value string
	}

	var recs []record

	for offset := stockHeaderLen; offset+stockKeyLen < len(sector); {
		key := binary.LittleEndian.Uint16(sector[offset:])

		end := offset + stockKeyLen
		for end < len(sector) && sector[end] != 0 {
			end++
		}

		if end == len(sector) {
			break // the last string runs past what was read
		}

		recs = append(recs, record{key, string(sector[offset+stockKeyLen : end])})
		offset = end + 1
		offset += offset & 1
	}

	for idx := 0; idx+2 < len(recs); idx++ {
		name, sec, passRec := recs[idx], recs[idx+1], recs[idx+2]
		if name.key != stockKeySSID || sec.key != stockKeySecurity || passRec.key != stockKeyPassphrase {
			continue
		}

		ssid, security, pass := name.value, strings.ToUpper(sec.value), passRec.value
		if ssid == "" || len(ssid) > maxSSIDLen {
			return StockProfile{}, fmt.Errorf("%w: no usable network name", errStockProfile)
		}
		// Only what was seen on hardware: a WPA network. How the app writes an open
		// network down is not known, so that one is left to the setup network.
		if strings.Contains(security, "PSK") &&
			(IsHexPSK(pass) || len(pass) >= minPassphraseLen && len(pass) <= maxPassphraseLen) {
			return StockProfile{SSID: ssid, Passphrase: pass}, nil
		}

		return StockProfile{}, fmt.Errorf("%w: security %q is not taken over", errStockProfile, sec.value)
	}

	return StockProfile{}, fmt.Errorf("%w: no saved network found", errStockProfile)
}
