// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"errors"
	"fmt"
	"math/bits"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// The AM335x RTC: SCRATCH2 holds U-Boot's boot count, and CM_RTC clocks the module. Reached
// with the RTC module unclocked, the SCRATCH2 write is an external abort that kills
// soundtapd (2026-09-28).
const (
	rtcBase      = 0x44E3E000
	rtcScratch2  = 0x68
	rtcKick0     = 0x6C
	rtcKick1     = 0x70
	cmBase       = 0x44E00000
	rtcClkCtrl   = 0x800
	rtcClkStCtrl = 0x804
)

// Our U-Boot counts boots in SCRATCH2 through CONFIG_BOOTCOUNT_AM33XX:
// CONFIG_SYS_BOOTCOUNT_MAGIC's top half over the count, stored big-endian
// (CONFIG_SYS_BOOTCOUNT_BE), so Linux reads it byte-swapped: 0x050001B0 is
// count 5 (measured on the WLA, 2026-10-08).
const bootCountMagic = 0xB001

// bootCountMask is the count's half of SCRATCH2, under the magic.
const bootCountMask = 0xffff

// The /dev/mem mappings: one page each, and how many milliseconds the RTC module gets to
// leave idle once clocked.
const (
	mapSize        = 0x1000
	clockWaitTries = 100
)

// errRTCIdle means the RTC module did not come out of idle once clocked.
var errRTCIdle = errors.New("RTC module stays idle")

// bootCount decodes SCRATCH2 as U-Boot's boot count; ok is false when it holds none.
func bootCount(scratch2 uint32) (uint32, bool) {
	value := bits.ReverseBytes32(scratch2)
	if value>>16 != bootCountMagic {
		return 0, false
	}

	return value & bootCountMask, true
}

// ConfirmBootCount resets U-Boot's boot count once this boot has proven itself. U-Boot
// keeps it in SCRATCH2, and for a selection not confirmed yet in the environment as well
// (envCount, PendingBootCount), since a cold reset clears SCRATCH2; it boots by the larger
// of the two. A count past BootLimit means U-Boot ran altbootcmd, so the running slot is
// not the one the environment selects (ADR 0010). settle runs first, told whether that
// happened: it selects the running slot then, and clears the environment's count either
// way. SCRATCH2 is cleared only once settle succeeded. A confirmed boot reboots cold
// again: init.d/soundtap reboots warm while U-Boot counts.
func ConfirmBootCount(envCount uint32, settle func(fallback bool) error) error {
	err := withRTC(func(scratch2 *uint32) error { return settleBootCount(scratch2, envCount, settle) })
	if err != nil {
		return err
	}
	// Not an error for the confirmation, which is done: without the file (a kernel
	// without reboot mode) the next reboot is warm and U-Boot counts from the cleared
	// SCRATCH2 again, as on a board without the count.
	_ = os.WriteFile(rebootModePath, []byte("cold"), 0)

	return nil
}

// rebootModePath is the kernel's reboot mode, which init.d/soundtap's set_reboot_mode sets.
const rebootModePath = "/sys/kernel/reboot/mode"

// RTC is where U-Boot keeps its boot count, SCRATCH2: read, and cleared. Clearing also
// makes the next reboot cold again, as ConfirmBootCount does.
type RTC interface {
	ReadScratch2() (uint32, error)
	ClearScratch2() error
}

// ConfirmBootCountVia is ConfirmBootCount over rtc. soundtapd runs without /dev/mem and
// reaches SCRATCH2 through soundtap-root (ADR 0066). settle runs between the read and the
// clear, as under ConfirmBootCount, and a failed settle leaves SCRATCH2 as it was.
func ConfirmBootCountVia(rtc RTC) func(envCount uint32, settle func(fallback bool) error) error {
	return func(envCount uint32, settle func(fallback bool) error) error {
		scratch2, err := rtc.ReadScratch2()
		if err != nil {
			return fmt.Errorf("reading SCRATCH2: %w", err)
		}

		err = settleBootCount(&scratch2, envCount, settle)
		if err != nil {
			return err
		}

		return rtc.ClearScratch2()
	}
}

// DevRTC reaches SCRATCH2 through /dev/mem, which only root opens: soundtap-root's side of
// ConfirmBootCountVia.
type DevRTC struct{}

// ReadScratch2 reads SCRATCH2 with the RTC module clocked.
func (DevRTC) ReadScratch2() (uint32, error) {
	var value uint32

	err := withRTC(func(scratch2 *uint32) error {
		value = *scratch2

		return nil
	})

	return value, err
}

// ClearScratch2 clears SCRATCH2 and puts the reboot mode back to cold.
func (DevRTC) ClearScratch2() error {
	err := withRTC(func(scratch2 *uint32) error {
		*scratch2 = 0

		return nil
	})
	if err != nil {
		return err
	}

	_ = os.WriteFile(rebootModePath, []byte("cold"), 0) // as in ConfirmBootCount

	return nil
}

// settleBootCount runs settle with whether U-Boot fell back, and clears scratch2 once it
// succeeded.
func settleBootCount(scratch2 *uint32, envCount uint32, settle func(fallback bool) error) error {
	count, _ := bootCount(*scratch2)

	err := settle(max(count, envCount) > BootLimit)
	if err != nil {
		return err
	}

	*scratch2 = 0

	return nil
}

// withRTC maps the RTC with its module clocked and unlocked, and hands use SCRATCH2.
func withRTC(use func(scratch2 *uint32) error) error {
	mem, err := os.OpenFile("/dev/mem", os.O_RDWR|os.O_SYNC, 0)
	if err != nil {
		return fmt.Errorf("RTC: %w", err)
	}
	defer mem.Close()

	clockRegs, err := syscall.Mmap(int(mem.Fd()), cmBase, mapSize,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("map CM_RTC: %w", err)
	}
	defer func() { _ = syscall.Munmap(clockRegs) }()

	*(*uint32)(unsafe.Pointer(&clockRegs[rtcClkStCtrl])) = 2 // SW_WKUP
	clk := (*uint32)(unsafe.Pointer(&clockRegs[rtcClkCtrl]))

	*clk = 2 // MODULEMODE enable
	for i := 0; *clk&0x30000 != 0; i++ {
		if i == clockWaitTries {
			return fmt.Errorf("%w: CM_RTC_RTC_CLKCTRL %#x", errRTCIdle, *clk)
		}

		time.Sleep(time.Millisecond)
	}

	regs, err := syscall.Mmap(int(mem.Fd()), rtcBase, mapSize, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("map RTC: %w", err)
	}
	defer func() { _ = syscall.Munmap(regs) }()

	reg := func(off int) *uint32 { return (*uint32)(unsafe.Pointer(&regs[off])) }
	*reg(rtcKick0) = 0x83E70B13
	*reg(rtcKick1) = 0x95A4F1E0
	// Any other value in KICK0 locks the registers again, as the kernel's rtc-omap leaves them.
	defer func() { *reg(rtcKick0) = 0 }()

	return use(reg(rtcScratch2))
}
