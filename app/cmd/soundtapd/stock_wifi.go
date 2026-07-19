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
		if err := os.Remove(src); err != nil {
			log.Warn("remove stock wifi key", "err", err)
		}
	}()
	if _, err := os.Stat(wifiConf); err == nil {
		return false
	}
	if len(key) != stockWifiKeyLen+stockWifiIVLen {
		log.Warn("stock wifi not taken over", "err", "key file has the wrong size")
		return false
	}
	f, err := os.Open(stockProfilesPath)
	if err != nil {
		log.Warn("stock wifi not taken over", "err", err)
		return false
	}
	defer f.Close()
	n, err := readStockNetwork(io.LimitReader(f, 256<<10), key[:stockWifiKeyLen], key[stockWifiKeyLen:])
	if err != nil {
		log.Warn("stock wifi not taken over", "err", err)
		return false
	}
	if err := saveStockNetwork(wifiConf, n); err != nil {
		log.Warn("stock wifi save", "err", err)
		return false
	}
	log.Info("wifi taken over from stock", "ssid", n.ssid)
	return true
}

// saveStockNetwork writes n as wifi.conf, the form the Wi-Fi manager reads.
func saveStockNetwork(wifiConf string, n stockNetwork) error {
	return wifi.WriteConf(wifiConf, wifi.Record{
		SSIDB64: base64.StdEncoding.EncodeToString([]byte(n.ssid)),
		PSKB64:  base64.StdEncoding.EncodeToString([]byte(n.psk)),
		HexPSK:  n.hexPSK,
	})
}

// adoptStockBCOWifi is adoptStockWifi for the ST20, where Bose's software keeps the network
// in the BCO and not on this processor. It runs while Bose's BCO app is still there, just
// before AutoInstall replaces it: afterwards the speaker reboots onto its own Wi-Fi path
// and needs wifi.conf to join anything. read is bco.ReadStockProfile on the BCO's UART.
func adoptStockBCOWifi(ctx context.Context, wifiConf string, read func(context.Context) (bco.StockProfile, error), log *slog.Logger) {
	if _, err := os.Stat(wifiConf); err == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	p, err := read(ctx)
	if err != nil {
		log.Warn("stock wifi not taken over from the BCO", "err", err)
		return
	}
	n := stockNetwork{ssid: p.SSID, psk: p.Passphrase}
	if bco.IsHexPSK(p.Passphrase) {
		n = stockNetwork{ssid: p.SSID, hexPSK: strings.ToLower(p.Passphrase)}
	}
	if err := saveStockNetwork(wifiConf, n); err != nil {
		log.Warn("stock wifi save", "err", err)
		return
	}
	log.Info("wifi taken over from stock's BCO", "ssid", p.SSID)
}

// readStockNetwork picks the network stock joined last from its NetworkProfiles.xml:
//
//	<WiFiProfiles><profile SSID="…" security="wpa_or_wpa2" passphrase="…" encrypted="true"
//	  lastConnected="…" priority="…" /></WiFiProfiles>
//
// An encrypted passphrase is base64 of AES-256-CBC with PKCS#7 padding. WEP networks are
// skipped: SoundTap joins WPA and open networks only. A 64-digit hex passphrase is a raw PSK,
// as wpa_supplicant reads it.
func readStockNetwork(r io.Reader, key, iv []byte) (stockNetwork, error) {
	var doc struct {
		Profiles []struct {
			SSID          string `xml:"SSID,attr"`
			Security      string `xml:"security,attr"`
			Passphrase    string `xml:"passphrase,attr"`
			Encrypted     string `xml:"encrypted,attr"`
			LastConnected string `xml:"lastConnected,attr"`
		} `xml:"profile"`
	}
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return stockNetwork{}, err
	}
	best, bestAt := -1, uint64(0)
	for i, p := range doc.Profiles {
		if p.SSID == "" || strings.EqualFold(p.Security, "wep") {
			continue
		}
		at, _ := strconv.ParseUint(p.LastConnected, 10, 64)
		if best < 0 || at > bestAt {
			best, bestAt = i, at
		}
	}
	if best < 0 {
		return stockNetwork{}, errors.New("no WPA or open network saved")
	}
	p := doc.Profiles[best]
	if strings.EqualFold(p.Security, "open") {
		return stockNetwork{ssid: p.SSID}, nil
	}
	pass := p.Passphrase
	if p.Encrypted == "true" || p.Encrypted == "1" {
		var err error
		if pass, err = decryptStockPassphrase(pass, key, iv); err != nil {
			return stockNetwork{}, err
		}
	}
	switch {
	case len(pass) == 64 && isHex(pass):
		return stockNetwork{ssid: p.SSID, hexPSK: strings.ToLower(pass)}, nil
	case len(pass) >= 8 && len(pass) <= 63:
		return stockNetwork{ssid: p.SSID, psk: pass}, nil
	}
	return stockNetwork{}, errors.New("passphrase is not a WPA passphrase")
}

// decryptStockPassphrase undoes stock's base64 and AES-256-CBC. A wrong key shows up as bad
// padding or text that is not UTF-8, and is refused rather than saved as a password.
func decryptStockPassphrase(b64 string, key, iv []byte) (string, error) {
	ct, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", err
	}
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return "", errors.New("encrypted passphrase is not whole AES blocks")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)
	pad := int(pt[len(pt)-1])
	if pad == 0 || pad > aes.BlockSize || !bytes.Equal(pt[len(pt)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return "", errors.New("passphrase does not decrypt with stock's key")
	}
	pt = pt[:len(pt)-pad]
	if !utf8.Valid(pt) || bytes.IndexByte(pt, 0) >= 0 {
		return "", errors.New("passphrase does not decrypt with stock's key")
	}
	return string(pt), nil
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}
