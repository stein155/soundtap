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
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/bco"
)

// A made-up key and IV of the same shape as stock's. Stock's own never appear in this repo:
// the installer reads them from the speaker.
func testWifiKey() []byte { return []byte("01234567890123456789012345678901") }

func testWifiIV() []byte { return []byte("9876543210987654") }

// errNoReplyInTest is a BCO that does not answer.
var errNoReplyInTest = errors.New("no reply")

// encryptStock does what stock does to a passphrase: PKCS#7, AES-256-CBC, base64.
func encryptStock(t *testing.T, key, initVector []byte, pass string) string {
	t.Helper()

	pad := aes.BlockSize - len(pass)%aes.BlockSize
	plain := append([]byte(pass), bytes.Repeat([]byte{byte(pad)}, pad)...)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	ciphertext := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, initVector).CryptBlocks(ciphertext, plain)

	return base64.StdEncoding.EncodeToString(ciphertext)
}

func profiles(entries ...string) string {
	return `<?xml version="1.0" encoding="UTF-8" ?>` + "\n<WiFiProfiles>" +
		strings.Join(entries, "") + "</WiFiProfiles>"
}

// network is a stockNetwork; an empty psk and hexPSK are an open one.
func network(ssid, psk, hexPSK string) stockNetwork {
	return stockNetwork{ssid: ssid, psk: psk, hexPSK: hexPSK}
}

func TestReadStockNetwork(t *testing.T) {
	t.Parallel()

	const hexKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	enc := func(pass string) string { return encryptStock(t, testWifiKey(), testWifiIV(), pass) }
	wpa := `security="wpa_or_wpa2"`
	none := network("", "", "")

	for _, test := range []struct {
		name, xml string
		want      stockNetwork
		bad       bool
	}{
		{
			name: "encrypted wpa",
			xml: profiles(`<profile SSID="Thuis" ` + wpa + ` passphrase="` + enc("geheim 123") +
				`" encrypted="true" lastConnected="5" />`),
			want: network("Thuis", "geheim 123", ""), bad: false,
		},
		{
			name: "sixteen characters pad a whole block",
			xml: profiles(`<profile SSID="Thuis" ` + wpa + ` passphrase="` + enc("abcdefghijklmnop") +
				`" encrypted="true" />`),
			want: network("Thuis", "abcdefghijklmnop", ""), bad: false,
		},
		{
			name: "plain passphrase from override-wifi-profile",
			xml:  profiles(`<profile SSID="Bob&apos;s &amp; co" ` + wpa + ` passphrase="it&quot;s me!" />`),
			want: network("Bob's & co", `it"s me!`, ""), bad: false,
		},
		{
			name: "hex psk",
			xml: profiles(`<profile SSID="Thuis" ` + wpa + ` passphrase="` + enc(strings.ToUpper(hexKey)) +
				`" encrypted="true" />`),
			want: network("Thuis", "", hexKey), bad: false,
		},
		{
			name: "open",
			xml:  profiles(`<profile SSID="Gast" security="open" />`),
			want: network("Gast", "", ""), bad: false,
		},
		{
			name: "utf-8",
			xml: profiles(`<profile SSID="Café" ` + wpa + ` passphrase="` + enc("wachtwoord€") +
				`" encrypted="true" />`),
			want: network("Café", "wachtwoord€", ""), bad: false,
		},
		{
			name: "the network joined last wins",
			xml: profiles(
				`<profile SSID="Oud" `+wpa+` passphrase="`+enc("oudoudoud")+
					`" encrypted="true" lastConnected="100" />`,
				`<profile SSID="Nieuw" `+wpa+` passphrase="`+enc("nieuwnieuw")+
					`" encrypted="true" lastConnected="200" />`,
				`<profile SSID="Nooit" `+wpa+` passphrase="`+enc("nooitnooit")+`" encrypted="true" />`),
			want: network("Nieuw", "nieuwnieuw", ""), bad: false,
		},
		{
			name: "wep is passed over",
			xml: profiles(
				`<profile SSID="Oud" security="wep" wepKey="`+enc("1234567890")+
					`" encrypted="true" lastConnected="300" />`,
				`<profile SSID="Thuis" `+wpa+` passphrase="`+enc("geheim123")+
					`" encrypted="true" lastConnected="1" />`),
			want: network("Thuis", "geheim123", ""), bad: false,
		},
		{
			name: "only wep", bad: true, want: none,
			xml: profiles(`<profile SSID="Oud" security="wep" wepKey="abcde" />`),
		},
		{
			name: "wrong key", bad: true, want: none,
			xml: profiles(`<profile SSID="Thuis" ` + wpa + ` passphrase="` +
				encryptStock(t, []byte("99999999999999999999999999999999"), testWifiIV(), "geheim123") +
				`" encrypted="true" />`),
		},
		{
			name: "not base64", bad: true, want: none,
			xml: profiles(`<profile SSID="Thuis" ` + wpa + ` passphrase="***" encrypted="true" />`),
		},
		{
			name: "not whole blocks", bad: true, want: none,
			xml: profiles(`<profile SSID="Thuis" ` + wpa + ` passphrase="AAAA" encrypted="true" />`),
		},
		{
			name: "passphrase too short", bad: true, want: none,
			xml: profiles(`<profile SSID="Thuis" ` + wpa + ` passphrase="` + enc("kort") + `" encrypted="true" />`),
		},
		{name: "no profiles", bad: true, want: none, xml: profiles()},
		{name: "not xml", bad: true, want: none, xml: "Thuis geheim123"},
	} {
		got, err := readStockNetwork(strings.NewReader(test.xml), testWifiKey(), testWifiIV())
		if test.bad {
			if err == nil {
				t.Errorf("%s: read %+v, want an error", test.name, got)
			}

			continue
		}

		if err != nil || got != test.want {
			t.Errorf("%s: got %+v %v, want %+v", test.name, got, err, test.want)
		}
	}
}

//nolint:paralleltest // swaps package variables other tests read: stockProfilesPath
func TestAdoptStockWifi(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	volume := t.TempDir()
	home := filepath.Join(volume, "soundtap")
	_ = os.MkdirAll(home, 0o755)
	conf := filepath.Join(volume, "wifi.conf")
	keyPath := filepath.Join(home, stockWifiKeyName)
	old := stockProfilesPath

	t.Cleanup(func() { stockProfilesPath = old })

	stockProfilesPath = filepath.Join(volume, "NetworkProfiles.xml")

	key := slices.Concat(testWifiKey(), testWifiIV())

	_ = os.WriteFile(stockProfilesPath, []byte(profiles(`<profile SSID="Thuis" security="wpa_or_wpa2" passphrase="`+
		encryptStock(t, testWifiKey(), testWifiIV(), "geheim123")+`" encrypted="true" />`)), 0o644)
	if adoptStockWifi(home, conf, log) {
		t.Fatal("adopted without the installer's key")
	}

	_ = os.WriteFile(keyPath, key[:40], 0o600)

	if adoptStockWifi(home, conf, log) {
		t.Fatal("adopted with a short key file")
	}

	_, err := os.Stat(keyPath)
	if !os.IsNotExist(err) {
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

	fi, err := os.Stat(conf)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("wifi.conf mode %v, %v", fi.Mode().Perm(), err)
	}

	_, err = os.Stat(keyPath)
	if !os.IsNotExist(err) {
		t.Fatal("the key file was left behind")
	}

	// A speaker that already has a network keeps it, and the key file still goes.
	_ = os.WriteFile(keyPath, key, 0o600)

	if adoptStockWifi(home, conf, log) {
		t.Fatal("overwrote an existing wifi.conf")
	}

	if got2, _ := os.ReadFile(conf); !bytes.Equal(got2, got) {
		t.Fatal("wifi.conf changed")
	}

	_, err = os.Stat(keyPath)
	if !os.IsNotExist(err) {
		t.Fatal("key file left behind when wifi.conf existed")
	}
}

func TestAdoptStockBCOWifi(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.DiscardHandler)
	read := func(profile bco.StockProfile, err error) func(context.Context) (bco.StockProfile, error) {
		return func(context.Context) (bco.StockProfile, error) { return profile, err }
	}
	profile := func(ssid, passphrase string) bco.StockProfile {
		return bco.StockProfile{SSID: ssid, Passphrase: passphrase}
	}
	ctx := context.Background()
	conf := filepath.Join(t.TempDir(), "wifi.conf")

	// A failed read leaves no file: the speaker opens its setup network instead.
	adoptStockBCOWifi(ctx, conf, read(profile("", ""), errNoReplyInTest), log)

	_, err := os.Stat(conf)
	if err == nil {
		t.Fatal("wifi.conf written after a failed read")
	}

	adoptStockBCOWifi(ctx, conf, read(profile("Home net", "correct horse"), nil), log)

	want := "SSID_B64=" + base64.StdEncoding.EncodeToString([]byte("Home net")) + "\nPSK_B64=" +
		base64.StdEncoding.EncodeToString([]byte("correct horse")) + "\n"
	if got, _ := os.ReadFile(conf); string(got) != want {
		t.Fatalf("wifi.conf = %q, want %q", got, want)
	}

	if st, _ := os.Stat(conf); st.Mode().Perm() != 0o600 {
		t.Fatalf("wifi.conf mode = %v, want 0600", st.Mode().Perm())
	}

	// A network the owner already chose is never replaced.
	adoptStockBCOWifi(ctx, conf, read(profile("Other", "another one"), nil), log)

	if got, _ := os.ReadFile(conf); string(got) != want {
		t.Fatalf("wifi.conf was replaced: %q", got)
	}

	// A raw 64-digit key goes in as PSK_HEX, as wpa_supplicant reads it.
	conf = filepath.Join(t.TempDir(), "wifi.conf")
	adoptStockBCOWifi(ctx, conf, read(profile("Home net", strings.Repeat("AB", 32)), nil), log)

	if got, _ := os.ReadFile(conf); !strings.Contains(string(got), "PSK_HEX="+strings.Repeat("ab", 32)+"\n") {
		t.Fatalf("wifi.conf = %q, want a PSK_HEX line", got)
	}
}
