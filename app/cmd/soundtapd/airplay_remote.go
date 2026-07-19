// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/stein155/soundtap/app/internal/mdns"
	"github.com/stein155/soundtap/app/internal/sysbus"
)

// The speaker's keys and remote steer an AirPlay sender the way stock does: through the
// sender's DACP remote-control service. shairport-sync passes on the sender's DACP id
// (daid) and the token it accepts (acre); the sender announces the service over mDNS as
// iTunes_Ctrl_<id>. Not every app on the phone honours every command.

var errNoAirPlayRemote = errors.New("the AirPlay sender offers no remote control")

type dacpRemote struct {
	mu        sync.Mutex
	id, token string
}

var airplayRemote dacpRemote

// note keeps a daid or acre item from the metadata pipe.
func (d *dacpRemote) note(code, value string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if code == "daid" {
		d.id = value
	} else {
		d.token = value
	}
}

func (d *dacpRemote) get() (id, token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.id, d.token
}

var dacpCommand = map[string]string{argNext: "nextitem", argPrev: "previtem", "pause": "pause", "play": "play"} //nolint:goconst // DACP verbs

// airplayKey sends a transport key to the sender that plays.
func airplayKey(ctx context.Context, arg string) error {
	cmd, ok := dacpCommand[arg]
	id, token := airplayRemote.get()
	if !ok || id == "" || token == "" {
		return errNoAirPlayRemote
	}
	addr, err := mdns.ResolveService(ctx, sysbus.Address, "iTunes_Ctrl_"+id, "_dacp._tcp")
	if err != nil {
		return fmt.Errorf("%w: resolve DACP: %w", errNoAirPlayRemote, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ctrl-int/1/"+cmd, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Active-Remote", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("DACP %s: %s", cmd, resp.Status)
	}
	return nil
}
