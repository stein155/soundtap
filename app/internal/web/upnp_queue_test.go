// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stein155/soundtap/app/internal/upnp"
	"github.com/stein155/soundtap/app/internal/web"
)

// Playing a track with its folder queues the folder; without a folder, or when the folder
// cannot be read, the one track still plays and nothing follows.
func TestUPnPPlayQueuesTheFolder(t *testing.T) {
	t.Parallel()

	audio := "object.item.audioItem.musicTrack"
	reg := newFakeRegistry()
	reg.items = []upnp.Item{
		mediaItem("1", "", audio, "http://192.168.1.9/1.mp3"),
		mediaItem("2", "", audio, "http://192.168.1.9/2.mp3"),
	}
	srv, _, _ := newServerSrv(t)
	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Registry = reg })

	queue := &upnp.Queue{}

	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Queue = queue })
	handler := srv.Handler()

	play := func(server, folder string) {
		t.Helper()

		body, err := json.Marshal(map[string]string{
			"url": "http://192.168.1.9/1.mp3", "name": "1", "server": server, "folder": folder,
		})
		if err != nil {
			t.Fatal(err)
		}

		if rec := do(t, handler, "POST", "/api/upnp/play", string(body)); rec.Code != http.StatusOK {
			t.Fatalf("play = %d %s", rec.Code, rec.Body)
		}
	}
	play("uuid:s", "7")

	if !queue.Keep("http://192.168.1.9/1.mp3") {
		t.Fatal("folder not queued on the tapped track")
	}

	play("", "")

	if queue.Keep("http://192.168.1.9/1.mp3") {
		t.Fatal("a play without a folder must end the queue")
	}

	reg.browseErr = errFolderGone

	play("uuid:s", "7")

	if queue.Keep("http://192.168.1.9/1.mp3") {
		t.Fatal("an unreadable folder must leave no queue")
	}
}

// errFolderGone is a folder the media server no longer has.
var errFolderGone = errors.New("gone")

// gatedRegistry holds the browse of one folder until released, to overlap two plays.
type gatedRegistry struct {
	fakeRegistry

	slow    string
	entered chan struct{}
	release chan struct{}
	items   map[string][]upnp.Item
}

func (g *gatedRegistry) Browse(_ context.Context, _, folder string) ([]upnp.Item, error) {
	if folder == g.slow {
		close(g.entered)
		<-g.release
	}

	return g.items[folder], nil
}

// A folder lookup that finishes after a newer play must not replace that play's queue.
func TestUPnPLateBrowseKeepsNewerQueue(t *testing.T) {
	t.Parallel()

	audio := "object.item.audioItem.musicTrack"
	track := func(n string) upnp.Item { return mediaItem(n, "", audio, "http://192.168.1.9/"+n) }
	reg := &gatedRegistry{
		fakeRegistry: *newFakeRegistry(),
		slow:         "old",
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
		items: map[string][]upnp.Item{
			"old": {track("o1"), track("o2")},
			"new": {track("n1"), track("n2")},
		},
	}
	srv, _, _ := newServerSrv(t)
	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Registry = reg })

	queue := &upnp.Queue{}

	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Queue = queue })
	handler := srv.Handler()
	play := func(url, folder string) {
		t.Helper()

		body, err := json.Marshal(map[string]string{"url": url, "name": "x", "server": "uuid:s", "folder": folder})
		if err != nil {
			t.Fatal(err)
		}

		if rec := do(t, handler, "POST", "/api/upnp/play", string(body)); rec.Code != http.StatusOK {
			t.Fatalf("play = %d %s", rec.Code, rec.Body)
		}
	}

	done := make(chan struct{})
	go func() { defer close(done); play("http://192.168.1.9/o1", "old") }()

	<-reg.entered
	play("http://192.168.1.9/n1", "new")
	close(reg.release)
	<-done

	if !queue.Holds("http://192.168.1.9/n1") {
		t.Fatal("the late older browse replaced the newer queue")
	}
}
