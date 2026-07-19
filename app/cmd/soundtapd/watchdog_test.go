// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stein155/soundtap/app/internal/audio"
)

func TestSpeakerHealthyNeedsHTTPAndAMovingPump(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	ctx := context.Background()
	var mixed uint64
	check := speakerHealthy(srv.URL, func() audio.Health { return audio.Health{MixedBytes: mixed} })
	if err := check(ctx); err != nil {
		t.Fatalf("first check: %v", err)
	}
	mixed += 3840
	if err := check(ctx); err != nil {
		t.Fatalf("moving pump: %v", err)
	}
	if err := check(ctx); err == nil {
		t.Fatal("a pump that did not move passed")
	}
	// A pump resting in standby does not move and is healthy; the check right after it
	// wakes is too, since the run is new.
	defer pumpRest.set(false)
	pumpRest.set(true)
	for range 2 {
		if err := check(ctx); err != nil {
			t.Fatalf("resting pump: %v", err)
		}
	}
	pumpRest.set(false)
	if err := check(ctx); err != nil {
		t.Fatalf("first check after the rest: %v", err)
	}
	if err := check(ctx); err == nil {
		t.Fatal("a woken pump that did not move passed")
	}
	srv.Close()
	if err := speakerHealthy(srv.URL, nil)(ctx); err == nil {
		t.Fatal("a dead HTTP listener passed")
	}
}
