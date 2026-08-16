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
	shown          []byte
	drawnAt        time.Time

	// wake ends the slow standby tick at once when something new is set.
	wake chan struct{}
}

func New(ctx context.Context, fbPath string, standby func(context.Context) bool, log *slog.Logger) *Manager {
	m := &Manager{
		fb:      oled.NewFramebuffer(fbPath),
		avail:   oled.Available(fbPath),
		standby: standby,
		log:     log,
		wake:    make(chan struct{}, 1),
	}
	if !m.avail {
		log.Info("no ST20 OLED framebuffer; display disabled", "path", fbPath)
		return m
	}
	safego.Go("display.loop", func() { m.loop(ctx) })
	return m
}

func (m *Manager) Available() bool { return m != nil && m.avail }

func (m *Manager) SetStandby(c Content) {
	if !m.Available() {
		return
	}
	m.mu.Lock()
	m.standbyFrame = c.Frame
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

func (m *Manager) SetNowPlaying(c Content) {
	if !m.Available() {
		return
	}
	m.mu.Lock()
	m.npFrame = c.Frame
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
func (m *Manager) SetOverlay(c Content) {
	if !m.Available() {
		return
	}
	m.mu.Lock()
	m.overlayFrame = c.Frame
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
func (m *Manager) SetTransient(c Content, d time.Duration) {
	if !m.Available() {
		return
	}
	m.mu.Lock()
	m.transientFrame = c.Frame
	m.transientUntil = time.Now().Add(d)
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
	busy := m.overlayFrame != nil || m.transientFrame != nil
	m.mu.Unlock()
	if !busy && m.standby != nil && m.standby(ctx) {
		return standbyPaintTick
	}
	return paintTick
}

func (m *Manager) step(ctx context.Context) {
	m.mu.Lock()
	standbyFrame, npFrame, overlay, transient, until, shown := m.standbyFrame, m.npFrame,
		m.overlayFrame, m.transientFrame, m.transientUntil, m.shown
	if transient != nil && !time.Now().Before(until) {
		m.transientFrame = nil
		transient = nil
	}
	m.mu.Unlock()

	var want []byte
	switch {
	case overlay != nil:
		want = overlay
	case transient != nil:
		want = transient
	case m.standby != nil && m.standby(ctx):
		want = standbyFrame
	default:
		want = npFrame
	}
	if want == nil {
		if shown != nil {
			if err := m.fb.Restore(); err == nil {
				m.setShown(nil)
			}
		}
		return
	}
	if bytes.Equal(want, shown) && time.Since(m.drawnAt) < repaintEvery {
		return
	}
	m.drawnAt = time.Now()
	if err := m.fb.Draw(want); err != nil {
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
