// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The backup carries the speaker's codec script, and a restore puts it back only
// on a speaker that lost it, and only the one this board loads.
func TestCodecScriptRoundTrip(t *testing.T) {
	script := []byte("w 30 00 00\n")
	oldCmd := kernelCmdline
	t.Cleanup(func() { kernelCmdline = oldCmd })
	dir := t.TempDir()
	codecDir := (&Server{homeDir: dir}).codecDir()
	kernelCmdline = filepath.Join(dir, "cmdline")
	if err := os.WriteFile(kernelCmdline, []byte("root=ubi0:rootfs variant=test module_type=scm\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exportCodec(codecDir) != nil {
		t.Fatal("exported a script the speaker does not have")
	}
	if err := os.MkdirAll(codecDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(codecDir, "test_scm_normal.cfg")
	if err := os.WriteFile(path, script, 0o600); err != nil {
		t.Fatal(err)
	}
	c := exportCodec(codecDir)
	if c == nil || c.Name != "test_scm_normal.cfg" || !bytes.Equal(c.Data, script) {
		t.Fatalf("export = %+v", c)
	}
	if ok, err := restoreCodec(codecDir, c, wantedCodec); ok || err != nil {
		t.Fatalf("overwrote a script the speaker has: %v %v", ok, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if ok, err := restoreCodec(codecDir, &backupCodec{Name: c.Name}, wantedCodec); ok || err == nil {
		t.Fatalf("restored an empty script: %v %v", ok, err)
	}
	if ok, _ := restoreCodec(codecDir, &backupCodec{Name: "other_sm2_normal.cfg", Data: script}, wantedCodec); ok {
		t.Fatal("restored another board's script")
	}
	if ok, err := restoreCodec(codecDir, c, wantedCodec); !ok || err != nil {
		t.Fatalf("restore = %v %v", ok, err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, script) {
		t.Fatalf("restored %q", b)
	}
}
