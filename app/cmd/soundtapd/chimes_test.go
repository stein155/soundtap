// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

type fakeClips map[string][]byte

func (f fakeClips) SetBuiltinClip(n string, d []byte, _ string) { f[n] = d }

func TestEventClipsFormat(t *testing.T) {
	t.Parallel()

	clips := fakeClips{}
	registerEventClips(clips)

	want := map[string]int{chimeBTConnected: 300, chimeBTCleared: 300, chimeGrouped: 250}
	for name, ms := range want {
		clip := clips[name]
		if len(clip) != 44+48*ms*4 {
			t.Fatalf("%s: %d bytes, want %d", name, len(clip), 44+48*ms*4)
		}

		if string(clip[:4]) != "RIFF" || string(clip[8:12]) != "WAVE" ||
			binary.LittleEndian.Uint32(clip[24:28]) != 48000 || binary.LittleEndian.Uint16(clip[22:24]) != 2 {
			t.Fatalf("%s: bad header", name)
		}
	}

	if bytes.Equal(clips[chimeBTConnected], clips[chimeBTCleared]) {
		t.Fatal("connect and cleared tones must differ")
	}
}

func TestEventChimesGate(t *testing.T) {
	t.Parallel()

	var played []string

	standby := false
	now := time.Unix(1000, 0)
	chimeEvent := eventChimes(
		func(n string) { played = append(played, n) },
		func() bool { return standby },
		func() time.Time { return now },
	)
	chimeEvent(chimeBTConnected)
	chimeEvent(chimeBTConnected) // reconnect inside the window
	chimeEvent(chimeGrouped)     // another event is not debounced by it

	if len(played) != 2 {
		t.Fatalf("played %v", played)
	}

	now = now.Add(chimeDebounce + time.Second)

	chimeEvent(chimeBTConnected)

	standby = true
	now = now.Add(time.Minute)

	chimeEvent(chimeBTCleared)

	if len(played) != 3 {
		t.Fatalf("played %v", played)
	}
}

func TestChimeVolume(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct{ listening, ceiling, want int }{
		{14, 41, 14}, // quiet listening: the chime is no louder than the music
		{60, 41, 41}, // loud listening: never above stock's chimeVolume
		{0, 41, 0},   // muted: no chime
	} {
		if got := chimeVolume(testCase.listening, testCase.ceiling); got != testCase.want {
			t.Errorf("chimeVolume(%d, %d) = %d, want %d", testCase.listening, testCase.ceiling, got, testCase.want)
		}
	}
}
