// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuf is a log a test reads while the watch writes it.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p) //nolint:wrapcheck // a bytes.Buffer
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	// Whole or not at all, as sysfs gives it: the watch may read at any moment.
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err == nil {
		err = os.WriteFile(path+".new", []byte(content), 0o644)
	}

	if err == nil {
		err = os.Rename(path+".new", path)
	}

	if err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}

		time.Sleep(2 * time.Millisecond)
	}
}

// The ST20's setup AP in the log: its first beacon once, with the time since it started; its
// state when it changes, not when only a client's silence grows; each client by the end of its
// address; and nothing more once the AP is gone.
func TestSetupAPStateIsLoggedWhenItChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := DefaultPaths()
	paths.SysNet, paths.State = filepath.Join(dir, "net"), filepath.Join(dir, "st")
	apFile := filepath.Join(paths.SysNet, "bcolink0", "bco", "ap")
	writeFile(t, filepath.Join(paths.SysNet, "wlan1", "statistics", "rx_packets"), "12\n")
	writeFile(t, apFile, "up 1\nbeacons 0\nprobes 0\n")
	writeFile(t, filepath.Join(paths.State, "setup-ap"), "")

	var out lockedBuf

	timing := DefaultTiming()
	timing.APWatch = 2 * time.Millisecond
	mgr := New(Config{
		Conf: "", Baked: "", STA: "", AP: "", Variant: "spotty", Paths: paths, Timing: timing, Sys: nil,
		Hostname: nil, SetupSSID: nil, Ready: nil, Joined: nil, Problem: nil,
		Log: slog.New(slog.NewTextHandler(&out, nil)),
	})

	mgr.watchAP(t.Context(), time.Now())
	waitFor(t, "the first status", func() bool { return strings.Contains(out.String(), "setup AP status") })

	if s := out.String(); !strings.Contains(s, "wlan1_rx_packets=12") || !strings.Contains(s, "beacons=0") ||
		strings.Contains(s, "beaconing") {
		t.Fatalf("first look:\n%s", s)
	}

	client := "client 02:99:88:77:66:55 dozing 1 held 2 idle_ms %s rx_data 3 rx_null 4 tx_ok 5 tx_failed 0\n"
	writeFile(t, apFile, "up 1\nbeacons 7\nfirst_beacon_ms 4\nprobes 2\ncore_filter_missed 9\n"+
		strings.Replace(client, "%s", "100", 1))
	waitFor(t, "the client", func() bool { return strings.Contains(out.String(), "setup AP client") })

	logged := out.String()
	if strings.Count(logged, "setup AP beaconing") != 1 || !strings.Contains(logged, "first_beacon_ms=4") {
		t.Fatalf("beaconing not logged once:\n%s", logged)
	}

	if !strings.Contains(logged, "client=…77:66:55 dozing=1 held=2 idle_ms=100 rx_data=3") ||
		strings.Contains(logged, "02:99:88") || !strings.Contains(logged, "core_filter_missed=9") {
		t.Fatalf("client line:\n%s", logged)
	}

	// only the client's silence grows: no new lines
	statuses := strings.Count(logged, "setup AP status")

	writeFile(t, apFile, "up 1\nbeacons 7\nfirst_beacon_ms 4\nprobes 2\ncore_filter_missed 9\n"+
		strings.Replace(client, "%s", "9000", 1))
	time.Sleep(30 * time.Millisecond)

	if got := strings.Count(out.String(), "setup AP status"); got != statuses {
		t.Fatalf("logged again for idle time only: %d status lines, want %d", got, statuses)
	}

	// the AP is gone: the watch ends
	_ = os.Remove(filepath.Join(paths.State, "setup-ap"))

	time.Sleep(10 * time.Millisecond)

	lines := strings.Count(out.String(), "\n")

	writeFile(t, apFile, "up 0\nbeacons 0\n")
	time.Sleep(30 * time.Millisecond)

	if got := strings.Count(out.String(), "\n"); got != lines {
		t.Fatalf("logged after the AP was gone:\n%s", out.String())
	}
}

func TestShortMACKeepsTheLastThreeOctets(t *testing.T) {
	t.Parallel()

	if got := shortMAC("02:99:88:77:66:55"); got != "…77:66:55" {
		t.Fatalf("shortMAC = %q", got)
	}

	if got := shortMAC("odd"); got != "odd" {
		t.Fatalf("shortMAC(odd) = %q", got)
	}
}
