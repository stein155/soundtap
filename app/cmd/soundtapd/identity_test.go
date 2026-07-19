// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestIdentityDefaultsAreModelSpecific(t *testing.T) {
	t.Setenv("SOUNDTAP_DEFAULT_NAME", "")
	t.Setenv("SOUNDTAP_DEVICE_ID", "")
	withNetDir(t, nil)
	name, id := identityDefaults(model.Named(model.ST20), "")
	// No MAC to read here, so the name is what is left of the label: what it runs and
	// which model. The id keeps its own fallback, which is per model too.
	if name != "SoundTap ST20" || id != "SOUNDTAPST20" {
		t.Fatalf("ST20 defaults = %q, %q", name, id)
	}
}

// withNetDir points MAC reads at a temporary /sys/class/net holding the given
// interfaces, so a test is not answered by whatever host it runs on.
func withNetDir(t *testing.T, ifaces map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, mac := range ifaces {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "address"), []byte(mac+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := netSysDir
	netSysDir = dir
	t.Cleanup(func() { netSysDir = old })
}

// Two speakers of one model used to answer to the same device id, which every
// persistent multi-device relation would then key off.
func TestDeviceIDComesFromTheMACWhenTheEnvironmentIsAbsent(t *testing.T) {
	t.Setenv("SOUNDTAP_DEVICE_ID", "")
	withNetDir(t, map[string]string{"wlan0": "02:00:00:a1:b2:30", "eth0": "00:00:00:00:00:00"})
	if _, id := identityDefaults(model.Named(model.ST10), ""); id != "020000A1B230" {
		t.Errorf("ST10 device id = %q, want the wlan0 MAC", id)
	}
	// The ST20 reads its wired port first, as init.d/soundtap does.
	withNetDir(t, map[string]string{"wlan0": "02:00:00:a1:b2:30", "eth0": "02:11:22:33:44:55"})
	if _, id := identityDefaults(model.Named(model.ST20), ""); id != "021122334455" {
		t.Errorf("ST20 device id = %q, want the eth0 MAC", id)
	}
	// An interface whose firmware has not loaded reports all zeroes; it is not an
	// identity, and the model fallback is better than a shared one.
	withNetDir(t, map[string]string{"wlan0": "00:00:00:00:00:00"})
	if _, id := identityDefaults(model.Named(model.ST10), ""); id != "SOUNDTAPST10" {
		t.Errorf("all-zero MAC gave device id %q", id)
	}
}

func TestST20MigratesOnlyWrongDefaultName(t *testing.T) {
	if !shouldInitializeName(model.Named(model.ST20), "Bose ST10 (SoundTap)", "") {
		t.Fatal("ST20 legacy default was not migrated")
	}
	if shouldInitializeName(model.Named(model.ST20), "Living Room", "") {
		t.Fatal("custom ST20 name would be overwritten")
	}
}

func TestManufacturingSerialIsPackagedProduct(t *testing.T) {
	dir := t.TempDir()
	proc := filepath.Join(dir, "mtd")
	if err := os.WriteFile(proc, []byte(`mtd7: 00040000 00020000 "NAND.Manufacturing-Data"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, packagedSerialOffset+serialFieldSize)
	binary.LittleEndian.PutUint16(data[0:2], 0xb05e)
	binary.LittleEndian.PutUint16(data[2:4], 2)
	binary.LittleEndian.PutUint16(data[4:6], 0x384)
	copy(data[0x1c0:], "SCM-NEIGHBOUR")
	copy(data[packagedSerialOffset:], "SERIAL0000000010A")
	if err := os.WriteFile(filepath.Join(dir, "mtd7"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readManufacturingSerial(proc, dir)
	if err != nil || got != "SERIAL0000000010A" {
		t.Fatalf("serial = %q, %v", got, err)
	}
}

func TestUnreadableSerialKeepsCurrentDefault(t *testing.T) {
	t.Setenv("SOUNDTAP_DEFAULT_NAME", "SoundTap-ST20-123abc")
	if _, err := readManufacturingSerial(filepath.Join(t.TempDir(), "missing"), t.TempDir()); err == nil {
		t.Fatal("unreadable serial succeeded")
	}
	name, _ := identityDefaults(model.Named(model.ST20), "")
	if name != "SoundTap-ST20-123abc" {
		t.Fatalf("fallback name = %q", name)
	}
}

// The default name says what the box is and which box it is: "SoundTap" plus the last six
// of its MAC, the way every other network device labels itself. The manufacturing serial
// is not it -- it tells its owner nothing and two speakers of one model can only be told
// apart by reading it letter for letter.
func TestDefaultNameIsSoundTapModelAndMACSuffix(t *testing.T) {
	t.Setenv("SOUNDTAP_DEVICE_ID", "020000C3D411")
	name, id := identityDefaults(model.Named(model.ST20), "SERIAL0000000010A")
	if name != "SoundTap ST20 C3D411" {
		t.Fatalf("default name = %q", name)
	}
	if id != "020000C3D411" {
		t.Fatalf("device id = %q", id)
	}
	if shouldInitializeName(model.Named(model.ST20), "Living Room", "") {
		t.Fatal("user-set name would be replaced")
	}
	// A speaker still carrying its own serial was never named by anyone: that was the old
	// default. It gets the new one.
	if !shouldInitializeName(model.Named(model.ST20), "SERIAL0000000010A", "SERIAL0000000010A") {
		t.Fatal("a stored serial is a default, not a chosen name")
	}
	if shouldInitializeName(model.Named(model.ST20), "SERIAL0000000010A", "SN-OTHER") {
		t.Fatal("a name that is not this speaker's serial was chosen by someone")
	}
}

// One box, one name. web.Server.identity applies the same rule, so /api/devices and the
// snapcast host id the init builds cannot disagree about what this speaker is called.
func TestSpeakerIdentityPrefersTheSerial(t *testing.T) {
	if got := speakerIdentity("SN-12345", "020000A1B231"); got != "SN-12345" {
		t.Fatalf("identity = %q, want the serial", got)
	}
	if got := speakerIdentity("", "020000A1B231"); got != "020000A1B231" {
		t.Fatalf("identity = %q, want the MAC-derived device id when there is no serial", got)
	}
	if got := speakerIdentity("", ""); got != "" {
		t.Fatalf("identity = %q, want empty when there is neither", got)
	}
}

// The ST20 uses layout 1: the packaged serial sits at 0x119, between two copies of the
// PCB serial, as on the bench ST20.
func TestST20ManufacturingSerialLayout1(t *testing.T) {
	dir := t.TempDir()
	proc := filepath.Join(dir, "mtd")
	if err := os.WriteFile(proc, []byte(`mtd12: 00040000 00020000 "NAND.Manufacturing-Data"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, packagedSerialOffset+serialFieldSize)
	binary.LittleEndian.PutUint16(data[0:2], 0xb05e)
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], 4)
	copy(data[0x100:], "K0000000000000000000020")
	copy(data[0x119:], "SERIAL0000000020A")
	copy(data[0x132:], "K0000000000000000000020")
	if err := os.WriteFile(filepath.Join(dir, "mtd12"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readManufacturingSerial(proc, dir)
	if err != nil || got != "SERIAL0000000020A" {
		t.Fatalf("serial = %q, %v", got, err)
	}
}

func TestStockDeviceName(t *testing.T) {
	dir := t.TempDir()
	old := stockConfigPath
	t.Cleanup(func() { stockConfigPath = old })
	stockConfigPath = filepath.Join(dir, "SystemConfigurationDB.xml")
	if got := stockDeviceName(); got != "" {
		t.Fatalf("no file gave %q", got)
	}
	xml := `<?xml version="1.0" encoding="UTF-8" ?>
<SystemConfiguration>
    <Password version="1">x</Password>
    <DeviceName> Woonkamer </DeviceName>
    <AccountAssociatedEMail />
</SystemConfiguration>`
	if err := os.WriteFile(stockConfigPath, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := stockDeviceName(); got != "Woonkamer" {
		t.Fatalf("stockDeviceName = %q, want Woonkamer", got)
	}
	if err := os.WriteFile(stockConfigPath, []byte("<not xml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := stockDeviceName(); got != "" {
		t.Fatalf("broken file gave %q", got)
	}
}

func TestStockPresets(t *testing.T) {
	stockPresetsPath = filepath.Join(t.TempDir(), "Presets.xml")
	if got := stockPresets(); got != nil {
		t.Fatalf("no file gave %v", got)
	}
	const doc = `<?xml version="1.0" encoding="UTF-8" ?><presets>
	<preset id="1"><ContentItem source="TUNEIN" type="stationurl" location="/v1/playback/station/s17523"><itemName>NPO Radio 1</itemName><containerArt></containerArt></ContentItem></preset>
	<preset id="2"><ContentItem source="SPOTIFY" type="tracklisturl" location="/playback/x"><itemName>Mix</itemName></ContentItem></preset>
	<preset id="3"><ContentItem source="TUNEIN" type="stationurl" location="/v1/playback/station/s6712"><itemName> </itemName></ContentItem></preset>
	<preset id="6"><ContentItem source="TUNEIN" type="stationurl" location="/v1/playback/station/s182221"><itemName>Radio Starfighter</itemName></ContentItem></preset>
	</presets>`
	if err := os.WriteFile(stockPresetsPath, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	got := stockPresets()
	if len(got) != 2 || got[0].Slot != 1 || got[0].StationID != "s17523" || got[0].Name != "NPO Radio 1" ||
		got[1].Slot != 6 || got[1].StationID != "s182221" {
		t.Fatalf("presets = %+v", got)
	}
}

// Stock's name and presets are taken over on the first start after an install, once: a
// factory reset followed by a restart must not bring them back.
func TestStockTakenOverOnceNotAfterReset(t *testing.T) {
	dir := t.TempDir()
	oldConfig, oldPresets := stockConfigPath, stockPresetsPath
	t.Cleanup(func() { stockConfigPath, stockPresetsPath = oldConfig, oldPresets })
	stockConfigPath = filepath.Join(dir, "SystemConfigurationDB.xml")
	stockPresetsPath = filepath.Join(dir, "Presets.xml")
	if err := os.WriteFile(stockConfigPath, []byte("<SystemConfiguration><DeviceName>Woonkamer</DeviceName></SystemConfiguration>"), 0o600); err != nil {
		t.Fatal(err)
	}
	const doc = `<presets><preset id="1"><ContentItem source="TUNEIN" type="stationurl" location="/v1/playback/station/s17523"><itemName>NPO Radio 1</itemName></ContentItem></preset></presets>`
	if err := os.WriteFile(stockPresetsPath, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	oldSetupName := setupNamePath
	t.Cleanup(func() { setupNamePath = oldSetupName })
	setupNamePath = filepath.Join(dir, "default-name")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := func() (string, int) {
		fromStock := takeStockOnce(home, log)
		set := settings.Open(filepath.Join(home, "presets.json.settings"))
		name := initialName(set, "", "SoundTap ST10", fromStock, log)
		st := loadPresets(context.Background(), speakerstate.New(), filepath.Join(home, "presets.json"), fromStock, log)
		n := len(st.All())
		_ = set.Close()
		_ = st.Close()
		return name, n
	}
	if name, n := start(); name != "Woonkamer" || n != 1 {
		t.Fatalf("first start: name %q, %d presets; want Woonkamer and 1", name, n)
	}
	factoryReset(home, filepath.Join(dir, "wifi.conf"), log, func() {})
	if name, n := start(); name != "SoundTap ST10" || n != 0 {
		t.Fatalf("start after reset: name %q, %d presets; want SoundTap ST10 and none", name, n)
	}
}

// The speaker reports this device and this build, never a stock speaker's identity:
// the MQTT bridge publishes Software as the device's sw_version.
func TestNewSpeakerReportsThisDeviceAndBuild(t *testing.T) {
	oldVersion, oldHW := version, board
	t.Cleanup(func() { version, board = oldVersion, oldHW })
	version, board = "app-v1.2.3", model.Named(model.ST20)
	sp := newSpeaker(options{deviceID: "A0B1C2D3E4F5", home: t.TempDir()}, "Keuken", slog.New(slog.NewTextHandler(io.Discard, nil)))
	info, err := sp.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.DeviceID != "A0B1C2D3E4F5" || info.Name != "Keuken" || info.Type != "SoundTouch 20" || info.Software != "1.2.3" {
		t.Fatalf("info = %+v, want this device, name, model and app version", info)
	}
	// The address comes from maintainIP once the network has one, not from a default.
	if info.IP != "" {
		t.Fatalf("IP before the network reported one = %q, want empty", info.IP)
	}
}
