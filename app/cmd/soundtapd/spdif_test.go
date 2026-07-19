// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

// The optical pipe takes whole blocks or none: a block that does not fit is dropped, so
// the frames behind it stay aligned and the analog sink is never held up.
func TestDropWriterDropsWholeBlocks(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	fd := int(w.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	d := &dropWriter{fd: fd, size: 100}
	for range 3 {
		if n, err := d.Write(make([]byte, 40)); n != 40 || err != nil {
			t.Fatalf("Write = %d, %v; want 40, nil", n, err)
		}
	}
	q, err := pipeQueued(w.Fd())
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skip("this system cannot report how many bytes wait in a pipe")
	}
	if err != nil || q != 80 {
		t.Fatalf("queued %d, %v; want 80 (the third block dropped whole)", q, err)
	}
}
