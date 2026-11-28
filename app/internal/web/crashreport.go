// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/settings"
)

const (
	// crashLogTail is how much of the end of a log a crash report carries.
	crashLogTail = 64 << 10
	// crashInfoTimeout bounds the backend reads a crash report adds.
	crashInfoTimeout = 2 * time.Second
)

// Crash reports (ADR 0126). With the owner's opt-in, GET /api/crash-report finds a crash on
// the speaker and writes it up as a GitHub issue the owner opens, reads and submits; the
// speaker sends nothing anywhere. A crash once reported or dismissed is not offered again.
const (
	// crashIssueURL is where the owner files the report.
	crashIssueURL = "https://github.com/stein155/soundtap/issues/new"
	// crashMaxURL bounds the issue link. Browsers and GitHub refuse or cut much longer
	// ones (around 8 KB); what does not fit is in the support bundle the owner attaches.
	crashMaxURL = 6000
	// crashMaxTitle bounds the issue title, in bytes.
	crashMaxTitle = 120
	// crashMaxLine bounds one line of the trace, so one long line cannot take the link.
	crashMaxLine = 240
	// crashKernelLines is how much of a kernel panic record the report quotes: its end,
	// where the oops, the registers and the call trace are.
	crashKernelLines = 40
	// crashDaemonLines is how many of the agent log's lines about a looping daemon it quotes.
	crashDaemonLines = 15
	// crashUnknown stands in for a fact the speaker could not read.
	crashUnknown = "unknown"
)

// The kinds of crash, in the order the speaker offers them.
const (
	crashPanic  = "panic"  // soundtapd's own, from panic.log
	crashKernel = "kernel" // the previous boot ended in a kernel panic or oops (ramoops)
	crashDaemon = "daemon" // a supervised media daemon crash-looped and was put to rest
)

// crash is one crash found on the speaker, before redaction and before it is written up.
type crash struct {
	id, kind string
	title    string // the issue title, without the "Crash: " prefix
	what     string // one sentence: what happened
	trace    string // what the report quotes
}

// crashReport is a crash written up, as GET /api/crash-report returns it: the exact title
// and body the issue link carries, which the UI shows before anything leaves the browser.
type crashReport struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

// crashReportAnswer is GET /api/crash-report: whether the owner turned reports on, and the
// first crash not yet reported or dismissed. crash is null when there is none, and always
// while reports are off, when the speaker does not look.
type crashReportAnswer struct {
	Enabled bool         `json:"enabled"`
	Crash   *crashReport `json:"crash"`
}

func (s *Server) getCrashReport(w http.ResponseWriter, r *http.Request) {
	cr := s.settings.Get().CrashReports

	out := crashReportAnswer{Enabled: cr.Enabled, Crash: nil}
	if cr.Enabled {
		if c, ok := firstUnhandled(s.findCrashes(), cr); ok {
			rep := s.writeCrashReport(r.Context(), &c)
			out.Crash = &rep
		}
	}

	writeJSON(w, http.StatusOK, out)
}

// firstUnhandled is the first crash the owner has neither reported nor dismissed.
func firstUnhandled(crashes []crash, cr settings.CrashReports) (crash, bool) {
	for _, c := range crashes {
		if !cr.IsHandled(c.id) {
			return c, true
		}
	}

	return crash{id: "", kind: "", title: "", what: "", trace: ""}, false
}

// crashOutcomes are what the owner did with an offered crash. Both end the offer.
func crashOutcomes() []string { return []string{"reported", "dismissed"} }

// postCrashReport records that the owner reported or dismissed a crash the speaker holds.
func (s *Server) postCrashReport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Outcome string `json:"outcome"`
	}

	err := decodeJSONStrict(w, r, tinyBody, &body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	if !slices.Contains(crashOutcomes(), body.Outcome) {
		s.badRequest(w, "outcome must be reported or dismissed", nil)

		return
	}
	// Only a crash the speaker holds: a made-up id must not push real ones off the list.
	if !slices.ContainsFunc(s.findCrashes(), func(c crash) bool { return c.id == body.ID }) {
		apiError(w, http.StatusNotFound, "no such crash on this speaker")

		return
	}

	err = s.settings.Update(func(next *settings.Settings) error {
		next.CrashReports.Handle(body.ID)

		return nil
	})
	if err != nil {
		s.fail(w, "save settings failed", err)

		return
	}

	s.log.Info("crash report", "id", body.ID, "outcome", body.Outcome)
	writeOK(w)
}

// findCrashes lists the crashes the speaker holds: soundtapd's last panic, a kernel panic
// or oops that ended the previous boot, and the media daemons that crash-looped.
func (s *Server) findCrashes() []crash {
	var out []crash
	if c, ok := panicCrash(filepath.Join(s.homeDir, PanicLogName)); ok {
		out = append(out, c)
	}

	if c, ok := kernelCrash(filepath.Join(s.homeDir, LastBootDir)); ok {
		out = append(out, c)
	}

	bootID, _ := os.ReadFile(sysPath(bootIDFile))

	return append(out, daemonCrashes(counters.CrashLoops(), strings.TrimSpace(string(bootID)), s.agentLog())...)
}

// crashID names a crash by its kind and its content, so the same crash keeps its id
// across restarts and a new one gets a new id.
func crashID(kind string, parts ...string) string {
	hash := sha256.New()
	for _, p := range parts {
		hash.Write([]byte(p))
		hash.Write([]byte{0})
	}

	return kind + "-" + hex.EncodeToString(hash.Sum(nil))[:12]
}

// panicHead is panic.log's first line, as writePanic in soundtapd writes it.
var panicHead = regexp.MustCompile(`^(\S+) goroutine "([^"]*)": panic: (.*)$`)

// panicCrash reads soundtapd's last panic.
func panicCrash(path string) (crash, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return crash{id: "", kind: "", title: "", what: "", trace: ""}, false
	}

	text := strings.TrimRight(string(raw), "\n")
	report := crash{
		id: crashID(crashPanic, text), kind: crashPanic, title: "soundtapd panic",
		what: "soundtapd stopped with a panic.", trace: text,
	}

	first, _, _ := strings.Cut(text, "\n")
	if m := panicHead.FindStringSubmatch(first); m != nil {
		report.title = "soundtapd panic: " + m[3]
		report.what = fmt.Sprintf("soundtapd stopped with a panic in goroutine %q at %s.", m[2], m[1])
	}

	return report, true
}

// kernelMarkers are the lines that name a kernel crash, the most telling first.
func kernelMarkers() []string {
	return []string{"Kernel panic - not syncing:", "Unable to handle", "Internal error:", "BUG:"}
}

// kernelPrefix is a kernel log line's level and timestamp: "<4>[  123.456789] ".
var kernelPrefix = regexp.MustCompile(`^(<\d+>)?\[\s*\d+\.\d+\]\s*`)

// kernelCrash reads a previous boot that ended in a kernel panic or oops: lastboot.go keeps
// its ramoops records in dir and how it ended in dir/how.
func kernelCrash(dir string) (crash, bool) {
	how := readFileLine(filepath.Join(dir, "how"))
	if how != "panic" && how != "oops" {
		return crash{id: "", kind: "", title: "", what: "", trace: ""}, false
	}

	names, _ := filepath.Glob(filepath.Join(dir, "dmesg-ramoops-*"))
	slices.Sort(names)

	var records []string

	for _, name := range names {
		b, err := os.ReadFile(name)
		if err == nil {
			records = append(records, string(b))
		}
	}

	if len(records) == 0 {
		return crash{id: "", kind: "", title: "", what: "", trace: ""}, false
	}
	// The oops is the cause when there is one: an oops panics (CONFIG_PANIC_ON_OOPS) and
	// leaves a panic record beside its own.
	record := records[0]
	for _, r := range records {
		if strings.HasPrefix(r, "Oops") {
			record = r

			break
		}
	}

	lines := strings.Split(strings.TrimRight(record, "\n"), "\n")

	report := crash{
		id: crashID(crashKernel, records...), kind: crashKernel, title: "kernel " + how + " in the previous boot",
		what:  fmt.Sprintf("The previous boot ended in a kernel %s (kept in ramoops).", how),
		trace: strings.Join(lines[max(0, len(lines)-crashKernelLines):], "\n"),
	}
	for _, marker := range kernelMarkers() {
		if i := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, marker) }); i >= 0 {
			report.title = "kernel " + how + ": " + kernelPrefix.ReplaceAllString(lines[i], "")

			break
		}
	}

	return report, true
}

// daemonCrashes writes up the media daemons that crash-looped in this soundtapd's life. The
// id holds the boot, so a loop dismissed in this boot is not offered again after a restart
// of soundtapd, and one in a later boot is. log is the agent log's tail, for the lines that
// name the daemon.
func daemonCrashes(loops map[string]counters.CrashLoop, bootID, log string) []crash {
	names := make([]string, 0, len(loops))
	for name := range loops {
		names = append(names, name)
	}

	slices.Sort(names)

	out := make([]crash, 0, len(names))
	for _, name := range names {
		loop := loops[name]

		var about []string

		for line := range strings.SplitSeq(log, "\n") {
			if strings.Contains(line, name) {
				about = append(about, line)
			}
		}

		trace := fmt.Sprintf("%s ended soon after its start %d times in a row and was put to rest at %s.\n"+
			"The last run ended with: %s",
			name, loop.Runs, loop.At.UTC().Format(time.RFC3339), loop.Err)

		if len(about) > 0 {
			last := about[max(0, len(about)-crashDaemonLines):]
			trace += "\n\nThe agent log's last lines about it:\n" + strings.Join(last, "\n")
		}

		out = append(out, crash{
			id: crashID(crashDaemon, bootID, name), kind: crashDaemon, title: name + " keeps crashing",
			what: fmt.Sprintf("The media daemon %s kept crashing and soundtapd put it to rest "+
				"(crash loop %d since soundtapd started).", name, loop.Count),
			trace: trace,
		})
	}

	return out
}

// agentLog is the end of soundtapd's own log, where the supervisor writes its lines.
func (s *Server) agentLog() string {
	for _, lg := range s.logPaths() {
		if lg.path == "/tmp/soundtap.log" {
			return tailFile(lg.file, crashLogTail)
		}
	}

	return ""
}

// writeCrashReport writes c up as the issue: the title, and a body with what happened, the
// versions, the model and the uptime, and the trace as far as the link has room. Everything
// from the crash itself goes through the support bundle's redactor first.
func (s *Server) writeCrashReport(ctx context.Context, report *crash) crashReport {
	red := s.newRedactor(ctx)
	model := crashUnknown

	ictx, cancel := context.WithTimeout(ctx, crashInfoTimeout)
	defer cancel()

	info, err := s.backend.Info(ictx)
	if err == nil && info != nil && info.Type != "" {
		model = info.Type
	}

	osVersion, osCommit := osBuild()
	facts := crashFacts{
		app: s.version, core: strings.TrimSpace(osVersion + " " + osCommit), model: model,
		uptime: fmt.Sprintf("speaker %s, soundtapd %s",
			systemUptime("/proc"), time.Since(s.startedAt).Round(time.Second)),
	}
	title := truncateBytes("Crash: "+red.scrub(report.title), crashMaxTitle)
	body, link := crashIssue(title, crashHead(red.scrub(report.what), report.id, facts), red.scrub(report.trace))

	return crashReport{ID: report.id, Kind: report.kind, Title: title, Body: body, URL: link}
}

// crashFacts are what the report says about the speaker.
type crashFacts struct{ app, core, model, uptime string }

// crashHead is the body above the trace.
func crashHead(what, crashID string, facts crashFacts) string {
	if facts.core == "" {
		facts.core = "not a SoundTap image"
	}

	return fmt.Sprintf("**What happened:** %s\n\n"+
		"- SoundTap app: %s\n- SoundTap Core: %s\n- Model: %s\n- Uptime: %s\n- Crash id: %s\n\n",
		what, facts.app, facts.core, facts.model, facts.uptime, crashID)
}

// crashFoot is the body below the trace: where the rest is.
const crashFoot = "**Support bundle:** please attach it to this issue. In the SoundTap app open Settings, " +
	"this speaker, Advanced Setup, System, and choose Support bundle; the speaker asks for a button press. " +
	"It holds the full logs, redacted the same way as this text.\n\n" +
	"_The owner opened this report from the speaker's web page. The speaker sent nothing by itself._\n"

// crashCut marks a trace cut to fit the link.
const crashCut = "[cut to fit the link: the support bundle has all of it]"

// crashIssue builds the body and the issue link, quoting as much of trace, from its start,
// as keeps the link within crashMaxURL.
// It returns the issue body, then the link.
func crashIssue(title, head, trace string) (string, string) {
	lines := strings.Split(trace, "\n")
	for i, l := range lines {
		lines[i] = truncateBytes(l, crashMaxLine)
	}

	build := func(n int) (string, string) {
		quoted := strings.Join(lines[:n], "\n")
		if n < len(lines) {
			quoted += "\n" + crashCut
		}
		// A fence longer than any run of backticks in the trace, so it cannot close early.
		fence := "```"
		for strings.Contains(quoted, fence) {
			fence += "`"
		}

		b := head + fence + "text\n" + quoted + "\n" + fence + "\n\n" + crashFoot

		return b, crashIssueURL + "?" + url.Values{"title": {title}, "body": {b}}.Encode()
	}
	// The link grows with every line, so the most lines that fit is a binary search.
	tooLong := sort.Search(len(lines)+1, func(n int) bool {
		_, link := build(n)

		return len(link) > crashMaxURL
	})

	return build(max(tooLong-1, 0))
}

// truncateBytes cuts s to at most n bytes on a rune boundary, marking the cut with "…".
func truncateBytes(text string, n int) string {
	if len(text) <= n {
		return text
	}

	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}

	return text[:cut] + "…"
}

// systemUptime is how long the speaker has been up, from proc's uptime; "unknown" off Linux.
func systemUptime(proc string) string {
	b, err := os.ReadFile(filepath.Join(proc, "uptime"))
	if err != nil {
		return crashUnknown
	}

	f := strings.Fields(string(b))
	if len(f) == 0 {
		return crashUnknown
	}

	secs, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return crashUnknown
	}

	return (time.Duration(secs) * time.Second).String()
}
