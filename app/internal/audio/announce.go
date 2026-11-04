// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"io"

	"github.com/stein155/soundtap/app/internal/safego"
)

// Announce starts ducking the source bus and mixing clip over the top at annGain
// (1.0 = clip at full scale). When clip returns EOF the source ramps back to full.
// A second Announce replaces the first.
func (m *Mixer) Announce(clip io.Reader, annGain float64) {
	if annGain <= 0 {
		annGain = 1.0
	}
	// The clip is read on its own goroutine, never on the mixer's. An announcement
	// arrives over HTTP and is decoded by ffmpeg, so a read can block for as long as
	// the network feels like it; doing that from stepInto stopped everything -- no PCM
	// and no silence reached the DAC, and SetVolume, Announce and Announcing all queued
	// behind the same mutex. A stalled notification took the whole device down with it.
	blocks := make(chan []byte, 4)
	stop := make(chan struct{})
	safego.Go("announce.read", func() {
		defer close(blocks)
		for {
			buf := make([]byte, blockBytes)
			n, err := io.ReadFull(clip, buf)
			if n > 0 {
				select {
				case blocks <- buf[:n]:
				case <-stop:
					return
				}
			}
			if err != nil {
				return
			}
		}
	})

	m.mu.Lock()
	prev := m.annStop
	m.ann, m.annStop, m.annBegun = blocks, stop, false
	m.annGain = annGain
	m.target = duckLevel
	m.mu.Unlock()
	if prev != nil { // a second Announce replaces the first: retire its reader
		close(prev)
	}
}

// Announcing reports whether a clip is currently mixed in.
func (m *Mixer) Announcing() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ann != nil
}
