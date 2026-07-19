// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package oplock is the one lock behind every operation that rewrites the speaker: a
// firmware install, an app update, a soundtapd swap, a backup restore and a factory reset
// (ADR 0052).
//
// It is an flock on Path, not only a mutex, so it outlives this process: the mtd-utils
// children of an install inherit the descriptor (ExtraFiles) and keep the lock until
// they exit, and a soundtapd restarted in the middle of a NAND write finds it taken.
package oplock

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Path is set by soundtapd at start-up. Empty (tests, development) leaves only the
// in-process mutex.
var Path string

// fallback stands in for the file when Path is set but cannot be opened: separate
// Locks then still exclude each other inside this process, instead of each trusting
// only its own mutex.
var fallback sync.Mutex

// Lock is usable as its zero value. Separate Locks exclude each other through the file.
type Lock struct {
	mu         sync.Mutex // held while locked; guards f and onFallback
	f          *os.File
	onFallback bool
}

// TryLock takes the lock if nothing else holds it.
func (l *Lock) TryLock() bool {
	if !l.mu.TryLock() {
		return false
	}
	if !l.acquire(syscall.LOCK_EX | syscall.LOCK_NB) {
		l.mu.Unlock()
		return false
	}
	return true
}

// Lock waits for the lock.
func (l *Lock) Lock() {
	l.mu.Lock()
	l.acquire(syscall.LOCK_EX)
}

// Unlock releases the lock. A child that inherited File() keeps the file locked until it exits.
func (l *Lock) Unlock() {
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	if l.onFallback {
		l.onFallback = false
		fallback.Unlock()
	}
	l.mu.Unlock()
}

// File is the locked descriptor while the lock is held, for a child's ExtraFiles; nil without Path.
func (l *Lock) File() *os.File { return l.f }

// acquire reports false only when another holder has the lock. A lock file that cannot
// be opened falls back to the process-wide mutex rather than refusing every install: an
// install is also the way out of a broken /run.
func (l *Lock) acquire(how int) bool {
	if Path == "" {
		return true
	}
	_ = os.MkdirAll(filepath.Dir(Path), 0o755)
	f, err := os.OpenFile(Path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		if how&syscall.LOCK_NB != 0 {
			l.onFallback = fallback.TryLock()
		} else {
			fallback.Lock()
			l.onFallback = true
		}
		return l.onFallback
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()
		return false
	}
	l.f = f
	return true
}
