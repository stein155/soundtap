// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
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
	dir := t.TempDir()
	path := filepath.Join(dir, "wifi.conf")
	// A fixed ".tmp" name next to the target is predictable: a symlink planted there
	// must not redirect the PSK into another file: one atomic writer.
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	ssid := `$(touch /tmp/pwned); "quoted"`
	psk := "price$5`touch /tmp/pwned`\\secret"
	if err := persistWifiProfile(path, ssid, psk, slog.New(slog.NewTextHandler(os.Stderr, nil))); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("invalid line %q", line)
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("decode %s: %v", key, err)
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
	if got, err := os.ReadFile(victim); err != nil || string(got) != "keep" {
		t.Fatalf("victim = %q, %v; want unchanged", got, err)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("wifi profile = %v, %v; want a regular file", info, err)
	}
}

func TestRemoveStalePluginsOnlyRemovesPluginDirectory(t *testing.T) {
	home := t.TempDir()
	plugins := filepath.Join(home, "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugins, "stale"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(home, "presets.json")
	if err := os.WriteFile(keep, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeStalePlugins(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plugins); !os.IsNotExist(err) {
		t.Fatalf("plugins directory remains: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("sibling state removed: %v", err)
	}
}

func TestFactoryResetRemovesPersistentStateAndClearsWifi(t *testing.T) {
	persistent := t.TempDir()
	home := filepath.Join(persistent, "soundtap")
	if err := os.Mkdir(home, 0o755); err != nil {
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
		if err := os.WriteFile(path, []byte("SYNTHETIC-SECRET"), 0o600); err != nil {
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
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	restarted := false
	factoryReset(home, wifi, slog.New(slog.NewTextHandler(os.Stderr, nil)), func() { restarted = true })
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
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s remains after reset: %v", path, err)
		}
	}
	if !restarted {
		t.Fatal("factory reset did not request a daemon restart")
	}
	if data, err := os.ReadFile(wifi); err != nil || string(data) != "SSID_B64=\nPSK_B64=\n" {
		t.Fatalf("Wi-Fi reset override = %q, %v", data, err)
	}
	for _, dir := range []string{"bluetooth", "bluealsa"} {
		entries, err := os.ReadDir(filepath.Join(persistent, dir))
		if err != nil || len(entries) != 0 {
			t.Fatalf("%s state directory = %v, %v; want empty", dir, entries, err)
		}
	}
}

func TestMaintainIPClearsLostRoute(t *testing.T) {
	sp := speakerstate.New()
	sp.SetIP("192.0.2.10")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		maintainIPWith(ctx, sp, nil, "test", mdns.Service{}, slog.New(slog.NewTextHandler(os.Stderr, nil)), time.Millisecond, func() string { return "" })
		close(done)
	}()
	for deadline := time.Now().Add(5 * time.Second); sp.StatusSnapshot().IP != "" && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if got := sp.StatusSnapshot().IP; got != "" {
		t.Fatalf("stale IP remains after route loss: %q", got)
	}
}

// A speaker without an address keeps asking, whatever the normal interval.
func TestMaintainIPAsksSoonWithoutAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	asked := make(chan struct{}, 8)
	lookup := func() string {
		asked <- struct{}{}
		return ""
	}
	sp := speakerstate.New()
	sp.SetIP("")
	go maintainIPWith(ctx, sp, nil, "test", mdns.Service{}, slog.New(slog.NewTextHandler(os.Stderr, nil)), time.Hour, lookup)
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
func TestFactoryResetBlocksLateSettingsWrite(t *testing.T) {
	home := filepath.Join(t.TempDir(), "soundtap")
	path := filepath.Join(home, "presets.json.settings")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	set := settings.Open(path)
	if err := set.SetName("Kitchen"); err != nil {
		t.Fatal(err)
	}
	factoryReset(home, filepath.Join(t.TempDir(), "wifi.conf"),
		slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}, set)
	if err := set.SetAudio(settings.Audio{Volume: 30, HasVolume: true}); !errors.Is(err, settings.ErrClosed) {
		t.Fatalf("late write err = %v, want ErrClosed", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("settings file recreated after reset: %v", err)
	}
}

// A raw key goes to the Wi-Fi manager as PSK_HEX: as a passphrase the supplicant would derive another key.
func TestPersistWifiProfileKeepsARawKeyRaw(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wifi.conf")
	key := strings.Repeat("ab", 32)
	if err := persistWifiProfile(path, "Home", key, slog.New(slog.NewTextHandler(os.Stderr, nil))); err != nil {
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
