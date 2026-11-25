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
func registerEventClips(clips clipSetter) {
	for name, notes := range map[string][]tone{
		chimeBTConnected: {{660, 120}, {880, 180}},        // rising pair
		chimeBTCleared:   {{880, 120}, {587, 180}},        // falling pair
		chimeGrouped:     {{740, 90}, {0, 70}, {740, 90}}, // soft double beep
	} {
		clips.SetBuiltinClip(name, genTonesWAV(notes...), "audio/wav")
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

		playedAt := now()
		if p, ok := last[name]; ok && playedAt.Sub(p) < chimeDebounce {
			mu.Unlock()

			return
		}

		last[name] = playedAt
		mu.Unlock()
		play(name)
	}
}

// The WAV container genTonesWAV writes: a 44-byte RIFF header, a 16-byte PCM fmt chunk,
// stereo 16-bit frames.
const (
	wavHeaderBytes    = 44 // RIFF, fmt and data chunk headers
	wavRIFFHeaderRest = 36 // the RIFF chunk's size counts the header after its own 8 bytes
	wavFmtChunkBytes  = 16
	wavFormatPCM      = 1
	wavChannels       = 2
	wavBitsPerSample  = 16
	wavFrameBytes     = wavChannels * wavBitsPerSample / 8
)

const (
	// chimeFadeDivisor makes each note's fade in and out a tenth of it.
	chimeFadeDivisor = 10
	// chimePlayTimeout bounds one chime's notification request.
	chimePlayTimeout = 5 * time.Second
)

// genTonesWAV renders notes back to back as stereo S16_LE 48 kHz PCM WAV, each with a
// 10% fade in/out so it does not click, at about -10 dB full scale.
func genTonesWAV(notes ...tone) []byte {
	const (
		sampleRate = 48000
		vol        = 0.30
	)

	var data []byte

	for _, note := range notes {
		frames := sampleRate * note.Ms / int(time.Second/time.Millisecond)
		fadeFrames := frames / chimeFadeDivisor

		seg := make([]byte, frames*wavFrameBytes)
		for step := 0; step < frames && note.Hz > 0; step++ {
			fade := 1.0
			if step < fadeFrames {
				fade = float64(step) / float64(fadeFrames)
			} else if step >= frames-fadeFrames {
				fade = float64(frames-step) / float64(fadeFrames)
			}

			phase := 2 * math.Pi * float64(note.Hz) * float64(step) / float64(sampleRate)
			v := int16(math.Sin(phase) * fade * vol * math.MaxInt16)
			frame := seg[step*wavFrameBytes:]
			binary.LittleEndian.PutUint16(frame, uint16(v))                   //nolint:gosec // two's-complement sample
			binary.LittleEndian.PutUint16(frame[wavFrameBytes/2:], uint16(v)) //nolint:gosec // two's-complement sample
		}

		data = append(data, seg...)
	}

	littleEndian := binary.LittleEndian
	hdr := make([]byte, 0, wavHeaderBytes+len(data))
	hdr = append(hdr, "RIFF"...)
	hdr = littleEndian.AppendUint32(hdr, uint32(wavRIFFHeaderRest+len(data))) //nolint:gosec // a few KiB
	hdr = append(hdr, "WAVEfmt "...)
	hdr = littleEndian.AppendUint32(hdr, wavFmtChunkBytes)
	hdr = littleEndian.AppendUint16(hdr, wavFormatPCM)
	hdr = littleEndian.AppendUint16(hdr, wavChannels)
	hdr = littleEndian.AppendUint32(hdr, sampleRate)
	hdr = littleEndian.AppendUint32(hdr, sampleRate*wavFrameBytes)
	hdr = littleEndian.AppendUint16(hdr, wavFrameBytes)
	hdr = littleEndian.AppendUint16(hdr, wavBitsPerSample)
	hdr = append(hdr, "data"...)
	hdr = littleEndian.AppendUint32(hdr, uint32(len(data))) //nolint:gosec // a few KiB

	return append(hdr, data...)
}

// wireChimes registers the built-in clips and event chimes, and returns the
// preset-stored chime for the buttons and the event chime for the web server.
func wireChimes(
	ctx context.Context, webSrv *web.Server, speakerState *speakerstate.Speaker, port string,
) (func(), func(string)) {
	// Preset-store confirmation tone: generate a short beep, register it as a
	// permanent clip, and wire the chime callback. Works when AUDIO_MIX=1 (the
	// arbiter is up); off-hardware (QEMU, AUDIO_MIX unset) the notification API
	// still accepts the call but nothing will play it — which is fine.
	presetToneURL := "http://127.0.0.1:" + port + "/api/speaker/audio/preset-stored.wav"

	webSrv.SetBuiltinClip("preset-stored.wav", genBeepWAV(), "audio/wav")
	// The alarm's fallback when its preset does not play (no internet, empty slot).
	webSrv.SetBuiltinClip(web.AlarmClip, genBeepWAV(), "audio/wav")
	registerEventClips(webSrv)

	event := eventChimes(func(name string) {
		cctx, cancel := context.WithTimeout(ctx, chimePlayTimeout)
		defer cancel()

		if v := chimeVolume(speakerState.StatusSnapshot().Output(), board.ChimeVolume); v > 0 {
			url := "http://127.0.0.1:" + port + "/api/speaker/audio/" + name
			_ = speakerState.PlayNotification(cctx, chimeNotification(url, v))
		}
	}, speakerState.IsStandby, time.Now)
	chime := func() {
		cctx, cancel := context.WithTimeout(ctx, chimePlayTimeout)
		defer cancel()

		if v := chimeVolume(speakerState.StatusSnapshot().Output(), board.ChimeVolume); v > 0 {
			_ = speakerState.PlayNotification(cctx, chimeNotification(presetToneURL, v))
		}
	}

	return chime, event
}

// genBeepWAV synthesises a 300 ms 880 Hz beep, the preset-store confirmation tone.
func genBeepWAV() []byte { return genTonesWAV(tone{880, 300}) }

// chimeNotification is a chime clip at url played at volume: no track to show.
func chimeNotification(url string, volume int) speaker.Notification {
	return speaker.Notification{URL: url, Volume: volume, Artist: "", Album: "", Track: ""}
}
