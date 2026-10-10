// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"

	"github.com/stein155/soundtap/app/internal/speaker"
)

// Backend is the speaker control interface, implemented by the in-process
// *speakerstate.Speaker.
type Backend interface {
	Info(ctx context.Context) (*speaker.Info, error)
	NowPlaying(ctx context.Context) (*speaker.NowPlaying, error)
	Presets(ctx context.Context) ([]speaker.Preset, error)
	Volume(ctx context.Context) (int, error)
	SetVolume(ctx context.Context, level int) error
	Bass(ctx context.Context) (*speaker.Bass, error)
	SetBass(ctx context.Context, level int) error
	NetworkInfo(ctx context.Context) (*speaker.Network, error)
	ScanWifi(ctx context.Context) ([]speaker.WifiNetwork, error)
	SetWifi(ctx context.Context, ssid, security, password string) error
	SetName(ctx context.Context, name string) error
	Key(ctx context.Context, key string) error
	Wake(ctx context.Context)
	Select(ctx context.Context, source, itemType, location, name, account, art string) error
	// Source lifecycle.
	SetSource(ctx context.Context, source string) error
	ReleaseSource(ctx context.Context, src string) error
	EnterStandby(ctx context.Context) error
	SetTrackMeta(ctx context.Context, source, track, artist, art string) error
	StorePreset(ctx context.Context, slot int, source, itemType, location, name, art string) error
	RemovePreset(ctx context.Context, slot int) error
	SwapPresets(ctx context.Context, a, b int) error
	PlayPreset(ctx context.Context, slot int) error
	PlayNotification(ctx context.Context, n speaker.Notification) error
}

// PauseRecorder is a Backend that keeps a play status of its own and can be told that
// a source paused or resumed in its own daemon (speakerstate.Speaker.SetPaused). The
// Spotify pause/resume handlers use it when the backend has it.
type PauseRecorder interface {
	SetPaused(source string, paused bool)
}

// SpotifyFollower is a Backend that can be told what Spotify really plays, for what was
// started from a phone (speakerstate.Speaker.FollowSpotify).
type SpotifyFollower interface {
	FollowSpotify(uri string)
}

// MuteReporter is a Backend whose mute is a flag beside the volume
// (speakerstate.Speaker.Muted): the volume it reports stays the level it comes back to.
type MuteReporter interface {
	Muted() bool
}

// muted is whether the backend is muted; one without the flag never is.
func muted(b Backend) bool {
	m, ok := b.(MuteReporter)
	return ok && m.Muted()
}

// Hostnamer is the mDNS responder: it reports the advertised host name (e.g.
// "soundtap-serial0000000020a.local") and re-advertises when the speaker is renamed.
type Hostnamer interface {
	Hostname() string
	SharedName() string
	SetName(name string)
}

// MQTTBridge is the MQTT bridge (internal/mqttbridge). The web layer
// only needs to nudge it to reconnect after a settings change and read its status;
// it stays an interface so web doesn't depend on the bridge's concrete type.
type MQTTBridge interface {
	Reload()
	Status() (connected bool, lastErr string)
}
