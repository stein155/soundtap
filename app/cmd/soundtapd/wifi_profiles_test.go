// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/web"
	"github.com/stein155/soundtap/app/internal/wifi"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestSavedWifiListsNamesOnly(t *testing.T) {
	t.Parallel()

	conf := filepath.Join(t.TempDir(), "wifi.conf")

	known := b64("home") + " " + b64("hunter2hunter2") + " -\n" + b64("cafe") + " - -\n"

	err := os.WriteFile(conf+".known", []byte(known), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	nets, err := savedWifiProfiles(conf)
	if err != nil || len(nets) != 2 || nets[0].SSID != "home" || nets[1].SSID != "cafe" {
		t.Fatalf("nets = %+v, %v", nets, err)
	}

	for _, n := range nets {
		if strings.Contains(n.ID+n.SSID, "hunter") || strings.Contains(n.ID, b64("hunter2hunter2")) {
			t.Fatalf("passphrase leaked: %+v", n)
		}
	}

	if nets, _ := savedWifiProfiles(filepath.Join(t.TempDir(), "none")); nets == nil || len(nets) != 0 {
		t.Fatalf("no file must give an empty list, got %#v", nets)
	}
}

// joinedTo is a joined() for forgetWifiProfile: the network joined now ("" for none).
func joinedTo(ssid string) func() (string, bool) {
	return func() (string, bool) { return ssid, true }
}

func TestForgetWifiKeepsTheNetworkInUse(t *testing.T) {
	t.Parallel()

	conf := filepath.Join(t.TempDir(), "wifi.conf")

	known := b64("home") + " " + b64("pw12345678") + " -\n" + b64("cafe") + " - -\n"

	err := os.WriteFile(conf, []byte("SSID_B64="+b64("home")+"\nPSK_B64="+b64("pw12345678")+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(conf+".known", []byte(known), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = forgetWifiProfile(conf, b64("home"), joinedTo("home"))
	if !errors.Is(err, web.ErrWifiProfileCurrent) {
		t.Fatalf("forgetting the network in use = %v", err)
	}

	// without a wpa_supplicant to ask, wifi.conf's network counts as in use
	err = forgetWifiProfile(conf, b64("home"), func() (string, bool) { return "", false })
	if !errors.Is(err, web.ErrWifiProfileCurrent) {
		t.Fatalf("forgetting wifi.conf's network, nothing known = %v", err)
	}

	err = forgetWifiProfile(conf, b64("cafe"), joinedTo("home"))
	if err != nil {
		t.Fatal(err)
	}

	err = forgetWifiProfile(conf, b64("cafe"), joinedTo("home"))
	if err != nil {
		t.Fatalf("forgetting twice = %v", err)
	}

	nets, _ := savedWifiProfiles(conf)
	if len(nets) != 1 || nets[0].SSID != "home" {
		t.Fatalf("nets = %+v", nets)
	}
}

// The speaker fell back from the test network, still named in wifi.conf, to home: only home is in
// use. The test network is forgotten, wifi.conf and wifi.conf.good then name home, and home itself
// still cannot be forgotten.
func TestForgetWifiTheNetworkFallenBackFrom(t *testing.T) {
	t.Parallel()

	conf := filepath.Join(t.TempDir(), "wifi.conf")
	test, home := wifi.NewRecord("test", wifi.SecurityWEP, "abcde"), wifi.NewRecord("home", "", "pw12345678")

	for path, rec := range map[string]wifi.Record{conf: test, conf + ".good": test} {
		err := wifi.WriteConf(path, rec)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := wifi.Remember(conf+".known", home, test, wifi.NewRecord("cafe", "", ""))
	if err != nil {
		t.Fatal(err)
	}

	err = forgetWifiProfile(conf, b64("test"), joinedTo("home"))
	if err != nil {
		t.Fatalf("forgetting the network fallen back from = %v", err)
	}

	nets, _ := savedWifiProfiles(conf)
	if len(nets) != 2 || nets[0].SSID != "home" || nets[1].SSID != "cafe" {
		t.Fatalf("nets = %+v", nets)
	}

	for _, path := range []string{conf, conf + ".good"} {
		b, _ := os.ReadFile(path)
		if got := wifi.ParseConf(b); got != home {
			t.Errorf("%s = %+v; want home", filepath.Base(path), got)
		}
	}

	err = forgetWifiProfile(conf, b64("home"), joinedTo("home"))
	if !errors.Is(err, web.ErrWifiProfileCurrent) {
		t.Fatalf("forgetting the network joined now = %v", err)
	}
}

// Joined to nothing (the setup AP up): wifi.conf's network can be forgotten, wifi.conf then names
// the newest one left, a wifi.conf.good of another network stays; the last one leaves none.
func TestForgetWifiWhileJoinedToNone(t *testing.T) {
	t.Parallel()

	conf := filepath.Join(t.TempDir(), "wifi.conf")
	lab, cafe := wifi.NewRecord("lab", "", "labpass12"), wifi.NewRecord("cafe", "", "")

	err := wifi.WriteConf(conf, lab)
	if err == nil {
		err = wifi.WriteConf(conf+".good", cafe)
	}

	if err == nil {
		err = wifi.Remember(conf+".known", lab, cafe)
	}

	if err != nil {
		t.Fatal(err)
	}

	err = forgetWifiProfile(conf, b64("lab"), joinedTo(""))
	if err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(conf)
	if got := wifi.ParseConf(b); got != cafe {
		t.Errorf("wifi.conf = %+v; want cafe", got)
	}

	if b, _ := os.ReadFile(conf + ".good"); wifi.ParseConf(b) != cafe {
		t.Errorf("wifi.conf.good = %q; want it kept", b)
	}

	err = forgetWifiProfile(conf, b64("cafe"), joinedTo(""))
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(conf); wifi.ParseConf(b).SSIDB64 != "" {
		t.Errorf("wifi.conf = %q; want no network", b)
	}

	_, err = os.Stat(conf + ".good")
	if !os.IsNotExist(err) {
		t.Errorf("wifi.conf.good still there: %v", err)
	}
}
