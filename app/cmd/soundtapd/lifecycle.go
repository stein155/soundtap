// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// shutdownMuteSettle is the wait between the mute and the amplifier's power-off, so the
// amplifier goes off silent.
const shutdownMuteSettle = 100 * time.Millisecond

// lifecycle is how soundtapd shuts down: once, whether a signal, a factory reset or
// the audio side asks.
type lifecycle struct {
	cancel func()
	log    *slog.Logger
	once   sync.Once
	// The arbiter's fade replaces the plain mute once it is up. It is swapped atomically
	// because the signal goroutine may run audioOff while main is still starting.
	fadeOut atomic.Pointer[func()]
	// Set once the audio prefs exist: the last volume and bass are saved before the
	// fade, not lost with the saver's final 10 s.
	savePrefs atomic.Pointer[func()]
}

// audioOff silences the speaker: the arbiter's fade when there is one, a mute and
// the amplifier off before that.
func (l *lifecycle) audioOff() {
	if f := l.fadeOut.Load(); f != nil {
		(*f)()

		return
	}

	amplifier.SetMute(true, l.log.With("comp", "shutdown"))
	time.Sleep(shutdownMuteSettle)
	amplifier.SetPowered(false, l.log.With("comp", "shutdown"))
}

func (l *lifecycle) shutdown() {
	l.once.Do(func() {
		if f := l.savePrefs.Load(); f != nil {
			(*f)()
		}

		orderedShutdown(l.audioOff, l.cancel)
	})
}

// shutdownOnSignal shuts down on SIGINT or SIGTERM. The returned func stops listening.
func (l *lifecycle) shutdownOnSignal(ctx context.Context) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case <-signals:
			l.shutdown()
		case <-ctx.Done():
		}
	}()

	return func() { signal.Stop(signals) }
}

func orderedShutdown(audio, cancel func()) {
	audio()
	cancel()
}
