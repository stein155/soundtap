// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"errors"
	"testing"
)

func TestAirPlayKeyNeedsASender(t *testing.T) {
	airplayRemote = dacpRemote{}
	if err := airplayKey(t.Context(), "next"); !errors.Is(err, errNoAirPlayRemote) {
		t.Errorf("no sender: %v", err)
	}
	airplayRemote.note("daid", "ABC")
	airplayRemote.note("acre", "123")
	if id, token := airplayRemote.get(); id != "ABC" || token != "123" {
		t.Errorf("kept %q %q", id, token)
	}
	airplayRemote = dacpRemote{}
}
