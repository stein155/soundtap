// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// snapserver runs below the Mixer and aplay (a pop on an ST20 multiroom leader, 2026-11-24).
func TestSnapserverRunsNiced(t *testing.T) {
	t.Parallel()

	got := snapserverArgs("/run/soundtap/snapserver.conf")
	want := []string{"-n", mediaNice, "snapserver", "-c", "/run/soundtap/snapserver.conf"}

	if !slices.Equal(got, want) {
		t.Fatalf("snapserver command %v, want %v", got, want)
	}
}

// maxNice is the lowest priority there is: nice stops there.
const maxNice = 19

// niceOf is the niceness of process pid, as ps reports it.
func niceOf(t *testing.T, pid int) int {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "ps", "-o", "nice=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Skipf("ps cannot report a niceness here: %v", err)
	}

	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Skipf("ps reported no niceness: %q", out)
	}

	return n
}

// librespot runs below the Mixer and aplay, as the decoder and snapserver do: its decode
// bursts after every track boundary took the ST20's CPU from aplay (2026-11-28). The command
// line is run against a stand-in librespot that reports its niceness, its umask and its
// arguments: nice and sh exec it with the umask that keeps its credentials private, and the
// speaker's name stays one argument.
func TestLibrespotRunsNicedWithPrivateUmask(t *testing.T) {
	t.Parallel()

	nice, err := exec.LookPath("nice")
	if err != nil {
		t.Skip("no nice here")
	}

	dir := t.TempDir()
	stub := "#!/bin/sh\nps -o nice= -p $$\numask\nprintf '%s\\n' \"$@\"\n"

	err = os.WriteFile(filepath.Join(dir, "librespot"), []byte(stub), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	name := `Kitchen "Main"; reboot`
	cmd := exec.CommandContext(t.Context(), nice, librespotCommand(name, 10)...)

	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("librespot's command line failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 3 {
		t.Fatalf("stand-in output %q", out)
	}

	step, _ := strconv.Atoi(mediaNice)
	if got, want := strings.TrimSpace(lines[0]), strconv.Itoa(min(niceOf(t, os.Getpid())+step, maxNice)); got != want {
		t.Errorf("librespot ran at nice %s, want %s", got, want)
	}

	if umask := strings.TrimSpace(lines[1]); strings.TrimLeft(umask, "0") != "77" {
		t.Errorf("librespot ran with umask %s, want 077", umask)
	}

	if !slices.Equal(lines[2:], librespotArgs(name, 10)) {
		t.Errorf("librespot got arguments %q, want %q", lines[2:], librespotArgs(name, 10))
	}

	if step <= 0 {
		t.Fatal("librespot is not niced")
	}
}
