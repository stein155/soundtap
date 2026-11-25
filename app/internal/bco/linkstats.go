// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LinkStats are the own BCO app's transmit and link counters and the SCM driver's flow-control
// state, as soundtap_bco shows them in /sys/class/net/bcolink0/bco/ (ADR 0031). The BCO counts
// from its app's start in 32 bits, so the counters wrap and start over after a BCO power cycle.
type LinkStats struct {
	// TxOK are Wi-Fi frames the peer acknowledged.
	TxOK uint64 `json:"txOk"`
	// TxFailed are frames lost on air after the core's retries.
	TxFailed uint64 `json:"txFailed"`
	// TxDropped are frames the BCO never sent: no free transmit buffer, or not joined.
	TxDropped uint64 `json:"txDropped"`
	// LinkDropped are messages the BCO dropped at the USB link, on any channel.
	LinkDropped uint64 `json:"linkDropped"`
	// TxFrames are frames from the SCM the radio took.
	TxFrames uint64 `json:"txFrames"`
	// FlowControl is true while the driver holds Wi-Fi frames in its queue for want of a free
	// BCO transmit buffer instead of sending them to be dropped.
	FlowControl bool `json:"flowControl"`
	// FlowStops counts the times the driver stopped a Wi-Fi queue for want of one.
	FlowStops uint64 `json:"flowStops"`
}

// ReadLinkStats reads the counters of the BCO link under netDir (/sys/class/net). False when
// there is no such link (every model but the ST20) or its BCO app has no counters (one from
// before BCOLINK_CAP_STATS). A read asks the BCO at most once a second (the driver caches).
func ReadLinkStats(netDir string) (LinkStats, bool) {
	ents, err := os.ReadDir(netDir)
	if err != nil {
		return LinkStats{}, false
	}

	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), "bcolink") {
			continue
		}

		dir := filepath.Join(netDir, e.Name(), "bco")
		if _, err := os.Stat(dir); err != nil {
			continue
		}

		return readLinkStats(dir)
	}

	return LinkStats{}, false
}

func readLinkStats(dir string) (LinkStats, bool) {
	ok := true
	num := func(name string) uint64 {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			ok = false

			return 0
		}

		v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		if err != nil {
			ok = false
		}

		return v
	}
	s := LinkStats{
		TxOK:        num("tx_ok"),
		TxFailed:    num("tx_failed"),
		TxDropped:   num("tx_dropped"),
		LinkDropped: num("link_dropped"),
		TxFrames:    num("tx_frames"),
		FlowControl: num("flow") == 1,
		FlowStops:   num("flow_stops"),
	}

	return s, ok
}
