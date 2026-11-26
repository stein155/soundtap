// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/bco"
)

// fakeLink writes a /sys/class/net with a bcolink0 whose bco/ holds files.
func fakeLink(t *testing.T, files map[string]string) string {
	t.Helper()

	net := t.TempDir()

	err := os.MkdirAll(filepath.Join(net, "wlan0"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(net, "bcolink0", "bco")

	err = os.MkdirAll(dir, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	for name, v := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o444)
		if err != nil {
			t.Fatal(err)
		}
	}

	return net
}

func allStats() map[string]string {
	return map[string]string{
		"tx_ok": "1200\n", "tx_failed": "3\n", "tx_dropped": "17\n", "link_dropped": "19\n",
		"tx_frames": "1220\n", "flow": "1\n", "flow_stops": "42\n",
	}
}

func TestReadLinkStats(t *testing.T) {
	t.Parallel()

	got, found := bco.ReadLinkStats(fakeLink(t, allStats()))

	want := bco.LinkStats{
		TxOK: 1200, TxFailed: 3, TxDropped: 17, LinkDropped: 19, TxFrames: 1220,
		FlowControl: true, FlowStops: 42,
	}
	if !found || got != want {
		t.Fatalf("got %+v %v, want %+v", got, found, want)
	}
}

func TestReadLinkStatsFlowOff(t *testing.T) {
	t.Parallel()

	files := allStats()
	files["flow"] = "0\n"

	got, ok := bco.ReadLinkStats(fakeLink(t, files))
	if !ok || got.FlowControl {
		t.Fatalf("got %+v %v, want flow control off", got, ok)
	}
}

// An app from before LINK_STATS: the driver's files exist but a counter cannot be read
// (the driver answers EOPNOTSUPP); a missing file stands in for that here.
func TestReadLinkStatsWithoutCounters(t *testing.T) {
	t.Parallel()

	files := allStats()
	delete(files, "tx_ok")

	if got, ok := bco.ReadLinkStats(fakeLink(t, files)); ok {
		t.Fatalf("got %+v, want nothing", got)
	}

	files = allStats()

	files["tx_failed"] = "garbage\n"
	if got, ok := bco.ReadLinkStats(fakeLink(t, files)); ok {
		t.Fatalf("got %+v from a garbled counter, want nothing", got)
	}
}

// Every model but the ST20 has no BCO link, and a BCO link from an older driver no bco/.
func TestReadLinkStatsNoLink(t *testing.T) {
	t.Parallel()

	net := t.TempDir()
	for _, d := range []string{"wlan0", "eth0", "bcolink0"} {
		err := os.MkdirAll(filepath.Join(net, d), 0o755)
		if err != nil {
			t.Fatal(err)
		}
	}

	if got, ok := bco.ReadLinkStats(net); ok {
		t.Fatalf("got %+v, want nothing", got)
	}

	if _, ok := bco.ReadLinkStats(filepath.Join(net, "missing")); ok {
		t.Fatal("a missing /sys/class/net gave counters")
	}
}
