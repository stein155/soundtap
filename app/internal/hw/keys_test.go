// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"log/slog"
	"sync"
)

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// recordKeys is a KeySink that records the edges it is handed: "+KEY" for a press,
// "-KEY" for a release.
type recordKeys struct {
	mu     sync.Mutex
	edges  []string
	resets int
}

func (r *recordKeys) Handle(_ int, pressed bool, action string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	edge := "-"
	if pressed {
		edge = "+"
	}

	r.edges = append(r.edges, edge+action)
}

func (r *recordKeys) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.resets++
}

func (r *recordKeys) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.edges...)
}

func (r *recordKeys) presses(action string) int {
	n := 0

	for _, e := range r.got() {
		if e == "+"+action {
			n++
		}
	}

	return n
}
