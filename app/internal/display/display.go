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

// Content is a finished panel frame, oled.Width*oled.Height bytes, and how far the whole
// panel is faded: 0 is full brightness, 1 dark. A fade is the panel's contrast, so a
// screen that fades in or out costs no redraw on the ST20 (oled.Framebuffer.DrawFaded).
type Content struct {
	Frame []byte
	Fade  float64
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
	npFade         float64
	overlayFrame   []byte
	overlayFade    float64
	transientFrame []byte
	transientUntil time.Time
	alertFrame     []byte
	alertUntil     time.Time
	shown          []byte
	shownFade      float64
	drawnAt        time.Time

	// wake ends the slow standby tick at once when something new is set.
	wake chan struct{}
	// done is closed when the paint loop has stopped; see Done.
	done chan struct{}
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
		npFade:         0,
		overlayFrame:   nil,
		overlayFade:    0,
		transientFrame: nil,
		transientUntil: time.Time{},
		alertFrame:     nil,
		alertUntil:     time.Time{},
		shown:          nil,
		shownFade:      0,
		drawnAt:        time.Time{},

		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
	if !mgr.avail {
		log.Info("no ST20 OLED framebuffer; display disabled", "path", fbPath)
		close(mgr.done)

		return mgr
	}

	safego.Go("display.loop", func() {
		defer close(mgr.done)

		mgr.loop(ctx)
	})

	return mgr
}

// Done is closed once the paint loop has stopped after New's context ended, with the
// framebuffer restored: nothing writes to it after that. Without a panel there is no
// loop and it is closed from the start.
func (m *Manager) Done() <-chan struct{} { return m.done }

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
	m.npFrame, m.npFade = content.Frame, content.Fade
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
	m.overlayFrame, m.overlayFade = content.Frame, content.Fade
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

// paintTick is how often the panel is looked at: the design's animations (the boot mark,
// the radio's fade, the network card's note) run at 20 frames a second, each frame
// changing little; only the changed pixels go to the panel. An unchanged frame is written again only after
// repaintEvery: on the ST20 a frame is a 6 KB SPI transfer, and writing the standby
// clock once a second cost CPU all night for a picture that changes once a minute.
// In standby, with only the clock to show, the panel is looked at once a second; any
// overlay or transient, or waking, brings the next tick back to paintTick.
const (
	paintTick        = 50 * time.Millisecond
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

// shownContent is what should be on the panel now, and what was put there last.
type shownContent struct {
	want, shown         []byte
	wantFade, shownFade float64
}

// wanted is the frame to show now: the alert, the overlay, a transient, then the standby or
// now-playing screen; and the frame shown last.
func (m *Manager) wanted(ctx context.Context) shownContent {
	m.mu.Lock()
	alert, transient := m.timed(time.Now())
	standbyFrame, npFrame, overlay := m.standbyFrame, m.npFrame, m.overlayFrame
	out := shownContent{want: nil, shown: m.shown, wantFade: 0, shownFade: m.shownFade}
	npFade, overlayFade := m.npFade, m.overlayFade
	m.mu.Unlock()

	switch {
	case alert != nil:
		out.want = alert
	case overlay != nil:
		out.want, out.wantFade = overlay, overlayFade
	case transient != nil:
		out.want = transient
	case m.standby != nil && m.standby(ctx):
		out.want = standbyFrame
	default:
		out.want, out.wantFade = npFrame, npFade
	}

	return out
}

func (m *Manager) step(ctx context.Context) {
	now := m.wanted(ctx)

	if now.want == nil {
		// Nothing to show (standby with the clock off): a dark panel. There is no
		// screen of anyone else's under ours to put back, and the framebuffer's
		// snapshot is a frame of our own, such as the boot screen's "Connecting…".
		if now.shown != nil {
			err := m.fb.Draw(make([]byte, oled.Width*oled.Height))
			if err == nil {
				m.setShown(nil, 0)
			}
		}

		return
	}

	if bytes.Equal(now.want, now.shown) && now.wantFade == now.shownFade && time.Since(m.drawnAt) < repaintEvery {
		return
	}

	if bytes.Equal(now.want, now.shown) && now.wantFade == now.shownFade {
		// Nothing changed for repaintEvery: send it all again, which heals a panel that lost
		// its frame; a partial draw of an unchanged frame would send nothing.
		m.fb.Resend()
	}

	m.drawnAt = time.Now()

	err := m.fb.DrawFaded(now.want, now.wantFade)
	if err != nil {
		m.log.Warn("display draw failed", "err", err)

		return
	}

	m.setShown(now.want, now.wantFade)
}

func (m *Manager) setShown(b []byte, fade float64) {
	m.mu.Lock()
	m.shown, m.shownFade = b, fade
	m.mu.Unlock()
}
