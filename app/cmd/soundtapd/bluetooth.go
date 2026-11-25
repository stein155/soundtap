// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/bluez"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/sysbus"
)

// btPairedInfo matches the info file BlueZ writes for each paired device, as bt-ctl.sh reads it.
const btPairedInfo = "/var/lib/bluetooth/*/*/info"

// bluetoothAgent starts soundtapd's BlueZ pairing agent once, on a model with Bluetooth
// (ADR 0114); nil elsewhere. bt.sh starts bluetoothd; the agent waits for it.
func bluetoothAgent(ctx context.Context, log *slog.Logger) *bluez.Agent {
	btAgentOnce.Do(func() {
		if !onFirmware() || !slices.Contains(board.UserSources, sourceBluetooth) {
			return
		}

		btAgent = bluez.New(log.With("comp", "bt-agent"), speakerNameForBluetooth)

		goSafe("bt-agent", func() { btAgent.Run(ctx) })
	})

	return btAgent
}

//nolint:gochecknoglobals // process-wide state: one BlueZ agent for the daemon, started once
var (
	btAgentOnce sync.Once
	btAgent     *bluez.Agent
)

// speakerNameForBluetooth is what a phone lists: the name soundtapd published, the
// serial-derived setup name before there is one, and "" (BlueZ's hostname) without either.
func speakerNameForBluetooth() string {
	for _, p := range []string{currentNamePath, setupNamePath} {
		b, err := os.ReadFile(p)
		if err == nil {
			if name := strings.TrimSpace(string(b)); name != "" {
				return name
			}
		}
	}

	return ""
}

// errNoBluetoothController is a speaker whose Bluetooth controller is absent or not up yet.
var errNoBluetoothController = errors.New("no Bluetooth controller")

// Bounds on the BlueZ calls: connecting to the system bus, and one pairing window request.
const (
	bluetoothBusTimeout  = 5 * time.Second
	bluetoothPairTimeout = 10 * time.Second
)

// bluetoothBus connects to the system bus for one BlueZ call of the Bluetooth endpoints.
func bluetoothBus(ctx context.Context) (*sysbus.Conn, error) {
	// No controller, no answer: the BlueZ call then waits for bluetoothd until
	// the context runs out, which held every settings page on the ST20 (gen 1,
	// no radio) for ten seconds. Checked per call, because hci_uart loads late.
	if hci, _ := os.ReadDir("/sys/class/bluetooth"); len(hci) == 0 {
		return nil, errNoBluetoothController
	}

	conn, err := sysbus.Dial(ctx, sysbus.Address, bluetoothBusTimeout)
	if err != nil {
		return nil, fmt.Errorf("system bus: %w", err)
	}

	return conn, nil
}

// wireBluetoothPair returns the pairing action for the Bluetooth key (nil without
// Bluetooth) and hooks selecting Bluetooth up to it.
func wireBluetoothPair(ctx context.Context, speaker *speakerstate.Speaker, log *slog.Logger) func() {
	// The Bluetooth key's press-and-hold opens the pairing window (ADR 0055).
	var btPair func()

	if agent := bluetoothAgent(ctx, log); agent != nil {
		frontPanel.Pairing = agent.Pairing
		playingBluetooth := func() bool { return speaker.StatusSnapshot().Source == sourceBluetooth }
		agent.OnTrack(playingBluetooth, func(phone string, track bluez.Track) {
			speaker.SetBluetoothTrack(phone, track.Title, track.Artist, track.Album)
		})
		agent.OnLinkLost(func() { recallLostPhone(ctx, speaker, log) })

		btPair = func() {
			cctx, cancel := context.WithTimeout(ctx, bluetoothPairTimeout)
			defer cancel()

			err := agent.Pair(cctx)
			if err != nil {
				log.Warn("bluetooth pairing window", "comp", "bt-pair", "err", err)
			}
		}
	}

	// Selecting Bluetooth with nothing paired opens the pairing window, as stock does:
	// "it will go to pairing mode if the pairing list is empty" (BoseApp 27.0.6).
	if btPair != nil {
		goSafe("bluetoothFollow", func() {
			followBluetooth(ctx, speaker, btSelectedMarker, func() { wakeBluetooth(btWakeFIFO) },
				func() error { return runBTCtl("pause") }, func() error { return runBTCtl("disconnect") },
				btIdleLink, log.With("comp", "bt-follow"))
		})
		speaker.OnBluetoothSelect(func() {
			if len(pairedBluetoothDevices(ctx)) == 0 {
				log.Info("bluetooth selected with nothing paired: opening the pairing window", "comp", "bt-pair")
				btPair()

				return
			}
			// Paired phones: call them rather than wait to be found.
			btReconnect(ctx, log)
		})
		// BlueZ keeps a directory per paired device; reading them is quick enough for the lock.
		speaker.OnBluetoothPaired(func() bool {
			m, _ := filepath.Glob(btPairedInfo) // the pattern is fixed and valid

			return len(m) > 0
		})
	}

	return btPair
}
