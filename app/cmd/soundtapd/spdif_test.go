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
	t.Parallel()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	writerFD := int(writer.Fd())

	err = syscall.SetNonblock(writerFD, true)
	if err != nil {
		t.Fatal(err)
	}

	drop := &dropWriter{fd: writerFD, size: 100}
	for range 3 {
		n, writeErr := drop.Write(make([]byte, 40))
		if n != 40 || writeErr != nil {
			t.Fatalf("Write = %d, %v; want 40, nil", n, writeErr)
		}
	}

	queued, err := pipeQueued(writer.Fd())
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skip("this system cannot report how many bytes wait in a pipe")
	}

	if err != nil || queued != 80 {
		t.Fatalf("queued %d, %v; want 80 (the third block dropped whole)", queued, err)
	}
}
