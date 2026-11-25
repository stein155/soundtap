// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/hw/stm32amp"
)

// sa5Bus is the SA-5 module's I2C1, where the amplifier controller sits next to the NCP6922.
const sa5Bus = "/dev/i2c-1"

// ampThermal is the gain the SA-5's controller asks the host to apply; unset (1) everywhere
// else. Stock applied it in the codec's miniDSP; soundtapd is the only DAC writer (ADR 0012),
// so it trims the source bus with it.
//
//nolint:gochecknoglobals // process-wide state: the SA-5 supervisor sets it, the mixer reads it
var ampThermal atomic.Pointer[float64]

// thermalGain is the SA-5's thermal gain, 1 until its controller asks for less.
func thermalGain() float64 {
	if gain := ampThermal.Load(); gain != nil {
		return *gain
	}

	return 1
}

// sourceTrim is how trackOwnership's source gain reaches the mixer again when only the thermal
// gain changed: the arbiter and the source it last set.
//
//nolint:gochecknoglobals // process-wide state: trackOwnership sets it, the SA-5 supervisor reapplies it
var sourceTrim struct {
	sync.Mutex

	arb    *audio.Arbiter
	source string
}

// setSourceTrim sets the source bus's fixed gain: the model's for source times the thermal gain.
func setSourceTrim(arb *audio.Arbiter, source string) {
	sourceTrim.Lock()
	sourceTrim.arb, sourceTrim.source = arb, source
	sourceTrim.Unlock()
	arb.SetSourceGain(board.SourceGain(source) * thermalGain())
}

func reapplySourceTrim() {
	sourceTrim.Lock()
	arb, source := sourceTrim.arb, sourceTrim.source
	sourceTrim.Unlock()

	if arb != nil {
		arb.SetSourceGain(board.SourceGain(source) * thermalGain())
	}
}

// runSA5Amp supervises the SA-5's amplifier controller until ctx ends: a fault mutes, the
// thermal attenuation trims the source bus. A model without one never calls it.
func runSA5Amp(ctx context.Context, log *slog.Logger) {
	dev, err := stm32amp.Open(sa5Bus)
	if err != nil {
		log.Error("SA-5: no amplifier controller", "err", err)

		return
	}
	defer dev.Close()

	supervisor := &stm32amp.Supervisor{
		Amp:     stm32amp.New(dev),
		OnFault: func() { amplifier.SetMute(true, log) },
		OnAttenuation: func(gain float64) {
			log.Info("SA-5 thermal attenuation", "gain", gain)
			ampThermal.Store(&gain)
			reapplySourceTrim()
		},
		Log: log,
	}
	sa5Supervisor.Store(supervisor)
	// Stock unmutes only once the controller says the amplifier is on, 100 ms later. The
	// mute holds until then; trackOwnership asks again every 50 ms.
	gate := supervisor.MayUnmute
	unmuteGate.Store(&gate)
	supervisor.Run(ctx)
}

// sa5Supervisor is the running supervisor, for the amp fault the panel and health report.
//
//nolint:gochecknoglobals // process-wide state: runSA5Amp sets it, the panel and health read it
var sa5Supervisor atomic.Pointer[stm32amp.Supervisor]

// sa5Faulted reports whether the SA-5's controller reported a fault.
func sa5Faulted() bool {
	if s := sa5Supervisor.Load(); s != nil {
		_, fault, _ := s.State()

		return fault
	}

	return false
}
