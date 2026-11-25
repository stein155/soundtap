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
func followBluetooth(
	ctx context.Context, speaker *speakerstate.Speaker, marker string, wake func(),
	pause, disconnect func() error, idle time.Duration, log *slog.Logger,
) {
	selected := false
	first := true

	var drop <-chan time.Time

	for {
		changed := speaker.Changed()

		now := speaker.StatusSnapshot().Source == sourceBluetooth
		if now != selected || first {
			switch {
			case now:
				markBluetooth(marker, log)

				drop = nil
			case selected:
				unmarkBluetooth(marker)

				drop = pauseThenDrop(pause, idle, log)
			default:
				unmarkBluetooth(marker) // none yet on the first pass

				drop = nil
			}

			wake()

			selected, first = now, false
		}

		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-drop:
			drop = nil

			err := disconnect()
			if err != nil {
				log.Warn("drop idle phone", "err", err)
			}
		}
	}
}

// btMarkerMode is the selection marker's mode: bt.sh reads it.
const btMarkerMode = 0o644

// markBluetooth records that Bluetooth is selected.
func markBluetooth(marker string, log *slog.Logger) {
	err := os.WriteFile(marker, nil, btMarkerMode)
	if err != nil {
		log.Warn("bluetooth marker", "err", err)
	}
}

// unmarkBluetooth records that Bluetooth is not selected.
func unmarkBluetooth(marker string) {
	_ = os.Remove(marker)
}

// pauseThenDrop tells the phone to pause after Bluetooth was left and returns the timer
// for dropping its link.
func pauseThenDrop(pause func() error, idle time.Duration, log *slog.Logger) <-chan time.Time {
	go func() {
		err := pause()
		if err != nil {
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
	case err == nil:
	case exitCode(err) == btNoPhoneCame:
		frontPanel.Show(panel.NoticeFor, func() *screen.Frame {
			return panel.MessageScreen(panel.Text("Can't connect"))
		})
	default:
		log.Warn("bluetooth reconnect failed", "comp", "bt-pair", "err", err)
	}
}

// btNoPhoneCame is bt-ctl.sh reconnect's exit when it called phones and none connected.
const btNoPhoneCame = 3

// btReconnecting is set while btReconnect calls the phones.
//
//nolint:gochecknoglobals // process-wide state: btReconnect sets it, the ST20 panel shows it
var btReconnecting atomic.Bool

// lostPhoneTries and lostPhoneGap bound the calls after a lost link: a phone that walks back
// in range within about two minutes is called back, as stock retries its paired list.
const (
	lostPhoneTries = 4
	lostPhoneGap   = 30 * time.Second
)

// recallingPhone keeps to one recall at a time.
//
//nolint:gochecknoglobals // process-wide state: every lost link's recall checks it
var recallingPhone atomic.Bool

// recallLostPhone calls the phones back after a link timed out while Bluetooth is still the
// source, a bounded number of times; bt-ctl.sh returns at once when a phone is back. One
// recall runs at a time.
func recallLostPhone(ctx context.Context, speaker *speakerstate.Speaker, log *slog.Logger) {
	if !recallingPhone.CompareAndSwap(false, true) {
		return
	}
	defer recallingPhone.Store(false)

	for range lostPhoneTries {
		if speaker.StatusSnapshot().Source != sourceBluetooth {
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
