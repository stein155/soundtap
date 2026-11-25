// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"
)

// KeySink takes the key edges of one input: the keypad matrix, a key device or the
// remote. Every edge of one input arrives on one goroutine.
type KeySink interface {
	// Handle is one edge: the key at device index went down or up, and the key it
	// maps to.
	Handle(index int, pressed bool, action string)
	// Reset drops whatever is held, for an input that went away mid-press: no
	// release edge is coming.
	Reset()
}

// KeyEvent decodes one fixed-size record read from an input device into a device
// index, its edge and the action it maps to.
type KeyEvent func(record []byte) (index int, pressed bool, action string, ok bool)

// KeypadEvent decodes the Bose keypad driver's one ASCII letter per edge.
func KeypadEvent(km map[int]string, log *slog.Logger) KeyEvent {
	return func(record []byte) (int, bool, string, bool) {
		index, pressed, ok := decodeEvent(record[0])
		if !ok {
			log.Debug("unmapped keypad byte", "byte", record[0])

			return 0, false, "", false
		}

		action, mapped := km[index]
		if !mapped {
			log.Debug("unmapped key index", "index", index, "pressed", pressed)
		}

		return index, pressed, action, mapped
	}
}

// InputEvent decodes a 32-bit ARM struct input_event from evdev.
func InputEvent(record []byte) (int, bool, string, bool) {
	code, pressed, ok := decodeInputEvent(record)

	return int(code), pressed, inputKeymap[code], ok
}

// ReadKeyDevice feeds a key device's events to keys until ctx ends, reopening the
// device when it goes away. Cancellation closes the file, which unblocks the read on
// any device Go polls (evdev and a FIFO on Linux; Go on macOS does not poll FIFOs);
// the reader then drops its in-flight gestures and returns.
func ReadKeyDevice(ctx context.Context, path string, recordSize int, decode KeyEvent, keys KeySink) {
	record := make([]byte, recordSize)

	for ctx.Err() == nil {
		f, err := os.Open(path)
		if err != nil {
			if !waitToReopen(ctx) {
				return
			}

			continue
		}

		stop := context.AfterFunc(ctx, func() { _ = f.Close() })

		for {
			_, err := io.ReadFull(f, record)
			if err != nil || ctx.Err() != nil {
				break
			}

			if index, pressed, action, ok := decode(record); ok {
				keys.Handle(index, pressed, action)
			}
		}

		keys.Reset() // device closed mid-gesture or shutting down: no release edge is coming

		if stop() {
			_ = f.Close()
		}
		// A node that opens but fails every read (EIO, a stale /dev/input/event*
		// from the glob fallback) would otherwise reopen with no delay and spin a
		// core flat on this single-core box.
		if !waitToReopen(ctx) {
			return
		}
	}
}

// reopenDelay is how long a device that went away waits before it is opened again.
const reopenDelay = 2 * time.Second

// waitToReopen waits reopenDelay, reporting false if ctx ended first.
func waitToReopen(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(reopenDelay):
		return true
	}
}
