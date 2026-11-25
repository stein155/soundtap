// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/web"
)

// The secrets a speaker holds, each in the file that holds it and again in the logs and
// boot records the support bundle carries.
const (
	homeSSID    = "HomeNet Upstairs"
	homeKey     = "correct horse battery"
	atticSSID   = "Zolder-5G"
	atticKey    = "Tr0ub4dor&3xyz"
	cafeSSID    = "Café Ünïcode"
	wpaPMK      = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"
	knownHex    = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	neighbour   = "BuurmanWifi"
	mqttPass    = "Mqtt-Pa55word!"
	spotifyUser = "anna.devries.1987"
	spotifyBlob = "QVVUSEJMT0Itc2VjcmV0LWJsb2ItZm9yLXRlc3Rpbmc="
	confirmTok  = "c0nf1rm-7f3e9a2b5d"
	household   = "hh-tok-91b2c3d4e5f6"
	bearer      = "eyJhbGciOiJIUzI1NiJ9.cGF5bG9hZA.c2ln"
	apiKey      = "AKIA-test-1234567"
	streamPass  = "streampass"
	publicIPv4  = "9.9.9.9"
	publicIPv6  = "2606:4700:4700::1111"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// seedSpeaker writes a fake root: /tmp, /run and /mnt/nv as a speaker has them, every
// file the bundle reads seeded with the secrets above. It returns soundtapd's home.
func seedSpeaker(t *testing.T, root string) string {
	t.Helper()

	home := filepath.Join(root, "mnt", "nv", "soundtap")
	// A log line that names every secret outright, in free text where no key= pattern
	// can find it: only knowing the value catches these.
	leaks := strings.Join([]string{
		"[wifi] retrying " + homeSSID + " with " + homeKey,
		"[wifi] saved " + atticSSID + " (" + atticKey + "), " + cafeSSID,
		"[mqtt] broker refused " + mqttPass,
		"[spotify] Authenticated as " + spotifyUser + " blob " + spotifyBlob,
		"[confirm] press token " + confirmTok + " rotated",
		"[api] old household " + household + " ignored",
		"[wifi] pmk " + wpaPMK + " known " + knownHex,
	}, "\n")
	// And the forms a credential takes in a log when nobody told the bundle about it.
	patterns := strings.Join([]string{
		"wpa_supplicant up (ssid=" + homeSSID + ")",
		"wlan0: Trying to associate with SSID '" + neighbour + "'",
		"SSID_B64=" + b64(homeSSID) + " PSK_B64=" + b64(homeKey),
		"Authorization: Bearer " + bearer,
		"weather api_key=" + apiKey,
		"[player] open http://radio:" + streamPass + "@stream.example.com/live",
		"[net] public address " + publicIPv4 + " and " + publicIPv6,
		"[net] lease 192.168.1.42 from 192.168.1.1",
		"plain line kept",
	}, "\n")
	log := leaks + "\n" + patterns + "\n"

	files := map[string]string{
		// A wpa_supplicant configuration with network blocks, and the saved networks.
		"tmp/wpa_supplicant.conf": "ctrl_interface=/var/run/wpa_supplicant\n" +
			"network={\n\tssid=\"" + homeSSID + "\"\n\tpsk=" + wpaPMK +
			"\n}\nnetwork={\n\tssid=" + hex.EncodeToString([]byte(cafeSSID)) + "\n\tpsk=" + knownHex + "\n}\n",
		"mnt/nv/wifi.conf":      "SSID_B64=" + b64(homeSSID) + "\nPSK_B64=" + b64(homeKey) + "\n",
		"mnt/nv/wifi.conf.good": "SSID_B64=" + b64(homeSSID) + "\nPSK_B64=" + b64(homeKey) + "\n",
		"mnt/nv/wifi.conf.known": b64(homeSSID) + " " + b64(homeKey) + " -\n" +
			b64(atticSSID) + " " + b64(atticKey) + " -\n" + b64(cafeSSID) + " - " + knownHex + "\n",
		"mnt/nv/spotify/system/credentials.json": `{"username":"` + spotifyUser +
			`","auth_type":1,"auth_data":"` + spotifyBlob + `"}`,
		"run/soundtap/confirm-token": confirmTok + "\n",
		"mnt/nv/soundtap/api-token":  household + "\n",
		"mnt/nv/soundtap/settings.json": `{"schemaVersion":1,"name":"Kitchen",` +
			`"mqtt":{"enabled":true,"host":"broker.lan",` +
			`"port":1883,"username":"speaker","password":"` + mqttPass + `"}}`,
		// Every log the bundle carries, and the previous boot's records.
		"tmp/soundtap.log":                            log,
		"tmp/soundtap.log.0":                          log,
		"tmp/soundtap-install.log":                    log,
		"tmp/soundtap-start.log":                      log,
		"run/soundtap/bco-flash.log":                  log,
		"run/soundtap/bco-lan.log":                    log,
		"mnt/nv/soundtap/previous-boot.log":           log,
		"mnt/nv/soundtap/previous-boot.1.log":         log,
		"mnt/nv/soundtap/previous-boot.2.log":         log,
		"mnt/nv/soundtap/current-boot.log":            log,
		"mnt/nv/soundtap-boot.log":                    log,
		"mnt/nv/soundtap-netfail.log":                 log,
		"mnt/nv/soundtap/panic.log":                   "panic: boom\n" + log,
		"mnt/nv/soundtap/last-boot/how":               "panic\n",
		"mnt/nv/soundtap/last-boot/console-ramoops-0": log + "reboot: Restarting system\n",
		"mnt/nv/soundtap/last-boot/dmesg-ramoops-0":   "Panic#1 Part1\n" + log,
	}
	for name, body := range files {
		path := filepath.Join(root, name)

		err := os.MkdirAll(filepath.Dir(path), 0o700)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(path, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return home
}

// The support archive leaves the speaker for a public issue tracker. Built from a speaker
// whose every file holds secrets, no secret may appear in any file of it, nor in the
// plain-text /api/debug, while the rest of the logs survives.
//
//nolint:paralleltest // roots the package-level system paths in a fake tree
func TestSupportBundleCarriesNoSecrets(t *testing.T) {
	root := t.TempDir()
	home := seedSpeaker(t, root)
	t.Cleanup(web.SetSystemRootForTest(root))
	srv, _, _ := newServerAt(t, home)
	handler := srv.Handler()

	rec := do(t, handler, "GET", "/api/debug?download=1", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("GET /api/debug?download=1 = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, `filename="soundtap-support-`) || !strings.HasSuffix(cd, `.zip"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}

	archive, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}

	files := map[string]string{}

	for _, entry := range archive.File {
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}

		content, err := io.ReadAll(file)
		_ = file.Close()

		if err != nil {
			t.Fatal(err)
		}

		files[entry.Name] = string(content)
	}

	files["GET /api/debug"] = do(t, handler, "GET", "/api/debug", "").Body.String()

	for _, want := range []string{
		"debug.txt", "version.json", "health.json", "kernel.log", "panic.log",
		"logs/tmp/soundtap.log", "logs/tmp/soundtap.log.0", "logs/tmp/soundtap-install.log",
		"logs/run/soundtap/bco-flash.log", "logs/mnt/nv/soundtap/previous-boot.log",
		"logs/mnt/nv/soundtap/previous-boot.2.log", "logs/mnt/nv/soundtap/current-boot.log",
		"logs/mnt/nv/soundtap-boot.log", "logs/mnt/nv/soundtap-netfail.log",
		"last-boot/how", "last-boot/console-ramoops-0", "last-boot/dmesg-ramoops-0",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("archive is missing %s", want)
		}
	}

	spelled := []string{homeSSID, homeKey, atticSSID, atticKey, cafeSSID}
	secrets := make([]string, 0, 18+2*len(spelled))

	secrets = append(secrets,
		homeSSID, homeKey, atticSSID, atticKey, cafeSSID, wpaPMK, knownHex, neighbour,
		mqttPass, spotifyUser, spotifyBlob, confirmTok, household, bearer, apiKey, streamPass,
		publicIPv4, publicIPv6,
	)
	for _, plain := range spelled {
		secrets = append(secrets, b64(plain), hex.EncodeToString([]byte(plain)))
	}

	for name, body := range files {
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s carries the secret %q", name, secret)
			}
		}
	}

	// Redacted, not dropped: the lines are still there to read, private addresses too.
	agent := files["logs/tmp/soundtap.log"]
	for _, want := range []string{
		"plain line kept", "[net] lease 192.168.1.42 from 192.168.1.1",
		"[wifi] retrying <wifi-ssid-", "<wifi-key>", "<mqtt-password>", "<spotify-user>",
		"<confirm-token>", "<household-token>", "<public-ip>",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent log lost %q:\n%s", want, agent)
		}
	}

	if !strings.Contains(files["last-boot/how"], "panic") || !strings.Contains(files["version.json"], `"api"`) ||
		!strings.Contains(files["health.json"], `"warnings"`) ||
		!strings.Contains(files["debug.txt"], "SoundTap debug bundle") {
		t.Error("the archive's summary files are not what /api/debug, /api/version and /api/health say")
	}
}
