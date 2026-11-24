// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/install"
)

// Bounds a speaker actually runs into. The ST10 has 128 MiB of RAM and a NAND
// rootfs of a few hundred MiB, and /tmp is a tmpfs — so a log that grows without
// bound eats RAM, not disk, which is why the log budget here is small and separate
// from the disk one.
const (
	// What an update stages on the persistent volume: a soundtapd binary (~11 MB) for an
	// app update, or the kernel (~5 MB) plus the bootloaders for a firmware install; the
	// rootfs streams straight to flash. 32 MiB was more than the whole 31.5 MiB volume,
	// so the warning showed on every speaker all the time.
	diskLowBytes  = 16 << 20
	diskLowPct    = 10
	memLowBytes   = 8 << 20
	logLargeBytes = 4 << 20
	// procManyCount is well above a healthy speaker (~40 processes) and well below
	// anything that would wedge the box, so it fires on a respawn loop, not on load.
	procManyCount = 200
)

// logFiles are the runtime logs, in the order the support bundle tails them. A relative
// path is under the home directory (Server.logPaths).
var logFiles = []struct{ name, path string }{
	{"install log", "/tmp/soundtap-install.log"},
	{"start log", "/tmp/soundtap-start.log"},
	{"agent log", "/tmp/soundtap.log"},
	// /tmp is gone after a reboot; init keeps the last 32 KiB of each of the last three
	// boots, snapshotted every 15 minutes so a watchdog reset or panic leaves one too.
	{"previous boot log", "previous-boot.log"},
	{"boot log before that", "previous-boot.1.log"},
	{"boot log two back", "previous-boot.2.log"},
	// The ST20 is off the LAN while its BCO flashes, so this is read afterwards.
	{"BCO flash log", "/run/soundtap/bco-flash.log"},
	// The BCO LAN watch runs early; its lines are gone from the agent log tail by the time
	// anyone reads the bundle over USB.
	{"BCO LAN watch", "/run/soundtap/bco-lan.log"},
}

// logFile is a runtime log: path is what the speaker calls it (shown in /api/health and
// the support bundle), file is where this process reads it (sysPath, or the home directory).
type logFile struct{ name, path, file string }

// logPaths is logFiles with the relative paths placed under the home directory.
func (s *Server) logPaths() []logFile {
	out := make([]logFile, 0, len(logFiles))
	for _, lg := range logFiles {
		if filepath.IsAbs(lg.path) {
			out = append(out, logFile{lg.name, lg.path, sysPath(lg.path)})
		} else {
			p := filepath.Join(s.homeDir, lg.path)
			out = append(out, logFile{lg.name, p, p})
		}
	}
	return out
}

type diskUsage struct {
	Path       string `json:"path"`
	FreeBytes  uint64 `json:"freeBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

type memUsage struct {
	TotalBytes     uint64 `json:"totalBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
}

type logUsage struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// health is what /api/health returns: the few resources that, when they run out,
// stop the speaker from updating or from staying up. Warnings are computed here
// rather than in the browser so the UI, the support bundle and MQTT cannot drift
// into disagreeing about what "nearly full" means.
type health struct {
	Disk      diskUsage  `json:"disk"`
	Memory    memUsage   `json:"memory"`
	Processes int        `json:"processes"`
	Logs      []logUsage `json:"logs"`
	AmpFault  bool       `json:"ampFault"`
	// Firewall is FirewallLoaded, FirewallMissing or FirewallNA (firewall.go).
	Firewall string   `json:"firewall"`
	Warnings []string `json:"warnings"`
	// Boot is the running boot, for the bench scripts (tools/hil/).
	Boot bootState `json:"boot"`
	// Counters are the restarts, underruns and failed writes since soundtapd started (ADR 0127).
	Counters healthCounters `json:"counters"`
	// firewallReason says why a missing filter is missing, for the support bundle.
	firewallReason string
}

// bootIDFile is the kernel's random id of this boot: every boot has a new one.
const bootIDFile = "/proc/sys/kernel/random/boot_id"

// bootState is the running boot as a bench script without a shell needs it (tools/hil/,
// ADR 0119): which boot, which slot, whether soundtapd confirmed it to U-Boot (ADR 0010)
// and how the boot before it ended. Nothing in it is a secret and reading it changes
// nothing. It is on /api/health rather than /api/version, which any web page may read
// (ADR 0113): a boot id would let one recognise the speaker until its next boot.
type bootState struct {
	// ID is /proc/sys/kernel/random/boot_id; empty where the kernel has none.
	ID string `json:"id"`
	// Slot is PING or PONG, the root ubi.mtd= of the kernel command line; empty off NAND.
	Slot string `json:"slot"`
	// Confirmed is true once soundtapd has reset U-Boot's boot count for this boot, a
	// minute after it started; a restarted soundtapd confirms again.
	Confirmed bool `json:"confirmed"`
	// Previous is how the boot before this one ended, as lastboot.go read it from ramoops:
	// reboot, power loss, panic and so on. Empty when nothing was recorded.
	Previous string `json:"previous"`
	// Bootloader is the SPL and U-Boot on NAND by SHA-256, the sums of a build's MLO and
	// u-boot.img; absent off NAND.
	Bootloader *install.BootChain `json:"bootloader,omitempty"`
}

// healthCounters are internal/counters' counts and how long they have been counting: each
// starts over with soundtapd, except soundtapdRestarts, which counts this boot's restarts.
type healthCounters struct {
	counters.Snapshot
	// SinceSeconds is how long ago soundtapd started.
	SinceSeconds int64 `json:"sinceSeconds"`
}

func (s *Server) healthCounters() healthCounters {
	return healthCounters{Snapshot: counters.Take(), SinceSeconds: int64(time.Since(s.startedAt).Seconds())}
}

func (s *Server) bootState() bootState {
	read := func(path string) string {
		b, _ := os.ReadFile(path)
		return strings.TrimSpace(string(b))
	}
	b := bootState{
		ID:        read(sysPath(bootIDFile)),
		Confirmed: s.fw.BootConfirmed(),
		Previous:  read(filepath.Join(s.homeDir, LastBootDir, "how")),
	}
	if slot, err := flash.SlotFromCmdline(read(sysPath("/proc/cmdline"))); err == nil {
		b.Slot = slot.Name
		chain := s.fw.BootChain()
		b.Bootloader = &chain
	}
	return b
}

func (d diskUsage) freePct() int {
	if d.TotalBytes == 0 {
		return 0
	}
	return int(d.FreeBytes * 100 / d.TotalBytes)
}

// low reports a volume without room to stage an update.
func (d diskUsage) low() bool {
	return d.TotalBytes > 0 && (d.FreeBytes < diskLowBytes || d.freePct() < diskLowPct)
}

// collectHealth samples the resources. Anything unreadable (a non-Linux dev box, a
// missing log) stays zero and simply produces no warning: a monitor that invents a
// problem when it cannot measure is worse than one that stays quiet.
func (s *Server) collectHealth(ctx context.Context) health {
	h := health{Disk: diskUsage{Path: s.homeDir}, Logs: []logUsage{}, Warnings: []string{}, Boot: s.bootState(),
		Counters: s.healthCounters()}
	if free, total, err := diskFree(s.homeDir); err == nil {
		h.Disk.FreeBytes, h.Disk.TotalBytes = free, total
	}
	h.Memory = readMeminfo("/proc/meminfo")
	h.Processes = countProcesses("/proc")
	for _, lg := range s.logPaths() {
		if fi, err := os.Stat(lg.file); err == nil {
			h.Logs = append(h.Logs, logUsage{Path: lg.path, Bytes: fi.Size()})
		}
	}

	if s.update != nil && s.update.Updatable() && h.Disk.low() {
		h.Warnings = append(h.Warnings, fmt.Sprintf("only %s free on %s (%d%%) — a firmware update needs room to stage an image",
			humanBytes(h.Disk.FreeBytes), h.Disk.Path, h.Disk.freePct()))
	}
	if h.Memory.TotalBytes > 0 && h.Memory.AvailableBytes < memLowBytes {
		h.Warnings = append(h.Warnings, fmt.Sprintf("only %s memory available", humanBytes(h.Memory.AvailableBytes)))
	}
	for _, lg := range h.Logs {
		if lg.Bytes >= logLargeBytes {
			h.Warnings = append(h.Warnings, fmt.Sprintf("%s has grown to %s; /tmp is RAM on this speaker", lg.Path, humanBytes(uint64(lg.Bytes))))
		}
	}
	if h.Processes >= procManyCount {
		h.Warnings = append(h.Warnings, fmt.Sprintf("%d processes running — something is respawning", h.Processes))
	}
	if s.ampFaultFn != nil && s.ampFaultFn() {
		h.AmpFault = true
		h.Warnings = append(h.Warnings, "amp fault detected: output muted")
	}
	if s.daemonProblems != nil {
		h.Warnings = append(h.Warnings, s.daemonProblems()...)
	}
	h.Firewall, h.firewallReason = s.firewallStatus(ctx)
	if h.Firewall == FirewallMissing {
		h.Warnings = append(h.Warnings, "the inbound firewall is not loaded: every listening port is open to the network")
	}
	return h
}

func (s *Server) healthInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.collectHealth(r.Context()))
}

// readMeminfo parses MemTotal/MemAvailable (kB) out of /proc/meminfo. A kernel too
// old for MemAvailable falls back to MemFree, which understates what is reclaimable
// but never overstates it.
func readMeminfo(path string) memUsage {
	f, err := os.Open(path)
	if err != nil {
		return memUsage{}
	}
	defer func() { _ = f.Close() }()
	var m memUsage
	var free uint64
	// Whether the line was there, not whether it is non-zero: MemAvailable 0 is real (the
	// box is out of memory), and replacing it with MemFree hid exactly that.
	haveAvailable := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		kb, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			m.TotalBytes = kb << 10
		case "MemAvailable":
			m.AvailableBytes = kb << 10
			haveAvailable = true
		case "MemFree":
			free = kb << 10
		}
	}
	if !haveAvailable {
		m.AvailableBytes = free
	}
	return m
}

// countProcesses counts the numeric entries in /proc. Zero means "could not tell".
func countProcesses(proc string) int {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err == nil {
			n++
		}
	}
	return n
}

func humanBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/(1<<10))
	}
	return fmt.Sprintf("%d B", b)
}

// cpuLine reports CPU use without a shell (production has none): idle against the wall
// clock, this process's own time and the fork count, each since boot. Two bundles a known
// time apart give the interval figures; docs/hardware/hardware-test.md "Measuring CPU" says why idle is
// read against uptime rather than trusting the busy ticks. USER_HZ is 100 on ARM.
func cpuLine(proc string) string {
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(proc, name))
		return string(b)
	}
	up, _ := strconv.ParseFloat(strings.Fields(read("uptime") + " 0")[0], 64)
	var idle, forks float64
	for _, line := range strings.Split(read("stat"), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) > 4 && f[0] == "cpu":
			idle, _ = strconv.ParseFloat(f[4], 64)
		case len(f) == 2 && f[0] == "processes":
			forks, _ = strconv.ParseFloat(f[1], 64)
		}
	}
	var self float64
	if _, rest, ok := strings.Cut(read("self/stat"), ") "); ok {
		if f := strings.Fields(rest); len(f) > 12 {
			u, _ := strconv.ParseFloat(f[11], 64)
			s, _ := strconv.ParseFloat(f[12], 64)
			self = u + s
		}
	}
	if up <= 0 {
		return "unavailable"
	}
	return fmt.Sprintf("uptime %.0f s, idle %.0f s (busy %.0f%% since boot), soundtapd %.0f s, forks %.0f",
		up, idle/100, 100-idle/up, self/100, forks)
}

// largestEntries sums the files under each entry of dir and returns the n biggest,
// largest first. Errors inside one entry only make its size an undercount.
func largestEntries(dir string, n int) []logUsage {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]logUsage, 0, len(entries))
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		var size int64
		_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if fi, err := d.Info(); err == nil {
					size += fi.Size()
				}
			}
			return nil
		})
		out = append(out, logUsage{Path: path, Bytes: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	if len(out) > n {
		out = out[:n]
	}
	return out
}
