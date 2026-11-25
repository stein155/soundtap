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

var (
	errNoAirPlayRemote = errors.New("the AirPlay sender offers no remote control")
	errDACPRefused     = errors.New("the AirPlay sender refused the command")
)

type dacpRemote struct {
	mu        sync.Mutex
	id, token string
}

//nolint:gochecknoglobals // process-wide state: the metadata reader notes the sender, the keys use it
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

// get returns the sender's DACP id and token.
func (d *dacpRemote) get() (string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.id, d.token
}

// dacpCommand is the DACP verb for a transport key.
func dacpCommand(arg string) (string, bool) {
	switch arg {
	case argNext:
		return "nextitem", true
	case argPrev:
		return "previtem", true
	case "pause", "play": // the DACP verbs are the keys' own names
		return arg, true
	default:
		return "", false
	}
}

// airplayKey sends a transport key to the sender that plays.
func airplayKey(ctx context.Context, arg string) error {
	cmd, ok := dacpCommand(arg)

	id, token := airplayRemote.get()
	if !ok || id == "" || token == "" {
		return errNoAirPlayRemote
	}

	addr, err := mdns.ResolveService(ctx, sysbus.Address, "iTunes_Ctrl_"+id, "_dacp._tcp")
	if err != nil {
		return fmt.Errorf("%w: resolve DACP: %w", errNoAirPlayRemote, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ctrl-int/1/"+cmd, http.NoBody)
	if err != nil {
		return fmt.Errorf("DACP %s request: %w", cmd, err)
	}

	req.Header.Set("Active-Remote", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("DACP %s: %w", cmd, err)
	}

	_ = resp.Body.Close()
	if resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: DACP %s: %s", errDACPRefused, cmd, resp.Status)
	}

	return nil
}
