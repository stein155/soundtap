// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stein155/soundtap/app/internal/upnp"
)

func TestRegistryBrowseUnknownUSN(t *testing.T) {
	t.Parallel()

	reg := upnp.NewRegistry()

	_, err := reg.Browse(context.Background(), "uuid:nope", "0")
	if err == nil {
		t.Fatal("expected error for unknown USN")
	}
}

// No server found is an empty JSON array: the web UI and the app read a list.
func TestServersEmptyIsArray(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(upnp.NewRegistry().Servers())
	if err != nil || string(b) != "[]" {
		t.Errorf("empty registry = %s, %v; want []", b, err)
	}
}
