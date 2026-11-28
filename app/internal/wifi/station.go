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
	"strings"
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
	security bool // the router refused our security (status 31, 43, 45 or 72): none this station can use
	lost     bool // the event stream ended: wpa_supplicant went away
}

// askTimeout bounds one request to wpa_supplicant inside a join or a watch, so a supplicant that
// stopped answering cannot stretch a 90 s join tenfold.
const askTimeout = 3 * time.Second

// The station's waits.
const (
	attachWait  = 5 * time.Second        // for wpa_supplicant's control socket to appear
	attachRetry = 100 * time.Millisecond // between tries at it
	pingTimeout = 2 * time.Second        // for wpa_supplicant to answer a watch's ping
	pingEvery   = 6                      // a watch pings every this many looks
	pingMisses  = 3                      // pings in a row without an answer: wpa_supplicant is gone
)

func (s *session) status(ctx context.Context) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()

	status, err := s.conn.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("wpa_supplicant status: %w", err)
	}

	return status, nil
}

func (s *session) scanResults(ctx context.Context) ([]wpactrl.BSS, error) {
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()

	results, err := s.conn.ScanResults(ctx)
	if err != nil {
		return nil, fmt.Errorf("wpa_supplicant scan results: %w", err)
	}

	return results, nil
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

// supplicantConf is the configuration wpa_supplicant starts with: its control socket, and
// management frame protection where the radio's driver can do it (pmf=1 is turned off for a
// driver without BIP, which the WL1835's is), with SAE only where PMF can then be used
// (sae_check_mfp: a WPA2/WPA3 router joins over PSK instead), ADR 0136. The networks come over
// the socket, so no key is ever written to a file.
func supplicantConf(ctrlDir string) string {
	return "ctrl_interface=" + ctrlDir + "\npmf=1\nsae_check_mfp=1\n"
}

// The settings a wpa_supplicant build may not know, each refused at most once per run
// (Manager.refused): a join goes on without them and never fails for want of one.
const (
	refusedGlobal = "pmf=1 sae_check_mfp=1"
	refusedSAE    = "SAE"
	refusedSHA256 = "WPA-PSK-SHA256"
	refusedWEP    = "WEP"
)

// refuse notes that this wpa_supplicant turned setting down, and logs it the first time.
func (m *Manager) refuse(setting, means string) {
	if m.refused[setting] {
		return
	}

	m.refused[setting] = true
	m.log.Warn("wpa_supplicant refused an optional setting", "setting", setting, "means", means)
}

// startSupplicant starts wpa_supplicant on the station with supplicantConf. pmf and
// sae_check_mfp are optional: when wpa_supplicant does not start with them, it is started
// with the control socket alone. Only a refusal whose output names one of them (wpa_supplicant
// prints "Line 2: unknown global field 'pmf=1'." for a build that does not know it) is taken
// as this build's answer and kept for the run; any other failure (the interface busy, the
// ST20's network card not ready yet) leaves the next start to try the full configuration
// again, so a WPA3 network is never given up for a passing error.
func (m *Manager) startSupplicant(ctx context.Context) error {
	conf := filepath.Join(m.cfg.Paths.Tmp, "wpa_supplicant.conf")
	bare := "ctrl_interface=" + m.cfg.Paths.CtrlDir + "\n"

	run := func(body string) ([]byte, error) {
		err := os.WriteFile(conf, []byte(body), privateFileMode)
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", conf, err)
		}

		return m.runCmd(ctx, "wpa_supplicant", "-B", "-P", m.pid("wpa"), "-i", m.cfg.STA, "-D", "nl80211", "-c", conf)
	}

	if m.refused[refusedGlobal] {
		_, err := run(bare)

		return err
	}

	out, err := run(supplicantConf(m.cfg.Paths.CtrlDir))
	if err == nil {
		return nil
	}

	_, bareErr := run(bare)
	if bareErr != nil {
		return err
	}

	if !refusesGlobal(out) {
		m.log.Warn("wpa_supplicant started only without pmf=1 and sae_check_mfp=1 this time", "err", err)

		return nil
	}

	m.log.Warn("wpa_supplicant did not start with pmf=1 and sae_check_mfp=1", "err", err)
	m.refuse(refusedGlobal, "no management frame protection: WPA2 networks join without it")

	return nil
}

// refusesGlobal reports whether wpa_supplicant's output names one of the optional global
// settings supplicantConf adds, as its configuration parser does for a line it turns down.
func refusesGlobal(out []byte) bool {
	text := string(out)

	return strings.Contains(text, "pmf") || strings.Contains(text, "sae_check_mfp")
}

// join starts wpa_supplicant with nets and udhcpc on wlan0 and waits for an address.
// An error means wpa_supplicant could not be run or configured at all.
func (m *Manager) join(ctx context.Context, nets []network) (*session, joinResult, error) {
	var none joinResult

	sta := m.cfg.STA
	// The interface may still be in whatever mode the setup AP left it in. Always normalise
	// the type; the down/up cycle stays out, it was what broke association on mainline.
	_, err := m.runCmd(ctx, "iw", "dev", sta, "set", "type", "managed")
	if err != nil {
		m.log.Warn("could not set the station to managed", "iface", sta, "err", err)
	}

	err = m.linkUp(ctx, sta)
	if err != nil {
		m.log.Warn("could not bring the station up", "iface", sta, "err", err)
	}
	// Stock keeps the radio awake. With power save on, the wl18xx answers only at its DTIM
	// wake: sparse pings averaged 68 ms (up to 246 ms) against 15 ms with it off. mac80211
	// keeps the setting across reassociations. The ST20's BCO has no such knob.
	_, err = m.runCmd(ctx, "iw", "dev", sta, "set", "power_save", "off")
	if err != nil {
		m.log.Debug("power save left as it was", "iface", sta, "err", err)
	}

	err = m.startSupplicant(ctx)
	if err != nil {
		return nil, none, fmt.Errorf("wpa_supplicant failed: %w", err)
	}

	sess, err := m.attach(ctx)
	if err != nil {
		return nil, none, fmt.Errorf("wpa_supplicant failed: %w", err)
	}

	err = m.configure(ctx, sess, nets)
	if err != nil {
		sess.close()

		return nil, none, fmt.Errorf("wpa_supplicant failed: %w", err)
	}

	m.log.Info("wpa_supplicant up", "ssid", nets[0].ssid, "networks", len(nets))
	// A move to another saved network needs a new address (renewLease); the first
	// connection only renews.
	m.lastSSID, m.hasLast = "", false
	m.startDHCP()

	return sess, m.waitJoin(ctx, sess, nets[0].ssid), nil
}

// attach connects to wpa_supplicant's control socket for wlan0, which appears once it has
// started, and subscribes to its events.
func (m *Manager) attach(ctx context.Context) (*session, error) {
	path := filepath.Join(m.cfg.Paths.CtrlDir, m.cfg.STA)
	deadline := time.Now().Add(attachWait)

	for {
		conn, err := wpactrl.Dial(path)
		if err == nil {
			err = conn.Attach(ctx)
			if err == nil {
				return &session{
					conn: conn, records: map[int]Record{}, wrongKey: false, security: false, lost: false,
				}, nil
			}

			_ = conn.Close()
		}

		if time.Now().After(deadline) || !sleep(ctx, attachRetry) {
			return nil, fmt.Errorf("control socket %s: %w", path, err)
		}
	}
}

// keyMgmt is wpa_supplicant's network variable for the authentication suites.
const keyMgmt = "key_mgmt"

// The authentication suites a password network offers, most capable first (ADR 0136):
// WPA/WPA2 with PSK-SHA256 and WPA3's SAE, then without SAE, then with SAE but without
// PSK-SHA256, then plain WPA-PSK. A wpa_supplicant built without SAE (or without PSK-SHA256)
// refuses the first; the network then joins over the next one it takes.
const (
	mgmtPSKSAE    = "WPA-PSK WPA-PSK-SHA256 SAE"
	mgmtPSKSHA256 = "WPA-PSK WPA-PSK-SHA256"
	mgmtPSKOnly   = "WPA-PSK SAE"
	mgmtPSK       = "WPA-PSK"
)

// offers reports whether a password network still tries mgmt, given what this
// wpa_supplicant refused so far. SAE needs the password itself, which a raw 64-digit key does
// not give; WPA-PSK with SAE alone is only worth a try once PSK-SHA256 is known to be missing.
func (m *Manager) offers(saved *network, mgmt string) bool {
	switch mgmt {
	case mgmtPSKSAE:
		return saved.passphrase != "" && !m.refused[refusedSAE] && !m.refused[refusedSHA256]
	case mgmtPSKSHA256:
		return !m.refused[refusedSHA256]
	case mgmtPSKOnly:
		return saved.passphrase != "" && !m.refused[refusedSAE] && m.refused[refusedSHA256]
	default:
		return true
	}
}

// setSecurity sets a network's security variables in wpa_supplicant (ADR 0136). It reports
// false, without an error, for a network this build cannot join at all (WEP without
// CONFIG_WEP); a refused optional setting only narrows what the network offers.
//
//   - WEP: the static key at the index it was saved with (0 unless stock's BCO had another),
//     sent at that index, open system or shared key authentication, whichever the access
//     point takes.
//   - A password or a raw key: the PSK, then the most capable suites (offers) that
//     wpa_supplicant takes; management frame protection comes from supplicantConf's pmf=1, so
//     wpa_supplicant leaves it off for a driver that cannot do it. SAE's password goes as hex,
//     so no quoting can go wrong.
//   - Neither: an open network.
func (m *Manager) setSecurity(ctx context.Context, conn *wpactrl.Conn, netID int, saved *network) (bool, error) {
	switch {
	case saved.wepHex != "":
		if m.refused[refusedWEP] {
			return false, nil
		}

		index := strconv.Itoa(saved.wepIndex)
		for _, setting := range [][2]string{
			{keyMgmt, "NONE"}, {"wep_key" + index, saved.wepHex}, {"wep_tx_keyidx", index}, {"auth_alg", "OPEN SHARED"},
		} {
			err := conn.SetNetwork(ctx, netID, setting[0], setting[1])
			if wpactrl.Refused(err) && setting[0] == "wep_key"+index {
				m.refuse(refusedWEP, "this build cannot join WEP networks")

				return false, nil
			}

			if err != nil {
				return false, fmt.Errorf("%s: %w", setting[0], err)
			}
		}

		return true, nil
	case saved.keyHex == "":
		err := conn.SetNetwork(ctx, netID, keyMgmt, "NONE")
		if err != nil {
			return false, fmt.Errorf("%s: %w", keyMgmt, err)
		}

		return true, nil
	default:
		return m.setPSK(ctx, conn, netID, saved)
	}
}

// setPSK sets a password network: its PSK, and the first suites wpa_supplicant takes, most
// capable first. A refused SAE password or suite is logged once and the next suite is tried.
// A refusal of the first, combined suite says only that SAE or PSK-SHA256 is missing: which
// one is learnt from the next suites, so a build with SAE but without PSK-SHA256 still offers
// WPA3.
func (m *Manager) setPSK(ctx context.Context, conn *wpactrl.Conn, netID int, saved *network) (bool, error) {
	err := conn.SetNetwork(ctx, netID, "psk", saved.keyHex)
	if err != nil {
		return false, fmt.Errorf("psk: %w", err)
	}

	try := pskTry{saePassword: false, combinedRefused: false}

	for _, mgmt := range []string{mgmtPSKSAE, mgmtPSKSHA256, mgmtPSKOnly, mgmtPSK} {
		if !m.offers(saved, mgmt) {
			continue
		}

		var done bool

		done, err = m.trySuite(ctx, conn, netID, saved, mgmt, &try)
		if done || err != nil && !wpactrl.Refused(err) {
			return done, err
		}
	}

	return false, err // plain WPA-PSK refused: nothing left to try
}

// pskTry is what setPSK learnt so far: whether SAE's password is set, and whether the
// combined suite was refused without saying for want of what.
type pskTry struct {
	saePassword     bool
	combinedRefused bool
}

// trySuite offers one suite, with SAE's password first where it has SAE. It reports true once
// wpa_supplicant takes the suite; a refusal comes back as wpactrl.Refused, after it is noted.
func (m *Manager) trySuite(
	ctx context.Context, conn *wpactrl.Conn, netID int, saved *network, mgmt string, try *pskTry,
) (bool, error) {
	if strings.HasSuffix(mgmt, " SAE") && !try.saePassword {
		err := conn.SetNetwork(ctx, netID, "sae_password", hex.EncodeToString([]byte(saved.passphrase)))
		if err != nil {
			if wpactrl.Refused(err) {
				m.refuse(refusedSAE, "no WPA3: WPA2 networks join without it")
			}

			return false, fmt.Errorf("sae_password: %w", err)
		}

		try.saePassword = true
	}

	err := conn.SetNetwork(ctx, netID, keyMgmt, mgmt)
	if err == nil {
		// PSK-SHA256 taken after the combined suite was refused: SAE was what it lacked
		if mgmt == mgmtPSKSHA256 && try.combinedRefused {
			m.refuse(refusedSAE, "no WPA3: WPA2 networks join without it")
		}

		return true, nil
	}

	if wpactrl.Refused(err) && m.refusedMgmt(mgmt) {
		try.combinedRefused = true
	}

	return false, fmt.Errorf("%s: %w", keyMgmt, err)
}

// refusedMgmt notes what a refused suite says this wpa_supplicant lacks. It reports true for
// the combined suite, whose refusal says only that SAE or PSK-SHA256 is missing: the next
// suites tell which.
func (m *Manager) refusedMgmt(mgmt string) bool {
	switch mgmt {
	case mgmtPSKSAE:
		return true
	case mgmtPSKSHA256:
		m.refuse(refusedSHA256, "no PSK-SHA256: WPA2 networks join without it")
	case mgmtPSKOnly:
		m.refuse(refusedSAE, "no WPA3: WPA2 networks join without it")
	default:
		// plain WPA-PSK refused: nothing left to try
	}

	return false
}

// configure hands wpa_supplicant the networks: wifi.conf at the highest priority, the
// known ones below in order. The name goes as hex and the key as the 32-byte PSK in hex, so
// no quoting can go wrong; the passphrase goes only as SAE's password, in hex too, over the
// control socket (ADR 0136). A hidden network answers only a probe that names it, and a typed
// SSID may be hidden: every network is probed for.
//
// Only a control socket that stops answering for wifi.conf's network fails the join. A
// network wpa_supplicant refuses, or one this build cannot join, is removed and the others
// still join: a missing optional feature never leaves the speaker without Wi-Fi.
func (m *Manager) configure(ctx context.Context, sess *session, nets []network) error {
	conn := sess.conn

	err := conn.RemoveAllNetworks(ctx)
	if err != nil {
		return fmt.Errorf("remove networks: %w", err)
	}

	for rank := range nets {
		saved := &nets[rank]

		netID, err := conn.AddNetwork(ctx)
		if err != nil {
			return fmt.Errorf("add network: %w", err)
		}

		usable, setErr := m.setNetwork(ctx, conn, netID, saved, rank)
		if setErr != nil && rank == 0 && !wpactrl.Refused(setErr) {
			return fmt.Errorf("set network: %w", setErr)
		}

		if !usable || setErr != nil {
			if setErr != nil {
				m.log.Warn("a saved network was skipped", "rank", rank, "err", setErr)
			} else {
				m.log.Warn("a saved network was skipped: this build cannot join it", "rank", rank)
			}

			err = conn.RemoveNetwork(ctx, netID)
			if err != nil {
				return fmt.Errorf("remove network: %w", err)
			}

			continue
		}

		sess.records[netID] = saved.rec

		err = conn.EnableNetwork(ctx, netID)
		if err != nil {
			return fmt.Errorf("enable network: %w", err)
		}
	}

	return nil
}

// setNetwork sets one network's name, probe and priority, then its security.
func (m *Manager) setNetwork(
	ctx context.Context, conn *wpactrl.Conn, netID int, saved *network, rank int,
) (bool, error) {
	for _, setting := range [][2]string{
		{"ssid", hex.EncodeToString([]byte(saved.ssid))},
		{"scan_ssid", "1"},
		{"priority", strconv.Itoa(KnownMax + 1 - rank)},
	} {
		err := conn.SetNetwork(ctx, netID, setting[0], setting[1])
		if err != nil {
			return false, fmt.Errorf("%s: %w", setting[0], err)
		}
	}

	return m.setSecurity(ctx, conn, netID, saved)
}

// bssMatches reports whether a scanned BSS is saved's network: its SSID, with the security it
// was saved with, as wpa_supplicant's flags show it. An open BSS never matches a WEP or WPA
// network, nor a protected one an open network; a WPA network matches over PSK, or over SAE when
// it has the password WPA3 needs.
func bssMatches(b wpactrl.BSS, saved *network) bool {
	has := func(words ...string) bool {
		return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(b.Flags, w) })
	}

	if b.SSID != saved.ssid {
		return false
	}

	switch saved.rec.security() {
	case SecurityWEP:
		return has("WEP") && !has("WPA", "RSN", "SAE")
	case secPSK:
		return has("PSK") || saved.passphrase != "" && has("SAE")
	default:
		return !has("WEP", "WPA", "RSN", "SAE")
	}
}

// startDHCP starts udhcpc on wlan0 in the background. No -q: udhcpc stays and renews the
// lease; with -q it exited once bound, and when the lease ran out the router could hand the
// address to another host (ADR 0063). -A 3: discovery often starts before the join
// completes, and busybox then waits 20 s before it tries again. -O ntpsrv keeps the lease's
// NTP servers for clock.sh; the hostname lets the router list the speaker by name.
func (m *Manager) startDHCP() {
	args := udhcpcArgs(m.cfg.STA, m.pid("udhcpc"), m.cfg.Hostname())

	_, _, err := m.cfg.Sys.Start("udhcpc", args...)
	if err != nil {
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
func (m *Manager) waitJoin(ctx context.Context, sess *session, ssid string) joinResult {
	res := joinResult{joined: false, assoc: -1, took: 0, seen: nil, reason: ""}
	state := ""
	// The window is wall-clock time: a poll can take seconds when wpa_supplicant waits on the
	// ST20's BCO driver, and 90 polls then stretched "90 s" of "Connecting" to minutes.
	start := time.Now()
	polls := max(int(m.cfg.Timing.Join/m.cfg.Timing.Poll), 1)

	secs := func(int) int { return int(time.Since(start) / time.Second) }

	for poll := range polls {
		if poll > 0 && time.Since(start) >= m.cfg.Timing.Join {
			break
		}

		if m.cfg.Sys.HasIPv4(m.cfg.STA) {
			res.joined, res.took = true, secs(poll)

			return res
		}

		st, err := sess.status(ctx)
		if err == nil && st["wpa_state"] != state {
			state = st["wpa_state"]
			res.seen = append(res.seen, state)

			found := 0

			bss, err := sess.scanResults(ctx)
			if err == nil {
				found = len(bss)
			}

			m.log.Info("wpa_state", "state", state, "after_s", secs(poll), "scan_results", found)
		}

		if res.assoc < 0 && state == "COMPLETED" {
			res.assoc = secs(poll)
			m.log.Info("associated", "after_s", res.assoc)
		}

		if !m.events(ctx, sess, m.cfg.Timing.Poll) {
			return res
		}
	}

	if res.joined = m.cfg.Sys.HasIPv4(m.cfg.STA); res.joined {
		res.took = secs(polls)

		return res
	}

	res.reason = joinFailure(ctx, sess, res.seen, ssid)

	return res
}

// joinFailure says why a join failed from the furthest wpa_state it reached. A wrong WPA
// passphrase stalls in the 4-way handshake while the router repeats its first message, and
// wpa_supplicant also reports it outright.
func joinFailure(ctx context.Context, sess *session, seen []string, ssid string) string {
	has := func(states ...string) bool {
		return slices.ContainsFunc(seen, func(st string) bool { return slices.Contains(states, st) })
	}
	switch {
	case has("COMPLETED"):
		return "no_address"
	case sess.wrongKey || has("4WAY_HANDSHAKE", "GROUP_HANDSHAKE"):
		return "wrong_password"
	case sess.security || has("AUTHENTICATING", "ASSOCIATING", "ASSOCIATED"):
		return "refused"
	default:
		// Never got that far: is the network there at all?
	}

	bss, err := sess.scanResults(ctx)
	if err == nil {
		for _, b := range bss {
			if b.SSID == ssid {
				return "timeout"
			}
		}
	}

	return "not_found"
}

// events handles wpa_supplicant's events for d. It reports false when ctx ended.
func (m *Manager) events(ctx context.Context, sess *session, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		case event, ok := <-sess.conn.Events():
			if !ok {
				if !sess.lost {
					sess.lost = true

					m.log.Warn("wpa_supplicant's event stream ended")
				}
				// Nothing more will come; wait out the rest of d.
				select {
				case <-ctx.Done():
					return false
				case <-timer.C:
					return true
				}
			}

			m.event(ctx, sess, event)
		}
	}
}

func (m *Manager) event(ctx context.Context, sess *session, event wpactrl.Event) {
	switch event.Name {
	case "CTRL-EVENT-CONNECTED":
		m.renewLease(ctx, sess)
	case "CTRL-EVENT-SSID-TEMP-DISABLED":
		if reason, _ := event.Field("reason"); reason == "WRONG_KEY" {
			sess.wrongKey = true

			m.log.Info("wpa_supplicant reports a wrong key")
		}
	case "CTRL-EVENT-DISCONNECTED":
		m.log.Info("wifi disconnected")
	case "CTRL-EVENT-ASSOC-REJECT":
		// The router's IEEE 802.11 status code. On the ST20 the BCO reports its own join
		// failures this way too: 15 when the 4-way handshake did not complete (with handshake
		// offload wpa_supplicant never sees WRONG_KEY), 43 when the network offers only security
		// it cannot join (an enterprise network, or one whose security its profile does not have).
		code, _ := event.Field("status_code")
		m.log.Info("association refused", "status_code", code)

		switch code {
		case "15":
			sess.wrongKey = true
		case "31", "43", "45", "72":
			sess.security = true
		default:
			// Another refusal: the furthest wpa_state decides.
		}
	default:
		// Not one the manager acts on.
	}
}

// renewLease keeps the address right when wpa_supplicant moves to another saved network on
// its own, when the current one goes away: udhcpc would keep the old network's address
// until its lease ran out. On a connection to another SSID udhcpc drops the address and
// asks again; the same SSID (a router back after a power cut, a roam between access points)
// only renews, so nothing playing is cut off.
func (m *Manager) renewLease(ctx context.Context, sess *session) {
	pid := readPid(m.pid("udhcpc"))
	if pid == 0 {
		return // udhcpc not started yet: it asks on its own
	}

	st, err := sess.status(ctx)
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
func (m *Manager) joined(ctx context.Context, sess *session, confPath string, conf Record, res *joinResult) {
	touch(m.state("network-ready"))

	good := confPath + ".good"
	// New settings that joined; the fallback pass has wifi.conf equal to .good and keeps
	// the notice.
	if !sameFile(confPath, good) {
		remove(m.state("wifi-fallback-reason"))
	}

	remove(m.state("wifi-join-failed"))
	m.tries, m.triedOthers = 0, false
	m.problem("")
	m.log.Info("dhcp ok", "iface", m.cfg.STA, "after_s", res.took)
	m.rememberJoined(ctx, sess, confPath, conf)

	if !sameFile(confPath, good) {
		err := copyFile(confPath, good)
		if err != nil {
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

	bootLog, err := os.OpenFile(m.cfg.Paths.BootLog, os.O_APPEND|os.O_WRONLY|os.O_CREATE, privateFileMode)
	if err == nil {
		fmt.Fprintf(bootLog, "%s wifi: associated after %ss, address after %ss\n",
			time.Now().Format(time.UnixDate), seconds(res.assoc), seconds(res.took))
		_ = bootLog.Close()
	}

	if m.cfg.Joined != nil {
		m.cfg.Joined()
	}
}

// rememberJoined puts the network that joined at the front of wifi.conf.known (the newest
// KnownMax are kept) and, when wpa_supplicant picked another than wifi.conf, makes it
// wifi.conf.
func (m *Manager) rememberJoined(ctx context.Context, sess *session, confPath string, conf Record) {
	rec := conf

	st, err := sess.status(ctx)
	if err == nil {
		netID, idErr := strconv.Atoi(st["id"])
		if idErr == nil {
			if r, ok := sess.records[netID]; ok {
				rec = r
			}
		}
	}

	err = Remember(confPath+".known", rec)
	if err != nil {
		m.log.Warn("could not remember the network", "err", err)
	}

	if rec.SSIDB64 != conf.SSIDB64 {
		err := WriteConf(confPath, rec)
		if err != nil {
			m.log.Warn("could not keep the joined network", "err", err)

			return
		}

		m.log.Info("joined a saved network instead of wifi.conf")
	}
}

// watch supervises a station until ctx ends or wpa_supplicant is gone: it renews the lease
// on a network change and, for a station still waiting (no AP mode), runs onAddress once the
// address comes; nil for a station that is joined. It reports whether wpa_supplicant went
// away, so the caller brings it up again. That follows the supplicant's own end, never an
// unanswered gateway.
func (m *Manager) watch(ctx context.Context, sess *session, onAddress func()) bool {
	misses := 0

	for look := 1; ; look++ {
		if !m.events(ctx, sess, m.cfg.Timing.Watch) {
			return false
		}
		// A datagram socket has no end of stream: a supplicant that went away shows only
		// as requests without an answer. One slow answer, during a scan say, is not that.
		if look%pingEvery == 0 {
			pctx, cancel := context.WithTimeout(ctx, pingTimeout)
			answered := sess.conn.Ping(pctx)

			cancel()

			switch {
			case answered:
				misses = 0
			case ctx.Err() == nil:
				misses++
				if misses >= pingMisses {
					sess.lost = true

					m.log.Warn("wpa_supplicant no longer answers", "pings", misses)
				}
			default:
				// ctx ended during the ping: the loop's next look returns.
			}
		}

		if sess.lost {
			return ctx.Err() == nil
		}

		if onAddress != nil && m.cfg.Sys.HasIPv4(m.cfg.STA) {
			onAddress()

			onAddress = nil
		}
	}
}
