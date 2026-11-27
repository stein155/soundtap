// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestIdentityDefaultsAreModelSpecific(t *testing.T) {
	t.Setenv("SOUNDTAP_DEFAULT_NAME", "")
	t.Setenv("SOUNDTAP_DEVICE_ID", "")
	withNetDir(t, nil)

	name, id := identityDefaults(new(model.Named(model.ST20)), "")
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
		err := os.MkdirAll(filepath.Join(dir, name), 0o755)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(dir, name, "address"), []byte(mac+"\n"), 0o600)
		if err != nil {
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

	if _, id := identityDefaults(new(model.Named(model.ST10)), ""); id != "020000A1B230" {
		t.Errorf("ST10 device id = %q, want the wlan0 MAC", id)
	}
	// The ST20 reads its wired port first, as init.d/soundtap does.
	withNetDir(t, map[string]string{"wlan0": "02:00:00:a1:b2:30", "eth0": "02:11:22:33:44:55"})

	if _, id := identityDefaults(new(model.Named(model.ST20)), ""); id != "021122334455" {
		t.Errorf("ST20 device id = %q, want the eth0 MAC", id)
	}
	// An interface whose firmware has not loaded reports all zeroes; it is not an
	// identity, and the model fallback is better than a shared one.
	withNetDir(t, map[string]string{"wlan0": "00:00:00:00:00:00"})

	if _, id := identityDefaults(new(model.Named(model.ST10)), ""); id != "SOUNDTAPST10" {
		t.Errorf("all-zero MAC gave device id %q", id)
	}
}

func TestST20MigratesOnlyWrongDefaultName(t *testing.T) {
	t.Parallel()

	st20 := model.Named(model.ST20)

	if !shouldInitializeName(&st20, "Bose ST10 (SoundTap)", "") {
		t.Fatal("ST20 legacy default was not migrated")
	}

	if shouldInitializeName(&st20, "Living Room", "") {
		t.Fatal("custom ST20 name would be overwritten")
	}
}

func TestManufacturingSerialIsPackagedProduct(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	proc := filepath.Join(dir, "mtd")

	err := os.WriteFile(proc, []byte(`mtd7: 00040000 00020000 "NAND.Manufacturing-Data"`+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	data := make([]byte, packagedSerialOffset+serialFieldSize)
	binary.LittleEndian.PutUint16(data[0:2], 0xb05e)
	binary.LittleEndian.PutUint16(data[2:4], 2)
	binary.LittleEndian.PutUint16(data[4:6], 0x384)
	copy(data[0x1c0:], "SCM-NEIGHBOUR")
	copy(data[packagedSerialOffset:], "SERIAL0000000010A")

	err = os.WriteFile(filepath.Join(dir, "mtd7"), data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got, err := readManufacturingSerial(proc, dir)
	if err != nil || got != "SERIAL0000000010A" {
		t.Fatalf("serial = %q, %v", got, err)
	}
}

func TestUnreadableSerialKeepsCurrentDefault(t *testing.T) {
	t.Setenv("SOUNDTAP_DEFAULT_NAME", "SoundTap-ST20-123abc")

	_, err := readManufacturingSerial(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	if err == nil {
		t.Fatal("unreadable serial succeeded")
	}

	name, _ := identityDefaults(new(model.Named(model.ST20)), "")
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

	st20 := model.Named(model.ST20)

	name, deviceID := identityDefaults(&st20, "SERIAL0000000010A")
	if name != "SoundTap ST20 C3D411" {
		t.Fatalf("default name = %q", name)
	}

	if deviceID != "020000C3D411" {
		t.Fatalf("device id = %q", deviceID)
	}

	if shouldInitializeName(&st20, "Living Room", "") {
		t.Fatal("user-set name would be replaced")
	}
	// A speaker still carrying its own serial was never named by anyone: that was the old
	// default. It gets the new one.
	if !shouldInitializeName(&st20, "SERIAL0000000010A", "SERIAL0000000010A") {
		t.Fatal("a stored serial is a default, not a chosen name")
	}

	if shouldInitializeName(&st20, "SERIAL0000000010A", "SN-OTHER") {
		t.Fatal("a name that is not this speaker's serial was chosen by someone")
	}
}

// One box, one name. web.Server.identity applies the same rule, so /api/devices and the
// snapcast host id the init builds cannot disagree about what this speaker is called.
func TestSpeakerIdentityPrefersTheSerial(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	dir := t.TempDir()

	proc := filepath.Join(dir, "mtd")

	err := os.WriteFile(proc, []byte(`mtd12: 00040000 00020000 "NAND.Manufacturing-Data"`+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	data := make([]byte, packagedSerialOffset+serialFieldSize)
	binary.LittleEndian.PutUint16(data[0:2], 0xb05e)
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], 4)
	copy(data[0x100:], "K0000000000000000000020")
	copy(data[0x119:], "SERIAL0000000020A")
	copy(data[0x132:], "K0000000000000000000020")

	err = os.WriteFile(filepath.Join(dir, "mtd12"), data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got, err := readManufacturingSerial(proc, dir)
	if err != nil || got != "SERIAL0000000020A" {
		t.Fatalf("serial = %q, %v", got, err)
	}
}

//nolint:paralleltest // swaps package variables other tests read: stockConfigPath
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

	err := os.WriteFile(stockConfigPath, []byte(xml), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if got := stockDeviceName(); got != "Woonkamer" {
		t.Fatalf("stockDeviceName = %q, want Woonkamer", got)
	}

	err = os.WriteFile(stockConfigPath, []byte("<not xml"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if got := stockDeviceName(); got != "" {
		t.Fatalf("broken file gave %q", got)
	}
}

//nolint:paralleltest // swaps package variables other tests read: stockPresetsPath
func TestStockPresets(t *testing.T) {
	stockPresetsPath = filepath.Join(t.TempDir(), "Presets.xml")
	if got := stockPresets(); got != nil {
		t.Fatalf("no file gave %v", got)
	}

	const doc = `<?xml version="1.0" encoding="UTF-8" ?><presets>
	<preset id="1"><ContentItem source="TUNEIN" type="stationurl" location="/v1/playback/station/s17523">` +
		`<itemName>NPO Radio 1</itemName><containerArt></containerArt></ContentItem></preset>
	<preset id="2"><ContentItem source="SPOTIFY" type="tracklisturl" location="/playback/x">` +
		`<itemName>Mix</itemName></ContentItem></preset>
	<preset id="3"><ContentItem source="TUNEIN" type="stationurl" location="/v1/playback/station/s6712">` +
		`<itemName> </itemName></ContentItem></preset>
	<preset id="6"><ContentItem source="TUNEIN" type="stationurl" location="/v1/playback/station/s182221">` +
		`<itemName>Radio Starfighter</itemName></ContentItem></preset>
	</presets>`

	err := os.WriteFile(stockPresetsPath, []byte(doc), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got := stockPresets()
	if len(got) != 2 || got[0].Slot != 1 || got[0].StationID != "s17523" || got[0].Name != "NPO Radio 1" ||
		got[1].Slot != 6 || got[1].StationID != "s182221" {
		t.Fatalf("presets = %+v", got)
	}
}

// stockClockFiles points the stock clock paths at a temporary /mnt/nv and writes what is
// given: the ClockDisplay.xml text ("" for none) and the localtime link target ("" for none).
func stockClockFiles(t *testing.T, doc, link string) {
	t.Helper()
	dir := t.TempDir()
	oldClock, oldLocal := stockClockPath, stockLocaltimePath

	t.Cleanup(func() { stockClockPath, stockLocaltimePath = oldClock, oldLocal })

	stockClockPath = filepath.Join(dir, "ClockDisplay.xml")
	stockLocaltimePath = filepath.Join(dir, "localtime")

	if doc != "" {
		err := os.WriteFile(stockClockPath, []byte(doc), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	if link != "" {
		err := os.Symlink(link, stockLocaltimePath)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// clockXML is stock's ClockDisplay.xml as it stands on an ST20: every field an attribute.
func clockXML(attrs string) string {
	return `<?xml version="1.0" encoding="UTF-8" ?>
<clockDisplay>
    <clockConfig ` + attrs + ` />
</clockDisplay>`
}

//nolint:paralleltest // swaps package variables other tests read: stockClockPath, stockLocaltimePath
func TestStockClock(t *testing.T) {
	for _, test := range []struct {
		name, doc, link string
		want            settings.Display
		ok              bool
	}{
		{"nothing left", "", "", settings.Display{ClockOff: false, Hour12: false, Timezone: ""}, false},
		{
			"off, 24 hours",
			clockXML(`timezoneInfo="Europe/Amsterdam" userEnable="false" timeFormat="TIME_FORMAT_24HOUR_ID" ` +
				`userOffsetMinute="0" brightnessLevel="70" userUtcTime="0"`),
			"/usr/share/zoneinfo/Europe/Amsterdam",
			settings.Display{ClockOff: true, Hour12: false, Timezone: "Europe/Amsterdam"},
			true,
		},
		{
			"on, 12 hours, stock's brightness to the default",
			clockXML(`timezoneInfo="America/New_York" userEnable="true" timeFormat="TIME_FORMAT_12HOUR_ID" ` +
				`brightnessLevel="40"`), "",
			settings.Display{ClockOff: false, Hour12: true, Timezone: "America/New_York"},
			true,
		},
		{
			"brightness 0 is off",
			clockXML(`timezoneInfo="Europe/Berlin" userEnable="true" timeFormat="TIME_FORMAT_24HOUR_ID" ` +
				`brightnessLevel="0"`), "",
			settings.Display{ClockOff: true, Hour12: false, Timezone: "Europe/Berlin"},
			true,
		},
		{
			"zone from the link alone", clockXML(`userEnable="true" timeFormat="TIME_FORMAT_24HOUR_ID"`),
			"/usr/share/zoneinfo/Europe/Amsterdam",
			settings.Display{ClockOff: false, Hour12: false, Timezone: "Europe/Amsterdam"},
			true,
		},
		{
			"no file, only the link", "", "/usr/share/zoneinfo/Europe/London",
			settings.Display{ClockOff: false, Hour12: false, Timezone: "Europe/London"},
			true,
		},
		{
			"an unknown zone is left out, so an on clock is not taken",
			clockXML(`timezoneInfo="Mars/Olympus" userEnable="true"`), "",
			settings.Display{ClockOff: false, Hour12: false, Timezone: ""},
			false,
		},
		{
			"a link outside zoneinfo is no zone", clockXML(`userEnable="false"`), "/etc/passwd",
			settings.Display{ClockOff: true, Hour12: false, Timezone: ""},
			true,
		},
		{
			"a broken file still gives the link's zone", "<clockDisplay", "/usr/share/zoneinfo/Europe/Paris",
			settings.Display{ClockOff: false, Hour12: false, Timezone: "Europe/Paris"},
			true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stockClockFiles(t, test.doc, test.link)

			got, ok := stockClock()
			if got != test.want || ok != test.ok {
				t.Errorf("stockClock() = %+v, %v; want %+v, %v", got, ok, test.want, test.ok)
			}
		})
	}
}

// The clock is taken from stock only on the first start after an install, only with a
// panel, and never over settings the clock already has.
//
//nolint:paralleltest // swaps package variables other tests read: stockClockPath, stockLocaltimePath
func TestTakeStockClock(t *testing.T) {
	stockClockFiles(t, clockXML(`timezoneInfo="Europe/Amsterdam" userEnable="false" `+
		`timeFormat="TIME_FORMAT_24HOUR_ID" brightnessLevel="70"`), "")

	want := settings.Display{ClockOff: true, Hour12: false, Timezone: "Europe/Amsterdam"}
	open := func() *settings.Store { return settings.Open(filepath.Join(t.TempDir(), "s.json")) }

	set := open()
	takeStockClock(set, stockClock, quietLog())

	if got := set.Get().Display; got != want {
		t.Errorf("first start with a panel: display = %+v, want %+v", got, want)
	}

	for name, run := range map[string]func(*settings.Store){
		"not from stock or no panel": func(s *settings.Store) { takeStockClock(s, nil, quietLog()) },
	} {
		s := open()
		run(s)

		if got := s.Get().Display; got != (settings.Display{ClockOff: false, Hour12: false, Timezone: ""}) {
			t.Errorf("%s: display = %+v, want untouched", name, got)
		}
	}

	own := open()

	mine := settings.Display{ClockOff: false, Hour12: true, Timezone: "Asia/Tokyo"}

	err := own.SetDisplay(mine)
	if err != nil {
		t.Fatal(err)
	}

	takeStockClock(own, stockClock, quietLog())

	if got := own.Get().Display; got != mine {
		t.Errorf("own settings replaced by stock's: %+v", got)
	}
}

// Stock's name and presets are taken over on the first start after an install, once: a
// factory reset followed by a restart must not bring them back.
//
//nolint:paralleltest // swaps package variables other tests read: setupNamePath, stockConfigPath, stockPresetsPath
func TestStockTakenOverOnceNotAfterReset(t *testing.T) {
	dir := t.TempDir()
	oldConfig, oldPresets := stockConfigPath, stockPresetsPath

	t.Cleanup(func() { stockConfigPath, stockPresetsPath = oldConfig, oldPresets })

	stockConfigPath = filepath.Join(dir, "SystemConfigurationDB.xml")
	stockPresetsPath = filepath.Join(dir, "Presets.xml")

	config := "<SystemConfiguration><DeviceName>Woonkamer</DeviceName></SystemConfiguration>"

	err := os.WriteFile(stockConfigPath, []byte(config), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	const doc = `<presets><preset id="1"><ContentItem source="TUNEIN" type="stationurl" ` +
		`location="/v1/playback/station/s17523"><itemName>NPO Radio 1</itemName></ContentItem></preset></presets>`

	err = os.WriteFile(stockPresetsPath, []byte(doc), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(dir, "home")

	err = os.Mkdir(home, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	oldSetupName := setupNamePath

	t.Cleanup(func() { setupNamePath = oldSetupName })

	setupNamePath = filepath.Join(dir, "default-name")
	log := slog.New(slog.DiscardHandler)

	start := func() (string, int) {
		leftovers := stockTakeover(home, log)
		set := settings.Open(filepath.Join(home, "presets.json.settings"))
		name := initialName(set, "", "SoundTap ST10", leftovers.name, log)
		stored := loadPresets(context.Background(), speakerstate.New(), filepath.Join(home, "presets.json"),
			leftovers.presets, log)
		leftovers.finish(log)

		n := len(stored.All())
		_ = set.Close()
		_ = stored.Close()

		return name, n
	}
	if name, n := start(); name != "Woonkamer" || n != 1 {
		t.Fatalf("first start: name %q, %d presets; want Woonkamer and 1", name, n)
	}

	factoryReset(t.Context(), home, filepath.Join(dir, "wifi.conf"), log, func() {})

	if name, n := start(); name != "SoundTap ST10" || n != 0 {
		t.Fatalf("start after reset: name %q, %d presets; want SoundTap ST10 and none", name, n)
	}
}

// The speaker reports this device and this build, never a stock speaker's identity:
// the MQTT bridge publishes Software as the device's sw_version.
//
//nolint:paralleltest // swaps package variables other tests read: board, version
func TestNewSpeakerReportsThisDeviceAndBuild(t *testing.T) {
	oldVersion, oldHW := version, board

	t.Cleanup(func() { version, board = oldVersion, oldHW })

	version, board = "app-v1.2.3", model.Named(model.ST20)
	opt := options{
		listen: "", home: t.TempDir(), deviceID: "A0B1C2D3E4F5", name: "",
		noAudio: false, dev: false, printIdentity: false, printOSAPI: false,
	}
	speaker := newSpeaker(opt, "Keuken", slog.New(slog.DiscardHandler))

	info, err := speaker.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if info.DeviceID != "A0B1C2D3E4F5" || info.Name != "Keuken" || info.Type != "SoundTouch 20" ||
		info.Software != "1.2.3" {
		t.Fatalf("info = %+v, want this device, name, model and app version", info)
	}
	// The address comes from maintainIP once the network has one, not from a default.
	if info.IP != "" {
		t.Fatalf("IP before the network reported one = %q, want empty", info.IP)
	}
}

// A start that stops after reading stock's leftovers but before storing them (a crash, the
// watchdog, a reboot) takes them over again on the next start: the takeover is recorded
// only once its name, presets and clock are stored.
//
//nolint:paralleltest // swaps package variables other tests read: stockConfigPath
func TestStockTakeoverRecordedOnlyWhenFinished(t *testing.T) {
	dir := t.TempDir()
	old := stockConfigPath

	t.Cleanup(func() { stockConfigPath = old })

	stockConfigPath = filepath.Join(dir, "SystemConfigurationDB.xml")
	must(t, os.WriteFile(stockConfigPath,
		[]byte("<SystemConfiguration><DeviceName>Woonkamer</DeviceName></SystemConfiguration>"), 0o600))

	home := t.TempDir()
	log := slog.New(slog.DiscardHandler)

	first := stockTakeover(home, log)
	if first.name == nil || first.name() != "Woonkamer" {
		t.Fatal("the first start does not take stock's name over")
	}

	if stockTaken(home) {
		t.Fatal("the takeover is recorded before it was stored")
	}

	again := stockTakeover(home, log) // the first start stopped before finish
	if again.name == nil {
		t.Fatal("a start after one that stopped half-way does not take stock over again")
	}

	again.finish(log)

	if !stockTaken(home) {
		t.Fatal("finish did not record the takeover")
	}

	if after := stockTakeover(home, log); after.name != nil || after.presets != nil || after.clock != nil {
		t.Fatal("a start after a finished takeover takes stock over again")
	}

	stockTakeover(home, log).finish(log) // nothing to record: no error, still recorded

	if !stockTaken(home) {
		t.Fatal("the record went away")
	}
}

// The takeover logs what each of stock's files gave, so a first start that brings nothing
// over says why: a missing file, a file that does not parse, a preset left out.
//
//nolint:paralleltest // swaps package variables other tests read: stockConfigPath, stockPresetsPath, stockClockPath
func TestStockTakeoverLogsWhy(t *testing.T) {
	dir := t.TempDir()
	oldConfig, oldPresets, oldClock := stockConfigPath, stockPresetsPath, stockClockPath

	t.Cleanup(func() { stockConfigPath, stockPresetsPath, stockClockPath = oldConfig, oldPresets, oldClock })

	stockConfigPath = filepath.Join(dir, "SystemConfigurationDB.xml")
	stockPresetsPath = filepath.Join(dir, "Presets.xml")
	stockClockPath = filepath.Join(dir, "ClockDisplay.xml")

	must(t, os.WriteFile(stockConfigPath, []byte("<SystemConfiguration><DeviceName>"), 0o600))
	must(t, os.WriteFile(stockPresetsPath, []byte(`<presets>`+
		`<preset id="1"><ContentItem source="TUNEIN" location="/v1/playback/station/s17523">`+
		`<itemName>NPO Radio 1</itemName></ContentItem></preset>`+
		`<preset id="2"><ContentItem source="SPOTIFY" location="/playback/x">`+
		`<itemName>Mix</itemName></ContentItem></preset>`+
		`<preset id="3"><ContentItem source="TUNEIN" location="/v1/other/s1">`+
		`<itemName>Odd</itemName></ContentItem></preset>`+
		`</presets>`), 0o600))

	var buf bytes.Buffer

	log := slog.New(slog.NewTextHandler(&buf, nil))
	leftovers := stockTakeover(t.TempDir(), log)

	if got := leftovers.name(); got != "" {
		t.Fatalf("a broken configuration gave the name %q", got)
	}

	if got := leftovers.presets(); len(got) != 1 || got[0].Slot != 1 {
		t.Fatalf("presets = %+v", got)
	}

	_, ok := leftovers.clock()
	if ok {
		t.Fatal("no clock file gave clock settings")
	}

	out := buf.String()
	for _, want := range []string{
		`level=WARN msg="stock name: not read"`,
		`msg="stock preset not taken over" slot=2 source=SPOTIFY why="not a TuneIn station"`,
		`msg="stock preset not taken over" slot=3 source=TUNEIN why="unknown TuneIn location /v1/other/s1"`,
		`msg="stock presets read" taken=1 skipped=2`,
		`msg="stock clock settings: none to take over"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s:\n%s", want, out)
		}
	}

	must(t, os.Remove(stockPresetsPath))
	buf.Reset()
	stockTakeover(t.TempDir(), log).presets()

	if !strings.Contains(buf.String(), `level=INFO msg="stock presets: no file"`) {
		t.Errorf("a missing Presets.xml is not logged as such:\n%s", buf.String())
	}
}
