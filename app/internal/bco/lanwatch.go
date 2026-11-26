// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/childproc"
)

// LANWatch gets the BCO's LAN link up when it missed the boot. The BCO boots alongside the
// SCM, and when Linux's USB host asks too early it answers -71 four times and Linux gives up
// for good: after a warm SoundTap reboot there was no LAN at all (2026-09-28). A power cycle
// makes the BCO enumerate again (bcoflash -cycle, LAN in 18 s), and the new netdev then needs
// the static address init.d/soundtap gave the old one.
type LANWatch struct {
	Link  func() string // the app's LAN netdev, "" while there is none
	Cycle func() error  // BCO supply off for 1 s, then on
	Apply func(netdev string) error
	Wait  time.Duration // how long a link gets to show up, before and after each cycle
	Tries int
	Poll  time.Duration // how often a missing link is looked for
	// UpPoll is how often a link that is up is looked at, Poll when zero. Looking costs a
	// walk of /sys/class/net, every second for as long as the speaker runs.
	UpPoll time.Duration
	Log    *slog.Logger
	// Radio reports whether the LAN answers over the BCO; nil means never checked. The
	// own BCO app can wedge with its USB link alive and nothing on the air (ST20,
	// 2026-10-23, while it led a multiroom group): the link check alone never noticed.
	Radio      func() bool
	RadioPolls int // UpPoll polls between Radio checks
	RadioMiss  int // consecutive failed checks before a power cycle; doubles per cycle that did not help
	// Evidence, when set, is logged before a radio power cycle: what the BCO and its
	// netdev show while the LAN is dead, so a wedge leaves a trace.
	Evidence func(ctx context.Context) string
	// Held reports a BCO flash in progress: the link is down on purpose and a power cycle
	// would abort the transfer. nil means never held.
	Held func() bool
}

// FlashHold is the file a BCO flash keeps while it runs; the LAN watch does not cycle the
// BCO while it exists. Pausing soundtapd instead is not an option: it is what
// feeds the watchdog, and the speaker resets after 120 s.
const FlashHold = "/run/soundtap/bco-flash"

// flashHoldMax bounds a hold whose owner died without removing it. An XMODEM flash of a
// full app takes about 6 minutes.
const flashHoldMax = 15 * time.Minute

// The modes of /run/soundtap and of the files the watch writes there and in /etc.
const (
	runDirMode  = 0o755
	runFileMode = 0o644
)

// HoldLANWatch creates FlashHold and returns the function that removes it.
func HoldLANWatch() (func(), error) {
	err := os.MkdirAll("/run/soundtap", runDirMode)
	if err != nil {
		return nil, fmt.Errorf("hold the LAN watch: %w", err)
	}

	err = os.WriteFile(FlashHold, []byte(strconv.Itoa(os.Getpid())+"\n"), runFileMode)
	if err != nil {
		return nil, fmt.Errorf("hold the LAN watch: %w", err)
	}

	// A hold that cannot be removed expires after flashHoldMax.
	return func() { _ = os.Remove(FlashHold) }, nil
}

// Flashing reports a BCO flash in progress, for the front panel.
func Flashing() bool { return flashHeld(FlashHold, time.Now()) }

// flashHeld reports whether path is a hold younger than flashHoldMax.
func flashHeld(path string, now time.Time) bool {
	fi, err := os.Stat(path)

	return err == nil && now.Sub(fi.ModTime()) < flashHoldMax
}

// NewLANWatch is LANWatch on this SCM: gpio0_14 for the supply and init.d/soundtap's
// st20-eth0 file ("ADDR/PREFIX GATEWAY DNS") under home, else under /run, for the address.
func NewLANWatch(home string, log *slog.Logger) LANWatch {
	return LANWatch{
		Link:  lanNetdev,
		Cycle: cyclePower,
		Apply: func(n string) error {
			return applyStatic(n, filepath.Join(home, "st20-eth0"), "/run/soundtap/st20-eth0")
		},
		Wait:   lanWait,
		Tries:  lanTries,
		Poll:   time.Second,
		UpPoll: lanUpPoll,
		Log:    log,
		Held:   func() bool { return flashHeld(FlashHold, time.Now()) },
		// A check every 20 s, cycle after 3 misses: a wedged BCO is back within ~2 min.
		Radio:      radioCheck(lanAnswers, lanRxPackets),
		Evidence:   wedgeEvidence,
		RadioPolls: lanRadioPolls,
		RadioMiss:  lanRadioMiss,
	}
}

// NewLANWatch's settings on this SCM. A cycle that does not bring the radio back doubles
// the misses before the next one, up to radioMissMax.
const (
	lanWait         = 45 * time.Second
	lanTries        = 2
	lanUpPoll       = 5 * time.Second
	lanRadioPolls   = 4
	lanRadioMiss    = 3
	radioMissGrowth = 2
	radioMissMax    = 32
)

// Run keeps the link up for as long as ctx lives. The BCO also drops off after the boot: on a
// warm reboot (2026-09-28) it disconnected at 118 s and never enumerated again by itself.
//
//nolint:gocritic // hugeParam: a value receiver, because soundtapd calls Run on the LANWatch NewLANWatch returns
func (w LANWatch) Run(ctx context.Context) { (*lanRun)(&w).run(ctx) }

// lanRun is a LANWatch while it runs; its steps take it by pointer.
type lanRun LANWatch

func (w *lanRun) run(ctx context.Context) {
	miss := max(w.RadioMiss, 1)
	for ctx.Err() == nil {
		if !w.up(ctx) {
			// Out of cycles: take the link only if it comes back by itself, so a dead BCO
			// is not power-cycled forever.
			for ctx.Err() == nil && w.Link() == "" {
				w.sleep(ctx)
			}

			if n := w.Link(); n != "" {
				err := w.Apply(n)
				w.Log.Info("BCO LAN link back by itself", "netdev", n, "err", err)
			}
		}

		if w.watchRadio(ctx, &miss) {
			continue
		}

		if ctx.Err() == nil {
			w.Log.Warn("BCO LAN link lost")
		}
	}
}

// holdOff waits while a flash holds the BCO, so a cycle never lands in the middle of one.
func (w *lanRun) holdOff(ctx context.Context) {
	logged := false
	for w.Held != nil && w.Held() && ctx.Err() == nil {
		if !logged {
			w.Log.Info("BCO flash in progress; not power-cycling the BCO")

			logged = true
		}

		w.sleep(ctx)
	}
}

// watchRadio waits while the link is there, and power-cycles the BCO when Radio fails
// *miss checks in a row. It returns true after such a cycle. A LAN that stays dead (the
// router, not the BCO) doubles *miss each time, so it is not cycled every minute.
func (w *lanRun) watchRadio(ctx context.Context, miss *int) bool {
	failed := 0

	for polls := 1; ctx.Err() == nil && w.Link() != ""; polls++ {
		sleepFor(ctx, cmp.Or(w.UpPoll, w.Poll))

		if w.Radio == nil || polls%max(w.RadioPolls, 1) != 0 {
			continue
		}

		if w.Radio() {
			failed, *miss = 0, max(w.RadioMiss, 1)

			continue
		}

		failed++
		if failed < *miss || w.Held != nil && w.Held() {
			continue
		}

		if w.Evidence != nil {
			w.Log.Warn("BCO wedge evidence", "evidence", w.Evidence(ctx))
		}

		w.Log.Warn("BCO link up but the LAN does not answer, power-cycling the BCO", "checks", failed)

		err := w.Cycle()
		if err != nil {
			w.Log.Error("BCO power cycle failed", "err", err)
		}

		*miss = min(*miss*radioMissGrowth, radioMissMax)

		return true
	}

	return false
}

// up returns true once the link is there and addressed, false after Tries power cycles
// without one.
func (w *lanRun) up(ctx context.Context) bool {
	// Applied even when the link is there: after a warm reboot the BCO enumerates only at
	// ~13 s, after init.d/soundtap already tried to address an eth0 that did not exist yet.
	if n := w.wait(ctx); n != "" {
		err := w.Apply(n)
		w.Log.Info("BCO LAN link present", "netdev", n, "err", err)

		return true
	}

	for try := 1; try <= w.Tries && ctx.Err() == nil; try++ {
		if w.Held != nil && w.Held() {
			w.holdOff(ctx)

			try = 0 // the flash restarted the BCO: start over with a fresh wait

			if n := w.wait(ctx); n != "" {
				err := w.Apply(n)
				w.Log.Info("BCO LAN link back after the flash", "netdev", n, "err", err)

				return true
			}

			continue
		}

		w.Log.Warn("BCO LAN link missing, power-cycling the BCO", "try", try)

		err := w.Cycle()
		if err != nil {
			w.Log.Error("BCO power cycle failed", "err", err)

			return false
		}

		if n := w.wait(ctx); n != "" {
			err := w.Apply(n)
			w.Log.Info("BCO LAN link back", "netdev", n, "err", err)

			return true
		}
	}

	w.Log.Error("BCO LAN link still missing", "cycles", w.Tries)

	return false
}

func (w *lanRun) wait(ctx context.Context) string {
	for end := time.Now().Add(w.Wait); ; {
		if n := w.Link(); n != "" {
			return n
		}

		if time.Now().After(end) || ctx.Err() != nil {
			return ""
		}

		w.sleep(ctx)
	}
}

func (w *lanRun) sleep(ctx context.Context) { sleepFor(ctx, w.Poll) }

// sleepFor waits for d or until ctx ends, whichever is first.
func sleepFor(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// radioCheck passes when ping passes or, failing that, when the LAN netdev received
// frames since the previous check. A ping alone judged the gateway, not the radio: a
// router that drops ICMP, or one whose uplink is down, made a working BCO look wedged
// and power-cycled it every few minutes. A working radio on a live Wi-Fi still
// receives ARP, mDNS and broadcast traffic, and the failed ping's own ARP request is
// answered; a wedged radio receives no frames at all. rx reports false when there is
// no counter to read, which counts as no evidence either way.
func radioCheck(ping func() bool, rxCount func() (uint64, bool)) func() bool {
	var (
		last uint64
		seen bool
	)

	return func() bool {
		count, counted := rxCount()
		grew := counted && seen && count != last
		last, seen = count, counted

		if ping() {
			return true
		}
		// Read again after the ping: its ARP exchange is evidence too.
		if after, countedAfter := rxCount(); countedAfter && counted && after != count {
			last, grew = after, true
		}

		return grew
	}
}

// lanRxPackets is the received-frame counter of the own BCO link's netdev.
func lanRxPackets() (uint64, bool) {
	n := lanNetdev()
	if n == "" {
		return 0, false
	}

	b, err := os.ReadFile("/sys/class/net/" + n + "/statistics/rx_packets")
	if err != nil {
		return 0, false
	}

	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)

	return v, err == nil
}

// pingTimeout bounds lanAnswers' ping, whose own reply wait is 3 s (-W3).
const pingTimeout = 5 * time.Second

// lanAnswers pings the default gateway when the own BCO link carries it. No gateway (setup
// AP, no lease yet, stock BCO) is not a failure: there is nothing to judge.
func lanAnswers() bool {
	n := lanNetdev()
	if n == "" || usbID(n) != ownLink {
		return true
	}

	gateway := defaultGateway("/proc/net/route")
	if gateway == "" {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ping", "-c1", "-W3", gateway)
	childproc.NetTool(cmd) // a raw ICMP socket

	return cmd.Run() == nil
}

// wedgeEvidence is the BCO's UART output over two of its 3 s heartbeats and how the
// LAN netdev's counters moved meanwhile: frames still arriving point at the host side,
// none at the radio. It holds the UART for those seconds, like any other BCO command.
func wedgeEvidence(ctx context.Context) string {
	netdev := lanNetdev()
	before := netdevCounters(netdev)
	uart := "uart busy"
	lctx, cancel := context.WithTimeout(ctx, evidenceLockWait)
	err := lockSerial(lctx)

	cancel()

	if err == nil {
		uart = readUART(ctx, evidenceUARTTime)

		unlockSerial()
	}

	return fmt.Sprintf("netdev %s before [%s] after [%s]; uart: %s", netdev, before, netdevCounters(netdev), uart)
}

// wedgeEvidence's waits: for the UART, and how long it listens (two 3 s heartbeats).
const (
	evidenceLockWait = 2 * time.Second
	evidenceUARTTime = 7 * time.Second
)

func netdevCounters(netdev string) string {
	if netdev == "" {
		return "none"
	}

	var out []string

	for _, c := range []string{"rx_packets", "tx_packets", "rx_errors", "tx_errors", "tx_dropped"} {
		b, _ := os.ReadFile("/sys/class/net/" + netdev + "/statistics/" + c)
		out = append(out, c+"="+strings.TrimSpace(string(b)))
	}

	return strings.Join(out, " ")
}

// readUART's read size and output bound, and its pause when the UART has nothing.
const (
	uartChunk = 256
	uartMax   = 2048
	uartIdle  = 50 * time.Millisecond
)

// readUART collects the BCO UART's non-empty lines for window, at most 2 KB.
func readUART(ctx context.Context, window time.Duration) string {
	port, err := SerialPort(ctx, os.Getenv("SOUNDTAP_BCO_TTY"))
	if err != nil {
		return err.Error()
	}
	defer port.Close()

	var raw []byte

	buf := make([]byte, uartChunk)
	for end := time.Now().Add(window); time.Now().Before(end) && ctx.Err() == nil && len(raw) < uartMax; {
		count, err := port.Read(buf)
		if err != nil {
			break
		}

		if count == 0 {
			time.Sleep(uartIdle)

			continue
		}

		raw = append(raw, buf[:count]...)
	}

	return uartLines(raw)
}

func uartLines(raw []byte) string {
	var lines []string

	for l := range strings.SplitSeq(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}

	return strings.Join(lines, " | ")
}

// An IPv4 address in /proc/net/route: 32 bits, lowest octet first.
const (
	ipv4Bits  = 32
	octetBits = 8
	octet     = 0xff
)

// defaultGateway is the IPv4 gateway of the first default route in a /proc/net/route file.
func defaultGateway(path string) string {
	b, _ := os.ReadFile(path)
	for l := range strings.SplitSeq(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}

		addr, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil || addr == 0 {
			continue
		}

		octets := make([]string, 0, ipv4Bits/octetBits)
		for shift := 0; shift < ipv4Bits; shift += octetBits {
			octets = append(octets, strconv.FormatUint(addr>>shift&octet, 10))
		}

		return strings.Join(octets, ".")
	}

	return ""
}

func lanNetdev() string {
	for _, n := range netdevs() {
		if id := usbID(n); id != "" && id != ":" && id != level2USB {
			return n
		}
	}

	return ""
}

func cyclePower() error {
	power, unmap, err := Power()
	if err != nil {
		return err
	}
	defer unmap()

	err = power(false)
	if err != nil {
		return err
	}

	time.Sleep(time.Second)

	return power(true)
}

// ownLink is the own BCO link app (ADR 0031). Its wlan0/eth0 are addressed by the Wi-Fi
// manager and udhcpc like on the ST10, so the watch only keeps the BCO alive and never sets
// a static address.
const ownLink = "1209:0001"

// dhcpPid is the pidfile of wlan0's udhcpc, as the Wi-Fi manager starts it.
const dhcpPid = "/run/soundtap-udhcpc.pid"

// The st20-eth0 file's fields: "ADDR/PREFIX GATEWAY", then an optional DNS server.
const (
	staticGatewayFields = 2
	staticDNSFields     = 3
)

// applyStatic's errors; each keeps the text it had before it was a sentinel.
var (
	errLinkUp       = errors.New("link up")
	errDefaultRoute = errors.New("default route")
)

// applyStatic mirrors st20_eth0 in init.d/soundtap, for the stock BCO's ethernet gadget.
// The first of files that holds an address wins.
func applyStatic(netdev string, files ...string) error {
	if usbID(netdev) == ownLink {
		// eth0 is the init script's eth0 watcher's (its own udhcpc and pidfile);
		// dhcpPid is wlan0's, so a redhcp("eth0") here killed wlan0's lease keeper.
		return redhcp("wlan0")
	}

	addr, gateway, dns := "192.168.1.101/24", "192.168.1.100", "192.168.1.100"

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		if fs := strings.Fields(string(b)); len(fs) >= staticGatewayFields && strings.Contains(fs[0], "/") {
			addr, gateway, dns = fs[0], fs[1], fs[1]
			if len(fs) >= staticDNSFields {
				dns = fs[2]
			}

			break
		}
	}

	ctx := context.Background()

	out, err := ip(ctx, "link", "set", netdev, "up")
	if err != nil {
		return fmt.Errorf("%w: %s", errLinkUp, strings.TrimSpace(out))
	}

	_, _ = ip(ctx, "addr", "add", addr, "dev", netdev) // "exists" is fine; a real failure fails the route below

	out, err = ip(ctx, "route", "replace", "default", "via", gateway, "dev", netdev)
	if err != nil {
		return fmt.Errorf("%w: %s", errDefaultRoute, strings.TrimSpace(out))
	}

	//nolint:gosec // G703: the path is the constant /etc/resolv.conf; only its content comes from the files
	err = os.WriteFile("/etc/resolv.conf", []byte("nameserver "+dns+"\n"), runFileMode)
	if err != nil {
		return fmt.Errorf("write resolv.conf: %w", err)
	}

	return nil
}

// redhcp asks for a lease again when the BCO came back as a new wlan0 or eth0. udhcpc runs
// without -q so that it renews its lease, which means one may still be running from
// before, bound to the netdev the BCO took away: it would wait for its renewal time before
// asking again. Without an address on n, it is replaced by a fresh one. After a BCO reflash
// on 2026-10-07 the ST20 stayed off the LAN for want of exactly this.
func redhcp(netdev string) error {
	addr, _ := ip(context.Background(), "-4", "addr", "show", "dev", netdev)
	if strings.Contains(addr, "inet ") {
		return nil
	}

	if pid := udhcpcPid(dhcpPid); pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	// No output pipe: busybox -b forks to the background with stdio left open, and a pipe
	// would keep this call waiting on the background udhcpc until it has a lease.
	// -A 3 as the Wi-Fi manager: retry 3 s after a failed discovery, not busybox's 20 s. The
	// hostname, as the manager sends it, so the router lists the speaker by name, and -O ntpsrv so
	// clock.sh gets the lease's NTP servers, as on every other DHCP start.
	// The parent returns once it forks or binds; the bound keeps a wedged one from holding
	// the watcher. Killing it then leaves any forked child alone.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	host, _ := os.Hostname()
	cmd := exec.CommandContext(ctx, "udhcpc",
		"-i", netdev, "-p", dhcpPid, "-b", "-A", "3", "-O", "ntpsrv", "-x", "hostname:"+host)
	childproc.NetTool(cmd)

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("udhcpc on %s: %w", netdev, err)
	}

	return nil
}

// udhcpcPid is the pid in pidfile when that process is still a udhcpc, else 0. A stale
// pidfile can name an unrelated process by now.
func udhcpcPid(pidfile string) int {
	b, _ := os.ReadFile(pidfile)

	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return 0
	}

	comm, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if strings.TrimSpace(string(comm)) != "udhcpc" {
		return 0
	}

	return pid
}
