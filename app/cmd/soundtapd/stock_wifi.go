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

// stockBCOWifiLimit bounds reading the network from Bose's BCO app.
const stockBCOWifiLimit = time.Minute

// The WPA passphrase lengths, and the length of a raw PSK in hex (as wpa_supplicant reads it).
const (
	wpaPassphraseMin = 8
	wpaPassphraseMax = 63
	wpaHexPSKLen     = 64
)

// Why stock's network is not taken over.
var (
	errStockNoNetwork    = errors.New("no WPA or open network saved")
	errStockNotWPA       = errors.New("passphrase is not a WPA passphrase")
	errStockNotAESBlocks = errors.New("encrypted passphrase is not whole AES blocks")
	errStockWrongKey     = errors.New("passphrase does not decrypt with stock's key")
)

// stockNetwork is one network to join: an empty PSK and hexPSK mean an open network.
type stockNetwork struct {
	ssid, psk, hexPSK string
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
	err := wifi.WriteConf(wifiConf, wifi.Record{
		SSIDB64: base64.StdEncoding.EncodeToString([]byte(network.ssid)),
		PSKB64:  base64.StdEncoding.EncodeToString([]byte(network.psk)),
		HexPSK:  network.hexPSK,
	})
	if err != nil {
		return fmt.Errorf("write wifi.conf: %w", err)
	}

	return nil
}

// adoptStockBCOWifi is adoptStockWifi for the ST20, where Bose's software keeps the network
// in the BCO and not on this processor. It runs while Bose's BCO app is still there, just
// before AutoInstall replaces it: afterwards the speaker reboots onto its own Wi-Fi path
// and needs wifi.conf to join anything. read is bco.ReadStockProfile on the BCO's UART.
func adoptStockBCOWifi(
	ctx context.Context, wifiConf string, read func(context.Context) (bco.StockProfile, error), log *slog.Logger,
) {
	_, err := os.Stat(wifiConf)
	if err == nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, stockBCOWifiLimit)
	defer cancel()

	profile, err := read(ctx)
	if err != nil {
		log.Warn("stock wifi not taken over from the BCO", "err", err)

		return
	}

	network := stockNetwork{ssid: profile.SSID, psk: profile.Passphrase, hexPSK: ""}
	if bco.IsHexPSK(profile.Passphrase) {
		network = stockNetwork{ssid: profile.SSID, psk: "", hexPSK: strings.ToLower(profile.Passphrase)}
	}

	err = saveStockNetwork(wifiConf, network)
	if err != nil {
		log.Warn("stock wifi save", "err", err)

		return
	}

	log.Info("wifi taken over from stock's BCO", "ssid", profile.SSID)
}

// readStockNetwork picks the network stock joined last from its NetworkProfiles.xml:
//
//	<WiFiProfiles><profile SSID="…" security="wpa_or_wpa2" passphrase="…" encrypted="true"
//	  lastConnected="…" priority="…" /></WiFiProfiles>
//
// An encrypted passphrase is base64 of AES-256-CBC with PKCS#7 padding. WEP networks are
// skipped: SoundTap joins WPA and open networks only. A 64-digit hex passphrase is a raw PSK,
// as wpa_supplicant reads it.
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
		return stockNetwork{ssid: profile.SSID, psk: "", hexPSK: ""}, nil
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
		return stockNetwork{ssid: profile.SSID, psk: "", hexPSK: strings.ToLower(pass)}, nil
	case len(pass) >= wpaPassphraseMin && len(pass) <= wpaPassphraseMax:
		return stockNetwork{ssid: profile.SSID, psk: pass, hexPSK: ""}, nil
	default:
		return stockNetwork{}, errStockNotWPA
	}
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
	Encrypted     string `xml:"encrypted,attr"`
	LastConnected string `xml:"lastConnected,attr"`
}

// lastJoinedProfile is the WPA or open network stock joined last; false without one.
func lastJoinedProfile(profiles []stockWifiProfile) (stockWifiProfile, bool) {
	best, bestAt := -1, uint64(0)

	for index, profile := range profiles {
		if profile.SSID == "" || strings.EqualFold(profile.Security, "wep") {
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
