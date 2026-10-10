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
	var b strings.Builder
	s.writeDebugHeader(&b, r)
	s.writeDebugNetwork(&b, r)
	s.writeDebugResources(&b, r)
	s.writeDebugBoot(&b)
	s.writeDebugAudio(&b, r)
	s.writeDebugSnapcast(&b, r)
	b.WriteString("\n== discovery ==\n")
	s.writeDiscoveryDebug(&b)

	s.writeDebugStores(&b)

	b.WriteString("\n== firmware install recovery ==\n")
	s.recovery.WriteDebug(&b)

	// The kernel ring buffer, filtered. A driver that fails to bring its hardware up says
	// so here and nowhere else, and on a speaker with no shell that made some questions
	// unanswerable: the WiLink8 Bluetooth controller reports "Reading TI version
	// information failed (-110)" to dmesg, and whether the line before it is a transmit
	// timeout or silence is what separates "the command never went out" from "the
	// controller never answered". Everything, unfiltered, would be pages of boot noise, so
	// this keeps the subsystems whose failures are invisible from userspace. The archive
	// carries the whole ring buffer.
	b.WriteString("\n== kernel messages (filtered) ==\n")
	b.WriteString(kernelMessages(r.Context()))

	for _, lg := range s.logPaths() {
		fmt.Fprintf(&b, "\n== %s (%s) ==\n", lg.name, lg.path)
		// A daemon can echo a credential into the log; the redactor scrubs it with the
		// rest. 32 KiB is about 130 slog lines; 8 KiB was
		// 30, under ten minutes of a busy speaker. The archive carries the whole files.
		b.WriteString(tailFile(lg.file, 32<<10))
	}
	return b.String()
}

// writeDebugStores gives one line per on-box JSON store: "ok", or what Open found. An
// unreadable file keeps its store read-only (the line says writes are refused), and a
// production speaker has no shell to find that out with.
func (s *Server) writeDebugStores(b *strings.Builder) {
	b.WriteString("\n== on-box stores ==\n")
	line := func(name string, err error) {
		status := "ok"
		if err != nil {
			status = err.Error()
		}
		fmt.Fprintf(b, "%-10s : %s\n", name, status)
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

func (s *Server) writeDebugHeader(b *strings.Builder, r *http.Request) {
	fmt.Fprintf(b, "SoundTap debug bundle\n")
	fmt.Fprintf(b, "version    : %s\n", s.version)
	osVersion, osCommit := osBuild()
	fmt.Fprintf(b, "core       : %s %s\n", osVersion, osCommit)
	fmt.Fprintf(b, "updatable  : %v\n", s.update.Updatable())
	fmt.Fprintf(b, "home       : %s\n", s.homeDir)
	fmt.Fprintf(b, "clock      : %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(b, "uptime     : %s\n", time.Since(s.startedAt).Round(time.Second))
	fmt.Fprintf(b, "runtime    : %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(b, "variant    : %s\n", speakerVariant())
	if info, err := s.backend.Info(r.Context()); err == nil && info != nil {
		fmt.Fprintf(b, "model      : %s\n", info.Type)
		fmt.Fprintf(b, "device id  : %s\n", info.DeviceID)
	}

}

func (s *Server) writeDebugNetwork(b *strings.Builder, r *http.Request) {
	b.WriteString("\n== network ==\n")
	if net, err := s.backend.NetworkInfo(r.Context()); err == nil && net != nil {
		fmt.Fprintf(b, "type       : %s\n", net.Type)
		fmt.Fprintf(b, "ip         : %s\n", net.IP)
		if net.Type != "ethernet" {
			// No PSK here, ever: this file is meant to be pasted into a public issue.
			fmt.Fprintf(b, "ssid       : %s\n", net.SSID)
			fmt.Fprintf(b, "signal     : %s\n", net.Signal)
		}
	} else {
		fmt.Fprintf(b, "unavailable: %v\n", err)
	}

}

func (s *Server) writeDebugResources(b *strings.Builder, r *http.Request) {
	b.WriteString("\n== resources ==\n")
	h := s.collectHealth(r.Context())
	fmt.Fprintf(b, "disk       : %s free of %s on %s\n", humanBytes(h.Disk.FreeBytes), humanBytes(h.Disk.TotalBytes), h.Disk.Path)
	// What fills the persistent volume, by top-level entry: production has no shell to
	// look with, and a full volume silently breaks app updates and settings.
	for _, u := range largestEntries(filepath.Dir(s.homeDir), 8) {
		fmt.Fprintf(b, "disk usage : %s %s\n", humanBytes(uint64(u.Bytes)), u.Path) //nolint:gosec // G115: a file size is never negative
	}
	fmt.Fprintf(b, "memory     : %s available of %s\n", humanBytes(h.Memory.AvailableBytes), humanBytes(h.Memory.TotalBytes))
	fmt.Fprintf(b, "processes  : %d\n", h.Processes)
	fmt.Fprintf(b, "cpu        : %s\n", cpuLine("/proc"))
	if h.firewallReason != "" {
		fmt.Fprintf(b, "firewall   : %s (%s)\n", h.Firewall, h.firewallReason)
	} else {
		fmt.Fprintf(b, "firewall   : %s\n", h.Firewall)
	}
	for _, lg := range h.Logs {
		fmt.Fprintf(b, "log        : %s (%s)\n", lg.Path, humanBytes(uint64(lg.Bytes))) //nolint:gosec // G115: a file size is never negative
	}
	c := h.Counters
	fmt.Fprintf(b, "counters   : since soundtapd started %s ago: soundtapd restarts %d (this boot), daemon restarts %v, buffer underruns %d, ALSA underruns %d, install failures %d, BCO flash failures %d, boot confirm retries %d\n",
		time.Duration(c.SinceSeconds)*time.Second, c.SoundtapdRestarts, c.DaemonRestarts, c.BufferUnderruns, c.ALSAUnderruns,
		c.InstallFailures, c.BCOFlashFailures, c.BootConfirmRetries)
	for _, warn := range h.Warnings {
		fmt.Fprintf(b, "WARNING    : %s\n", warn)
	}

}

func (s *Server) writeDebugBoot(b *strings.Builder) {
	// The last crash, written by soundtapd's goSafe before the restart (ADR 0052).
	if raw, err := os.ReadFile(filepath.Join(s.homeDir, PanicLogName)); err == nil {
		b.WriteString("\n== last panic ==\n")
		b.Write(raw)
	}

	// What the kernel kept of the previous boot: how it ended, the end of each
	// panic or oops record, and the end of its console log.
	if dir := filepath.Join(s.homeDir, LastBootDir); readFileLine(filepath.Join(dir, "how")) != "<none>" {
		b.WriteString("\n== previous boot ==\n")
		fmt.Fprintf(b, "ended      : %s\n", readFileLine(filepath.Join(dir, "how")))
		names, _ := filepath.Glob(filepath.Join(dir, "*-ramoops-*"))
		for _, name := range names {
			lines := 40
			if strings.HasPrefix(filepath.Base(name), "console-") {
				lines = 20
			}
			fmt.Fprintf(b, "-- %s (last %d lines)\n%s\n", filepath.Base(name), lines, tailLines(name, lines))
		}
	}

}

func (s *Server) writeDebugAudio(b *strings.Builder, r *http.Request) {
	b.WriteString("\n== audio arbiter ==\n")
	mix := os.Getenv("AUDIO_MIX")
	if mix == "" {
		mix = "0 (unset — legacy direct-to-device)"
	}
	fmt.Fprintf(b, "AUDIO_MIX  : %s\n", mix)
	bus := os.Getenv("AUDIO_BUS")
	if bus == "" {
		bus = "/run/soundtap/source.pcm"
	}
	fmt.Fprintf(b, "source bus : %s (%s)\n", bus, fifoStatus(bus))
	fmt.Fprintf(b, "asound     : %s\n", readFileLine("/etc/asound.conf")) // first line: bus vs shelby
	mg := s.debugMuteGPIO()
	fmt.Fprintf(b, "mute gpio : %s = %s\n", mg, readFileLine(mg))
	if np, err := s.backend.NowPlaying(r.Context()); err == nil && np != nil {
		fmt.Fprintf(b, "source     : %s (%s)\n", np.Source, np.PlayStatus)
	}

}

func (s *Server) writeDebugSnapcast(b *strings.Builder, r *http.Request) {
	b.WriteString("\n== snapcast ==\n")
	fmt.Fprintf(b, "local id   : %s\n", s.snapclientID)
	tap := os.Getenv("AUDIO_TAP")
	if tap == "" {
		tap = "/run/soundtap/snapcast.pcm"
	}
	fmt.Fprintf(b, "tap        : %s (%s)\n", tap, fifoStatus(tap))
	if s.multiroom == nil {
		b.WriteString("status     : unavailable (not configured)\n")
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		status, err := s.multiroom.GetStatus(ctx)
		cancel()
		if err != nil {
			fmt.Fprintf(b, "status     : unavailable (%v)\n", err)
		} else {
			clients, connected := 0, 0
			for _, group := range status.Groups {
				clients += len(group.Clients)
				for _, client := range group.Clients {
					if client.Connected {
						connected++
					}
				}
			}
			fmt.Fprintf(b, "status     : %d groups, %d clients, %d connected\n", len(status.Groups), clients, connected)
		}
	}

}

// kernelMessagePrefixes are the subsystems whose failures never reach a userspace log.
// Bluetooth and the serial bus it rides on, the flash the slots live on, the audio path,
// and anything the kernel itself called an error.
var kernelMessagePrefixes = []string{
	"Bluetooth", "hci", "bluetooth",
	"serdev", "omap8250", "8250",
	"nand", "ubi", "mtd", "UBIFS",
	"asoc", "ASoC", "davinci", "mcasp",
	"wlcore", "wl18xx",
	"usb 1-", "cdc_ether", "soundtap_bco", // the ST20 BCO link
}

// kernelMessageWanted reports whether one kernel line belongs in the bundle: anything the
// kernel called an error or a failure, plus everything from the subsystems above.
func kernelMessageWanted(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "error") || strings.Contains(lower, "fail") {
		return true
	}
	for _, prefix := range kernelMessagePrefixes {
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
	for _, line := range strings.Split(string(raw), "\n") {
		if line != "" && kernelMessageWanted(line) {
			keep = append(keep, line)
		}
	}
	if len(keep) == 0 {
		return "no matching kernel messages\n"
	}
	if len(keep) > 120 {
		keep = keep[len(keep)-120:]
	}
	return strings.Join(keep, "\n") + "\n"
}

// kernelLog is the whole kernel ring buffer, live first: /var/log/dmesg is a snapshot from
// boot and misses everything after it.
func kernelLog(ctx context.Context) ([]byte, error) {
	raw, err := exec.CommandContext(ctx, "dmesg").Output()
	if err != nil {
		if snap, readErr := os.ReadFile("/var/log/dmesg"); readErr == nil {
			return snap, nil
		}
		return nil, err
	}
	return raw, nil
}

// tailLines is the last n lines of a file, or "" when it cannot be read.
func tailLines(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
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
	if b, err := os.ReadFile("/proc/variant"); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/proc/cmdline"); err == nil {
		for _, f := range strings.Fields(string(b)) {
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

// tailFile returns at most max bytes from the end of path, dropping a leading partial
// line when truncated. Missing/empty files yield a short placeholder instead of an error.
func tailFile(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return "<none>\n"
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "<unreadable>\n"
	}
	truncated := fi.Size() > max
	if truncated {
		if _, err := f.Seek(-max, io.SeekEnd); err != nil {
			return "<unreadable>\n"
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "<unreadable>\n"
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
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// fifoStatus describes the source-bus path for the debug bundle: whether it exists
// and is actually a FIFO (arbiter mode) rather than a stray regular file.
func fifoStatus(path string) string {
	st, err := os.Stat(path)
	switch {
	case err != nil:
		return "absent"
	case st.Mode()&os.ModeNamedPipe != 0:
		return "fifo ok"
	default:
		return "exists but not a fifo"
	}
}
