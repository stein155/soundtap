// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
)

// What a leader does for the speakers following it, by its owner's preferences
// (settings.Multiroom): move their volume with its own, and let them go when it is
// turned off. Both are calls to the followers' own APIs, made by this speaker, because
// the browser cannot make them itself.
//
// Both are best effort and asynchronous. A follower that is unplugged or rebooting is
// the normal case in a household, and the person turning the leader's knob or switching
// it off must not wait for a speaker in another room to answer, or see an error for it.

// volumeSync coalesces follower volume steps. A drag of the group slider is a burst of
// requests, and one GET+POST per follower per request, each on its own goroutine, used
// to overlap: two reads of the same old level, two writes, one step lost, or an older
// step landing after a newer one. Instead every step is added to pending, and one worker
// at a time drains it, applying the summed step to each follower in turn. However the
// requests fall, the followers end up moved by the sum of the steps.
//
// A drag also keeps every level unclamped while it lasts: a follower dragged past 0 and
// back comes back to where it was, not up from 0, so the group's balance survives an
// end of the slider. levels belongs to the one running worker; asked and askedAt to the
// caller of LeaderLevel and NoteAsked, which serialises them (web's volumeMu).
type volumeSync struct {
	mu      sync.Mutex
	pending int
	running bool

	levels   map[string]int // follower base URL -> its unclamped level in this drag
	levelsAt time.Time
	asked    int // the leader's last unclamped level in this drag
	askedAt  time.Time
}

// volumeDrag is how long after its last step a drag still counts as going on.
const volumeDrag = 2 * time.Second

// maxVolume is the top of the 0-100 volume scale.
const maxVolume = 100

// peerRounds is how many peer calls one background job may take: its deadline is this
// many PeerTimeouts.
const peerRounds = 2

// LeaderLevel is the leader's level to measure the next step from: the unclamped level
// last asked in this drag if the speaker still sits at its clamped value, else cur.
func (g *Group) LeaderLevel(cur int) int {
	state := &g.volSync
	if time.Since(state.askedAt) < volumeDrag && min(max(state.asked, 0), maxVolume) == cur {
		return state.asked
	}

	return cur
}

// NoteAsked records the leader's unclamped level for LeaderLevel.
func (g *Group) NoteAsked(level int) { g.volSync.asked, g.volSync.askedAt = level, time.Now() }

// SyncFollowerVolume moves every follower's own volume by delta, in the background. Each
// follower keeps its own level relative to the leader's -- the kitchen stays quieter
// than the living room -- which is why this is a step and not an absolute level.
//
//nolint:gocritic // exported; web passes Env by value
func (g *Group) SyncFollowerVolume(env Env, delta int) {
	state := &g.volSync
	state.mu.Lock()

	state.pending += delta
	if state.running {
		state.mu.Unlock()

		return // the running worker picks it up
	}

	state.running = true
	state.mu.Unlock()
	g.peerJobs.Add(1)
	safego.Go("multiroom.followerVolume", func() {
		defer g.peerJobs.Done()

		for {
			state.mu.Lock()
			delta := state.pending

			state.pending = 0
			if delta == 0 {
				state.running = false
				state.mu.Unlock()

				return
			}
			state.mu.Unlock()

			if state.levels == nil || time.Since(state.levelsAt) >= volumeDrag {
				state.levels = map[string]int{}
			}

			ctx, cancel := context.WithTimeout(context.Background(), peerRounds*env.PeerTimeout)
			for _, base := range env.FollowerPeers(ctx) {
				from, known := state.levels[base]

				level, err := env.StepPeerVolume(ctx, base, delta, from, known)
				if err != nil {
					env.Log.Info("follower volume not synced", "peer", base, "err", err)
					delete(state.levels, base)

					continue
				}

				state.levels[base] = level
			}

			state.levelsAt = time.Now()

			cancel()
		}
	})
}

// ReleaseFollowers tells every follower to stop following, in the background. Each goes
// back to its own snapserver and to standby.
//
//nolint:gocritic // exported; web passes Env by value
func (g *Group) ReleaseFollowers(env Env) {
	g.peerJobs.Add(1)
	safego.Go("multiroom.releaseFollowers", func() {
		defer g.peerJobs.Done()

		ctx, cancel := context.WithTimeout(context.Background(), peerRounds*env.PeerTimeout)
		defer cancel()

		for _, base := range env.FollowerPeers(ctx) {
			err := env.ReleasePeer(ctx, base)
			if err != nil {
				env.Log.Info("follower not released", "peer", base, "err", err)
			}
		}
	})
}

// WaitPeers waits for the calls to followers started so far.
func (g *Group) WaitPeers() { g.peerJobs.Wait() }
