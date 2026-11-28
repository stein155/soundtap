// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/oled/screen"
)

// BootStatus is what the boot screen shows: whether the speaker serves, its network, an
// install and the amp fault.
type BootStatus struct {
	Up     func() bool
	Link   func() hw.Link
	Update func() int // an install's percentage, -1 when none runs
	// NetCard says the ST20's BCO app is being written: its network card is off on
	// purpose for a few minutes. nil on models without one.
	NetCard func() bool
	Faulted func() bool // the amp fault is latched
	// Network is the saved network's name, which the connecting screen names; nil or "" for
	// none.
	Network func() string
	// Quiet is told whether the boot screen keeps quiet about a network that is not up yet:
	// from power-on through the mark and the grace after it (bootClock.quietAt), and again
	// false when the screen stops. The Wi-Fi lamp follows it (hw.LampStatus.NetQuiet), so the lamp and the
	// panel agree. nil: nobody listens.
	Quiet func(quiet bool)
}

// RunBootScreen owns the ST20 panel before the speaker is usable, and whenever it
// is not on a network. These screens are the only instruction the owner gets at that
// moment, which is why they outrank the clock and now-playing:
//
//   - booting: an empty bar outline, as U-Boot painted it, that fills against the time
//     since power-on and holds just short of full until the speaker serves. Then the bar
//     fills, slides into the stem of the SoundTap note, the mark draws itself and the
//     panel fades out (screen.BootMark).
//   - offline: no network, retrying, with moving dots. Not during the boot, the mark and
//     the grace after it (bootClock.quietAt): bootNetGrace, or bootJoinGrace while the
//     boot's first join is still under way. "Connecting" flashed between the mark and the
//     home screen otherwise.
//   - setup: hostapd is running, so the speaker has no Wi-Fi and is offering its
//     own. Says which network to join and where to go once joined.
func RunBootScreen(ctx context.Context, disp *display.Manager, status BootStatus, log *slog.Logger) {
	if !disp.Available() {
		return
	}

	log.Info("oled boot/setup screen up")

	// The mark's name is the first text of a power-on: the atlas is decoded now, while the
	// bar runs, not in the middle of the mark.
	safego.Go("panel.fonts", screen.LoadFonts)

	wait := time.NewTimer(0)
	defer wait.Stop()

	clock := bootClock{
		started: time.Now(), served: time.Time{}, netcard: time.Time{}, held: 0,
		markLast: time.Time{}, markRun: 0, markEnd: time.Time{}, online: false,
	}
	last := ""

	quiet := quietTeller(status.Quiet)
	defer quiet(false)

	for {
		boot := clock.state(&status, time.Now())
		quiet(boot.quiet)

		frame, fade, key := bootOverlay(boot)
		if frame == nil {
			if last != "" {
				disp.ClearOverlay()

				last = ""
			}
		} else if key != last {
			disp.SetOverlay(display.Content{Frame: frame, Fade: fade})

			last = key
		}

		wait.Reset(bootSleep(boot))

		select {
		case <-ctx.Done():
			return
		case <-wait.C:
		}
	}
}

// quietTeller is tell, called only when the boot screen's quiet changes. A nil tell
// listens to nothing.
func quietTeller(tell func(bool)) func(bool) {
	told, have := false, false

	return func(quiet bool) {
		if tell == nil || (have && quiet == told) {
			return
		}

		tell(quiet)

		told, have = quiet, true
	}
}

// bootClock is when the boot screen's moments happened.
type bootClock struct {
	started time.Time // the boot screen came up
	served  time.Time // the boot bar ended: the mark plays from there
	netcard time.Time // the network card's write began
	held    float64   // the bar's fill when the speaker came to serve
	// markRun is how far the bar's last fill and the mark have played, and markLast when it
	// last moved on: the mark's own clock (markAdvance).
	markRun  time.Duration
	markLast time.Time
	markEnd  time.Time // the mark ended: bootNetGrace and bootJoinGrace count from here
	// online: the network has been up since the boot screen came up. Until then the boot's
	// first join is still under way, and bootJoinGrace applies instead of bootNetGrace.
	online bool
}

// markStepMax is the most the mark moves on between two looks at it. At the panel's frame
// rate, or a slower one, the mark runs on the wall clock; a look that comes late, because
// the speaker was busy starting its services, moves it on by this much only, so the panel
// still shows every part of it instead of jumping past the name to the end.
const markStepMax = 250 * time.Millisecond

// markAdvance moves the mark's clock on to now and is how far the mark has played.
func (c *bootClock) markAdvance(now time.Time) time.Duration {
	if c.markLast.IsZero() {
		c.markLast = now
	}

	c.markRun += min(max(0, now.Sub(c.markLast)), markStepMax)
	c.markLast = now

	return c.markRun
}

// state is the boot screen's state at now.
func (c *bootClock) state(status *BootStatus, now time.Time) *bootState {
	since := now.Sub(c.started)
	booting := !status.Up() || since < bootScreenMinimum

	link := hw.LinkDown
	if !booting {
		link = status.Link()
	}

	if link == hw.LinkUp {
		c.online = true
	}

	writing := status.NetCard != nil && status.NetCard()
	if !writing {
		c.netcard = time.Time{}
	} else if c.netcard.IsZero() {
		c.netcard = now
	}

	boot := &bootState{
		booting: booting,
		waiting: !status.Up(),
		since:   since,
		mark:    -1,
		held:    c.held,
		update:  status.Update(),
		netcard: -1,
		faulted: status.Faulted(),
		link:    link,
		network: "",
		uptime:  readUptime,
		quiet:   true,
	}

	if link == hw.LinkConnecting && status.Network != nil {
		boot.network = status.Network()
	}

	if booting {
		c.held = bootProgress(boot)
	} else if c.served.IsZero() {
		c.served = now
	}

	if !c.served.IsZero() && c.markEnd.IsZero() {
		if played := c.markAdvance(now); played < bootMarkFor {
			boot.mark = played
		} else {
			c.markEnd = now
		}
	}

	boot.quiet = c.quietAt(now)

	if writing {
		boot.netcard = now.Sub(c.netcard)
	}

	return boot
}

// quietAt says whether the boot screen keeps quiet at now about a network that is not up:
// before the mark has ended; within bootNetGrace after it; and, while the boot's first join is
// still under way (the network has not been up since power-on), within bootJoinGrace after it.
func (c *bootClock) quietAt(now time.Time) bool {
	if c.markEnd.IsZero() {
		return true
	}

	since := now.Sub(c.markEnd)

	return since < bootNetGrace || (!c.online && since < bootJoinGrace)
}

// bootSleep is how long the boot screen waits before it looks again. The boot bar, the mark
// and the network card's note move at the frame rate, the offline dots and the setup pages
// slower. Otherwise this loop just watches the link, and link() dials and reads pidfiles:
// once a second is enough for a screen that changes when Wi-Fi does.
func bootSleep(boot *bootState) time.Duration {
	switch {
	case boot.booting || boot.mark >= 0 || boot.netcard >= 0:
		return panelTick
	case boot.link == hw.LinkDown || boot.link == hw.LinkConnecting:
		return offlineTick
	case boot.link == hw.LinkSetupAP:
		return setupTick
	default:
		return time.Second
	}
}

// typicalBoot is how long a speaker takes from power-on until soundtapd serves, as
// measured. The bar fills against it and holds at bootHold until the speaker serves.
// The boot screen stays up at least bootScreenMinimum so the bar always shows.
const (
	typicalBoot       = 55 * time.Second
	bootHold          = 0.95
	bootScreenMinimum = 2800 * time.Millisecond
	// offlineDots is how many dots the offline screen steps through.
	offlineDots = 3
	// bootKeySteps is how finely the boot bar's fill is told apart: a new key, and so a
	// new frame, at most this many times over the whole bar.
	bootKeySteps = 80
	// percent is a whole bar in percent.
	percent = 100
	// bootFill is how long the bar takes from where it held to full once the speaker
	// serves; the mark starts after it.
	bootFill = 300 * time.Millisecond
	// bootMarkFor is the whole of the bar's last fill and the mark, faded out at the end.
	bootMarkFor = bootFill + time.Duration(screen.BootMarkFor*float64(time.Second))
	// frameStep is an animation's frame: a new key, and so a new frame, at most this often.
	frameStep = 50 * time.Millisecond
	// setupPage is how long each page of the setup screen shows, setupPages how many there are,
	// and setupTick how often the screen looks for the next.
	setupPage  = 5 * time.Second
	setupPages = 2
	setupTick  = 500 * time.Millisecond
	// offlineTick is how often the offline screen looks: twice a dot.
	offlineTick = dotStep / 2
	// typicalNetcard is how long the network card's write takes, as a guess to be measured
	// on the bench: its bar fills against it and holds at bootHold, as the boot bar does,
	// since the write reports no progress of its own.
	typicalNetcard = 8 * time.Minute
	// setupName is the name every speaker answers to on the network (internal/mdns).
	setupName = "soundtap.local"
	// bootNetGrace is how long after the mark a network that is not up yet goes unmentioned,
	// on the panel and on the Wi-Fi lamp: the home screen shows meanwhile. Wi-Fi usually
	// joins within a few seconds of the mark, and "connecting" flashed for about 2 s between
	// the mark and the home screen (the owner, 2026-11-28). A network still not up after it
	// gets the connecting or no-connection screen as before.
	bootNetGrace = 6 * time.Second
	// bootJoinGrace is how long after the mark the boot's first join goes unmentioned, while
	// the network has not been up once since power-on. bootNetGrace alone was too short for
	// the ST20: its BCO network card brings wlan0 up seconds after soundtapd serves and the
	// join then takes 10-50 s (wifi.DefaultTiming), so "connecting" still showed for about a
	// second after the 6 s, between a dark panel and the home screen (the owner, 0.1.0-alpha.8,
	// 2026-11-28). A first join that has not succeeded by then gets the connecting or
	// no-connection screen; a join that fails opens the setup AP, whose screen is never held back.
	bootJoinGrace = time.Minute
)

// readUptime is the time since power-on. ok is false where /proc/uptime cannot be read,
// and the bar then counts from the start of this process.
func readUptime() (time.Duration, bool) {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, false
	}

	secs, err := strconv.ParseFloat(strings.Fields(string(raw) + " ")[0], 64)
	if err != nil {
		return 0, false
	}

	return time.Duration(secs * float64(time.Second)), true
}

// bootState is what the boot screen is drawn from.
type bootState struct {
	booting bool          // the speaker does not serve yet, or the screen is up too briefly
	waiting bool          // the speaker does not serve yet: the bar holds short of full
	since   time.Duration // since the boot screen came up
	mark    time.Duration // since the boot bar ended, while the mark plays; -1 otherwise
	held    float64       // the bar's fill when the speaker came to serve
	update  int           // an install's percentage, -1 without one
	netcard time.Duration // since the BCO app's write began; -1 when none runs
	faulted bool          // the amp fault is latched
	link    hw.Link
	network string // the saved network's name, "" for none
	// uptime is the time since power-on (readUptime; tests give their own).
	uptime func() (time.Duration, bool)
	// quiet: the boot, the mark or the grace after it (bootClock.quietAt), when a network
	// that is not up yet goes unmentioned.
	quiet bool
}

// bootProgress is the bar's fill 0..1. It follows the uptime, so it never goes back,
// and stays below full until the speaker serves.
func bootProgress(state *bootState) float64 {
	if !state.waiting {
		return 1
	}

	up, ok := state.uptime()
	if !ok {
		up = state.since
	}

	return min(bootHold, float64(up)/float64(typicalBoot))
}

// bootOverlay picks the screen, how far the panel is faded, and a key that changes whenever
// either does.
func bootOverlay(state *bootState) ([]byte, float64, string) {
	switch {
	case state.booting:
		progress := bootProgress(state)

		return bootFrame(progress), 0, "boot" + strconv.Itoa(int(progress*bootKeySteps))
	case state.mark >= 0:
		frame, fade, key := markFrame(state.mark, state.held)

		return frame, fade, key
	case state.update >= 0:
		// Outranks the network: an install drops Wi-Fi, and the one thing to say is
		// to leave the plug in.
		return screen.Progress(Text("Updating"), state.update, Text("Do not unplug")).Pix(fullLevel), 0,
			"update" + strconv.Itoa(state.update)
	case state.netcard >= 0:
		// Writing the BCO app takes the network down for minutes. Without this the panel
		// says it is connecting, which reads as a fault, and a power cut would interrupt
		// the write. It has no words of the speaker's: its language is often not known yet.
		step := int(state.netcard / frameStep)
		fill := int(min(bootHold, float64(state.netcard)/float64(typicalNetcard)) * percent)

		return screen.Netcard(float64(step)*frameStep.Seconds(), fill).Pix(fullLevel), 0,
			"netcard" + strconv.Itoa(step) + "|" + strconv.Itoa(fill)
	case state.faulted:
		// As stock says so on the panel: otherwise a muted speaker looks merely broken.
		return SayScreen("Amp fault").Pix(fullLevel), 0, "ampfault"
	case state.link == hw.LinkSetupAP:
		page := int(state.since/setupPage) % setupPages

		return setupFrame(page), 0, "setup" + strconv.Itoa(page)
	case state.quiet && (state.link == hw.LinkConnecting || state.link == hw.LinkDown):
		// Just after the mark: the network usually follows within seconds, so the home
		// screen shows rather than a "connecting" that flashes by (bootNetGrace).
		return nil, 0, ""
	case state.link == hw.LinkConnecting || state.link == hw.LinkDown:
		// As stock: joining says so; only a link that is not even trying is "no connection".
		// Joining names the network it joins.
		kind, title, network := screen.IconWifiOff, "No connection", "" //nolint:goconst // English, a key of Text
		if state.link == hw.LinkConnecting {
			kind, title, network = screen.IconWifi, "Connecting…", state.network //nolint:goconst // as above
		}

		dots := waitingDots(int(state.since / dotStep))
		opt := screen.NoticeOpt{IconLevel: 0, Small: false, Dots: dots}

		return screen.Notice(kind, Text(title), network, opt).Pix(fullLevel), 0,
			"offline" + title + network + strconv.Itoa(dots)
	default:
		return nil, 0, ""
	}
}

// markFrame is the boot screen at since after the speaker came to serve: the bar fills
// from held to full over bootFill, then the mark (screen.BootMark) draws itself and fades
// out.
func markFrame(since time.Duration, held float64) ([]byte, float64, string) {
	step := int(since / frameStep)
	shown := time.Duration(step) * frameStep

	if shown < bootFill {
		progress := held + (1-held)*float64(shown)/float64(bootFill)

		return bootFrame(progress), 0, "fill" + strconv.Itoa(step)
	}

	frame, fade := screen.BootMark((shown - bootFill).Seconds())

	return frame.Pix(fullLevel), fade, "mark" + strconv.Itoa(step)
}

func bootFrame(progress float64) []byte {
	return screen.Boot(progress).Pix(fullLevel)
}

// setupFrame is the setup screen's page: which network to join, then what to open.
func setupFrame(page int) []byte {
	if page == 0 {
		return screen.SetupJoin("1 · "+Text("Join Wi-Fi"), speakerstate.SetupSSID()).Pix(fullLevel)
	}

	return screen.SetupOpen("2 · "+Text("Then open"), speakerstate.SetupAPAddress, setupName).Pix(fullLevel)
}
