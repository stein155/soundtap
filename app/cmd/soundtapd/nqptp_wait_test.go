// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A /proc/net/udp table as the ST10 shows it with nqptp up (rollout, 2026-10-27).
const udpTable = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt" +
	"   uid  timeout inode ref pointer drops\n" +
	`   12: 0100007F:2328 00000000:0000 07 00000000:00000000 00 00000000 00000000     0        0 1234 2 00000000 0
   40: 00000000:013F 00000000:0000 07 00000000:00000000 00 00000000 00000000     0        0 1235 2 00000000 0
   77: 00000000:14E9 00000000:2328 07 00000000:00000000 00 00000000 00000000     0        0 1236 2 00000000 0
`

func TestUDPListeningReadsTheLocalPort(t *testing.T) {
	t.Parallel()

	table := filepath.Join(t.TempDir(), "udp")

	err := os.WriteFile(table, []byte(udpTable), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if !udpListening(table, 9000) || !udpListening(table, 319) {
		t.Fatal("bound ports not seen")
	}
	// 320 is not bound here.
	if udpListening(table, 320) || udpListening(table+".missing", 9000) {
		t.Fatal("unbound port reported as listening")
	}
}

func TestWaitUntilSeesALateSignal(t *testing.T) {
	t.Parallel()

	start := time.Now()

	late := func() bool { return time.Since(start) > 200*time.Millisecond }
	if !waitUntil(context.Background(), 5*time.Second, late) {
		t.Fatal("a signal after 200ms was not seen")
	}

	never := func() bool { return false }
	if waitUntil(context.Background(), 150*time.Millisecond, never) {
		t.Fatal("a missing signal was reported present")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if waitUntil(ctx, time.Minute, never) {
		t.Fatal("a cancelled wait reported the signal present")
	}
}
