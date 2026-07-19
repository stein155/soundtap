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
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Write marshals v as indented JSON and atomically replaces path with it, creating
// path with the given file mode.
func Write(path string, v any, mode fs.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(path, data, mode)
}

// WriteFile atomically replaces path with data, creating path with the given file
// mode: temp file in the same directory, fsync, rename, fsync of the directory.
func WriteFile(path string, data []byte, mode fs.FileMode) error {
	return WriteStream(path, mode, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// WriteStream is WriteFile for content too large to hold in memory: write streams it
// into the temporary file, and path is replaced only when write returns nil and the
// file is on disk. A failure leaves path as it was and no temporary file behind.
func WriteStream(path string, mode fs.FileMode, write func(io.Writer) error) error {
	// fsync the data before the rename, else power loss (a speaker's normal
	// off-switch) can journal the rename ahead of the data blocks and leave a
	// zero-length/truncated target — the very corruption this package prevents.
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = f.Chmod(mode)
	if err == nil {
		err = write(f)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // best effort: the error that matters is err
		return err
	}
	// fsync the directory so the rename itself survives power loss. Its error is not
	// returned: the rename is done and the new content is what path holds now, so a
	// caller told "failed" would keep old state in memory over a file that has the new.
	syncDir(filepath.Dir(path))
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()  // see WriteStream
		_ = d.Close() // read-only descriptor: nothing to flush
	}
}
