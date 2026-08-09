// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/stein155/soundtap/app/internal/hw/model"
)

// Amp is the amplifier's mute and power lines.
type Amp struct {
	modelName string
	mute      string // value file of the mute line (1=unmuted); "" when the model has none
	power     string // value file of the amp enable line (1=powered); "" when not runtime-controlled

	// muteState caches the last value written to the mute GPIO. trackOwnership calls
	// SetMute 20 times a second forever; re-writing an unchanged value was 20 sysfs
	// open/write/close cycles per second, and — when the GPIO path is wrong — 20 warning
	// lines per second into the RAM-backed log. 0 = unknown, 1 = muted, 2 = unmuted.
	muteState atomic.Int32
	// powerState caches the ST20 amp-enable line. 0 = unknown, 1 = off, 2 = on.
	powerState atomic.Int32
	powerMu    sync.Mutex
}

// NewAmp drives the mute and power value files of the model named modelName; "" is a
// line the model does not have. The ST20's lines are found by bank at run time unless
// MUTE_GPIO or AMP_POWER_GPIO names them.
func NewAmp(modelName, mute, power string) *Amp {
	return &Amp{modelName: modelName, mute: mute, power: power}
}

// HasPower reports whether the amplifier's power is runtime-controlled.
func (a *Amp) HasPower() bool { return a.power != "" }

// SetMute mutes or unmutes the amplifier, writing only a change.
func (a *Amp) SetMute(muted bool, log *slog.Logger) {
	if os.Getenv("AUDIO_NOINIT") == "1" || a.mute == "" {
		return
	}
	want := int32(2)
	v := "1"
	if muted {
		want, v = 1, "0"
	}
	// Write first, record afterwards. Swapping the cache before the write meant a failed
	// write still left the cache claiming the line was set: the next identical call short
	// circuits on that cache and never retries, so one transient EIO leaves the amplifier
	// in whatever state it was in, permanently, with the daemon believing otherwise. The
	// worst direction is the one that matters -- believing it is muted while the GPIO is
	// not -- and this is the mute line on a speaker.
	//
	// Load-then-write rather than Swap for the same reason: nothing may be recorded until
	// the hardware has actually taken it.
	if a.muteState.Load() == want {
		return
	}
	if err := SysfsWrite(a.MuteLine(), v, log); err != nil {
		// Leave the cache alone. It still holds whatever was last written successfully,
		// which is the truth, and the next call will try again.
		return
	}
	a.muteState.Store(want)
	// One line per edge: the order of mute against source start is what a pop is read from.
	log.Info("amp mute", "muted", muted)
}

// MuteLine is the value file of the mute line.
func (a *Amp) MuteLine() string { return a.resolveST20Line("MUTE_GPIO", a.mute, 17) }

// resolveST20Line is the value file of an ST20 amplifier line, bank 1 pin offset, numbered
// from the bank's sysfs base at run time; elsewhere, or when env overrides it, fallback.
func (a *Amp) resolveST20Line(env, fallback string, offset int) string {
	if os.Getenv(env) != "" || a.modelName != model.ST20 {
		return fallback
	}
	if base, err := gpioBankBase(gpioBank1Addr); err == nil {
		return filepath.Join(gpioRoot, fmt.Sprintf("gpio%d/value", base+offset))
	}
	return fallback
}

// SetPowered switches the amplifier on or off, writing only a change. It reports whether
// the line is in that state now; a model without the line always is.
func (a *Amp) SetPowered(powered bool, log *slog.Logger) bool {
	if os.Getenv("AUDIO_NOINIT") == "1" || a.power == "" {
		return a.power == ""
	}
	want := int32(1)
	v := "0"
	if powered {
		want, v = 2, "1"
	}
	a.powerMu.Lock()
	defer a.powerMu.Unlock()
	if a.powerState.Load() == want {
		return true
	}
	if err := SysfsWrite(a.PowerLine(), v, log); err == nil {
		a.powerState.Store(want)
		log.Info("amp power", "on", powered)
		return true
	}
	return false
}

// PowerLine is the value file of the amp enable line.
func (a *Amp) PowerLine() string { return a.resolveST20Line("AMP_POWER_GPIO", a.power, 23) }

var unavailableSysfs sync.Map

// SysfsWrite writes val to a sysfs attribute. A missing attribute is said once per path
// at Info, any other failure at Warn every time.
func SysfsWrite(path, val string, log *slog.Logger) error {
	if err := os.WriteFile(path, []byte(val), 0o644); err != nil {
		if os.IsNotExist(err) {
			if _, loaded := unavailableSysfs.LoadOrStore(path, struct{}{}); !loaded {
				log.Info("sysfs unavailable", "path", path)
			}
			return err
		}
		log.Warn("sysfs write", "path", path, "val", val, "err", err)
		return err
	}
	return nil
}
