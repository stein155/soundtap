// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package supervise

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// capWriter passes at most budget bytes per minute to w and drops the rest, saying so
// once per minute. A child process that floods its output -- ffmpeg on a broken stream
// prints a line per packet -- would otherwise push every other daemon out of the shared
// log ring (ADR 0066).
// childLogBudget is per child and minute: a healthy decoder says a few lines a minute.
const childLogBudget = 16 << 10

// ChildLog is where a child process named name writes its output: stderr, capped at
// childLogBudget a minute.
func ChildLog(name string) io.Writer { return newCapWriter(os.Stderr, name, childLogBudget) }

type capWriter struct {
	mu      sync.Mutex
	w       io.Writer
	name    string
	budget  int
	left    int
	window  time.Time
	dropped int
	now     func() time.Time
}

func newCapWriter(w io.Writer, name string, budget int) *capWriter {
	return &capWriter{
		mu: sync.Mutex{}, w: w, name: name, budget: budget,
		left: 0, window: time.Time{}, dropped: 0, now: time.Now,
	}
}

func (c *capWriter) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if t := c.now(); t.Sub(c.window) >= time.Minute {
		if c.dropped > 0 {
			fmt.Fprintf(c.w, "%s: %d bytes of output dropped\n", c.name, c.dropped)
		}

		c.window, c.left, c.dropped = t, c.budget, 0
	}

	kept := min(len(data), c.left)
	if kept > 0 {
		_, err := c.w.Write(data[:kept])
		if err != nil {
			return 0, fmt.Errorf("%s output: %w", c.name, err)
		}

		c.left -= kept
	}

	c.dropped += len(data) - kept

	return len(data), nil // the child must never block or fail on its log
}
