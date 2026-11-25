// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/ubootenv"
	"github.com/stein155/soundtap/app/internal/ubootenv/envfixture"
	"github.com/stein155/soundtap/app/internal/ubootenv/ubootenvtest"
)

func factoryEnv(t *testing.T) *ubootenv.Env {
	t.Helper()

	env, err := ubootenv.ParseLegacy(envfixture.Image())
	if err != nil {
		t.Fatal(err)
	}

	return env
}

// Selecting PONG is the change the WLA install path never made. Without it a
// package written into PONG is simply never booted, and the endpoint said so in a
// warning instead of doing something about it.
func TestSelectPongPointsBootcmdAtTheSecondSlot(t *testing.T) {
	t.Parallel()

	env := factoryEnv(t)

	err := SelectSlot(env, pong, SoundTapMarkers("NAND.Persistent-Storage"))
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := env.Get("bootcmd"); got != bootcmdPong {
		t.Fatalf("bootcmd = %q, want %q", got, bootcmdPong)
	}

	args, _ := env.Get("nand_args2")
	for _, want := range []string{"ubi.mtd=9,2048", "soundtap.nandroot=1", "ubi.mtd=NAND.Persistent-Storage,2048"} {
		if !strings.Contains(args, want) {
			t.Errorf("nand_args2 = %q, want it to contain %q", args, want)
		}
	}
	// The slot that is not being booted must not claim SoundTap's command line.
	if other, _ := env.Get("nand_args"); strings.Contains(other, "soundtap.nandroot") {
		t.Errorf("nand_args kept SoundTap markers: %q", other)
	}
}

func TestSelectPingUsesTheFirstSlotsArgs(t *testing.T) {
	t.Parallel()

	env := factoryEnv(t)

	err := SelectSlot(env, ping, SoundTapMarkers("NAND.Persistent-Storage"))
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := env.Get("bootcmd"); got != bootcmdPing {
		t.Fatalf("bootcmd = %q, want %q", got, bootcmdPing)
	}

	if args, _ := env.Get("nand_args"); !strings.Contains(args, "soundtap.nandroot=1") {
		t.Errorf("nand_args = %q, want the markers on the slot being booted", args)
	}
}

// Installing twice must not grow the entry. The environment is a single erase
// block, so every rewrite erases the only copy there is.
func TestSelectingTwiceIsIdempotent(t *testing.T) {
	t.Parallel()

	markers := SoundTapMarkers("NAND.Persistent-Storage")

	once := factoryEnv(t)

	err := SelectSlot(once, pong, markers)
	if err != nil {
		t.Fatal(err)
	}

	first, err := once.EncodeLegacy()
	if err != nil {
		t.Fatal(err)
	}

	twice, err := ubootenv.ParseLegacy(first)
	if err != nil {
		t.Fatal(err)
	}

	err = SelectSlot(twice, pong, markers)
	if err != nil {
		t.Fatal(err)
	}

	second, err := twice.EncodeLegacy()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Fatal("selecting the same slot twice changed the environment")
	}
}

// Going back to stock must hand the environment back the way Bose would have left
// it: PING selected and not one SoundTap marker anywhere. Byte-for-byte against the
// factory dump, which is the only standard worth holding this to.
func TestGoingBackToStockRestoresTheFactoryEnvironment(t *testing.T) {
	t.Parallel()

	original := envfixture.Image()

	env := factoryEnv(t)

	err := SelectSlot(env, pong, SoundTapMarkers("NAND.Persistent-Storage"))
	if err != nil {
		t.Fatal(err)
	}

	flashed, err := env.EncodeLegacy()
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(flashed, original) {
		t.Fatal("the SoundTap install changed nothing, so this proves nothing")
	}

	back, err := ubootenv.ParseLegacy(flashed)
	if err != nil {
		t.Fatal(err)
	}

	err = SelectSlot(back, ping, nil)
	if err != nil {
		t.Fatal(err)
	}

	restored, err := back.EncodeLegacy()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(restored, original) {
		t.Fatal("going back to stock did not restore the factory environment byte for byte")
	}
}

// Stock misspells the partition on some images, and the marker has to say what
// /proc/mtd says or UBI will not find it.
func TestMarkersFollowTheSpellingOnTheBoard(t *testing.T) {
	t.Parallel()

	env := factoryEnv(t)

	err := SelectSlot(env, pong, SoundTapMarkers("NAND.Persistant-Storage"))
	if err != nil {
		t.Fatal(err)
	}

	args, _ := env.Get("nand_args2")
	if !strings.Contains(args, "ubi.mtd=NAND.Persistant-Storage,2048") {
		t.Errorf("nand_args2 = %q", args)
	}
}

// An environment without the entry bootcmd would be told to run is not one to
// rewrite: U-Boot would run an empty command and stop at a prompt no one can see.
func TestSelectRefusesWhenTheBootCommandIsMissing(t *testing.T) {
	t.Parallel()

	env := factoryEnv(t)

	trimmed, err := ubootenv.ParseLegacy(envfixture.Image())
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild without nand_boot2 by writing every other key into a fresh image.
	fresh, err := ubootenv.ParseLegacy(mustEncodeWithout(t, trimmed, "nand_boot2"))
	if err != nil {
		t.Fatal(err)
	}

	err = SelectSlot(fresh, pong, nil)
	if err == nil {
		t.Fatal("selecting a slot whose boot command is missing was accepted")
	}

	err = SelectSlot(env, pong, nil)
	if err != nil {
		t.Fatalf("the intact environment must still work: %v", err)
	}
}

func mustEncodeWithout(t *testing.T, env *ubootenv.Env, drop string) []byte {
	t.Helper()

	fresh := ubootenv.NewEnv()

	for _, key := range env.Keys() {
		if key == drop {
			continue
		}

		value, _ := env.Get(key)
		fresh.Set(key, value)
	}

	b, err := fresh.EncodeLegacy()
	if err != nil {
		t.Fatal(err)
	}

	return b
}

// An owner key a build before 0.1.0 stored (ADR 0048 has none any more) leaves with an
// install's selection, and from both copies once the next write lands in the other one.
func TestSelectionRemovesTheRetiredOwnerKey(t *testing.T) {
	t.Parallel()

	fake := newRawNAND()
	env := factoryEnv(t)
	env.Set(retiredOwnerKey, "88omIN8FRazo9PIEnIHv3JUqYTQBzN+wnqpexHvX+MY=")

	err := ubootenvtest.Image(fake.nand, "/dev/mtd5", "/dev/mtd4", env)
	if err != nil {
		t.Fatal(err)
	}

	flasher := fake.installer(true)

	err = flasher.SelectBootSlot(fake.read, ping, SoundTapMarkers(""))
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := fake.env(t).Get(retiredOwnerKey); ok {
		t.Fatal("the selection kept the owner key")
	}

	err = flasher.SelectBootSlot(fake.read, pong, SoundTapMarkers(""))
	if err != nil {
		t.Fatal(err)
	}

	for index, raw := range [][]byte{
		fake.nand["/dev/mtd5"][:ubootenv.Size],
		fake.nand["/dev/mtd4"][ubootenv.RedundantOffset : ubootenv.RedundantOffset+ubootenv.Size],
	} {
		c, _, err := ubootenv.ParseCopy(raw)
		if err != nil {
			t.Fatalf("copy %d: %v", index, err)
		}

		if _, ok := c.Get(retiredOwnerKey); ok {
			t.Errorf("copy %d still holds the owner key", index)
		}
	}
}
