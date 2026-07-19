// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"encoding/binary"
	"testing"
	"time"
)

type fakeClips map[string][]byte

func (f fakeClips) SetBuiltinClip(n string, d []byte, _ string) { f[n] = d }

func TestEventClipsFormat(t *testing.T) {
	c := fakeClips{}
	registerEventClips(c)
	want := map[string]int{chimeBTConnected: 300, chimeBTCleared: 300, chimeGrouped: 250}
	for name, ms := range want {
		d := c[name]
		if len(d) != 44+48*ms*4 {
			t.Fatalf("%s: %d bytes, want %d", name, len(d), 44+48*ms*4)
		}
		if string(d[:4]) != "RIFF" || string(d[8:12]) != "WAVE" || binary.LittleEndian.Uint32(d[24:28]) != 48000 || binary.LittleEndian.Uint16(d[22:24]) != 2 {
			t.Fatalf("%s: bad header", name)
		}
	}
	if string(c[chimeBTConnected]) == string(c[chimeBTCleared]) {
		t.Fatal("connect and cleared tones must differ")
	}
}

func TestEventChimesGate(t *testing.T) {
	var played []string
	standby := false
	now := time.Unix(1000, 0)
	f := eventChimes(func(n string) { played = append(played, n) }, func() bool { return standby }, func() time.Time { return now })
	f(chimeBTConnected)
	f(chimeBTConnected) // reconnect inside the window
	f(chimeGrouped)     // another event is not debounced by it
	if len(played) != 2 {
		t.Fatalf("played %v", played)
	}
	now = now.Add(chimeDebounce + time.Second)
	f(chimeBTConnected)
	standby = true
	now = now.Add(time.Minute)
	f(chimeBTCleared)
	if len(played) != 3 {
		t.Fatalf("played %v", played)
	}
}

func TestChimeVolume(t *testing.T) {
	for _, c := range []struct{ listening, ceiling, want int }{
		{14, 41, 14}, // quiet listening: the chime is no louder than the music
		{60, 41, 41}, // loud listening: never above stock's chimeVolume
		{0, 41, 0},   // muted: no chime
	} {
		if got := chimeVolume(c.listening, c.ceiling); got != c.want {
			t.Errorf("chimeVolume(%d, %d) = %d, want %d", c.listening, c.ceiling, got, c.want)
		}
	}
}
