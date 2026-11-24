// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/hw/stm32amp"
)

// sa5Bus is the SA-5 module's I2C1, where the amplifier controller sits next to the NCP6922.
const sa5Bus = "/dev/i2c-1"

// ampThermal is the gain the SA-5's controller asks the host to apply, as float64 bits;
// 1 everywhere else. Stock applied it in the codec's miniDSP; soundtapd is the only DAC
// writer (ADR 0012), so it trims the source bus with it.
var ampThermal atomic.Uint64

func init() { ampThermal.Store(math.Float64bits(1)) }

func thermalGain() float64 { return math.Float64frombits(ampThermal.Load()) }

// sourceTrim is how trackOwnership's source gain reaches the mixer again when only the thermal
// gain changed: the arbiter and the source it last set.
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
	s := &stm32amp.Supervisor{
		Amp:     stm32amp.New(dev),
		OnFault: func() { amplifier.SetMute(true, log) },
		OnAttenuation: func(g float64) {
			log.Info("SA-5 thermal attenuation", "gain", g)
			ampThermal.Store(math.Float64bits(g))
			reapplySourceTrim()
		},
		Log: log,
	}
	sa5Supervisor.Store(s)
	// Stock unmutes only once the controller says the amplifier is on, 100 ms later. The
	// mute holds until then; trackOwnership asks again every 50 ms.
	gate := s.MayUnmute
	unmuteGate.Store(&gate)
	s.Run(ctx)
}

// sa5Supervisor is the running supervisor, for the amp fault the panel and health report.
var sa5Supervisor atomic.Pointer[stm32amp.Supervisor]

// sa5Faulted reports whether the SA-5's controller reported a fault.
func sa5Faulted() bool {
	if s := sa5Supervisor.Load(); s != nil {
		_, fault, _ := s.State()
		return fault
	}
	return false
}
