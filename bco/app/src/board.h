/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#pragma once
/* DM870 board basics shared by hello and the RTOS. Evidence: bco-hw-map and the
 * hardware runs recorded in README (2026-10-02). */
#define UART ((volatile unsigned *)0xb1001000)
#define XTAL_HZ 24000000u /* live read 2026-10-02: UART1 +0x08 = 0x56a = 2*80 MHz/115200-2, so PLL 480 MHz, AHB 80 MHz */
#define BAUD 115200u
#define INTC ((volatile unsigned *)0xffff0000)
#define TICK_IRQ 6

/* Stock stage 2 programs the switch matrix before anything else; write only the live-proven
 * A..E words (hw-map section 3). F/USB/Ethernet/WILD are left as BFL left them. */
static inline void matrix(void)
{
    volatile unsigned *m = (void *)0xe0013000;
    static const unsigned w[5] = {0x102, 0x703, 0xf01, 0x706, 0x103};
    for (unsigned i = 0; i < 5; i++)
        m[i] = w[i];
}

/* AHB clock from the live clock block, same formula as stock 0x6015b510; 24 MHz fallback. */
static inline unsigned ahb_hz(void)
{
    const volatile unsigned *r = (void *)0xe0007000;
    unsigned pll = r[0], n = (pll >> 16) & 0x1f, m = (pll >> 8) & 0xff, div = r[4] & 0x3f;
    if ((r[1] & 7) != 1 || (pll >> 31) || !n || !div)
        return 24000000u;
    return (XTAL_HZ / n * m >> (pll & 3)) / div;
}

static inline void say(const char *s)
{
    volatile unsigned char *thr = (void *)(0xb1001e80);
    const volatile unsigned *status = (void *)(0xb1001f44);
    while (*s) {
        /* status bit2 is not "busy" on hardware (it never clears), so this is a
         * fixed ~1 ms wait per char (> 87 us char time); poll the real ready bit once known. */
        unsigned budget = 3000;
        while ((*status & 4) && --budget) {}
        *thr = *s++;
    }
}

static inline void hex(const char *tag, unsigned v)
{
    char b[11];
    b[8] = '\r', b[9] = '\n', b[10] = 0;
    for (int i = 7; i >= 0; i--, v >>= 4)
        b[i] = "0123456789abcdef"[v & 15];
    say(tag);
    say(b);
}

/* Point vector slot 0x18 (IRQ) or 0x1c (FIQ) at fn through an SRAM trampoline, like stock
 * does; vectors are high (SCTLR.V = 1 at entry, hw 2026-10-02). Slots 0x04..0x10 are NOT
 * separate: they read back one shared value (hw 2026-10-02), so use low_vectors() for them. */
static inline void vector(unsigned slot, volatile unsigned *tramp, void (*fn)(void))
{
    tramp[0] = 0xe51ff004; /* ldr pc, [pc, #-4] */
    tramp[1] = (unsigned)fn;
    INTC[slot / 4] = 0xea000000u | ((((unsigned)tramp - (0xffff0000u + slot + 8)) >> 2) & 0xffffff);
}

/* Undef/SWI/prefetch/data abort. Slots 0x04..0x10 are one shared register that drops the
 * low 5 bits of the branch offset (wrote 0xea103ffe, read 0xea103fe0; BFL's own 0xea105160),
 * so exception N lands at B + 4N with B = 0x08 mod 0x80 (BFL: B = 0x404588). Hw 2026-10-02.
 * Stock's own write (target 0x400004) is masked to 0x3fff8c: harmless only because stock
 * never takes undef/SWI. Our table: B = 0x400088, ldr-pc entries, literals at B + 4(N + 7). */
#define LOW_VECTORS 0x00400088u
static inline void low_vectors(void (*undef)(void), void (*swi)(void),
                               void (*pabort)(void), void (*dabort)(void))
{
    volatile unsigned *t = (void *)LOW_VECTORS;
    t[0] = 0xeafffffe; /* reset: b . */
    for (unsigned i = 1; i <= 4; i++)
        t[i] = 0xe59ff014; /* ldr pc, [pc, #0x14] -> t[i + 7] */
    t[8] = (unsigned)undef, t[9] = (unsigned)swi, t[10] = (unsigned)pabort, t[11] = (unsigned)dabort;
    /* cppcheck-suppress duplicateExpression ; register offset 0x04 in words, written as every other offset */
    INTC[0x04 / 4] = 0xea000000u | (((LOW_VECTORS - 0xffff0008u) >> 2) & 0xffffff);
}

/* INTC on (stock 0x600fdc48 order; without it 0x90 reads 0 and nothing fires), every line
 * off, then timer1 = IRQ 6 at hz (stock 0x600a59f4 + 0x60309570(1, 1); bit5 masks its IRQ). */
static inline void tick_start(unsigned hz)
{
    unsigned sctlr;
    asm volatile("mrc p15, 0, %0, c1, c0, 0" : "=r"(sctlr));
    asm volatile("mcr p15, 0, %0, c1, c0, 0" : : "r"(sctlr | 1u << 13));
    INTC[0x84 / 4] = 0xf0000;
    INTC[0x80 / 4] = 0x100000;
    INTC[0x84 / 4] = 1;
    for (unsigned i = 0; i < 3; i++)
        INTC[0x190 / 4 + i] = ~0u;
    INTC[0x204 / 4] = 2;
    INTC[0x214 / 4] = 0;
    INTC[0x204 / 4] = 0x200;
    INTC[0x224 / 4] = ahb_hz() / hz;
    INTC[0x180 / 4] = 1u << TICK_IRQ;
    INTC[0x204 / 4] = 0x20;
    INTC[0x200 / 4] = 2;
}
