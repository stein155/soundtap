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
func RunBootScreen(ctx context.Context, disp *display.Manager, st BootStatus, log *slog.Logger) {
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
		booting := !st.Up() || since < bootScreenMinimum
		state := hw.LinkDown
		if !booting {
			state = st.Link()
		}
		at, hour12 := st.Clock()
		netcard := st.NetCard != nil && st.NetCard()
		frame, key := bootOverlay(booting, !st.Up(), since, st.Update(), netcard, st.Faulted(), state, clockText(at, hour12))
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
		sleep := time.Second
		switch {
		case booting:
			sleep = panelTick
		case state == hw.LinkDown || state == hw.LinkConnecting:
			sleep = offlineDotTick
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
)

// uptime is the time since power-on; tests replace it. ok is false where /proc/uptime
// cannot be read, and the bar then counts from the start of this process.
var uptime = func() (time.Duration, bool) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, false
	}
	secs, err := strconv.ParseFloat(strings.Fields(string(b) + " ")[0], 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}

// bootProgress is the bar's fill 0..1. It follows the uptime, so it never goes back,
// and stays below full until the speaker serves.
func bootProgress(waiting bool, since time.Duration) float64 {
	if !waiting {
		return 1
	}
	up, ok := uptime()
	if !ok {
		up = since
	}
	return min(bootHold, float64(up)/float64(typicalBoot))
}

// bootOverlay picks the screen and a key that changes whenever the screen does. update
// is an install's percentage, -1 without one; netcard says the BCO app is being written;
// faulted says the amp fault is latched.
func bootOverlay(booting, waiting bool, since time.Duration, update int, netcard, faulted bool, link hw.Link, clock string) ([]byte, string) {
	switch {
	case booting:
		p := bootProgress(waiting, since)
		return bootFrame(p), "boot" + strconv.Itoa(int(p*80))
	case update >= 0:
		// Outranks the network: an install drops Wi-Fi, and the one thing to say is
		// to leave the plug in.
		return screen.Notice(Text("Updating"), []string{strconv.Itoa(update) + "%", Text("Do not unplug")}, float64(update)/100).Pix(15),
			"update" + strconv.Itoa(update)
	case netcard:
		// Writing the BCO app takes the network down for minutes. Without this the panel
		// says it is connecting, which reads as a fault, and a power cut would interrupt
		// the write.
		return screen.Notice(Text("Updating"), []string{Text("Network card"), Text("Do not unplug")}, -1).Pix(15), "netcard"
	case faulted:
		// As stock says so on the panel: otherwise a muted speaker looks merely broken.
		return screen.Notice(Text("Amp fault"), []string{Text("No sound")}, -1).Pix(15), "ampfault"
	case link == hw.LinkSetupAP:
		return setupFrame(), "setup"
	case link == hw.LinkConnecting || link == hw.LinkDown:
		// As stock: joining says so; only a link that is not even trying is "no connection".
		title, sub := Text("No connection"), Text("Retrying")
		if link == hw.LinkConnecting {
			title, sub = Text("Connecting…"), ""
		}
		step := int(since / offlineDotTick)
		return screen.Offline(screen.Bar{Time: clock}, title, sub, step).Pix(15),
			"offline" + title + clock + strconv.Itoa(step%3)
	default:
		return nil, ""
	}
}

func bootFrame(progress float64) []byte {
	return screen.Boot(progress).Pix(15)
}

func setupFrame() []byte {
	return screen.Notice(Text("Setup"), []string{Text("Join Wi-Fi"), speakerstate.SetupSSID(), Text("Then open"), speakerstate.SetupAPAddress}, -1).Pix(15)
}

// Clock is the time for the panel's status line: in the town's zone, 12- or 24-hour
// as the clock setting says.
func Clock(set *settings.Store) func() (time.Time, bool) {
	return func() (time.Time, bool) {
		d := set.Get().Display
		return time.Now().In(clockZone(d.Timezone)), d.Hour12
	}
}
