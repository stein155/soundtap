// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate_test

import (
	"context"
	"testing"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func TestChangedFiresOnStateChangeOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spk := speakerstate.New()
	events := spk.Changed()
	spk.StatusSnapshot() // a read is not a change

	select {
	case <-events:
		t.Fatal("Changed fired on a read")
	default:
	}

	err := spk.SetSource(ctx, "SPOTIFY")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-events:
	default:
		t.Fatal("Changed did not fire on a source change")
	}

	events = spk.Changed()

	err = spk.SetVolume(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-events:
	default:
		t.Fatal("Changed did not fire on a volume change")
	}
}
