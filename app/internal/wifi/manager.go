// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package wifi brings up the speaker's Wi-Fi: it joins the saved networks through
// wpa_supplicant's control socket, falls back to the network that last worked, and opens
// the setup access point with hostapd when nothing joins (ADR 0063, ADR 0115).
//
// It runs inside soundtapd. wpa_supplicant, hostapd, udhcpc, udhcpd and dnsd are daemons
// that outlive a soundtapd restart, so a crash of the daemon does not take the speaker off
// the network: the next soundtapd finds them by their pid files and keeps them.
package wifi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
)

// Sys is what the manager does to the box. A test records it.
type Sys interface {
	// Run runs a command to its end and returns its combined output.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// Start starts a command that keeps running, in its own process group, and returns
	// its pid; exited closes when it ends.
	Start(name string, args ...string) (pid int, exited <-chan struct{}, err error)
	// Signal sends sig to pid; 0 asks whether pid lives.
	Signal(pid int, sig syscall.Signal) error
	// HasIPv4 reports whether iface holds an IPv4 address.
	HasIPv4(iface string) bool
}

// Paths is where the manager reads and writes. A test points them into a temp tree.
type Paths struct {
	State         string // /run/soundtap: the markers soundtapd's other parts and the boot read
	Pid           string // /run: the daemons' pid files
	Tmp           string // /tmp: hostapd.conf (docs/hardware/hardware-test.md reads it), udhcpd.conf, dnsd.conf
	CtrlDir       string // wpa_supplicant's ctrl_interface
	ScanCache     string // the setup AP's scan, for the setup page
	SysNet        string // /sys/class/net
	SysPhy        string // /sys/class/ieee80211
	ProcMTD       string
	Dev           string
	Cmdline       string
	Firmware      string // /lib/firmware/ti-connectivity
	FirmwareParam string // the kernel firmware loader's search path parameter
	BootLog       string // the boot trace on /mnt/nv
	LanAllow      string // the production filter's subnet helper
	IPv6Conf      string // /proc/sys/net/ipv6/conf: the setup AP takes no router advertisements
}

// DefaultPaths are the speaker's.
func DefaultPaths() Paths {
	return Paths{
		State:         "/run/soundtap",
		Pid:           "/run",
		Tmp:           "/tmp",
		CtrlDir:       "/var/run/wpa_supplicant",
		ScanCache:     "/run/soundtap-wifi-scan",
		SysNet:        "/sys/class/net",
		SysPhy:        "/sys/class/ieee80211",
		ProcMTD:       "/proc/mtd",
		Dev:           "/dev",
		Cmdline:       "/proc/cmdline",
		Firmware:      "/lib/firmware/ti-connectivity",
		FirmwareParam: "/sys/module/firmware_class/parameters/path",
		BootLog:       "/mnt/nv/soundtap-boot.log",
		LanAllow:      "/opt/soundtap/lan-allow.sh",
		IPv6Conf:      "/proc/sys/net/ipv6/conf",
	}
}

// Timing holds every wait. Tests shorten them.
type Timing struct {
	Iface      time.Duration // for wlan0 to appear
	Join       time.Duration // for an address after wpa_supplicant starts
	Poll       time.Duration // between looks at the join
	Ready      time.Duration // for soundtapd's listener before the AP hands out leases
	Settle     time.Duration // for a daemon started in the background to die if it will
	Scan       time.Duration // one setup scan
	ScanRetry  time.Duration // between setup scans
	ShortPause time.Duration // AP before the next try, router not there yet
	LongPause  time.Duration // AP before the next try otherwise
	Hold       time.Duration // the longest a device on the AP holds the next try off
	Release    time.Duration // between a DHCP release and the renew after a network change
	Watch      time.Duration // between looks at a joined station
	Gone       time.Duration // for a stopped daemon to exit
}

// The speaker's waits (DefaultTiming).
const (
	defaultIfaceWait  = 20 * time.Second
	defaultJoinWait   = 90 * time.Second
	defaultScanWait   = 15 * time.Second
	defaultScanRetry  = 2 * time.Second
	defaultLongPause  = 5 * time.Minute
	defaultHold       = 30 * time.Minute
	defaultWatchEvery = 5 * time.Second
	defaultGoneWait   = 3 * time.Second
)

// DefaultTiming is the speaker's. The join window is 90 s, not 45: the ST20's BCO join
// took 10-50 s (2026-10-27), and a join past the window dropped the speaker onto the setup
// AP for 5 minutes.
func DefaultTiming() Timing {
	return Timing{
		Iface: defaultIfaceWait, Join: defaultJoinWait, Poll: time.Second, Ready: time.Minute,
		Settle: time.Second, Scan: defaultScanWait, ScanRetry: defaultScanRetry,
		ShortPause: time.Minute, LongPause: defaultLongPause, Hold: defaultHold,
		Release: time.Second, Watch: defaultWatchEvery, Gone: defaultGoneWait,
	}
}

// The modes of the files the manager writes: private ones hold keys or configuration,
// public ones are markers and caches other parts read.
const (
	privateFileMode = 0o600
	publicFileMode  = 0o644
	publicDirMode   = 0o755
)

// pidPoll is how often a stopped daemon is looked at until it is gone.
const pidPoll = 20 * time.Millisecond

// errBootContract is wlan0 never appearing: the radio's driver did not probe.
var errBootContract = errors.New("BOOT CONTRACT FAILURE")

// errExitedAtOnce is a daemon gone again before Timing.Settle.
var errExitedAtOnce = errors.New("exited at once")

// Config wires a Manager.
type Config struct {
	Conf     string // /mnt/nv/wifi.conf
	Baked    string // /etc/soundtap-wifi.conf: read when Conf is missing
	STA, AP  string // wlan0, wlan1
	Variant  string // binky, spotty, …; from the kernel command line when empty
	Paths    Paths
	Timing   Timing
	Sys      Sys
	Hostname func() string
	// SetupSSID is the name the setup AP carries: the speaker's published name.
	SetupSSID func() string
	// Ready reports soundtapd's listener up; the AP hands out no lease before.
	Ready func() bool
	// Joined runs after a join gave an address: the clock sync.
	Joined func()
	// Problem reports what keeps Wi-Fi from working, "" once it works.
	Problem func(string)
	Log     *slog.Logger
}

type kind int

const (
	kindBoot    kind = iota // keep what a previous soundtapd left running, else bring up
	kindRestart             // read wifi.conf again and join
	kindSetup               // the setup AP, keeping the saved network
)

// Manager owns the speaker's Wi-Fi for as long as Run runs.
type Manager struct {
	cfg     Config
	log     *slog.Logger
	mu      sync.Mutex
	pending *kind
	wake    chan struct{}

	// Owned by Run's transitions, one at a time.
	reg      Regulatory
	regDone  bool
	variant  string
	tries    int
	lastSSID string
	hasLast  bool
}

// New returns a Manager; Run starts it.
//
//nolint:gocritic // hugeParam: cmd/soundtapd builds the Config by value; a pointer changes the API it uses
func New(cfg Config) *Manager {
	var noPaths Paths
	if cfg.Paths == noPaths {
		cfg.Paths = DefaultPaths()
	}

	var noTiming Timing
	if cfg.Timing == noTiming {
		cfg.Timing = DefaultTiming()
	}

	if cfg.STA == "" {
		cfg.STA = "wlan0"
	}

	if cfg.AP == "" {
		cfg.AP = "wlan1"
	}

	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}

	if cfg.Hostname == nil {
		cfg.Hostname = func() string {
			h, _ := os.Hostname()

			return h
		}
	}

	return &Manager{
		cfg:      cfg,
		log:      cfg.Log,
		mu:       sync.Mutex{},
		pending:  nil,
		wake:     make(chan struct{}, 1),
		reg:      normalise("", ""),
		regDone:  false,
		variant:  "",
		tries:    0,
		lastSSID: "",
		hasLast:  false,
	}
}

// Restart reads wifi.conf again and joins, or opens the setup AP when there is nothing to
// join. A transition still running is cut short: the new one starts from a clean slate.
func (m *Manager) Restart() { m.request(kindRestart) }

// OpenSetup opens the setup AP and keeps the saved network: wifi.conf stays as it is, so a
// power cycle, or nobody on the AP for the retry pause, goes back to that network.
func (m *Manager) OpenSetup() { m.request(kindSetup) }

// Run brings Wi-Fi up and handles requests until ctx ends. The daemons stay when it
// returns: soundtapd stopping does not take the network.
func (m *Manager) Run(ctx context.Context) {
	if m.cfg.Variant != "" {
		m.variant = m.cfg.Variant
	} else {
		m.variant = variant(m.cfg.Paths.Cmdline)
	}

	cancel := context.CancelFunc(func() {})
	done := make(chan struct{})
	close(done)

	start := func(next kind) {
		cancel()
		<-done

		tctx, c := context.WithCancel(ctx)
		cancel, done = c, make(chan struct{})
		d := done

		safego.Go("wifi-transition", func() {
			defer close(d)

			m.transition(tctx, next)
		})
	}
	start(kindBoot)

	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done

			return
		case <-m.wake:
			if next, ok := m.take(); ok {
				start(next)
			}
		}
	}
}

func (m *Manager) request(want kind) {
	m.mu.Lock()
	m.pending = &want
	m.mu.Unlock()

	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) take() (kind, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.pending == nil {
		return 0, false
	}

	next := *m.pending
	m.pending = nil

	return next, true
}

func (m *Manager) transition(ctx context.Context, want kind) {
	if want == kindBoot {
		adopted := m.adopt(ctx)
		if adopted == adoptKept {
			return
		}
		// Kept the setup AP until its retry: join. Nothing kept: bring up as asked.
		if adopted == adoptRetry {
			want = kindRestart
		}
	}
	// Only the first pass opens the setup AP when asked; the passes after it join.
	pass := want
	for ctx.Err() == nil {
		again, err := m.bringUp(ctx, pass)
		pass = kindRestart

		if err != nil && ctx.Err() == nil {
			m.log.Error("wifi", "err", err)
			m.problem(err.Error())
		}

		if !again {
			return
		}
	}
}

func (m *Manager) problem(s string) {
	if m.cfg.Problem != nil {
		m.cfg.Problem(s)
	}
}

type adoption int

const (
	adoptNone  adoption = iota // nothing kept: bring up
	adoptKept                  // kept and supervised until ctx ended
	adoptRetry                 // kept the setup AP until its retry, or a kept station lost wpa_supplicant: bring up
)

// adoptStation keeps a station a previous soundtapd left joined, and supervises it until ctx
// ends (adoptKept) or its wpa_supplicant goes away (adoptRetry). It reports false when there
// is no joined station to keep.
func (m *Manager) adoptStation(ctx context.Context) (adoption, bool) {
	sess, err := m.attach(ctx)
	if err != nil {
		return adoptNone, false
	}

	status, err := sess.status(ctx)
	if err != nil || status["wpa_state"] != "COMPLETED" {
		sess.close()

		return adoptNone, false
	}

	m.log.Info("wifi already joined; keeping it", "ssid", status["ssid"])
	m.lastSSID, m.hasLast = status["ssid"], true
	m.ensureRegulatory(ctx)
	m.problem("")

	if m.watch(ctx, sess, nil) {
		// wpa_supplicant went away: bring it up again.
		sess.close()

		return adoptRetry, true
	}

	return adoptKept, true
}

// adopt keeps what a previous soundtapd left running: a joined station, or the setup AP.
// At boot nothing runs and it returns at once.
func (m *Manager) adopt(ctx context.Context) adoption {
	if m.alive(m.pid("wpa")) && m.cfg.Sys.HasIPv4(m.cfg.STA) {
		if adopted, ok := m.adoptStation(ctx); ok {
			return adopted
		}
	}

	if m.alive(m.pid("hostapd")) && exists(m.state("setup-ap")) && exists(m.state("network-ready")) {
		m.log.Info("setup AP already up; keeping it")
		m.ensureRegulatory(ctx)
		m.problem("")

		if _, saved := m.readConf(); !saved {
			<-ctx.Done()

			return adoptKept
		}

		if m.holdAP(ctx, m.cfg.Timing.LongPause) {
			return adoptRetry
		}

		return adoptKept
	}

	return adoptNone
}

// ensureRegulatory reads the country and region once and links the WiLink's board
// configuration for the region.
func (m *Manager) ensureRegulatory(ctx context.Context) {
	if m.regDone {
		return
	}

	m.regDone = true
	// Read once, so a transition cut short must not cut the read short too.
	rctx := context.WithoutCancel(ctx)
	reg, country, region := readRegulatory(&m.cfg.Paths, func(cfg string) ([]byte, error) {
		return m.runCmd(rctx, "fw_printenv", "-c", cfg)
	})
	m.reg = reg
	m.log.Info("regulatory domain from manufacturing data or environment",
		"country", reg.Country, "region", reg.Region, "read_country", country, "read_region", region)

	err := linkBoardConfig(&m.cfg.Paths, reg.Region)
	if err != nil {
		m.log.Warn("WiLink board configuration left at DF", "err", err)
	}
}

// readConf reads wifi.conf, or the image's baked one when there is none, and reports
// whether it names a network.
func (m *Manager) readConf() (string, bool) {
	path := m.cfg.Conf
	if !exists(path) && m.cfg.Baked != "" {
		path = m.cfg.Baked
	}

	b, err := os.ReadFile(path)

	return path, err == nil && ParseConf(b).SSIDB64 != ""
}

// bringUp is one pass of a transition of kind pass: clean up, then join the saved networks
// or open the setup AP. It returns again for another pass: the fallback to the last network
// that worked, or the setup AP's retry. Otherwise it supervises what it brought up until ctx
// ends.
func (m *Manager) bringUp(ctx context.Context, pass kind) (bool, error) {
	m.ensureRegulatory(ctx)
	remove(m.state("network-ready"), m.state("setup-ap"), m.cfg.Paths.ScanCache)

	if !m.waitIface(ctx) {
		if ctx.Err() != nil {
			return false, nil
		}

		return false, fmt.Errorf(
			"%w: %s never appeared; check the radio's probe in dmesg (wl18xx over SDIO, soundtap_bco on the ST20)",
			errBootContract, m.cfg.STA)
	}

	m.cleanup(ctx)

	confPath, saved := m.readConf()
	// A Wireless Link that has only the setup AP to open needs a real country for it.
	if !saved && m.variant == "binky" {
		err := m.setSetupCountry(ctx)
		if err != nil {
			return false, err
		}
	} else {
		m.setCountry(ctx)
	}

	if !saved {
		return false, m.apOnly(ctx, "no wifi.conf")
	}

	if pass == kindSetup {
		err := m.openAP(ctx, "asked for")
		if err == nil {
			m.problem("")

			return m.holdAP(ctx, m.cfg.Timing.LongPause), nil
		}

		m.log.Warn("setup AP unavailable; back to the saved network", "err", err)
	}

	conf, _ := os.ReadFile(confPath)

	nets, err := networks(confPath, ParseConf(conf))
	if err != nil {
		return false, m.apOnly(ctx, err.Error())
	}

	sess, res, err := m.join(ctx, nets)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil
		}
		// wpa_supplicant did not start or answer, a socket of the previous one still in the
		// way say: there is a saved network, so the setup AP waits for the next try.
		m.log.Warn("wifi join could not start", "err", err)

		return m.apUntilRetry(ctx, err.Error(), m.cfg.Timing.ShortPause), nil
	}
	defer sess.close()

	switch {
	case res.joined:
		m.joined(ctx, sess, confPath, nets[0].rec, &res)

		return m.watch(ctx, sess, nil), nil
	case ctx.Err() != nil:
		return false, nil
	default:
		return m.joinFailed(ctx, sess, confPath, &res)
	}
}

// joinFailed decides what follows a join that gave no address: keep trying without an AP,
// go back to the last network that worked, or open the setup AP until the next try.
func (m *Manager) joinFailed(ctx context.Context, sess *session, confPath string, res *joinResult) (bool, error) {
	good := confPath + ".good"
	_ = os.WriteFile(m.state("wifi-join-failed"), []byte(res.reason+"\n"), publicFileMode)
	m.log.Warn("wifi join failed", "reason", res.reason, "states", strings.Join(res.seen, " "))

	switch {
	case !m.hasAPMode(ctx):
		// No AP mode: a setup AP cannot start, so wpa_supplicant and udhcpc keep trying
		// instead of the network being dropped.
		m.log.Info("wifi join pending; no AP mode, the station keeps trying")

		watchConf, _ := m.readConf()

		return m.watch(ctx, sess, func() {
			// The address came: what a join does next.
			b, _ := os.ReadFile(watchConf)
			m.joined(ctx, sess, watchConf, ParseConf(b), &joinResult{
				joined: false, assoc: -1, took: -1, seen: nil, reason: "",
			})
		}), nil
	case exists(good) && !sameFile(confPath, good) && copyFile(good, confPath) == nil:
		// New settings that do not join, a mistyped password above all: a speaker that was
		// on the network must not need the setup AP to get back.
		m.log.Info("wifi join failed; back to the last network that worked")
		_ = os.WriteFile(m.state("wifi-fallback-reason"), []byte(res.reason+"\n"), publicFileMode)

		return true, nil
	default:
		// Open the setup AP until the next try.
	}

	pause := m.retryPause(res.reason)

	sess.close()

	return m.apUntilRetry(ctx, "wifi join failed", pause), nil
}

// apUntilRetry opens the setup AP until the saved network's next try. An AP that does not
// open is a problem, not the end: the saved network is tried again after the pause, so a
// speaker never stays without both its network and its setup AP.
func (m *Manager) apUntilRetry(ctx context.Context, reason string, pause time.Duration) bool {
	err := m.openAP(ctx, reason)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}

		m.log.Error("wifi", "err", err)
		m.problem(err.Error())
		m.tries++

		return sleep(ctx, pause)
	}

	m.problem("")

	return m.holdAP(ctx, pause)
}

// apOnly opens the setup AP with no saved network to go back to, and keeps it.
func (m *Manager) apOnly(ctx context.Context, reason string) error {
	err := m.openAP(ctx, reason)
	if err != nil {
		return err
	}

	m.problem("")
	<-ctx.Done()

	return nil
}

// networks is wifi.conf first, then every known network not already there, at falling
// priority. A broken known entry is skipped; a broken wifi.conf is an error.
func networks(confPath string, conf Record) ([]network, error) {
	first, err := decode(conf)
	if err != nil {
		return nil, err
	}

	nets := []network{first}

	b, _ := os.ReadFile(confPath + ".known") // none yet: wifi.conf alone
	for _, rec := range ParseKnown(b) {
		if rec.SSIDB64 == conf.SSIDB64 || len(nets) > KnownMax {
			continue
		}

		known, err := decode(rec)
		if err != nil {
			continue
		}

		nets = append(nets, known)
	}

	return nets, nil
}

// waitIface waits for wlan0.
func (m *Manager) waitIface(ctx context.Context) bool {
	dev := filepath.Join(m.cfg.Paths.SysNet, m.cfg.STA)

	deadline := time.Now().Add(m.cfg.Timing.Iface)
	for !exists(dev) {
		if time.Now().After(deadline) || !sleep(ctx, m.cfg.Timing.Poll) {
			return false
		}
	}

	return true
}

// cleanup stops every network daemon and takes the addresses away, so each pass starts
// from nothing.
func (m *Manager) cleanup(ctx context.Context) {
	for _, d := range []string{"wpa", "udhcpc", "hostapd", "udhcpd", "dnsd"} {
		m.stopPidfile(m.pid(d))
	}

	if exists(filepath.Join(m.cfg.Paths.SysNet, m.cfg.AP)) {
		m.runLogged(ctx, "ip", "addr", "flush", "dev", m.cfg.AP)
		m.runLogged(ctx, "iw", "dev", m.cfg.AP, "del")
	}

	m.runLogged(ctx, "ip", "addr", "flush", "dev", m.cfg.STA)
	// A killed udhcpc reports no deconfig, so its subnet leaves the filter here.
	m.lanAllow(ctx, "del", m.cfg.AP)
	m.lanAllow(ctx, "del", m.cfg.STA)
}

// hasAPMode reports whether the phy can host an AP. The ST20's BCO card before its AP
// support could not.
func (m *Manager) hasAPMode(ctx context.Context) bool {
	out, err := m.runCmd(ctx, "iw", "phy")
	if err != nil {
		return false
	}

	for line := range strings.Lines(string(out)) {
		if strings.TrimSpace(line) == "* AP" {
			return true
		}
	}

	return false
}

// --- small helpers ---------------------------------------------------------------

func (m *Manager) pid(daemon string) string {
	return filepath.Join(m.cfg.Paths.Pid, "soundtap-"+daemon+".pid")
}

func (m *Manager) state(name string) string { return filepath.Join(m.cfg.Paths.State, name) }

func (m *Manager) runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := m.cfg.Sys.Run(ctx, name, args...)
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			err = fmt.Errorf("%s: %w: %s", name, err, lastLine(msg))
		} else {
			err = fmt.Errorf("%s: %w", name, err)
		}
	}

	return out, err
}

// runLogged runs a command whose failure is only worth a debug line.
func (m *Manager) runLogged(ctx context.Context, name string, args ...string) {
	_, err := m.runCmd(ctx, name, args...)
	if err != nil {
		m.log.Debug("wifi step failed", "cmd", name+" "+strings.Join(args, " "), "err", err)
	}
}

func (m *Manager) lanAllow(ctx context.Context, args ...string) {
	if m.cfg.Paths.LanAllow == "" {
		return
	}

	m.runLogged(ctx, m.cfg.Paths.LanAllow, args...) // logs its own failure
}

func (m *Manager) linkUp(ctx context.Context, iface string) error {
	_, err := m.runCmd(ctx, "ip", "link", "set", iface, "up")
	if err == nil {
		return nil
	}

	_, err2 := m.runCmd(ctx, "ifconfig", iface, "up")
	if err2 != nil {
		return err
	}

	return nil
}

// startDaemon starts a daemon that does not fork (dnsd, udhcpd), keeps its pid in a pid
// file, and fails when it is gone again after Timing.Settle.
func (m *Manager) startDaemon(ctx context.Context, pidName, name string, args ...string) error {
	pid, exited, err := m.cfg.Sys.Start(name, args...)
	if err != nil {
		// The caller names the daemon; the owner reads the text as the Wi-Fi problem.
		return err //nolint:wrapcheck // the caller wraps it with the daemon's name
	}

	path := m.pid(pidName)
	_ = os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), publicFileMode)

	select {
	case <-exited:
		remove(path)

		return errExitedAtOnce
	case <-time.After(m.cfg.Timing.Settle):
		// A daemon that ends later takes its pid file along, so a reused pid is never
		// signalled as this daemon. A newer daemon's pid file is left alone.
		safego.Go("wifi-pidfile", func() {
			<-exited

			if readPid(path) == pid {
				remove(path)
			}
		})

		return nil
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", name, ctx.Err())
	}
}

func readPid(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return 0
	}

	return pid
}

func (m *Manager) alive(pidfile string) bool {
	pid := readPid(pidfile)

	return pid > 0 && m.cfg.Sys.Signal(pid, 0) == nil
}

// stopPidfile stops the daemon a pid file names and waits a moment for it to go, so the
// next one does not find its socket or interface still taken.
func (m *Manager) stopPidfile(path string) {
	pid := readPid(path)
	remove(path)

	if pid == 0 || m.cfg.Sys.Signal(pid, syscall.SIGTERM) != nil {
		return
	}

	deadline := time.Now().Add(m.cfg.Timing.Gone)
	for m.cfg.Sys.Signal(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(pidPoll)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)

	return err == nil
}

func touch(p string) { _ = os.WriteFile(p, nil, publicFileMode) }

func remove(paths ...string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// sleep waits d, or less when ctx ends; it reports whether the whole wait passed.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}

	return s
}
