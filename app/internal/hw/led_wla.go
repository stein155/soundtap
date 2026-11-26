// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// The Wireless Link's front panel, which is not the ST10's.
//
// Two bi-colour lamps, four channels, and one of the four is a PWM: the Wi-Fi lamp's
// white channel runs off timer4, which is why it is the only channel stock ever hands
// an intensity to. The ledState in led.go cannot describe that -- it is seven booleans
// named after lamps this box does not have -- so this is a second, smaller loop rather
// than a seventh field on the first one.
//
// Every timing and level below was read out of BoseApp; the evidence, and the one
// thing that is still open, are in docs/hardware/wla/led-behaviour.md.

// wlaStep is one leg of an animation: hold this level for this long. Level is a
// percentage, because that is the scale stock uses (100 on, 5 in standby) and the
// device tree gives the PWM channel max-brightness 100 to match.
type wlaStep struct {
	ms    int
	level int
}

// wlaLEDs is one animation per channel. A nil channel is dark; a single step is solid.
type wlaLEDs struct {
	wifiWhite []wlaStep
	wifiAmber []wlaStep
	btWhite   []wlaStep
	btBlue    []wlaStep
}

const (
	// What stock writes to the Wi-Fi white channel. Standby is dimmer, not dark:
	// a speaker asleep is still distinguishable from one unplugged.
	wlaOnLevel      = 100
	wlaStandbyLevel = 5
	// "fast blink" and "slow blink" in Bose's own log lines.
	wlaFastBlinkMS = 350
	wlaSlowBlinkMS = 700
	// wlaSolidMS is the length of a solid lamp's one step; it never changes level.
	wlaSolidMS = 1000
	// wlaLeastChanging is the fewest steps an animation that changes level has.
	wlaLeastChanging = 2
	// wlaLEDMode is the mode a lamp file would be created with; the kernel's files exist.
	wlaLEDMode = 0o644
	// wlaLEDDir is the gpio-leds tree.
	wlaLEDDir = "/sys/class/leds"
)

func wlaSolid(level int) []wlaStep { return []wlaStep{{ms: wlaSolidMS, level: level}} }

func wlaBlink(ms, level int) []wlaStep {
	return []wlaStep{{ms: ms, level: level}, {ms: ms, level: 0}}
}

// wlaLEDPattern is the whole of CLedViewBinkyInterface::SetLed that this box can
// reach: the Wi-Fi lamp reports the network and the update, the Bluetooth lamp
// reports the source.
func wlaLEDPattern(snap *speakerstate.StatusSnapshot, link Link, booting, updating, confirming bool) wlaLEDs {
	level := wlaOnLevel
	if snap.Standby {
		level = wlaStandbyLevel
	}

	var leds wlaLEDs

	switch {
	case confirming:
		// A request waits for a key press: fast amber, which no state has.
		leds.wifiAmber = wlaBlink(wlaFastBlinkMS, wlaOnLevel)
	case updating:
		// "set Status LED to white, solid for 3 seconds, then double-blink, repeat".
		leds.wifiWhite = []wlaStep{{3000, level}, {250, 0}, {250, level}, {250, 0}}
	case booting, link == LinkConnecting:
		// Stock blinks white while it associates, and the daemon coming up is the
		// same thing from the outside: not connected yet, working on it.
		leds.wifiWhite = wlaBlink(wlaFastBlinkMS, level)
	case link == LinkSetupAP:
		// Solid amber is the "come and set me up" signal, and it is the one LED
		// state an owner of this speaker will recognise on sight.
		leds.wifiAmber = wlaSolid(wlaOnLevel)
	case link == LinkUp:
		leds.wifiWhite = wlaSolid(level)
	default:
		leds.wifiAmber = wlaBlink(wlaSlowBlinkMS, wlaOnLevel)
	}

	// The source lamp. Stock drives the blue channel with a pulse animation built
	// into its own LED driver rather than with a step list, so there is no period to
	// copy: blue is a plain GPIO here and can only blink. Everything else about the
	// Bluetooth lamp is stock's: solid white when a device is connected and playing,
	// dark for every other source.
	if !snap.Standby && snap.Source == sourceBluetooth {
		// A transport key wins for the length of one blink. Every one of stock's
		// transport states -- play/pause/stop, next/prev, mute, and "that did
		// nothing" -- builds the same 250 ms dark, 250 ms lit pattern and hands it
		// to this channel with a repeat count of 1 rather than the 0xffff the
		// steady states use. So it is one blink over the top of whatever the lamp
		// was showing, and the window in speakerstate is its length.
		switch {
		case snap.TransportAck:
			leds.btBlue = nil
			leds.btWhite = []wlaStep{{250, 0}, {250, wlaOnLevel}}
		case snap.PlayStatus == playing:
			leds.btWhite = wlaSolid(wlaOnLevel)
		default:
			leds.btBlue = wlaBlink(wlaSlowBlinkMS, wlaOnLevel)
		}
	}

	return leds
}

// wlaLevelAt is the level a channel shows `elapsed` into its animation. An empty
// animation is dark. The steps repeat forever, as they do in stock.
func wlaLevelAt(steps []wlaStep, elapsed time.Duration) int {
	total := 0
	for _, s := range steps {
		total += s.ms
	}

	if total <= 0 {
		return 0
	}

	into := int(elapsed.Milliseconds()) % total
	for _, s := range steps {
		if into < s.ms {
			return s.level
		}

		into -= s.ms
	}

	return 0
}

// wlaChannels pairs each field with the gpio-leds label the device tree gives it.
// Only the Wi-Fi white channel takes a percentage; the other three are plain GPIOs
// whose brightness file accepts 0 or 1, so any non-zero level is simply "on".
func wlaChannels(leds *wlaLEDs) []struct {
	label string
	steps []wlaStep
	pwm   bool
} {
	return []struct {
		label string
		steps []wlaStep
		pwm   bool
	}{
		{"wla:white:wifi", leds.wifiWhite, true},
		{"wla:amber:wifi", leds.wifiAmber, false},
		{"wla:white:bt", leds.btWhite, false},
		{"wla:blue:bt", leds.btBlue, false},
	}
}

func wlaWriteLevel(ledDir, label string, level int, pwm bool) error {
	value := "0"

	switch {
	case pwm:
		value = strconv.Itoa(level)
	case level > 0:
		value = "1"
	default:
		// A dark plain GPIO lamp: 0.
	}

	brightness := filepath.Join(ledDir, label, "brightness")

	err := os.WriteFile(brightness, []byte(value), wlaLEDMode) //nolint:gosec // G703: /sys/class/leds, or LEDS
	if err != nil {
		return fmt.Errorf("lamp %s: %w", label, err)
	}

	return nil
}

// RunWLALEDs drives the Wireless Link's two lamps from st until ctx ends.
//
// It wakes on the step boundaries of whatever is blinking, because the animations are
// described in milliseconds rather than in blinks (the shortest leg stock uses is 250 ms).
func RunWLALEDs(ctx context.Context, status LampStatus, log *slog.Logger) {
	ledDir := os.Getenv("LEDS")
	if ledDir == "" {
		ledDir = wlaLEDDir
	}

	brightness := filepath.Join(ledDir, "wla:white:wifi", "brightness")

	_, err := os.Stat(brightness) //nolint:gosec // G703: /sys/class/leds, or LEDS
	if err != nil {
		// An older device tree with no LED nodes. Say so once: four silent write
		// failures per tick is not a diagnosis.
		log.Info("leds", "dir", ledDir, "note", "no wla lamps in this device tree")

		return
	}

	log.Info("leds", "dir", ledDir)

	wait := time.NewTimer(0)
	defer wait.Stop()

	var current wlaLEDs

	started := time.Now()
	last := map[string]int{}
	haveLast := false

	var (
		link   Link
		linkAt time.Time
	)

	changed := status.Changed()

	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-wait.C:
		}

		changed = status.Changed()
		// The link probe dials UDP and reads pidfiles; at the 50 ms blink tick that was
		// real load on the single-core Wireless Link. Once a second is plenty for a lamp.
		if time.Since(linkAt) >= time.Second {
			link, linkAt = ReadLink(status.HaveIP), time.Now()
		}

		snapshot := status.StatusSnapshot()

		next := wlaLEDPattern(&snapshot, link, !status.Up(), status.Updating(), status.Confirming())
		if !haveLast || !sameWLALEDs(&next, &current) {
			// A new pattern starts at its first step, not wherever the old one was:
			// otherwise a lamp that has just been told to blink can begin dark for
			// most of its first period.
			current, started, haveLast = next, time.Now(), true
		}

		err = wlaWriteLevels(ledDir, &current, time.Since(started), last)
		if err != nil {
			log.Warn("led write", "dir", ledDir, "err", err)
		}

		wait.Reset(wlaSleep(&current, time.Since(started)))
	}
}

// wlaWriteLevels writes each channel's level elapsed into its animation, where it differs
// from the level last holds for it.
func wlaWriteLevels(ledDir string, leds *wlaLEDs, elapsed time.Duration, last map[string]int) error {
	errs := []error{}

	for _, channel := range wlaChannels(leds) {
		level := wlaLevelAt(channel.steps, elapsed)
		if was, seen := last[channel.label]; seen && was == level {
			continue
		}

		errs = append(errs, wlaWriteLevel(ledDir, channel.label, level, channel.pwm))
		last[channel.label] = level
	}

	return errors.Join(errs...)
}

// wlaSleep is how long the loop sleeps: until a blinking lamp's next step, a state change,
// or a second at most. The link, the daemon and an update are not state changes and are
// polled. A steady pattern used to wake this loop 20 times a second for nothing.
func wlaSleep(leds *wlaLEDs, elapsed time.Duration) time.Duration {
	sleep := time.Second
	for _, channel := range wlaChannels(leds) {
		if d := wlaNextStep(channel.steps, elapsed); d > 0 && d < sleep {
			sleep = d
		}
	}

	return sleep
}

// wlaNextStep is how long until a channel's level may change, or 0 when it never does
// (dark or solid).
func wlaNextStep(steps []wlaStep, elapsed time.Duration) time.Duration {
	if len(steps) < wlaLeastChanging {
		return 0
	}

	total := 0
	for _, s := range steps {
		total += s.ms
	}

	if total <= 0 {
		return 0
	}

	into := int(elapsed.Milliseconds()) % total
	for _, s := range steps {
		if into < s.ms {
			return time.Duration(s.ms-into) * time.Millisecond
		}

		into -= s.ms
	}

	return time.Millisecond
}

func sameWLALEDs(left, right *wlaLEDs) bool {
	return sameWLASteps(left.wifiWhite, right.wifiWhite) && sameWLASteps(left.wifiAmber, right.wifiAmber) &&
		sameWLASteps(left.btWhite, right.btWhite) && sameWLASteps(left.btBlue, right.btBlue)
}

func sameWLASteps(left, right []wlaStep) bool {
	return slices.Equal(left, right)
}
