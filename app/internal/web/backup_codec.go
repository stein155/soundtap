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

// kernelCmdline is read for the variant and module type, which name the script init loads.
var kernelCmdline = "/proc/cmdline"

// wantedCodec reports whether name is the script init loads on this board:
// <variant>_<module_type>_normal.cfg, as rhino_sm2 on an ST10 or spotty_scm on an ST20.
func wantedCodec(name string) bool {
	b, err := os.ReadFile(kernelCmdline)
	if err != nil {
		return false
	}
	var variant, module string
	for _, f := range strings.Fields(string(b)) {
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
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return &backupCodec{Name: filepath.Base(path), Data: b}
		}
	}
	return nil
}

// restoreCodec puts a backed-up script back when this speaker has none, and only the one
// init would load here: another model's script never lands. It takes effect at the next
// boot, which loads the codec. restored is false when there was nothing to do.
func restoreCodec(codecDir string, c *backupCodec, wanted func(string) bool) (restored bool, err error) {
	if c == nil || !wanted(c.Name) {
		return false, nil
	}
	if len(c.Data) == 0 || len(c.Data) > codecMaxBytes {
		return false, fmt.Errorf("codec script %q has %d bytes", c.Name, len(c.Data))
	}
	path := filepath.Join(codecDir, c.Name)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err := os.MkdirAll(codecDir, 0o755); err != nil {
		return false, err
	}
	return true, atomicjson.WriteFile(path, c.Data, 0o644)
}
