// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"

	"github.com/stein155/soundtap/app/internal/lpm"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/web"
)

// soundbarKeys is the button handler for the keys the soundbar processor reports. They
// confirm a waiting request (buttonHandler.confirms): the SoundTouch 300 has no buttons of
// its own, so its remote (producer 2) is the only way to confirm an update, a reset or the
// way back to Bose; it needs someone in the room, or an IR blaster in front of the bar.
// The producer is logged with every hold (ADR 0055).
type soundbarKeys struct{ *buttonHandler }

func newSoundbarKeys(sp keyActor, log *slog.Logger) soundbarKeys {
	h := newButtonHandler(sp, log)
	h.confirms = true
	return soundbarKeys{h}
}

// Key hands a key of the processor to the handler, with its producer for the log of a
// confirming hold.
func (k soundbarKeys) Key(index int, pressed bool, action string, producer uint32) {
	k.confirmLog = []any{"producer", producer}
	k.Handle(index, pressed, action)
}

// wireSoundbar hands the SoundTouch 300's soundbar processor to the web server and runs
// the link to it (ADR 0109).
func wireSoundbar(ctx context.Context, webSrv *web.Server, sp *speakerstate.Speaker, log *slog.Logger) {
	bar := soundbar
	webSrv.AttachSoundbar(web.SoundbarHooks{
		Speakers: bar.Soundbar,
		Tone:     bar.Tone,
		SetTone:  bar.SetTone,
		Choices: map[string]web.SoundbarChoice{
			"audio-mode": {Get: bar.AudioModes.Get, Set: bar.AudioModes.Set},
			"cec-mode":   {Get: bar.CECModes.Get, Set: bar.CECModes.Set},
			"hdmi-input": {Get: bar.HDMIInputs.Get, Set: bar.HDMIInputs.Set},
		},
		LipSync:      bar.LipSync,
		SetLipSync:   bar.SetLipSync,
		LipSyncMax:   lpm.LipSyncMax,
		StartADAPTiQ: bar.StartADAPTiQ,
		PickInput:    bar.PickInput,
	})
	goSafe("lpm", func() { bar.Run(ctx, sp, newSoundbarKeys(sp, log), log) })
}
