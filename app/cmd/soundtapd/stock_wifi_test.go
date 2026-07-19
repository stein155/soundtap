// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/bco"
)

// A made-up key and IV of the same shape as stock's. Stock's own never appear in this repo:
// the installer reads them from the speaker.
var (
	testWifiKey = []byte("01234567890123456789012345678901")
	testWifiIV  = []byte("9876543210987654")
)

// encryptStock does what stock does to a passphrase: PKCS#7, AES-256-CBC, base64.
func encryptStock(t *testing.T, key, iv []byte, pass string) string {
	t.Helper()
	pad := aes.BlockSize - len(pass)%aes.BlockSize
	pt := append([]byte(pass), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ct := make([]byte, len(pt))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, pt)
	return base64.StdEncoding.EncodeToString(ct)
}

func profiles(entries ...string) string {
	return `<?xml version="1.0" encoding="UTF-8" ?>` + "\n<WiFiProfiles>" + strings.Join(entries, "") + "</WiFiProfiles>"
}

func TestReadStockNetwork(t *testing.T) {
	const hexKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	enc := func(p string) string { return encryptStock(t, testWifiKey, testWifiIV, p) }
	for _, c := range []struct {
		name, xml string
		want      stockNetwork
		bad       bool
	}{
		{name: "encrypted wpa",
			xml:  profiles(`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="` + enc("geheim 123") + `" encrypted="true" lastConnected="5" />`),
			want: stockNetwork{ssid: "Thuis", psk: "geheim 123"}},
		{name: "sixteen characters pad a whole block",
			xml:  profiles(`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="` + enc("abcdefghijklmnop") + `" encrypted="true" />`),
			want: stockNetwork{ssid: "Thuis", psk: "abcdefghijklmnop"}},
		{name: "plain passphrase from override-wifi-profile",
			xml:  profiles(`<profile SSID="Bob&apos;s &amp; co" security="wpa_or_wpa2" passphrase="it&quot;s me!" />`),
			want: stockNetwork{ssid: "Bob's & co", psk: `it"s me!`}},
		{name: "hex psk",
			xml:  profiles(`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="` + enc(strings.ToUpper(hexKey)) + `" encrypted="true" />`),
			want: stockNetwork{ssid: "Thuis", hexPSK: hexKey}},
		{name: "open",
			xml:  profiles(`<profile SSID="Gast" security="open" />`),
			want: stockNetwork{ssid: "Gast"}},
		{name: "utf-8",
			xml:  profiles(`<profile SSID="Café" security="wpa_or_wpa2" passphrase="` + enc("wachtwoord€") + `" encrypted="true" />`),
			want: stockNetwork{ssid: "Café", psk: "wachtwoord€"}},
		{name: "the network joined last wins",
			xml: profiles(
				`<profile SSID="Oud" security="wpa_or_wpa2" passphrase="`+enc("oudoudoud")+`" encrypted="true" lastConnected="100" />`,
				`<profile SSID="Nieuw" security="wpa_or_wpa2" passphrase="`+enc("nieuwnieuw")+`" encrypted="true" lastConnected="200" />`,
				`<profile SSID="Nooit" security="wpa_or_wpa2" passphrase="`+enc("nooitnooit")+`" encrypted="true" />`),
			want: stockNetwork{ssid: "Nieuw", psk: "nieuwnieuw"}},
		{name: "wep is passed over",
			xml: profiles(
				`<profile SSID="Oud" security="wep" wepKey="`+enc("1234567890")+`" encrypted="true" lastConnected="300" />`,
				`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="`+enc("geheim123")+`" encrypted="true" lastConnected="1" />`),
			want: stockNetwork{ssid: "Thuis", psk: "geheim123"}},
		{name: "only wep", bad: true,
			xml: profiles(`<profile SSID="Oud" security="wep" wepKey="abcde" />`)},
		{name: "wrong key", bad: true,
			xml: profiles(`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="` +
				encryptStock(t, []byte("99999999999999999999999999999999"), testWifiIV, "geheim123") + `" encrypted="true" />`)},
		{name: "not base64", bad: true,
			xml: profiles(`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="***" encrypted="true" />`)},
		{name: "not whole blocks", bad: true,
			xml: profiles(`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="AAAA" encrypted="true" />`)},
		{name: "passphrase too short", bad: true,
			xml: profiles(`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="` + enc("kort") + `" encrypted="true" />`)},
		{name: "no profiles", bad: true, xml: profiles()},
		{name: "not xml", bad: true, xml: "Thuis geheim123"},
	} {
		got, err := readStockNetwork(strings.NewReader(c.xml), testWifiKey, testWifiIV)
		if c.bad {
			if err == nil {
				t.Errorf("%s: read %+v, want an error", c.name, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %+v %v, want %+v", c.name, got, err, c.want)
		}
	}
}

func TestAdoptStockWifi(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	nv := t.TempDir()
	home := filepath.Join(nv, "soundtap")
	_ = os.MkdirAll(home, 0o755)
	conf := filepath.Join(nv, "wifi.conf")
	keyPath := filepath.Join(home, stockWifiKeyName)
	old := stockProfilesPath
	t.Cleanup(func() { stockProfilesPath = old })
	stockProfilesPath = filepath.Join(nv, "NetworkProfiles.xml")
	key := append(append([]byte{}, testWifiKey...), testWifiIV...)

	_ = os.WriteFile(stockProfilesPath, []byte(profiles(`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="`+
		encryptStock(t, testWifiKey, testWifiIV, "geheim123")+`" encrypted="true" />`)), 0o644)
	if adoptStockWifi(home, conf, log) {
		t.Fatal("adopted without the installer's key")
	}

	_ = os.WriteFile(keyPath, key[:40], 0o600)
	if adoptStockWifi(home, conf, log) {
		t.Fatal("adopted with a short key file")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("a bad key file was left behind")
	}

	_ = os.WriteFile(keyPath, key, 0o600)
	if !adoptStockWifi(home, conf, log) {
		t.Fatal("stock network not adopted")
	}
	got, _ := os.ReadFile(conf)
	if string(got) != "SSID_B64=VGh1aXM=\nPSK_B64=Z2VoZWltMTIz\n" {
		t.Fatalf("wifi.conf = %q", got)
	}
	if fi, err := os.Stat(conf); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("wifi.conf mode %v, %v", fi.Mode().Perm(), err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("the key file was left behind")
	}

	// A speaker that already has a network keeps it, and the key file still goes.
	_ = os.WriteFile(keyPath, key, 0o600)
	if adoptStockWifi(home, conf, log) {
		t.Fatal("overwrote an existing wifi.conf")
	}
	if got2, _ := os.ReadFile(conf); string(got2) != string(got) {
		t.Fatal("wifi.conf changed")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("key file left behind when wifi.conf existed")
	}
}

func TestAdoptStockBCOWifi(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	read := func(p bco.StockProfile, err error) func(context.Context) (bco.StockProfile, error) {
		return func(context.Context) (bco.StockProfile, error) { return p, err }
	}
	conf := filepath.Join(t.TempDir(), "wifi.conf")

	// A failed read leaves no file: the speaker opens its setup network instead.
	adoptStockBCOWifi(context.Background(), conf, read(bco.StockProfile{}, errors.New("no reply")), log)
	if _, err := os.Stat(conf); err == nil {
		t.Fatal("wifi.conf written after a failed read")
	}

	adoptStockBCOWifi(context.Background(), conf, read(bco.StockProfile{SSID: "Home net", Passphrase: "correct horse"}, nil), log)
	want := "SSID_B64=" + base64.StdEncoding.EncodeToString([]byte("Home net")) + "\nPSK_B64=" +
		base64.StdEncoding.EncodeToString([]byte("correct horse")) + "\n"
	if got, _ := os.ReadFile(conf); string(got) != want {
		t.Fatalf("wifi.conf = %q, want %q", got, want)
	}
	if st, _ := os.Stat(conf); st.Mode().Perm() != 0o600 {
		t.Fatalf("wifi.conf mode = %v, want 0600", st.Mode().Perm())
	}

	// A network the owner already chose is never replaced.
	adoptStockBCOWifi(context.Background(), conf, read(bco.StockProfile{SSID: "Other", Passphrase: "another one"}, nil), log)
	if got, _ := os.ReadFile(conf); string(got) != want {
		t.Fatalf("wifi.conf was replaced: %q", got)
	}

	// A raw 64-digit key goes in as PSK_HEX, as wpa_supplicant reads it.
	conf = filepath.Join(t.TempDir(), "wifi.conf")
	adoptStockBCOWifi(context.Background(), conf, read(bco.StockProfile{SSID: "Home net", Passphrase: strings.Repeat("AB", 32)}, nil), log)
	if got, _ := os.ReadFile(conf); !strings.Contains(string(got), "PSK_HEX="+strings.Repeat("ab", 32)+"\n") {
		t.Fatalf("wifi.conf = %q, want a PSK_HEX line", got)
	}
}
