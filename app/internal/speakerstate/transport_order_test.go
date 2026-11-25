// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// Pause and play pressed in quick succession reach the source in that order: the
// source must end up playing, as the speaker says it is.
func TestTransportKeysReachTheSourceInOrder(t *testing.T) {
	t.Parallel()

	spk := speakerstate.New()

	var (
		mu  sync.Mutex
		got []string
	)

	spk.OnTransport(func(a string) error {
		mu.Lock()

		got = append(got, a)
		mu.Unlock()

		return nil
	})

	err := spk.SetSource(context.Background(), "SPOTIFY")
	if err != nil {
		t.Fatal(err)
	}

	want := make([]string, 0, 10)

	for range 5 {
		spk.ApplyKey("PAUSE")
		spk.ApplyKey("PLAY")

		want = append(want, "PAUSE", "PLAY")
	}

	deadline := time.Now().Add(2 * time.Second)

	for {
		mu.Lock()
		count := len(got)
		done := slices.Equal(got, want)
		mu.Unlock()

		if done {
			return
		}

		if count >= len(want) || time.Now().After(deadline) {
			mu.Lock()
			seen := slices.Clone(got)
			mu.Unlock()

			t.Fatalf("source got %v, want %v", seen, want)
		}

		time.Sleep(5 * time.Millisecond)
	}
}
