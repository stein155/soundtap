// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package counters

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	return &matchWriter{w: w, needle: []byte(needle), c: c}
}

func (m *matchWriter) Write(p []byte) (int, error) {
	m.mu.Lock()

	buf := append(append([]byte(nil), m.tail...), p...)
	if n := bytes.Count(buf, m.needle); n > 0 {
		m.c.Add(uint64(n))
		// What follows the last match can still begin the next one, nothing before it.
		buf = buf[bytes.LastIndex(buf, m.needle)+len(m.needle):]
	}

	keep := min(len(buf), len(m.needle)-1)
	m.tail = append([]byte(nil), buf[len(buf)-keep:]...)
	m.mu.Unlock()

	return m.w.Write(p)
}

// CountStart records one start of soundtapd in path, a file in /run that a reboot clears,
// and returns how many starts came before it in this boot: the init script's restarts.
func CountStart(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}

	before, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64) // missing or garbled: the first
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return before, err
	}

	return before, os.WriteFile(path, []byte(strconv.FormatUint(before+1, 10)+"\n"), 0o644)
}
