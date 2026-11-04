// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package counters keeps the event counts GET /api/health reports (ADR 0127): restarts,
// underruns and failed writes that otherwise only show as lines in a log that has rotated
// by the time anyone looks. Each is an atomic in memory, so counting costs one atomic add
// and nothing is written to flash. They start over when soundtapd does; SoundtapdRestarts
// is the one count that spans soundtapd's restarts within a boot, and it lives in /run.
package counters

import (
	"maps"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// SoundtapdRestarts is how often the init script started soundtapd again in this
	// boot: soundtapd's starts recorded in /run, less the first.
	SoundtapdRestarts atomic.Uint64
	// BufferUnderruns counts the Mixer's jitter buffer running dry while a source played,
	// each time it fell into silence (internal/audio).
	BufferUnderruns atomic.Uint64
	// ALSAUnderruns counts the xruns aplay reported: the sound card ran out of samples.
	ALSAUnderruns atomic.Uint64
	// InstallFailures counts firmware installs that were admitted and then failed.
	InstallFailures atomic.Uint64
	// BCOFlashFailures counts flashes of the ST20's BCO image that failed.
	BCOFlashFailures atomic.Uint64
	// BootConfirmRetries counts the boot confirmation's looks that found the boot not
	// good yet (ADR 0010): each is another bootConfirmPoll before U-Boot hears of it.
	BootConfirmRetries atomic.Uint64
)

// CrashLoop is a media daemon that ended too soon too often in a row and was put to rest
// (supervise's restartPolicy).
type CrashLoop struct {
	Daemon string    // the supervised process, as supervise names it
	Runs   int       // short runs in a row before the rest
	Err    string    // how the last run ended
	At     time.Time // when it was put to rest
	Count  int       // crash loops of this daemon since soundtapd started
}

var daemons struct {
	mu       sync.Mutex
	restarts map[string]uint64
	loops    map[string]CrashLoop
}

// DaemonRestarted records that the supervisor started name again after it exited on its
// own or failed to start. A deliberate stop (a preempt, a follow change) is not one.
func DaemonRestarted(name string) {
	daemons.mu.Lock()
	defer daemons.mu.Unlock()
	if daemons.restarts == nil {
		daemons.restarts = map[string]uint64{}
	}
	daemons.restarts[name]++
}

// DaemonRestarts is each daemon's restarts, a copy.
func DaemonRestarts() map[string]uint64 {
	daemons.mu.Lock()
	defer daemons.mu.Unlock()
	out := make(map[string]uint64, len(daemons.restarts))
	maps.Copy(out, daemons.restarts)
	return out
}

// DaemonCrashLooped records that name was put to rest after runs short runs in a row, the
// last ending with err.
func DaemonCrashLooped(name string, runs int, err string, at time.Time) {
	daemons.mu.Lock()
	defer daemons.mu.Unlock()
	if daemons.loops == nil {
		daemons.loops = map[string]CrashLoop{}
	}
	n := daemons.loops[name].Count + 1
	daemons.loops[name] = CrashLoop{Daemon: name, Runs: runs, Err: err, At: at, Count: n}
}

// CrashLoops is the last crash loop of each daemon that had one, a copy.
func CrashLoops() map[string]CrashLoop {
	daemons.mu.Lock()
	defer daemons.mu.Unlock()
	out := make(map[string]CrashLoop, len(daemons.loops))
	maps.Copy(out, daemons.loops)
	return out
}

// Snapshot is every count at one moment, as GET /api/health reports them.
type Snapshot struct {
	SoundtapdRestarts  uint64            `json:"soundtapdRestarts"`
	DaemonRestarts     map[string]uint64 `json:"daemonRestarts"`
	BufferUnderruns    uint64            `json:"bufferUnderruns"`
	ALSAUnderruns      uint64            `json:"alsaUnderruns"`
	InstallFailures    uint64            `json:"installFailures"`
	BCOFlashFailures   uint64            `json:"bcoFlashFailures"`
	BootConfirmRetries uint64            `json:"bootConfirmRetries"`
}

// Take reads every count.
func Take() Snapshot {
	return Snapshot{
		SoundtapdRestarts:  SoundtapdRestarts.Load(),
		DaemonRestarts:     DaemonRestarts(),
		BufferUnderruns:    BufferUnderruns.Load(),
		ALSAUnderruns:      ALSAUnderruns.Load(),
		InstallFailures:    InstallFailures.Load(),
		BCOFlashFailures:   BCOFlashFailures.Load(),
		BootConfirmRetries: BootConfirmRetries.Load(),
	}
}

// Reset sets every count to zero, for a test.
func Reset() {
	for _, c := range []*atomic.Uint64{&SoundtapdRestarts, &BufferUnderruns, &ALSAUnderruns,
		&InstallFailures, &BCOFlashFailures, &BootConfirmRetries} {
		c.Store(0)
	}
	daemons.mu.Lock()
	daemons.restarts, daemons.loops = nil, nil
	daemons.mu.Unlock()
}
