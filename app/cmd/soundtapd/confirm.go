// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/web"
)

// confirmTokenPath holds the one-time token that stands in for the hold on the speaker. It
// is root-only, so reading it takes root ssh, which production does not have.
//
//nolint:gochecknoglobals,gosec // a test seam: tests write the token in a temp dir; a path, not a credential
var confirmTokenPath = "/run/soundtap/confirm-token"

// confirmHold is how long a key on the speaker is held to confirm a waiting request. A
// tap is what a hand does by accident or out of habit, turning a speaker down or picking
// a preset; two seconds of one key is meant. The confirming key does nothing else, so
// holdToStore being shorter cannot store a preset on the way.
const confirmHold = 2 * time.Second

// confirmPending is set while a request waits for the hold, so the lamps and the display
// can ask for one.
//
//nolint:gochecknoglobals // process-wide state: the confirmer sets it, the lamps and the display read it
var confirmPending atomic.Bool

// The confirm token's size and modes: its directory is the daemon's /run tree, the token
// itself root-only.
const (
	confirmTokenBytes   = 16
	confirmTokenDirMode = 0o755
	confirmTokenMode    = 0o600
)

// confirmResultShown is how long the display says a request was or was not confirmed.
const confirmResultShown = 2 * time.Second

// confirmWaiter is the one request that waits for the hold.
type confirmWaiter struct {
	gen  uint64     // which request this is; a hold confirms only the one it began under
	done chan error // buffered: nil when held, web.ErrConfirmSuperseded when replaced
}

// deviceConfirm is the speaker half of the hold confirmation: a dangerous API request waits
// here until a key on the speaker's own panel is held for confirmHold (buttonHandler).
//
// One request waits at a time, and a newer one replaces it: the older gets
// web.ErrConfirmSuperseded. Refusing the newer one instead let a host on the network that
// repeats a request keep the lane, and with it the owner's own request, shut for good.
// Replacing needs no rate limit on top: a flood only replaces itself, the owner's newest
// request still takes the lane, and the panel names what a hold would confirm.
type deviceConfirm struct {
	mu     sync.Mutex
	wait   *confirmWaiter // non-nil while a request waits
	gen    uint64
	token  string
	window time.Duration
	show   func(text string, d time.Duration) // the ST20 display; nil elsewhere
	prompt string                             // what the display asks for while a request waits
	log    *slog.Logger
}

// confirmer is the speaker's one confirmation lane.
//
//nolint:gochecknoglobals // process-wide state: the API's requests and the speaker's keys share it
var confirmer = &deviceConfirm{
	mu:     sync.Mutex{},
	wait:   nil,
	gen:    0,
	token:  "",
	window: web.ConfirmWindow,
	show:   nil,
	prompt: "",
	log:    nil,
}

// rotate writes a fresh token. A failed write leaves no usable token, only the hold.
func (c *deviceConfirm) rotate() {
	token := make([]byte, confirmTokenBytes)
	_, _ = rand.Read(token) // crypto/rand.Read never returns an error; it aborts the process instead

	c.token = hex.EncodeToString(token)

	err := os.MkdirAll(filepath.Dir(confirmTokenPath), confirmTokenDirMode)
	if err == nil {
		err = os.WriteFile(confirmTokenPath, []byte(c.token+"\n"), confirmTokenMode)
		if err == nil {
			return
		}
	}

	c.token = ""
	if c.log != nil {
		c.log.Warn("confirm token not written; only a hold on the speaker confirms", "path", confirmTokenPath)
	}
}

// confirmPrompt is what the ST20 panel says while a request waits, so whoever holds a key
// sees what it confirms. what is the request's method and path.
func confirmPrompt(what string) string {
	switch what {
	case "POST /api/factory-reset":
		return "Hold a button to reset"
	case "POST /api/restore":
		return "Hold a button to restore"
	case "POST /api/reboot":
		return "Hold a button to restart"
	case "POST /api/wifi":
		return "Hold a button for Wi-Fi"
	case "POST /api/setup":
		return "Hold a button for setup"
	case "POST /api/bluetooth/clear", "POST /api/bluetooth/forget":
		return "Hold a button to unpair"
	case "POST /api/firmware", "POST /api/core-update", "POST /api/soundtapd", "DELETE /api/soundtapd",
		"POST /api/update":
		return "Hold a button to install"
	}

	if strings.HasPrefix(what, "GET /api/debug") {
		return "Hold a button for logs"
	}

	return "Hold a button"
}

func (c *deviceConfirm) confirm(ctx context.Context, what, token string) error {
	c.mu.Lock()
	// The token is root on the speaker already: it confirms at once and leaves a waiting
	// request alone.
	if token != "" && c.token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(c.token)) == 1 {
		c.rotate()
		c.mu.Unlock()
		c.log.Info("confirmed by token", "action", what)

		return nil
	}

	if c.wait != nil {
		c.wait.done <- web.ErrConfirmSuperseded
	}

	c.gen++
	waiter := &confirmWaiter{gen: c.gen, done: make(chan error, 1)}
	c.wait = waiter
	c.prompt = confirmPrompt(what)

	confirmPending.Store(true)
	c.mu.Unlock()
	c.log.Info("waiting for a hold on the speaker", "action", what)
	c.display(c.prompt, c.window)

	timer := time.NewTimer(c.window)
	defer timer.Stop()

	var err error

	answered := false

	select {
	case err = <-waiter.done:
		answered = true
	case <-timer.C:
		err = web.ErrNotConfirmed
	case <-ctx.Done():
		err = ctx.Err()
	}

	c.mu.Lock()
	if c.wait == waiter {
		c.wait = nil

		confirmPending.Store(false)
	} else if !answered {
		// A hold or a newer request answered as this one gave up: that answer stands,
		// unless the caller has gone, when a hold confirmed nothing it will see.
		err2 := <-waiter.done
		if err2 != nil || ctx.Err() == nil {
			err = err2
		}
	}
	c.mu.Unlock()

	c.report(what, err)

	return err
}

// report logs and shows how the request what ended.
func (c *deviceConfirm) report(what string, err error) {
	switch {
	case err == nil:
		c.log.Info("confirmed on the speaker", "action", what)
		c.display("Confirmed", confirmResultShown)
	case errors.Is(err, web.ErrConfirmSuperseded):
		// The newer request's prompt is on the panel now.
		c.log.Info("replaced by a newer request", "action", what)
	case errors.Is(err, web.ErrNotConfirmed):
		c.log.Info("not confirmed in time", "action", what)
		c.display("Not confirmed", confirmResultShown)
	default: // the caller went away; nothing to show
	}
}

func (c *deviceConfirm) display(text string, d time.Duration) {
	if c.show != nil {
		c.show(text, d)
	}
}

// waiting returns the waiting request's generation, or 0 when none waits. A hold that
// starts now confirms that request and no later one.
func (c *deviceConfirm) waiting() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.wait == nil {
		return 0
	}

	return c.wait.gen
}

// held confirms the request of generation gen, if it still waits, and reports whether it
// did. A request that replaced it since is not confirmed by a hold begun before it.
func (c *deviceConfirm) held(gen uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.wait == nil || c.wait.gen != gen {
		return false
	}

	c.wait.done <- nil

	c.wait = nil

	confirmPending.Store(false)

	return true
}

// remind asks again, after a press too short to confirm.
func (c *deviceConfirm) remind() {
	c.mu.Lock()
	prompt, waiting := c.prompt, c.wait != nil
	c.mu.Unlock()

	if waiting {
		c.display(prompt, c.window)
	}
}
