// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp_test

import (
	"testing"

	"github.com/stein155/soundtap/app/internal/upnp"
)

// item is a folder entry with no artist, album or cover.
func item(id, class, title, url string) upnp.Item {
	return upnp.Item{ID: id, Title: title, Class: class, Artist: "", Album: "", Art: "", URL: url}
}

func TestQueue(t *testing.T) {
	t.Parallel()

	audio := "object.item.audioItem.musicTrack"
	folder := []upnp.Item{
		item("d", "object.container", "sub", ""),
		item("1", audio, "", "http://n/1"),
		item("v", "object.item.videoItem", "", "http://n/v"),
		item("2", audio, "", "http://n/2"),
	}

	var queue upnp.Queue
	queue.Set(folder, "http://n/1")

	if !queue.Keep("http://n/1") {
		t.Fatal("queue is not on the tapped track")
	}

	if _, ok := queue.Step(-1); ok {
		t.Fatal("stepped before the first track")
	}

	if it, ok := queue.Step(1); !ok || it.URL != "http://n/2" {
		t.Fatalf("next = %+v %v: the video must be skipped", it, ok)
	}

	if _, ok := queue.Step(1); ok {
		t.Fatal("stepped past the last track")
	}

	if queue.Keep("http://other/") {
		t.Fatal("another track must end the queue")
	}

	if queue.Keep("http://n/2") {
		t.Fatal("ended queue still holds a track")
	}

	queue.Set(folder[:2], "http://n/1")

	if queue.Keep("http://n/1") {
		t.Fatal("one track is no queue")
	}
}

func TestQueueRepeatAndShuffle(t *testing.T) {
	t.Parallel()

	track := func(url string) upnp.Item { return item("", "object.item.audioItem.musicTrack", "", url) }
	folder := []upnp.Item{track("a"), track("b"), track("c")}

	var queue upnp.Queue
	queue.Set(folder, "c")

	if _, ok := queue.Step(1); ok {
		t.Fatal("repeat off: the last track wrapped")
	}

	if queue.SetRepeat("sometimes") {
		t.Fatal("an unknown repeat mode was taken")
	}

	queue.SetRepeat(upnp.RepeatAll)

	if it, _ := queue.Step(1); it.URL != "a" {
		t.Fatalf("repeat all: after c came %q, want a", it.URL)
	}

	if it, _ := queue.Step(-1); it.URL != "c" {
		t.Fatalf("repeat all: before a came %q, want c", it.URL)
	}

	queue.SetRepeat(upnp.RepeatTrack)

	if it, _ := queue.Advance(); it.URL != "c" {
		t.Fatalf("repeat track: the end of c played %q", it.URL)
	}

	if it, _ := queue.Step(1); it.URL != "a" {
		t.Fatalf("repeat track: next still skips, got %q", it.URL)
	}

	queue.SetRepeat(upnp.RepeatOff)
	queue.SetShuffle(true)

	if !queue.Holds("a") {
		t.Fatal("shuffle moved off the playing track")
	}

	seen := map[string]bool{"a": true}

	for range 2 {
		it, ok := queue.Step(1)
		if !ok {
			t.Fatal("shuffle ended before every track played")
		}

		seen[it.URL] = true
	}

	if len(seen) != 3 {
		t.Fatalf("shuffle played %v, want all three", seen)
	}

	queue.SetShuffle(false)

	if active, shuffle, repeat := queue.Modes(); !active || shuffle || repeat != upnp.RepeatOff {
		t.Fatalf("modes = %v %v %q", active, shuffle, repeat)
	}

	last, _ := queue.Step(0)

	want := map[string]string{"a": "b", "b": "c"}[last.URL]
	if it, ok := queue.Step(1); want != "" && (!ok || it.URL != want) {
		t.Fatalf("shuffle off: after %q came %q, want the folder's order (%q)", last.URL, it.URL, want)
	}
}
