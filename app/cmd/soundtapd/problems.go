// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"log/slog"
	"slices"
	"sync"
)

// daemonProblems are faults soundtapd found in itself that the owner should see without
// reading a log: /api/health lists them as warnings (web.SetDaemonProblems). One entry
// per subject, so a fault that recurs replaces its own line and one that clears goes.
type daemonProblems struct {
	mu sync.Mutex
	m  map[string]string
}

var problems daemonProblems

func (p *daemonProblems) set(subject, text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]string{}
	}
	p.m[subject] = text
}

func (p *daemonProblems) clear(subject string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, subject)
}

func (p *daemonProblems) list() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.m))
	for _, text := range p.m {
		out = append(out, text)
	}
	slices.Sort(out)
	return out
}

// refuseUnknownHardware turns the audio side (audio, buttons, lamps, IR, amplifier) off
// when the model could not be identified, says so at error level and in /api/health, and
// leaves the API up so the speaker can still be updated. Guessing a profile used to drive
// the ST10's GPIOs and codec on whatever board this was.
func refuseUnknownHardware(modelErr error, opt *options, log *slog.Logger) {
	if modelErr == nil {
		return
	}
	log.Error("refusing to drive hardware: speaker model not identified; serving the API only", "err", modelErr)
	problems.set("model", "hardware model not identified, audio and controls are off: "+modelErr.Error())
	opt.noAudio = true
}
