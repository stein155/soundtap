// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package stu

import (
	"encoding/binary"
	"testing"
)

// The universal package is ours from the magic down: it wears BOSE because that is the one
// field stock authenticates, and every module name is ours. It must still be recognised as
// native, because Native is what gates the bootloader phase and the mainline environment
// markers -- the label a package wears for stock's benefit does not change whose it is.
// See docs/decisions/0008-one-universal-stu.md.
func TestScanAcceptsTheUniversalChain(t *testing.T) {
	f, err := open(t, buildChain(t, ExpectedSoundTap, nil))
	if err != nil {
		t.Fatalf("universal package rejected: %v", err)
	}
	// One package installs on either speaker, so its chain matches both models and
	// narrows to neither: which of them it may be written to is a question about the
	// hardware in front of it, and the mainline kernel name is what picks between them.
	if f.Target != "" {
		t.Errorf("target = %q, want no single target", f.Target)
	}
	for _, model := range []string{"st10", "wla"} {
		if !f.Modules[0].Installable(model) {
			t.Errorf("the universal chain must be installable on %s", model)
		}
	}
	if !f.Native {
		t.Error("the universal chain is ours and must scan as native")
	}
	if len(f.Modules) != len(ExpectedSoundTap) {
		t.Fatalf("got %d modules, want %d", len(f.Modules), len(ExpectedSoundTap))
	}
	// Module 0 is the installer and stock takes it by position, so it has to be first.
	if f.Modules[0].Name != "soundtap-installer" {
		t.Errorf("module 0 = %q, want soundtap-installer", f.Modules[0].Name)
	}
	for _, name := range []string{"soundtap-kernel", "soundtap-rootfs", "soundtap-spl", "soundtap-uboot"} {
		if _, ok := f.Module(name); !ok {
			t.Errorf("%s missing from the universal chain", name)
		}
	}
}

// The native RTCH chain has to keep working beside it: SoundTap's own web API still builds and
// installs that one, and nothing about the universal shape replaces it yet.
func TestTheNativeChainStillScans(t *testing.T) {
	f, err := open(t, buildChain(t, ExpectedWLANative, nil))
	if err != nil {
		t.Fatalf("native package rejected: %v", err)
	}
	if !f.Native || f.Target != "wla" {
		t.Errorf("native chain = target %q native %v", f.Target, f.Native)
	}
}

// The duo package names its kernels per model, which is how one file carries a kernel for
// each speaker. The chain still has to scan, still has to be native, and still has to be
// installable on both -- the suffix picks a payload, it does not pick a speaker.
func TestScanAcceptsTheDuoChain(t *testing.T) {
	f, err := open(t, buildChain(t, ExpectedSoundTapDuo, nil))
	if err != nil {
		t.Fatalf("duo package rejected: %v", err)
	}
	if f.Target != "" {
		t.Errorf("target = %q, want no single target", f.Target)
	}
	for _, model := range []string{"st10", "wla"} {
		if !f.Modules[0].Installable(model) {
			t.Errorf("the duo chain must be installable on %s", model)
		}
	}
	if !f.Native {
		t.Error("the duo chain is ours and must scan as native")
	}
	if f.Modules[0].Name != "soundtap-installer" {
		t.Errorf("module 0 = %q, want soundtap-installer", f.Modules[0].Name)
	}
}

// What each speaker takes out of that package, and what it leaves alone. The bootloaders
// are binky's and no ST10 may touch them, which is the same rule the C installer enforces.
func TestModuleForModelPicksThisSpeakersPayloads(t *testing.T) {
	for _, tc := range []struct {
		name, variant, want string
		mine                bool
	}{
		{"soundtap-kernel.rhino", "rhino", "soundtap-kernel", true},
		{"soundtap-kernel.binky", "rhino", "", false},
		{"soundtap-kernel.binky", "binky", "soundtap-kernel", true},
		{"soundtap-spl.binky", "rhino", "", false},
		{"soundtap-uboot.binky", "binky", "soundtap-uboot", true},
		{"soundtap-rootfs", "rhino", "soundtap-rootfs", true},
		{"soundtap-rootfs", "binky", "soundtap-rootfs", true},
		{"soundtap-installer", "spotty", "soundtap-installer", true},
		// Not a model suffix: a name that merely contains a dot is left alone.
		{"ubi.img", "rhino", "ubi.img", true},
	} {
		got, mine := ModuleForModel(tc.name, tc.variant)
		if mine != tc.mine || got != tc.want {
			t.Errorf("ModuleForModel(%q, %q) = %q, %v; want %q, %v",
				tc.name, tc.variant, got, mine, tc.want, tc.mine)
		}
	}
}

// Whether a package carries a SoundTap kernel is read off the kernel module's uImage header,
// and a universal package names that module per model. Both the sniff and the record ask
// BaseModuleName, or the package looks like a stock one and gets sent to a stock updater
// that is not there -- which is exactly how the first duo install was refused.
func TestBaseModuleNameSeesThroughAModelSuffix(t *testing.T) {
	for name, want := range map[string]string{
		"soundtap-kernel.rhino": "soundtap-kernel",
		"soundtap-kernel.binky": "soundtap-kernel",
		"linuxPatched.spotty":   "linuxPatched",
		"soundtap-rootfs":       "soundtap-rootfs",
		"ubi.img":               "ubi.img",
	} {
		if got := BaseModuleName(name); got != want {
			t.Errorf("BaseModuleName(%q) = %q, want %q", name, got, want)
		}
	}
}

// The regression the ST10 found on 2026-09-22: a package whose kernels are named per model
// scanned as not-mainline, and the install was refused as if it carried a stock Bose kernel.
// Both halves of that -- deciding to read the uImage header, and recording what it said --
// went through the module name, and a suffixed name matched neither.
func TestADuoPackageScansAsMainline(t *testing.T) {
	kernel := make([]byte, 0x200)
	binary.BigEndian.PutUint32(kernel, 0x27051956)
	copy(kernel[32:], "Linux-6.12 SoundTap st10")
	f, err := open(t, buildChainWith(t, ExpectedSoundTapDuo, nil, map[string][]byte{
		"soundtap-kernel.rhino": kernel,
		"soundtap-kernel.binky": kernel,
	}))
	if err != nil {
		t.Fatalf("duo package rejected: %v", err)
	}
	if !f.Mainline {
		t.Error("a package whose kernels are named per model must still scan as mainline")
	}
	if f.KernelTarget != "st10" {
		t.Errorf("kernel target = %q, want st10", f.KernelTarget)
	}
}

// The tri package is the duo plus the ST20's kernel, and all three models take it. The
// kernel identity recorded is the last kernel module's, spotty's, which the suffix gate in
// the web API looks past. Whether an ST20 may install it is the web API's question: only
// under our own boot chain.
func TestScanAcceptsTheTriChainForAllThree(t *testing.T) {
	st10 := make([]byte, 0x200)
	binary.BigEndian.PutUint32(st10, 0x27051956)
	copy(st10[32:], "Linux-6.12 SoundTap st10")
	st20 := make([]byte, 0x200)
	binary.BigEndian.PutUint32(st20, 0x27051956)
	copy(st20[32:], "Linub-6.12 SoundTap st20")
	f, err := open(t, buildChainWith(t, ExpectedSoundTapTri, nil, map[string][]byte{
		"soundtap-kernel.rhino":  st10,
		"soundtap-kernel.binky":  st10,
		"soundtap-kernel.spotty": st20,
	}))
	if err != nil {
		t.Fatalf("tri package rejected: %v", err)
	}
	if !f.Native || !f.Mainline {
		t.Errorf("tri chain native %v mainline %v, want both", f.Native, f.Mainline)
	}
	for model, want := range map[string]bool{"st10": true, "wla": true, "st20": true} {
		if got := f.Modules[0].Installable(model); got != want {
			t.Errorf("tri installable on %s = %v, want %v", model, got, want)
		}
	}
}
