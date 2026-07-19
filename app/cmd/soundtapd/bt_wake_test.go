// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWakeBluetooth(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "wake")
	wakeBluetooth(fifo) // nothing there: must not panic or create the file
	if _, err := os.Stat(fifo); err == nil {
		t.Fatal("wakeBluetooth created the file")
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no fifos here:", err)
	}
	wakeBluetooth(fifo) // no reader: ENXIO, must return rather than block

	// bt.sh holds the FIFO read-write; do the same so the open succeeds.
	r, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	wakeBluetooth(fifo)
	buf := make([]byte, 1)
	if n, _ := r.Read(buf); n != 1 || buf[0] != '\n' {
		t.Fatalf("read %q, want a newline", buf[:n])
	}
}
