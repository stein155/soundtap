// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package counters

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	// runDirMode and startsFileMode are the permissions of /run's directory and the count
	// file: world-readable, written by root.
	runDirMode     = 0o755
	startsFileMode = 0o644
)

// matchWriter passes everything to w and adds one to c for each needle in it.
type matchWriter struct {
	mu     sync.Mutex
	w      io.Writer
	needle []byte
	c      *atomic.Uint64
	tail   []byte // the end of the last write, so a needle split across two writes counts
}

// Matches returns a writer that passes everything to w and counts each needle in it on c:
// aplay's "underrun!!!" lines on their way to the log, for example.
func Matches(w io.Writer, needle string, c *atomic.Uint64) io.Writer {
	return &matchWriter{mu: sync.Mutex{}, w: w, needle: []byte(needle), c: c, tail: nil}
}

func (m *matchWriter) Write(chunk []byte) (int, error) {
	m.mu.Lock()

	buf := append(append([]byte(nil), m.tail...), chunk...)
	if n := bytes.Count(buf, m.needle); n > 0 {
		m.c.Add(uint64(n))
		// What follows the last match can still begin the next one, nothing before it.
		buf = buf[bytes.LastIndex(buf, m.needle)+len(m.needle):]
	}

	keep := min(len(buf), len(m.needle)-1)
	m.tail = append([]byte(nil), buf[len(buf)-keep:]...)
	m.mu.Unlock()

	n, err := m.w.Write(chunk)
	if err != nil {
		return n, fmt.Errorf("counting writer: %w", err)
	}

	return n, nil
}

// CountStart records one start of soundtapd in path, a file in /run that a reboot clears,
// and returns how many starts came before it in this boot: the init script's restarts.
func CountStart(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("read start count: %w", err)
	}

	before, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64) // missing or garbled: the first

	err = os.MkdirAll(filepath.Dir(path), runDirMode)
	if err != nil {
		return before, fmt.Errorf("create start count directory: %w", err)
	}

	err = os.WriteFile(path, []byte(strconv.FormatUint(before+1, 10)+"\n"), startsFileMode)
	if err != nil {
		return before, fmt.Errorf("write start count: %w", err)
	}

	return before, nil
}
