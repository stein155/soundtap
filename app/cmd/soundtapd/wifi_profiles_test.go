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

	err = forgetWifiProfile(conf, b64("home"))
	if !errors.Is(err, web.ErrWifiProfileCurrent) {
		t.Fatalf("forgetting the network in use = %v", err)
	}

	err = forgetWifiProfile(conf, b64("cafe"))
	if err != nil {
		t.Fatal(err)
	}

	err = forgetWifiProfile(conf, b64("cafe"))
	if err != nil {
		t.Fatalf("forgetting twice = %v", err)
	}

	nets, _ := savedWifiProfiles(conf)
	if len(nets) != 1 || nets[0].SSID != "home" {
		t.Fatalf("nets = %+v", nets)
	}
}
