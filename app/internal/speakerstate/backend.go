// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/wifi"
)

// SetType renames the product after the server started, for a model whose name is only known
// once it runs: the SoundTouch adapter takes its host product's.
func (s *Speaker) SetType(t string) {
	s.lock()
	s.Type = t
	s.unlock()
}

func (s *Speaker) Info(ctx context.Context) (*speaker.Info, error) {
	err := live(ctx)
	if err != nil {
		return nil, err
	}

	s.lock()
	defer s.unlock()

	return &speaker.Info{
		DeviceID: s.DeviceID,
		Name:     s.Name,
		Type:     s.Type,
		Software: s.Software,
		IP:       s.IP,
	}, nil
}

func (s *Speaker) NowPlaying(ctx context.Context) (*speaker.NowPlaying, error) {
	err := live(ctx)
	if err != nil {
		return nil, err
	}

	s.lock()
	defer s.unlock()

	if s.source == sourceStandby {
		return &speaker.NowPlaying{
			Source: sourceStandby, PlayStatus: s.playStatus, Track: "", Artist: "", Album: "", Station: "",
			StationID: "", Location: "", Art: "", CanSkip: false, Failed: false, Unheard: false,
		}, nil
	}

	station := s.station
	if s.source == sourceAUX && s.auxName != nil {
		if n := s.auxName(); n != "" {
			station = n
		}
	}

	return &speaker.NowPlaying{
		Source:     s.source,
		Track:      s.track,
		Artist:     s.artist,
		Album:      s.album,
		Station:    station,
		StationID:  stationIDFromLocation(s.location),
		Location:   s.location,
		Art:        s.art,
		PlayStatus: s.playStatus,
		Failed:     s.playStatus == stateStop && s.failedGen == s.playGen,
		Unheard:    s.unheard,
		CanSkip:    false,
	}, nil
}

func (s *Speaker) Presets(ctx context.Context) ([]speaker.Preset, error) {
	err := live(ctx)
	if err != nil {
		return nil, err
	}

	s.lock()
	defer s.unlock()

	out := make([]speaker.Preset, 0, len(s.presets))
	for _, slot := range sortedSlots(s.presets) {
		preset := s.presets[slot]
		if preset.Location == "" {
			continue
		}

		out = append(out, speaker.Preset{
			Slot:      slot,
			Name:      preset.Name,
			StationID: stationIDFromLocation(preset.Location),
			Source:    preset.Source,
			Type:      preset.Type,
			Location:  preset.Location,
			Logo:      preset.Art,
		})
	}

	return out, nil
}

func (s *Speaker) Volume(ctx context.Context) (int, error) {
	err := live(ctx)
	if err != nil {
		return 0, err
	}

	s.lock()
	defer s.unlock()

	return s.volume, nil
}

// SetVolume sets the master volume as a percentage, clamped to 0..100.
//
// A change is pushed to listeners, because this is not only the web UI's own
// slider: AirPlay senders carry their own volume control, and the Home app,
// Control Center and the phone's hardware keys all drive it. A browser showing
// the speaker must follow that, not sit on a stale number until its next poll.
func (s *Speaker) SetVolume(ctx context.Context, level int) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	level = clamp(level, 0, maxVolume)

	s.lock()
	before := s.outputLocked()
	changed := before != level || s.muted
	s.volume, s.muted = level, false // setting a level is asking to hear it
	notify := s.onPlayChange
	volChanged := s.volumeHookLocked(before)
	s.unlock()

	if volChanged != nil {
		volChanged()
	}

	if changed && notify != nil {
		notify()
	}

	return nil
}

func (s *Speaker) Bass(ctx context.Context) (*speaker.Bass, error) {
	err := live(ctx)
	if err != nil {
		return nil, err
	}

	s.lock()
	defer s.unlock()

	return &speaker.Bass{
		Actual:  s.bassActual,
		Target:  s.bassTarget,
		Min:     s.bassMin,
		Max:     s.bassMax,
		Default: s.bassDefault,
	}, nil
}

// NoBass gives the speaker stock's Wireless Link range, 0..0: no bass control.
func (s *Speaker) NoBass() {
	s.lock()
	s.bassMin, s.bassMax, s.bassTarget, s.bassActual = 0, 0, 0, 0
	s.unlock()
}

func (s *Speaker) SetBass(ctx context.Context, level int) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	s.lock()
	lvl := clamp(level, s.bassMin, s.bassMax)
	s.bassTarget, s.bassActual = lvl, lvl
	s.unlock()

	return nil
}

const (
	networkWifi     = "wifi"
	networkEthernet = "ethernet"
)

// wiredIP is eth0's IPv4 address while a cable is in, "" otherwise (no port, no cable or
// no lease yet), and always "" off the speaker. Only the ST20 and the ST300 have the port;
// the init script drops the address when the cable goes.
//
//nolint:gochecknoglobals // a hook the tests replace with a fixed address.
var wiredIP = func() string { return WiredIP(Eth0()) }

// Eth0 is what the kernel says about eth0: whether it has carrier, and its addresses. Off
// the speaker, without the port or while eth0 is down it reports neither.
func Eth0() (bool, []net.Addr) {
	// Only on the speaker: a test or dev host has an eth0 of its own.
	if os.Getenv("SOUNDTAP_FIRMWARE") != "1" {
		return false, nil
	}
	// The carrier file is absent without eth0 and unreadable while eth0 is down.
	b, err := os.ReadFile("/sys/class/net/eth0/carrier")
	if err != nil || strings.TrimSpace(string(b)) != "1" {
		return false, nil
	}

	ifc, err := net.InterfaceByName("eth0")
	if err != nil {
		return true, nil
	}

	addrs, err := ifc.Addrs()
	if err != nil {
		return true, nil
	}

	return true, addrs
}

// WiredIP is the address the speaker runs its cable on, given eth0's state: its first IPv4
// address while it has carrier, "" without carrier or before the lease.
//
//nolint:revive // flag-parameter: carrier is eth0's state as Eth0 reads it, not a mode.
func WiredIP(carrier bool, addrs []net.Addr) string {
	if !carrier {
		return ""
	}

	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return n.IP.String()
		}
	}

	return ""
}

// Wired reports whether the speaker runs on its cable: eth0 has carrier and an address.
func Wired() bool { return wiredIP() != "" }

// The setup network as the Wi-Fi manager starts it: its address, its fallback SSID and
// the 32-byte cap on an SSID.
const (
	SetupAPAddress    = wifi.APAddress
	setupSSIDFallback = wifi.APFallback
	maxSSIDBytes      = wifi.MaxSSIDBytes
)

// SetupSSID is the name of the speaker's setup network, the one the Wi-Fi manager gives
// the AP: the name soundtapd published, or the fixed fallback if there is none or it does
// not fit an SSID.
func SetupSSID() string {
	path := os.Getenv("SOUNDTAP_SETUP_NAME")
	if path == "" {
		path = "/run/soundtap/default-name"
	}

	b, err := os.ReadFile(path) //nolint:gosec // the path comes from the init script's environment

	name, _, _ := strings.Cut(string(b), "\n")
	if err != nil || name == "" || len(name) > maxSSIDBytes {
		return setupSSIDFallback
	}

	return name
}

func (s *Speaker) NetworkInfo(ctx context.Context) (*speaker.Network, error) {
	err := live(ctx)
	if err != nil {
		return nil, err
	}

	// A cable wins, as on stock: with eth0 up Wi-Fi settings no longer decide the route,
	// and the UI said "Wi-Fi" on an ST20 that was plugged in. It wins over the setup AP
	// too, which can run beside the cable (an ST20 with no Wi-Fi saved): the speaker is on
	// the LAN, not on its own setup network.
	if ip := wiredIP(); ip != "" {
		return &speaker.Network{Type: networkEthernet, IP: ip, SSID: "", Signal: "", Dbm: 0, LinkMbps: 0, MHz: 0}, nil
	}

	if marker := os.Getenv("SOUNDTAP_SETUP_AP_MARKER"); marker != "" {
		// The marker path is the init script's own setting, not input.
		_, statErr := os.Stat(marker) //nolint:gosec // see above.
		if statErr == nil {
			return &speaker.Network{
				Type: networkWifi, SSID: SetupSSID(), IP: SetupAPAddress, Signal: "", Dbm: 0, LinkMbps: 0, MHz: 0,
			}, nil
		}
	}
	// Read the live association before taking the lock: liveWifiLink takes it itself,
	// and shelling out under s.mu would stall every other API call for the duration.
	link, live := s.liveWifiLink(ctx)

	s.lock()
	defer s.unlock()

	if strings.TrimSpace(s.IP) == "" {
		return nil, errNoInterface
	}

	if os.Getenv("SOUNDTAP_WIFI_MODE") == wifiModeBCO {
		if live {
			return &speaker.Network{
				Type: networkWifi, SSID: link.ssid, IP: s.IP, Signal: "", Dbm: 0, LinkMbps: 0, MHz: 0,
			}, nil
		}

		return &speaker.Network{Type: networkEthernet, IP: s.IP, SSID: "", Signal: "", Dbm: 0, LinkMbps: 0, MHz: 0}, nil
	}
	// What wpa_supplicant reports wins: s.wifiSSID is only what someone last asked us to
	// join, which says nothing about what we are on now (and is a placeholder until the
	// first join through the UI). Fall back to it only where there is no supplicant to ask.
	if live {
		return &speaker.Network{
			Type: networkWifi, SSID: link.ssid, Signal: link.signal, Dbm: link.dbm, LinkMbps: link.mbps, MHz: link.mhz,
			IP: s.IP,
		}, nil
	}

	return &speaker.Network{Type: networkWifi, SSID: s.wifiSSID, IP: s.IP, Signal: "", Dbm: 0, LinkMbps: 0, MHz: 0}, nil
}

func (s *Speaker) SetName(ctx context.Context, name string) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	s.lock()
	s.Name = name
	s.unlock()

	return nil
}

func (s *Speaker) Key(ctx context.Context, key string) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	s.pressKey(key)

	return nil
}

func (s *Speaker) Select(ctx context.Context, source, itemType, location, name, account, art string) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	_ = itemType
	_ = account

	s.applySource(source, location, name, art)

	return nil
}

// SelectAt selects location like Select, starting at offset into it: a UPnP file the
// controller seeks in or resumes.
func (s *Speaker) SelectAt(ctx context.Context, source, location, name, art string, offset time.Duration) error {
	err := live(ctx)
	if err != nil {
		return err
	}

	s.lock()
	defer s.unlock()

	s.applySourceLocked(source, location, name, art)
	s.startAt = offset

	return nil
}

// SetSource makes an external source (AIRPLAY/BLUETOOTH/SPOTIFY) own the amp. STANDBY
// is not a source: it is a real standby, as leaving a multiroom group asks for. Taken as
// a source it reported PLAY_STATE and made STANDBY the wake target.
func (s *Speaker) SetSource(ctx context.Context, source string) error {
	if source == sourceStandby {
		return s.EnterStandby(ctx)
	}

	err := live(ctx)
	if err != nil {
		return err
	}

	s.lock()

	if s.source != source {
		if source == sourceSpotify && s.spotifyItem.Location != "" {
			p := s.spotifyItem
			s.applySourceLocked(source, p.Location, p.Name, p.Art)
		} else {
			s.applySourceLocked(source, "", source, "")
		}

		s.spotifyItem = Preset{Source: "", Type: "", Location: "", Name: "", Art: ""}
	}

	s.unlock()

	return nil
}

// maxVolume is the top of the volume scale.
const maxVolume = 100

// The refusals the speaker's API reports, as clients show them.
var (
	errNoInterface     = errors.New("no active network interface")
	errNoNotifyURL     = errors.New("notification URL is required")
	errPresetSlot      = errors.New("preset slot out of range")
	errSSIDRequired    = errors.New("ssid required")
	errOpenPassword    = errors.New("open network must not have a password")
	errWPAPassword     = errors.New("WPA password must be 8..63 bytes without control characters")
	errUnknownSecurity = errors.New("unknown security")
	errWifiDisabled    = errors.New("wifi setup is disabled on this speaker")
)

// live is nil while ctx is, and otherwise why the request ended.
func live(ctx context.Context) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("speaker state: %w", err)
	}

	return nil
}
