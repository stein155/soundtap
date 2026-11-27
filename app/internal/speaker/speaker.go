// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package speaker holds the SoundTouch speaker types the web API and speakerstate share.
//
//nolint:revive // max-public-structs: this package exists to hold the shared API types
package speaker

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// InternetRadioIcon is the stock SoundTouch local display asset for internet
// radio. ST20 can render this file path on its OLED; browsers cannot.
const InternetRadioIcon = "file:///opt/Bose/Internet-Radio.png"

// Info is the speaker's identity as /api/info, multiroom peers and the MQTT bridge
// report it.
type Info struct {
	DeviceID string // stable id, the SCM MAC without separators where there is one
	Name     string // user-visible speaker name
	Type     string // device model, e.g. "SoundTouch 10"
	Software string // SoundTap app version
	IP       string // current LAN address
}

// NowPlaying is a trimmed view of /now_playing.
type NowPlaying struct {
	Source    string `json:"source"`
	Track     string `json:"track"`
	Artist    string `json:"artist"`
	Album     string `json:"album,omitempty"`
	Station   string `json:"station"`
	StationID string `json:"stationId,omitempty"`
	// Location is the raw ContentItem location (e.g. /v1/playback/station/s12345 for
	// TuneIn or an http URL for UPnP). Empty for sources that cannot be replayed.
	Location   string `json:"location,omitempty"`
	Art        string `json:"art"`
	PlayStatus string `json:"playStatus"`
	// CanSkip: next and previous work on this source through the transport keys, as they do
	// on a media server's folder. Spotify has its own skips and does not set this.
	CanSkip bool `json:"canSkip,omitempty"`
	// Failed: this attempt ended without ever playing, as opposed to the user stopping it.
	Failed bool `json:"failed,omitempty"`
	// Unheard: this selection has not played a sample yet, paused or not.
	Unheard bool `json:"-"`
}

// Preset is one of the speaker's native preset buttons, as reported by /presets.
type Preset struct {
	Slot      int    `json:"slot"`
	Name      string `json:"name"`
	StationID string `json:"stationId"` // TuneIn guide id parsed from the location
	Source    string `json:"source,omitempty"`
	Type      string `json:"type,omitempty"`
	Location  string `json:"location"`
	Logo      string `json:"logo,omitempty"`
}

// OwnStream reports whether a station is a stream URL the user gave rather than a TuneIn
// guide id. Such a URL is its own id and its own location (ADR 0041).
func OwnStream(station string) bool {
	return strings.HasPrefix(station, "http://") || strings.HasPrefix(station, "https://")
}

// StationLocation is the ContentItem location of a station.
func StationLocation(station string) string {
	if OwnStream(station) {
		return station
	}

	return "/v1/playback/station/" + station
}

// The bounds CheckPreset holds a preset's name and logo to.
const (
	maxPresetNameRunes = 64   // characters in a preset name, as for the speaker name
	maxPresetLogoBytes = 2048 // bytes in a preset logo URL
)

// The reasons CheckPreset refuses a preset; the messages reach the client as they are.
var (
	ErrPresetNameEmpty   = errors.New("preset name is empty")
	ErrPresetNotUTF8     = errors.New("preset name or logo is not valid UTF-8")
	ErrPresetNameTooLong = errors.New("preset name must be at most 64 characters")
	ErrPresetLogoTooLong = errors.New("preset logo must be at most 2048 bytes")
	ErrPresetControlChar = errors.New("preset name or logo contains a control character")
)

// PresetSlots is how many presets a speaker holds, on slots 1..PresetSlots: one per
// preset key.
const PresetSlots = 6

// CheckPreset validates what a client may store as a preset's name and logo: they land
// on NAND, in every presets read and SSE push, on the OLED and in MQTT. The name
// follows the speaker name's rules; the logo is a URL, bounded but not fetched here.
func CheckPreset(name, logo string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return ErrPresetNameEmpty
	case !utf8.ValidString(name) || !utf8.ValidString(logo):
		return ErrPresetNotUTF8
	case utf8.RuneCountInString(name) > maxPresetNameRunes:
		return ErrPresetNameTooLong
	case len(logo) > maxPresetLogoBytes:
		return ErrPresetLogoTooLong
	case strings.IndexFunc(name+logo, unicode.IsControl) >= 0:
		return ErrPresetControlChar
	}

	return nil
}

// Notification is an audio notification: a short clip that interrupts whatever is playing
// (or wakes the speaker from standby) and then resumes the previous source. Only URL is
// required.
type Notification struct {
	URL    string // http(s) URL of the clip; MP3/AAC/WMA/etc. Required.
	Volume int    // playback level of the clip, on the music's curve; 0 plays it at the current volume
	Artist string // shown as NowPlaying artist (<service>)
	Album  string // shown as NowPlaying album (<message>)
	Track  string // shown as NowPlaying track (<reason>)
}

// Bass is the speaker's bass level + its capability range.
type Bass struct {
	Actual  int `json:"actual"`
	Target  int `json:"target"`
	Min     int `json:"min"`
	Max     int `json:"max"`
	Default int `json:"default"`
}

// Soundbar is what a soundbar's processor says about the speakers wirelessly paired to it:
// the bass module and the rears. Paired but not connected means it is switched off or out
// of range.
type Soundbar struct {
	BassPaired     bool `json:"bassPaired"`
	BassConnected  bool `json:"bassConnected"`
	RearsPaired    bool `json:"rearsPaired"`
	RearsConnected bool `json:"rearsConnected"`
}

// ToneControl is one soundbar tone level with the range the processor allows.
type ToneControl struct {
	Value int `json:"value"`
	Min   int `json:"min"`
	Max   int `json:"max"`
	Step  int `json:"step"`
}

// Tone is a soundbar's tone and speaker levels.
type Tone struct {
	Bass   ToneControl `json:"bass"`
	Treble ToneControl `json:"treble"`
	Centre ToneControl `json:"centre"`
	Rear   ToneControl `json:"rear"`
}

// Network is a compact, read-only view of the speaker's active network connection,
// read from /networkInfo (the connected interface — Wi-Fi preferred).
type Network struct {
	Type   string `json:"type"`             // "wifi" or "ethernet"
	SSID   string `json:"ssid,omitempty"`   // Wi-Fi network name
	Signal string `json:"signal,omitempty"` // "excellent" | "good" | "fair" | "poor"
	IP     string `json:"ip,omitempty"`     // current LAN address
	// The Wi-Fi link in detail, as stock's network page showed it; absent when unknown.
	Dbm      int `json:"dbm,omitempty"`      // RSSI
	LinkMbps int `json:"linkMbps,omitempty"` // link speed
	MHz      int `json:"mhz,omitempty"`      // channel frequency: 2.4, 5 or 6 GHz band
}

// WifiNetwork is one nearby Wi-Fi network seen during a site survey (see ScanWifi).
type WifiNetwork struct {
	SSID   string `json:"ssid"`
	Signal string `json:"signal,omitempty"` // "excellent" | "good" | "fair" | "poor"
	Secure bool   `json:"secure"`           // needs a password to join
	WEP    bool   `json:"wep,omitempty"`    // its password is a WEP key: Privacy without WPA (ADR 0136)
}

// The methods below let the legacy XML client satisfy web.Backend alongside the in-process
// speakerstate. They map to the closest stock SoundTouch action so the same handlers work
// against a real speaker (these mirror the fallbacks the web layer used to type-assert for).

// Member is one speaker in a multiroom zone: its deviceID (which is also the
// speaker's MAC, used as the zone's master id and a member's text content) and
// its current LAN address.
type Member struct {
	DeviceID string `json:"deviceId"`
	IP       string `json:"ip"`
}

// Zone is the speaker's current multiroom grouping as reported by /getZone. Master
// is the master speaker's deviceID (empty when the speaker is not in any zone);
// Members lists every speaker in the zone (the master first, then the slaves).
type Zone struct {
	Master  string   `json:"master"`
	Members []Member `json:"members"`
}
