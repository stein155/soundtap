// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package display owns the SoundTouch 20 front-panel OLED.
package display

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/oled"
)

// Content is a finished panel frame, oled.Width*oled.Height bytes.
type Content struct {
	Frame []byte
}

// Manager arbitrates the native standby clock and now-playing screens.
type Manager struct {
	fb      *oled.Framebuffer
	avail   bool
	standby func(context.Context) bool
	log     *slog.Logger

	mu             sync.Mutex
	standbyFrame   []byte
	npFrame        []byte
	overlayFrame   []byte
	transientFrame []byte
	transientUntil time.Time
	alertFrame     []byte
	alertUntil     time.Time
	shown          []byte
	drawnAt        time.Time

	// wake ends the slow standby tick at once when something new is set.
	wake chan struct{}
}

func New(ctx context.Context, fbPath string, standby func(context.Context) bool, log *slog.Logger) *Manager {
	mgr := &Manager{
		fb:      oled.NewFramebuffer(fbPath),
		avail:   oled.Available(fbPath),
		standby: standby,
		log:     log,

		mu:             sync.Mutex{},
		standbyFrame:   nil,
		npFrame:        nil,
		overlayFrame:   nil,
		transientFrame: nil,
		transientUntil: time.Time{},
		alertFrame:     nil,
		alertUntil:     time.Time{},
		shown:          nil,
		drawnAt:        time.Time{},

		wake: make(chan struct{}, 1),
	}
	if !mgr.avail {
		log.Info("no ST20 OLED framebuffer; display disabled", "path", fbPath)

		return mgr
	}

	safego.Go("display.loop", func() { mgr.loop(ctx) })

	return mgr
}

func (m *Manager) Available() bool { return m != nil && m.avail }

func (m *Manager) SetStandby(content Content) {
	if !m.Available() {
		return
	}

	m.mu.Lock()
	m.standbyFrame = content.Frame
	m.mu.Unlock()
	m.nudge()
}

// HasStandby reports whether a standby screen is registered.
func (m *Manager) HasStandby() bool {
	if !m.Available() {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.standbyFrame != nil
}

// ClearStandby drops the standby screen. With nothing to paint, step() restores the
// framebuffer that existed before SoundTap first drew on it.
func (m *Manager) ClearStandby() {
	if !m.Available() {
		return
	}

	m.mu.Lock()
	m.standbyFrame = nil
	m.mu.Unlock()
	m.nudge()
}

func (m *Manager) SetNowPlaying(content Content) {
	if !m.Available() {
		return
	}

	m.mu.Lock()
	m.npFrame = content.Frame
	m.mu.Unlock()
	m.nudge()
}

func (m *Manager) ClearNowPlaying() {
	if !m.Available() {
		return
	}

	m.mu.Lock()
	m.npFrame = nil
	m.mu.Unlock()
	m.nudge()
}

// SetOverlay paints a screen that outranks both now-playing and the standby
// clock: booting, and "this speaker is not on your Wi-Fi yet". Both are states
// where what the panel says is the only instruction the owner gets, so neither
// may be pushed aside by a clock or a station name.
func (m *Manager) SetOverlay(content Content) {
	if !m.Available() {
		return
	}

	m.mu.Lock()
	m.overlayFrame = content.Frame
	m.mu.Unlock()
	m.nudge()
}

func (m *Manager) ClearOverlay() {
	if !m.Available() {
		return
	}

	m.mu.Lock()
	m.overlayFrame = nil
	m.mu.Unlock()
	m.nudge()
}

// SetTransient briefly shows direct user feedback such as volume or preset state.
// Boot/setup remains higher priority; afterward the previous screen returns.
func (m *Manager) SetTransient(content Content, lasting time.Duration) {
	if !m.Available() {
		return
	}

	m.mu.Lock()
	m.transientFrame = content.Frame
	m.transientUntil = time.Now().Add(lasting)
	m.mu.Unlock()
	m.nudge()
}

// SetAlert shows what a held key combination is about to do, above everything, the boot,
// offline and setup screens included: the owner holds the setup combo from those screens too,
// and a count hidden under them was let go of at 7 and 9 s (ST20, 2026-11-27). It lasts as a
// transient does, so the caller renews it while it holds.
func (m *Manager) SetAlert(content Content, lasting time.Duration) {
	if !m.Available() {
		return
	}

	m.mu.Lock()
	m.alertFrame = content.Frame
	m.alertUntil = time.Now().Add(lasting)
	m.mu.Unlock()
	m.nudge()
}

// paintTick is how often the panel is looked at: the design's bars and dots move in
// steps of a tenth of a second. An unchanged frame is written again only after
// repaintEvery: on the ST20 a frame is a 6 KB SPI transfer, and writing the standby
// clock once a second cost CPU all night for a picture that changes once a minute.
// In standby, with only the clock to show, the panel is looked at once a second; any
// overlay or transient, or waking, brings the next tick back to paintTick.
const (
	paintTick        = 100 * time.Millisecond
	standbyPaintTick = time.Second
	repaintEvery     = time.Minute
)

func (m *Manager) loop(ctx context.Context) {
	every := paintTick

	tick := time.NewTicker(every)
	defer tick.Stop()

	for {
		// Only the slow standby tick listens to nudges; at paintTick the loop is as it was.
		var wake chan struct{}
		if every == standbyPaintTick {
			wake = m.wake
		}

		select {
		case <-ctx.Done():
			_ = m.fb.Restore()

			return
		case <-tick.C:
		case <-wake:
		}

		m.step(ctx)

		if want := m.tickEvery(ctx); want != every {
			every = want
			tick.Reset(every)
		}
	}
}

func (m *Manager) nudge() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// tickEvery is the period to look at the panel at: slow only while it shows the
// standby clock and nothing outranks it.
func (m *Manager) tickEvery(ctx context.Context) time.Duration {
	m.mu.Lock()
	busy := m.overlayFrame != nil || m.transientFrame != nil || m.alertFrame != nil
	m.mu.Unlock()

	if !busy && m.standby != nil && m.standby(ctx) {
		return standbyPaintTick
	}

	return paintTick
}

// timed returns the alert and the transient still due, dropping those whose time is up.
// Under m.mu.
func (m *Manager) timed(now time.Time) ([]byte, []byte) {
	if m.transientFrame != nil && !now.Before(m.transientUntil) {
		m.transientFrame = nil
	}

	if m.alertFrame != nil && !now.Before(m.alertUntil) {
		m.alertFrame = nil
	}

	return m.alertFrame, m.transientFrame
}

// wanted is the frame to show now: the alert, the overlay, a transient, then the standby or
// now-playing screen; and the frame shown last.
func (m *Manager) wanted(ctx context.Context) ([]byte, []byte) {
	m.mu.Lock()
	alert, transient := m.timed(time.Now())
	standbyFrame, npFrame, overlay, shown := m.standbyFrame, m.npFrame, m.overlayFrame, m.shown
	m.mu.Unlock()

	switch {
	case alert != nil:
		return alert, shown
	case overlay != nil:
		return overlay, shown
	case transient != nil:
		return transient, shown
	case m.standby != nil && m.standby(ctx):
		return standbyFrame, shown
	default:
		return npFrame, shown
	}
}

func (m *Manager) step(ctx context.Context) {
	want, shown := m.wanted(ctx)

	if want == nil {
		// Nothing to show (standby with the clock off): a dark panel. There is no
		// screen of anyone else's under ours to put back, and the framebuffer's
		// snapshot is a frame of our own, such as the boot screen's "Connecting…".
		if shown != nil {
			err := m.fb.Draw(make([]byte, oled.Width*oled.Height))
			if err == nil {
				m.setShown(nil)
			}
		}

		return
	}

	if bytes.Equal(want, shown) && time.Since(m.drawnAt) < repaintEvery {
		return
	}

	m.drawnAt = time.Now()

	err := m.fb.Draw(want)
	if err != nil {
		m.log.Warn("display draw failed", "err", err)

		return
	}

	m.setShown(want)
}

func (m *Manager) setShown(b []byte) {
	m.mu.Lock()
	m.shown = b
	m.mu.Unlock()
}
