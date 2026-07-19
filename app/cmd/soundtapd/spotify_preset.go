// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/web"
)

// presetLoadWindow bounds how long a Spotify preset counts as still loading. librespot's
// /load only queues the request; activating the device and resolving the context took
// more than ten seconds on the ST20 (2026-10-07). A claim later than this is not the
// preset's any more but someone starting Spotify from a phone.
const presetLoadWindow = 60 * time.Second

// presetLoad remembers a Spotify preset that librespot has not started yet. When the
// user picks another source before it starts, librespot's late "sink running" claim
// must not take the amp back: on 2026-10-07 a radio preset pressed during the load was
// replaced by Spotify seconds later, because the pause sent on the switch arrived while
// librespot was "Not Active" and was ignored.
type presetLoad struct {
	mu        sync.Mutex
	at        time.Time // zero: no preset loading
	abandoned bool
	retried   bool // the load was sent again after librespot stopped it
	now       func() time.Time
}

func (p *presetLoad) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *presetLoad) pendingLocked() bool {
	return !p.at.IsZero() && p.clock().Sub(p.at) < presetLoadWindow
}

// start records a preset load sent to librespot and returns its stamp for unclaimed.
func (p *presetLoad) start() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.at, p.abandoned, p.retried = p.clock(), false, false
	return p.at
}

// stoppedEarly reports whether librespot's stop came while the preset was still loading
// and not yet retried. librespot can accept a load and then stop it at once ("failed
// filling up next_track during stopping"); the same press a moment later plays.
// The first such stop asks for one more load; a second stop releases the source.
func (p *presetLoad) stoppedEarly() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.pendingLocked() || p.abandoned || p.retried {
		return false
	}
	p.retried = true
	return true
}

// unclaimed settles the load stamped at when librespot never claimed it: it accepted
// the load and then played nothing ("context is not available"). The speaker
// showed Spotify playing in silence, with pause and play doing nothing. False when the
// load was claimed, cleared or replaced by a newer one.
func (p *presetLoad) unclaimed(at time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.at.IsZero() || !p.at.Equal(at) {
		return false
	}
	p.at, p.abandoned = time.Time{}, false
	return true
}

// clear forgets the load (it failed, so nothing will claim).
func (p *presetLoad) clear() {
	p.mu.Lock()
	p.at, p.abandoned = time.Time{}, false
	p.mu.Unlock()
}

// leave records that SPOTIFY lost the amp; a load still in flight is now abandoned.
func (p *presetLoad) leave() {
	p.mu.Lock()
	if p.pendingLocked() {
		p.abandoned = true
	}
	p.mu.Unlock()
}

// claim reports whether librespot's SPOTIFY claim may take the amp. It is false only
// for the late start of a preset the user already left. Either way the load is settled.
// took is how long the preset waited for this claim, zero when none was loading: the
// number that says whether a slow preset is librespot or us.
func (p *presetLoad) claim() (ok bool, took time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pendingLocked() {
		took = p.clock().Sub(p.at)
	}
	late := p.pendingLocked() && p.abandoned
	p.at, p.abandoned = time.Time{}, false
	return !late, took
}

// wireSpotify hooks Spotify's volume, presets and source handover into the speaker.
func wireSpotify(ctx context.Context, a audioWiring, tap audioTap) {
	sp, webSrv, spot, log := a.sp, a.web, a.spot, a.log
	sp.OnVolume(spot.FollowVolume(ctx))
	sp.OnVolume(func(int) { wakeBluetooth(btWakeFIFO) })
	presets := &presetLoad{}
	retry := wireSpotifyPresets(ctx, sp, spot, presets, log)
	webSrv.AttachSpotify(web.SpotifyHooks{
		Client:  spot,
		Account: spotifyAccount{},
		Claim:   spotifyClaim(ctx, sp, spot, presets, log),
		Stop: func(source string) bool {
			if source != sourceSpotify || !presets.stoppedEarly() {
				return true
			}
			go retry()
			return false
		},
	})
	// Pause librespot rather than kill it when another source takes over: a killed
	// librespot has to log in again, which made every Spotify preset a cold start.
	receivers.SetYield(sourceSpotify, func() bool {
		presets.leave()
		yctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		err := spot.Command(yctx, "pause")
		if err == nil {
			// librespot acknowledges the pause before it stops writing: what it had
			// buffered still reaches the bus and chopped up the next source.
			waitBusQuiet(tap.Health, spotifyDrainQuiet, spotifyDrainMax)
		}
		return err == nil || errors.Is(err, spotify.ErrNotConnected)
	})
}

// wireSpotifyPresets loads a Spotify preset on request and drops it again if it never
// starts or was left before it did. It returns the retry for a load librespot stopped
// before it started.
func wireSpotifyPresets(ctx context.Context, sp *speakerstate.Speaker, spot *spotify.Client, presets *presetLoad, log *slog.Logger) (retry func()) {
	var mu sync.Mutex
	var last [2]string // track, context of the newest load
	retry = func() {
		mu.Lock()
		track, contextURI := last[0], last[1]
		mu.Unlock()
		log.Warn("spotify preset stopped before it started; loading it again", "track", track, "context", contextURI)
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := spot.Play(pctx, track, contextURI); err != nil {
			presets.clear()
			log.Warn("spotify preset retry", "err", err)
			_ = sp.ReleaseSource(ctx, sourceSpotify)
		}
	}
	sp.OnSpotifyPreset(func(uri string) {
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		track, contextURI := "", uri
		if kind, _, _ := spotify.ParseURI(uri); kind == "track" {
			track, contextURI = uri, ""
		}
		at := presets.start()
		mu.Lock()
		last = [2]string{track, contextURI}
		mu.Unlock()
		began := time.Now()
		if err := spot.Play(pctx, track, contextURI); err != nil {
			presets.clear()
			log.Warn("spotify preset", "uri", uri, "err", err)
			_ = sp.ReleaseSource(ctx, sourceSpotify) // don't show a preset that never starts
			return
		}
		log.Info("spotify preset loaded", "uri", uri, "load", time.Since(began).Round(time.Millisecond))
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(presetLoadWindow):
			}
			if presets.unclaimed(at) {
				log.Warn("spotify preset never started", "uri", uri)
				_ = sp.ReleaseSource(ctx, sourceSpotify)
			}
		}()
	})
	return retry
}

// spotifyClaim keeps a preset the user already left from starting late and taking the amp back.
func spotifyClaim(ctx context.Context, sp *speakerstate.Speaker, spot *spotify.Client, presets *presetLoad, log *slog.Logger) func(source string) bool {
	return func(source string) bool {
		// A phone never takes the speaker, as in stock: this only reports on a Bluetooth
		// already selected. Closes the window after the user left it.
		if source == sourceBluetooth {
			return sp.StatusSnapshot().Source == sourceBluetooth
		}
		if source != sourceSpotify {
			return true
		}
		ok, took := presets.claim()
		if took > 0 {
			log.Info("spotify preset started", "after", took.Round(time.Millisecond), "taken", ok)
		}
		if ok {
			return true
		}
		log.Info("spotify preset started after it was left; pausing it")
		go func() {
			pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := spot.Command(pctx, "pause"); err != nil {
				log.Warn("pause late spotify preset", "err", err)
			}
		}()
		return false
	}
}
