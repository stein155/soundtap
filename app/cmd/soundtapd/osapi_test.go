// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/osapi"
)

// An override that outlived the OS it was uploaded to must not start.
func TestCheckOSAPIRefusesOlderOS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "os-api")
	if err := os.WriteFile(path, []byte(strconv.Itoa(osapi.Need()-1)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if checkOSAPI(path) == nil {
		t.Error("started on an OS below the level this build needs")
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(osapi.Need())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkOSAPI(path); err != nil {
		t.Errorf("refused a matching OS: %v", err)
	}
}

// The reboot hook turns the sound off before the reboot, and leaves everything on disk
// to the next boot: only init.d/soundtap confirms an uploaded app (web/firmware's
// TestRebootLeavesAnUploadOnTrial runs the route).
func TestRebootSpeakerSilencesFirst(t *testing.T) {
	var order []string
	hook := rebootSpeaker(func() { order = append(order, "audio off") }, func() error {
		order = append(order, "reboot")
		return errors.New("no reboot here")
	})
	if err := hook(); err == nil || strings.Join(order, ",") != "audio off,reboot" {
		t.Fatalf("order %q, err %v", order, err)
	}
}
