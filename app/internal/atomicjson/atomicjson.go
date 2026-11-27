// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package atomicjson writes a value as indented JSON to a file atomically: it
// writes a temporary file next to the target and renames it into place, so a
// crash or power loss mid-write can't leave a half-written file. Several small
// on-disk stores (presets, settings, marge presets) share this pattern; WriteFile
// does the same for state files that are not JSON (wifi.conf, tokens, markers).
//
// Read, Load and Quarantine are the matching open rule: only a missing file starts
// a store from its defaults; an unreadable one makes it read-only, and a corrupt one
// is moved aside before anything can overwrite it.
package atomicjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// tempMark is what WriteStream puts between the target's name and the temporary file's
// random digits: ".<name>.tmp-<digits>".
const tempMark = ".tmp-"

// Write marshals v as indented JSON and atomically replaces path with it, creating
// path with the given file mode.
func Write(path string, v any, mode fs.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}

	return WriteFile(path, data, mode)
}

// WriteFile atomically replaces path with data, creating path with the given file
// mode: temp file in the same directory, fsync, rename, fsync of the directory.
func WriteFile(path string, data []byte, mode fs.FileMode) error {
	return WriteStream(path, mode, func(w io.Writer) error {
		_, err := w.Write(data)
		if err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}

		return nil
	})
}

// WriteStream is WriteFile for content too large to hold in memory: write streams it
// into the temporary file, and path is replaced only when write returns nil and the
// file is on disk. A failure leaves path as it was and no temporary file behind.
func WriteStream(path string, mode fs.FileMode, write func(io.Writer) error) error {
	// fsync the data before the rename, else power loss (a speaker's normal
	// off-switch) can journal the rename ahead of the data blocks and leave a
	// zero-length/truncated target — the very corruption this package prevents.
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+tempMark+"*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}

	tmp := file.Name()

	err = file.Chmod(mode)
	if err == nil {
		err = write(file)
	}

	if err == nil {
		err = file.Sync()
	}

	if cerr := file.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(tmp, path)
	}

	if err != nil {
		_ = os.Remove(tmp) // best effort: the error that matters is err

		return fmt.Errorf("replace %s: %w", path, err)
	}
	// fsync the directory so the rename itself survives power loss. Its error is not
	// returned: the rename is done and the new content is what path holds now, so a
	// caller told "failed" would keep old state in memory over a file that has the new.
	syncDir(filepath.Dir(path))

	return nil
}

// CleanTemps removes from dir the temporary files a write cut short left behind: a power cut
// before the rename leaves one, and nothing else ever removes it. It is called before
// anything writes in dir, since it cannot tell such a file from one a write still holds open.
// Only regular files named as WriteStream names them go; dir's subdirectories are not
// looked into. A missing dir has nothing to clean.
func CleanTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("clean temporary files: %w", err)
	}

	var errs []error

	for _, entry := range entries {
		if !entry.Type().IsRegular() || !isTempName(entry.Name()) {
			continue
		}

		err := os.Remove(filepath.Join(dir, entry.Name()))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("clean temporary files: %w", errors.Join(errs...))
	}

	return nil
}

// isTempName reports a name WriteStream gives its temporary file: a dot, the target's name,
// tempMark and os.CreateTemp's random digits.
func isTempName(name string) bool {
	cut := strings.LastIndex(name, tempMark)
	if cut < 2 || name[0] != '.' {
		return false
	}

	digits := name[cut+len(tempMark):]
	if digits == "" {
		return false
	}

	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}

func syncDir(dir string) {
	dirFile, err := os.Open(dir)
	if err != nil {
		return
	}

	_ = dirFile.Sync()  // see WriteStream
	_ = dirFile.Close() // read-only descriptor: nothing to flush
}
