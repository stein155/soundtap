// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/childproc"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/wifi"
	"github.com/stein155/soundtap/app/internal/wpactrl"
)

// scanBusyWait is how long a survey waits for a scan already running, which takes about 4 s.
const scanBusyWait = 6 * time.Second

// iwScanWait bounds iw's own wait for the end of a survey. The ST20's link driver answers
// in milliseconds from what the BCO heard, before iw listens for that answer, so iw would
// wait for ever; the results are there all the same.
//
//nolint:gochecknoglobals // a test shortens it.
var iwScanWait = 10 * time.Second

// Waits: wpa_supplicant gets wpaTimeout to answer; a busy radio is asked again every
// scanRetryStep.
const (
	wpaTimeout    = 3 * time.Second
	scanRetryStep = 250 * time.Millisecond
)

// The quality thresholds of the UI's signal tokens, in percent.
const (
	qualityExcellent = 75
	qualityGood      = 50
	qualityFair      = 25
)

// dBm to percent: about -100 dBm is 0 % and -50 dBm 100 %.
const (
	dbmFloor = 100
	dbmScale = 2
)

// scanResultFields is SCAN_RESULTS' bssid, frequency, signal, flags and ssid.
const scanResultFields = 5

func (*Speaker) ScanWifi(ctx context.Context) ([]speaker.WifiNetwork, error) {
	err := live(ctx)
	if err != nil {
		return nil, err
	}

	if os.Getenv("SOUNDTAP_WIFI_MODE") == wifiModeBCO {
		networks, scanErr := bco.ScanNetworks(ctx, os.Getenv("SOUNDTAP_BCO_TTY"))
		if scanErr != nil {
			return nil, fmt.Errorf("BCO scan: %w", scanErr)
		}

		result := make([]speaker.WifiNetwork, 0, len(networks))
		for _, network := range networks {
			result = append(result, speaker.WifiNetwork{
				SSID: network.SSID, Signal: qualityToken(network.Quality), Secure: network.Secure, WEP: false,
			})
		}

		return result, nil
	}

	if os.Getenv("SOUNDTAP_WIFI_MODE") == "disabled" {
		return []speaker.WifiNetwork{}, nil
	}

	_, err = os.Stat(setupAPMarker)
	if err == nil {
		// The setup AP owns the radio. A live scan there takes the BCO off the AP's channel
		// (its beacons stop) and never finishes, past the UI's 8 s deadline; the Wi-Fi
		// manager scanned just before hostapd started, so answer from that.
		return cachedScan(), nil
	}

	const iface = "wlan0"

	networks, err := iwScan(ctx, iface)
	if err == nil && len(networks) != 0 {
		return networks, nil
	}
	// What wpa_supplicant last saw is next, then the scan the Wi-Fi manager captured before
	// hostapd occupied the radio.
	if networks := supplicantScan(ctx); len(networks) != 0 {
		return networks, nil
	}

	return cachedScan(), nil
}

// supplicantScan is wpa_supplicant's last scan table, or nothing without a supplicant.
func supplicantScan(ctx context.Context) []speaker.WifiNetwork {
	client, err := wpactrl.Dial(wpaCtrlPath)
	if err != nil {
		return nil
	}
	defer client.Close()

	cctx, cancel := context.WithTimeout(ctx, wpaTimeout)
	defer cancel()

	out, err := client.Request(cctx, "SCAN_RESULTS")
	if err != nil {
		return nil
	}

	return parseScanResults(out)
}

// iwScan surveys the air and waits for the answer, about 4 s. wpa_supplicant's SCAN cannot
// serve here: it only starts one, and SCAN_RESULTS until then is an old table, which after
// a few idle minutes holds nothing but the network the speaker is on.
func iwScan(ctx context.Context, iface string) ([]speaker.WifiNetwork, error) {
	deadline := time.Now().Add(scanBusyWait)

	for {
		var stderr bytes.Buffer

		attempt, cancel := context.WithTimeout(ctx, iwScanWait)
		cmd := exec.CommandContext(attempt, "iw", "dev", iface, "scan")
		childproc.NetTool(cmd)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		missed := attempt.Err() != nil && ctx.Err() == nil

		cancel()

		if missed {
			out, err = exec.CommandContext(ctx, "iw", "dev", iface, "scan", "dump").Output()
		}

		if err == nil {
			return parseIWScan(string(out)), nil
		}

		if !strings.Contains(stderr.String(), "busy") || time.Now().After(deadline) {
			return nil, fmt.Errorf("iw scan: %w", err)
		}

		select { // wpa_supplicant's own scan has the radio
		case <-ctx.Done():
			return nil, live(ctx)
		case <-time.After(scanRetryStep):
		}
	}
}

// Written by the Wi-Fi manager: the setup AP's marker, and the scan it took before
// hostapd started.
//
//nolint:gochecknoglobals // a test points them at its own files.
var setupAPMarker, scanCache = "/run/soundtap/setup-ap", "/run/soundtap-wifi-scan"

func cachedScan() []speaker.WifiNetwork {
	out, err := os.ReadFile(scanCache)
	if err != nil {
		return []speaker.WifiNetwork{} // honest empty — never fabricated networks
	}

	return parseIWScan(string(out))
}

func qualityToken(quality int) string {
	switch {
	case quality >= qualityExcellent:
		return "excellent"
	case quality >= qualityGood:
		return "good"
	case quality >= qualityFair:
		return "fair"
	default:
		return "poor"
	}
}

// iwNetwork is one BSS in the output of iw scan: secure when it has Privacy or a WPA or RSN
// element, wpa when it has one of those elements (secure without it is WEP).
type iwNetwork struct {
	ssid   string
	dbm    int
	secure bool
	wpa    bool
}

func parseIWScan(out string) []speaker.WifiNetwork {
	return strongestPerSSID(iwNetworks(out))
}

// iwSecurityLine reports a line of a BSS that says the network is secured.
func iwSecurityLine(field string) bool {
	return strings.HasPrefix(field, "RSN:") || strings.HasPrefix(field, "WPA:") ||
		strings.Contains(field, "capability:") && strings.Contains(field, "Privacy")
}

func iwNetworks(out string) []iwNetwork {
	var (
		found   []iwNetwork
		current *iwNetwork
	)

	flush := func() {
		if current != nil && current.ssid != "" {
			found = append(found, *current)
		}
	}

	for line := range strings.SplitSeq(out, "\n") {
		// Only the indentation goes: SSID bytes are significant.
		field := strings.TrimLeft(strings.TrimRight(line, "\r"), " \t")
		switch {
		case strings.HasPrefix(field, "BSS "):
			flush()

			current = &iwNetwork{ssid: "", dbm: 0, secure: false, wpa: false}
		case current != nil && strings.HasPrefix(field, "SSID: "):
			current.ssid = unescapeIW(strings.TrimPrefix(field, "SSID: "))
		case current != nil && strings.HasPrefix(field, "signal: "):
			value := strings.Fields(strings.TrimPrefix(field, "signal: "))
			if len(value) != 0 {
				level, _ := strconv.ParseFloat(value[0], 64)
				current.dbm = int(level)
			}
		case current != nil && iwSecurityLine(field):
			current.secure = true
			current.wpa = current.wpa || strings.HasPrefix(field, "RSN:") || strings.HasPrefix(field, "WPA:")
		default: // a line this does not read
		}
	}

	flush()

	return found
}

// strongestPerSSID keeps one entry per network name, strongest first.
func strongestPerSSID(found []iwNetwork) []speaker.WifiNetwork {
	strongest := map[string]iwNetwork{}
	order := []string{}

	for _, net := range found {
		if old, ok := strongest[net.ssid]; ok && old.dbm >= net.dbm {
			continue
		}

		if _, ok := strongest[net.ssid]; !ok {
			order = append(order, net.ssid)
		}

		strongest[net.ssid] = net
	}
	// Strongest first; a BSS without a level (0 dBm: not measured) goes last.
	level := func(ssid string) int {
		if d := strongest[ssid].dbm; d < 0 {
			return d
		}

		return -1000
	}

	slices.SortStableFunc(order, func(a, b string) int { return cmp.Compare(level(b), level(a)) })

	result := make([]speaker.WifiNetwork, 0, len(order))
	for _, ssid := range order {
		net := strongest[ssid]
		result = append(result, speaker.WifiNetwork{
			SSID: net.ssid, Signal: dbmToken(net.dbm), Secure: net.secure, WEP: net.secure && !net.wpa,
		})
	}

	return result
}

// unescapeIW undoes iw's SSID escaping: it prints a leading or trailing space, a
// backslash and any non-printable byte as \xNN.
func unescapeIW(text string) string {
	if !strings.Contains(text, `\x`) {
		return text
	}

	var out strings.Builder

	for index := 0; index < len(text); index++ {
		if text[index] == '\\' && index+3 < len(text) && text[index+1] == 'x' {
			v, err := strconv.ParseUint(text[index+2:index+4], 16, 8)
			if err == nil {
				out.WriteByte(byte(v))

				index += 3

				continue
			}
		}

		out.WriteByte(text[index])
	}

	return out.String()
}

// parseScanResults parses SCAN_RESULTS (tab-separated: bssid, freq, signal dBm,
// flags, ssid) into networks, dropping hidden SSIDs and keeping the strongest per SSID.
func parseScanResults(out string) []speaker.WifiNetwork {
	seen := map[string]int{} // ssid -> index in result
	strongest := map[string]int{}

	var res []speaker.WifiNetwork

	for i, line := range strings.Split(out, "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // header / blank
		}

		fields := strings.Split(line, "\t")
		if len(fields) < scanResultFields {
			continue
		}

		ssid := strings.TrimRight(fields[4], "\r") // spaces are part of the SSID
		if ssid == "" {
			continue // hidden network
		}

		dbm, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil {
			continue
		}

		net := speaker.WifiNetwork{
			SSID:   ssid,
			Signal: dbmToken(dbm),
			Secure: strings.Contains(fields[3], "WPA") || strings.Contains(fields[3], "WEP") ||
				strings.Contains(fields[3], "RSN") || strings.Contains(fields[3], "SAE"),
			WEP: strings.Contains(fields[3], "WEP") && !strings.Contains(fields[3], "WPA") &&
				!strings.Contains(fields[3], "RSN"),
		}
		if idx, ok := seen[ssid]; ok {
			if dbm > strongest[ssid] {
				res[idx], strongest[ssid] = net, dbm
			}

			continue
		}

		seen[ssid] = len(res)
		strongest[ssid] = dbm

		res = append(res, net)
	}

	slices.SortStableFunc(res, func(a, b speaker.WifiNetwork) int {
		return cmp.Compare(strongest[b.SSID], strongest[a.SSID])
	})

	return res
}

// dbmToken maps a Wi-Fi signal in dBm to the same tokens the UI expects.
func dbmToken(dbm int) string {
	if dbm < -dbmFloor || dbm >= 0 {
		return ""
	}

	return qualityToken(dbmScale * (dbm + dbmFloor)) // ~ -100dBm=0%, -50dBm=100%
}

// CheckWifi says what is wrong with join details, in words a client can show.
func CheckWifi(ssid, security, password string) error {
	control := func(r rune) bool { return r < ' ' }
	if ssid == "" || len(ssid) > maxSSIDBytes || strings.IndexFunc(ssid, control) >= 0 {
		return errSSIDRequired
	}

	switch security {
	case "none":
		if password != "" {
			return errOpenPassword
		}
	case "", "wpa_or_wpa2":
		if bco.IsHexPSK(password) {
			return nil
		}

		if len(password) < 8 || len(password) > 63 || strings.IndexFunc(password, control) >= 0 {
			return errWPAPassword
		}
	case wifi.SecurityWEP: // ADR 0136
		if !wifi.IsWEPKey(password) {
			return errWEPKey
		}
	default:
		return fmt.Errorf("%w %q", errUnknownSecurity, security)
	}

	return nil
}

func (s *Speaker) SetWifi(ctx context.Context, ssid, security, password string) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	mode := os.Getenv("SOUNDTAP_WIFI_MODE")
	if mode == "disabled" {
		return errWifiDisabled
	}

	err = CheckWifi(ssid, security, password)
	if err != nil {
		return err
	}
	// On the ST20 the radio belongs to the BCO front processor, so the join goes
	// through its supplicant. bco.Connect waits for the association and only then
	// persists it, so an error here means the speaker did not actually join — and the
	// credentials must not be recorded as current.
	if mode == wifiModeBCO {
		// Bose's BCO app, still there before SoundTap's replaces it, is driven for WPA only.
		if security == wifi.SecurityWEP {
			return errWEPStockBCO
		}

		err := bco.Connect(ctx, os.Getenv("SOUNDTAP_BCO_TTY"), ssid, password)
		if err != nil {
			return err //nolint:wrapcheck // the BCO's own words for the client, which shows them as they are.
		}

		s.lock()
		s.wifiSSID = ssid
		s.wifiPSK = password
		s.unlock()

		return nil
	}

	s.lock()
	save := s.wifiSave
	s.unlock()

	if save != nil {
		err := save(ssid, security, password)
		if err != nil {
			return err
		}
	}

	s.lock()
	s.wifiSSID = ssid
	s.wifiPSK = password
	s.unlock()

	return nil
}
