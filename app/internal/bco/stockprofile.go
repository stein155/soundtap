// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

// The Wi-Fi network Bose's BCO app joined, read from the BCO's own flash.
//
// On the ST20 the BCO is the Wi-Fi station, and Bose's software keeps the network there
// and nowhere on the main processor: there is no NetworkProfiles.xml to take over. Bose's
// BCO app keeps its settings (its "CnE User" store) as an append-only log in a NOR sector,
// described in docs/hardware/st20/bco-recovery.md#6-the-stock-apps-saved-network:
//
//   - Two sectors take turns (stockSectors). The low nibble of a sector's first u16 is its
//     state: 0xf not used, 0xe free, 0xc being written, 0x8 active, 0x0 being removed
//     (the owner's dump of the active sector began with the u16 0x0008).
//     When the active one is full, stock copies the valid records into the other.
//   - Records follow from sector offset 2: a u16 header (little endian), a NUL-terminated
//     string, and one pad byte when the next record would start at an odd address. The
//     header's low nibble is the record's state (0xc valid, 0xe being written, 0x8
//     superseded, 0xf erased: the end of the log), bits 15..4 the setting it holds.
//   - The last valid record of a setting wins; where a record sits says only when it was
//     written. A setting with no valid record keeps stock's factory default.
//
// The sector is read through the sdsShell's `rd <address> <words>`, which prints
//
//	0x40030000: 0x4fbc0008 0x6b75654b 0x00006e65 0x304d4c00
//
// four little-endian words to a line (read on hardware 2026-11-11 with BCO app 9423).
// Only Bose's app has that shell, so this has to run before AutoInstall replaces it.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

// stockSectors are the two sectors of the settings log, in the order they are tried. The
// first was read on hardware; the second comes from offline analysis of app 9423 and is
// read only when the first is not the active one.
func stockSectors() [2]uint32 { return [2]uint32{0x40030000, 0x407e0000} }

const (
	// stockLogMax bounds how much of a sector is read: the sector's size.
	stockLogMax = 64 << 10
	// stockChunkWords is what one `rd` asks for; stockChunkWordsMin is the size proven on
	// hardware, used when the shell prints nothing for a bigger request.
	stockChunkWords    = 1024
	stockChunkWordsMin = 128
	// stockHeaderLen is the sector's header before the first record; stockKeyLen a
	// record's header.
	stockHeaderLen = 2
	stockKeyLen    = 2
	// stateMask is the state nibble of a sector's or a record's header; stateBits its width.
	stateMask = 0xf
	stateBits = 4
	// erasedByte is erased NOR flash.
	erasedByte = 0xff
	// rdWordLen is one word as rd prints it: "0x" and eight hex digits; rdWordBytes the
	// bytes it stands for.
	rdWordLen   = 10
	rdWordBytes = 4
)

// Sector and record states, the low nibble of the header u16. A sector also has 0xf (not
// used), 0xe (free) and 0x0 (being removed), none of which holds the current log.
const (
	sectorActive       = 0x8
	sectorBeingWritten = 0xc
	recordValid        = 0xc
	recordWriting      = 0xe
	recordSuperseded   = 0x8
	recordEnd          = 0xf // erased flash: the end of the log
)

// stockProfileCount is how many profiles stock keeps; stockSetupProfile is stock's own setup
// access point, never a home network.
const (
	stockProfileCount = 5
	stockSetupProfile = 4
)

// stockActiveProfile9423 is the leaf index of WlanCfg/ActiveProfile in BCO app 9423.
const stockActiveProfile9423 = 0x549

// stockLayout is where one BCO app version keeps the settings soundtapd reads.
type stockLayout struct {
	defaultSecurity            map[int]string
	ssid, security, passphrase [stockProfileCount]uint16
	activeProfile              uint16
	defaultActive              int
}

// stockLayout9423 names the settings soundtapd reads, by the leaf index in bits 15..4 of
// their record header. These numbers are BCO app 9423's and nothing else's: another app
// version may number its settings otherwise. A valid record's header is index<<4 | 0xc:
//
//	                     SSID    Security  Passphrase
//	Profile0            0x50ec   0x50cc    0x50bc
//	Profile1            0x51bc   0x519c    0x518c    (Cipher 0x517c, not read)
//	Profile2            0x528c   0x526c    0x525c
//	Profile3            0x535c   0x533c    0x532c
//	Profile4            0x544c   0x540c    0x53fc    stock's own setup AP, never taken
//	WlanCfg/ActiveProfile 0x549c (factory default "1"); DeviceName 0x4fbc and
//	CountryCode 0x54ec are not read.
//
// Factory defaults known for an unwritten setting: Profile1's Security "WPAPSK" (and
// Cipher "AES"). Another profile without a Security record is not taken.
func stockLayout9423() stockLayout {
	return stockLayout{
		ssid:            [stockProfileCount]uint16{0x50e, 0x51b, 0x528, 0x535, 0x544},
		security:        [stockProfileCount]uint16{0x50c, 0x519, 0x526, 0x533, 0x540},
		passphrase:      [stockProfileCount]uint16{0x50b, 0x518, 0x525, 0x532, 0x53f},
		activeProfile:   stockActiveProfile9423,
		defaultActive:   1,
		defaultSecurity: map[int]string{1: "WPAPSK"},
	}
}

// ErrStockProfileNotImportable is a definitive answer: stock's settings were read whole and
// hold no network SoundTap takes over (none saved, a WEP network, a passphrase that is no
// WPA passphrase). Asking again gives the same answer. Every other error from
// ReadStockProfile (no reply, a garbled dump, a cut-off log) may pass on a second try.
var ErrStockProfileNotImportable = errors.New("BCO stock profile not importable")

// The settings read's other errors, each one that may pass on a second try.
var (
	errMemoryDump  = errors.New("BCO memory dump")
	errSettingsLog = errors.New("BCO settings log")
)

func notImportable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrStockProfileNotImportable, fmt.Sprintf(format, args...))
}

// StockProfile is the network Bose's BCO app has saved: a WPA network with its Passphrase,
// or an open one with none (ADR 0136). A Passphrase of 64 hex digits is a raw key
// (IsHexPSK).
type StockProfile struct {
	SSID, Passphrase string
}

// memReader returns the bytes from addr on, at least one word and at most words of them.
type memReader func(ctx context.Context, addr uint32, words int) ([]byte, error)

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

	return readStockProfileWith(ctx, func(ctx context.Context, addr uint32, words int) ([]byte, error) {
		reply, err := shellCommand(ctx, serialFD, fmt.Sprintf("rd 0x%08x %d", addr, words))
		if err != nil {
			return nil, err
		}

		return parseMemoryDump(reply, addr)
	})
}

// readStockProfileWith finds the settings log through readMem and takes the network out of it.
func readStockProfileWith(ctx context.Context, readMem memReader) (StockProfile, error) {
	base, err := findStockLog(ctx, readMem)
	if err != nil {
		return StockProfile{}, err
	}

	settings, err := readStockLog(ctx, readMem, base)
	if err != nil {
		return StockProfile{}, err
	}

	return stockProfileFrom(settings)
}

// findStockLog picks the active sector, or failing that the one being written (stock was
// cut off while it compacted the log). The second sector is read only when the first is
// not active: its address is not proven on hardware.
func findStockLog(ctx context.Context, readMem memReader) (uint32, error) {
	writing := uint32(0)

	for idx, addr := range stockSectors() {
		head, err := readMem(ctx, addr, 1)
		if err != nil {
			if idx > 0 && writing != 0 {
				break // the first is being written and the second unreadable: take the first
			}

			return 0, fmt.Errorf("BCO settings sector 0x%08x: %w", addr, err)
		}

		switch head[0] & stateMask {
		case sectorActive:
			return addr, nil
		case sectorBeingWritten:
			if writing == 0 {
				writing = addr
			}
		default: // not used, free or being removed: try the next sector
		}
	}

	if writing != 0 {
		return writing, nil
	}

	return 0, notImportable("no active settings sector (stock saved nothing)")
}

// readStockLog reads the log at base in chunks until its end and returns the value of each
// setting's last valid record, by leaf index.
func readStockLog(ctx context.Context, readMem memReader, base uint32) (map[uint16]string, error) {
	var sector []byte

	chunk := stockChunkWords

	for {
		settings, done, err := walkStockLog(sector)
		if err != nil || done {
			return settings, err
		}

		if len(sector) >= stockLogMax {
			return nil, fmt.Errorf("%w: no end in the first %d bytes", errSettingsLog, stockLogMax)
		}

		words := min(chunk, (stockLogMax-len(sector))/rdWordBytes)
		chunkAddr := base + uint32(len(sector)) //nolint:gosec // G115: len(sector) < stockLogMax

		data, err := readMem(ctx, chunkAddr, words)
		if err != nil && chunk > stockChunkWordsMin {
			// The shell may refuse a long rd; fall back to the length read on hardware.
			chunk = stockChunkWordsMin

			continue
		}

		if err != nil {
			return nil, fmt.Errorf("%w at 0x%08x: %w", errSettingsLog, chunkAddr, err)
		}

		sector = append(sector, data...)
	}
}

// walkStockLog walks the records of a sector read from its start. done is false when the
// log runs past what was read: read more and walk again.
func walkStockLog(sector []byte) (map[uint16]string, bool, error) {
	settings := map[uint16]string{}
	offset := stockHeaderLen

	for {
		if offset+stockKeyLen > len(sector) {
			return nil, false, nil
		}

		header := binary.LittleEndian.Uint16(sector[offset:])
		if header&stateMask == recordEnd {
			return settings, true, nil
		}

		end := offset + stockKeyLen
		for end < len(sector) && sector[end] != 0 && sector[end] != erasedByte {
			end++
		}

		if end == len(sector) {
			return nil, false, nil
		}

		if sector[end] == erasedByte {
			// Erased flash inside a string: a record cut off while it was written, the
			// last one stock started. What came before it stands.
			if header&stateMask == recordWriting {
				return settings, true, nil
			}

			return nil, false, fmt.Errorf("%w: record 0x%04x at +0x%x runs into erased flash",
				errSettingsLog, header, offset)
		}

		switch header & stateMask {
		case recordValid:
			settings[header>>stateBits] = string(sector[offset+stockKeyLen : end])
		case recordWriting, recordSuperseded:
		default:
			return nil, false, fmt.Errorf("%w: record 0x%04x at +0x%x has an unknown state",
				errSettingsLog, header, offset)
		}

		offset = end + 1
		offset += offset & 1
	}
}

// stockProfileFrom takes the active profile's network out of stock's settings. It takes a
// WPA network with a passphrase and an open network; anything else is
// ErrStockProfileNotImportable, never a guess.
func stockProfileFrom(settings map[uint16]string) (StockProfile, error) {
	layout := stockLayout9423()

	active, err := stockActiveProfile(settings, layout)
	if err != nil {
		return StockProfile{}, err
	}

	ssid, ok := settings[layout.ssid[active]]
	if !ok || ssid == "" {
		return StockProfile{}, notImportable("profile %d has no network saved", active)
	}

	if len(ssid) > maxSSIDLen {
		return StockProfile{}, notImportable("profile %d: network name longer than %d bytes", active, maxSSIDLen)
	}

	security, ok := settings[layout.security[active]]
	if !ok {
		security, ok = layout.defaultSecurity[active]
		if !ok {
			return StockProfile{}, notImportable("profile %d: security not saved and its default is unknown", active)
		}
	}

	switch strings.ToUpper(strings.TrimSpace(security)) {
	case "WPAPSK", "WPA2PSK", "WPAWPA2PSK":
	case "NONE":
		// An open network: stock may keep a passphrase from before, which is not used.
		return StockProfile{SSID: ssid, Passphrase: ""}, nil
	default:
		return StockProfile{}, notImportable("security %q of %q is not taken over", security, ssid)
	}

	pass := settings[layout.passphrase[active]]
	if !IsHexPSK(pass) && (len(pass) < minPassphraseLen || len(pass) > maxPassphraseLen) {
		return StockProfile{}, notImportable("the passphrase of %q is not a WPA passphrase", ssid)
	}

	return StockProfile{SSID: ssid, Passphrase: pass}, nil
}

// stockActiveProfile is the profile stock joins: its ActiveProfile setting, or the factory
// default. Stock's own setup network and numbers it has no profile for are refused.
func stockActiveProfile(settings map[uint16]string, layout stockLayout) (int, error) {
	value, ok := settings[layout.activeProfile]
	if !ok {
		return layout.defaultActive, nil
	}

	profile, err := strconv.Atoi(strings.TrimSpace(value))

	switch {
	case err != nil || profile < 0 || profile >= len(layout.ssid):
		return 0, notImportable("active profile %q is not one stock has", value)
	case profile == stockSetupProfile:
		return 0, notImportable("the active profile is stock's setup network")
	}

	return profile, nil
}

// dumpLineAddr is the address a line of `rd` output starts with, if it is a dump line.
func dumpLineAddr(line string) (uint64, string, bool) {
	addr, words, ok := strings.Cut(strings.TrimSpace(line), ":")
	if !ok || !strings.HasPrefix(addr, "0x") {
		return 0, "", false
	}

	lineAddr, err := strconv.ParseUint(strings.TrimPrefix(addr, "0x"), 16, 32)
	if err != nil {
		return 0, "", false
	}

	return lineAddr, words, true
}

// parseMemoryDump turns `rd` output into the bytes from base on. Lines must follow each
// other without a gap; anything else on the console (a log line, the prompt) is skipped. A
// last line cut off mid-word ends the data there: the caller reads on from where it stops.
func parseMemoryDump(reply string, base uint32) ([]byte, error) {
	var out []byte

	lines := strings.Split(reply, "\n")
	for idx, line := range lines {
		lineAddr, words, ok := dumpLineAddr(line)
		if !ok {
			continue
		}

		if due := uint64(base) + uint64(len(out)); lineAddr != due {
			return nil, fmt.Errorf("%w: line for 0x%08x where 0x%08x was due", errMemoryDump, lineAddr, due)
		}

		for w := range strings.FieldsSeq(words) {
			value, err := strconv.ParseUint(strings.TrimPrefix(w, "0x"), 16, 32)
			if err != nil || len(w) != rdWordLen || !strings.HasPrefix(w, "0x") {
				if lastDumpLine(lines[idx+1:]) {
					break
				}

				return nil, fmt.Errorf("%w: bad word at 0x%08x", errMemoryDump, lineAddr)
			}

			out = binary.LittleEndian.AppendUint32(out, uint32(value))
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no data in the reply", errMemoryDump)
	}

	return out, nil
}

// lastDumpLine reports whether no dump line follows in rest.
func lastDumpLine(rest []string) bool {
	for _, line := range rest {
		_, _, ok := dumpLineAddr(line)
		if ok {
			return false
		}
	}

	return true
}
