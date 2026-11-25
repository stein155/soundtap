// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/stu"
)

// RemoveStaleStaging clears what an interrupted install left behind. The deferred removal
// covers a request that returns; it does not cover a speaker that reboots or is killed
// mid-write, and on a volume this small one orphan is the difference between an install
// and "no space left on device".
func RemoveStaleStaging(dir string) {
	if dir == "" {
		dir = os.TempDir()
	}

	for _, prefix := range []string{"soundtap-kernel-", "soundtap-mlo-", "soundtap-uboot-"} {
		matches, err := filepath.Glob(filepath.Join(dir, prefix+"*"))
		if err != nil {
			continue
		}

		for _, match := range matches {
			_ = os.Remove(match)
		}
	}
}

// CheckKernelForTarget validates Bose fixsig behavior and the model suffix in the
// appended-DTB mainline kernel. This separates ST10 from WLA's shared module chain.
//
//nolint:revive // flag-parameter: suffixed is what the package said about this kernel, not a mode.
func CheckKernelForTarget(path, target string, suffixed bool) error {
	image, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("staged kernel: %w", err)
	}

	err = flash.ValidateKernel(image)
	if err != nil {
		return fmt.Errorf("staged kernel: %w", err)
	}

	got := stu.MainlineTarget(image)
	if got == "" {
		// A stock Bose kernel names no model, so there is nothing to match: the
		// package's own module chain already said which models it is for. This
		// endpoint is every model's way back to stock. ValidateKernel above still
		// holds, so the signature and the uImage geometry are checked either way.
		return nil
	}

	if got != target && !suffixed {
		return fmt.Errorf("%w %s, not %s", errKernelTargets, got, target)
	}

	return nil
}

// The refusals of staging, with their values around the sentinel.
var (
	errKernelTargets = errors.New("kernel targets")
	errCannotWrite   = errors.New("this image cannot write NAND")
)

// UBIBodySize returns how many bytes of a rootfs module are the UBI image: the
// module as declared, or that minus the overlap it shares with the next header,
// whichever is a whole number of eraseblocks.
func UBIBodySize(size, erase int64) int64 {
	switch {
	case erase <= 0, size%erase == 0:
		return size
	case (size-stu.Overlap)%erase == 0:
		return size - stu.Overlap
	}
	// Neither length is a whole number of eraseblocks. Hand over what the package
	// declares and let ubiformat be the one to refuse it: this function is here to
	// choose between two right answers, not to invent a new way to say no.
	return size
}

// TruncatingWriter passes the first left bytes through and silently drops the
// rest. The STU scanner streams a module's whole declared length, overlap
// included; ubiformat must see only the image.
type TruncatingWriter struct {
	W    io.Writer
	Left int64
}

// Write passes on what is left of the first Left bytes and reports all of p written.
func (t *TruncatingWriter) Write(chunk []byte) (int, error) {
	size := len(chunk)
	if int64(size) > t.Left {
		chunk = chunk[:t.Left]
	}

	if len(chunk) > 0 {
		written, err := t.W.Write(chunk)

		t.Left -= int64(written)
		if err != nil {
			return written, fmt.Errorf("writing the rootfs image: %w", err)
		}

		if written < len(chunk) {
			return written, io.ErrShortWrite
		}
	}

	return size, nil
}

// ToolsRefusal names the NAND tools this image lacks, or returns nil.
func ToolsRefusal(missing []string) error {
	if len(missing) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %s missing. Install with a USB stick, as the install guide describes",
		errCannotWrite, strings.Join(missing, ", "))
}
