// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"time"

	"github.com/stein155/soundtap/app/internal/hw/battery"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/spotify"
	"github.com/stein155/soundtap/app/internal/upnp"
)

// AudioHooks are the audio side's: the arbiter, the multiroom group and the chimes. Only a
// speaker running its audio side has them.
type AudioHooks struct {
	// SourceChanging is the synchronous hardware handoff that must finish before an
	// external receiver's session hook returns and starts writing PCM.
	SourceChanging func(string)
	// Flush drops audio already queued for the DAC. Unlike SourceChanging it must not
	// touch source ownership — see setSource. FlushWait acknowledges a stopped
	// producer's audio boundary.
	Flush     func()
	FlushWait func(context.Context) error
	// Channel applies the stored channel; Downstream is the audio delay after the bus;
	// LeaderTrim is, while this speaker leads, how much earlier its followers must play
	// (multiroom.Env.LeaderTrim).
	Channel    func(string)
	Downstream func() time.Duration
	LeaderTrim func() time.Duration
	// MultiroomActive switches the Mixer's copy for Snapcast.
	MultiroomActive func(bool)
	// SelectSource picks the source the arbiter grants the card to, for the one case the
	// speaker changes its own source: joining or leaving another speaker's stream.
	SelectSource func(context.Context, string) error
	// LeaderLive and LeaderNow report a follower's leader: whether it answers, and what
	// it plays.
	LeaderLive func(bool)
	LeaderNow  func(speaker.NowPlaying)
	// SelfAddress is this speaker's own address on the network, for the one case it has
	// to tell another speaker where to find it.
	SelfAddress func(context.Context) string
	// RestartSnapclient kicks the supervised snapclient so it re-reads which server it
	// follows; WantAnchor starts this speaker's group anchor and keeps it for
	// multiroom.AnchorHold.
	RestartSnapclient func()
	WantAnchor        func()
	// EventChime plays a named builtin tone for an event (BT connected/cleared, grouped).
	EventChime func(name string)
	// AmpFault reports whether the amp fault GPIO is asserted, for the health warning.
	AmpFault func() bool
	// Battery is the battery as last read, for /api/health; false on a model without one.
	Battery func() (battery.Status, bool)
}

// attached is every group handed over after New, as last attached.
type attached struct {
	Audio    AudioHooks
	Spotify  SpotifyHooks
	Soundbar SoundbarHooks
	UPnP     UPnPHooks
}

// AttachAudio hands the audio side's hooks over. Call before serving starts.
func (s *Server) AttachAudio(hooks AudioHooks) { //nolint:gocritic // hugeParam: cmd/soundtapd passes the hooks by value
	s.attached.Audio = hooks
	s.sourceChanging, s.flushAudio, s.flushAudioWait = hooks.SourceChanging, hooks.Flush, hooks.FlushWait
	s.channel, s.downstream, s.multiroomActive = hooks.Channel, hooks.Downstream, hooks.MultiroomActive
	s.leaderTrim = hooks.LeaderTrim
	s.selectSource, s.leaderLive = hooks.SelectSource, hooks.LeaderLive
	s.leaderNow, s.selfAddress = hooks.LeaderNow, hooks.SelfAddress
	s.restartSnapclient, s.wantAnchor = hooks.RestartSnapclient, hooks.WantAnchor
	s.eventChime, s.ampFaultFn, s.batteryFn = hooks.EventChime, hooks.AmpFault, hooks.Battery
}

// SpotifyHooks are Spotify browsing and playback through the receiver's own session, and
// the filters on what librespot claims. Without a Client every /api/spotify route answers
// spotify_not_connected.
type SpotifyHooks struct {
	Client  *spotify.Client
	Account SpotifyAccount
	// Claim filters daemon source claims: false drops the claim. Stop filters daemon stop
	// events: false keeps the source, because the daemon is being asked to start again.
	Claim func(source string) bool
	Stop  func(source string) bool
}

// AttachSpotify hands Spotify over. Call before serving starts.
func (s *Server) AttachSpotify(h SpotifyHooks) {
	s.attached.Spotify = h
	s.spotify, s.spotifyAccount = h.Client, h.Account
	s.claimSource, s.stopSource = h.Claim, h.Stop
}

// SoundbarChoice is one of a soundbar's one-of-a-few settings: Get returns "" until the
// processor has said, Set refuses a value it does not know.
type SoundbarChoice struct {
	Get func() string
	Set func(string) error
}

// SoundbarHooks reach a soundbar's processor (the SoundTouch 300).
type SoundbarHooks struct {
	// Speakers is the paired bass module and rears, nil until the processor has answered.
	Speakers func() *speaker.Soundbar
	// Tone and SetTone read and set the tone levels; Tone returns nil until the
	// processor has answered, SetTone refuses a level outside its range.
	Tone    func() *speaker.Tone
	SetTone func(bass, treble, centre, rear int) error
	// Choices are served at /api/soundbar/<name> (audio-mode, cec-mode, hdmi-input).
	Choices map[string]SoundbarChoice
	// LipSync and SetLipSync: the delay in ms the sound waits for the TV picture, 0 to
	// LipSyncMax. LipSync says false until the soundbar has told.
	LipSync    func() (int, bool)
	SetLipSync func(int) error
	LipSyncMax int
	// StartADAPTiQ starts the calibration (POST /api/soundbar/adaptiq).
	StartADAPTiQ func() error
	// PickInput routes every user source pick to the processor instead of the AUX and
	// source keys.
	PickInput func(string) error
}

// AttachSoundbar hands a soundbar's processor over. Call before serving starts.
func (s *Server) AttachSoundbar(hooks SoundbarHooks) {
	s.attached.Soundbar = hooks
	s.soundbar, s.tone, s.setTone = hooks.Speakers, hooks.Tone, hooks.SetTone

	s.choices = map[string]soundbarChoice{}
	for name, c := range hooks.Choices {
		s.choices[name] = soundbarChoice{c.Get, c.Set}
	}

	s.lipSync, s.setLipSync, s.lipSyncMax = hooks.LipSync, hooks.SetLipSync, hooks.LipSyncMax
	s.startCalibration, s.pickInput = hooks.StartADAPTiQ, hooks.PickInput
}

// UPnPHooks are the UPnP renderer's and the LAN's media servers.
type UPnPHooks struct {
	// Seek moves the renderer's track, for the time bar of a track a controller sent.
	Seek func(context.Context, time.Duration) error
	// Registry browses MediaServer:1 ContentDirectory devices; nil disables the routes (404).
	Registry UPnPRegistry
	// Queue is the folder a played browse result goes into.
	Queue *upnp.Queue
}

// AttachUPnP hands UPnP over. Call before serving starts.
func (s *Server) AttachUPnP(h UPnPHooks) {
	s.attached.UPnP = h
	s.upnpSeek, s.upnpReg, s.upnpQueue = h.Seek, h.Registry, h.Queue
}
