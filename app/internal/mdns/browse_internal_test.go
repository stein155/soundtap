// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mdns

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// A host that announces and withdraws names over and over cannot start resolves without
// end: they stop at maxPeers in flight, and every one hands its outcome back.
func TestFlappingNamesCannotPileUpResolves(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	brw := &browser{
		r: new(Responder), c: nil, t: &table{self: "", known: map[string]map[string]Peer{}, why: ""},
		updates: make(chan Peer, 1), path: "/b", pending: map[instanceKey]uint64{}, gen: 0,
		results: make(chan lookup), slots: make(chan struct{}), // no slot ever frees: every resolve waits
		inflight: 0, cancels: map[instanceKey]context.CancelFunc{},
	}

	report := func(what, name string) {
		err := brw.signal(ctx, &dbus.Signal{
			Sender: "", Sequence: 0, Path: "/b", Name: avahiBrowser + "." + what,
			Body: []any{int32(2), protoIPv4, name, serviceType, "local", uint32(0)},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for count := range 3 * maxPeers {
		name := "flap-" + strconv.Itoa(count)
		report("ItemNew", name)
		report("ItemRemove", name)

		if brw.inflight > maxPeers {
			t.Fatalf("%d resolves in flight after %d names", brw.inflight, count+1)
		}
	}

	if !strings.Contains(strings.Join(brw.r.Recent(), "\n"), "too many instances resolving") {
		t.Fatal("the flood was never held back")
	}

	deadline := time.After(5 * time.Second)

	for brw.inflight > 0 {
		select {
		case result := <-brw.results:
			brw.inflight--
			brw.resolved(&result)
		case <-deadline:
			t.Fatalf("%d superseded resolves never handed back", brw.inflight)
		}
	}
}
