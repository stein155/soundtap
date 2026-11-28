// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	t.Parallel()

	client := func(id string, percent int, muted bool) snapcast.ClientInfo {
		info := snapClient(id, true)
		info.Config.Volume = snapcast.Volume{Percent: percent, Muted: muted}

		return info
	}
	status := func(anchorMuted bool) snapcast.Status {
		return snapcast.Status{Groups: []snapcast.Group{{ID: "g", Clients: []snapcast.ClientInfo{
			client("local", 20, anchorMuted), client("peer", 30, false),
		}}}}
	}
	fake := &volumeFake{Backend: nil, id: "", volume: snapcast.Volume{Percent: 0, Muted: false}}
	env := testEnv()
	env.Snapcast, env.LocalID = fake, "local"
	MuteAnchor(context.Background(), &env, status(false))

	if fake.id != "local" || !fake.volume.Muted || fake.volume.Percent != 20 {
		t.Fatalf("SetVolume(%q, %+v), want local muted at 20", fake.id, fake.volume)
	}

	fake.id = ""

	MuteAnchor(context.Background(), &env, status(true))

	if fake.id != "" {
		t.Fatalf("an already muted anchor was set again: %q", fake.id)
	}
}

// A follower whose snapclient is streaming from the leader must not dial the leader to
// check on it: each dial is an error line in the leader's snapserver log.
func TestStreamingFromReadsTheConnectionTable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	table := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1\n" +
		"   1: 870200C0:B2C4 7E0200C0:06A8 01 00000000:00000000 00:00000000 00000000     0        0 2 1\n" +
		"   2: 870200C0:B2C6 810200C0:06A8 06 00000000:00000000 00:00000000 00000000     0        0 3 1\n"
	// 2001:db8::7 and, from an IPv6 socket, the IPv4 peer 192.0.2.140 as ::ffff:192.0.2.140.
	table6 := "  sl  local_address                         remote_address                        st\n" +
		"   0: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A\n" +
		"   1: B80D0120000000000000000001000000:B2C4 B80D0120000000000000000007000000:06A8 01\n" +
		"   2: 0000000000000000FFFF0000870200C0:B2C6 0000000000000000FFFF00008C0200C0:06A8 01\n"

	for name, body := range map[string]string{"tcp": table, "tcp6": table6} {
		err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	for host, want := range map[string]bool{
		"192.0.2.126":  true,  // established to :1704
		"192.0.2.129":  false, // TIME_WAIT only
		"192.0.2.135":  false, // no connection
		"192.0.2.140":  true,  // established from an IPv6 socket
		"2001:db8::7":  true,  // established over IPv6
		"2001:db8::8":  false, // no connection
		"keuken.local": false, // not an address: dial instead
	} {
		if got := streamingFrom(dir, host); got != want {
			t.Errorf("streamingFrom(%s) = %v, want %v", host, got, want)
		}
	}
}

// The leader's residual goes on the follower's chain, rounded to the ms, and a latency
// never goes below zero.
func TestFollowerLatencyAddsTheLeadersTrim(t *testing.T) {
	t.Parallel()

	for _, step := range []struct {
		downstream int
		trim       time.Duration
		want       int
	}{
		{downstream: 300, trim: 0, want: 300},
		{downstream: 300, trim: 18400 * time.Microsecond, want: 318},
		{downstream: 300, trim: 18600 * time.Microsecond, want: 319},
		{downstream: 300, trim: -25 * time.Millisecond, want: 275},
		{downstream: 10, trim: -40 * time.Millisecond, want: 0},
	} {
		if got := FollowerLatency(step.downstream, step.trim); got != step.want {
			t.Errorf("FollowerLatency(%d, %v) = %d, want %d", step.downstream, step.trim, got, step.want)
		}
	}
}
