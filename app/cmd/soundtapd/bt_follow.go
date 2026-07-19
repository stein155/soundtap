// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/panel"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/oled/screen"
)

// btSelectedMarker tells bt.sh that Bluetooth is the selected source.
const btSelectedMarker = "/run/soundtap/bt-selected"

// btIdleLink is how long a phone stays connected after Bluetooth was left, as in stock.
const btIdleLink = time.Minute

// followBluetooth keeps stock's rule that only an explicit select makes Bluetooth play.
// While it is selected the marker exists and bt.sh routes the phone to the amp. On leaving
// it the phone is told to pause, and the link is dropped after idle unless Bluetooth is
// selected again first.
func followBluetooth(ctx context.Context, sp *speakerstate.Speaker, marker string, wake func(), pause, disconnect func() error, idle time.Duration, log *slog.Logger) {
	selected := false
	first := true
	var drop <-chan time.Time
	for {
		changed := sp.Changed()
		now := sp.StatusSnapshot().Source == sourceBluetooth
		if now != selected || first {
			drop = markBluetooth(now, selected, marker, pause, idle, log)
			wake()
			selected, first = now, false
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-drop:
			drop = nil
			if err := disconnect(); err != nil {
				log.Warn("drop idle phone", "err", err)
			}
		}
	}
}

// markBluetooth applies a change of selection and returns the timer for dropping the phone,
// which runs only after Bluetooth was left.
func markBluetooth(now, was bool, marker string, pause func() error, idle time.Duration, log *slog.Logger) <-chan time.Time {
	if now {
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			log.Warn("bluetooth marker", "err", err)
		}
		return nil
	}
	_ = os.Remove(marker) // none yet on the first pass
	if !was {
		return nil
	}
	go func() {
		if err := pause(); err != nil {
			log.Warn("pause phone", "err", err)
		}
	}()
	return time.After(idle)
}

// btReconnect calls the paired phones. bt-ctl.sh does nothing if one is already connected
// and gives up after five bounded tries. The ST20 panel says it is reconnecting while
// this runs and, as stock, that it could not connect when no phone came.
func btReconnect(ctx context.Context, log *slog.Logger) {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	btReconnecting.Store(true)
	defer btReconnecting.Store(false)
	err := runBTCtlVerb(cctx, "reconnect")
	switch {
	case err != nil && exitCode(err) == btNoPhoneCame:
		frontPanel.Show(panel.NoticeFor, func() *screen.Frame { return panel.MessageScreen(panel.Text("Can't connect")) })
	case err != nil:
		log.Warn("bluetooth reconnect failed", "comp", "bt-pair", "err", err)
	}
}

// btNoPhoneCame is bt-ctl.sh reconnect's exit when it called phones and none connected.
const btNoPhoneCame = 3

// btReconnecting is set while btReconnect calls the phones.
var btReconnecting atomic.Bool

// lostPhoneTries and lostPhoneGap bound the calls after a lost link: a phone that walks back
// in range within about two minutes is called back, as stock retries its paired list.
const (
	lostPhoneTries = 4
	lostPhoneGap   = 30 * time.Second
)

var recallingPhone atomic.Bool

// recallLostPhone calls the phones back after a link timed out while Bluetooth is still the
// source, a bounded number of times; bt-ctl.sh returns at once when a phone is back. One
// recall runs at a time.
func recallLostPhone(ctx context.Context, sp *speakerstate.Speaker, log *slog.Logger) {
	if !recallingPhone.CompareAndSwap(false, true) {
		return
	}
	defer recallingPhone.Store(false)
	for range lostPhoneTries {
		if sp.StatusSnapshot().Source != sourceBluetooth {
			return
		}
		btReconnect(ctx, log)
		select {
		case <-ctx.Done():
			return
		case <-time.After(lostPhoneGap):
		}
	}
}
