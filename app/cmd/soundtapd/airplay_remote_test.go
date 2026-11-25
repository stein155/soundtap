// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"errors"
	"sync"
	"testing"
)

//nolint:paralleltest // swaps package variables other tests read: airplayRemote
func TestAirPlayKeyNeedsASender(t *testing.T) {
	airplayRemote = dacpRemote{mu: sync.Mutex{}, id: "", token: ""}

	err := airplayKey(t.Context(), "next")
	if !errors.Is(err, errNoAirPlayRemote) {
		t.Errorf("no sender: %v", err)
	}

	airplayRemote.note("daid", "ABC")
	airplayRemote.note("acre", "123")

	if id, token := airplayRemote.get(); id != "ABC" || token != "123" {
		t.Errorf("kept %q %q", id, token)
	}

	airplayRemote = dacpRemote{mu: sync.Mutex{}, id: "", token: ""}
}
