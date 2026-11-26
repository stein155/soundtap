// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package childproc_test

import (
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
	"testing"

	"github.com/stein155/soundtap/app/internal/childproc"
)

func TestRunAsKeepsTheParentDeathSignal(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("RunAs only switches users as root")
	}

	current, err := user.Current()
	if err != nil {
		t.Skip(err)
	}

	cmd := exec.CommandContext(t.Context(), "true")
	childproc.DieWithParent(cmd)

	err = childproc.RunAs(cmd, current.Username)
	if err != nil {
		t.Fatal(err)
	}

	attr := cmd.SysProcAttr
	if attr.Pdeathsig != syscall.SIGTERM {
		t.Fatalf("Pdeathsig = %v after RunAs", attr.Pdeathsig)
	}

	if attr.Credential == nil || strconv.Itoa(int(attr.Credential.Uid)) != current.Uid ||
		len(attr.Credential.Groups) != 0 {
		t.Fatalf("Credential = %+v, want uid %s and no extra groups", attr.Credential, current.Uid)
	}

	if childproc.RunAs(exec.CommandContext(t.Context(), "true"), "no-such-soundtap-user") == nil {
		t.Fatal("RunAs accepted an unknown user")
	}
}
