// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/mdns"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestPersistWifiProfileIsInertAndAtomic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "wifi.conf")
	// A fixed ".tmp" name next to the target is predictable: a symlink planted there
	// must not redirect the PSK into another file: one atomic writer.
	victim := filepath.Join(dir, "victim")

	err := os.WriteFile(victim, []byte("keep"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(victim, path+".tmp")
	if err != nil {
		t.Fatal(err)
	}

	ssid := `$(touch /tmp/pwned); "quoted"`

	psk := "price$5`touch /tmp/pwned`\\secret"

	err = persistWifiProfile(path, ssid, "", psk, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	values := map[string]string{}

	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("invalid line %q", line)
		}

		decoded, decodeErr := base64.StdEncoding.DecodeString(value)
		if decodeErr != nil {
			t.Fatalf("decode %s: %v", key, decodeErr)
		}

		values[key] = string(decoded)
	}

	if values["SSID_B64"] != ssid || values["PSK_B64"] != psk {
		t.Fatalf("round trip = %#v", values)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("wifi profile mode = %v, want 0600", info.Mode().Perm())
	}

	got, err := os.ReadFile(victim)
	if err != nil || string(got) != "keep" {
		t.Fatalf("victim = %q, %v; want unchanged", got, err)
	}

	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("wifi profile = %v, %v; want a regular file", info, err)
	}
}

func TestRemoveStalePluginsOnlyRemovesPluginDirectory(t *testing.T) {
	t.Parallel()

	home := t.TempDir()

	plugins := filepath.Join(home, "plugins")

	err := os.MkdirAll(plugins, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(plugins, "stale"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	keep := filepath.Join(home, "presets.json")

	err = os.WriteFile(keep, []byte("[]"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = removeStalePlugins(home)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(plugins)
	if !os.IsNotExist(err) {
		t.Fatalf("plugins directory remains: %v", err)
	}

	_, err = os.Stat(keep)
	if err != nil {
		t.Fatalf("sibling state removed: %v", err)
	}
}

//nolint:paralleltest // changes package-wide state other tests read: rootHelper
func TestFactoryResetRemovesPersistentStateAndClearsWifi(t *testing.T) {
	persistent := t.TempDir()

	home := filepath.Join(persistent, "soundtap")

	err := os.Mkdir(home, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	wifi := filepath.Join(persistent, "wifi.conf")
	for _, path := range []string{
		filepath.Join(home, "presets.json"),
		filepath.Join(home, "presets.json.settings"),
		filepath.Join(home, "presets.json.settings.corrupt"),
		filepath.Join(home, "presets.json.settings.corrupt.1"),
		filepath.Join(home, "presets.json.corrupt"),
		filepath.Join(home, ".sessions"),
		filepath.Join(home, "recent.json"),
		filepath.Join(home, "recent.json.corrupt"),
		filepath.Join(home, "favorites.json"),
		filepath.Join(home, "favorites.json.corrupt.1"),
		filepath.Join(home, "multiroom-enabled"),
		filepath.Join(home, "multiroom-server"),
		wifi,
		wifi + ".good",
		wifi + ".known",
		filepath.Join(home, ".presets.json.settings.tmp-123456"),
		filepath.Join(home, ".presets.json.tmp-1"),
		filepath.Join(persistent, ".wifi.conf.tmp-987"),
		filepath.Join(persistent, ".wifi.conf.known.tmp-42"),
	} {
		err := os.WriteFile(path, []byte("SYNTHETIC-SECRET"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, path := range []string{
		filepath.Join(home, "plugins", "stale"),
		filepath.Join(home, "snapserver", "server.json"),
		filepath.Join(persistent, "spotify", "system", "credentials.json"),
		filepath.Join(persistent, "bluetooth", "adapter", "device", "info"),
		filepath.Join(persistent, "bluealsa", "00:11:22:33:44:55"),
	} {
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(path, []byte("x"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	restarted := false

	factoryReset(t.Context(), home, wifi, slog.New(slog.NewTextHandler(os.Stderr, nil)), func() { restarted = true })

	for _, path := range []string{
		filepath.Join(home, "presets.json"),
		filepath.Join(home, "presets.json.settings"),
		filepath.Join(home, "presets.json.settings.corrupt"),
		filepath.Join(home, "presets.json.settings.corrupt.1"),
		filepath.Join(home, "presets.json.corrupt"),
		filepath.Join(home, ".sessions"),
		filepath.Join(home, "recent.json"),
		filepath.Join(home, "recent.json.corrupt"),
		filepath.Join(home, "favorites.json"),
		filepath.Join(home, "favorites.json.corrupt.1"),
		filepath.Join(home, "plugins"),
		filepath.Join(home, "snapserver"),
		filepath.Join(home, "multiroom-enabled"),
		filepath.Join(home, "multiroom-server"),
		filepath.Join(persistent, "spotify"),
		wifi + ".good",
		wifi + ".known",
		filepath.Join(home, ".presets.json.settings.tmp-123456"),
		filepath.Join(home, ".presets.json.tmp-1"),
		filepath.Join(persistent, ".wifi.conf.tmp-987"),
		filepath.Join(persistent, ".wifi.conf.known.tmp-42"),
	} {
		_, err := os.Stat(path)
		if !os.IsNotExist(err) {
			t.Errorf("%s remains after reset: %v", path, err)
		}
	}

	if !restarted {
		t.Fatal("factory reset did not request a daemon restart")
	}

	data, readErr := os.ReadFile(wifi)
	if readErr != nil || string(data) != "SSID_B64=\nPSK_B64=\n" {
		t.Fatalf("Wi-Fi reset override = %q, %v", data, readErr)
	}

	for _, dir := range []string{"bluetooth", "bluealsa"} {
		entries, err := os.ReadDir(filepath.Join(persistent, dir))
		if err != nil || len(entries) != 0 {
			t.Fatalf("%s state directory = %v, %v; want empty", dir, entries, err)
		}
	}
}

func TestMaintainIPClearsLostRoute(t *testing.T) {
	t.Parallel()

	speaker := speakerstate.New()
	speaker.SetIP("192.0.2.10")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		maintainIPWith(ctx, speaker, nil, "test", noMDNSService(), slog.New(slog.NewTextHandler(os.Stderr, nil)),
			time.Millisecond, func() string { return "" })
		close(done)
	}()

	for deadline := time.Now().Add(5 * time.Second); speaker.StatusSnapshot().IP != "" && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}

	cancel()
	<-done

	if got := speaker.StatusSnapshot().IP; got != "" {
		t.Fatalf("stale IP remains after route loss: %q", got)
	}
}

// A speaker without an address keeps asking, whatever the normal interval.
func TestMaintainIPAsksSoonWithoutAddress(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	asked := make(chan struct{}, 8)
	lookup := func() string {
		asked <- struct{}{}

		return ""
	}
	sp := speakerstate.New()

	sp.SetIP("")
	go maintainIPWith(ctx, sp, nil, "test", noMDNSService(), slog.New(slog.NewTextHandler(os.Stderr, nil)),
		time.Hour, lookup)

	for range 2 {
		select {
		case <-asked:
		case <-time.After(3 * time.Second):
			t.Fatal("no second lookup while the address is missing")
		}
	}
}

// A save still in flight when the reset runs (the shutdown's last volume) must not
// write the deleted settings back.
//
//nolint:paralleltest // changes package-wide state other tests read: rootHelper
func TestFactoryResetBlocksLateSettingsWrite(t *testing.T) {
	home := filepath.Join(t.TempDir(), "soundtap")

	path := filepath.Join(home, "presets.json.settings")

	err := os.MkdirAll(home, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	set := settings.Open(path)

	err = set.SetName("Kitchen")
	if err != nil {
		t.Fatal(err)
	}

	factoryReset(t.Context(), home, filepath.Join(t.TempDir(), "wifi.conf"),
		slog.New(slog.DiscardHandler), func() {}, set)

	err = set.SetAudio(settings.Audio{Volume: 30, Bass: 0, HasVolume: true, AutoOff: 0})
	if !errors.Is(err, settings.ErrClosed) {
		t.Fatalf("late write err = %v, want ErrClosed", err)
	}

	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("settings file recreated after reset: %v", err)
	}
}

// A raw key goes to the Wi-Fi manager as PSK_HEX: as a passphrase the supplicant would derive another key.
func TestPersistWifiProfileKeepsARawKeyRaw(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wifi.conf")

	key := strings.Repeat("ab", 32)

	err := persistWifiProfile(path, "Home", "", key, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "\nPSK_B64=\nPSK_HEX="+key+"\n") {
		t.Errorf("wifi.conf = %q", data)
	}
}

// noMDNSService is a service with nothing to announce: a test's speaker has no name on the LAN.
func noMDNSService() mdns.Service {
	return mdns.Service{ID: "", Model: "", Firmware: "", API: 0, Port: 0}
}
