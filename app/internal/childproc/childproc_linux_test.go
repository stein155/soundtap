// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build linux

package childproc

import (
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
	"testing"
)

func TestRunAsKeepsTheParentDeathSignal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("RunAs only switches users as root")
	}

	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}

	cmd := exec.Command("true")
	DieWithParent(cmd)

	if err := RunAs(cmd, me.Username); err != nil {
		t.Fatal(err)
	}

	a := cmd.SysProcAttr
	if a.Pdeathsig != syscall.SIGTERM {
		t.Fatalf("Pdeathsig = %v after RunAs", a.Pdeathsig)
	}

	if a.Credential == nil || strconv.Itoa(int(a.Credential.Uid)) != me.Uid || len(a.Credential.Groups) != 0 {
		t.Fatalf("Credential = %+v, want uid %s and no extra groups", a.Credential, me.Uid)
	}

	if RunAs(exec.Command("true"), "no-such-soundtap-user") == nil {
		t.Fatal("RunAs accepted an unknown user")
	}
}
