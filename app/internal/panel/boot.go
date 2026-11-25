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
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/oled/screen"
)

// BootStatus is what the boot screen shows: whether the speaker serves, its network, an
// install, the amp fault and the time.
type BootStatus struct {
	Up     func() bool
	Link   func() hw.Link
	Update func() int // an install's percentage, -1 when none runs
	// NetCard says the ST20's BCO app is being written: its network card is off on
	// purpose for a few minutes. nil on models without one.
	NetCard func() bool
	Faulted func() bool // the amp fault is latched
	Clock   func() (time.Time, bool)
}

// RunBootScreen owns the ST20 panel before the speaker is usable, and whenever it
// is not on a network. These screens are the only instruction the owner gets at that
// moment, which is why they outrank the clock and now-playing:
//
//   - booting: a bar alone, as stock draws it. It fills against the time since power-on
//     and holds just short of full until the speaker serves.
//   - offline: no network, retrying, with the design's moving dots.
//   - setup: hostapd is running, so the speaker has no Wi-Fi and is offering its
//     own. Says which network to join and where to go once joined.
func RunBootScreen(ctx context.Context, disp *display.Manager, status BootStatus, log *slog.Logger) {
	if !disp.Available() {
		return
	}

	log.Info("oled boot/setup screen up")

	wait := time.NewTimer(0)
	defer wait.Stop()

	started := time.Now()
	last := ""

	for {
		since := time.Since(started)
		booting := !status.Up() || since < bootScreenMinimum

		state := hw.LinkDown
		if !booting {
			state = status.Link()
		}

		now, hour12 := status.Clock()
		netcard := status.NetCard != nil && status.NetCard()

		frame, key := bootOverlay(&bootState{
			booting: booting,
			waiting: !status.Up(),
			since:   since,
			update:  status.Update(),
			netcard: netcard,
			faulted: status.Faulted(),
			link:    state,
			clock:   clockText(now, hour12),
			uptime:  readUptime,
		})
		if frame == nil {
			if last != "" {
				disp.ClearOverlay()

				last = ""
			}
		} else if key != last {
			disp.SetOverlay(display.Content{Frame: frame})

			last = key
		}
		// The boot bar and the offline dots move. Otherwise this loop just watches the
		// link, and link() dials and reads pidfiles: once a second is enough for a screen
		// that changes when Wi-Fi does.
		var sleep time.Duration

		switch {
		case booting:
			sleep = panelTick
		case state == hw.LinkDown || state == hw.LinkConnecting:
			sleep = offlineDotTick
		default:
			sleep = time.Second
		}

		wait.Reset(sleep)

		select {
		case <-ctx.Done():
			return
		case <-wait.C:
		}
	}
}

// typicalBoot is how long a speaker takes from power-on until soundtapd serves, as
// measured. The bar fills against it and holds at bootHold until the speaker serves.
// The boot screen stays up at least bootScreenMinimum so the bar always shows.
const (
	typicalBoot       = 55 * time.Second
	bootHold          = 0.95
	bootScreenMinimum = 2800 * time.Millisecond
	offlineDotTick    = 300 * time.Millisecond
	// offlineDots is how many dots the offline screen steps through.
	offlineDots = 3
	// bootKeySteps is how finely the boot bar's fill is told apart: a new key, and so a
	// new frame, at most this many times over the whole bar.
	bootKeySteps = 80
	// percent turns an install's percentage into the notice bar's 0..1.
	percent = 100
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
	update  int           // an install's percentage, -1 without one
	netcard bool          // the BCO app is being written
	faulted bool          // the amp fault is latched
	link    hw.Link
	clock   string // the time for the status bar
	// uptime is the time since power-on (readUptime; tests give their own).
	uptime func() (time.Duration, bool)
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

// bootOverlay picks the screen and a key that changes whenever the screen does.
func bootOverlay(state *bootState) ([]byte, string) {
	switch {
	case state.booting:
		progress := bootProgress(state)

		return bootFrame(progress), "boot" + strconv.Itoa(int(progress*bootKeySteps))
	case state.update >= 0:
		// Outranks the network: an install drops Wi-Fi, and the one thing to say is
		// to leave the plug in.
		lines := []string{strconv.Itoa(state.update) + "%", Text("Do not unplug")}

		return screen.Notice(Text("Updating"), lines, float64(state.update)/percent).Pix(fullLevel),
			"update" + strconv.Itoa(state.update)
	case state.netcard:
		// Writing the BCO app takes the network down for minutes. Without this the panel
		// says it is connecting, which reads as a fault, and a power cut would interrupt
		// the write.
		lines := []string{Text("Network card"), Text("Do not unplug")}

		return screen.Notice(Text("Updating"), lines, -1).Pix(fullLevel), "netcard"
	case state.faulted:
		// As stock says so on the panel: otherwise a muted speaker looks merely broken.
		return screen.Notice(Text("Amp fault"), []string{Text("No sound")}, -1).Pix(fullLevel), "ampfault"
	case state.link == hw.LinkSetupAP:
		return setupFrame(), "setup"
	case state.link == hw.LinkConnecting || state.link == hw.LinkDown:
		// As stock: joining says so; only a link that is not even trying is "no connection".
		title, sub := Text("No connection"), Text("Retrying")
		if state.link == hw.LinkConnecting {
			title, sub = Text("Connecting…"), ""
		}

		step := int(state.since / offlineDotTick)
		bar := screen.Bar{Source: "", Room: "", Time: state.clock, State: "", Mark: "", Battery: 0, Charging: false}

		return screen.Offline(bar, title, sub, step).Pix(fullLevel),
			"offline" + title + state.clock + strconv.Itoa(step%offlineDots)
	default:
		return nil, ""
	}
}

func bootFrame(progress float64) []byte {
	return screen.Boot(progress).Pix(fullLevel)
}

func setupFrame() []byte {
	lines := []string{Text("Join Wi-Fi"), speakerstate.SetupSSID(), Text("Then open"), speakerstate.SetupAPAddress}

	return screen.Notice(Text("Setup"), lines, -1).Pix(fullLevel)
}

// Clock is the time for the panel's status line: in the town's zone, 12- or 24-hour
// as the clock setting says.
func Clock(set *settings.Store) func() (time.Time, bool) {
	return func() (time.Time, bool) {
		cfg := set.Get().Display

		return time.Now().In(clockZone(cfg.Timezone)), cfg.Hour12
	}
}
