// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/supervise"
)

func TestLibrespotNameIsSingleArgument(t *testing.T) {
	t.Parallel()

	name := `Kitchen "Main"; reboot`

	args := librespotArgs(name, 10)
	for i, arg := range args {
		if arg == "--name" && i+1 < len(args) && args[i+1] == name {
			return
		}
	}

	t.Fatalf("name not preserved as one argv value: %q", args)
}

func TestShairportNameIsSingleArgument(t *testing.T) {
	t.Parallel()

	name := `Kitchen "Main"; reboot`

	args := shairportArgs(shairportRunConf, name)
	if !slices.Equal(args, []string{"-c", shairportRunConf, "-a", name}) {
		t.Fatalf("shairport args = %q", args)
	}
}

// An ST20 must never start shairport: its SCM is behind the BCO NAT. The init script's model
// detection is narrower than internal/hw/model's, so an empty/scm-ap2 mode on ST20 hardware is a
// reachable state, not a hypothetical one.
func TestAirplayEnabled(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		mode, model string
		want        bool
	}{
		{"scm-ap2", "st10", true},
		{"", "st10", true},
		{"bco-ap1", "st10", false},
		{"bco-ap1", "st20", false},
		{"scm-ap2", "st20", true},
		{"", "st20", false},
	} {
		if got := airplayEnabled(testCase.mode, testCase.model); got != testCase.want {
			t.Errorf("airplayEnabled(%q, %q) = %v, want %v", testCase.mode, testCase.model, got, testCase.want)
		}
	}
}

func TestLibrespotKeepsDefaultALSABuffering(t *testing.T) {
	t.Parallel()

	args := librespotArgs("Kitchen", 10)
	if !slices.Contains(args, "--disable-audio-cache") {
		t.Fatalf("librespot writes streamed audio to persistent storage: %q", args)
	}

	want := []string{"--device", "default"}
	for i := 0; i+1 < len(args); i++ {
		if slices.Equal(args[i:i+2], want) {
			return
		}
	}

	t.Fatalf("librespot args lost default ALSA device: %q", args)
}

func TestLibrespotStartsAtSpeakerVolume(t *testing.T) {
	t.Parallel()

	args := librespotArgs("Kitchen", 150)
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--initial-volume" && args[i+1] == "100" {
			return
		}
	}

	t.Fatalf("librespot args lack clamped initial volume: %q", args)
}

func TestWaitBusQuietWaitsForTheWriterToStop(t *testing.T) {
	t.Parallel()

	// A fake clock: sleeps overshot under load, so the writer looked stopped early and a
	// busy bus held past its deadline (2026-11-28, two busy loops per core, -cpu 1).
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Uint64

		stop := time.Now().Add(80 * time.Millisecond)
		go func() {
			for time.Now().Before(stop) {
				calls.Add(1)
				time.Sleep(2 * time.Millisecond)
			}
		}()

		health := func() audio.Health { return audioHealthOf(calls.Load(), 0) }
		start := time.Now()

		waitBusQuiet(health, 40*time.Millisecond, time.Second)

		if took := time.Since(start); took < 80*time.Millisecond || took > 500*time.Millisecond {
			t.Fatalf("returned after %v; writer stopped at 80ms", took)
		}

		start = time.Now()

		busy := func() audio.Health { return audioHealthOf(uint64(time.Now().UnixNano()), 0) }
		waitBusQuiet(busy, 40*time.Millisecond, 60*time.Millisecond)

		if took := time.Since(start); took > 300*time.Millisecond {
			t.Fatalf("busy bus held the yield for %v", took)
		}
	})
}

// The AirPlay device ID is the speaker's device ID, not whatever MAC shairport-sync finds
// first: on the ST20 that was bcolink0's random one, new every boot.
func TestShairportConfPinsTheDeviceID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	base, out := filepath.Join(dir, "base.conf"), filepath.Join(dir, "run.conf")

	err := os.WriteFile(base, []byte("general = {\n\tname = \"SoundTap\";\n};\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.DiscardHandler)
	if got := shairportConf(base, out, "020000E5F640", log); got != out {
		t.Fatalf("conf = %q, want %q", got, out)
	}

	b, _ := os.ReadFile(out)
	want := "general = {\n\tairplay_device_id = 0x020000E5F640L;\n\tname = \"SoundTap\";"

	if !strings.HasPrefix(string(b), want) {
		t.Fatalf("pinned conf:\n%s", b)
	}

	for _, bad := range []string{"", "0C8AB72138", "not-a-mac!!!"} {
		if got := shairportConf(base, out, bad, log); got != base {
			t.Errorf("device id %q: conf = %q, want the base", bad, got)
		}
	}
}

// nqptp is soundtapd's to run, and only where AirPlay 2 can run (ADR 0121).
func TestRunMediaDaemonsStartsNqptpOnlyForAirPlay2(t *testing.T) {
	dir := t.TempDir()

	started := filepath.Join(dir, "started")

	err := os.WriteFile(filepath.Join(dir, "nqptp"), []byte("#!/bin/sh\n: >"+started+"\nexec sleep 30\n"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("SOUNDTAP_SNAPSERVER_CONF", "")

	log := slog.New(slog.DiscardHandler)
	// Tests run as root in CI's container, which has no ptp user.
	user := procUser["nqptp"]
	delete(procUser, "nqptp")
	t.Cleanup(func() { procUser["nqptp"] = user })

	t.Setenv("SOUNDTAP_AIRPLAY_MODE", "bco-ap1")
	runMediaDaemons(context.Background(), dir, nil, log) // returns at once: nothing to supervise

	_, statErr := os.Stat(started)
	if statErr == nil {
		t.Fatal("nqptp started without AirPlay 2")
	}

	t.Setenv("SOUNDTAP_AIRPLAY_MODE", "scm-ap2")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { defer close(done); runMediaDaemons(ctx, dir, nil, log) }()

	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		_, err := os.Stat(started)
		if err == nil {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("nqptp never started")
		}
	}

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor outlived its context")
	}
}

func TestSnapclientArgsAnchorOrFollower(t *testing.T) {
	server := filepath.Join(t.TempDir(), "multiroom-server")
	anchor := []string{
		"-n", "19", "snapclient", "--player", "file:filename=/dev/null",
		"--mixer", "none", "--logsink", "stderr", "--logfilter", "*:warning", "--hostID", "soundtap-local",
		"tcp://127.0.0.1:1704",
	}
	// Following nobody, or this speaker itself: the silent anchor at nice 19 that never
	// opens the card and takes the stream's own format (no --sampleformat).
	for _, content := range []string{"", "127.0.0.1\nself\n", "localhost\n", "$();|&\n"} {
		if content != "" {
			err := os.WriteFile(server, []byte(content), 0o600)
			if err != nil {
				t.Fatal(err)
			}
		}

		if got := snapclientArgs(server, "", sourceMultiroom); !slices.Equal(got, anchor) {
			t.Fatalf("%q: %q", content, got)
		}
	}
	// Following another speaker: its stream into the bus at the Mixer's rate, channels "*"
	// (snapclient 0.35 exits on a number), and nothing but address characters on the line.
	err := os.WriteFile(server, []byte("192.168.1.20; rm -rf /\nsoundtap-abc\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("AUDIO_BUS", "/run/soundtap/source.pcm")

	want := []string{
		"-n", "0", "snapclient", "--player", "file:filename=/run/soundtap/source.pcm",
		"--sampleformat", "48000:16:*", "--mixer", "none", "--logsink", "stderr", "--logfilter", "*:warning",
		"--hostID", "soundtap-me", "tcp://192.168.1.20rm-rf:1704",
	}
	if got := snapclientArgs(server, "soundtap-me", sourceMultiroom); !slices.Equal(got, want) {
		t.Fatalf("follower: %q", got)
	}
	// Off MULTIROOM (the group's standby) the follow stays, but snapclient is the anchor:
	// the leader's silence kept the follower's whole chain busy.
	anchor[len(anchor)-2] = "soundtap-me"
	if got := snapclientArgs(server, "soundtap-me", sourceStandby); !slices.Equal(got, anchor) {
		t.Fatalf("follower in standby: %q", got)
	}
}

// snapclient runs as a follower on MULTIROOM, and otherwise only while the anchor is
// wanted: plain standby, a follower in its group's standby and a follow of this speaker
// itself all park it.
func TestSnapclientWantedOnlyToFollowOrAnchor(t *testing.T) {
	t.Parallel()

	server := filepath.Join(t.TempDir(), "multiroom-server")
	if snapclientWanted(server, sourceStandby, false) || snapclientWanted(server, sourceMultiroom, false) {
		t.Fatal("wanted with no follow and no group")
	}

	if !snapclientWanted(server, sourceStandby, true) {
		t.Fatal("not wanted while the anchor is")
	}

	err := os.WriteFile(server, []byte("127.0.0.1\nself\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if snapclientWanted(server, sourceMultiroom, false) {
		t.Fatal("wanted to follow this speaker itself")
	}

	err = os.WriteFile(server, []byte("192.168.1.20\nlead\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if !snapclientWanted(server, sourceMultiroom, false) {
		t.Fatal("a playing follower's snapclient is parked")
	}

	if snapclientWanted(server, sourceStandby, false) {
		t.Fatal("a follower in its group's standby keeps snapclient")
	}
}

// The anchor's process gate, with a fake process: in standby without a group nothing
// runs; a group starting (a Want) starts it at once; once the group is gone it keeps
// running for the hold and then stops.
func TestAnchorRunsOnlyWhileAGroupWantsIt(t *testing.T) {
	t.Parallel()

	server := filepath.Join(t.TempDir(), "multiroom-server") // follows nobody
	poke := make(chan struct{}, 1)
	demand := multiroom.NewAnchorDemand(300*time.Millisecond, func() {
		select {
		case poke <- struct{}{}:
		default:
		}
	})

	var running, starts, parks atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)

		wanted := func() bool { return snapclientWanted(server, sourceStandby, demand.Wanted()) }
		supervise.RunWhileWanted(ctx, wanted, poke,
			func(ctx context.Context) {
				starts.Add(1)
				running.Store(1)
				<-ctx.Done()
				running.Store(0)
			}, func() { parks.Add(1) })
	}()

	defer func() { cancel(); <-done }()

	waitFor := func(what string, cond func() bool, within time.Duration) {
		t.Helper()

		for deadline := time.Now().Add(within); !cond(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s not seen within %v", what, within)
			}
		}
	}

	time.Sleep(100 * time.Millisecond)

	if starts.Load() != 0 {
		t.Fatal("the anchor runs in standby without a group")
	}

	poke <- struct{}{} // a source change in standby: still nothing to anchor

	time.Sleep(50 * time.Millisecond)

	if starts.Load() != 0 {
		t.Fatal("a poke without a group started the anchor")
	}

	start := time.Now()

	demand.Want() // an invite or a membership change
	waitFor("the anchor start", func() bool { return running.Load() == 1 }, 100*time.Millisecond)
	waitFor("the anchor stop", func() bool { return running.Load() == 0 }, 2*time.Second)

	if held := time.Since(start); held < 300*time.Millisecond {
		t.Fatalf("the anchor stopped after %v, before its hold", held)
	}

	if starts.Load() != 1 || parks.Load() != 1 {
		t.Fatalf("%d starts and %d parks, want 1 and 1", starts.Load(), parks.Load())
	}

	demand.Want() // the next group starts it again
	waitFor("the second start", func() bool { return starts.Load() == 2 && running.Load() == 1 }, 100*time.Millisecond)
}

func TestSpotifyUsesUpstreamSinkBoundaries(t *testing.T) {
	t.Parallel()

	args := librespotArgs("Kitchen", 10)
	if !slices.Contains(args, "--disable-gapless") || !slices.Contains(args, "--emit-sink-events") {
		t.Fatalf("Spotify needs synchronous close/open boundaries: %v", args)
	}

	if !slices.Contains(procEnv()["librespot"], "SOUNDTAP_SPOTIFY_BOUNDARY=1") {
		t.Fatal("hook still uses asynchronous track-change flushes")
	}
}

// Every daemon that parses network input runs as a user the image creates: a
// user missing from users.txt would stop the daemon, and a daemon missing here runs as root.
func TestMediaDaemonsHaveImageUsers(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../../../os/board/rootfs/users.txt")
	if err != nil {
		t.Fatal(err)
	}

	users := map[string]bool{}

	for line := range strings.SplitSeq(string(data), "\n") {
		if f := strings.Fields(line); len(f) > 0 && !strings.HasPrefix(f[0], "#") {
			users[f[0]] = true
		}
	}

	for _, name := range []string{"shairport-sync", "librespot", snapclient, "snapserver", "nqptp"} {
		if u := procUser[name]; !users[u] {
			t.Errorf("%s runs as %q, which users.txt does not create", name, u)
		}
	}

	if !users[decoderUser] {
		t.Errorf("ffmpeg runs as %q, which users.txt does not create", decoderUser)
	}
}

// audioHealthOf is a Mixer's health with source bytes read from the bus and mixed bytes
// written, and nothing else counted.
func audioHealthOf(source, mixed uint64) audio.Health {
	return audio.Health{
		SourceBytes: source, SourceNonzeroBlocks: 0, MixedBytes: mixed, MixedNonzeroBlocks: 0,
		SinkBytes: 0, SinkNonzeroBlocks: 0, Underruns: 0,
	}
}
