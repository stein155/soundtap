// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package supervise

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/counters"
)

func TestSourceProcessesWaitForStop(t *testing.T) {
	p := NewProcesses()
	release := make(chan struct{})
	p.Register("SPOTIFY", func() { <-release }, nil)
	done := make(chan struct{})
	go func() {
		p.Preempt("SPOTIFY", "")
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("preempt returned before process stopped")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("preempt did not return after process stopped")
	}
}

func TestSourceProcessesPreemptAndKeepOwner(t *testing.T) {
	p := NewProcesses()
	var stopped []string
	p.Register("AIRPLAY", func() { stopped = append(stopped, "AIRPLAY") }, nil)
	p.Register("SPOTIFY", func() { stopped = append(stopped, "SPOTIFY") }, nil)

	p.Preempt("AIRPLAY", "SPOTIFY")
	p.Preempt("SPOTIFY", "SPOTIFY")
	if !slices.Equal(stopped, []string{"AIRPLAY"}) {
		t.Fatalf("stopped = %v, want only AIRPLAY", stopped)
	}
}

func TestSourceProcessesYieldBeforeStop(t *testing.T) {
	p := NewProcesses()
	stopped := 0
	p.Register("SPOTIFY", func() { stopped++ }, nil)
	yields := true
	p.SetYield("SPOTIFY", func() bool { return yields })
	p.Preempt("SPOTIFY", "TUNEIN")
	if stopped != 0 {
		t.Fatal("librespot stopped although it paused")
	}
	yields = false
	p.Preempt("SPOTIFY", "TUNEIN")
	if stopped != 1 {
		t.Fatal("librespot kept running although pausing failed")
	}
}

// A preempt that lands while a receiver's start keeps failing must not end its
// supervisor: that unregistered the receiver for the rest of the daemon's life.
func TestSuperviseSurvivesPreemptDuringStartRetry(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "receiver")
	if err := os.WriteFile(bin, []byte("not an executable format"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	procs := NewProcesses()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), procs, "AIRPLAY", Spec{Name: "receiver", Bin: bin, Args: func() []string { return nil }})
	}()
	registered := func() bool {
		procs.mu.Lock()
		defer procs.mu.Unlock()
		return procs.stops["AIRPLAY"] != nil
	}
	for deadline := time.Now().Add(2 * time.Second); !registered(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("supervisor never registered")
		}
	}
	procs.Restart("AIRPLAY") // lands in the 3 s retry wait after the failed start
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("supervisor exited after a preempt during its start retry")
	default:
	}
	if !registered() {
		t.Fatal("receiver unregistered after a preempt during its start retry")
	}
}

// A follow change restarts snapclient on purpose: the new run must not wait out the
// crash backoff, or joining a group lags by seconds.
func TestSuperviseRestartsAtOnceAfterDeliberateStop(t *testing.T) {
	dir := t.TempDir()
	starts := filepath.Join(dir, "starts")
	bin := filepath.Join(dir, "daemon")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho start >>"+starts+"\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	procs := NewProcesses()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), procs, "daemon", Spec{Name: "daemon", Bin: bin, Args: func() []string { return nil }})
	}()
	defer func() { cancel(); <-done }()
	waitStarts := func(n int, within time.Duration) {
		t.Helper()
		for deadline := time.Now().Add(within); ; time.Sleep(10 * time.Millisecond) {
			b, _ := os.ReadFile(starts)
			if strings.Count(string(b), "start") >= n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d starts not seen within %v", n, within)
			}
		}
	}
	waitStarts(1, 2*time.Second)
	procs.Restart("daemon")
	waitStarts(2, supervisorBackoff/3)
}

// The follower's snapclient died while its group was in standby, so its supervisor waits
// out a crash backoff. A re-follow (restart) or the switch back to MULTIROOM (wake) must
// start it at once, not when the backoff ends. A wake leaves a running one alone.
func TestSuperviseStartsAtOnceFromBackoff(t *testing.T) {
	for _, kick := range []string{"restart", "wake"} {
		t.Run(kick, func(t *testing.T) {
			dir := t.TempDir()
			starts := filepath.Join(dir, "starts")
			bin := filepath.Join(dir, "daemon")
			// The first run dies at once; later runs keep running.
			script := "#!/bin/sh\necho start >>" + starts + "\n[ $(wc -l <" + starts + ") -gt 1 ] || exit 1\nexec sleep 30\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			procs := NewProcesses()
			done := make(chan struct{})
			go func() {
				defer close(done)
				Run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), procs, "daemon", Spec{Name: "daemon", Bin: bin, Args: func() []string { return nil }})
			}()
			// Let the supervisor end before the next test starts one.
			defer func() { cancel(); <-done }()
			count := func() int {
				b, _ := os.ReadFile(starts)
				return strings.Count(string(b), "start")
			}
			waitStarts := func(n int, within time.Duration) {
				t.Helper()
				for deadline := time.Now().Add(within); count() < n; time.Sleep(10 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatalf("%d starts not seen within %v", n, within)
					}
				}
			}
			waitStarts(1, 2*time.Second)
			time.Sleep(500 * time.Millisecond) // the first run has died; the supervisor waits 6 s
			if kick == "restart" {
				procs.Restart("daemon")
			} else {
				procs.Wake("daemon")
			}
			waitStarts(2, supervisorBackoff/3)
			procs.Wake("daemon")
			time.Sleep(200 * time.Millisecond)
			if n := count(); n != 2 {
				t.Fatalf("a wake restarted a running process: %d starts", n)
			}
		})
	}
}

func TestRestartPolicyBacksOffResetsAndGivesUp(t *testing.T) {
	r := restartPolicy{wait: supervisorBackoff}
	for range 10 {
		r.next(time.Second)
	}
	if r.wait != supervisorBackoffMax {
		t.Fatalf("rapid exits: backoff %v, want cap %v", r.wait, supervisorBackoffMax)
	}
	if w, ok := r.next(supervisorStableRun); w != supervisorBackoff || !ok || r.rapid != 0 {
		t.Fatalf("after a stable run: wait %v ok %v rapid %d, want %v true 0", w, ok, r.rapid, supervisorBackoff)
	}
	var total time.Duration
	for i := 1; i < supervisorMaxRapid; i++ {
		w, ok := r.next(0)
		if !ok {
			t.Fatalf("short run %d: resting already", i)
		}
		total += w
	}
	// Bounded, yet long enough for a cause that clears after boot.
	if total < 10*time.Minute || total > 20*time.Minute {
		t.Fatalf("19 short runs span %v", total)
	}
	// The 20th rests, it does not stop for good: an internet outage ends, and librespot
	// exits within seconds of every start until it does.
	if w, ok := r.next(0); ok || w != supervisorRest {
		t.Fatalf("20th short run: wait %v ok %v, want the %v rest", w, ok, supervisorRest)
	}
	if w, ok := r.next(0); !ok || w != supervisorBackoff*2 {
		t.Fatalf("after the rest: wait %v ok %v, want a fresh ladder", w, ok)
	}
}

// /api/health counts a run that ended on its own (ADR 0127), not a deliberate stop.
func TestSuperviseCountsCrashesNotDeliberateStops(t *testing.T) {
	dir := t.TempDir()
	starts := filepath.Join(dir, "starts")
	bin := filepath.Join(dir, "daemon")
	// The first run dies at once; later runs keep running.
	script := "#!/bin/sh\necho start >>" + starts + "\n[ $(wc -l <" + starts + ") -gt 1 ] || exit 1\nexec sleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	name := uniqueName("counted-daemon")
	ctx, cancel := context.WithCancel(context.Background())
	procs := NewProcesses()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), procs, name, Spec{Name: name, Bin: bin, Args: func() []string { return nil }})
	}()
	defer func() { cancel(); <-done }()
	count := func() int {
		b, _ := os.ReadFile(starts)
		return strings.Count(string(b), "start")
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal(what)
			}
		}
	}
	waitFor("the crash was not counted", func() bool { return counters.DaemonRestarts()[name] >= 1 })
	procs.Wake(name) // out of the backoff: the second run keeps running
	waitFor("no second start", func() bool { return count() >= 2 })
	procs.Restart(name)
	waitFor("no start after the deliberate stop", func() bool { return count() >= 3 })
	if n := counters.DaemonRestarts()[name]; n != 1 {
		t.Errorf("%d restarts counted, want the one crash", n)
	}
	if _, ok := counters.CrashLoops()[name]; ok {
		t.Error("one crash recorded as a crash loop")
	}
}

// The 20th short run in a row puts the daemon to rest and records a crash loop, which the
// crash report offers (ADR 0126).
func TestSuperviseRecordsCrashLoop(t *testing.T) {
	name := uniqueName("looping-daemon")
	s := &supervisor{spec: Spec{Name: name}}
	s.counted(true, errors.New("exit status 1"))
	if _, ok := counters.CrashLoops()[name]; ok {
		t.Fatal("a restart recorded as a crash loop")
	}
	s.counted(false, errors.New("exit status 1"))
	loop, ok := counters.CrashLoops()[name]
	if !ok || loop.Err != "exit status 1" || loop.Runs != supervisorMaxRapid || loop.Count != 1 {
		t.Errorf("crash loop %+v, %v", loop, ok)
	}
	if n := counters.DaemonRestarts()[name]; n != 2 {
		t.Errorf("%d restarts, want 2", n)
	}
}

// The counters are the process's: a daemon name of its own per run, so a second run
// (go test -count) starts from zero.
var nameSeq atomic.Int64

func uniqueName(base string) string { return fmt.Sprintf("%s-%d", base, nameSeq.Add(1)) }
