// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//nolint:paralleltest // swaps package variables other tests read: setupAPUp
func TestSetupChordKeepsTheNetworkAndToggles(t *testing.T) {
	dir := t.TempDir()
	up := setupAPUp

	t.Cleanup(func() { setupAPUp = up })

	setupAPUp = filepath.Join(dir, "up")
	conf := filepath.Join(dir, "wifi.conf")

	const saved = "SSID_B64=aG9tZQ==\nPSK_B64=c2VjcmV0\n"

	err := os.WriteFile(conf, []byte(saved), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	restarts, setups := 0, 0
	restart := func() error {
		restarts++

		return nil
	}
	setup := func() error {
		setups++

		return nil
	}

	// First chord: asks for the AP.
	err = toggleSetupAP(conf, restart, setup)
	if err != nil {
		t.Fatal(err)
	}

	if setups != 1 || restarts != 0 {
		t.Fatalf("setups %d, restarts %d", setups, restarts)
	}

	if got, _ := os.ReadFile(conf); string(got) != saved {
		t.Fatalf("wifi.conf changed: %q", got)
	}

	// The AP is up. The second chord goes back.
	err = os.WriteFile(setupAPUp, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = toggleSetupAP(conf, restart, setup)
	if err != nil {
		t.Fatal(err)
	}

	if setups != 1 || restarts != 1 {
		t.Fatalf("setups %d, restarts %d", setups, restarts)
	}

	// With nothing saved there is nowhere to go back to.
	err = os.WriteFile(conf, []byte("SSID_B64=\nPSK_B64=\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = toggleSetupAP(conf, restart, setup)
	if err != nil {
		t.Fatal(err)
	}

	if setups != 2 {
		t.Fatal("no setup AP with nothing saved")
	}
}

// Off the speaker there is no Wi-Fi manager: a restart or a setup request says so and
// does nothing else.
func TestNoWifiManagerOffTheSpeaker(t *testing.T) {
	t.Parallel()

	if wifiMgr.Load() != nil {
		t.Skip("a manager is running")
	}

	err := restartWifi(quietLog())
	if !errors.Is(err, errNoWifi) {
		t.Fatalf("restart = %v", err)
	}

	err = openSetupAP(quietLog())
	if !errors.Is(err, errNoWifi) {
		t.Fatalf("setup = %v", err)
	}
}

// What keeps Wi-Fi from working is in /api/health until it works.
//
//nolint:paralleltest // changes package-wide state other tests read: problems
func TestWifiProblemSurfacesAndClears(t *testing.T) {
	t.Cleanup(func() { problems.clear("wifi") })
	wifiProblem("hostapd failed; setup AP unavailable")

	if got := problems.list(); len(got) != 1 || !strings.Contains(got[0], "hostapd failed") {
		t.Fatalf("problems = %q", got)
	}

	wifiProblem("")

	if got := problems.list(); len(got) != 0 {
		t.Fatalf("a working Wi-Fi left %q", got)
	}
}
