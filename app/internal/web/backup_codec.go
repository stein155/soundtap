// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// Modes of the restored codec script and its directory.
const (
	codecDirMode  = 0o755
	codecFileMode = 0o644
)

// kernelCmdline is read for the variant and module type, which name the script init loads.
//
// codecSizeError is a restored codec script whose size is out of bounds.
type codecSizeError struct {
	name string
	size int
}

func (e *codecSizeError) Error() string {
	return fmt.Sprintf("codec script %q has %d bytes", e.name, e.size)
}

//nolint:gochecknoglobals // a variable so tests can replace it
var kernelCmdline = "/proc/cmdline"

// wantedCodec reports whether name is the script init loads on this board:
// <variant>_<module_type>_normal.cfg, as rhino_sm2 on an ST10 or spotty_scm on an ST20.
func wantedCodec(name string) bool {
	cmdline, err := os.ReadFile(kernelCmdline)
	if err != nil {
		return false
	}

	var variant, module string

	for f := range strings.FieldsSeq(string(cmdline)) {
		if v, ok := strings.CutPrefix(f, "variant="); ok {
			variant = v
		} else if v, ok := strings.CutPrefix(f, "module_type="); ok {
			module = v
		}
	}

	return variant != "" && module != "" && name == variant+"_"+module+"_normal.cfg"
}

// codecMaxBytes bounds a restored script; the ST20's is 344605 bytes.
const codecMaxBytes = 1 << 20

// codecDir is where init keeps this speaker's codec script (ADR 0012): codec under home.
func (s *Server) codecDir() string { return filepath.Join(s.homeDir, "codec") }

// backupCodec carries the speaker's own codec script, so a speaker that lost it gets its
// sound back from a restore. It is the owner's copy of what their speaker had.
type backupCodec struct {
	Name string `json:"name"`
	Data []byte `json:"data"` // base64 in the file
}

// exportCodec is this speaker's codec script, or nil when it has none it knows.
func exportCodec(codecDir string) *backupCodec {
	names, _ := filepath.Glob(filepath.Join(codecDir, "*_normal.cfg"))
	for _, path := range names {
		b, err := os.ReadFile(path)
		if err == nil && len(b) > 0 {
			return &backupCodec{Name: filepath.Base(path), Data: b}
		}
	}

	return nil
}

// restoreCodec puts a backed-up script back when this speaker has none, and only the one
// init would load here: another model's script never lands. It takes effect at the next
// boot, which loads the codec. restored is false when there was nothing to do.
// It reports whether a script was restored.
func restoreCodec(codecDir string, codec *backupCodec, wanted func(string) bool) (bool, error) {
	if codec == nil || !wanted(codec.Name) {
		return false, nil
	}

	if len(codec.Data) == 0 || len(codec.Data) > codecMaxBytes {
		return false, &codecSizeError{name: codec.Name, size: len(codec.Data)}
	}

	path := filepath.Join(codecDir, codec.Name)

	_, err := os.Stat(path)
	if !errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	err = os.MkdirAll(codecDir, codecDirMode)
	if err != nil {
		return false, fmt.Errorf("codec directory: %w", err)
	}

	err = atomicjson.WriteFile(path, codec.Data, codecFileMode)
	if err != nil {
		return true, fmt.Errorf("codec script %s: %w", codec.Name, err)
	}

	return true, nil
}
