// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package supervise runs the media daemons soundtapd owns (ADR 0121): one supervisor per
// process with one restart rule for all of them, their output capped, and per source the
// hooks that stop, wake or yield the process that plays it.
package supervise

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/childproc"
	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/safego"
)

// Processes holds, per source, the hooks of the process that plays it: how to stop it,
// wake it from a crash backoff, or have it give up the amplifier without being stopped.
type Processes struct {
	mu     sync.Mutex
	stops  map[string]func()
	wakes  map[string]func()
	yields map[string]func() bool
}

// NewProcesses is an empty set of hooks.
func NewProcesses() *Processes {
	return &Processes{
		mu:    sync.Mutex{},
		stops: make(map[string]func()), wakes: make(map[string]func()),
		yields: make(map[string]func() bool),
	}
}

// Preempt synchronously stops the process that owned the amp before keep took over.
// Only the previous owner can be writing; stopping every other daemon too restarted
// shairport-sync on each source change. A source with a yield hook (librespot pauses
// over its control API) gives up the amp without dying; if the hook fails, it is
// stopped. Waiting for the child to exit guarantees its ALSA writer is gone before the
// next source starts.
func (p *Processes) Preempt(previous, keep string) {
	if previous == keep {
		return
	}

	p.mu.Lock()
	stop, yield := p.stops[previous], p.yields[previous]
	p.mu.Unlock()

	if stop == nil || (yield != nil && yield()) {
		return
	}

	stop()
}

// SetYield registers how source gives up the amp without being stopped.
func (p *Processes) SetYield(source string, fn func() bool) {
	p.mu.Lock()
	p.yields[source] = fn
	p.mu.Unlock()
}

// Restart stops one source's process; its supervisor starts it again at once.
func (p *Processes) Restart(source string) {
	p.mu.Lock()
	stop := p.stops[source]
	p.mu.Unlock()

	if stop != nil {
		stop()
	}
}

// Wake starts a source's process at once if its supervisor is waiting out a crash
// backoff, and resets that backoff. A running process is left alone. It never blocks.
func (p *Processes) Wake(source string) {
	p.mu.Lock()
	wake := p.wakes[source]
	p.mu.Unlock()

	if wake != nil {
		wake()
	}
}

// Register sets source's stop and wake hooks and returns what removes them.
func (p *Processes) Register(source string, stop, wake func()) func() {
	p.mu.Lock()
	p.stops[source], p.wakes[source] = stop, wake
	p.mu.Unlock()

	return func() {
		p.mu.Lock()
		delete(p.stops, source)
		delete(p.wakes, source)
		p.mu.Unlock()
	}
}

// Spec says how one daemon runs.
type Spec struct {
	Name string          // what it is logged as, its output included
	Bin  string          // the binary to start
	Args func() []string // read at every start, crash restarts included
	Env  []string        // added to soundtapd's environment
	// User is the system user it runs as (ADR 0066); "" keeps root. A daemon whose user
	// is missing does not start: running it as root instead would quietly undo the
	// separation.
	User string
	Caps []uintptr // capabilities it keeps after dropping root, as ambient ones
}

// Run runs spec for the life of ctx as the process of source in processes, restarting it
// on exit by restartPolicy. A missing binary is logged once and not retried.
//
//nolint:gocritic // hugeParam: every caller passes its Spec by value; Run keeps its own copy
func Run(ctx context.Context, log *slog.Logger, processes *Processes, source string, spec Spec) {
	_, err := exec.LookPath(spec.Bin)
	if err != nil {
		log.Warn("not installed, skipping", "bin", spec.Bin)

		return
	}

	err = childproc.RunAs(exec.CommandContext(ctx, spec.Bin), spec.User)
	if spec.User != "" && err != nil {
		log.Error("no system user, not starting", "bin", spec.Name, "user", spec.User, "err", err)

		return
	}

	sup := &supervisor{
		log: log, spec: spec, out: ChildLog(spec.Name),
		stop: make(chan chan struct{}), wake: make(chan struct{}), restarts: freshRestarts(),
	}
	stopped := make(chan struct{})

	unregister := processes.Register(source, func() {
		done := make(chan struct{})
		select {
		case sup.stop <- done:
			<-done
		case <-stopped:
		case <-ctx.Done():
		}
	}, func() {
		select {
		case sup.wake <- struct{}{}: // taken only while waiting out a backoff
		default:
		}
	})
	defer unregister()
	defer close(stopped)

	for ctx.Err() == nil {
		wait, ok := sup.runOnce(ctx)
		if !ok {
			return
		}

		if wait >= 0 && !sup.rest(ctx, wait) {
			return
		}
	}
}

// supervisor is one Run: the process it runs, the hooks that stop and wake it, and its
// restart state.
type supervisor struct {
	log      *slog.Logger
	spec     Spec
	out      io.Writer
	stop     chan chan struct{}
	wake     chan struct{}
	restarts restartPolicy
}

// runOnce starts the process and waits for it. It returns how long to rest before the
// next start, -1 to start again at once, and false once ctx has ended.
func (s *supervisor) runOnce(ctx context.Context) (time.Duration, bool) {
	name := s.spec.Name
	cmd := exec.CommandContext(ctx, s.spec.Bin, s.spec.Args()...)
	cmd.Env = append(os.Environ(), s.spec.Env...)
	childproc.DieWithParent(cmd)

	if s.spec.User != "" {
		_ = childproc.RunAs(cmd, s.spec.User) // checked before the loop
		childproc.KeepCaps(cmd, s.spec.Caps)
	}

	cmd.Stdout, cmd.Stderr = s.out, s.out
	s.log.Info("start", "bin", name)

	err := cmd.Start()
	if err != nil {
		// Retry rather than return: returning ran the deferred unregister, so one
		// transient fork failure under memory pressure silently lost the receiver
		// for the rest of the daemon's life — and left preempt unaware of it.
		// A start that keeps failing counts like a crash, or it was retried forever.
		wait, again := s.restarts.next(0)
		s.restarted()

		if again {
			s.log.Warn("start failed, retrying", "bin", name, "err", err, "in", wait)
		} else {
			s.crashLooped(err)
			s.log.Error("start failed too often in a row, resting", "bin", name, "err", err, "in", wait)
		}

		return wait, true
	}

	started := time.Now()
	exited := make(chan error, 1)

	safego.Go(name+" wait", func() { exited <- cmd.Wait() })

	select {
	case err := <-exited:
		wait, again := s.restarts.next(time.Since(started))

		if ctx.Err() != nil {
			return 0, false
		}

		s.restarted()

		if !again {
			s.crashLooped(err)
			s.log.Error("exited too often in a row, resting",
				"bin", name, "err", err, "short runs", supervisorMaxRapid, "in", wait)
		} else if err != nil {
			s.log.Warn("exited, restarting", "bin", name, "err", err, "in", wait)
		}

		return wait, true
	case done := <-s.stop:
		// A deliberate stop (a follow change, a preempt) starts the next run at once
		// and forgets earlier crashes: only an unexpected exit waits out the backoff.
		_ = cmd.Process.Kill()

		s.log.Debug("stopped", "bin", name, "err", <-exited)
		close(done)

		s.restarts = freshRestarts()

		return -1, true
	case <-ctx.Done():
		_ = cmd.Process.Kill()

		s.log.Debug("stopped", "bin", name, "err", <-exited)

		return 0, false
	}
}

// restarted records an exit or a failed start in the counters /api/health reports (ADR
// 0127).
func (s *supervisor) restarted() {
	counters.DaemonRestarted(s.spec.Name)
}

// crashLooped records a crash loop when the process is put to rest, after restarted
// counted the run that ended it.
func (s *supervisor) crashLooped(err error) {
	why := "exited"
	if err != nil {
		why = err.Error()
	}

	counters.DaemonCrashLooped(s.spec.Name, supervisorMaxRapid, why, time.Now())
}

// rest waits wait before the next start. A stop or a wake ends it early and forgets
// earlier crashes. It reports false once ctx has ended.
func (s *supervisor) rest(ctx context.Context, wait time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case done := <-s.stop:
		// Nothing runs to stop: the process exited, or its start failed. A preempt must
		// not end the supervisor either.
		close(done)

		s.restarts = freshRestarts()
	case <-s.wake:
		s.restarts = freshRestarts()
	case <-time.After(wait):
	}

	return true
}

// A process that keeps crashing is restarted ever more slowly, up to a minute apart,
// instead of burning a fork and a codec load every 3 s on a 32-bit speaker. One run that
// lasted a while resets the count. After 20 short runs in a row it rests for
// supervisorRest, then the count starts over: an unbounded fast restart loop on a 128 MB
// board with no swap is a fork bomb (ADR 0121), but staying off for good lost Spotify
// Connect to any internet outage longer than the 16 minutes 20 short runs span, because
// librespot with stored credentials exits within seconds when it cannot reach Spotify.
const (
	supervisorBackoff    = 3 * time.Second
	supervisorBackoffMax = time.Minute
	supervisorStableRun  = 30 * time.Second
	supervisorMaxRapid   = 20
	supervisorRest       = 10 * time.Minute
	// supervisorBackoffGrowth multiplies the wait after every short run.
	supervisorBackoffGrowth = 2
)

// restartPolicy is the one restart rule for every process soundtapd supervises.
type restartPolicy struct {
	wait  time.Duration // before the next start
	rapid int           // short runs in a row
}

// freshRestarts is the restart state of a process with no crashes behind it.
func freshRestarts() restartPolicy {
	return restartPolicy{wait: supervisorBackoff, rapid: 0}
}

// next records a run that lasted ran and returns the wait before the next start; false
// means the process ended too soon too often in a row and the wait is the long rest,
// after which the count starts over.
func (r *restartPolicy) next(ran time.Duration) (time.Duration, bool) {
	if ran >= supervisorStableRun {
		r.wait, r.rapid = supervisorBackoff, 0

		return r.wait, true
	}

	r.wait = min(r.wait*supervisorBackoffGrowth, supervisorBackoffMax)

	r.rapid++
	if r.rapid >= supervisorMaxRapid {
		r.wait, r.rapid = supervisorBackoff, 0

		return supervisorRest, false
	}

	return r.wait, true
}

// RunWhileWanted runs run while wanted holds, and again whenever a poke finds it holding
// anew. A poke that finds it false ends run's context; run must then return, and parked
// is called. A run that returns on its own (a binary that is not installed) is not
// started again.
func RunWhileWanted(
	ctx context.Context, wanted func() bool, poke <-chan struct{}, run func(context.Context), parked func(),
) {
	for {
		for !wanted() {
			select {
			case <-ctx.Done():
				return
			case <-poke:
			}
		}

		if !runWhile(ctx, wanted, poke, run) {
			return
		}

		if parked != nil {
			parked()
		}
	}
}

// runWhile runs run until a poke finds wanted false, and reports whether RunWhileWanted
// then waits for it again: false once ctx ended, or when run returned on its own.
func runWhile(ctx context.Context, wanted func() bool, poke <-chan struct{}, run func(context.Context)) bool {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan struct{})

	safego.Go("runWhileWanted", func() {
		defer close(done)

		run(runCtx)
	})

	for {
		select {
		case <-done:
			return false
		case <-poke:
			if wanted() {
				continue
			}

			cancel()
			<-done

			return ctx.Err() == nil
		case <-ctx.Done():
			cancel()
			<-done

			return false
		}
	}
}
