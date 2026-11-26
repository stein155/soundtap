// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// scanEndMarker terminates BCO's site-survey transcript on older firmware.
const scanEndMarker = "END OF SCAN RESULTS"

// scanQuiet ends a survey without the marker: ST20 BCO 9423 dumps every record in
// one burst ~4 s after the command and never prints the marker, then only
// unrelated log lines (ARP notices) follow tens of seconds later.
const scanQuiet = 2 * time.Second

// A survey's bounds: how long it may take and how much of its transcript is kept.
const (
	scanTimeout   = 75 * time.Second
	scanMaxOutput = 65536
)

// errScanIncomplete means the survey ended without a marker or a record.
var errScanIncomplete = errors.New("BCO Wi-Fi scan returned no complete result")

type Network struct {
	SSID    string
	Quality int
	Secure  bool
}

// ScanNetworks runs BCO's passive site survey. It changes no CNE settings.
func ScanNetworks(ctx context.Context, device string) ([]Network, error) {
	err := lockSerial(ctx)
	if err != nil {
		return nil, err
	}

	defer unlockSerial()

	serialFD, err := openSerial(ctx, device)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(serialFD)

	buf := make([]byte, serialChunk)
	drainSerial(serialFD, buf)

	_, err = syscall.Write(serialFD, []byte("sys wlan ctrl scan\r"))
	if err != nil {
		return nil, fmt.Errorf("BCO serial write: %w", err)
	}

	return readSurvey(ctx, serialFD, buf)
}

// readSurvey reads the survey's transcript until its end marker, or until it has gone
// quiet after records, and parses it.
func readSurvey(ctx context.Context, serialFD int, buf []byte) ([]Network, error) {
	deadline := time.Now().Add(scanTimeout)
	// A survey takes tens of seconds and this loop spins every 20 ms, so the
	// end-marker search must not rescan the whole transcript each time: at 64 KiB and
	// ~3750 iterations that is quadratic work on a single-core ARM. Search only the
	// newly-read bytes plus the marker's own length, in case a read splits it.
	var (
		out      []byte
		lastRead time.Time
	)

	for time.Now().Before(deadline) && len(out) <= scanMaxOutput {
		count, err := syscall.Read(serialFD, buf)

		switch {
		case count > 0:
			from := max(0, len(out)-(len(scanEndMarker)-1))
			out = append(out, buf[:count]...)
			lastRead = time.Now()

			if bytes.Contains(out[from:], []byte(scanEndMarker)) {
				return parseScanResults(string(out)), nil
			}
		case !lastRead.IsZero() && time.Since(lastRead) >= scanQuiet:
			if networks := parseScanResults(string(out)); len(networks) > 0 {
				return networks, nil
			}
		case err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK):
			return nil, fmt.Errorf("BCO serial read: %w", err)
		default:
			// Nothing yet: wait below.
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("BCO Wi-Fi scan: %w", ctx.Err())
		case <-time.After(serialPoll):
		}
	}

	return nil, errScanIncomplete
}

func parseScanResults(response string) []Network {
	strongest := map[string]Network{}

	var current Network

	active := false
	flush := func() {
		if !active || strings.TrimSpace(current.SSID) == "" {
			return
		}

		current.SSID = strings.TrimSpace(current.SSID)
		if old, ok := strongest[current.SSID]; !ok || current.Quality > old.Quality {
			strongest[current.SSID] = current
		}
	}

	for raw := range strings.SplitSeq(strings.ReplaceAll(response, "\r", "\n"), "\n") {
		line, ok := scanLine(raw)
		if !ok {
			continue
		}

		if line == "Scan Result:" || strings.HasPrefix(line, "SSID =") {
			flush()

			current, active = Network{SSID: "", Quality: 0, Secure: true}, true
		}

		if active && scanField(&current, line) {
			flush()

			active = false
		}
	}

	flush() // no-op after the marker; keeps the last record when there is none

	result := make([]Network, 0, len(strongest))
	for _, network := range strongest {
		result = append(result, network)
	}

	slices.SortFunc(result, func(a, b Network) int { return cmp.Compare(b.Quality, a.Quality) })

	return result
}

// scanLine is one transcript line in a single spelling, false for the shell's redraws.
// 9423 pads keys ("SSID    = x") and glues the prompt to the first record.
func scanLine(raw string) (string, bool) {
	if strings.ContainsRune(raw, '\b') {
		return "", false
	}

	line := strings.TrimPrefix(strings.TrimSpace(raw), "sds://>")
	if key, value, ok := strings.Cut(line, "="); ok {
		if key = strings.TrimSpace(key); key == "SSID" || key == "RSSIPercent" {
			line = key + " = " + strings.TrimSpace(value)
		}
	}

	return line, true
}

// scanField reads line into the record being built. It returns true at the end marker.
func scanField(current *Network, line string) bool {
	switch {
	case strings.HasPrefix(line, "SSID:"):
		current.SSID = strings.TrimSpace(strings.TrimPrefix(line, "SSID:"))
	case strings.HasPrefix(line, "SSID ="):
		current.SSID = strings.TrimSpace(strings.TrimPrefix(line, "SSID ="))
	case strings.HasPrefix(line, "RSSIPercent:"):
		current.Quality, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "RSSIPercent:")))
	case strings.HasPrefix(line, "RSSIPercent ="):
		current.Quality, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "RSSIPercent =")))
	case strings.EqualFold(line, "Encryption: None"):
		current.Secure = false
	case line == "WPAIE" || line == "RSNIE" || strings.HasPrefix(line, "Encryption:"):
		current.Secure = true
	case line == scanEndMarker:
		return true
	default:
		// Other fields (BSSID, channel, dashes) are not used.
	}

	return false
}
