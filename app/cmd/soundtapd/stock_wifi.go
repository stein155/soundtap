// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/wifi"
)

// stockWifiKeyName is where the installer leaves the key and IV it took from stock's
// NetManager on a first install, next to soundtapd's home on /mnt/nv.
const stockWifiKeyName = "stock-wifi-key"

// stockWifiKeyLen and stockWifiIVLen are the AES-256 key and CBC IV in that file, in that order.
const (
	stockWifiKeyLen = 32
	stockWifiIVLen  = aes.BlockSize
)

// stockProfilesLimit bounds how much of stock's NetworkProfiles.xml is read.
const stockProfilesLimit = 256 << 10

// Bounds of the ST20's take-over from stock's BCO. Stock's BCO app may still be starting,
// or busy on its console, when the first look comes: a read that gets no reply or a garbled
// dump is tried again. The whole take-over is capped, so the BCO install never waits on it
// for longer than stockBCOImportLimit.
const (
	stockBCOReadTries   = 5
	stockBCORetryDelay  = 30 * time.Second
	stockBCOReadLimit   = 90 * time.Second
	stockBCOImportLimit = 5 * time.Minute
)

// The WPA passphrase lengths, and the length of a raw PSK in hex (as wpa_supplicant reads it).
const (
	wpaPassphraseMin = 8
	wpaPassphraseMax = 63
	wpaHexPSKLen     = 64
)

// Why stock's network is not taken over.
var (
	errStockNoNetwork    = errors.New("no network saved")
	errStockNotWEP       = errors.New("WEP key is not a WEP key")
	errStockNotWPA       = errors.New("passphrase is not a WPA passphrase")
	errStockNotAESBlocks = errors.New("encrypted passphrase is not whole AES blocks")
	errStockWrongKey     = errors.New("passphrase does not decrypt with stock's key")
)

// stockNetwork is one network to join: an empty PSK and hexPSK mean an open network; a WEP
// network has its key in wep, and in wepIndex the index stock transmits it at (ADR 0136).
type stockNetwork struct {
	ssid, psk, hexPSK, wep string
	wepIndex               int
}

// adoptStockWifi turns the network stock was on into wifi.conf on the first boot after an
// install from Bose's software, so the speaker joins the home network instead of opening
// its setup AP. It runs before the Wi-Fi manager starts and reports whether it wrote
// wifi.conf. The key file is removed either way.
func adoptStockWifi(home, wifiConf string, log *slog.Logger) bool {
	src := filepath.Join(home, stockWifiKeyName)

	key, err := os.ReadFile(src)
	if err != nil {
		return false
	}
	defer func() {
		removeErr := os.Remove(src)
		if removeErr != nil {
			log.Warn("remove stock wifi key", "err", removeErr)
		}
	}()

	_, statErr := os.Stat(wifiConf)
	if statErr == nil {
		return false
	}

	if len(key) != stockWifiKeyLen+stockWifiIVLen {
		log.Warn("stock wifi not taken over", "err", "key file has the wrong size")

		return false
	}

	profiles, err := os.Open(stockProfilesPath)
	if err != nil {
		log.Warn("stock wifi not taken over", "err", err)

		return false
	}
	defer profiles.Close()

	network, err := readStockNetwork(io.LimitReader(profiles, stockProfilesLimit),
		key[:stockWifiKeyLen], key[stockWifiKeyLen:])
	if err != nil {
		log.Warn("stock wifi not taken over", "err", err)

		return false
	}

	saveErr := saveStockNetwork(wifiConf, network)
	if saveErr != nil {
		log.Warn("stock wifi save", "err", saveErr)

		return false
	}

	log.Info("wifi taken over from stock", "ssid", network.ssid)

	return true
}

// saveStockNetwork writes network as wifi.conf, the form the Wi-Fi manager reads.
func saveStockNetwork(wifiConf string, network stockNetwork) error {
	rec := wifi.Record{
		SSIDB64:     base64.StdEncoding.EncodeToString([]byte(network.ssid)),
		PSKB64:      base64.StdEncoding.EncodeToString([]byte(network.psk)),
		HexPSK:      network.hexPSK,
		Security:    "",
		WEPKeyIndex: 0,
	}
	if network.wep != "" {
		rec = wifi.NewRecord(network.ssid, wifi.SecurityWEP, network.wep)
		rec.WEPKeyIndex = network.wepIndex
	}

	err := wifi.WriteConf(wifiConf, rec)
	if err != nil {
		return fmt.Errorf("write wifi.conf: %w", err)
	}

	return nil
}

// adoptStockBCOWifi is adoptStockWifi for the ST20, where Bose's software keeps the network
// in the BCO and not on this processor. It runs while Bose's BCO app is still there, before
// AutoInstall replaces it: afterwards the speaker reboots onto its own Wi-Fi path, needs
// wifi.conf to join anything, and stock's settings can no longer be read. read is
// bco.ReadStockProfile on the BCO's UART, wait sleepCtx.
//
// It returns once the read gave an answer: a network written to wifi.conf, or
// bco.ErrStockProfileNotImportable (nothing stock saved can be taken). Any other error is
// tried again, stockBCOReadTries times in all and within stockBCOImportLimit, and then given
// up so the install goes ahead; the owner then sets the network up on the setup network.
func adoptStockBCOWifi(
	ctx context.Context, wifiConf string, read func(context.Context) (bco.StockProfile, error),
	wait func(context.Context, time.Duration) bool, log *slog.Logger,
) {
	_, err := os.Stat(wifiConf)
	if err == nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, stockBCOImportLimit)
	defer cancel()

	for try := 1; ; try++ {
		readCtx, readCancel := context.WithTimeout(ctx, stockBCOReadLimit)
		profile, err := read(readCtx)

		readCancel()

		switch {
		case err == nil:
			saveStockBCOProfile(wifiConf, profile, try, log)

			return
		case errors.Is(err, bco.ErrStockProfileNotImportable):
			log.Warn("stock wifi not taken over from the BCO; the speaker opens its setup network", "err", err)

			return
		case try >= stockBCOReadTries || ctx.Err() != nil:
			log.Warn("stock wifi not taken over from the BCO, giving up; the speaker opens its setup network",
				"err", err, "tries", try)

			return
		}

		log.Warn("stock wifi read from the BCO failed, trying again before stock's BCO app is replaced",
			"err", err, "try", try, "in", stockBCORetryDelay)

		if !wait(ctx, stockBCORetryDelay) {
			log.Warn("stock wifi not taken over from the BCO, giving up; the speaker opens its setup network",
				"err", "out of time", "tries", try)

			return
		}
	}
}

// saveStockBCOProfile writes the network read from stock's BCO as wifi.conf.
func saveStockBCOProfile(wifiConf string, profile bco.StockProfile, try int, log *slog.Logger) {
	network := stockNetwork{ssid: profile.SSID, psk: profile.Passphrase, hexPSK: "", wep: "", wepIndex: 0}

	switch {
	case profile.WEP:
		network = stockNetwork{
			ssid: profile.SSID, psk: "", hexPSK: "", wep: profile.Passphrase, wepIndex: profile.WEPKeyIndex,
		}
	case bco.IsHexPSK(profile.Passphrase):
		network = stockNetwork{
			ssid: profile.SSID, psk: "", hexPSK: strings.ToLower(profile.Passphrase), wep: "", wepIndex: 0,
		}
	default:
		// A WPA passphrase, or an open network: as read.
	}

	err := saveStockNetwork(wifiConf, network)
	if err != nil {
		log.Warn("stock wifi save", "err", err)

		return
	}

	log.Info("wifi taken over from stock's BCO", "ssid", profile.SSID, "try", try)
}

// readStockNetwork picks the network stock joined last from its NetworkProfiles.xml:
//
//	<WiFiProfiles><profile SSID="…" security="wpa_or_wpa2" passphrase="…" encrypted="true"
//	  lastConnected="…" priority="…" /></WiFiProfiles>
//
// An encrypted passphrase is base64 of AES-256-CBC with PKCS#7 padding. A 64-digit hex
// passphrase is a raw PSK, as wpa_supplicant reads it. A WEP network keeps its key in wepKey,
// as hex digits stock's own scripts write (set-wifi.sh, override-wifi-profile): taken as it
// is when it is a WEP key, else decrypted like a passphrase when the profile is encrypted
// (ADR 0136).
func readStockNetwork(src io.Reader, key, initVector []byte) (stockNetwork, error) {
	var doc stockWifiProfiles

	err := xml.NewDecoder(src).Decode(&doc)
	if err != nil {
		return stockNetwork{}, fmt.Errorf("stock's network profiles: %w", err)
	}

	profile, ok := lastJoinedProfile(doc.Profiles)
	if !ok {
		return stockNetwork{}, errStockNoNetwork
	}

	if strings.EqualFold(profile.Security, "open") {
		return stockNetwork{ssid: profile.SSID, psk: "", hexPSK: "", wep: "", wepIndex: 0}, nil
	}

	if strings.EqualFold(profile.Security, "wep") {
		return stockWEPNetwork(&profile, key, initVector)
	}

	pass := profile.Passphrase
	if profile.Encrypted == "true" || profile.Encrypted == "1" {
		pass, err = decryptStockPassphrase(pass, key, initVector)
		if err != nil {
			return stockNetwork{}, err
		}
	}

	switch {
	case len(pass) == wpaHexPSKLen && isHex(pass):
		return stockNetwork{ssid: profile.SSID, psk: "", hexPSK: strings.ToLower(pass), wep: "", wepIndex: 0}, nil
	case len(pass) >= wpaPassphraseMin && len(pass) <= wpaPassphraseMax:
		return stockNetwork{ssid: profile.SSID, psk: pass, hexPSK: "", wep: "", wepIndex: 0}, nil
	default:
		return stockNetwork{}, errStockNotWPA
	}
}

// stockWEPNetwork is a WEP profile's network: its wepKey as it stands when that is a WEP key,
// else decrypted with stock's key when the profile says it is encrypted.
func stockWEPNetwork(profile *stockWifiProfile, key, initVector []byte) (stockNetwork, error) {
	wep := profile.WEPKey
	if !wifi.IsWEPKey(wep) && (profile.Encrypted == "true" || profile.Encrypted == "1") {
		plain, err := decryptStockPassphrase(wep, key, initVector)
		if err != nil {
			return stockNetwork{}, err
		}

		wep = plain
	}

	if !wifi.IsWEPKey(wep) {
		return stockNetwork{}, errStockNotWEP
	}

	return stockNetwork{ssid: profile.SSID, psk: "", hexPSK: "", wep: wep, wepIndex: 0}, nil
}

// stockWifiProfiles is stock's NetworkProfiles.xml.
type stockWifiProfiles struct {
	Profiles []stockWifiProfile `xml:"profile"`
}

// stockWifiProfile is one network in it.
type stockWifiProfile struct {
	SSID          string `xml:"SSID,attr"`
	Security      string `xml:"security,attr"`
	Passphrase    string `xml:"passphrase,attr"`
	WEPKey        string `xml:"wepKey,attr"`
	Encrypted     string `xml:"encrypted,attr"`
	LastConnected string `xml:"lastConnected,attr"`
}

// lastJoinedProfile is the network stock joined last; false without one.
func lastJoinedProfile(profiles []stockWifiProfile) (stockWifiProfile, bool) {
	best, bestAt := -1, uint64(0)

	for index, profile := range profiles {
		if profile.SSID == "" {
			continue
		}

		at, _ := strconv.ParseUint(profile.LastConnected, 10, 64)
		if best < 0 || at > bestAt {
			best, bestAt = index, at
		}
	}

	if best < 0 {
		var none stockWifiProfile

		return none, false
	}

	return profiles[best], true
}

// decryptStockPassphrase undoes stock's base64 and AES-256-CBC. A wrong key shows up as bad
// padding or text that is not UTF-8, and is refused rather than saved as a password.
func decryptStockPassphrase(b64 string, key, initVector []byte) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", fmt.Errorf("encrypted passphrase: %w", err)
	}

	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", errStockNotAESBlocks
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("stock's key: %w", err)
	}

	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, initVector).CryptBlocks(plain, ciphertext)

	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > aes.BlockSize || !bytes.Equal(plain[len(plain)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return "", errStockWrongKey
	}

	plain = plain[:len(plain)-pad]
	if !utf8.Valid(plain) || bytes.IndexByte(plain, 0) >= 0 {
		return "", errStockWrongKey
	}

	return string(plain), nil
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)

	return err == nil
}
