// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/counters"
)

// longTraceFrames is a trace too long for any link.
const longTraceFrames = 2000

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, body := range files {
		path := filepath.Join(dir, name)

		err := os.MkdirAll(filepath.Dir(path), 0o700)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(path, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A long trace is cut from its end to keep the link within the limit, and says so; a short
// one goes whole. The body the link carries is the body returned.
func TestCrashIssueFitsTheLink(t *testing.T) {
	t.Parallel()

	head := crashHead("soundtapd stopped with a panic.", "panic-0123456789ab", crashFacts{
		app:    "0.1.0",
		model:  "SoundTouch 10",
		uptime: "1h",
		core:   "",
	})

	long := make([]string, 0, longTraceFrames)
	for i := range longTraceFrames {
		long = append(long, fmt.Sprintf("main.frame%d(0x%x)\n\t/src/app/cmd/soundtapd/file.go:%d +0x1c", i, i, i))
	}

	body, link := crashIssue("Crash: soundtapd panic", head, strings.Join(long, "\n"))
	if len(link) > crashMaxURL {
		t.Fatalf("link is %d bytes, limit %d", len(link), crashMaxURL)
	}

	if len(link) < crashMaxURL-400 {
		t.Errorf("link is %d bytes: the cut leaves room unused", len(link))
	}

	if !strings.Contains(body, crashCut) ||
		!strings.Contains(body, "main.frame0(0x0)") ||
		strings.Contains(body, "main.frame1999") {
		t.Errorf("the trace is not cut from its end:\n%s", body)
	}

	u, err := url.Parse(link)
	if err != nil || u.Query().Get("body") != body || u.Query().Get("title") != "Crash: soundtapd panic" {
		t.Fatalf("the link does not carry the body: %v", err)
	}

	body, _ = crashIssue("Crash: x", head, "short trace")
	if strings.Contains(body, crashCut) || !strings.Contains(body, "```text\nshort trace\n```") {
		t.Errorf("a short trace was cut:\n%s", body)
	}
	// Backticks in the trace cannot close the code block early.
	body, _ = crashIssue("Crash: x", head, "before\n```\nafter")
	if !strings.Contains(body, "````text\nbefore\n```\nafter\n````") {
		t.Errorf("the fence does not outlast the trace's backticks:\n%s", body)
	}
	// One very long line cannot take the whole link.
	body, link = crashIssue("Crash: x", head, strings.Repeat("x", 20000)+"\nnext")
	if len(link) > crashMaxURL || !strings.Contains(body, "next") || !strings.Contains(body, "x…") {
		t.Errorf("a long line was not shortened: %d bytes", len(link))
	}
}

// panic.log as soundtapd's writePanic writes it.
func TestPanicCrashReadsWritePanicsFormat(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), PanicLogName)
	text := "2026-11-21T09:00:00Z goroutine \"audioMix\": panic: " +
		"runtime error: index out of range [3] with length 3\n\n" +
		"goroutine 42 [running]:\nmain.mix(...)\n\t/src/app/cmd/soundtapd/audio_mix.go:12 +0x1c\n"
	writeFiles(t, filepath.Dir(path), map[string]string{PanicLogName: text})

	found, ok := panicCrash(path)
	if !ok || found.kind != crashPanic || !strings.HasPrefix(found.id, "panic-") || len(found.id) != len("panic-")+12 {
		t.Fatalf("crash %+v, %v", found, ok)
	}

	if found.title != "soundtapd panic: runtime error: index out of range [3] with length 3" ||
		found.what != `soundtapd stopped with a panic in goroutine "audioMix" at 2026-11-21T09:00:00Z.` {
		t.Errorf("title %q, what %q", found.title, found.what)
	}

	if again, _ := panicCrash(path); again.id != found.id {
		t.Error("the same panic got another id")
	}

	writeFiles(t, filepath.Dir(path), map[string]string{PanicLogName: strings.Replace(text, "[3]", "[4]", 1)})

	if other, _ := panicCrash(path); other.id == found.id {
		t.Error("another panic kept the id")
	}

	writeFiles(t, filepath.Dir(path), map[string]string{PanicLogName: "\n"})

	if _, ok := panicCrash(path); ok {
		t.Error("an empty panic.log is a crash")
	}
}

// A previous boot that ended in a panic or oops is a crash; the oops is its cause and the
// title names the kernel's own line. Any other end is not one.
func TestKernelCrashReadsTheRamoopsRecords(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	oops := "Oops#1 Part1\n<6>[   10.000000] wlcore: loaded\n<1>[   12.345678] " +
		"Unable to handle kernel NULL pointer dereference at virtual address 00000000\n" +
		"<4>[   12.345700] PC is at wl1271_irq+0x10/0x200 [wlcore]\n<4>[   12.345800] Backtrace:\n"
	writeFiles(t, dir, map[string]string{
		"how":             "oops\n",
		"dmesg-ramoops-0": "Panic#1 Part1\n<0>[   12.400000] Kernel panic - not syncing: Fatal exception\n",
		"dmesg-ramoops-1": oops,
	})

	found, ok := kernelCrash(dir)
	if !ok || found.kind != crashKernel {
		t.Fatalf("crash %+v, %v", found, ok)
	}

	if found.title != "kernel oops: Unable to handle kernel NULL pointer dereference at virtual address 00000000" {
		t.Errorf("title %q", found.title)
	}

	if !strings.Contains(found.trace, "PC is at wl1271_irq") || strings.Contains(found.trace, "Fatal exception") {
		t.Errorf("the trace is not the oops record:\n%s", found.trace)
	}

	for _, how := range []string{"reboot", "power loss", "reset without a reboot (watchdog or hang)"} {
		writeFiles(t, dir, map[string]string{"how": how + "\n"})

		if _, ok := kernelCrash(dir); ok {
			t.Errorf("a boot that ended in %q is a crash", how)
		}
	}

	if _, ok := kernelCrash(filepath.Join(dir, "missing")); ok {
		t.Error("no records is a crash")
	}
}

// A crash loop is named by its daemon and the boot: dismissed once, not offered again in
// that boot; in another boot it is a new crash.
func TestDaemonCrashesQuoteTheirLogLines(t *testing.T) {
	t.Parallel()

	loops := map[string]counters.CrashLoop{
		"shairport-sync": {Daemon: "shairport-sync", Runs: 20, Err: "exit status 2", At: time.Unix(0, 0), Count: 1},
		"librespot":      {Daemon: "librespot", Runs: 20, Err: "signal: killed", At: time.Unix(0, 0), Count: 3},
	}
	log := "level=INFO msg=start bin=librespot\nlevel=WARN msg=\"exited, " +
		"restarting\" bin=librespot err=\"exit status 1\"\nunrelated\n"

	got := daemonCrashes(loops, "boot-a", log)
	if len(got) != 2 || got[0].title != "librespot keeps crashing" || got[1].title != "shairport-sync keeps crashing" {
		t.Fatalf("crashes %+v", got)
	}

	if !strings.Contains(got[0].trace, "bin=librespot err=") || strings.Contains(got[0].trace, "unrelated") ||
		!strings.Contains(got[0].trace, "signal: killed") || !strings.Contains(got[0].what, "crash loop 3") {
		t.Errorf("trace %q, what %q", got[0].trace, got[0].what)
	}

	if again := daemonCrashes(loops, "boot-a", ""); again[0].id != got[0].id {
		t.Error("the same loop in the same boot got another id")
	}

	if later := daemonCrashes(loops, "boot-b", ""); later[0].id == got[0].id {
		t.Error("a loop in another boot kept the id")
	}
}

func TestTruncateBytesKeepsRunesWhole(t *testing.T) {
	t.Parallel()

	for _, s := range []string{strings.Repeat("é", 100), strings.Repeat("a", 100), "short"} {
		got := truncateBytes(s, 21)
		if len(got) > 21 || !utf8.ValidString(got) {
			t.Errorf("truncateBytes(%q) = %q", s[:10], got)
		}
	}

	if truncateBytes("short", 21) != "short" {
		t.Error("a short string changed")
	}
}
