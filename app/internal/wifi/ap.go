// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The setup network (ADR 0063): an open AP on wlan1 named after the speaker, our own
// subnet, a catch-all DNS that sends every captive-portal probe to the setup page, and
// leases of 600 s.
const (
	APAddress    = "192.168.42.1"
	APFallback   = "SoundTap-Setup"
	MaxSSIDBytes = 32
	apPrefix     = 24
)

var freqLine = regexp.MustCompile(`(?m)^\s*freq:\s*([0-9.]+)`)

// apChannel is the setup AP's channel: of 1, 6 and 11 the one with the fewest networks in
// scan (iw scan output), 6 on a tie or without a scan, as stock picks it.
func apChannel(scan string) int {
	n := map[int]int{}
	for _, m := range freqLine.FindAllStringSubmatch(scan, -1) {
		f, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		switch int(f) {
		case 2412, 2437, 2462:
			n[int(f)]++
		}
	}
	best := 2437
	if n[2412] < n[best] {
		best = 2412
	}
	if n[2462] < n[best] {
		best = 2462
	}
	return (best - 2407) / 5
}

// hostapdConf is the setup AP's hostapd configuration: open, 802.11g, no WMM, and the
// country in the beacon (802.11d) when one is known. The ST20's AP is the BCO's radio
// (soundtap_bco, ADR 0031), proven on channel 6 without a country element, and stays so.
func hostapdConf(iface, ssid string, channel int, country string) string {
	c := fmt.Sprintf("interface=%s\ndriver=nl80211\nssid=%s\nhw_mode=g\nchannel=%d\nwmm_enabled=0\n", iface, ssid, channel)
	if country != "" && country != "00" {
		c += fmt.Sprintf("country_code=%s\nieee80211d=1\n", country)
	}
	return c
}

// udhcpdConf hands phones on the setup AP an address, the speaker as router and DNS, and
// a 600 s lease.
func udhcpdConf(iface string) string {
	return "start 192.168.42.10\nend   192.168.42.50\ninterface " + iface +
		"\noption subnet 255.255.255.0\noption router " + APAddress + "\noption dns " + APAddress +
		"\noption lease 600\n"
}

// setupSSID is the speaker's name as the AP's SSID, or the fallback when it does not fit
// IEEE 802.11's 32 octets: a speaker name may be 64 characters, and hostapd refuses a
// longer SSID.
func (m *Manager) setupSSID() string {
	ssid := ""
	if m.cfg.SetupSSID != nil {
		ssid = m.cfg.SetupSSID()
	}
	if ssid == "" || len(ssid) > MaxSSIDBytes || strings.ContainsAny(ssid, "\n\x00") {
		m.log.Info("speaker name does not fit an SSID; using the fallback", "ssid", APFallback)
		return APFallback
	}
	return ssid
}

// openAP brings up the setup network. wlan0 stays managed for the scans the setup page
// shows; the wl18xx cannot reliably scan off-channel once hostapd owns a second interface,
// so the scan is taken first and cached. Every step that fails undoes what came before it.
func (m *Manager) openAP(ctx context.Context, reason string) error {
	ssid := m.setupSSID()
	m.log.Info("starting the setup AP", "reason", reason, "iface", m.cfg.AP, "ssid", ssid, "ip", APAddress)
	scan, err := m.stationForAP(ctx)
	if err != nil {
		return err
	}
	stopAP, err := m.startHostapd(ctx, ssid, scan)
	if err != nil {
		return err
	}
	return m.startPortal(ctx, stopAP)
}

// stationForAP stops the station and keeps wlan0 managed and up for the scan the setup
// page shows, and returns that scan.
func (m *Manager) stationForAP(ctx context.Context) (string, error) {
	m.stopPidfile(m.pid("wpa"))
	m.stopPidfile(m.pid("udhcpc"))
	m.runLogged(ctx, "ip", "addr", "flush", "dev", m.cfg.STA)
	m.lanAllow(ctx, "del", m.cfg.STA)
	if _, err := m.run(ctx, "iw", "dev", m.cfg.STA, "set", "type", "managed"); err != nil {
		return "", fmt.Errorf("could not set %s to managed; setup AP unavailable: %w", m.cfg.STA, err)
	}
	if err := m.linkUp(ctx, m.cfg.STA); err != nil {
		return "", fmt.Errorf("could not bring up %s; setup AP unavailable: %w", m.cfg.STA, err)
	}
	return m.scanForAP(ctx), nil
}

// startHostapd adds wlan1 and starts hostapd on it with its address. stopAP undoes it.
func (m *Manager) startHostapd(ctx context.Context, ssid, scan string) (stopAP func(), err error) {
	phy := m.firstPhy()
	if phy == "" {
		return nil, errors.New("no wireless phy; setup AP unavailable")
	}
	if _, err := m.run(ctx, "iw", "phy", phy, "interface", "add", m.cfg.AP, "type", "__ap"); err != nil {
		return nil, fmt.Errorf("could not create %s; setup AP unavailable: %w", m.cfg.AP, err)
	}
	delAP := func() { m.runLogged(context.WithoutCancel(ctx), "iw", "dev", m.cfg.AP, "del") }

	channel, country := 6, m.reg.Country
	if m.variant == "spotty" {
		country = ""
	} else {
		channel = apChannel(scan)
	}
	conf := filepath.Join(m.cfg.Paths.Tmp, "hostapd.conf")
	if err := os.WriteFile(conf, []byte(hostapdConf(m.cfg.AP, ssid, channel, country)), 0o600); err != nil {
		delAP()
		return nil, fmt.Errorf("hostapd.conf: %w", err)
	}
	if _, err := m.run(ctx, "hostapd", "-B", "-P", m.pid("hostapd"), conf); err != nil {
		delAP()
		return nil, fmt.Errorf("hostapd failed; setup AP unavailable: %w", err)
	}
	m.log.Info("hostapd up", "ssid", ssid, "channel", channel)
	stopAP = func() {
		m.stopPidfile(m.pid("hostapd"))
		delAP()
	}
	if _, err := m.run(ctx, "ip", "addr", "add", fmt.Sprintf("%s/%d", APAddress, apPrefix), "dev", m.cfg.AP); err != nil {
		stopAP()
		return nil, fmt.Errorf("could not assign the setup AP's address: %w", err)
	}
	m.lanAllow(ctx, "add", m.cfg.AP, APAddress, strconv.Itoa(apPrefix))
	return stopAP, nil
}

// startPortal starts the catch-all DNS and, once soundtapd answers, the DHCP server.
func (m *Manager) startPortal(ctx context.Context, stopAP func()) error {

	dnsConf := filepath.Join(m.cfg.Paths.Tmp, "dnsd.conf")
	if err := os.WriteFile(dnsConf, []byte("* "+APAddress+"\n"), 0o600); err != nil {
		stopAP()
		return fmt.Errorf("dnsd.conf: %w", err)
	}
	if err := m.startDaemon(ctx, "dnsd", "dnsd", "-i", APAddress, "-c", dnsConf); err != nil {
		stopAP()
		return fmt.Errorf("dnsd failed; captive portal unavailable: %w", err)
	}
	stopHostapd := stopAP
	stopAP = func() {
		m.stopPidfile(m.pid("dnsd"))
		stopHostapd()
	}

	// Let the boot skip its internet-only clock wait, then withhold DHCP until the web UI
	// listens: phones probe for a captive portal as soon as they have a lease, and when that
	// first request is refused iOS may not open the setup sheet at all.
	touch(m.state("setup-ap"))
	if !m.waitReady(ctx) {
		remove(m.state("setup-ap"), m.state("network-ready"))
		stopAP()
		return errors.New("soundtapd does not answer; captive portal unavailable")
	}
	dhcpConf := filepath.Join(m.cfg.Paths.Tmp, "udhcpd.conf")
	if err := os.WriteFile(dhcpConf, []byte(udhcpdConf(m.cfg.AP)), 0o600); err != nil {
		remove(m.state("setup-ap"))
		stopAP()
		return fmt.Errorf("udhcpd.conf: %w", err)
	}
	if err := m.startDaemon(ctx, "udhcpd", "udhcpd", "-f", dhcpConf); err != nil {
		remove(m.state("setup-ap"), m.state("network-ready"))
		stopAP()
		return fmt.Errorf("udhcpd failed; setup AP unavailable: %w", err)
	}
	touch(m.state("network-ready"))
	m.log.Info("udhcpd up", "net", fmt.Sprintf("%s/%d", APAddress, apPrefix))
	return nil
}

// scanForAP caches a scan for the setup page and returns it. wlan0 may exist before
// cfg80211 can scan it, so it is tried three times while it is still the radio's only
// interface. No scan leaves an empty cache: the page then offers manual entry.
func (m *Manager) scanForAP(ctx context.Context) string {
	cache := m.cfg.Paths.ScanCache
	remove(cache)
	for attempt := 1; attempt <= 3; attempt++ {
		if out, ok := m.scanOnce(ctx); ok && os.WriteFile(cache, out, 0o644) == nil {
			m.log.Info("setup scan cached", "attempt", attempt)
			return string(out)
		}
		m.log.Info("setup scan failed", "attempt", attempt)
		if !sleep(ctx, m.cfg.Timing.ScanRetry) {
			break
		}
	}
	_ = os.WriteFile(cache, nil, 0o644)
	m.log.Info("setup scan found no networks; the setup page offers manual entry")
	return ""
}

// scanOnce scans on wlan0 once, if it is there and cfg80211 knows it.
func (m *Manager) scanOnce(ctx context.Context) ([]byte, bool) {
	if !exists(filepath.Join(m.cfg.Paths.SysNet, m.cfg.STA)) {
		return nil, false
	}
	if _, err := m.run(ctx, "iw", "dev", m.cfg.STA, "info"); err != nil {
		return nil, false
	}
	sctx, cancel := context.WithTimeout(ctx, m.cfg.Timing.Scan)
	defer cancel()
	out, err := m.run(sctx, "iw", "dev", m.cfg.STA, "scan")
	return out, err == nil
}

// firstPhy is the first wireless phy, phy0 on every model.
func (m *Manager) firstPhy() string {
	entries, err := os.ReadDir(m.cfg.Paths.SysPhy)
	if err != nil || len(entries) == 0 {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names[0]
}

// waitReady waits for soundtapd's own listener, at most Timing.Ready.
func (m *Manager) waitReady(ctx context.Context) bool {
	deadline := time.Now().Add(m.cfg.Timing.Ready)
	for m.cfg.Ready != nil && !m.cfg.Ready() {
		if time.Now().After(deadline) || !sleep(ctx, m.cfg.Timing.Poll) {
			return false
		}
	}
	return true
}

// holdAP keeps the setup AP up while saved credentials wait (ADR 0063): usually the router
// is still booting after a power cut. Every pause it tries the saved network again while
// nobody is on the AP, so the speaker does not stay an open AP for good. A device that
// stays on the AP longer than a setup by hand takes, such as a phone that remembers the open
// network, holds it off for Timing.Hold at most. It reports true when it is time to try.
func (m *Manager) holdAP(ctx context.Context, pause time.Duration) bool {
	var held time.Duration
	for sleep(ctx, pause) {
		if !exists(m.state("setup-ap")) {
			return false
		}
		out, _ := m.run(ctx, "iw", "dev", m.cfg.AP, "station", "dump") // no AP interface reads as no stations
		if strings.TrimSpace(string(out)) != "" {
			held += pause
			if held < m.cfg.Timing.Hold {
				continue
			}
			m.log.Info("a device has stayed on the setup AP; trying the saved network anyway", "held", held)
		} else {
			m.log.Info("setup AP idle; trying the saved network again")
		}
		m.tries++
		return true
	}
	return false
}

// retryPause is how long the setup AP waits before trying the saved network again: every
// short pause for the first ten tries while the router is simply not there yet. A wrong
// password is not helped by trying sooner, and needs the AP.
func (m *Manager) retryPause(reason string) time.Duration {
	if (reason == "not_found" || reason == "no_address") && m.tries < 10 {
		return m.cfg.Timing.ShortPause
	}
	return m.cfg.Timing.LongPause
}
