// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import "testing"

func TestQueue(t *testing.T) {
	audio := "object.item.audioItem.musicTrack"
	folder := []Item{
		{ID: "d", Class: "object.container", Title: "sub"},
		{ID: "1", Class: audio, URL: "http://n/1"},
		{ID: "v", Class: "object.item.videoItem", URL: "http://n/v"},
		{ID: "2", Class: audio, URL: "http://n/2"},
	}
	var q Queue
	q.Set(folder, "http://n/1")
	if !q.Keep("http://n/1") {
		t.Fatal("queue is not on the tapped track")
	}
	if _, ok := q.Step(-1); ok {
		t.Fatal("stepped before the first track")
	}
	if it, ok := q.Step(1); !ok || it.URL != "http://n/2" {
		t.Fatalf("next = %+v %v: the video must be skipped", it, ok)
	}
	if _, ok := q.Step(1); ok {
		t.Fatal("stepped past the last track")
	}
	if q.Keep("http://other/") {
		t.Fatal("another track must end the queue")
	}
	if q.Keep("http://n/2") {
		t.Fatal("ended queue still holds a track")
	}
	q.Set(folder[:2], "http://n/1")
	if q.Keep("http://n/1") {
		t.Fatal("one track is no queue")
	}
}

func TestParseDIDLKeepsArtistAndCover(t *testing.T) {
	items, err := parseDIDL([]byte(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"
		xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">
		<item id="1"><dc:title>a</dc:title><upnp:artist>Band</upnp:artist><upnp:album>Record</upnp:album><upnp:albumArtURI>http://n/c.jpg</upnp:albumArtURI></item>
		<item id="2"><dc:title>b</dc:title><dc:creator>Solo</dc:creator><upnp:albumArtURI>file:///etc/x</upnp:albumArtURI></item>
	</DIDL-Lite>`))
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if items[0].Artist != "Band" || items[0].Album != "Record" || items[0].Art != "http://n/c.jpg" {
		t.Errorf("first = %+v", items[0])
	}
	if items[1].Artist != "Solo" || items[1].Art != "" {
		t.Errorf("second = %+v: creator stands in, a file URL does not", items[1])
	}
}

func TestQueueRepeatAndShuffle(t *testing.T) {
	track := func(u string) Item { return Item{URL: u, Class: "object.item.audioItem.musicTrack"} }
	folder := []Item{track("a"), track("b"), track("c")}
	var q Queue
	q.Set(folder, "c")
	if _, ok := q.Step(1); ok {
		t.Fatal("repeat off: the last track wrapped")
	}
	if q.SetRepeat("sometimes") {
		t.Fatal("an unknown repeat mode was taken")
	}
	q.SetRepeat(RepeatAll)
	if it, _ := q.Step(1); it.URL != "a" {
		t.Fatalf("repeat all: after c came %q, want a", it.URL)
	}
	if it, _ := q.Step(-1); it.URL != "c" {
		t.Fatalf("repeat all: before a came %q, want c", it.URL)
	}
	q.SetRepeat(RepeatTrack)
	if it, _ := q.Advance(); it.URL != "c" {
		t.Fatalf("repeat track: the end of c played %q", it.URL)
	}
	if it, _ := q.Step(1); it.URL != "a" {
		t.Fatalf("repeat track: next still skips, got %q", it.URL)
	}

	q.SetRepeat(RepeatOff)
	q.SetShuffle(true)
	if !q.Holds("a") {
		t.Fatal("shuffle moved off the playing track")
	}
	seen := map[string]bool{"a": true}
	for range 2 {
		it, ok := q.Step(1)
		if !ok {
			t.Fatal("shuffle ended before every track played")
		}
		seen[it.URL] = true
	}
	if len(seen) != 3 {
		t.Fatalf("shuffle played %v, want all three", seen)
	}
	q.SetShuffle(false)
	if active, shuffle, repeat := q.Modes(); !active || shuffle || repeat != RepeatOff {
		t.Fatalf("modes = %v %v %q", active, shuffle, repeat)
	}
	last, _ := q.Step(0)
	want := map[string]string{"a": "b", "b": "c"}[last.URL]
	if it, ok := q.Step(1); want != "" && (!ok || it.URL != want) {
		t.Fatalf("shuffle off: after %q came %q, want the folder's order (%q)", last.URL, it.URL, want)
	}
}
