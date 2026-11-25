// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"reflect"
	"testing"
)

func TestOrderedShutdownMutesBeforeCancel(t *testing.T) {
	t.Parallel()

	var got []string

	orderedShutdown(
		func() { got = append(got, "fade-and-mute") },
		func() { got = append(got, "cancel") },
	)

	if want := []string{"fade-and-mute", "cancel"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shutdown order = %v, want %v", got, want)
	}
}
