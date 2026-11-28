// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"cmp"
	"context"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/wpactrl/wpactrltest"
)

// errExitStatus is a command of the fake box that fails.
var errExitStatus = errors.New("exit status 1")

// fakeNet is a network as the fake wpa_supplicant holds it.
type fakeNet struct {
	ssid     string // decoded
	vars     map[string]string
	enabled  bool
	priority int
}

// supplicant is a fake wpa_supplicant behind the control socket. Where says, per SSID,
// what the air does: "ok" joins, "wrong_key" fails the handshake, "refused" associates and is
// sent away (status 43) again and again, holding wpa_supplicant on it as the ST20's bench showed
// (no lower-priority network is tried meanwhile), anything else (or nothing) is not in range. A
// network whose security is not the one the air has for its SSID (fakeSys.sec) is not in range
// for it either, as wpa_supplicant skips a BSS whose security does not match. A join takes one
// STATUS to scan, then the result.
type supplicant struct {
	mu        sync.Mutex
	sys       *fakeSys
	nets      map[int]*fakeNet
	nextID    int
	steps     int
	state     string
	current   int
	connected bool
	srv       *wpactrltest.Server
	scan      []string // SSIDs in SCAN_RESULTS
}

func (f *supplicant) handle(cmd string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	words := strings.Fields(cmd)
	switch {
	case cmd == "REMOVE_NETWORK all":
		f.nets = map[int]*fakeNet{}

		return "OK\n"
	case len(words) == 2 && words[0] == "REMOVE_NETWORK":
		id, _ := strconv.Atoi(words[1])
		if _, ok := f.nets[id]; ok {
			delete(f.nets, id)

			return "OK\n"
		}

		return "FAIL\n"
	case cmd == "ADD_NETWORK":
		id := f.nextID
		f.nextID++
		f.nets[id] = &fakeNet{ssid: "", vars: map[string]string{}, enabled: false, priority: 0}

		return strconv.Itoa(id) + "\n"
	case len(words) >= 4 && words[0] == "SET_NETWORK":
		// the value is the rest of the line, as wpa_supplicant reads it ("OPEN SHARED")
		return f.setNetwork(append(words[:3:3], strings.Join(words[3:], " ")))
	case len(words) == 2 && words[0] == "ENABLE_NETWORK":
		id, _ := strconv.Atoi(words[1])
		if known, ok := f.nets[id]; ok {
			known.enabled = true

			return "OK\n"
		}

		return "FAIL\n"
	case cmd == "STATUS":
		f.step()

		reply := "wpa_state=" + f.state + "\nmode=station\n"
		if f.state == "COMPLETED" {
			reply += fmt.Sprintf("ssid=%s\nid=%d\n", f.nets[f.current].ssid, f.current)
		}

		return reply
	case cmd == "SCAN_RESULTS":
		reply := "bssid / frequency / signal level / flags / ssid\n"

		var rows strings.Builder

		for i, ssid := range f.scan {
			flags := map[string]string{"open": "[ESS]", "wep": "[WEP][ESS]"}[f.sys.security(ssid)]
			flags = cmp.Or(flags, "[WPA2-PSK-CCMP][ESS]")
			fmt.Fprintf(&rows, "aa:bb:cc:dd:ee:%02x\t2437\t-50\t%s\t%s\n", i, flags, ssid)
		}

		reply += rows.String()

		return reply
	default:
		return ""
	}
}

// setNetwork is SET_NETWORK <id> <variable> <value>.
func (f *supplicant) setNetwork(words []string) string {
	id, _ := strconv.Atoi(words[1])

	known, ok := f.nets[id]
	if !ok || f.sys.refuses(words[2], words[3]) {
		return "FAIL\n"
	}

	known.vars[words[2]] = words[3]
	switch words[2] {
	case "ssid":
		b, err := hex.DecodeString(words[3])
		if err != nil {
			return "FAIL\n"
		}

		known.ssid = string(b)
	case "priority":
		known.priority, _ = strconv.Atoi(words[3])
	default:
		// Kept in vars only.
	}

	return "OK\n"
}

// step moves the join on by one STATUS: scanning first, then the highest-priority enabled
// network that is in range decides.
func (f *supplicant) step() {
	f.steps++
	if f.connected || f.steps < 2 {
		if f.state == "" {
			f.state = "SCANNING"
		}

		return
	}

	ids := make([]int, 0, len(f.nets))
	for id, n := range f.nets {
		if n.enabled {
			ids = append(ids, id)
		}
	}

	slices.SortFunc(ids, func(a, b int) int { return cmp.Compare(f.nets[b].priority, f.nets[a].priority) })

	for _, id := range ids {
		switch f.result(f.nets[id]) {
		case "ok":
			f.state, f.current, f.connected = "COMPLETED", id, true
			go f.srv.Event(fmt.Sprintf(
				"CTRL-EVENT-CONNECTED - Connection to aa:bb:cc:dd:ee:ff completed [id=%d id_str=]", id))

			return
		case "refused":
			f.state = "ASSOCIATING"
			go f.srv.Event("CTRL-EVENT-ASSOC-REJECT bssid=aa:bb:cc:dd:ee:ff status_code=43")

			return
		default:
			// Not this one: the next network down.
		}
	}

	for _, netID := range ids {
		if f.result(f.nets[netID]) == "wrong_key" {
			if f.state != "4WAY_HANDSHAKE" {
				f.state = "4WAY_HANDSHAKE"
				go f.srv.Event(fmt.Sprintf(
					`CTRL-EVENT-SSID-TEMP-DISABLED id=%d ssid=%q auth_failures=1 duration=10 reason=WRONG_KEY`,
					netID, f.nets[netID].ssid))
			} else {
				f.state = "DISCONNECTED"
			}

			return
		}
	}

	f.state = "SCANNING"
}

// result is what joining saved does: a network in range with another key than saved's
// refuses the handshake; one with other security is not there for it.
func (f *supplicant) result(saved *fakeNet) string {
	if sec := f.sys.security(saved.ssid); sec != "" && sec != saved.security() {
		return ""
	}

	verdict, pass, ok := f.sys.air(saved.ssid)
	if ok && verdict == "ok" {
		key, _ := pbkdf2.Key(sha1.New, pass, []byte(saved.ssid), 4096, 32)
		if saved.vars["psk"] != hex.EncodeToString(key) {
			return "wrong_key"
		}
	}

	return verdict
}

// security is how wpa_supplicant was told to join n: "wep" with a WEP key, "open" with
// key_mgmt NONE alone, "psk" otherwise.
func (n *fakeNet) security() string {
	switch {
	case n.vars[keyMgmt] != "NONE":
		return "psk"
	case n.vars["wep_key0"] != "" || n.vars["wep_key1"] != "" || n.vars["wep_key2"] != "" || n.vars["wep_key3"] != "":
		return "wep"
	default:
		return "open"
	}
}

// fakeSys records every command and plays the box: the radio's interfaces, the daemons'
// pid files, wpa_supplicant's control socket and the address DHCP gives.
type fakeSys struct {
	t       *testing.T
	p       Paths
	mu      sync.Mutex
	calls   []string
	signals []string
	alive   map[int]bool
	nextPid int
	sup     *supplicant
	where   map[string]string
	sec     map[string]string // SSID -> the air's security: "open", "wep" or "psk"; absent: any, WPA2 in a scan
	scan    []string
	dhcp    bool // udhcpc runs
	noIP    bool // DHCP never answers
	apMode  bool
	fail    map[string]bool // command prefixes that fail
	iwScan  string
	station string            // iw station dump on wlan1
	keys    map[string]string // SSID -> the passphrase the network takes; absent: any
	env     string            // what fw_printenv prints; "" fails
	servers map[int]*wpactrltest.Server
	exits   map[int]chan struct{} // closed to end a daemon Start started
	// refuse says which SET_NETWORK variable and value this wpa_supplicant build turns down
	// (nil: none), as one built without CONFIG_SAE or CONFIG_WEP does
	refuse func(variable, value string) bool
	// badConf is a line wpa_supplicant does not start with ("": it takes every line)
	badConf string
	// busy is how many wpa_supplicant starts still fail for a reason of the moment (the
	// interface not up yet), whatever the configuration
	busy int
	// powerSave is what `iw dev wlan0 get power_save` prints ("": off, as a set to off leaves it)
	powerSave string
}

func newFakeSys(t *testing.T, paths *Paths) *fakeSys {
	t.Helper()

	return &fakeSys{
		t: t, p: *paths, mu: sync.Mutex{}, calls: nil, signals: nil, alive: map[int]bool{}, nextPid: 1000,
		sup: nil, where: map[string]string{}, sec: map[string]string{}, scan: nil, dhcp: false, noIP: false,
		apMode: true, fail: map[string]bool{}, iwScan: "", station: "", keys: map[string]string{}, env: "",
		servers: map[int]*wpactrltest.Server{}, exits: map[int]chan struct{}{}, refuse: nil, badConf: "", busy: 0,
		powerSave: "",
	}
}

func arg(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}

	return ""
}

func (s *fakeSys) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.TrimSpace(filepath.Base(name) + " " + strings.Join(args, " "))
	s.record(line)

	if s.failing(line) {
		return []byte("refused"), errExitStatus
	}

	switch {
	case name == "wpa_supplicant" && s.busyNow():
		return []byte("nl80211: Could not set interface 'wlan0' UP\nwlan0: Failed to initialize driver interface"),
			errExitStatus
	case name == "wpa_supplicant" && s.rejectsConf(arg(args, "-c")):
		return []byte(fmt.Sprintf("Line 3: unknown global field '%s'.\nLine 3: Invalid configuration line '%s'.\n"+
			"Failed to read or parse configuration '%s'.", s.badConf, s.badConf, arg(args, "-c"))), errExitStatus
	case name == "wpa_supplicant":
		s.mu.Lock()
		sup := &supplicant{
			mu: sync.Mutex{}, sys: s, nets: map[int]*fakeNet{}, nextID: 0, steps: 0, state: "", current: 0,
			connected: false, srv: nil, scan: s.scan,
		}
		s.sup = sup
		s.mu.Unlock()
		sup.mu.Lock()
		srv, err := wpactrltest.Open(filepath.Join(s.p.CtrlDir, arg(args, "-i")), sup.handle)
		sup.srv = srv
		sup.mu.Unlock()

		if err != nil {
			return nil, fmt.Errorf("control socket: %w", err)
		}

		s.t.Cleanup(srv.Close)
		pid := s.spawn(arg(args, "-P"))
		s.mu.Lock()
		s.servers[pid] = srv
		s.mu.Unlock()
	case name == "fw_printenv":
		if s.env == "" {
			return []byte("Cannot read environment"), errExitStatus
		}

		return []byte(s.env), nil
	case name == "hostapd":
		s.spawn(arg(args, "-P"))
	case line == "iw phy":
		if s.apMode {
			return []byte("Wiphy phy0\n\tSupported interface modes:\n\t\t * managed\n\t\t * AP\n"), nil
		}

		return []byte("Wiphy phy0\n\tSupported interface modes:\n\t\t * managed\n"), nil
	case strings.HasSuffix(line, "get power_save"):
		s.mu.Lock()
		defer s.mu.Unlock()

		if s.powerSave == "" {
			return []byte("Power save: off\n"), nil
		}

		return []byte(s.powerSave), nil
	case strings.HasSuffix(line, " scan"):
		return []byte(s.iwScan), nil
	case strings.HasSuffix(line, "station dump"):
		s.mu.Lock()
		defer s.mu.Unlock()

		return []byte(s.station), nil
	case strings.Contains(line, "interface add"):
		_ = os.MkdirAll(filepath.Join(s.p.SysNet, args[3]), 0o755)
	case strings.HasPrefix(line, "iw dev wlan1 del"):
		_ = os.RemoveAll(filepath.Join(s.p.SysNet, "wlan1"))
	default:
		// Every other command just succeeds.
	}

	return nil, nil
}

func (s *fakeSys) Start(name string, args ...string) (int, <-chan struct{}, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	s.record(line)

	exited := make(chan struct{})
	if s.failing(line) {
		close(exited)

		return s.spawn(""), exited, nil
	}

	pid := s.spawn(arg(args, "-p"))

	s.mu.Lock()
	s.exits[pid] = exited
	s.mu.Unlock()

	if name == "udhcpc" {
		s.mu.Lock()
		s.dhcp = true
		s.mu.Unlock()
	}

	return pid, exited, nil
}

func (s *fakeSys) Signal(pid int, sig syscall.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.alive[pid] {
		return syscall.ESRCH
	}

	if sig != 0 {
		s.signals = append(s.signals, fmt.Sprintf("%d %s", pid, sig))
	}

	if sig == syscall.SIGTERM {
		delete(s.alive, pid)

		if srv, ok := s.servers[pid]; ok {
			srv.Close() // wpa_supplicant exits and takes its socket along
			delete(s.servers, pid)
		}
	}

	return nil
}

func (s *fakeSys) HasIPv4(iface string) bool {
	s.mu.Lock()
	sup, ok := s.sup, iface == "wlan0" && s.dhcp && !s.noIP && s.sup != nil
	s.mu.Unlock()

	if !ok {
		return false
	}

	sup.mu.Lock()
	defer sup.mu.Unlock()

	return sup.connected
}

func (s *fakeSys) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.calls...)
}

func (s *fakeSys) Signals() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.signals...)
}

func (s *fakeSys) record(line string) {
	s.mu.Lock()
	s.calls = append(s.calls, line)
	s.mu.Unlock()
}

func (s *fakeSys) failing(line string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for p := range s.fail {
		if strings.HasPrefix(line, p) {
			return true
		}
	}

	return false
}

// refuses reports whether this wpa_supplicant build turns down variable set to value.
func (s *fakeSys) refuses(variable, value string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.refuse != nil && s.refuse(variable, value)
}

// busyNow reports whether this wpa_supplicant start fails for a reason of the moment, and
// counts it.
func (s *fakeSys) busyNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.busy == 0 {
		return false
	}

	s.busy--

	return true
}

// rejectsConf reports whether the configuration file at path has the line wpa_supplicant
// does not start with.
func (s *fakeSys) rejectsConf(path string) bool {
	s.mu.Lock()
	bad := s.badConf
	s.mu.Unlock()

	if bad == "" {
		return false
	}

	b, _ := os.ReadFile(path)

	return slices.Contains(strings.Split(string(b), "\n"), bad)
}

func (s *fakeSys) spawn(pidfile string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextPid++

	s.alive[s.nextPid] = true
	if pidfile != "" {
		_ = os.WriteFile(pidfile, []byte(strconv.Itoa(s.nextPid)+"\n"), 0o644)
	}

	return s.nextPid
}

// air is what the test set for ssid: in range or not, and the passphrase it takes.
func (s *fakeSys) air(ssid string) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pass, hasKey := s.keys[ssid]

	return s.where[ssid], pass, hasKey
}

// security is the air's security for ssid, "" when the test set none.
func (s *fakeSys) security(ssid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.sec[ssid]
}

// setAir changes what the air does for ssid while the manager runs: in range or not, its
// security, and the passphrase it takes ("": any).
func (s *fakeSys) setAir(ssid, verdict, security, pass string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.where[ssid], s.sec[ssid] = verdict, security
	if pass != "" {
		s.keys[ssid] = pass
	}
}

func (s *fakeSys) called(prefix string) bool {
	for _, c := range s.Calls() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}

	return false
}

// rig is a Manager on a fake box in a temp tree.
type rig struct {
	t        *testing.T
	dir      string
	p        Paths
	sys      *fakeSys
	m        *Manager
	conf     string
	problems []string
	joins    int
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// supServer is the running fake wpa_supplicant's control socket, read under the locks its
// start writes them under: a test reads it while the manager's goroutine may start another.
func (s *fakeSys) supServer() *wpactrltest.Server {
	s.mu.Lock()
	sup := s.sup
	s.mu.Unlock()

	sup.mu.Lock()
	defer sup.mu.Unlock()

	return sup.srv
}

func newRig(t *testing.T, variant string) *rig {
	t.Helper()
	dir := wpactrltest.ShortDir(t)

	paths := Paths{
		State: filepath.Join(dir, "st"), Pid: filepath.Join(dir, "pid"), Tmp: filepath.Join(dir, "tmp"),
		CtrlDir: filepath.Join(dir, "c"), ScanCache: filepath.Join(dir, "scan"),
		SysNet: filepath.Join(dir, "net"), SysPhy: filepath.Join(dir, "phy"),
		ProcMTD: filepath.Join(dir, "mtd"), Dev: filepath.Join(dir, "dev"), Cmdline: filepath.Join(dir, "cmdline"),
		Firmware: "/lib/firmware/ti-connectivity", FirmwareParam: filepath.Join(dir, "fwpath"),
		BootLog: filepath.Join(dir, "nv", "soundtap-boot.log"), LanAllow: "lan-allow",
		IPv6Conf: filepath.Join(dir, "ipv6"),
	}
	for _, d := range []string{
		paths.State, paths.Pid, paths.Tmp, paths.CtrlDir, filepath.Join(paths.SysNet, "wlan0"),
		filepath.Join(paths.SysPhy, "phy0"), paths.Dev, filepath.Join(dir, "nv"),
		filepath.Join(paths.IPv6Conf, "wlan1"),
	} {
		err := os.MkdirAll(d, 0o755)
		if err != nil {
			t.Fatal(err)
		}
	}

	bench := &rig{
		t: t, dir: dir, p: paths, sys: nil, m: nil, conf: filepath.Join(dir, "nv", "wifi.conf"),
		problems: nil, joins: 0, mu: sync.Mutex{}, cancel: nil, done: nil,
	}
	bench.sys = newFakeSys(t, &paths)
	bench.m = New(Config{
		Conf: bench.conf, Baked: "", STA: "", AP: "", Variant: variant, Paths: paths, Sys: bench.sys,
		Timing: Timing{
			Iface: 200 * time.Millisecond, Join: 300 * time.Millisecond, Poll: 10 * time.Millisecond,
			Ready: 200 * time.Millisecond, Settle: 5 * time.Millisecond, Scan: time.Second,
			ScanRetry: 5 * time.Millisecond, ShortPause: 30 * time.Millisecond, LongPause: 60 * time.Millisecond,
			Hold: 90 * time.Millisecond, Release: 5 * time.Millisecond, Watch: 10 * time.Millisecond,
			Gone: 10 * time.Millisecond, APWatch: 10 * time.Millisecond,
		},
		Hostname:  func() string { return "soundtap-test" },
		SetupSSID: func() string { return "SoundTap ST10 C3D411" },
		Ready:     func() bool { return true },
		Joined:    func() { bench.mu.Lock(); bench.joins++; bench.mu.Unlock() },
		Problem:   func(s string) { bench.mu.Lock(); bench.problems = append(bench.problems, s); bench.mu.Unlock() },
		Log:       quiet(),
	})

	return bench
}

func (r *rig) save(ssid, psk string) {
	r.t.Helper()

	err := WriteConf(r.conf, NewRecord(ssid, "", psk))
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) start() {
	ctx, cancel := context.WithCancel(context.Background())

	r.cancel, r.done = cancel, make(chan struct{})
	go func() { defer close(r.done); r.m.Run(ctx) }()

	r.t.Cleanup(r.stop)
}

func (r *rig) stop() {
	if r.cancel != nil {
		r.cancel()
		<-r.done
		r.cancel = nil
	}
}

// eventually waits for cond, failing with what after 5 s.
func (r *rig) eventually(what string, cond func() bool) {
	r.t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			r.t.Fatalf("%s did not happen; calls:\n%s", what, strings.Join(r.sys.Calls(), "\n"))
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func (r *rig) exists(name string) bool { return exists(filepath.Join(r.p.State, name)) }

func (*rig) read(path string) string {
	b, _ := os.ReadFile(path)

	return string(b)
}

func (r *rig) lastProblem() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.problems) == 0 {
		return "<none>"
	}

	return r.problems[len(r.problems)-1]
}

func (r *rig) joinCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.joins
}

// end ends the daemon Start started as pid, as a crash would.
func (s *fakeSys) end(pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.alive, pid)

	if exited, ok := s.exits[pid]; ok {
		close(exited)
		delete(s.exits, pid)
	}
}
