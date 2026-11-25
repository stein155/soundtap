// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// supportFileMax bounds each file the archive carries. soundtap.log rotates at 256 KiB
// and the boot logs and ramoops records are smaller, so on a speaker every file goes in
// whole; the bound only keeps a runaway file from filling a 128 MiB speaker's memory.
const supportFileMax = 512 << 10

// archiveLogs are the logs the archive carries on top of logFiles, whose tails the plain
// text already shows: syslogd's rotated copy of the agent log, the boot log that is still
// being written, the init script's boot trace and its record of a boot whose network never
// came up. Logical paths, read through sysPath.
func archiveLogs() []string {
	return []string{
		"/tmp/soundtap.log.0",
		"/mnt/nv/soundtap/current-boot.log",
		"/mnt/nv/soundtap-boot.log",
		"/mnt/nv/soundtap-netfail.log",
	}
}

// supportArchive answers GET /api/debug?download=1 with one zip an owner attaches to an
// issue report, the only way into a production speaker (ADR 0119):
//
//	debug.txt         the plain-text bundle, as GET /api/debug shows it
//	version.json      GET /api/version
//	health.json       GET /api/health
//	kernel.log        the whole kernel ring buffer
//	panic.log         soundtapd's last crash, when there was one
//	logs/<path>       every log, whole: this boot's, the rotated copy, the last three boots'
//	last-boot/<name>  what the kernel kept of the previous boot (ramoops) and how it ended
//
// Every file goes through red.scrub, in supportZip.add and nowhere else.
func (s *Server) supportArchive(w http.ResponseWriter, r *http.Request, text string, red *redactor) {
	var buf bytes.Buffer

	archive := &supportZip{zw: zip.NewWriter(&buf), red: red, now: time.Now(), err: nil}
	s.fillSupportArchive(archive, r, text)

	if archive.err == nil {
		archive.err = archive.zw.Close()
	}

	if archive.err != nil {
		s.fail(w, "support bundle failed", archive.err)

		return
	}

	header := w.Header()
	header.Set("Content-Type", "application/zip")
	header.Set("Cache-Control", "no-store")

	stamp := archive.now.UTC().Format("20060102-150405")
	header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="soundtap-support-%s.zip"`, stamp))
	header.Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = buf.WriteTo(w) // the client went away: nothing left to tell it
}

func (s *Server) fillSupportArchive(archive *supportZip, r *http.Request, text string) {
	archive.add("debug.txt", []byte(text))
	archive.addJSON("version.json", s.versionData())
	archive.addJSON("health.json", s.collectHealth(r.Context()))

	kernel, err := kernelLog(r.Context())
	if err != nil {
		kernel = fmt.Appendf(nil, "unavailable: %v\n", err)
	}

	archive.add("kernel.log", kernel)
	archive.addFile(PanicLogName, filepath.Join(s.homeDir, PanicLogName))

	for _, logPath := range s.logPaths() {
		// A home log is named as the speaker names it, whatever root a test gives it.
		rel, err := filepath.Rel(sysRoot, logPath.path)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = logPath.path
		}

		archive.addFile("logs/"+strings.TrimPrefix(filepath.ToSlash(rel), "/"), logPath.file)
	}

	for _, p := range archiveLogs() {
		archive.addLog(p)
	}

	dir := filepath.Join(s.homeDir, LastBootDir)

	entries, _ := os.ReadDir(dir) // no records: the kernel kept nothing, or this is not a speaker
	for _, e := range entries {
		if e.Type().IsRegular() {
			archive.addFile(LastBootDir+"/"+e.Name(), filepath.Join(dir, e.Name()))
		}
	}
}

// supportZip writes the archive's files, each through the redactor. The first failure
// sticks, and every add after it does nothing.
type supportZip struct {
	zw  *zip.Writer
	red *redactor
	now time.Time
	err error
}

func (z *supportZip) add(name string, body []byte) {
	if z.err != nil {
		return
	}

	f, err := z.zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: z.now})
	if err == nil {
		_, err = io.WriteString(f, z.red.scrub(string(body)))
	}

	z.err = err
}

func (z *supportZip) addJSON(name string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		z.err = cmp.Or(z.err, err)

		return
	}

	z.add(name, append(data, '\n'))
}

// addFile adds a file if it is there; absent, this model or this boot never wrote it.
func (z *supportZip) addFile(name, path string) {
	if b, ok := readCapped(path, supportFileMax); ok {
		z.add(name, b)
	}
}

// addLog adds a log under its logical path, as logs/tmp/soundtap.log.
func (z *supportZip) addLog(path string) {
	z.addFile("logs/"+strings.TrimPrefix(filepath.ToSlash(path), "/"), sysPath(path))
}

// readCapped reads a file, keeping at most limit bytes from its end and dropping the
// partial line a cut leaves. ok is false when it cannot be read.
func readCapped(path string, limit int64) ([]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()

	fi, err := file.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}

	cut := fi.Size() > limit
	if cut {
		_, seekErr := file.Seek(-limit, io.SeekEnd)
		if seekErr != nil {
			return nil, false
		}
	}

	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, false
	}

	if cut {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = append([]byte("…(truncated)\n"), data[i+1:]...)
		}
	}

	return data, true
}
