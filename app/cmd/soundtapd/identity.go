// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/hw/model"
	"github.com/stein155/soundtap/app/internal/radiobrowser"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/store"
)

const (
	manufacturingPartition = "NAND.Manufacturing-Data"
	// Layout 2 (ST10, Wireless Link) keeps the packaged-product serial at 0x1e0 in a
	// 32-byte field; layout 1 (ST20) keeps it at 0x119 in a 25-byte field, right after
	// the 25-byte PCB serial at 0x100. Both read off the bench speakers' NAND backups.
	packagedSerialOffset = 0x1e0
	serialFieldSize      = 32
	v1SerialOffset       = 0x119
	v1SerialFieldSize    = 25
	// manufacturingMagic opens the manufacturing data, little-endian.
	manufacturingMagic = 0xb05e
)

// Why the manufacturing data gave no serial.
var (
	errManufacturingMissing  = errors.New("NAND.Manufacturing-Data is missing from /proc/mtd")
	errManufacturingTooSmall = errors.New("NAND.Manufacturing-Data is too small")
	errManufacturingMagic    = errors.New("invalid manufacturing-data magic")
	errManufacturingLayout   = errors.New("unsupported manufacturing-data layout")
	errSerialInvalid         = errors.New("invalid packaged-product serial")
	errSerialPadding         = errors.New("invalid packaged-product serial padding")
	errSerialEmpty           = errors.New("packaged-product serial is empty")
)

// The sizes of what the speaker's name becomes and of what is read of stock's files.
const (
	macSuffixDigits    = 6        // the end of the MAC a default name carries
	dnsLabelMax        = 63       // the longest DNS label, so the longest DHCP host name
	stockXMLLimit      = 64 << 10 // stock's SystemConfigurationDB.xml and Presets.xml
	stockClockXMLLimit = 16 << 10 // stock's ClockDisplay.xml
	nameDirMode        = 0o755
	nameFileMode       = 0o644 // bt.sh and init.d read the names
	stockTakenMode     = 0o600
	hostnameMode       = 0o600
)

// currentNamePath is the name the speaker currently answers to, as opposed to
// setupNamePath. bt.sh reads it for the Bluetooth adapter's alias: without it a phone
// scanning for speakers finds the hostname, "soundtap", on every one of them. A variable
// so a development run (-dev) keeps it with its home.
//
//nolint:gochecknoglobals // a test seam: a development run and tests keep it in their home
var currentNamePath = "/run/soundtap/name"

// setupNamePath is the speaker's out-of-box name, for the setup AP. A variable so a test
// writes it into a temp dir, not into the host's /run.
//
//nolint:gochecknoglobals // a test seam: a development run and tests keep it in their home
var setupNamePath = "/run/soundtap/default-name"

// deviceSerial reads the manufacturing serial once per process.
//
//nolint:gochecknoglobals // process-wide state: the serial read once, for every caller
var deviceSerial = sync.OnceValues(func() (string, error) {
	return readManufacturingSerial("/proc/mtd", "/dev")
})

func readManufacturingSerial(procMTD, devDir string) (string, error) {
	table, err := os.Open(procMTD)
	if err != nil {
		return "", fmt.Errorf("open partition table: %w", err)
	}

	mtd, err := flash.ParseProcMTD(table)
	_ = table.Close()

	if err != nil {
		return "", fmt.Errorf("parse partition table: %w", err)
	}

	part, ok := mtd[manufacturingPartition]
	if !ok {
		return "", errManufacturingMissing
	}

	if part.Size != 0 && part.Size < packagedSerialOffset+serialFieldSize {
		return "", fmt.Errorf("%w: %d bytes", errManufacturingTooSmall, part.Size)
	}

	dev, err := os.Open(filepath.Join(devDir, fmt.Sprintf("mtd%d", part.Index)))
	if err != nil {
		return "", fmt.Errorf("open %s: %w", manufacturingPartition, err)
	}
	defer dev.Close()

	data := make([]byte, packagedSerialOffset+serialFieldSize)

	_, err = io.ReadFull(dev, data)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", manufacturingPartition, err)
	}

	field, err := serialField(data)
	if err != nil {
		return "", err
	}

	return packagedSerial(field)
}

// serialField is the packaged-product serial's field in the manufacturing data, by layout.
func serialField(data []byte) ([]byte, error) {
	if binary.LittleEndian.Uint16(data[0:2]) != manufacturingMagic {
		return nil, errManufacturingMagic
	}

	switch version, word := binary.LittleEndian.Uint16(data[2:4]), binary.LittleEndian.Uint16(data[4:6]); {
	case version == 2 && word == 0x384:
		return data[packagedSerialOffset : packagedSerialOffset+serialFieldSize], nil
	case version == 1 && word == 4:
		return data[v1SerialOffset : v1SerialOffset+v1SerialFieldSize], nil
	default:
		return nil, errManufacturingLayout
	}
}

// packagedSerial is the printable serial at the start of field, padded with 0x00 or 0xff.
func packagedSerial(field []byte) (string, error) {
	end := len(field)
	for i, char := range field {
		if char == 0 || char == 0xff {
			end = i

			break
		}

		if char < 0x21 || char > 0x7e {
			return "", errSerialInvalid
		}
	}

	for _, pad := range field[end:] {
		if pad != 0 && pad != 0xff {
			return "", errSerialPadding
		}
	}

	if end == 0 {
		return "", errSerialEmpty
	}

	return string(field[:end]), nil
}

// identityDefaults picks the name a speaker answers to before anyone has named it, and
// the id it reports.
//
// The name is "SoundTap", the model, and the last six hex digits of this speaker's own MAC
// -- "SoundTap ST10 C3D411". It says what the box runs, which box it is, and which model,
// the way the label on the back of every other network device does. It is also the setup
// AP's SSID (the Wi-Fi manager reads the speaker name), so the network a phone sees
// while provisioning names the speaker it belongs to.
//
// The manufacturing serial used to be the default and it was none of that:
// "SERIAL0000000010A" tells its owner nothing, and two speakers of one model can only be
// told apart by reading it letter for letter.
//
// Falling back to the model type keeps a speaker whose MAC cannot be read from becoming
// nameless; SOUNDTAP_DEFAULT_NAME still overrides everything, which is how the init script
// carries a name across a reinstall.
func identityDefaults(speakerModel *model.Model, _ string) (string, string) {
	deviceID := os.Getenv("SOUNDTAP_DEVICE_ID")
	if deviceID == "" {
		deviceID = macDeviceID(speakerModel)
	}

	name := os.Getenv("SOUNDTAP_DEFAULT_NAME")
	if name == "" {
		name = "SoundTap " + strings.ToUpper(speakerModel.Name)
		if len(deviceID) >= macSuffixDigits {
			name += " " + deviceID[len(deviceID)-macSuffixDigits:]
		}
	}

	if deviceID == "" {
		deviceID = "SOUNDTAP" + strings.ToUpper(speakerModel.Name)
	}

	return name, deviceID
}

// netSysDir is where interface MACs are read from; a test points it elsewhere.
//
//nolint:gochecknoglobals // a test seam: tests point it at a fake /sys/class/net
var netSysDir = "/sys/class/net"

// macDeviceID is the MAC of this speaker's own interface as upper-case hex without
// separators. It is what init.d/soundtap exports as SOUNDTAP_DEVICE_ID, and the shape
// stock uses for its own id -- this board's stock firmware reported itself as
// 020000A1B230. Only this fallback path needs it, for a daemon started without that
// environment: without it every speaker of one model answers to "SOUNDTAPST10", and
// two of them on one network are indistinguishable.
//
// The init script normally exports the AM335x's own MAC (mac_id0) instead, which is
// stock's deviceID; this order is its fallback when that cannot be read. The ST20 has
// a wired port that stock itself reports, the others do not.
func macDeviceID(speakerModel *model.Model) string {
	order := []string{"wlan0", "eth0"}
	if speakerModel.Name == model.ST20 {
		order = []string{"eth0", "wlan0"}
	}

	for _, iface := range order {
		data, err := os.ReadFile(filepath.Join(netSysDir, iface, "address"))
		if err != nil {
			continue
		}

		mac := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(string(data)), ":", ""))
		// All zeroes is what an interface reports before its firmware has loaded.
		if len(mac) != 12 || strings.Trim(mac, "0") == "" {
			continue
		}

		if strings.Trim(mac, "0123456789ABCDEF") != "" {
			continue
		}

		return mac
	}

	return ""
}

func shouldInitializeName(speakerModel *model.Model, stored, serial string) bool {
	if stored == "" || stored == "Bose "+speakerModel.Type+" (SoundTap)" {
		return true
	}
	// The manufacturing serial was the default for a while, so a speaker carrying exactly
	// its own serial was never named by anyone -- replace it. Only the literal serial:
	// somebody who types their serial as a name keeps it, which costs nothing to allow.
	if serial != "" && stored == serial {
		return true
	}

	if speakerModel.Name == model.ST20 && stored == "Bose ST10 (SoundTap)" {
		return true
	}

	prefix := "SoundTap-" + strings.ToUpper(speakerModel.Name) + "-"
	if suffix, ok := strings.CutPrefix(stored, prefix); ok && len(suffix) == 6 {
		for _, c := range suffix {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}

		return true
	}

	return false
}

func publishSetupName(name string) error { return publishName(setupNamePath, name) }

// publishCurrentName records the name the speaker answers to now, so processes that
// soundtapd does not start -- bt.sh and the BlueZ adapter alias -- can follow a rename.
// It also becomes the host name the speaker gives its router: set now, and kept
// in hostnameFile for init.d, which names the host before the first DHCP lease.
func publishCurrentName(name string) error {
	return errors.Join(publishHostname(name), publishName(currentNamePath, name))
}

func publishHostname(name string) error {
	host := dhcpHostname(name)
	if host == "" {
		// A name with nothing DNS-safe left: drop the old room name, or init.d would
		// keep giving the router the name from before the rename. soundtap-<mac6> from
		// the next boot.
		err := os.Remove(hostnameFile)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("drop the old host name: %w", err)
		}

		return nil
	}

	_ = setHostname([]byte(host)) // not root (a development run): the router keeps the old name
	// Every boot publishes the name again: write the flash only when it changed.
	old, err := os.ReadFile(hostnameFile)
	if err != nil || string(old) != host+"\n" {
		return publishName(hostnameFile, host)
	}

	return nil
}

// setHostname sets the kernel's hostname; hostnameFile (home.go) keeps it for init.
//
//nolint:gochecknoglobals // a hook: the root helper and a development run replace it, tests stub it
var setHostname = func(host []byte) error { return os.WriteFile("/proc/sys/kernel/hostname", host, hostnameMode) }

// dhcpHostname turns a speaker name into a DNS label as stock does for its friendly
// name: lowercase a-z, 0-9 and single hyphens, at most 63 characters. "" when nothing
// is left (an all-emoji or all-CJK name); init.d then keeps soundtap-<mac6>.
// Accented Latin letters lose their accent first, so "Küche" becomes "kuche", not "k-che".
func dhcpHostname(name string) string {
	var label strings.Builder

	dash := false

	for _, char := range latinFold().Replace(strings.ToLower(name)) {
		switch {
		case char >= 'a' && char <= 'z' || char >= '0' && char <= '9':
			if dash && label.Len() > 0 {
				label.WriteByte('-')
			}

			dash = false

			label.WriteRune(char)
		default:
			dash = true
		}

		if label.Len() >= dnsLabelMax {
			break
		}
	}

	return strings.TrimRight(label.String()[:min(label.Len(), dnsLabelMax)], "-")
}

// latinFold maps the accented letters of the stock UI languages to plain ASCII.
// A fixed table, not Unicode decomposition: golang.org/x/text is not a dependency.
func latinFold() *strings.Replacer {
	return strings.NewReplacer(
		"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "ą", "a", "ă", "a", "æ", "ae",
		"ç", "c", "ć", "c", "č", "c", "ď", "d", "đ", "d",
		"è", "e", "é", "e", "ê", "e", "ë", "e", "ę", "e", "ě", "e",
		"ì", "i", "í", "i", "î", "i", "ï", "i", "ł", "l", "ľ", "l",
		"ñ", "n", "ń", "n", "ň", "n",
		"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o", "ő", "o", "œ", "oe",
		"ř", "r", "ś", "s", "š", "s", "ș", "s", "ş", "s", "ß", "ss", "ť", "t", "ț", "t", "ţ", "t",
		"ù", "u", "ú", "u", "û", "u", "ü", "u", "ů", "u", "ű", "u",
		"ý", "y", "ÿ", "y", "ź", "z", "ż", "z", "ž", "z",
	)
}

func publishName(path, name string) error {
	err := os.MkdirAll(filepath.Dir(path), nameDirMode)
	if err != nil {
		return fmt.Errorf("publish name: %w", err)
	}

	err = atomicjson.WriteFile(path, []byte(name+"\n"), nameFileMode)
	if err != nil {
		return fmt.Errorf("publish name: %w", err)
	}

	return nil
}

// speakerIdentity is what this speaker is called by anything that has to remember it
// across a rename or a new DHCP lease: a device list, a group, a proxy route, a snapcast
// host id. The factory serial when the manufacturing partition could be read, and the
// MAC-derived device id when it could not -- the same rule web.Server.identity applies,
// so /api/devices and the audio layer cannot disagree about what this box is called.
func speakerIdentity(serial, deviceID string) string {
	if serial != "" {
		return serial
	}

	return deviceID
}

// stockDeviceName is the name stock knew the speaker by, or "" when there is none.
func stockDeviceName() string {
	name, _ := readStockDeviceName()

	return name
}

// readStockDeviceName reads the name from stock's SystemConfigurationDB.xml: "" with the
// reason when there is none (fs.ErrNotExist for no file), "" and nil for an empty name.
func readStockDeviceName() (string, error) {
	file, err := os.Open(stockConfigPath)
	if err != nil {
		return "", fmt.Errorf("open stock configuration: %w", err)
	}
	defer file.Close()

	var config struct {
		DeviceName string `xml:"DeviceName"`
	}

	err = xml.NewDecoder(io.LimitReader(file, stockXMLLimit)).Decode(&config)
	if err != nil {
		return "", fmt.Errorf("parse stock configuration: %w", err)
	}

	return strings.TrimSpace(config.DeviceName), nil
}

// stockTakenName marks a home whose first start after an install from stock is behind it.
// A factory reset removes presets and settings but keeps this file, so the next start does
// not take the reset for that first start and bring stock's name and presets back.
const stockTakenName = ".stock-imported"

// stockLeftovers reads what stock left behind for this start to take over: its name, its
// presets and its clock settings. Each is nil when there is nothing to take over. home is
// set only on the first start after an install from stock: finish then records that the
// takeover is behind it.
type stockLeftovers struct {
	name    func() string
	presets func() stockPresetSet
	clock   func() (settings.Display, bool)
	home    string
}

// stockTakeover is what this start takes over from stock: everything on the first start
// after an install from Bose's software, nothing otherwise. Each part logs what stock's
// file gave, so a takeover that brings nothing says why.
func stockTakeover(home string, log *slog.Logger) stockLeftovers {
	if stockTaken(home) {
		return stockLeftovers{name: nil, presets: nil, clock: nil, home: ""}
	}

	log.Info("first start after an install from stock: taking over its name, presets and clock")

	return stockLeftovers{
		name: func() string {
			name, err := readStockDeviceName()
			logStockRead(log, "name", stockConfigPath, err)

			return name
		},
		presets: func() stockPresetSet {
			presets, skipped, err := readStockPresets()
			logStockRead(log, "presets", stockPresetsPath, err)

			for _, skip := range skipped {
				log.Info("stock preset not taken over", "slot", skip.slot, "source", skip.source, "why", skip.why)
			}

			for _, wait := range presets.pending {
				log.Info("stock preset waits for a Radio Browser lookup", "slot", wait.Slot, "name", wait.Name)
			}

			if err == nil {
				log.Info("stock presets read",
					"taken", len(presets.taken), "pending", len(presets.pending), "skipped", len(skipped))
			}

			return presets
		},
		clock: func() (settings.Display, bool) {
			display, ok := stockClock()
			if !ok {
				log.Info("stock clock settings: none to take over", "path", stockClockPath)
			}

			return display, ok
		},
		home: home,
	}
}

// finish records that the takeover is behind this home, after its name, presets and clock
// were stored: a start that stops before that takes them over again next time.
func (s stockLeftovers) finish(log *slog.Logger) {
	if s.home == "" {
		return
	}

	err := markStockTaken(s.home)
	if err != nil {
		log.Warn("record stock takeover", "err", err)

		return
	}

	log.Info("stock takeover recorded")
}

// logStockRead logs why reading one of stock's files gave nothing; a missing file is no
// fault (stock may never have written it), anything else is.
func logStockRead(log *slog.Logger, what, path string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist):
		log.Info("stock "+what+": no file", "path", path)
	default:
		log.Warn("stock "+what+": not read", "path", path, "err", err)
	}
}

// stockTaken reports whether stock's name and presets were taken over already.
func stockTaken(home string) bool {
	_, err := os.Stat(filepath.Join(home, stockTakenName))

	return !errors.Is(err, fs.ErrNotExist)
}

func markStockTaken(home string) error {
	err := os.WriteFile(filepath.Join(home, stockTakenName), nil, stockTakenMode)
	if err != nil {
		return fmt.Errorf("mark stock taken over: %w", err)
	}

	return nil
}

// stockPresetItem is what a preset in stock's Presets.xml plays.
type stockPresetItem struct {
	Source   string `xml:"source,attr"`
	Location string `xml:"location,attr"`
	Name     string `xml:"itemName"`
	Art      string `xml:"containerArt"`
}

// stockPreset is one preset in stock's Presets.xml.
type stockPreset struct {
	Slot int             `xml:"id,attr"`
	Item stockPresetItem `xml:"ContentItem"`
}

// stockPresetSet is what stock's Presets.xml gives: the TuneIn presets taken over as they
// are, and the Radio Browser presets that wait for their stream address to be looked up.
type stockPresetSet struct {
	taken   []store.Preset
	pending []pendingStockPreset
}

// stockPresets reads the radio presets stock left behind. Only TuneIn and Radio Browser
// stations: stock's other sources need its accounts or cloud, so those slots stay empty.
func stockPresets() stockPresetSet {
	presets, _, _ := readStockPresets()

	return presets
}

// stockSkip is a preset in stock's file that is not taken over, and why.
type stockSkip struct {
	slot   int
	source string
	why    string
}

// readStockPresets reads stock's Presets.xml: the TuneIn presets to take over, the Radio
// Browser presets to look up, the others with the reason each is left out, and why the file
// gave nothing (fs.ErrNotExist for no file).
func readStockPresets() (stockPresetSet, []stockSkip, error) {
	var none stockPresetSet

	file, err := os.Open(stockPresetsPath)
	if err != nil {
		return none, nil, fmt.Errorf("open stock presets: %w", err)
	}
	defer file.Close()

	var doc struct {
		Presets []stockPreset `xml:"preset"`
	}

	err = xml.NewDecoder(io.LimitReader(file, stockXMLLimit)).Decode(&doc)
	if err != nil {
		return none, nil, fmt.Errorf("parse stock presets: %w", err)
	}

	var (
		out     stockPresetSet
		skipped []stockSkip
	)

	for _, preset := range doc.Presets {
		name, art := strings.TrimSpace(preset.Item.Name), strings.TrimSpace(preset.Item.Art)

		why := stockPresetProblem(preset.Slot, preset.Item, name, art)
		if why != "" {
			skipped = append(skipped, stockSkip{slot: preset.Slot, source: preset.Item.Source, why: why})

			continue
		}

		if preset.Item.Source == sourceRadioBrowser {
			out.pending = append(out.pending, pendingStockPreset{
				Slot: preset.Slot, UUID: stockRadioBrowserUUID(preset.Item.Location), Name: name, Logo: art,
				FirstSeen: time.Time{},
			})

			continue
		}

		out.taken = append(out.taken, store.Preset{
			Slot: preset.Slot, StationID: stockStation(preset.Item.Location), Source: sourceTuneIn,
			Type: "stationurl", Location: preset.Item.Location, Name: name, Logo: art,
		})
	}

	return out, skipped, nil
}

// stockStation is the TuneIn station a stock preset's location names, "" for none.
func stockStation(location string) string {
	station, found := strings.CutPrefix(location, "/v1/playback/station/")
	if !found {
		return ""
	}

	return station
}

// stockRadioBrowserUUID is the Radio Browser station a stock preset's location names, ""
// for none.
func stockRadioBrowserUUID(location string) string {
	uuid, found := strings.CutPrefix(location, "/stations/byuuid/")
	if !found || !radiobrowser.ValidUUID(uuid) {
		return ""
	}

	return uuid
}

// stockPresetProblem is why a preset in stock's file is not taken over or looked up, ""
// when it is.
func stockPresetProblem(slot int, item stockPresetItem, name, art string) string {
	if slot < 1 || slot > speaker.PresetSlots {
		return "no preset slot " + strconv.Itoa(slot)
	}

	switch item.Source {
	case sourceTuneIn:
		if stockStation(item.Location) == "" {
			return "unknown TuneIn location " + item.Location
		}
	case sourceRadioBrowser:
		if stockRadioBrowserUUID(item.Location) == "" {
			return "unknown Radio Browser location " + item.Location
		}
	default:
		return "not a TuneIn or Radio Browser station"
	}

	err := speaker.CheckPreset(name, art)
	if err != nil {
		return err.Error()
	}

	return ""
}

// stockClock is the ST20 standby clock as stock left it (ClockDisplay.xml, every field an
// attribute of clockConfig; the zone also as the /mnt/nv/localtime link), in SoundTap's
// form; ok is false when stock left nothing to go by. Stock's clock is on by default and
// off with userEnable false or brightness 0; it does not dim otherwise, and neither does
// SoundTap's, so any other brightness changes nothing. A zone this build does not know is left out, and without a
// zone only an off clock is taken.
func stockClock() (settings.Display, bool) {
	var doc struct {
		Config stockClockConfig `xml:"clockConfig"`
	}

	var display settings.Display

	found := false

	file, err := os.Open(stockClockPath)
	if err == nil {
		found = xml.NewDecoder(io.LimitReader(file, stockClockXMLLimit)).Decode(&doc) == nil
		_ = file.Close()
	}

	config := doc.Config
	if found {
		display.ClockOff = config.Enable == "false" || strings.TrimSpace(config.Brightness) == "0"
		display.Hour12 = config.Format == "TIME_FORMAT_12HOUR_ID"
	}

	zone := strings.TrimSpace(config.Timezone)
	if zone == "" {
		target, linkErr := os.Readlink(stockLocaltimePath)
		if linkErr == nil {
			zone, _ = strings.CutPrefix(target, "/usr/share/zoneinfo/")
			if zone == target {
				zone = ""
			}
		}
	}

	if zone != "" {
		_, zoneErr := time.LoadLocation(zone)
		if zoneErr == nil {
			display.Timezone = zone
		}
	}

	_, err = settings.NormalizeDisplay(display)
	if err != nil {
		return settings.Display{ClockOff: false, Hour12: false, Timezone: ""}, false
	}

	return display, found || display.Timezone != ""
}

// stockClockConfig is the clockConfig element of stock's ClockDisplay.xml.
type stockClockConfig struct {
	Timezone   string `xml:"timezoneInfo,attr"`
	Enable     string `xml:"userEnable,attr"`
	Format     string `xml:"timeFormat,attr"`
	Brightness string `xml:"brightnessLevel,attr"`
}

// takeStockClock gives the standby clock stock's settings on the first start after an
// install from Bose's software, so an ST20 shows its clock as before without the settings
// first being opened in a browser. Only on a speaker with a panel, and only while the
// clock has no settings of its own. fromStock reads stock's clock settings; it is nil when
// this is not that first start or the speaker has no panel.
func takeStockClock(set *settings.Store, fromStock func() (settings.Display, bool), log *slog.Logger) {
	if fromStock == nil || set.Get().Display != (settings.Display{ClockOff: false, Hour12: false, Timezone: ""}) {
		return
	}

	display, ok := fromStock()
	if !ok {
		return
	}

	err := set.SetDisplay(display)
	if err != nil {
		log.Warn("clock settings from stock not kept", "err", err)

		return
	}

	log.Info("clock settings taken over from stock",
		"off", display.ClockOff, "hour12", display.Hour12, "zone", display.Timezone)
}

// initialName gives a new speaker its name and returns the one it goes by. stockName reads
// stock's name on the first start after an install from Bose's software, and is nil otherwise.
func initialName(set *settings.Store, serial, name string, stockName func() string, log *slog.Logger) string {
	if shouldInitializeName(&board, set.Get().Name, serial) {
		// Not fatal: a full or read-only /mnt/nv must not crash-loop soundtapd before
		// the boot is confirmed. The speaker runs under the default name instead.
		// The first boot after an install from Bose's software keeps stock's name.
		first := name

		if stockName != nil {
			if stock := stockName(); stock != "" {
				first = stock
				log.Info("speaker name taken over from stock", "name", stock)
			}
		}

		err := set.SetName(first)
		if err != nil {
			log.Error("initialize speaker name", "err", err)
		}
	}

	err := publishSetupName(name)
	if err != nil {
		log.Warn("publish setup AP name", "err", err)
	}

	if identity := set.Get().Name; identity != "" {
		return identity
	}

	return name
}
