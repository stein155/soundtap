// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
)

// Repeat modes of a Queue, as Spotify names them.
const (
	RepeatOff   = "off"
	RepeatAll   = "all"
	RepeatTrack = "track"
)

// Queue is the folder a browse result was played from, as stock plays a folder or album of
// a media server rather than one track. It holds the tracks and which one is on. Empty
// means nothing follows.
type Queue struct {
	mu    sync.Mutex
	items []Item // in play order: shuffled while shuffle is on
	orig  []Item // in the folder's order
	pos   int

	// Shuffle and repeat, as stock's queue had them. They outlast a folder, as a player's do.
	shuffle bool
	repeat  string // RepeatOff when empty
}

// Set makes the folder's audio tracks the queue, on the track at url. A folder that does
// not hold url, or holds nothing else, leaves no queue.
func (q *Queue) Set(folder []Item, url string) {
	var tracks []Item
	pos := -1
	for _, it := range folder {
		if it.URL == "" || !strings.HasPrefix(it.Class, "object.item.audioItem") {
			continue
		}
		if it.URL == url && pos < 0 {
			pos = len(tracks)
		}
		tracks = append(tracks, it)
	}
	if pos < 0 || len(tracks) < 2 {
		tracks, pos = nil, 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.orig, q.items, q.pos = tracks, slices.Clone(tracks), pos
	if q.shuffle {
		q.shuffleLocked()
	}
}

// shuffleLocked puts the queue in a random order with the track that is on first, so all
// the others still follow it.
func (q *Queue) shuffleLocked() {
	if len(q.items) == 0 {
		return
	}
	q.items[0], q.items[q.pos] = q.items[q.pos], q.items[0]
	rest := q.items[1:]
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] }) //nolint:gosec // play order, not a secret
	q.pos = 0
}

// Modes reports whether a folder is queued, and its shuffle and repeat.
func (q *Queue) Modes() (active, shuffle bool, repeat string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) > 0, q.shuffle, cmp.Or(q.repeat, RepeatOff)
}

// SetShuffle turns shuffle on or off. Off goes back to the folder's order, on the same track.
func (q *Queue) SetShuffle(on bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if on == q.shuffle {
		return
	}
	q.shuffle = on
	if on {
		q.shuffleLocked()
		return
	}
	if len(q.items) > 0 {
		cur := q.items[q.pos].URL
		q.items = slices.Clone(q.orig)
		q.pos = max(0, slices.IndexFunc(q.items, func(it Item) bool { return it.URL == cur }))
	}
}

// SetRepeat sets RepeatOff, RepeatAll or RepeatTrack, and reports false for anything else.
func (q *Queue) SetRepeat(mode string) bool {
	switch mode {
	case RepeatOff, RepeatAll, RepeatTrack:
	default:
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.repeat = mode
	return true
}

// Clear ends the queue.
func (q *Queue) Clear() { q.Set(nil, "") }

// Keep ends the queue unless it is on the track at url, and reports whether it still is.
// One step, so a Set that lands while the caller looked at an older track survives.
func (q *Queue) Keep(url string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pos < len(q.items) && q.items[q.pos].URL == url {
		return true
	}
	q.items, q.orig, q.pos = nil, nil, 0
	return false
}

// Holds reports whether the queue is on the track at url, and leaves the queue as it is.
func (q *Queue) Holds(url string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pos < len(q.items) && q.items[q.pos].URL == url
}

// Step moves delta tracks on and returns the track there. Past either end it stays put and
// reports false, unless repeat is on: then the last track wraps to the first.
func (q *Queue) Step(delta int) (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.stepLocked(delta)
}

// Advance is Step(1) for a track that ended: on repeat-track the same track again.
func (q *Queue) Advance() (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.repeat == RepeatTrack {
		return q.stepLocked(0)
	}
	return q.stepLocked(1)
}

func (q *Queue) stepLocked(delta int) (Item, bool) {
	n := q.pos + delta
	if l := len(q.items); l > 0 && q.repeat != "" && q.repeat != RepeatOff {
		n = ((n % l) + l) % l
	}
	if n < 0 || n >= len(q.items) {
		return Item{}, false
	}
	q.pos = n
	return q.items[n], true
}
