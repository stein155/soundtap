// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/btctl"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// pairTimeout bounds a Bluetooth pairing.
const pairTimeout = 10 * time.Second

// bluetoothPresent reports whether the kernel has a Bluetooth adapter. The model cannot
// say: ST20s ship with and without one, and bt.sh attaches it after soundtapd starts.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var bluetoothPresent = func() bool {
	m, _ := filepath.Glob("/sys/class/bluetooth/hci*")

	return len(m) > 0
}

// getBluetooth answers what stock answered on /bluetoothInfo: whether there is a radio,
// what a phone sees this speaker called, and which devices it remembers. A speaker has
// no screen, so without this there is no way to find out whether a pairing exists.
func (s *Server) getBluetooth(w http.ResponseWriter, r *http.Request) {
	if s.btBus == nil {
		var none btctl.Status

		writeJSON(w, http.StatusOK, none)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), slowReadDeadline)
	defer cancel()
	// One at a time. A read opens a bus connection, so several
	// browsers polling this page at once would multiply connections on a box with 128
	// MB of RAM, and a read that overlaps a clear sees the list half removed.
	s.btMu.Lock()
	defer s.btMu.Unlock()

	writeJSON(w, http.StatusOK, btctl.Read(ctx, s.btBus))
}

// clearBluetooth forgets every pairing, which stock offered as /clearBluetoothPaired.
// It existed here only as a ten-second hold of a key the Wireless Link Adapter does not
// have, so on that model a phone that had paired once could never be forgotten.
func (s *Server) clearBluetooth(w http.ResponseWriter, r *http.Request) {
	if s.btClear == nil {
		s.badRequest(w, "this speaker has no Bluetooth", nil)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), bluetoothClearDeadline)
	defer cancel()

	s.btMu.Lock()
	defer s.btMu.Unlock()

	err := s.btClear(ctx)
	if err != nil {
		s.fail(w, "clearing Bluetooth pairings failed", err)

		return
	}
	// Removing the pairings drops a streaming phone without a disconnect report, which
	// left the speaker BLUETOOTH and playing. End the stream here as bt-source.sh does on a
	// disconnect: paused, and the idle auto-off takes it to standby.
	s.sourceEvent(ctx, sourceBluetooth, "pause")
	s.chimeEvent("bt-cleared.wav")

	if s.panelNotice != nil {
		s.panelNotice("Pairing list cleared")
	}

	writeOK(w)
}

// forgetBluetooth forgets one paired device, by the address GET /api/bluetooth lists, as
// stock's app forgot one phone at a time. A device that was connected drops its stream
// with it, so that stream ends as on a clear. The body and the pairing are checked before
// the press on the speaker is asked for (onDevice leaves this route to it): a bad or stale
// address is refused at once instead of after a walk to the speaker.
func (s *Server) forgetBluetooth(w http.ResponseWriter, r *http.Request) {
	if s.btForget == nil {
		s.badRequest(w, "this speaker has no Bluetooth", nil)

		return
	}

	var body struct {
		Address string `json:"address"`
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, tinyBody))
	dec.DisallowUnknownFields()

	err := dec.Decode(&body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	_, err = net.ParseMAC(body.Address)
	if err != nil {
		s.badRequest(w, "address is not a Bluetooth address", err)

		return
	}

	if !s.btIsPaired(w, r, body.Address) || !s.pressed(w, r) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), bluetoothClearDeadline)
	defer cancel()

	s.btMu.Lock()
	defer s.btMu.Unlock()
	// Checked again: the pairing may have gone while the speaker waited for the press.
	connected, err := s.btForget(ctx, body.Address)
	if s.btForgetFailed(w, err) {
		return
	}

	if connected {
		s.sourceEvent(ctx, sourceBluetooth, "pause")
	}

	writeOK(w)
}

// btIsPaired answers 404 when no device with address is paired, and false then or when
// BlueZ cannot be asked; without a check (btPaired nil) it lets the request on.
func (s *Server) btIsPaired(w http.ResponseWriter, r *http.Request, address string) bool {
	if s.btPaired == nil {
		return true
	}

	ctx, cancel := context.WithTimeout(r.Context(), slowReadDeadline)
	defer cancel()

	s.btMu.Lock()
	paired, err := s.btPaired(ctx, address)
	s.btMu.Unlock()

	if err == nil && !paired {
		err = btctl.ErrNotPaired
	}

	return !s.btForgetFailed(w, err)
}

// btForgetFailed writes the answer for a failed check or forget: 404 for an address with no
// pairing, 500 otherwise. It reports whether err was one.
func (s *Server) btForgetFailed(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, btctl.ErrNotPaired):
		apiError(w, http.StatusNotFound, "no paired device with that address")
	case err != nil:
		s.fail(w, "forgetting the Bluetooth device failed", err)
	default:
		return false
	}

	return true
}

// bluetoothConnected is bt.sh telling us a device connected (loopback only); it chimes.
func (s *Server) bluetoothConnected(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		apiError(w, http.StatusForbidden, "local only")

		return
	}

	s.chimeEvent("bt-connected.wav")
	writeOK(w)
}

// pairBluetooth opens the Bluetooth pairing window (ADR 0055).
func (s *Server) pairBluetooth(w http.ResponseWriter, r *http.Request) {
	if s.btPair == nil {
		s.badRequest(w, "this speaker has no Bluetooth", nil)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), pairTimeout)
	defer cancel()

	s.btMu.Lock()
	defer s.btMu.Unlock()

	err := s.btPair(ctx)
	if err != nil {
		s.fail(w, "opening the Bluetooth pairing window failed", err)

		return
	}

	writeOK(w)
}

// wifiScan surveys nearby Wi-Fi networks so the settings sheet can offer a pick
// list. An empty list is a valid answer (the UI falls back to manual SSID entry).
// wifiScanReuse is how long one survey answers repeat scans. A survey takes the radio
// off its channel (and on an ST20 holds the BCO for up to 75 s), so a page that keeps
// asking must not keep the speaker surveying.
const wifiScanReuse = 10 * time.Second

// wifiScanCache single-flights surveys: a scan that arrives while one runs waits for it
// and takes its result, as does any scan within wifiScanReuse of the last one.
type wifiScanCache struct {
	mu   sync.Mutex
	at   time.Time
	nets []speaker.WifiNetwork
}

// wifiScan is a POST: it costs the speaker its radio, so it goes through the same
// origin guard as a write.
func (s *Server) wifiScan(w http.ResponseWriter, r *http.Request) {
	s.scan.mu.Lock()
	defer s.scan.mu.Unlock()

	if !s.scan.at.IsZero() && time.Since(s.scan.at) < wifiScanReuse {
		writeJSON(w, http.StatusOK, map[string]any{"networks": s.scan.nets})

		return
	}
	// A site survey runs on the speaker for many seconds; give it room (the speaker
	// client bounds the actual HTTP call).
	ctx, cancel := context.WithTimeout(r.Context(), wifiScanDeadline)
	defer cancel()

	nets, err := s.backend.ScanWifi(ctx)
	if err != nil {
		s.fail(w, "wifi scan failed", err)

		return
	}

	s.scan.at, s.scan.nets = time.Now(), nets
	writeJSON(w, http.StatusOK, map[string]any{"networks": nets})
}

// setWifi joins the speaker to a Wi-Fi network. This can drop the speaker's current
// connection (and this app) while it switches over — the UI warns before calling.
func (s *Server) setWifi(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SSID     string `json:"ssid"`
		Security string `json:"security"`
		Password string `json:"password"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	if strings.TrimSpace(body.SSID) == "" {
		s.badRequest(w, "ssid required", nil)

		return
	}

	err = speakerstate.CheckWifi(body.SSID, body.Security, body.Password)
	if err != nil {
		s.badRequest(w, err.Error(), err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), wifiJoinDeadline)
	defer cancel()

	err = s.backend.SetWifi(ctx, body.SSID, body.Security, body.Password)
	if err != nil {
		s.fail(w, "set wifi failed", err)

		return
	}

	writeOK(w)
}

// WifiProfile is a saved network. ID is an opaque handle for forgetting it; the
// passphrase is never part of it.
type WifiProfile struct {
	ID   string `json:"id"`
	SSID string `json:"ssid"`
}

// ErrWifiProfileCurrent is returned when asked to forget the network in use.
var ErrWifiProfileCurrent = errors.New("the network in use cannot be forgotten")

func (s *Server) savedWifi(w http.ResponseWriter, _ *http.Request) {
	nets := []WifiProfile{}

	if s.wifiProfiles != nil {
		var err error

		nets, err = s.wifiProfiles()
		if err != nil {
			s.fail(w, "saved wifi failed", err)

			return
		}
	}

	writeJSON(w, http.StatusOK, struct {
		Networks   []WifiProfile `json:"networks"`
		JoinFailed string        `json:"joinFailed,omitempty"`
		FellBack   string        `json:"fellBack,omitempty"`
	}{nets, wifiJoinFailed(), wifiReason(wifiFallbackFile)})
}

// wifiJoinFailedFile is where the Wi-Fi manager writes why its last join failed.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var wifiJoinFailedFile = "/run/soundtap/wifi-join-failed"

// wifiFallbackFile is where the Wi-Fi manager keeps why new settings did not take after it went back
// to the last network that worked.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var wifiFallbackFile = "/run/soundtap/wifi-fallback-reason"

func wifiJoinFailed() string { return wifiReason(wifiJoinFailedFile) }

// wifiReason is the one-word reason in file, such as wrong_password, or "" when there is none.
func wifiReason(file string) string {
	b, err := os.ReadFile(file)
	if err != nil || len(b) > 32 {
		return ""
	}

	return strings.TrimSpace(string(b))
}

// dismissWifiFallback clears the notice once it was shown.
func (s *Server) dismissWifiFallback(w http.ResponseWriter, _ *http.Request) {
	err := os.Remove(wifiFallbackFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.fail(w, "dismiss failed", err)

		return
	}

	writeOK(w)
}

func (s *Server) forgetSavedWifi(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil || body.ID == "" {
		s.badRequest(w, "id required", err)

		return
	}

	if s.forgetWifi == nil {
		apiError(w, http.StatusNotImplemented, "saved networks unavailable")

		return
	}

	switch err := s.forgetWifi(body.ID); {
	case errors.Is(err, ErrWifiProfileCurrent):
		apiError(w, http.StatusConflict, err.Error())
	case err != nil:
		s.fail(w, "forget wifi failed", err)
	default:
		writeOK(w)
	}
}

// enterSetup drops the speaker into provisioning mode. On-speaker this means:
// ask the Wi-Fi manager to make wlan1 the setup AP again, keeping the saved network.
// Off-speaker/tests the callback is usually nil, in which case the endpoint stays a
// harmless no-op and reports unsupported.
func (s *Server) enterSetup(w http.ResponseWriter, r *http.Request) {
	if s.setupMode == nil {
		apiError(w, http.StatusNotImplemented, "setup mode unavailable")

		return
	}

	var body struct {
		Confirm bool `json:"confirm"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	if !body.Confirm {
		s.badRequest(w, "confirm must be true", nil)

		return
	}

	err = s.setupMode()
	if err != nil {
		s.fail(w, "enter setup failed", err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "setup"})
}
