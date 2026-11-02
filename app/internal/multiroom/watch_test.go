// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/snapcast"
)

// volumeFake is a snapserver that records the last SetVolume.
type volumeFake struct {
	Backend
	id     string
	volume snapcast.Volume
}

func (f *volumeFake) SetVolume(_ context.Context, id string, volume snapcast.Volume) error {
	f.id, f.volume = id, volume
	return nil
}

// The anchor -- this speaker's own client -- is muted so snapserver stops streaming the
// mix to it, and the other clients are left alone.
func TestMuteAnchorMutesOnlyTheLocalClient(t *testing.T) {
	client := func(id string, percent int, muted bool) snapcast.ClientInfo {
		var c snapcast.ClientInfo
		c.ID, c.Connected = id, true
		c.Config.Volume = snapcast.Volume{Percent: percent, Muted: muted}
		return c
	}
	status := func(anchorMuted bool) snapcast.Status {
		return snapcast.Status{Groups: []snapcast.Group{{ID: "g", Clients: []snapcast.ClientInfo{
			client("local", 20, anchorMuted), client("peer", 30, false),
		}}}}
	}
	fake := &volumeFake{}
	env := Env{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Snapcast: fake, LocalID: "local"}
	MuteAnchor(context.Background(), env, status(false))
	if fake.id != "local" || !fake.volume.Muted || fake.volume.Percent != 20 {
		t.Fatalf("SetVolume(%q, %+v), want local muted at 20", fake.id, fake.volume)
	}
	fake.id = ""
	MuteAnchor(context.Background(), env, status(true))
	if fake.id != "" {
		t.Fatalf("an already muted anchor was set again: %q", fake.id)
	}
}

// A follower whose snapclient is streaming from the leader must not dial the leader to
// check on it: each dial is an error line in the leader's snapserver log.
func TestStreamingFromReadsTheConnectionTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tcp")
	table := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1\n" +
		"   1: 870200C0:B2C4 7E0200C0:06A8 01 00000000:00000000 00:00000000 00000000     0        0 2 1\n" +
		"   2: 870200C0:B2C6 810200C0:06A8 06 00000000:00000000 00:00000000 00000000     0        0 3 1\n"
	if err := os.WriteFile(path, []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{
		"192.0.2.126":  true,  // established to :1704
		"192.0.2.129":  false, // TIME_WAIT only
		"192.0.2.135":  false, // no connection
		"keuken.local": false, // not an address: dial instead
	} {
		if got := streamingFrom(path, host); got != want {
			t.Errorf("streamingFrom(%s) = %v, want %v", host, got, want)
		}
	}
}
