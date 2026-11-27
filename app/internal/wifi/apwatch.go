// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
)

// apStartRounding is how finely the time to the first beacon is logged.
const apStartRounding = 100 * time.Millisecond

// watchAP logs the setup AP's state every Timing.APWatch while it runs, when anything in it
// changed: wlan1's frame counts on every model, and on the ST20 what the BCO reports in
// bcolink0/bco/ap (soundtap_bco, ADR 0031): beacons and how long the first took from AP_START,
// probe responses, the core's receive counters and each associated client. Its first beacon
// is logged once, with the time since "starting the setup AP". A phone that joins and gets no
// address then shows where its frames stopped: at the radio, the BCO, the link or udhcpd.
func (m *Manager) watchAP(ctx context.Context, started time.Time) {
	every := m.cfg.Timing.APWatch
	if every <= 0 {
		return
	}

	gen := m.apGen.Add(1)

	safego.Go("wifi-apwatch", func() {
		var last string

		beaconing := false

		for sleep(ctx, every) && m.apGen.Load() == gen && exists(m.state("setup-ap")) {
			state := m.readAPState()
			if !beaconing && state.counter("beacons") > 0 {
				beaconing = true

				m.log.Info("setup AP beaconing", "after", time.Since(started).Round(apStartRounding),
					"first_beacon_ms", state.counter("first_beacon_ms"))
			}

			key := state.key()
			if key != last {
				last = key

				state.log(m.log.Info)
			}
		}
	})
}

// apState is one look at the setup AP: name/value counters in the order read, and a line per
// client as soundtap_bco writes it.
type apState struct {
	names   []string
	values  map[string]uint64
	clients [][]string // "client" <mac> then name value pairs
}

// nameValue is a line of one counter: its name and its value.
const nameValue = 2

// readAPState reads wlan1's statistics and, where there is one, the BCO's bco/ap.
func (m *Manager) readAPState() apState {
	state := apState{names: nil, values: map[string]uint64{}, clients: nil}
	stats := filepath.Join(m.cfg.Paths.SysNet, m.cfg.AP, "statistics")

	for _, name := range []string{"rx_packets", "tx_packets", "rx_dropped", "tx_dropped"} {
		if v, ok := readUint(filepath.Join(stats, name)); ok {
			state.add("wlan1_"+name, v)
		}
	}

	files, _ := filepath.Glob(filepath.Join(m.cfg.Paths.SysNet, "bcolink*", "bco", "ap"))
	if len(files) == 0 {
		return state
	}

	raw, err := os.ReadFile(files[0])
	if err != nil {
		return state
	}

	for line := range strings.Lines(string(raw)) {
		fields := strings.Fields(line)
		switch {
		case len(fields) >= nameValue && fields[0] == "client":
			state.clients = append(state.clients, fields)
		case len(fields) == nameValue:
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				state.add(fields[0], v)
			}
		default:
			// Not a line soundtap_bco writes.
		}
	}

	return state
}

func (s *apState) add(name string, v uint64) {
	s.names = append(s.names, name)
	s.values[name] = v
}

func (s *apState) counter(name string) uint64 { return s.values[name] }

// key is what makes a look worth logging: everything but how long each client has been
// silent, which changes on every look.
func (s *apState) key() string {
	var key strings.Builder

	for _, n := range s.names {
		key.WriteString(n + "=" + strconv.FormatUint(s.values[n], 10) + " ")
	}

	for _, client := range s.clients {
		for field := 0; field < len(client); field++ {
			if client[field] == "idle_ms" {
				field++

				continue
			}

			key.WriteString(client[field] + " ")
		}
	}

	return key.String()
}

// log writes the counters as one line and each client as one, the client by the last three
// octets of its address: enough to tell phones apart, not to name one.
func (s *apState) log(info func(msg string, args ...any)) {
	attrs := make([]any, 0, nameValue*len(s.names))
	for _, n := range s.names {
		attrs = append(attrs, n, s.values[n])
	}

	info("setup AP status", attrs...)

	for _, client := range s.clients {
		attrs = []any{"client", shortMAC(client[1])}
		for at := nameValue; at+1 < len(client); at += nameValue {
			attrs = append(attrs, client[at], client[at+1])
		}

		info("setup AP client", attrs...)
	}
}

// shortMAC is a MAC's last three octets.
func shortMAC(mac string) string {
	const tail = len("77:66:55")
	if len(mac) <= tail {
		return mac
	}

	return "…" + mac[len(mac)-tail:]
}

// readUint reads a sysfs counter.
func readUint(path string) (uint64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}

	v, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)

	return v, err == nil
}
