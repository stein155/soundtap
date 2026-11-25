// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// LampStatus is what the lamps show: the speaker's playback state, and the daemon's own.
type LampStatus interface {
	StatusSnapshot() speakerstate.StatusSnapshot
	// Changed is closed on the next change of the playback state.
	Changed() <-chan struct{}
	// Up is false until the HTTP listener is bound — the same moment the boot watchdog
	// is disarmed. The lamps show a boot until then.
	Up() bool
	// Confirming is true while a request waits for a key press, or a held chord counts down.
	Confirming() bool
	// Updating is true while a firmware install is writing to NAND.
	Updating() bool
	// HaveIP reports whether the speaker has an address on the LAN.
	HaveIP() bool
}

// playing is the play status of a source that is playing, as speakerstate reports it.
const playing = "PLAY_STATE"

// The source names the lamps tell apart, as they appear in the API.
const (
	sourceAirPlay   = "AIRPLAY"
	sourceAux       = "AUX"
	sourceBluetooth = "BLUETOOTH"
	sourceMultiroom = "MULTIROOM"
	sourceSpotify   = "SPOTIFY"
	sourceTuneIn    = "TUNEIN"
	sourceUPnP      = "UPNP"
)

// ledState is one lamp per field, named after the lamp on the front panel rather than
// after Bose's rledN channel numbering.
//
// The panel has seven lamps, not nine. Bose's driver exposes uledw, uledy and rled2..8,
// and the first mapping of those to lamps was inferred from the channel names — which
// turned out to be shifted across four of them, and to invent two lamps that are not
// wired at all. Walking the channels on hardware (each channel lit by hand) gave the truth
// below; a wrong guess here is invisible, because the write succeeds either way and the
// lamp simply never lights.
type ledState struct {
	wifiWhite bool // uledw, gpio0_19
	wifiAmber bool // uledy, gpio2_17
	btWhite   bool // rled3, gpio0_4
	stWhite   bool // rled4, gpio0_12  — the SoundTouch lamp
	stAmber   bool // rled5, gpio2_12
	btBlue    bool // rled6, gpio2_9   — one blue, not two
	auxWhite  bool // rled7, gpio2_23
	// wifiDim drives the Wi-Fi white lamp at low brightness instead of off: stock's
	// standby look on the ST10 is that one lamp, dimmed, and nothing else. It needs a PWM
	// lamp to be dim; on a plain gpio-leds lamp it is simply on. Only meaningful when
	// wifiWhite is false.
	wifiDim bool
	// rled2 (gpio2_1) and rled8 (gpio2_16) drive nothing on this board: both light no
	// lamp, and Bose's own pinmux leaves gpio2_16 in LCD mode on a board with no LCD.
}

// linkState is what the front panel needs to know about the network. It is
// derived from facts the daemon can observe for itself — a live wpa_supplicant,
// a live hostapd, a routable address — rather than from a marker file that a
// shell script has to remember to write and, more importantly, to remove.
type Link int

const (
	LinkDown       Link = iota // radio not up yet, or Wi-Fi never configured
	LinkConnecting             // associating: wpa_supplicant is running, no address yet
	LinkSetupAP                // hosting SoundTap-Setup; the user has to come configure us
	LinkUp                     // associated with an address
)

// bootChase is the "I am starting" animation: stock's own, measured rather than invented.
//
// /etc/init.d/rhino-boot-splash starts /opt/Bose/led-splash-write before the rest of
// Bose's userland and calls it a "knight rider LED pattern". Running that binary under
// qemu against FIFOs in place of /dev/shelby-leds gives the sequence byte for byte:
// uledw, rled3, rled7, rled4, rled7, rled3, and round again -- a sweep out to the far
// lamp and back, with the two endpoints not repeated. One lamp at full brightness, the
// other three written 0, every 350 ms, which is this loop's tick.
// docs/hardware/st10/boot-splash.md has the measurement.
//
// A board that has no lamp for one of these steps simply shows nothing on that step;
// applyLEDState skips a channel the tree does not name.
func bootChase(step int) ledState {
	lamps := []func(*ledState){
		func(s *ledState) { s.wifiWhite = true },
		func(s *ledState) { s.btWhite = true },
		func(s *ledState) { s.auxWhite = true },
		func(s *ledState) { s.stWhite = true },
		func(s *ledState) { s.auxWhite = true },
		func(s *ledState) { s.btWhite = true },
	}
	st := ledState{}
	lamps[((step%len(lamps))+len(lamps))%len(lamps)](&st)

	return st
}

func ledPattern(st speakerstate.StatusSnapshot, link Link, blink bool) ledState {
	next := ledState{}
	// The Wi-Fi lamp reports the network regardless of what is playing, so a user
	// staring at a silent speaker can tell "still connecting" from "come set me up".
	switch link {
	case LinkUp:
		next.wifiWhite = true
	case LinkConnecting:
		next.wifiWhite = blink
	case LinkSetupAP:
		next.wifiAmber = true
	case LinkDown:
		// Slow amber blink as stock: solid amber means setup mode, come and configure me.
		next.wifiAmber = blink
	}

	// Empty-preset feedback: blink stAmber for ~2 s after pressing an empty slot.
	// Stock gives the same one-tap amber blink. Shown even in standby so the user
	// can feel the button responded; returns early so WiFi state is still visible.
	if st.EmptyPreset {
		next.stAmber = blink

		return next
	}

	// Standby wins over the source lamps: the speaker goes dark even if a
	// notification window is still open (a chime that fired just before power-off)
	// — otherwise the LED keeps blinking in standby until the window lapses.
	//
	// Setup and association remain visible in standby; they are boot progress, not a
	// playback source. A healthy speaker shows only the Wi-Fi lamp, dimmed, which is what
	// stock does; lighting the SoundTouch lamp here kept it at full brightness on a board
	// whose gpio-leds cannot dim, so standby looked like "on".
	if st.Standby {
		return ledState{
			wifiAmber: link == LinkSetupAP,
			wifiWhite: link == LinkConnecting && blink,
			// Dim on linkUp only; linkDown stays dark (no network = off state)
			wifiDim: link == LinkUp,
		}
	}

	if st.Notification {
		next.stAmber, next.stWhite = blink, blink

		return next
	}

	return sourceLamps(next, st, blink)
}

// sourceLamps lights the lamp of the playing source over next, which holds the Wi-Fi lamp.
func sourceLamps(next ledState, st speakerstate.StatusSnapshot, blink bool) ledState {
	// AUX is a line-in: it is live whenever it is selected, and it has no transport
	// to be buffering or paused, so it is decided before any play-status branch.
	if st.Source == sourceAux {
		next.auxWhite = true

		return next
	}

	if st.Source == sourceBluetooth {
		// Blue blinking is "discoverable, waiting for a phone"; white is a device
		// actually playing. Selecting Bluetooth and seeing nothing at all was
		// indistinguishable from a dead source.
		if st.PlayStatus == playing {
			next.btWhite = true
		} else {
			next.btBlue = blink
		}

		return next
	}

	if st.PlayStatus == "BUFFERING_STATE" {
		next.stWhite = blink

		return next
	}

	if st.PlayStatus != playing {
		next.stAmber = true

		return next
	}

	switch st.Source {
	case sourceTuneIn, sourceAirPlay, sourceSpotify, sourceUPnP, sourceMultiroom:
		next.stWhite = true
	default:
		next.stAmber = true
	}

	return next
}

// pidAlive reports whether a pidfile names a process that still exists. A daemon that
// crashed leaves its pidfile behind, so the file existing is not enough.
func pidAlive(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	// On Linux this is the standard liveness probe: signal 0 validates the pid
	// without delivering anything.
	return syscall.Kill(pid, 0) == nil
}

// ReadLink samples the network for the front panel. The pidfile paths are the ones the
// Wi-Fi manager starts the daemons with; see app/internal/wifi.
func ReadLink(haveIP func() bool) Link {
	return linkFromFacts(haveIP(), pidAlive("/run/soundtap-hostapd.pid"), pidAlive("/run/soundtap-wpa.pid"))
}

func linkFromFacts(haveIP, hostapd, wpa bool) Link {
	switch {
	case hostapd:
		return LinkSetupAP
	case wpa && !haveIP:
		return LinkConnecting
	case haveIP:
		return LinkUp
	default:
		return LinkDown
	}
}

// ledChannels pairs each lamp with the file that drives it on either kernel. The stock
// rledN name and the mainline label must always name the same GPIO; that pairing, not the
// name, is what makes the two paths agree.
func ledChannels(st ledState) []struct {
	mainline string
	stock    string
	on       bool
} {
	return []struct {
		mainline string
		stock    string
		on       bool
	}{
		{"wifi-white", "uledw", st.wifiWhite},
		{"wifi-amber", "uledy", st.wifiAmber},
		{"bt-white", "rled3", st.btWhite},
		{"soundtouch-white", "rled4", st.stWhite},
		{"soundtouch-amber", "rled5", st.stAmber},
		{"bt-blue", "rled6", st.btBlue},
		{"aux-white", "rled7", st.auxWhite},
	}
}

// gpioLEDs reports whether this is the mainline gpio-leds tree.
//
// It used to demand every channel, on the reasoning that a partial set means the DTB does
// not match this binary. What that produced instead was a speaker with no lamps at all:
// the ST10 tree dropped aux-white while the pad it sits on was in dispute, and soundtapd
// then fell back to the stock character devices, which a mainline kernel does not have --
// "open /dev/shelby-leds/uledw: no such file or directory" for every lamp, forever, on a
// board whose six other lamps were right there. A tree that names the lamps this binary
// knows is a mainline tree; lamps it does not name are lamps this board does not have, and
// applyLEDState already skips a channel with no brightness file.
func gpioLEDs(ledDir string) bool {
	for _, channel := range ledChannels(ledState{}) {
		if _, err := os.Stat(filepath.Join(ledDir, channel.mainline, "brightness")); err == nil { //nolint:gosec // G703: the model's lamp directory, or LEDS
			return true
		}
	}

	return false
}

// ledDimByte is the duty cycle written to stock shelby-led for a dim lamp: 1..100 is a
// percentage there.
const ledDimByte = 6

// ledDimBrightness is the same dim lamp on mainline, where the Wi-Fi lamp is a pwm-leds
// lamp with max-brightness 255 (16 ≈ 6%). Tune both by eye against a stock speaker.
const ledDimBrightness = "16"

func applyLEDState(ledDir string, st ledState) error {
	channels := ledChannels(st)

	errs := make([]error, 0, len(channels))
	if gpioLEDs(ledDir) {
		for _, channel := range channels {
			// 255 is full on a PWM lamp; the LED core clamps it to 1 on a gpio-leds one.
			value := []byte("0")
			if channel.on {
				value = []byte("255")
			}
			// A lamp the tree does not name is a lamp this board does not have, not a
			// failure: the panels differ per model and a missing one must not turn
			// every write into a logged error four times a second.
			err := os.WriteFile(filepath.Join(ledDir, channel.mainline, "brightness"), value, 0o644) //nolint:gosec // G703: the model's lamp directory, or LEDS
			if os.IsNotExist(err) {
				continue
			}

			errs = append(errs, err)
		}
		// wifiDim overwrites the 0 the regular pass just wrote to wifi-white.
		if st.wifiDim {
			errs = append(errs, os.WriteFile(filepath.Join(ledDir, "wifi-white", "brightness"), []byte(ledDimBrightness), 0o644)) //nolint:gosec // G703: as above
		}

		return errors.Join(errs...)
	}

	for _, channel := range channels {
		b := []byte{0x00}
		if channel.on {
			b = []byte{0xff}
		}

		errs = append(errs, os.WriteFile(filepath.Join(ledDir, channel.stock), b, 0o644)) //nolint:gosec // G703: as above
	}
	// wifiDim overwrites the 0x00 the regular pass just wrote to uledw.
	if st.wifiDim {
		errs = append(errs, os.WriteFile(filepath.Join(ledDir, "uledw"), []byte{ledDimByte}, 0o644)) //nolint:gosec // G703: as above
	}

	return errors.Join(errs...)
}

// linkEveryTicks: the LED loop reads the network state every sixth 350 ms tick (~2 s).
const linkEveryTicks = 6

// RunLEDs drives the front LEDs from st until ctx ends. modelDir is the model's lamp
// directory (model.Model.LEDDir). Stock hardware uses
// /dev/shelby-leds/{uledw,uledy,rled3..rled7}; mainline uses gpio-leds. LEDS env
// overrides the device dir for tests.
//
// A model with no LEDs at all (the WLA, whose lamps RunWLALEDs drives) returns early.
func RunLEDs(ctx context.Context, modelDir string, st LampStatus, log *slog.Logger) {
	ledDir := os.Getenv("LEDS")
	if ledDir == "" {
		if modelDir == "" {
			// Bail before the gpio-leds probe. That probe only asks whether the tree
			// exists, so on a lampless model it would happily adopt /sys/class/leds and
			// drive whatever unrelated lamps the kernel exposes there.
			log.Info("leds", "dir", "", "note", "model has no LEDs")

			return
		}

		ledDir = modelDir
		if gpioLEDs("/sys/class/leds") {
			ledDir = "/sys/class/leds"
		}
	}
	// Which tree was chosen decides whether any lamp can light at all, and getting it
	// wrong looks exactly like a dead panel. On mainline a renamed gpio-leds label sends
	// this to the stock /dev path, which does not exist there — so say it out loud once
	// rather than leaving nine silent write failures to be inferred.
	log.Info("leds", "dir", ledDir)

	err := applyLEDState(ledDir, ledState{})
	if err != nil {
		log.Warn("led startup clear", "dir", ledDir, "err", err)
	}

	tick := time.NewTicker(350 * time.Millisecond)
	defer tick.Stop()

	lamps := lampWriter{dir: ledDir, log: log}
	blink := false
	step := 0

	var link Link

	linkRead := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		blink = !blink
		step++

		switch {
		case !st.Up():
			// Still coming up: run the wave instead of the state pattern, which at this
			// point would only be reporting a speaker that has not finished starting.
			lamps.show(bootChase(step))
		case st.Confirming():
			// A request waits for a key press, or a held chord counts down: the
			// source lamps blink together, a look no state of the speaker has.
			lamps.show(ledState{stWhite: blink, btWhite: blink, auxWhite: blink})
		case st.Updating():
			// Firmware is writing to NAND: blink both ST lamps (amber solid, white
			// blinking) so the panel is visibly "busy" and distinct from standby or
			// a source state. Stock shows a similar cycling pattern during an update.
			lamps.show(ledState{stAmber: true, stWhite: blink})
		default:
			// The link moves in seconds, the blink in 350 ms: sampling it every tick (a UDP
			// socket plus two pidfiles) was most of soundtapd's standby CPU on the ST10.
			if step%linkEveryTicks == 1 || !linkRead {
				link, linkRead = ReadLink(st.HaveIP), true
			}

			if next := ledPattern(st.StatusSnapshot(), link, blink); lamps.show(next) {
				// Debug, not Info: every blink of every blinking pattern is a change,
				// so at Info this alone writes a line every 350 ms forever.
				log.Debug("leds", "state", next, "link", link)
			}
		}
	}
}

// lampWriter writes a lamp state when it differs from the last one written.
type lampWriter struct {
	dir  string
	log  *slog.Logger
	last ledState
	have bool
}

// show writes next unless the lamps show it already, and reports whether it wrote.
func (w *lampWriter) show(next ledState) bool {
	if w.have && next == w.last {
		return false
	}

	err := applyLEDState(w.dir, next)
	if err != nil {
		w.log.Warn("led write", "dir", w.dir, "err", err)
	}

	w.last, w.have = next, true

	return true
}
