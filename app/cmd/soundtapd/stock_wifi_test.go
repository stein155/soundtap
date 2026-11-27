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
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/wifi"
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
	return stockNetwork{ssid: ssid, psk: psk, hexPSK: hexPSK, wep: "", wepIndex: 0}
}

func TestReadStockNetworks(t *testing.T) {
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
			name: "wep with an encrypted key (ADR 0136)",
			xml: profiles(`<profile SSID="Oud" security="wep" wepKey="` + enc("1234567890") +
				`" encrypted="true" lastConnected="300" />`),
			want: stockNetwork{ssid: "Oud", psk: "", hexPSK: "", wep: "1234567890", wepIndex: 0}, bad: false,
		},
		{
			name: "wep with a plain key, as stock's scripts write it",
			xml:  profiles(`<profile SSID="Oud" security="wep" wepKey="0123456789abcdef0123456789" />`),
			want: stockNetwork{
				ssid: "Oud", psk: "", hexPSK: "", wep: "0123456789abcdef0123456789", wepIndex: 0,
			}, bad: false,
		},
		{
			name: "wep with a key that is none", bad: true, want: none,
			xml: profiles(`<profile SSID="Oud" security="wep" wepKey="abcdef" />`),
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
		got, err := readStockNetworks(strings.NewReader(test.xml), testWifiKey(), testWifiIV())
		if test.bad {
			if err == nil || len(got) != 0 {
				t.Errorf("%s: read %+v, want an error", test.name, got)
			}

			continue
		}

		if err != nil || len(got) != 1 || got[0] != test.want {
			t.Errorf("%s: got %+v %v, want %+v", test.name, got, err, test.want)
		}
	}
}

// TestReadStockNetworksTakesEvery holds that every network stock saved is taken, in the order
// to try them. Stock stamps lastConnected from a clock that NTP may not have set yet, so a join
// at boot gets July 2015: such a stamp cannot rank a network below one with a real date.
func TestReadStockNetworksTakesEvery(t *testing.T) {
	t.Parallel()

	enc := func(pass string) string { return encryptStock(t, testWifiKey(), testWifiIV(), pass) }
	wpa := func(ssid, pass, stamp string) string {
		profile := `<profile SSID="` + ssid + `" security="wpa_or_wpa2" passphrase="` + enc(pass) + `" encrypted="true"`
		if stamp != "" {
			profile += ` lastConnected="` + stamp + `"`
		}

		return profile + " />"
	}

	for _, test := range []struct {
		name, xml string
		want      []stockNetwork
		skipped   bool
	}{
		{
			// The field report: the network the speaker was on was joined at boot, before NTP.
			name:    "a pre-2016 stamp is taken as well, after the dated ones",
			xml:     profiles(wpa("Nieuw", "nieuwnieuw", "1436213725"), wpa("Oud", "oudoudoud", "1748502923")),
			want:    []stockNetwork{network("Oud", "oudoudoud", ""), network("Nieuw", "nieuwnieuw", "")},
			skipped: false,
		},
		{
			name: "dated ones newest first, then the rest as stock lists them",
			xml: profiles(
				wpa("Nooit", "nooitnooit", ""),
				wpa("Boot", "bootboot", "1436213720"),
				wpa("Vorig jaar", "vorigjaar", "1735689600"),
				wpa("Rommel", "rommelrommel", "gisteren"),
				wpa("Dit jaar", "ditjaar1", "1767225600")),
			want: []stockNetwork{
				network("Dit jaar", "ditjaar1", ""), network("Vorig jaar", "vorigjaar", ""),
				network("Nooit", "nooitnooit", ""), network("Boot", "bootboot", ""),
				network("Rommel", "rommelrommel", ""),
			},
			skipped: false,
		},
		{
			name: "every kind: wep, open and wpa",
			xml: profiles(
				`<profile SSID="Oud" security="wep" wepKey="`+enc("1234567890")+`" encrypted="true" />`,
				`<profile SSID="Gast" security="open" lastConnected="1748502923" />`,
				wpa("Thuis", "geheim123", "1")),
			want: []stockNetwork{
				network("Gast", "", ""),
				{ssid: "Oud", psk: "", hexPSK: "", wep: "1234567890", wepIndex: 0},
				network("Thuis", "geheim123", ""),
			},
			skipped: false,
		},
		{
			name: "a profile that cannot be taken is left out, the others are not",
			xml: profiles(wpa("Kort", "kort", "1748502923"), `<profile SSID="" security="open" />`,
				wpa("Thuis", "geheim123", "")),
			want:    []stockNetwork{network("Thuis", "geheim123", "")},
			skipped: true,
		},
		{
			name: "a name saved twice counts once, the newer",
			xml: profiles(wpa("Thuis", "oudwachtwoord", "1436213725"),
				wpa("Thuis", "nieuwwachtwoord", "1748502923")),
			want:    []stockNetwork{network("Thuis", "nieuwwachtwoord", "")},
			skipped: false,
		},
	} {
		got, err := readStockNetworks(strings.NewReader(test.xml), testWifiKey(), testWifiIV())
		if !slices.Equal(got, test.want) || (err != nil) != test.skipped {
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

	// Only the one network stock saved is in the known list.
	known, _ := os.ReadFile(conf + ".known")
	if string(known) != "VGh1aXM= Z2VoZWltMTIz -\n" {
		t.Fatalf("wifi.conf.known = %q", known)
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

// TestAdoptStockWifiEveryNetwork is the field report: an ST10 with a few saved networks, the
// one it was on joined at boot with a 2015 stamp. Every network goes into wifi.conf.known, so
// wpa_supplicant joins the one in range; wifi.conf is the one with the newest real date.
//
//nolint:paralleltest // swaps package variables other tests read: stockProfilesPath
func TestAdoptStockWifiEveryNetwork(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	volume := t.TempDir()
	home := filepath.Join(volume, "soundtap")
	_ = os.MkdirAll(home, 0o755)
	conf := filepath.Join(volume, "wifi.conf")
	old := stockProfilesPath

	t.Cleanup(func() { stockProfilesPath = old })

	stockProfilesPath = filepath.Join(volume, "NetworkProfiles.xml")
	enc := func(pass string) string { return encryptStock(t, testWifiKey(), testWifiIV(), pass) }
	wpa := func(ssid, pass, stamp string) string {
		return `<profile SSID="` + ssid + `" security="wpa_or_wpa2" passphrase="` + enc(pass) +
			`" encrypted="true" lastConnected="` + stamp + `" />`
	}
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	adopt := func(entries ...string) []wifi.Record {
		t.Helper()

		_ = os.Remove(conf)
		_ = os.Remove(conf + ".known")
		_ = os.WriteFile(stockProfilesPath, []byte(profiles(entries...)), 0o644)
		_ = os.WriteFile(filepath.Join(home, stockWifiKeyName), slices.Concat(testWifiKey(), testWifiIV()), 0o600)

		if !adoptStockWifi(home, conf, log) {
			t.Fatal("stock networks not adopted")
		}

		known, _ := os.ReadFile(conf + ".known")

		st, err := os.Stat(conf + ".known")
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("wifi.conf.known: %v", err)
		}

		return wifi.ParseKnown(known)
	}

	known := adopt(wpa("Oud", "oudoudoud", "1748502923"), wpa("Nieuw", "nieuwnieuw", "1436213725"))

	got, _ := os.ReadFile(conf)
	if string(got) != "SSID_B64="+b64("Oud")+"\nPSK_B64="+b64("oudoudoud")+"\n" {
		t.Fatalf("wifi.conf = %q", got)
	}

	want := []wifi.Record{
		wifi.NewRecord("Oud", "", "oudoudoud"), wifi.NewRecord("Nieuw", "", "nieuwnieuw"),
	}
	if !slices.Equal(known, want) {
		t.Fatalf("wifi.conf.known = %+v, want %+v", known, want)
	}

	// More networks than the Wi-Fi manager keeps: the first wifi.KnownMax in the order to try.
	entries := make([]string, 0, wifi.KnownMax+2)
	for n := range wifi.KnownMax + 2 {
		entries = append(entries, wpa(fmt.Sprintf("Net %d", n), "wachtwoord", strconv.Itoa(1748502923+n)))
	}

	known = adopt(entries...)
	if len(known) != wifi.KnownMax || known[0].SSIDB64 != b64(fmt.Sprintf("Net %d", wifi.KnownMax+1)) {
		t.Fatalf("wifi.conf.known = %+v", known)
	}
}

func TestAdoptStockBCOWifi(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.DiscardHandler)
	read := func(profile bco.StockProfile, err error) func(context.Context) (bco.StockProfile, error) {
		return func(context.Context) (bco.StockProfile, error) { return profile, err }
	}
	profile := func(ssid, passphrase string) bco.StockProfile {
		return bco.StockProfile{SSID: ssid, Passphrase: passphrase, WEP: false, WEPKeyIndex: 0}
	}
	ctx := context.Background()
	conf := filepath.Join(t.TempDir(), "wifi.conf")

	// A failed read leaves no file: the speaker opens its setup network instead.
	adoptStockBCOWifi(ctx, conf, read(profile("", ""), errNoReplyInTest), noWait, log)

	_, err := os.Stat(conf)
	if err == nil {
		t.Fatal("wifi.conf written after a failed read")
	}

	adoptStockBCOWifi(ctx, conf, read(profile("Home net", "correct horse"), nil), noWait, log)

	want := "SSID_B64=" + base64.StdEncoding.EncodeToString([]byte("Home net")) + "\nPSK_B64=" +
		base64.StdEncoding.EncodeToString([]byte("correct horse")) + "\n"
	if got, _ := os.ReadFile(conf); string(got) != want {
		t.Fatalf("wifi.conf = %q, want %q", got, want)
	}

	if st, _ := os.Stat(conf); st.Mode().Perm() != 0o600 {
		t.Fatalf("wifi.conf mode = %v, want 0600", st.Mode().Perm())
	}

	// A network the owner already chose is never replaced.
	adoptStockBCOWifi(ctx, conf, read(profile("Other", "another one"), nil), noWait, log)

	if got, _ := os.ReadFile(conf); string(got) != want {
		t.Fatalf("wifi.conf was replaced: %q", got)
	}

	// A raw 64-digit key goes in as PSK_HEX, as wpa_supplicant reads it.
	conf = filepath.Join(t.TempDir(), "wifi.conf")
	adoptStockBCOWifi(ctx, conf, read(profile("Home net", strings.Repeat("AB", 32)), nil), noWait, log)

	if got, _ := os.ReadFile(conf); !strings.Contains(string(got), "PSK_HEX="+strings.Repeat("ab", 32)+"\n") {
		t.Fatalf("wifi.conf = %q, want a PSK_HEX line", got)
	}

	// A WEP network goes in with its key and SECURITY=wep (ADR 0136).
	conf = filepath.Join(t.TempDir(), "wifi.conf")
	adoptStockBCOWifi(ctx, conf,
		read(bco.StockProfile{SSID: "Oud", Passphrase: "abcde", WEP: true, WEPKeyIndex: 0}, nil), noWait, log)

	if got, _ := os.ReadFile(conf); !strings.Contains(string(got), "SECURITY=wep\n") ||
		!strings.Contains(string(got), "PSK_B64="+base64.StdEncoding.EncodeToString([]byte("abcde"))+"\n") ||
		strings.Contains(string(got), "WEP_KEYIDX") {
		t.Fatalf("wifi.conf = %q, want the WEP key and SECURITY=wep", got)
	}

	// A WEP key at another index keeps it, so it goes out at the index the access point has it.
	conf = filepath.Join(t.TempDir(), "wifi.conf")
	adoptStockBCOWifi(ctx, conf,
		read(bco.StockProfile{SSID: "Oud", Passphrase: "abcde", WEP: true, WEPKeyIndex: 2}, nil), noWait, log)

	if got, _ := os.ReadFile(conf); !strings.Contains(string(got), "SECURITY=wep\nWEP_KEYIDX=2\n") {
		t.Fatalf("wifi.conf = %q, want WEP_KEYIDX=2", got)
	}

	// An open network goes in without a passphrase, as the Wi-Fi manager reads an open one.
	conf = filepath.Join(t.TempDir(), "wifi.conf")
	adoptStockBCOWifi(ctx, conf, read(profile("Cafe", ""), nil), noWait, log)

	want = "SSID_B64=" + base64.StdEncoding.EncodeToString([]byte("Cafe")) + "\nPSK_B64=\n"
	if got, _ := os.ReadFile(conf); string(got) != want {
		t.Fatalf("wifi.conf = %q, want %q", got, want)
	}
}

// noWait stands in for sleepCtx: no time passes in a test.
func noWait(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }

// Reads that fail in a test, each for a reason that may pass.
var (
	errDumpInTest  = errors.New("BCO memory dump: bad word")
	errOtherInTest = errors.New("still no reply")
)

// TestAdoptStockBCOWifiRetries holds the ST20's one chance to read stock's network: a read
// that fails for a reason that may pass is tried again before the BCO app is replaced, a
// definitive answer is not, and the tries are bounded.
func TestAdoptStockBCOWifiRetries(t *testing.T) {
	t.Parallel()

	notImportable := fmt.Errorf("%w: WEP network", bco.ErrStockProfileNotImportable)
	good := bco.StockProfile{SSID: "Home net", Passphrase: "correct horse", WEP: false, WEPKeyIndex: 0}

	for name, tcase := range map[string]struct {
		errs      []error // one per read; past the end, the read succeeds
		wantReads int
		wantConf  bool
	}{
		"first read works":           {nil, 1, true},
		"BCO app still starting":     {[]error{errNoReplyInTest, errNoReplyInTest}, 3, true},
		"noisy dump, then good":      {[]error{errDumpInTest}, 2, true},
		"not importable: asked once": {[]error{notImportable}, 1, false},
		"transient, then definitive": {[]error{errNoReplyInTest, notImportable}, 2, false},
		"never answers: bounded": {
			[]error{errNoReplyInTest, errOtherInTest, errDumpInTest, errNoReplyInTest, errOtherInTest, errDumpInTest},
			5, false,
		},
	} {
		conf := filepath.Join(t.TempDir(), "wifi.conf")
		reads, waits := 0, 0
		read := func(ctx context.Context) (bco.StockProfile, error) {
			_, ok := ctx.Deadline()
			if !ok {
				t.Errorf("%s: read without a deadline", name)
			}

			reads++
			if reads <= len(tcase.errs) && tcase.errs[reads-1] != nil {
				return bco.StockProfile{}, tcase.errs[reads-1]
			}

			return good, nil
		}
		wait := func(ctx context.Context, d time.Duration) bool {
			waits++

			if d != stockBCORetryDelay {
				t.Errorf("%s: waited %v", name, d)
			}

			return ctx.Err() == nil
		}

		adoptStockBCOWifi(t.Context(), conf, read, wait, quietLog())

		_, err := os.Stat(conf)
		if reads != tcase.wantReads || (err == nil) != tcase.wantConf || waits != reads-1 {
			t.Errorf("%s: %d reads, %d waits, wifi.conf written %v; want %d reads, written %v",
				name, reads, waits, err == nil, tcase.wantReads, tcase.wantConf)
		}
	}

	// A wait cut short (shutdown, or the take-over's own time limit) ends the take-over.
	reads := 0

	adoptStockBCOWifi(t.Context(), filepath.Join(t.TempDir(), "wifi.conf"),
		func(context.Context) (bco.StockProfile, error) {
			reads++

			return bco.StockProfile{}, errNoReplyInTest
		},
		func(context.Context, time.Duration) bool { return false }, quietLog())

	if reads != 1 {
		t.Fatalf("%d reads after the wait was cut short", reads)
	}
}
