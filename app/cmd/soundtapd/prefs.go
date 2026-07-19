// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/btctl"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// Volume and bass persistence, and the idle auto-off. Both are things the stock
// firmware does that SoundTap did not: every reboot landed at the default volume with bass
// flat, and the speaker never powered itself down, so the amp stayed live
// indefinitely after the last listener walked away.

const (
	// prefsSaveInterval is how often the sampler looks for a change. It is a
	// coalescing window, not a delay on the user's action: a held volume key steps
	// every 160 ms and this lands on NAND, so the write happens once the knob has
	// stopped moving rather than twenty times on the way there.
	prefsSaveInterval = 10 * time.Second

	// DefaultAutoOffMinutes matches stock SoundTouch: 20 minutes idle, then standby.
	DefaultAutoOffMinutes = 20

	// autoOffPoll is how often idleness is sampled. Coarse on purpose — the timeout
	// is measured in tens of minutes and this runs forever on one core.
	autoOffPoll = 30 * time.Second
)

// restoreAudioPrefs applies the persisted volume and bass at boot.
//
// Errors are logged and skipped rather than fatal: a speaker that comes up at the
// default volume is a nuisance, one that refuses to come up is not usable.
func restoreAudioPrefs(ctx context.Context, sp *speakerstate.Speaker, set *settings.Store, log *slog.Logger) {
	a := set.Get().Audio
	if !a.HasVolume {
		return // nothing saved yet: keep the built-in defaults
	}
	if err := sp.SetVolume(ctx, a.Volume); err != nil {
		log.Warn("restore volume", "err", err)
	}
	if err := sp.SetBass(ctx, a.Bass); err != nil {
		log.Warn("restore bass", "err", err)
	}
	log.Info("restored audio prefs", "volume", a.Volume, "bass", a.Bass)
}

// runPrefsSaver persists volume and bass whenever they settle on a new value.
func runPrefsSaver(ctx context.Context, sp *speakerstate.Speaker, set *settings.Store, log *slog.Logger) {
	t := time.NewTicker(prefsSaveInterval)
	defer t.Stop()
	last := set.Get().Audio
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		next, err := currentAudioPrefs(ctx, sp)
		if err != nil {
			continue // shutting down
		}
		if next.Volume == last.Volume && next.Bass == last.Bass && last.HasVolume {
			continue
		}
		if err := set.SetAudio(next); err != nil {
			if errors.Is(err, settings.ErrNewerSchema) || errors.Is(err, atomicjson.ErrUnreadable) {
				last = next // read-only (a rollback, an unreadable file): retrying only repeats the warning
			}
			log.Warn("save audio prefs", "err", err)
			continue
		}
		last = next
	}
}

// saveAudioPrefs is the saver's last pass, run on shutdown with its own deadline
// because the daemon's context is about to be cancelled.
func saveAudioPrefs(sp *speakerstate.Speaker, set *settings.Store, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, err := currentAudioPrefs(ctx, sp)
	if err != nil {
		return
	}
	last := set.Get().Audio
	if next.Volume == last.Volume && next.Bass == last.Bass && last.HasVolume {
		return
	}
	// ErrClosed: a factory reset is under way and the prefs must stay gone.
	if err := set.SetAudio(next); err != nil && !errors.Is(err, settings.ErrClosed) {
		log.Warn("save audio prefs on shutdown", "err", err)
	}
}

func currentAudioPrefs(ctx context.Context, sp *speakerstate.Speaker) (settings.Audio, error) {
	bass, err := sp.Bass(ctx)
	if err != nil {
		return settings.Audio{}, err
	}
	return settings.Audio{
		Volume:    sp.StatusSnapshot().Volume,
		Bass:      bass.Target,
		HasVolume: true,
	}, nil
}

// runAutoOff drops the speaker to standby after the configured idle timeout.
//
// "Idle" is decided from PCM that actually reached the DAC, not from the source or
// play status. A stream that has silently died, a paused phone that never told us,
// an AirPlay session whose sender went to sleep — all of those still look like a
// selected, playing source, and all of them leave the amp powered for nothing. The
// nonzero-block counter only advances when real audio is coming out of the speaker.
// AUX is analog passthrough and therefore counts as active while selected, as does an
// input the ST300's processor plays (PRODUCT): its audio never reaches our sink.
func runAutoOff(ctx context.Context, sp *speakerstate.Speaker, set *settings.Store, health audioHealth, log *slog.Logger) {
	if health == nil {
		return // no arbiter (AUDIO_MIX off): no trustworthy idleness signal
	}
	t := time.NewTicker(autoOffPoll)
	defer t.Stop()
	lastBlocks := health().SinkNonzeroBlocks
	lastUser := userFacing(sp.StatusSnapshot())
	quietSince := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		timeout := autoOffTimeout(set)
		blocks := health().SinkNonzeroBlocks
		st := sp.StatusSnapshot()
		// Someone at the keys or the web page re-arms the timer, as stock's user-present
		// event does: a volume change while paused must not be followed by a power-off.
		user := userFacing(st)
		// BlueZ is asked only while Bluetooth is paused, the one case where it decides.
		phone := st.Source == sourceBluetooth && st.PlayStatus == statusPaused && bluetoothPhoneConnected(ctx)
		if autoOffHasActivity(st, phone, blocks, lastBlocks) || user != lastUser {
			lastBlocks, lastUser = blocks, user
			quietSince = time.Now()
			continue
		}
		// Standby resets the clock too, so waking the speaker gives a full timeout
		// rather than powering off again moments later.
		if sp.IsStandby() {
			quietSince = time.Now()
			continue
		}
		if timeout <= 0 || time.Since(quietSince) < timeout {
			continue
		}
		log.Info("auto-off: idle, entering standby", "after", timeout)
		if err := sp.EnterStandby(ctx); err != nil {
			log.Warn("auto-off standby", "err", err)
		}
		quietSince = time.Now()
	}
}

// autoOffHasActivity: AUX or a processor input while selected; Bluetooth while a phone plays,
// even silence, or is paused but still connected; otherwise PCM reaching the DAC.
// A lost phone also leaves Bluetooth paused, and auto-off is what ends that wait.
func autoOffHasActivity(st speakerstate.StatusSnapshot, phoneConnected bool, blocks, lastBlocks uint64) bool {
	bt := st.Source == sourceBluetooth && (st.PlayStatus == playPlaying || st.PlayStatus == statusPaused && phoneConnected)
	return st.Source == sourceAux || st.Source == sourceProduct || bt || blocks != lastBlocks
}

// bluetoothPhoneConnected reports whether a paired phone is connected; false when BlueZ cannot be asked.
func bluetoothPhoneConnected(ctx context.Context) bool {
	return slices.ContainsFunc(pairedBluetoothDevices(ctx), func(d btctl.Device) bool { return d.Connected })
}

// autoOffUser is the part of the status a person changes: a key, a preset or the web page.
type autoOffUser struct {
	source, playStatus string
	volume, bass       int
	muted              bool
}

// A player retrying a dead stream flips between buffering and stopped on its own, and
// counting that as a person kept the speaker on for good; the two count as one state.
func userFacing(st speakerstate.StatusSnapshot) autoOffUser {
	playStatus := st.PlayStatus
	if playStatus == playBuffering {
		playStatus = playStopped
	}
	return autoOffUser{st.Source, playStatus, st.Volume, st.Bass, st.Muted}
}

// autoOffTimeout is read every tick so a settings change takes effect without a
// restart. A negative stored value is treated as disabled, not as "immediately".
func autoOffTimeout(set *settings.Store) time.Duration {
	minutes := set.Get().Audio.AutoOff
	switch {
	case minutes < 0:
		return 0
	case minutes == 0:
		return DefaultAutoOffMinutes * time.Minute
	default:
		return time.Duration(minutes) * time.Minute
	}
}
