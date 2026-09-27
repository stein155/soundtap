/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include <stdint.h>

/*
 * Our own, Bose-free BCO first stage (ADR 0031). BFL checks the image CRC, copies it from +0x18
 * on to SRAM 0x40da00 and runs it. It brings up the clock and SDRAM itself, then copies stage 2
 * (the `link` app) from NOR into SDRAM and jumps to it.
 *
 * The register addresses and values below are hardware facts read from the stock first stage
 * (App_BD 9423) and from this board; the code is ours (ADR 0031). The exact bring-up write
 * sequence is pinned in first-init-trace.json and checked, image against stock, by
 * check-first-init.py.
 */

#define SRAM_BASE 0x0040da00u /* where BFL puts image +0x18 (bco-pack's second record) */
#define NOR_BASE 0x40000000u
#define SDRAM_BASE 0x60000000u

#define MMIO(a) (*(volatile uint32_t *)(uintptr_t)(a))

static void stop(void)
{
    for (;;) {}
}

/* Busy-wait; counts mirror the stock first stage's settle loops. */
static void spin(uint32_t n)
{
    while (n--)
        __asm__ volatile("" ::: "memory");
}

static uint32_t udiv(uint32_t n, uint32_t d)
{
    uint32_t q = 0, r = 0;
    for (int i = 31; i >= 0; i--) {
        r = r << 1 | (n >> i & 1);
        if (r >= d)
            r -= d, q |= 1u << i;
    }
    return q;
}

/* SDRAM controller command (0xe001d004 bits 19..20); the controller clears them when done. */
static void sdram_cmd(uint32_t base, uint32_t cmd)
{
    MMIO(0xe001d004) = (base & ~0x180000u) | cmd;
    for (uint32_t n = 0; (MMIO(0xe001d004) & 0x180000u) && n < 1000000; n++) {}
}

/*
 * Clock (0xe0007000): 24 MHz crystal -> PLL 480 MHz, ARM 240 MHz, AHB 80 MHz.
 * SDRAM controller (0xe001d000): reset, program timing/geometry, enable, calibrate the read
 * delay line, run the mode-register commands. Values are the ones the stock first stage uses
 * for this fixed clock.
 */
static void bringup(void)
{
    MMIO(0xe0007010) = 0x00000006u;
    MMIO(0xe0007014) = 0x22221223u; /* clock dividers */
    /* If the CPU already runs from a PLL set differently (source 1, multiplier/divider bits
     * 0x1fff00 differ), go back to the crystal before reprogramming it, as stock does. */
    if ((MMIO(0xe0007004) & 7) == 1 && ((MMIO(0xe0007000) ^ 0x00022800u) & 0x001fff00u)) {
        MMIO(0xe0007004) = 0;
        MMIO(0xe0007018) = 1;
    }
    MMIO(0xe0007000) = 0x00022800u; /* PLL: enable, x40, /2 */
    MMIO(0xe0007018) = 0x00000001u;
    MMIO(0xe0007004) = 0x00000001u; /* PLL source select */
    MMIO(0xe0007020) = 0x00008000u; /* clock-cycle enable */
    spin(0x31);
    MMIO(0xe0007024) = 0x00008000u;

    MMIO(0xe001dff0) = 0x00000001u; /* controller soft reset */
    MMIO(0xe001dff0) = 0x00000001u;
    MMIO(0xe001d00c) = 0x00000040u;
    spin(0x1770);
    MMIO(0xe001d004) = 0xfda07c60u; /* refresh + timing */
    MMIO(0xe001d008) = 0x00460000u; /* geometry */
    MMIO(0xe001d00c) = 0x01000400u;
    MMIO(0xe001d000) = 0x00000001u; /* go */

    /* Delay-line measurement: start with 0xe00070f0 (bits 8..9 = attempt), done at 0xe0007844
     * bit 31, bit 29 = retry. Fields: a = bits 16..21, b = bits 8..11, c = bits 0..3. On the
     * ST20 it reads 0xa00d0f07, then 0x800d0703. */
    uint32_t m = 0;
    for (uint32_t try = 0; try < 4; try++) {
        MMIO(0xe00070f0) = 0x80000006u | try << 8;
        for (uint32_t n = 0; !((m = MMIO(0xe0007844)) & 0x80000000u) && n < 1000000; n++) {}
        if (!(m & 0x20000000u))
            break;
    }
    uint32_t a = m >> 16 & 0x3f, b = m >> 8 & 0xf, c = m & 0xf;
    /* Read delay taps: (100a + 100c/b) * PLL MHz (480) * 168 / 4800000; 0x16 on the ST20. */
    uint32_t taps = b ? udiv((100 * a + udiv(100 * c, b)) * 168, 10000) : 0;
    uint32_t dl = MMIO(0xe000701c) & ~0x100u;
    MMIO(0xe000701c) = dl;
    dl = (dl & ~0x3fu) | (taps & 0x3f);
    MMIO(0xe000701c) = dl;
    MMIO(0xe000701c) = dl | 0x100u;

    uint32_t t = MMIO(0xe001d004);
    sdram_cmd(t, 0x080000u);
    sdram_cmd(t, 0x100000u);
    sdram_cmd(t, 0x100000u);
    sdram_cmd(t, 0x180000u);
    *(volatile uint32_t *)0x70000004u = *(volatile uint32_t *)0x70000000u;
    MMIO(0xe001d010) = 0x25u;
    MMIO(0xe001d00c) = 0x25u;
}

void first_stage(void)
{
    bringup();

    /* SRAM_BASE holds the second record: {branch, magic, dest, source, length, Fletcher sum}. */
    const volatile uint32_t *record = (void *)(uintptr_t)SRAM_BASE;
    const volatile uint16_t *source = (void *)(uintptr_t)(NOR_BASE + record[3]);
    unsigned words = record[4] / 2;
    uint32_t a = 0xffff, b = 0xffff;
    for (unsigned start = 0; start < words; start += 360) {
        unsigned end = start + 360 < words ? start + 360 : words;
        for (unsigned i = start; i < end; ++i) {
            a += source[i];
            b += a;
        }
        a = (a & 0xffff) + (a >> 16);
        b = (b & 0xffff) + (b >> 16);
    }
    a = (a & 0xffff) + (a >> 16);
    b += b >> 16;
    if ((a | (b << 16)) != record[5])
        stop();
    volatile uint16_t *dest = (void *)(uintptr_t)(SDRAM_BASE + record[2]);
    for (unsigned i = 0; i < words; ++i)
        dest[i] = source[i];
    for (unsigned i = 0; i < words; ++i) /* SDRAM must read back what we wrote */
        if (dest[i] != source[i])
            stop();
    ((void (*)(void))(uintptr_t)(SDRAM_BASE + record[2]))();
    stop();
}
