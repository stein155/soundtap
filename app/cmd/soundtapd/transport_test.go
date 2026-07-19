// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"errors"
	"testing"
)

func TestTransportHookMapsActionsOnBluetooth(t *testing.T) {
	var got []string
	h := transportHook(func() string { return "BLUETOOTH" }, map[string]func(string) error{"BLUETOOTH": func(a string) error {
		got = append(got, a)
		return nil
	}}, func() {}, quietLog())

	if err := h("NEXT_TRACK"); err != nil {
		t.Fatalf("NEXT_TRACK: %v", err)
	}
	if err := h("PREV_TRACK"); err != nil {
		t.Fatalf("PREV_TRACK: %v", err)
	}
	if len(got) != 2 || got[0] != "next" || got[1] != "prev" {
		t.Fatalf("ran %v, want [next prev]", got)
	}
}

func TestTransportHookIgnoresSourcesThatCannotSkip(t *testing.T) {
	for _, src := range []string{"TUNEIN", "AIRPLAY", "AUX", "STANDBY"} {
		ran := false
		h := transportHook(func() string { return src }, map[string]func(string) error{"BLUETOOTH": func(string) error { ran = true; return nil }}, func() {}, quietLog())
		if err := h("NEXT_TRACK"); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if ran {
			t.Fatalf("%s: skip reached the Bluetooth shell", src)
		}
	}
}

func TestTransportHookReportsShellFailure(t *testing.T) {
	want := errors.New("bluetoothctl gone")
	h := transportHook(func() string { return "BLUETOOTH" }, map[string]func(string) error{"BLUETOOTH": func(string) error { return want }}, func() {}, quietLog())
	if err := h("NEXT_TRACK"); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestTransportHookSkipsSpotify(t *testing.T) {
	var got []string
	h := transportHook(func() string { return "SPOTIFY" }, map[string]func(string) error{
		"BLUETOOTH": func(string) error { t.Fatal("Spotify skip reached the Bluetooth shell"); return nil },
		"SPOTIFY":   func(a string) error { got = append(got, a); return nil },
	}, func() {}, quietLog())
	if err := h("PREV_TRACK"); err != nil || len(got) != 1 || got[0] != "prev" {
		t.Fatalf("got %v, err %v; want [prev]", got, err)
	}
}

// A skip the source cannot do says so; a play or pause it cannot do is not a skip.
func TestTransportHookSaysASkipIsUnavailable(t *testing.T) {
	said := 0
	h := transportHook(func() string { return "TUNEIN" }, nil, func() { said++ }, quietLog())
	for _, action := range []string{"PAUSE", "PLAY", "NEXT_TRACK", "PREV_TRACK"} {
		if err := h(action); err != nil {
			t.Fatal(err)
		}
	}
	if said != 2 {
		t.Errorf("said so %d times, want 2", said)
	}
}
