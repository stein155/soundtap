// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// scanEndMarker terminates BCO's site-survey transcript on older firmware.
var scanEndMarker = []byte("END OF SCAN RESULTS")

// scanQuiet ends a survey without the marker: ST20 BCO 9423 dumps every record in
// one burst ~4 s after the command and never prints the marker, then only
// unrelated log lines (ARP notices) follow tens of seconds later.
const scanQuiet = 2 * time.Second

type Network struct {
	SSID    string
	Quality int
	Secure  bool
}

// ScanNetworks runs BCO's passive site survey. It changes no CNE settings.
func ScanNetworks(ctx context.Context, device string) ([]Network, error) {
	if err := lockSerial(ctx); err != nil {
		return nil, err
	}

	defer unlockSerial()

	fd, err := openSerial(ctx, device)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)

	buf := make([]byte, 1024)
	for range 16 {
		if n, err := syscall.Read(fd, buf); err != nil || n == 0 {
			break
		}
	}

	if _, err := syscall.Write(fd, []byte("sys wlan ctrl scan\r")); err != nil {
		return nil, fmt.Errorf("BCO serial write: %w", err)
	}

	deadline := time.Now().Add(75 * time.Second)
	// A survey takes tens of seconds and this loop spins every 20 ms, so the
	// end-marker search must not rescan the whole transcript each time: at 64 KiB and
	// ~3750 iterations that is quadratic work on a single-core ARM. Search only the
	// newly-read bytes plus the marker's own length, in case a read splits it.
	var (
		out      []byte
		lastRead time.Time
	)

	for time.Now().Before(deadline) && len(out) <= 65536 {
		n, err := syscall.Read(fd, buf)
		if n > 0 {
			from := max(0, len(out)-(len(scanEndMarker)-1))
			out = append(out, buf[:n]...)
			lastRead = time.Now()

			if bytes.Contains(out[from:], scanEndMarker) {
				return parseScanResults(string(out)), nil
			}
		} else if !lastRead.IsZero() && time.Since(lastRead) >= scanQuiet {
			if networks := parseScanResults(string(out)); len(networks) > 0 {
				return networks, nil
			}
		} else if err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("BCO serial read: %w", err)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}

	return nil, errors.New("BCO Wi-Fi scan returned no complete result")
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
		if strings.ContainsRune(raw, '\b') {
			continue
		}
		// 9423 pads keys ("SSID    = x") and glues the prompt to the first record.
		line := strings.TrimPrefix(strings.TrimSpace(raw), "sds://>")
		if key, value, ok := strings.Cut(line, "="); ok {
			if key = strings.TrimSpace(key); key == "SSID" || key == "RSSIPercent" {
				line = key + " = " + strings.TrimSpace(value)
			}
		}

		switch {
		case line == "Scan Result:" || strings.HasPrefix(line, "SSID ="):
			flush()

			current, active = Network{Secure: true}, true
		}

		if !active {
			continue
		}

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
		case line == "END OF SCAN RESULTS":
			flush()

			active = false
		}
	}

	flush() // no-op after the marker; keeps the last record when there is none

	result := make([]Network, 0, len(strongest))
	for _, network := range strongest {
		result = append(result, network)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Quality > result[j].Quality })

	return result
}
