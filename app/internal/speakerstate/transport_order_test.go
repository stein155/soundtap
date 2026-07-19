// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

// Pause and play pressed in quick succession reach the source in that order: the
// source must end up playing, as the speaker says it is.
func TestTransportKeysReachTheSourceInOrder(t *testing.T) {
	s := New()
	var mu sync.Mutex
	var got []string
	s.OnTransport(func(a string) error {
		mu.Lock()
		got = append(got, a)
		mu.Unlock()
		return nil
	})
	if err := s.SetSource(context.Background(), "SPOTIFY"); err != nil {
		t.Fatal(err)
	}
	var want []string
	for range 5 {
		s.ApplyKey("PAUSE")
		s.ApplyKey("PLAY")
		want = append(want, "PAUSE", "PLAY")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		done := slices.Equal(got, want)
		mu.Unlock()
		if done {
			return
		}
		if n >= len(want) || time.Now().After(deadline) {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("source got %v, want %v", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
