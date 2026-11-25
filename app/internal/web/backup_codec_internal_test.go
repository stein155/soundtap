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
func TestCodecScriptRoundTrip(t *testing.T) { //nolint:paralleltest // it swaps the package-level cmdline path
	script := []byte("w 30 00 00\n")
	oldCmd := kernelCmdline

	t.Cleanup(func() { kernelCmdline = oldCmd })
	dir := t.TempDir()
	srv := new(Server)
	srv.homeDir = dir
	codecDir := srv.codecDir()

	kernelCmdline = filepath.Join(dir, "cmdline")

	err := os.WriteFile(kernelCmdline, []byte("root=ubi0:rootfs variant=test module_type=scm\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if exportCodec(codecDir) != nil {
		t.Fatal("exported a script the speaker does not have")
	}

	err = os.MkdirAll(codecDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(codecDir, "test_scm_normal.cfg")

	err = os.WriteFile(path, script, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	codec := exportCodec(codecDir)
	if codec == nil || codec.Name != "test_scm_normal.cfg" || !bytes.Equal(codec.Data, script) {
		t.Fatalf("export = %+v", codec)
	}

	kept, err := restoreCodec(codecDir, codec, wantedCodec)
	if kept || err != nil {
		t.Fatalf("overwrote a script the speaker has: %v %v", kept, err)
	}

	err = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}

	empty, err := restoreCodec(codecDir, &backupCodec{Name: codec.Name, Data: nil}, wantedCodec)
	if empty || err == nil {
		t.Fatalf("restored an empty script: %v %v", empty, err)
	}

	if ok, _ := restoreCodec(codecDir, &backupCodec{Name: "other_sm2_normal.cfg", Data: script}, wantedCodec); ok {
		t.Fatal("restored another board's script")
	}

	restored, err := restoreCodec(codecDir, codec, wantedCodec)
	if !restored || err != nil {
		t.Fatalf("restore = %v %v", restored, err)
	}

	if b, _ := os.ReadFile(path); !bytes.Equal(b, script) {
		t.Fatalf("restored %q", b)
	}
}
