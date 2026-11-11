// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/wpactrl"
)

// session is one wpa_supplicant run as soundtapd configured it: the control connection,
// and the saved record behind each network id.
type session struct {
	conn     *wpactrl.Conn
	records  map[int]Record
	wrongKey bool // wpa_supplicant said a key was wrong (CTRL-EVENT-SSID-TEMP-DISABLED reason=WRONG_KEY)
	lost     bool // the event stream ended: wpa_supplicant went away
}

// askTimeout bounds one request to wpa_supplicant inside a join or a watch, so a supplicant that
// stopped answering cannot stretch a 90 s join tenfold.
const askTimeout = 3 * time.Second

func (s *session) status(ctx context.Context) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	return s.conn.Status(ctx)
}

func (s *session) scanResults(ctx context.Context) ([]wpactrl.BSS, error) {
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	return s.conn.ScanResults(ctx)
}

func (s *session) close() {
	if s != nil && s.conn != nil {
		_ = s.conn.Close()
	}
}

// joinResult is how far a join got.
type joinResult struct {
	joined bool
	assoc  int // seconds to COMPLETED, -1 when never
	took   int // seconds to an address
	seen   []string
	reason string // why it failed: no_address, wrong_password, refused, timeout, not_found
}

// supplicantConf is the configuration wpa_supplicant starts with: only its control socket.
// The networks come over that socket, so no key is ever written to a file.
func supplicantConf(ctrlDir string) string { return "ctrl_interface=" + ctrlDir + "\n" }

// join starts wpa_supplicant with nets and udhcpc on wlan0 and waits for an address.
// An error means wpa_supplicant could not be run or configured at all.
func (m *Manager) join(ctx context.Context, nets []network) (*session, joinResult, error) {
	sta := m.cfg.STA
	// The interface may still be in whatever mode the setup AP left it in. Always normalise
	// the type; the down/up cycle stays out, it was what broke association on mainline.
	if _, err := m.run(ctx, "iw", "dev", sta, "set", "type", "managed"); err != nil {
		m.log.Warn("could not set the station to managed", "iface", sta, "err", err)
	}
	if err := m.linkUp(ctx, sta); err != nil {
		m.log.Warn("could not bring the station up", "iface", sta, "err", err)
	}
	// Stock keeps the radio awake. With power save on, the wl18xx answers only at its DTIM
	// wake: sparse pings averaged 68 ms (up to 246 ms) against 15 ms with it off. mac80211
	// keeps the setting across reassociations. The ST20's BCO has no such knob.
	if _, err := m.run(ctx, "iw", "dev", sta, "set", "power_save", "off"); err != nil {
		m.log.Debug("power save left as it was", "iface", sta, "err", err)
	}
	conf := filepath.Join(m.cfg.Paths.Tmp, "wpa_supplicant.conf")
	if err := os.WriteFile(conf, []byte(supplicantConf(m.cfg.Paths.CtrlDir)), 0o600); err != nil {
		return nil, joinResult{}, fmt.Errorf("wpa_supplicant failed: %w", err)
	}
	if _, err := m.run(ctx, "wpa_supplicant", "-B", "-P", m.pid("wpa"), "-i", sta, "-D", "nl80211", "-c", conf); err != nil {
		return nil, joinResult{}, fmt.Errorf("wpa_supplicant failed: %w", err)
	}
	s, err := m.attach(ctx)
	if err != nil {
		return nil, joinResult{}, fmt.Errorf("wpa_supplicant failed: %w", err)
	}
	if err := m.configure(ctx, s, nets); err != nil {
		s.close()
		return nil, joinResult{}, fmt.Errorf("wpa_supplicant failed: %w", err)
	}
	m.log.Info("wpa_supplicant up", "ssid", nets[0].ssid, "networks", len(nets))
	// A move to another saved network needs a new address (renewLease); the first
	// connection only renews.
	m.lastSSID, m.hasLast = "", false
	m.startDHCP()
	return s, m.waitJoin(ctx, s, nets[0].ssid), nil
}

// attach connects to wpa_supplicant's control socket for wlan0, which appears once it has
// started, and subscribes to its events.
func (m *Manager) attach(ctx context.Context) (*session, error) {
	path := filepath.Join(m.cfg.Paths.CtrlDir, m.cfg.STA)
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := wpactrl.Dial(path)
		if err == nil {
			if err = c.Attach(ctx); err == nil {
				return &session{conn: c, records: map[int]Record{}}, nil
			}
			_ = c.Close()
		}
		if time.Now().After(deadline) || !sleep(ctx, 100*time.Millisecond) {
			return nil, fmt.Errorf("control socket %s: %w", path, err)
		}
	}
}

// configure hands wpa_supplicant the networks: wifi.conf at the highest priority, the
// known ones below in order. The name goes as hex and the key as the 32-byte PSK in hex, so
// no quoting can go wrong and no passphrase reaches the supplicant. A hidden network answers
// only a probe that names it, and a typed SSID may be hidden: every network is probed for.
func (m *Manager) configure(ctx context.Context, s *session, nets []network) error {
	c := s.conn
	if err := c.RemoveAllNetworks(ctx); err != nil {
		return err
	}
	for i, n := range nets {
		id, err := c.AddNetwork(ctx)
		if err != nil {
			return err
		}
		set := [][2]string{
			{"ssid", hex.EncodeToString([]byte(n.ssid))},
			{"scan_ssid", "1"},
			{"priority", strconv.Itoa(KnownMax + 1 - i)},
		}
		if n.keyHex != "" {
			set = append(set, [2]string{"psk", n.keyHex})
		} else {
			set = append(set, [2]string{"key_mgmt", "NONE"})
		}
		for _, kv := range set {
			if err := c.SetNetwork(ctx, id, kv[0], kv[1]); err != nil {
				if i == 0 {
					return err
				}
				m.log.Warn("a saved network was skipped", "err", err)
				break
			}
		}
		s.records[id] = n.rec
		if err := c.EnableNetwork(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// startDHCP starts udhcpc on wlan0 in the background. No -q: udhcpc stays and renews the
// lease; with -q it exited once bound, and when the lease ran out the router could hand the
// address to another host (ADR 0063). -A 3: discovery often starts before the join
// completes, and busybox then waits 20 s before it tries again. -O ntpsrv keeps the lease's
// NTP servers for clock.sh; the hostname lets the router list the speaker by name.
func (m *Manager) startDHCP() {
	args := udhcpcArgs(m.cfg.STA, m.pid("udhcpc"), m.cfg.Hostname())
	if _, _, err := m.cfg.Sys.Start("udhcpc", args...); err != nil {
		m.log.Warn("dhcp not started", "iface", m.cfg.STA, "err", err)
		return
	}
	m.log.Info("dhcp requested", "iface", m.cfg.STA)
}

func udhcpcArgs(iface, pidfile, hostname string) []string {
	return []string{"-i", iface, "-p", pidfile, "-b", "-A", "3", "-O", "ntpsrv", "-x", "hostname:" + hostname}
}

// waitJoin waits Timing.Join for an address and logs each wpa_state change with the scan
// results seen so far: on the ST20 a slow join finished in the same second as the BCO's LAN
// link came up, and this splits it into scan, association and DHCP.
func (m *Manager) waitJoin(ctx context.Context, s *session, ssid string) joinResult {
	res := joinResult{assoc: -1}
	state := ""
	polls := max(int(m.cfg.Timing.Join/m.cfg.Timing.Poll), 1)
	secs := func(i int) int { return int(time.Duration(i) * m.cfg.Timing.Poll / time.Second) }
	for i := range polls {
		if m.cfg.Sys.HasIPv4(m.cfg.STA) {
			res.joined, res.took = true, secs(i)
			return res
		}
		st, err := s.status(ctx)
		if err == nil && st["wpa_state"] != state {
			state = st["wpa_state"]
			res.seen = append(res.seen, state)
			n := 0
			if bss, err := s.scanResults(ctx); err == nil {
				n = len(bss)
			}
			m.log.Info("wpa_state", "state", state, "after_s", secs(i), "scan_results", n)
		}
		if res.assoc < 0 && state == "COMPLETED" {
			res.assoc = secs(i)
			m.log.Info("associated", "after_s", res.assoc)
		}
		if !m.events(ctx, s, m.cfg.Timing.Poll) {
			return res
		}
	}
	if res.joined = m.cfg.Sys.HasIPv4(m.cfg.STA); res.joined {
		res.took = secs(polls)
		return res
	}
	res.reason = m.joinFailure(ctx, s, res.seen, ssid)
	return res
}

// joinFailure says why a join failed from the furthest wpa_state it reached. A wrong WPA
// passphrase stalls in the 4-way handshake while the router repeats its first message, and
// wpa_supplicant also reports it outright.
func (m *Manager) joinFailure(ctx context.Context, s *session, seen []string, ssid string) string {
	has := func(states ...string) bool {
		return slices.ContainsFunc(seen, func(st string) bool { return slices.Contains(states, st) })
	}
	switch {
	case has("COMPLETED"):
		return "no_address"
	case s.wrongKey || has("4WAY_HANDSHAKE", "GROUP_HANDSHAKE"):
		return "wrong_password"
	case has("AUTHENTICATING", "ASSOCIATING", "ASSOCIATED"):
		return "refused"
	}
	if bss, err := s.scanResults(ctx); err == nil {
		for _, b := range bss {
			if b.SSID == ssid {
				return "timeout"
			}
		}
	}
	return "not_found"
}

// events handles wpa_supplicant's events for d. It reports false when ctx ended.
func (m *Manager) events(ctx context.Context, s *session, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		case ev, ok := <-s.conn.Events():
			if !ok {
				if !s.lost {
					s.lost = true
					m.log.Warn("wpa_supplicant's event stream ended")
				}
				// Nothing more will come; wait out the rest of d.
				select {
				case <-ctx.Done():
					return false
				case <-t.C:
					return true
				}
			}
			m.event(ctx, s, ev)
		}
	}
}

func (m *Manager) event(ctx context.Context, s *session, ev wpactrl.Event) {
	switch ev.Name {
	case "CTRL-EVENT-CONNECTED":
		m.renewLease(ctx, s)
	case "CTRL-EVENT-SSID-TEMP-DISABLED":
		if r, _ := ev.Field("reason"); r == "WRONG_KEY" {
			s.wrongKey = true
			m.log.Info("wpa_supplicant reports a wrong key")
		}
	case "CTRL-EVENT-DISCONNECTED":
		m.log.Info("wifi disconnected")
	}
}

// renewLease keeps the address right when wpa_supplicant moves to another saved network on
// its own, when the current one goes away: udhcpc would keep the old network's address
// until its lease ran out. On a connection to another SSID udhcpc drops the address and
// asks again; the same SSID (a router back after a power cut, a roam between access points)
// only renews, so nothing playing is cut off.
func (m *Manager) renewLease(ctx context.Context, s *session) {
	pid := readPid(m.pid("udhcpc"))
	if pid == 0 {
		return // udhcpc not started yet: it asks on its own
	}
	st, err := s.status(ctx)
	if err != nil {
		return
	}
	ssid := st["ssid"]
	if m.hasLast && m.lastSSID != ssid {
		m.log.Info("joined another network; asking for a new address", "ssid", ssid)
		_ = m.cfg.Sys.Signal(pid, syscall.SIGUSR2) // release: the old network's address is no use here
		sleep(ctx, m.cfg.Timing.Release)           // let udhcpc finish the release before it is asked again
	}
	m.lastSSID, m.hasLast = ssid, true
	_ = m.cfg.Sys.Signal(pid, syscall.SIGUSR1) // renew, or a new discover after the release
}

// joined is what follows an address: the markers, the network to the front of the known
// list, wifi.conf.good, the boot trace and the clock.
func (m *Manager) joined(ctx context.Context, s *session, confPath string, conf Record, res joinResult) {
	touch(m.state("network-ready"))
	good := confPath + ".good"
	// New settings that joined; the fallback pass has wifi.conf equal to .good and keeps
	// the notice.
	if !sameFile(confPath, good) {
		remove(m.state("wifi-fallback-reason"))
	}
	remove(m.state("wifi-join-failed"))
	m.tries = 0
	m.problem("")
	m.log.Info("dhcp ok", "iface", m.cfg.STA, "after_s", res.took)
	m.rememberJoined(ctx, s, confPath, conf)
	if !sameFile(confPath, good) {
		if err := copyFile(confPath, good); err != nil {
			m.log.Warn("could not keep the working network", "err", err)
		}
	}
	// The log lives in RAM; the split goes to flash next to the boot's "network up after"
	// mark, so a slow join can still be read after the next reboot.
	seconds := func(n int) string {
		if n < 0 {
			return "?"
		}
		return strconv.Itoa(n)
	}
	if f, err := os.OpenFile(m.cfg.Paths.BootLog, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600); err == nil {
		fmt.Fprintf(f, "%s wifi: associated after %ss, address after %ss\n", time.Now().Format(time.UnixDate), seconds(res.assoc), seconds(res.took))
		_ = f.Close()
	}
	if m.cfg.Joined != nil {
		m.cfg.Joined()
	}
}

// rememberJoined puts the network that joined at the front of wifi.conf.known (the newest
// KnownMax are kept) and, when wpa_supplicant picked another than wifi.conf, makes it
// wifi.conf.
func (m *Manager) rememberJoined(ctx context.Context, s *session, confPath string, conf Record) {
	rec := conf
	if st, err := s.status(ctx); err == nil {
		if id, err := strconv.Atoi(st["id"]); err == nil {
			if r, ok := s.records[id]; ok {
				rec = r
			}
		}
	}
	if err := remember(confPath+".known", rec); err != nil {
		m.log.Warn("could not remember the network", "err", err)
	}
	if rec.SSIDB64 != conf.SSIDB64 {
		if err := WriteConf(confPath, rec); err != nil {
			m.log.Warn("could not keep the joined network", "err", err)
			return
		}
		m.log.Info("joined a saved network instead of wifi.conf")
	}
}

// watch supervises a station until ctx ends: it renews the lease on a network change and,
// for a station still waiting (no AP mode), does what a join does once the address comes.
func (m *Manager) watch(ctx context.Context, s *session, joined bool) {
	confPath, _ := m.readConf()
	for n := 1; ; n++ {
		if !m.events(ctx, s, m.cfg.Timing.Watch) {
			return
		}
		// A datagram socket has no end of stream: a supplicant that went away shows only
		// as a request without an answer.
		if n%6 == 0 {
			pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			if !s.conn.Ping(pctx) && ctx.Err() == nil {
				s.lost = true
				m.log.Warn("wpa_supplicant no longer answers")
			}
			cancel()
		}
		if s.lost {
			// wpa_supplicant went away; the next Restart brings it back.
			<-ctx.Done()
			return
		}
		if !joined && m.cfg.Sys.HasIPv4(m.cfg.STA) {
			joined = true
			b, _ := os.ReadFile(confPath)
			m.joined(ctx, s, confPath, ParseConf(b), joinResult{assoc: -1, took: -1})
		}
	}
}
