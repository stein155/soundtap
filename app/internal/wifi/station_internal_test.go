// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestParsePowerSave(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		out  string
		want powerSaveState
	}{
		{"Power save: off\n", powerSaveOff},
		{"Power save: on\n", powerSaveOn},
		{"\tPower save:  ON \n", powerSaveOn},
		{"", powerSaveUnknown},
		{"command failed: Operation not supported (-95)\n", powerSaveUnknown},
		{"Power save: maybe\n", powerSaveUnknown},
	} {
		if got := parsePowerSave([]byte(test.out)); got != test.want {
			t.Errorf("parsePowerSave(%q) = %d, want %d", test.out, got, test.want)
		}
	}
}

// lockedBuffer is a log sink a test reads while nothing else writes it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // bytes.Buffer never fails a write
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// awakeRig is a Manager on the fake system whose log a test reads.
func awakeRig(t *testing.T) (*Manager, *fakeSys, *lockedBuffer) {
	t.Helper()

	bench := newRig(t, "binky")
	logs := &lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}
	bench.m.log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{
		AddSource: false, Level: slog.LevelDebug, ReplaceAttr: nil,
	}))

	return bench.m, bench.sys, logs
}

func countCalls(sys *fakeSys, line string) int {
	count := 0

	for _, c := range sys.Calls() {
		if c == line {
			count++
		}
	}

	return count
}

// A radio that takes power save off is set once and read back once, with nothing to warn of.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestKeepAwakeReadsPowerSaveBack(t *testing.T) {
	m, sys, logs := awakeRig(t)
	m.keepAwake(context.Background(), "associated")

	if countCalls(sys, "iw dev wlan0 set power_save off") != 1 || countCalls(sys, "iw dev wlan0 get power_save") != 1 {
		t.Fatalf("calls = %q", sys.Calls())
	}

	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("warned with power save off: %s", logs)
	}
}

// A station that stays in power save is set off twice and then named in a warning, so a slow
// mDNS or ping answer on a WiLink speaker shows in the log instead of only on the network.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestKeepAwakeWarnsWhenPowerSaveStaysOn(t *testing.T) {
	m, sys, logs := awakeRig(t)
	sys.powerSave = "Power save: on\n"

	m.keepAwake(context.Background(), "associated")

	if got := countCalls(sys, "iw dev wlan0 set power_save off"); got != keepAwakeTries {
		t.Fatalf("set power_save off %d times, want %d: %q", got, keepAwakeTries, sys.Calls())
	}

	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "when=associated") {
		t.Errorf("no warning naming the association: %s", out)
	}
}

// The ST20's BCO has no power save: both requests fail, which is no warning.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestKeepAwakeWithoutPowerSaveIsQuiet(t *testing.T) {
	m, sys, logs := awakeRig(t)
	sys.fail["iw dev wlan0 set power_save"] = true
	sys.fail["iw dev wlan0 get power_save"] = true

	m.keepAwake(context.Background(), "join")

	if countCalls(sys, "iw dev wlan0 set power_save off") != 1 {
		t.Fatalf("calls = %q", sys.Calls())
	}

	out := logs.String()
	if strings.Contains(out, "level=WARN") || !strings.Contains(out, "power save left as it was") {
		t.Errorf("log = %s", out)
	}
}
