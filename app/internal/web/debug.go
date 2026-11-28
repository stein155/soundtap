// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// debugLogTail is how much of the end of each log the debug page shows.
	debugLogTail = 32 << 10
	// debugLargestEntries is how many of the largest entries under /mnt/nv it lists.
	debugLargestEntries = 8
	// snapcastStatusTimeout bounds asking Snapcast for its status.
	snapcastStatusTimeout = 2 * time.Second
	// maxKernelLines is how many matching kernel messages it keeps, the newest.
	maxKernelLines = 120
)

// debugBundle is the support bundle (ADR 0055). GET /api/debug is a plain-text snapshot of
// the speaker for an issue report, meant to be opened in a browser
// (http://<speaker>/api/debug); ?download=1 is the archive an owner attaches to a bug
// report (support.go): that text, the whole logs, the previous boot's records, version
// and health. No SSH needed, which a production speaker does not have (ADR 0119). Both
// pass through one redactor (redact.go). Read-only: it changes nothing on the speaker.
func (s *Server) debugBundle(w http.ResponseWriter, r *http.Request) {
	red := s.newRedactor(r.Context())

	text := s.debugText(r)
	if r.URL.Query().Get("download") != "" {
		s.supportArchive(w, r, text, red)

		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, red.scrub(text)) // the client went away: nothing left to tell it
}

// debugText is the plain-text bundle before redaction: version, network, resources,
// boot, audio, multiroom, discovery, the stores, the installer state, the kernel's
// messages and the log tails.
func (s *Server) debugText(r *http.Request) string {
	var out strings.Builder
	s.writeDebugHeader(&out, r)
	s.writeDebugNetwork(&out, r)
	s.writeDebugResources(&out, r)
	s.writeDebugBoot(&out)
	s.writeDebugAudio(&out, r)
	s.writeDebugSnapcast(&out, r)
	out.WriteString("\n== discovery ==\n")
	s.writeDiscoveryDebug(&out)

	s.writeDebugStores(&out)

	out.WriteString("\n== firmware install recovery ==\n")
	s.recovery.WriteDebug(&out)

	// The kernel ring buffer, filtered. A driver that fails to bring its hardware up says
	// so here and nowhere else, and on a speaker with no shell that made some questions
	// unanswerable: the WiLink8 Bluetooth controller reports "Reading TI version
	// information failed (-110)" to dmesg, and whether the line before it is a transmit
	// timeout or silence is what separates "the command never went out" from "the
	// controller never answered". Everything, unfiltered, would be pages of boot noise, so
	// this keeps the subsystems whose failures are invisible from userspace. The archive
	// carries the whole ring buffer.
	out.WriteString("\n== kernel messages (filtered) ==\n")
	out.WriteString(kernelMessages(r.Context()))

	for _, lg := range s.logPaths() {
		fmt.Fprintf(&out, "\n== %s (%s) ==\n", lg.name, lg.path)
		// A daemon can echo a credential into the log; the redactor scrubs it with the
		// rest. 32 KiB is about 130 slog lines; 8 KiB was
		// 30, under ten minutes of a busy speaker. The archive carries the whole files.
		out.WriteString(tailFile(lg.file, debugLogTail))
	}

	return out.String()
}

// writeDebugStores gives one line per on-box JSON store: "ok", or what Open found. An
// unreadable file keeps its store read-only (the line says writes are refused), and a
// production speaker has no shell to find that out with.
func (s *Server) writeDebugStores(out *strings.Builder) {
	out.WriteString("\n== on-box stores ==\n")

	line := func(name string, err error) {
		status := "ok"
		if err != nil {
			status = err.Error()
		}

		fmt.Fprintf(out, "%-10s : %s\n", name, status)
	}
	if s.settings != nil {
		line("settings", s.settings.LoadErr())
	}

	if s.presetsLoadErr != nil {
		line("presets", s.presetsLoadErr())
	}

	if s.recentStore != nil {
		line("recent", s.recentStore.LoadErr())
	}

	if s.favoritesStore != nil {
		line("favorites", s.favoritesStore.LoadErr())
	}
}

func (s *Server) writeDebugHeader(out *strings.Builder, r *http.Request) {
	fmt.Fprint(out, "SoundTap debug bundle\n")
	fmt.Fprintf(out, "version    : %s\n", s.version)

	osVersion, osCommit := osBuild()
	fmt.Fprintf(out, "core       : %s %s\n", osVersion, osCommit)
	fmt.Fprintf(out, "updatable  : %v\n", s.update.Updatable())
	fmt.Fprintf(out, "home       : %s\n", s.homeDir)
	fmt.Fprintf(out, "clock      : %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(out, "uptime     : %s\n", time.Since(s.startedAt).Round(time.Second))
	fmt.Fprintf(out, "runtime    : %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(out, "variant    : %s\n", speakerVariant())

	info, err := s.backend.Info(r.Context())
	if err == nil && info != nil {
		fmt.Fprintf(out, "model      : %s\n", info.Type)
		fmt.Fprintf(out, "device id  : %s\n", info.DeviceID)
	}
}

func (s *Server) writeDebugNetwork(out *strings.Builder, r *http.Request) {
	out.WriteString("\n== network ==\n")

	net, err := s.backend.NetworkInfo(r.Context())
	if err == nil && net != nil {
		fmt.Fprintf(out, "type       : %s\n", net.Type)
		fmt.Fprintf(out, "ip         : %s\n", net.IP)

		if net.Type != "ethernet" {
			// No PSK here, ever: this file is meant to be pasted into a public issue.
			fmt.Fprintf(out, "ssid       : %s\n", net.SSID)
			fmt.Fprintf(out, "signal     : %s\n", net.Signal)
		}
	} else {
		fmt.Fprintf(out, "unavailable: %v\n", err)
	}
}

func (s *Server) writeDebugResources(out *strings.Builder, r *http.Request) {
	out.WriteString("\n== resources ==\n")

	health := s.collectHealth(r.Context())
	fmt.Fprintf(out, "disk       : %s free of %s on %s\n",
		humanBytes(health.Disk.FreeBytes), humanBytes(health.Disk.TotalBytes), health.Disk.Path)
	// What fills the persistent volume, by top-level entry: production has no shell to
	// look with, and a full volume silently breaks app updates and settings.
	for _, u := range largestEntries(filepath.Dir(s.homeDir), debugLargestEntries) {
		size := uint64(u.Bytes) //nolint:gosec // G115: a file size is never negative
		fmt.Fprintf(out, "disk usage : %s %s\n", humanBytes(size), u.Path)
	}

	fmt.Fprintf(out, "memory     : %s available of %s\n",
		humanBytes(health.Memory.AvailableBytes), humanBytes(health.Memory.TotalBytes))
	fmt.Fprintf(out, "processes  : %d\n", health.Processes)
	fmt.Fprintf(out, "cpu        : %s\n", cpuLine("/proc"))

	if health.firewallReason != "" {
		fmt.Fprintf(out, "firewall   : %s (%s)\n", health.Firewall, health.firewallReason)
	} else {
		fmt.Fprintf(out, "firewall   : %s\n", health.Firewall)
	}

	for _, lg := range health.Logs {
		size := uint64(lg.Bytes) //nolint:gosec // G115: a file size is never negative
		fmt.Fprintf(out, "log        : %s (%s)\n", lg.Path, humanBytes(size))
	}

	c := health.Counters
	fmt.Fprintf(out, "counters   : since soundtapd started %s ago: soundtapd restarts %d (this boot), "+
		"daemon restarts %v, buffer underruns %d, ALSA underruns %d, install failures %d, "+
		"BCO flash failures %d, boot confirm retries %d\n",
		time.Duration(c.SinceSeconds)*time.Second, c.SoundtapdRestarts, c.DaemonRestarts,
		c.BufferUnderruns, c.ALSAUnderruns, c.InstallFailures, c.BCOFlashFailures, c.BootConfirmRetries)

	for _, warn := range health.Warnings {
		fmt.Fprintf(out, "WARNING    : %s\n", warn)
	}
}

func (s *Server) writeDebugBoot(out *strings.Builder) {
	cmdline, _ := os.ReadFile("/proc/cmdline")

	out.WriteString("\n== usb stick at power-on ==\n")
	fmt.Fprintf(out, "this boot  : %s\n", stickResult(string(cmdline)))

	// The last crash, written by soundtapd's goSafe before the restart (ADR 0052).
	raw, err := os.ReadFile(filepath.Join(s.homeDir, PanicLogName))
	if err == nil {
		out.WriteString("\n== last panic ==\n")
		out.Write(raw)
	}

	// What the kernel kept of the previous boot: how it ended, the end of each
	// panic or oops record, and the end of its console log.
	if dir := filepath.Join(s.homeDir, LastBootDir); readFileLine(filepath.Join(dir, "how")) != "<none>" {
		out.WriteString("\n== previous boot ==\n")
		fmt.Fprintf(out, "ended      : %s\n", readFileLine(filepath.Join(dir, "how")))

		names, _ := filepath.Glob(filepath.Join(dir, "*-ramoops-*"))
		for _, name := range names {
			lines := 40
			if strings.HasPrefix(filepath.Base(name), "console-") {
				lines = 20
			}

			fmt.Fprintf(out, "-- %s (last %d lines)\n%s\n", filepath.Base(name), lines, tailLines(name, lines))
		}
	}
}

func (s *Server) writeDebugAudio(out *strings.Builder, r *http.Request) {
	out.WriteString("\n== audio arbiter ==\n")

	mix := os.Getenv("AUDIO_MIX")
	if mix == "" {
		mix = "0 (unset — legacy direct-to-device)"
	}

	fmt.Fprintf(out, "AUDIO_MIX  : %s\n", mix)

	bus := os.Getenv("AUDIO_BUS")
	if bus == "" {
		bus = "/run/soundtap/source.pcm"
	}

	fmt.Fprintf(out, "source bus : %s (%s)\n", bus, fifoStatus(bus))
	fmt.Fprintf(out, "asound     : %s\n", readFileLine("/etc/asound.conf")) // first line: bus vs shelby

	mg := s.debugMuteGPIO()
	fmt.Fprintf(out, "mute gpio : %s = %s\n", mg, readFileLine(mg))

	np, err := s.backend.NowPlaying(r.Context())
	if err == nil && np != nil {
		fmt.Fprintf(out, "source     : %s (%s)\n", np.Source, np.PlayStatus)
	}
}

func (s *Server) writeDebugSnapcast(out *strings.Builder, r *http.Request) {
	out.WriteString("\n== snapcast ==\n")
	fmt.Fprintf(out, "local id   : %s\n", s.snapclientID)

	tap := os.Getenv("AUDIO_TAP")
	if tap == "" {
		tap = "/run/soundtap/snapcast.pcm"
	}

	fmt.Fprintf(out, "tap        : %s (%s)\n", tap, fifoStatus(tap))

	if s.multiroom == nil {
		out.WriteString("status     : unavailable (not configured)\n")

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), snapcastStatusTimeout)
	status, err := s.multiroom.GetStatus(ctx)

	cancel()

	if err != nil {
		fmt.Fprintf(out, "status     : unavailable (%v)\n", err)

		return
	}

	clients, connected := 0, 0
	for _, group := range status.Groups {
		clients += len(group.Clients)
		for _, client := range group.Clients {
			if client.Connected {
				connected++
			}
		}
	}

	fmt.Fprintf(out, "status     : %d groups, %d clients, %d connected\n", len(status.Groups), clients, connected)
}

// kernelMessagePrefixes are the subsystems whose failures never reach a userspace log.
// Bluetooth and the serial bus it rides on, the flash the slots live on, the audio path,
// and anything the kernel itself called an error.
func kernelMessagePrefixes() []string {
	return []string{
		"Bluetooth", "hci", "bluetooth",
		"serdev", "omap8250", "8250",
		"nand", "ubi", "mtd", "UBIFS",
		"asoc", "ASoC", "davinci", "mcasp",
		"wlcore", "wl18xx",
		"usb 1-", "cdc_ether", "soundtap_bco", // the ST20 BCO link
	}
}

// kernelMessageWanted reports whether one kernel line belongs in the bundle: anything the
// kernel called an error or a failure, plus everything from the subsystems above.
func kernelMessageWanted(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "error") || strings.Contains(lower, "fail") {
		return true
	}

	for _, prefix := range kernelMessagePrefixes() {
		if strings.Contains(line, prefix) {
			return true
		}
	}

	return false
}

// kernelMessages returns the matching lines of /dev/kmsg's readable twin, newest last.
// /proc/kmsg is a consuming read and must never be used here -- draining it would take
// those messages away from anything else reading the log.
func kernelMessages(ctx context.Context) string {
	raw, err := kernelLog(ctx)
	if err != nil {
		return fmt.Sprintf("unavailable: %v\n", err)
	}

	var keep []string

	for line := range strings.SplitSeq(string(raw), "\n") {
		if line != "" && kernelMessageWanted(line) {
			keep = append(keep, line)
		}
	}

	if len(keep) == 0 {
		return "no matching kernel messages\n"
	}

	if len(keep) > maxKernelLines {
		keep = keep[len(keep)-maxKernelLines:]
	}

	return strings.Join(keep, "\n") + "\n"
}

// kernelLog is the whole kernel ring buffer, live first: /var/log/dmesg is a snapshot from
// boot and misses everything after it.
func kernelLog(ctx context.Context) ([]byte, error) {
	raw, err := exec.CommandContext(ctx, "dmesg").Output()
	if err != nil {
		snap, readErr := os.ReadFile("/var/log/dmesg")
		if readErr == nil {
			return snap, nil
		}

		return nil, fmt.Errorf("dmesg: %w", err)
	}

	return raw, nil
}

// tailLines is the last n lines of a file, or "" when it cannot be read.
func tailLines(path string, count int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")

	return strings.Join(lines[max(0, len(lines)-count):], "\n")
}

// readFileLine returns the first line of a small state file, or "<none>".
func readFileLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "<none>"
	}

	s := strings.TrimSpace(string(b))
	if s == "" {
		return "<empty>"
	}

	return s
}

// speakerVariant names the board this build is running on, the same way the boot
// scripts do: stock "lisa" and mainline "spotty" are the ST20 SCM. Off the
// speaker neither file exists, which is itself the answer.
func speakerVariant() string {
	variant, err := os.ReadFile("/proc/variant")
	if err == nil && strings.TrimSpace(string(variant)) != "" {
		return strings.TrimSpace(string(variant))
	}

	cmdline, err := os.ReadFile("/proc/cmdline")
	if err == nil {
		for f := range strings.FieldsSeq(string(cmdline)) {
			if v, ok := strings.CutPrefix(f, "variant="); ok {
				return v
			}
		}

		return "rhino_sm2 (assumed: no variant= on the kernel command line)"
	}

	return "<not on a speaker>"
}

func (s *Server) debugMuteGPIO() string {
	if s.muteLine == nil {
		return "<unknown: no resolver>"
	}

	return s.muteLine()
}

// tailFile returns at most limit bytes from the end of path, dropping a leading partial
// line when truncated. Missing/empty files yield a short placeholder instead of an error.
func tailFile(path string, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return "<none>\n"
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return unreadable
	}

	truncated := fi.Size() > limit
	if truncated {
		_, seekErr := file.Seek(-limit, io.SeekEnd)
		if seekErr != nil {
			return unreadable
		}
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return unreadable
	}

	out := string(data)
	if truncated {
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = "…(truncated)\n" + out[i+1:]
		}
	}

	if strings.TrimSpace(out) == "" {
		return "<empty>\n"
	}

	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}

	return out
}

func fileExists(path string) bool {
	st, err := os.Stat(path) //nolint:gosec // G703: a marker path the daemon itself names, never request input

	return err == nil && !st.IsDir()
}

// fifoStatus describes the source-bus path for the debug bundle: whether it exists
// and is actually a FIFO (arbiter mode) rather than a stray regular file.
func fifoStatus(path string) string {
	st, err := os.Stat(path) //nolint:gosec // G703: the daemon's own audio paths, never request input
	switch {
	case err != nil:
		return "absent"
	case st.Mode()&os.ModeNamedPipe != 0:
		return "fifo ok"
	default:
		return "exists but not a fifo"
	}
}

// stickResult reads U-Boot's soundtap.recovery=<rc> from the kernel command line (ADR 0130):
// whether the USB-stick gesture was seen at power-on, and what came of it. On a speaker
// without SSH, the ST300's above all, this is the one place an owner can read it.
func stickResult(cmdline string) string {
	for f := range strings.FieldsSeq(cmdline) {
		code, ok := strings.CutPrefix(f, "soundtap.recovery=")
		if !ok {
			continue
		}

		meaning := map[string]string{
			"0":   "installed from the stick",
			"-19": "gesture seen, no USB stick found",
			"-2":  "gesture seen, no Update.stu on the stick",
			"-8":  "gesture seen, the file was refused (not a .stu or not for this speaker)",
			"-74": "gesture seen, a CRC or read-back check failed",
			"-5":  "gesture seen, a read or NAND error",
		}[code]
		if meaning == "" {
			meaning = "gesture seen, error " + code
		}

		return code + " (" + meaning + ")"
	}

	return "no gesture seen"
}
