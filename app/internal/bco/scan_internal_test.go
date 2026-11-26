// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseScanResults(t *testing.T) {
	t.Parallel()

	out := `Scan Result:
SSID: Home
RSSIPercent: 42
RSNIE
Scan Result:
SSID: Guest
RSSIPercent: 80
Encryption: None
Scan Result:
SSID: Home
RSSIPercent: 70
WPAIE
END OF SCAN RESULTS`

	got := parseScanResults(out)
	if len(got) != 2 || got[0] != (Network{SSID: "Guest", Quality: 80, Secure: false}) ||
		got[1] != (Network{SSID: "Home", Quality: 70, Secure: true}) {
		t.Fatalf("parseScanResults() = %#v", got)
	}
}

func TestParseAlternateScanResults(t *testing.T) {
	t.Parallel()

	got := parseScanResults("SSID = Cafe\nRSSIPercent = 55\nEncryption: WPA2-CCMP\nEND OF SCAN RESULTS")
	if len(got) != 1 || got[0] != (Network{SSID: "Cafe", Quality: 55, Secure: true}) {
		t.Fatalf("parseScanResults() = %#v", got)
	}
}

// TestParseScanResultsWithoutMarker pins the ST20 BCO 9423 transcript: echo,
// records separated by dashes, no end marker, a trailing log line.
func TestParseScanResultsWithoutMarker(t *testing.T) {
	t.Parallel()

	out := "sys wlan ctrl scan \r\nsds://>SSID    = Home\r\nBSSID   = 02:00:00:00:00:A1\r\nRSSIPercent   = 43\r\n" +
		"Encryption: WPA2-CCMP \r\n----\r\nSSID    = \r\nRSSIPercent   = 66\r\n----\r\n" +
		"SSID    = Cafe\r\nRSSIPercent   = 55\r\nEncryption: None\r\n----\r\n" +
		"private -> public: target ARP request Ethernet address is not set to broadcast, fixing!\r\n"

	got := parseScanResults(out)
	if len(got) != 2 || got[0] != (Network{SSID: "Cafe", Quality: 55, Secure: false}) ||
		got[1] != (Network{SSID: "Home", Quality: 43, Secure: true}) {
		t.Fatalf("parseScanResults() = %#v", got)
	}
}

// TestLockSerialRespectsContext covers a second caller arriving while a site survey
// holds the UART. A survey can hold it for over a minute, so waiting must be
// cancellable — a sync.Mutex would have blocked the whole time.
func TestLockSerialRespectsContext(t *testing.T) {
	t.Parallel()

	err := lockSerial(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	defer unlockSerial()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = lockSerial(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lockSerial on a cancelled context = %v, want context.Canceled", err)
	}

	// A caller with a deadline gives up rather than queueing behind the survey.
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()

	err = lockSerial(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lockSerial with a deadline = %v, want context.DeadlineExceeded", err)
	}

	if waited := time.Since(start); waited > 2*time.Second {
		t.Errorf("waited %v for a 20 ms deadline", waited)
	}
}

// TestScanEndMarkerSplitAcrossReads pins the incremental search: the marker must
// still be found when a serial read cuts it in half, which is why the search window
// overlaps the previous tail instead of only covering the new bytes.
func TestScanEndMarkerSplitAcrossReads(t *testing.T) {
	t.Parallel()

	full := "Scan Result:\nSSID: Home\nRSSIPercent: 70\nEND OF SCAN RESULTS\n"
	for split := 1; split < len(full); split++ {
		var out []byte

		found := false

		for _, chunk := range []string{full[:split], full[split:]} {
			from := max(0, len(out)-(len(scanEndMarker)-1))

			out = append(out, chunk...)
			if bytes.Contains(out[from:], []byte(scanEndMarker)) {
				found = true
			}
		}

		if !found {
			t.Fatalf("marker missed when the transcript is split at byte %d", split)
		}
	}
}
