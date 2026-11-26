// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestLANWatchCyclesOnlyWhenTheLinkIsMissing(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		upAfter     int // cycles before the link shows up; -1 = never
		wantCycles  int
		wantApplied bool
	}{
		{"up at boot", 0, 0, true},
		{"up after one cycle", 1, 1, true},
		{"never", -1, 2, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cycles, applied := 0, ""

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			watch := LANWatch{
				Link: func() string {
					if test.upAfter >= 0 && cycles >= test.upAfter {
						return "eth0"
					}

					if !test.wantApplied && cycles == test.wantCycles {
						cancel() // out of cycles: stop deterministically, no wall-clock timer
					}

					return ""
				},
				Cycle: func() error {
					cycles++

					return nil
				},
				Apply: func(n string) error {
					applied = n

					cancel()

					return nil
				},
				Wait:       time.Millisecond,
				Tries:      2,
				Poll:       time.Millisecond,
				UpPoll:     0,
				Log:        slog.New(slog.DiscardHandler),
				Radio:      nil,
				RadioPolls: 0,
				RadioMiss:  0,
				Evidence:   nil,
				Held:       nil,
			}
			watch.Run(ctx)

			if cycles != test.wantCycles || (applied == "eth0") != test.wantApplied {
				t.Fatalf("cycles=%d applied=%q, want %d cycles, applied=%v",
					cycles, applied, test.wantCycles, test.wantApplied)
			}
		})
	}
}

// The warm-reboot case: the link is there, is addressed, drops out later and needs a cycle.
func TestLANWatchCyclesWhenTheLinkIsLostLater(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	polls, cycles, applies := 0, 0, 0
	watch := LANWatch{
		Link: func() string {
			polls++
			if polls > 3 && cycles == 0 {
				return "" // lost after a few looks
			}

			return "eth0"
		},
		Cycle: func() error {
			cycles++

			return nil
		},
		Apply: func(string) error {
			applies++
			if applies == 2 {
				cancel()
			}

			return nil
		},
		Wait:       time.Millisecond,
		Tries:      2,
		Poll:       time.Millisecond,
		UpPoll:     0,
		Log:        slog.New(slog.DiscardHandler),
		Radio:      nil,
		RadioPolls: 0,
		RadioMiss:  0,
		Evidence:   nil,
		Held:       nil,
	}
	watch.Run(ctx)

	if cycles != 1 || applies != 2 {
		t.Fatalf("cycles=%d applies=%d, want 1 cycle and 2 applies", cycles, applies)
	}
}

func TestUdhcpcPidOnlyNamesARunningUdhcpc(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("udhcpcPid reads /proc")
	}

	dir := t.TempDir()
	// A sleep renamed to udhcpc stands in for the real one: /proc/<pid>/comm is the file name.
	bin := dir + "/udhcpc"

	sleep, err := os.ReadFile("/bin/sleep")
	if err != nil {
		t.Skip("no /bin/sleep")
	}

	err = os.WriteFile(bin, sleep, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), bin, "30")

	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = cmd.Process.Kill() }() // t.Context's end kills it too

	write := func(name, body string) string {
		werr := os.WriteFile(dir+"/"+name, []byte(body), 0o644)
		if werr != nil {
			t.Fatal(werr)
		}

		return dir + "/" + name
	}
	for _, test := range []struct {
		pidfile string
		want    int
	}{
		{write("live", strconv.Itoa(cmd.Process.Pid)+"\n"), cmd.Process.Pid},
		{write("other", strconv.Itoa(os.Getpid())+"\n"), 0}, // reused pid: not ours to kill
		{write("dead", "999999999\n"), 0},
		{write("empty", ""), 0},
		{write("init", "1\n"), 0},
		{dir + "/none", 0},
	} {
		if got := udhcpcPid(test.pidfile); got != test.want {
			t.Errorf("udhcpcPid(%s) = %d, want %d", test.pidfile, got, test.want)
		}
	}
}

// While a BCO flash holds the watch, a missing link is expected and must not be answered
// with a power cycle: that aborted the XMODEM transfer.
func TestLANWatchDoesNotCycleDuringAFlash(t *testing.T) {
	t.Parallel()

	cycles, held := 0, 20 // the flash holds for 20 polls; the link returns after it
	link := ""
	watch := LANWatch{
		Link: func() string { return link },
		Cycle: func() error {
			cycles++

			return nil
		},
		Apply:      func(string) error { return nil },
		Wait:       5 * time.Millisecond,
		Tries:      2,
		Poll:       time.Millisecond,
		UpPoll:     0,
		Log:        slog.New(slog.DiscardHandler),
		Radio:      nil,
		RadioPolls: 0,
		RadioMiss:  0,
		Evidence:   nil,
		Held: func() bool {
			held--
			if held <= 0 {
				link = "eth0"
			}

			return held > 0
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if !(*lanRun)(&watch).up(ctx) {
		t.Fatal("link not taken after the flash ended")
	}

	if cycles != 0 {
		t.Fatalf("BCO power-cycled %d times during a flash", cycles)
	}
}

func TestFlashHoldExpires(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/bco-flash"

	now := time.Now()
	if flashHeld(path, now) {
		t.Fatal("held without a hold file")
	}

	err := os.WriteFile(path, nil, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	if !flashHeld(path, now) {
		t.Fatal("fresh hold not seen")
	}

	if flashHeld(path, now.Add(flashHoldMax+time.Minute)) {
		t.Fatal("a hold left by a dead flash never expires")
	}
}

// The 2026-10-23 wedge: the USB link stays, the LAN is gone. Three failed checks cycle the
// BCO; a LAN that stays dead after that needs twice as many before the next cycle.
func TestLANWatchCyclesAWedgedRadio(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	checks, cycles, evidence := 0, 0, 0

	var cycledAt []int

	watch := LANWatch{
		Link: func() string { return "wlan0" },
		Cycle: func() error {
			cycles++

			cycledAt = append(cycledAt, checks)

			return nil
		},
		Evidence: func(context.Context) string {
			evidence = cycles + 1

			return ""
		},
		Apply: func(string) error {
			if cycles == 2 {
				cancel()
			}

			return nil
		},
		Radio: func() bool {
			checks++

			return false
		},
		RadioPolls: 2,
		RadioMiss:  3,
		Wait:       time.Millisecond,
		Tries:      2,
		Poll:       time.Millisecond,
		UpPoll:     0,
		Log:        slog.New(slog.DiscardHandler),
		Held:       nil,
	}
	watch.Run(ctx)

	if len(cycledAt) != 2 || cycledAt[0] != 3 || cycledAt[1] != 9 {
		t.Fatalf("cycled after checks %v, want [3 9]", cycledAt)
	}

	if evidence != 2 {
		t.Fatalf("evidence taken before cycle %d, want it before each cycle", evidence)
	}
}

func TestDefaultGateway(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/route"

	route := "Iface\tDestination\tGateway \tFlags\n" +
		"usb0\t0007A8C0\t00000000\t0001\n" +
		"wlan0\t00000000\t010200C0\t0003\n"

	err := os.WriteFile(path, []byte(route), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	if got := defaultGateway(path); got != "192.0.2.1" {
		t.Fatalf("gateway = %q", got)
	}

	if got := defaultGateway(path + ".missing"); got != "" {
		t.Fatalf("missing file gateway = %q", got)
	}
}

func TestUARTLinesDropsBlanksAndJoins(t *testing.T) {
	t.Parallel()

	got := uartLines([]byte("\r\nsup nfree/av0/av3/leak 00000000\r\n\r\nsup core/rx 00120034\r\n"))
	if want := "sup nfree/av0/av3/leak 00000000 | sup core/rx 00120034"; got != want {
		t.Fatalf("uartLines = %q, want %q", got, want)
	}
}

// A gateway that drops ICMP, or a router whose uplink is down, fails every ping while the
// radio keeps receiving: that is no reason to power-cycle the BCO. A wedged BCO receives
// nothing, and its failed pings are what count.
func TestRadioCheckJudgesTheRadioNotTheGateway(t *testing.T) {
	t.Parallel()

	var received uint64

	rxOK := true
	counter := func() (uint64, bool) { return received, rxOK }
	noPing := func() bool { return false }

	icmpFiltered := radioCheck(noPing, func() (uint64, bool) {
		received += 7

		return received, true
	})
	for i := range 5 {
		if !icmpFiltered() && i > 0 {
			t.Fatalf("check %d: a receiving radio behind an ICMP-dropping gateway failed", i)
		}
	}

	received = 100

	wedged := radioCheck(noPing, counter)
	for i := range 5 {
		if wedged() {
			t.Fatalf("check %d: a radio that receives nothing passed", i)
		}
	}

	pingSendsARP := radioCheck(func() bool {
		received++

		return false
	}, counter)
	if !pingSendsARP() {
		t.Fatal("the failed ping's answered ARP is evidence of a working radio")
	}

	rxOK = false

	if !radioCheck(func() bool { return true }, counter)() {
		t.Fatal("a ping that answers passes without counters")
	}

	if radioCheck(noPing, counter)() {
		t.Fatal("no counter and no ping is no evidence of a working radio")
	}
}
