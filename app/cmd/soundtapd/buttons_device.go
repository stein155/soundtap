// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// sleepCtx waits for d, reporting false if ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// panelKeys is the button handler of the speaker's own keys: they confirm a waiting
// request, the remote's do not.
func panelKeys(
	sp *speakerstate.Speaker, chords []chord, feedback func(string), chime, btPair func(), auxHoldBT bool,
	log *slog.Logger,
) *buttonHandler {
	h := newButtonHandler(sp, log, chords...)
	h.feedback, h.chime, h.btPair, h.auxHoldBT = feedback, chime, btPair, auxHoldBT
	h.confirms = true

	return h
}

// keyDevices is BUTTON_DEVICES, else the model's keypad device. Without that device
// there is no Bose keypad driver: the mainline kernel ships without one on purpose, so
// the matrix is scanned over GPIO instead (matrix) — same scancodes, same key map
// (internal/hw/keypad.go). Only when even that is unavailable does it fall back to
// evdev, which is the QEMU verify path. It returns the devices, comma-separated, and
// whether the matrix is scanned instead.
func keyDevices() (string, bool) {
	devs := os.Getenv("BUTTON_DEVICES")
	if devs != "" {
		return devs, false
	}

	devs = board.KeypadDev

	_, err := os.Stat(devs)
	if err == nil {
		return devs, false
	}

	if hw.KeypadMatrixSupported(board.Name) {
		return "", true
	}

	if events, _ := filepath.Glob("/dev/input/event*"); len(events) > 0 {
		devs = strings.Join(events, ",")
	}

	return devs, false
}

func runButtons(
	ctx context.Context, speaker *speakerstate.Speaker, chords []chord, feedback func(string), chime, btPair func(),
	auxHoldBT bool, log *slog.Logger,
) {
	keymap := hw.LoadKeymap(buttonsUserMap)

	devs, matrix := keyDevices()
	if matrix {
		// The ST10's and ST20's own keypad. Bluetooth has no key on it.
		h := panelKeys(speaker, chords, feedback, chime, nil, auxHoldBT, log)
		hw.RunKeypadMatrix(ctx, board.Name, h, keymap, speaker.IsStandby, log)

		return
	}

	var readers sync.WaitGroup

	for device := range strings.SplitSeq(devs, ",") {
		if device = strings.TrimSpace(device); device == "" {
			continue
		}
		// The speaker's own keys: the Wireless Link's two gpio-keys, the stock keypad
		// driver. The SoundTouch 300's module has no key node, so none of these is there.
		handler := panelKeys(speaker, chords, feedback, chime, btPair, auxHoldBT, log)

		size, decode := 1, hw.KeypadEvent(keymap, log)
		if strings.HasPrefix(device, "/dev/input/event") {
			size, decode = 16, hw.InputEvent
		}

		readers.Go(func() {
			hw.ReadKeyDevice(ctx, device, size, decode, handler)
		})
	}

	readers.Wait()
}

// runIR drives the speaker from its infrared remote. The remote goes through the same
// key states as the panel; hook sets what it shares with the panel's keys.
func runIR(ctx context.Context, speaker *speakerstate.Speaker, hook func(*buttonHandler), log *slog.Logger) {
	dev := hw.IRDevice(board.IRDev)
	if dev == "" {
		log.Info("ir disabled")

		return
	}
	// No recovery chords: the factory-reset and setup-mode combos are front-panel
	// only on purpose — they are the way back in when the network is gone, and a
	// remote that lives on the couch is not that. A remote also cannot hold two
	// keys at once. For the same reason a remote never confirms a waiting request
	// (buttonHandler.confirms stays false): its keys act as they always do.
	h := newButtonHandler(speaker, log)
	hook(h)
	hw.RunIR(ctx, dev, irUserMap, h, log)
}
