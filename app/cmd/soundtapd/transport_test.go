// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"errors"
	"testing"
)

//nolint:paralleltest // changes package-wide state other tests read: confirmPending
func TestTransportHookMapsActionsOnBluetooth(t *testing.T) {
	var got []string

	skippers := map[string]func(string) error{"BLUETOOTH": func(arg string) error {
		got = append(got, arg)

		return nil
	}}
	hook := transportHook(func() string { return "BLUETOOTH" }, skippers, func() {}, quietLog())

	err := hook("NEXT_TRACK")
	if err != nil {
		t.Fatalf("NEXT_TRACK: %v", err)
	}

	err = hook("PREV_TRACK")
	if err != nil {
		t.Fatalf("PREV_TRACK: %v", err)
	}

	if len(got) != 2 || got[0] != "next" || got[1] != "prev" {
		t.Fatalf("ran %v, want [next prev]", got)
	}
}

//nolint:paralleltest // changes package-wide state other tests read: confirmPending
func TestTransportHookIgnoresSourcesThatCannotSkip(t *testing.T) {
	for _, src := range []string{"TUNEIN", "AIRPLAY", "AUX", "STANDBY"} {
		ran := false

		skippers := map[string]func(string) error{"BLUETOOTH": func(string) error {
			ran = true

			return nil
		}}
		hook := transportHook(func() string { return src }, skippers, func() {}, quietLog())

		err := hook("NEXT_TRACK")
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}

		if ran {
			t.Fatalf("%s: skip reached the Bluetooth shell", src)
		}
	}
}

// errTransportShellGone is the AVRCP shell's failure TestTransportHookReportsShellFailure feeds.
var errTransportShellGone = errors.New("bluetoothctl gone")

//nolint:paralleltest // changes package-wide state other tests read: confirmPending
func TestTransportHookReportsShellFailure(t *testing.T) {
	skippers := map[string]func(string) error{"BLUETOOTH": func(string) error { return errTransportShellGone }}
	hook := transportHook(func() string { return "BLUETOOTH" }, skippers, func() {}, quietLog())

	err := hook("NEXT_TRACK")
	if !errors.Is(err, errTransportShellGone) {
		t.Fatalf("err = %v, want %v", err, errTransportShellGone)
	}
}

//nolint:paralleltest // changes package-wide state other tests read: confirmPending
func TestTransportHookSkipsSpotify(t *testing.T) {
	var got []string

	hook := transportHook(func() string { return "SPOTIFY" }, map[string]func(string) error{
		"BLUETOOTH": func(string) error {
			t.Fatal("Spotify skip reached the Bluetooth shell")

			return nil
		},
		"SPOTIFY": func(arg string) error {
			got = append(got, arg)

			return nil
		},
	}, func() {}, quietLog())

	err := hook("PREV_TRACK")
	if err != nil || len(got) != 1 || got[0] != "prev" {
		t.Fatalf("got %v, err %v; want [prev]", got, err)
	}
}

// A skip the source cannot do says so; a play or pause it cannot do is not a skip.
//
//nolint:paralleltest // changes package-wide state other tests read: confirmPending
func TestTransportHookSaysASkipIsUnavailable(t *testing.T) {
	said := 0

	hook := transportHook(func() string { return "TUNEIN" }, nil, func() { said++ }, quietLog())
	for _, action := range []string{"PAUSE", "PLAY", "NEXT_TRACK", "PREV_TRACK"} {
		err := hook(action)
		if err != nil {
			t.Fatal(err)
		}
	}

	if said != 2 {
		t.Errorf("said so %d times, want 2", said)
	}
}
