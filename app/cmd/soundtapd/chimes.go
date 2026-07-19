// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/binary"
	"math"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/web"
)

// Event chimes: short tones of our own for a Bluetooth device connecting,
// Bluetooth pairings being cleared and this speaker joining a group as a member.
const (
	chimeBTConnected = "bt-connected.wav"
	chimeBTCleared   = "bt-cleared.wav"
	chimeGrouped     = "grouped.wav"

	// chimeDebounce keeps a phone that drops and reconnects within seconds from chiming twice.
	chimeDebounce = 10 * time.Second
)

// tone is one sine note; Hz 0 is silence.
type tone struct{ Hz, Ms int }

// clipSetter is what registerEventClips needs from the web server.
type clipSetter interface {
	SetBuiltinClip(name string, data []byte, contentType string)
}

// registerEventClips generates the three tones and serves them as builtin clips.
func registerEventClips(w clipSetter) {
	for name, notes := range map[string][]tone{
		chimeBTConnected: {{660, 120}, {880, 180}},        // rising pair
		chimeBTCleared:   {{880, 120}, {587, 180}},        // falling pair
		chimeGrouped:     {{740, 90}, {0, 70}, {740, 90}}, // soft double beep
	} {
		w.SetBuiltinClip(name, genTonesWAV(notes...), "audio/wav")
	}
}

// chimeVolume is the level a chime plays at: the listening volume, never above the
// model's chimeVolume. Stock always played at chimeVolume, which on an ST10 listened to
// at 14 made every chime three times louder than the music (2026-11-06). Muted
// gives 0: no chime.
func chimeVolume(listening, ceiling int) int {
	return max(min(listening, ceiling), 0)
}

// eventChimes returns the callback the web server fires per event. It stays silent in
// standby and for repeats of one event within chimeDebounce.
func eventChimes(play func(name string), standby func() bool, now func() time.Time) func(string) {
	var mu sync.Mutex
	last := map[string]time.Time{}
	return func(name string) {
		if standby() {
			return
		}
		mu.Lock()
		t := now()
		if p, ok := last[name]; ok && t.Sub(p) < chimeDebounce {
			mu.Unlock()
			return
		}
		last[name] = t
		mu.Unlock()
		play(name)
	}
}

// genTonesWAV renders notes back to back as stereo S16_LE 48 kHz PCM WAV, each with a
// 10% fade in/out so it does not click, at about -10 dB full scale.
func genTonesWAV(notes ...tone) []byte {
	const sampleRate = 48000
	const vol = 0.30
	var data []byte
	for _, nt := range notes {
		n := sampleRate * nt.Ms / 1000
		fadeFrames := n / 10
		seg := make([]byte, n*4)
		for i := 0; i < n && nt.Hz > 0; i++ {
			fade := 1.0
			if i < fadeFrames {
				fade = float64(i) / float64(fadeFrames)
			} else if i >= n-fadeFrames {
				fade = float64(n-i) / float64(fadeFrames)
			}
			v := int16(math.Sin(2*math.Pi*float64(nt.Hz)*float64(i)/float64(sampleRate)) * fade * vol * 32767)
			binary.LittleEndian.PutUint16(seg[i*4:], uint16(v))   //nolint:gosec // two's-complement sample
			binary.LittleEndian.PutUint16(seg[i*4+2:], uint16(v)) //nolint:gosec // two's-complement sample
		}
		data = append(data, seg...)
	}
	le := binary.LittleEndian
	hdr := make([]byte, 0, 44+len(data))
	hdr = append(hdr, "RIFF"...)
	hdr = le.AppendUint32(hdr, uint32(36+len(data))) //nolint:gosec // a few KiB
	hdr = append(hdr, "WAVEfmt "...)
	hdr = le.AppendUint32(hdr, 16)
	hdr = le.AppendUint16(hdr, 1) // PCM
	hdr = le.AppendUint16(hdr, 2) // stereo
	hdr = le.AppendUint32(hdr, sampleRate)
	hdr = le.AppendUint32(hdr, sampleRate*4)
	hdr = le.AppendUint16(hdr, 4)
	hdr = le.AppendUint16(hdr, 16)
	hdr = append(hdr, "data"...)
	hdr = le.AppendUint32(hdr, uint32(len(data))) //nolint:gosec // a few KiB
	return append(hdr, data...)
}

// wireChimes registers the built-in clips and event chimes, and returns the
// preset-stored chime for the buttons.
func wireChimes(ctx context.Context, webSrv *web.Server, sp *speakerstate.Speaker, port string) (chime func(), event func(name string)) {
	// Preset-store confirmation tone: generate a short beep, register it as a
	// permanent clip, and wire the chime callback. Works when AUDIO_MIX=1 (the
	// arbiter is up); off-hardware (QEMU, AUDIO_MIX unset) the notification API
	// still accepts the call but nothing will play it — which is fine.
	presetToneURL := "http://127.0.0.1:" + port + "/api/speaker/audio/preset-stored.wav"
	webSrv.SetBuiltinClip("preset-stored.wav", genBeepWAV(), "audio/wav")
	// The alarm's fallback when its preset does not play (no internet, empty slot).
	webSrv.SetBuiltinClip(web.AlarmClip, genBeepWAV(), "audio/wav")
	registerEventClips(webSrv)
	event = eventChimes(func(name string) {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if v := chimeVolume(sp.StatusSnapshot().Output(), board.ChimeVolume); v > 0 {
			_ = sp.PlayNotification(cctx, speaker.Notification{URL: "http://127.0.0.1:" + port + "/api/speaker/audio/" + name, Volume: v})
		}
	}, sp.IsStandby, time.Now)
	chime = func() {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if v := chimeVolume(sp.StatusSnapshot().Output(), board.ChimeVolume); v > 0 {
			_ = sp.PlayNotification(cctx, speaker.Notification{URL: presetToneURL, Volume: v})
		}
	}
	return chime, event
}

// genBeepWAV synthesises a 300 ms 880 Hz beep, the preset-store confirmation tone.
func genBeepWAV() []byte { return genTonesWAV(tone{880, 300}) }
