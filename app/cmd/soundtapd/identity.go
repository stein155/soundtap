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
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/hw/model"
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
)

// currentNamePath is the name the speaker currently answers to, as opposed to
// setupNamePath. bt.sh reads it for the Bluetooth adapter's alias: without it a phone
// scanning for speakers finds the hostname, "soundtap", on every one of them. A variable
// so a development run (-dev) keeps it with its home.
var currentNamePath = "/run/soundtap/name"

// setupNamePath is the speaker's out-of-box name, for the setup AP. A variable so a test
// writes it into a temp dir, not into the host's /run.
var setupNamePath = "/run/soundtap/default-name"

var deviceSerial = sync.OnceValues(func() (string, error) {
	return readManufacturingSerial("/proc/mtd", "/dev")
})

func readManufacturingSerial(procMTD, devDir string) (string, error) {
	f, err := os.Open(procMTD)
	if err != nil {
		return "", fmt.Errorf("open partition table: %w", err)
	}
	mtd, err := flash.ParseProcMTD(f)
	_ = f.Close()
	if err != nil {
		return "", fmt.Errorf("parse partition table: %w", err)
	}
	part, ok := mtd[manufacturingPartition]
	if !ok {
		return "", errors.New("NAND.Manufacturing-Data is missing from /proc/mtd")
	}
	if part.Size != 0 && part.Size < packagedSerialOffset+serialFieldSize {
		return "", fmt.Errorf("NAND.Manufacturing-Data is too small: %d bytes", part.Size)
	}

	f, err = os.Open(filepath.Join(devDir, fmt.Sprintf("mtd%d", part.Index)))
	if err != nil {
		return "", fmt.Errorf("open %s: %w", manufacturingPartition, err)
	}
	defer f.Close()
	data := make([]byte, packagedSerialOffset+serialFieldSize)
	if _, err := io.ReadFull(f, data); err != nil {
		return "", fmt.Errorf("read %s: %w", manufacturingPartition, err)
	}
	if binary.LittleEndian.Uint16(data[0:2]) != 0xb05e {
		return "", errors.New("invalid manufacturing-data magic")
	}
	var field []byte
	switch version, word := binary.LittleEndian.Uint16(data[2:4]), binary.LittleEndian.Uint16(data[4:6]); {
	case version == 2 && word == 0x384:
		field = data[packagedSerialOffset : packagedSerialOffset+serialFieldSize]
	case version == 1 && word == 4:
		field = data[v1SerialOffset : v1SerialOffset+v1SerialFieldSize]
	default:
		return "", errors.New("unsupported manufacturing-data layout")
	}

	end := len(field)
	for i, b := range field {
		if b == 0 || b == 0xff {
			end = i
			break
		}
		if b < 0x21 || b > 0x7e {
			return "", errors.New("invalid packaged-product serial")
		}
	}
	for _, b := range field[end:] {
		if b != 0 && b != 0xff {
			return "", errors.New("invalid packaged-product serial padding")
		}
	}
	if end == 0 {
		return "", errors.New("packaged-product serial is empty")
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
func identityDefaults(m model.Model, _ string) (string, string) {
	deviceID := os.Getenv("SOUNDTAP_DEVICE_ID")
	if deviceID == "" {
		deviceID = macDeviceID(m)
	}
	name := os.Getenv("SOUNDTAP_DEFAULT_NAME")
	if name == "" {
		name = "SoundTap " + strings.ToUpper(m.Name)
		if len(deviceID) >= 6 {
			name += " " + deviceID[len(deviceID)-6:]
		}
	}
	if deviceID == "" {
		deviceID = "SOUNDTAP" + strings.ToUpper(m.Name)
	}
	return name, deviceID
}

// netSysDir is where interface MACs are read from; a test points it elsewhere.
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
func macDeviceID(m model.Model) string {
	order := []string{"wlan0", "eth0"}
	if m.Name == model.ST20 {
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

func shouldInitializeName(m model.Model, stored string, serial string) bool {
	if stored == "" || stored == "Bose "+m.Type+" (SoundTap)" {
		return true
	}
	// The manufacturing serial was the default for a while, so a speaker carrying exactly
	// its own serial was never named by anyone -- replace it. Only the literal serial:
	// somebody who types their serial as a name keeps it, which costs nothing to allow.
	if serial != "" && stored == serial {
		return true
	}
	if m.Name == model.ST20 && stored == "Bose ST10 (SoundTap)" {
		return true
	}
	prefix := "SoundTap-" + strings.ToUpper(m.Name) + "-"
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
		if err := os.Remove(hostnameFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	_ = setHostname([]byte(host)) // not root (a development run): the router keeps the old name
	// Every boot publishes the name again: write the flash only when it changed.
	if old, err := os.ReadFile(hostnameFile); err != nil || string(old) != host+"\n" {
		return publishName(hostnameFile, host)
	}
	return nil
}

// setHostname sets the kernel's hostname; hostnameFile (home.go) keeps it for init.
var setHostname = func(b []byte) error { return os.WriteFile("/proc/sys/kernel/hostname", b, 0o600) }

// dhcpHostname turns a speaker name into a DNS label as stock does for its friendly
// name: lowercase a-z, 0-9 and single hyphens, at most 63 characters. "" when nothing
// is left (an all-emoji or all-CJK name); init.d then keeps soundtap-<mac6>.
// Accented Latin letters lose their accent first, so "Küche" becomes "kuche", not "k-che".
func dhcpHostname(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range latinFold.Replace(strings.ToLower(name)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
		if b.Len() >= 63 {
			break
		}
	}
	return strings.TrimRight(b.String()[:min(b.Len(), 63)], "-")
}

// latinFold maps the accented letters of the stock UI languages to plain ASCII.
// A fixed table, not Unicode decomposition: golang.org/x/text is not a dependency.
var latinFold = strings.NewReplacer(
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

func publishName(path, name string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicjson.WriteFile(path, []byte(name+"\n"), 0o644)
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
	f, err := os.Open(stockConfigPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	var c struct {
		DeviceName string `xml:"DeviceName"`
	}
	if xml.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&c) != nil {
		return ""
	}
	return strings.TrimSpace(c.DeviceName)
}

// stockTakenName marks a home whose first start after an install from stock is behind it.
// A factory reset removes presets and settings but keeps this file, so the next start does
// not take the reset for that first start and bring stock's name and presets back.
const stockTakenName = ".stock-imported"

// takeStockOnce reports whether stock's name and presets may still be taken over, and
// records that they now have been.
func takeStockOnce(home string, log *slog.Logger) bool {
	if _, err := os.Stat(filepath.Join(home, stockTakenName)); !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err := markStockTaken(home); err != nil {
		log.Warn("record stock takeover", "err", err)
	}
	return true
}

func markStockTaken(home string) error {
	return os.WriteFile(filepath.Join(home, stockTakenName), nil, 0o600)
}

// stockPresets reads the radio presets stock left behind. Only TuneIn stations:
// stock's other sources need its accounts or cloud, so those slots stay empty.
func stockPresets() []store.Preset {
	f, err := os.Open(stockPresetsPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	var doc struct {
		Presets []struct {
			Slot int `xml:"id,attr"`
			Item struct {
				Source   string `xml:"source,attr"`
				Location string `xml:"location,attr"`
				Name     string `xml:"itemName"`
				Art      string `xml:"containerArt"`
			} `xml:"ContentItem"`
		} `xml:"preset"`
	}
	if xml.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&doc) != nil {
		return nil
	}
	var out []store.Preset
	for _, p := range doc.Presets {
		name, art := strings.TrimSpace(p.Item.Name), strings.TrimSpace(p.Item.Art)
		station, ok := strings.CutPrefix(p.Item.Location, "/v1/playback/station/")
		if p.Item.Source != sourceTuneIn || !ok || station == "" || speaker.CheckPreset(name, art) != nil {
			continue
		}
		out = append(out, store.Preset{
			Slot: p.Slot, StationID: station, Source: sourceTuneIn, Type: "stationurl",
			Location: p.Item.Location, Name: name, Logo: art,
		})
	}
	return out
}

// stockClock is the ST20 standby clock as stock left it (ClockDisplay.xml, every field an
// attribute of clockConfig; the zone also as the /mnt/nv/localtime link), in SoundTap's
// form; ok is false when stock left nothing to go by. Stock's clock is on by default and
// off with userEnable false or brightness 0; it does not dim otherwise, so its brightness
// maps to SoundTap's default. A zone this build does not know is left out, and without a
// zone only an off clock is taken.
func stockClock() (d settings.Display, ok bool) {
	var doc struct {
		Config struct {
			Timezone   string `xml:"timezoneInfo,attr"`
			Enable     string `xml:"userEnable,attr"`
			Format     string `xml:"timeFormat,attr"`
			Brightness string `xml:"brightnessLevel,attr"`
		} `xml:"clockConfig"`
	}
	found := false
	if f, err := os.Open(stockClockPath); err == nil {
		found = xml.NewDecoder(io.LimitReader(f, 16<<10)).Decode(&doc) == nil
		_ = f.Close()
	}
	c := doc.Config
	if found {
		d.ClockOff = c.Enable == "false" || strings.TrimSpace(c.Brightness) == "0"
		d.Hour12 = c.Format == "TIME_FORMAT_12HOUR_ID"
	}
	zone := strings.TrimSpace(c.Timezone)
	if zone == "" {
		if target, err := os.Readlink(stockLocaltimePath); err == nil {
			zone, _ = strings.CutPrefix(target, "/usr/share/zoneinfo/")
			if zone == target {
				zone = ""
			}
		}
	}
	if zone != "" {
		if _, err := time.LoadLocation(zone); err == nil {
			d.Timezone = zone
		}
	}
	if _, err := settings.NormalizeDisplay(d); err != nil {
		return settings.Display{}, false
	}
	return d, found || d.Timezone != ""
}

// takeStockClock gives the standby clock stock's settings on the first start after an
// install from Bose's software, so an ST20 shows its clock as before without the settings
// first being opened in a browser. Only on a speaker with a panel, and only while the
// clock has no settings of its own.
func takeStockClock(set *settings.Store, fromStock, panel bool, log *slog.Logger) {
	if !fromStock || !panel || set.Get().Display != (settings.Display{}) {
		return
	}
	d, ok := stockClock()
	if !ok {
		return
	}
	if err := set.SetDisplay(d); err != nil {
		log.Warn("clock settings from stock not kept", "err", err)
		return
	}
	log.Info("clock settings taken over from stock", "off", d.ClockOff, "hour12", d.Hour12, "zone", d.Timezone)
}

// initialName gives a new speaker its name and returns the one it goes by.
func initialName(set *settings.Store, serial, name string, fromStock bool, log *slog.Logger) string {
	if shouldInitializeName(board, set.Get().Name, serial) {
		// Not fatal: a full or read-only /mnt/nv must not crash-loop soundtapd before
		// the boot is confirmed. The speaker runs under the default name instead.
		// The first boot after an install from Bose's software keeps stock's name.
		first := name
		if stock := stockDeviceName(); fromStock && stock != "" {
			first = stock
			log.Info("speaker name taken over from stock", "name", stock)
		}
		if err := set.SetName(first); err != nil {
			log.Error("initialize speaker name", "err", err)
		}
	}
	if err := publishSetupName(name); err != nil {
		log.Warn("publish setup AP name", "err", err)
	}
	if identity := set.Get().Name; identity != "" {
		return identity
	}
	return name
}
