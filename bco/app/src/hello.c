/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Stage 2 hello (hardware-proven 2026-10-02); -DTICK adds the 1 kHz IRQ tick test. */
#include "board.h"

#ifdef TICK
/* IRQ entry: select 0x90, EOI 0x98, line disable 0x190 (stock 9423, hw-proven). */
extern void irq_entry(void), fiq_entry(void);
static volatile unsigned ticks, others, last_other, fiqs;

/* cppcheck-suppress unusedFunction ; app.S calls it */
void irq_dispatch(void)
{
    unsigned n = INTC[0x90 / 4];
    if (n == TICK_IRQ) {
        ticks++;
    } else {
        last_other = n;
        /* Never let an unknown line storm: switch it off after 1000 hits. */
        if (++others > 1000 && n < 96)
            INTC[0x190 / 4 + (n >> 5)] = 1u << (n & 31);
    }
    INTC[0x98 / 4] = n;
}

/* Only counts: we do not know how a FIQ is acknowledged. Nonzero = mask FIQ on return. */
/* cppcheck-suppress unusedFunction ; app.S calls it */
unsigned fiq_dispatch(void)
{
    return ++fiqs >= 100;
}

static void tick(void)
{
    unsigned sctlr, next, said = 0;
    asm volatile("mrc p15, 0, %0, c1, c0, 0" : "=r"(sctlr));
    hex("sctlr ", sctlr);
    vector(0x18, (void *)0x00400100, irq_entry);
    vector(0x1c, (void *)0x00400110, fiq_entry);
    tick_start(1000);
    asm volatile("msr cpsr_c, #0x53"); /* SVC, IRQ on, FIQ off */
    for (next = (ticks / 1000 + 1) * 1000;;) {
        if (ticks >= next) {
            if (!said++)
                say("BCO tick: 1000 IRQs\r\n");
            hex("ticks ", ticks);
            if (others)
                hex("other irq ", last_other << 16 | (others & 0xffff));
            next += 1000;
        }
    }
}
#endif

#ifdef VECTEST
/* Which way do undef/SWI really go on hardware? (RTOS yield vanished, 2026-10-02) */
extern void vt_undef(void), vt_swi(void), vt_pabort(void), vt_dabort(void);

/* cppcheck-suppress unusedFunction ; app.S calls it */
void vt_hit(unsigned kind, unsigned lr)
{
    hex("vt hit kind ", kind);
    hex("vt hit lr ", lr);
}

static void slots(const char *when)
{
    say(when);
    for (unsigned slot = 0; slot < 0x20; slot += 4)
        hex(" ", INTC[slot / 4]);
}

static void vectest(void)
{
    slots("slots before\r\n");
    low_vectors(vt_undef, vt_swi, vt_pabort, vt_dabort);
    slots("slots after low_vectors\r\n");
    say("vt: udf\r\n");
    asm volatile(".word 0xe7f000f0" ::: "memory");
    say("vt: back from udf\r\n");
    asm volatile("swi 0" ::: "lr", "memory");
    say("vt: back from swi\r\n");
    say("BCO vectest done\r\n");
}
#endif

/* cppcheck-suppress unusedFunction ; app.S calls it */
void app_main(void)
{
    unsigned ahb = ahb_hz();
    matrix();
    UART[2] = 2 * ahb / BAUD - 2;
    say("BCO own app: UART hello\r\n");
#ifdef TICK
    tick();
#endif
#ifdef VECTEST
    vectest();
#endif
    for (;;) {}
}
