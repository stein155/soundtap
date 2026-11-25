/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include "board.h"

#define MUSB ((volatile unsigned char *)0xb0000000)
#define MUSB_IRQ 8

#ifdef USB_IRQ
extern void irq_entry(void);
static volatile unsigned irq_count, bus_events, tx_events, rx_events, other_irqs;

void irq_dispatch(void)
{
    unsigned n = INTC[0x90 / 4];
    if (n == MUSB_IRQ) {
        unsigned tx = *(volatile unsigned short *)(MUSB + 0x02);
        unsigned rx = *(volatile unsigned short *)(MUSB + 0x04);
        unsigned bus = MUSB[0x0a];
        irq_count++;
        bus_events |= bus;
        tx_events |= tx;
        rx_events |= rx;
    } else if (n < 96) {
        other_irqs++;
        INTC[0x190 / 4 + (n >> 5)] = 1u << (n & 31);
    }
    INTC[0x98 / 4] = n;
}

static void irq_start(void)
{
    unsigned sctlr;
    vector(0x18, (void *)0x00400100, irq_entry);
    INTC[0x84 / 4] = 0xf0000;
    INTC[0x80 / 4] = 0x100000;
    INTC[0x84 / 4] = 1;
    for (unsigned i = 0; i < 3; i++)
        INTC[0x190 / 4 + i] = ~0u;
    INTC[0x180 / 4] = 1u << MUSB_IRQ;
    asm volatile("mrc p15, 0, %0, c1, c0, 0" : "=r"(sctlr));
    asm volatile("mcr p15, 0, %0, c1, c0, 0" : : "r"(sctlr | 1u << 13));
    asm volatile("msr cpsr_c, #0x53"); /* SVC, IRQ on, FIQ off */
}
#endif

#ifdef USB_CONNECT
/* ~1.25 ms per 3000 volatile reads (the say() budget, measured on hardware). */
static void ms(unsigned n)
{
    for (volatile unsigned i = 0; i < n * 2400; i++) {}
}
#endif

void app_main(void)
{
    unsigned ahb = ahb_hz();
    matrix();
    UART[2] = 2 * ahb / BAUD - 2;

    /* MUSB registers are byte-wide. Step 1 is deliberately read-only. */
    hex("MUSB POWER ", MUSB[0x01]);
    hex("MUSB DEVCTL ", MUSB[0x60]);
#ifdef USB_IRQ
    irq_start();
#ifdef USB_CONNECT
    /* Step 3: enable the controller interrupts, then connect (stock 0x601c2b74: HSENAB, then
     * SOFTCONN). Nothing else is touched: no endpoints, no FIFO, EP0 stays unanswered, so the
     * SCM should see a device that resets and enumerates badly, which is the proof wanted. */
    *(volatile unsigned short *)(MUSB + 0x06) = 0xffff; /* INTRTXE */
    *(volatile unsigned short *)(MUSB + 0x08) = 0xfffe; /* INTRRXE */
    MUSB[0x0b] = 0xf7;                                  /* INTRUSBE */
    MUSB[0x01] = 0x20 | 0x40;                           /* HSENAB | SOFTCONN */
    for (unsigned t = 0; t < 20000; t++)                /* ~20 s window for the SCM to react */
        ms(1);
    hex("MUSB POWER after ", MUSB[0x01]);
    hex("MUSB DEVCTL after ", MUSB[0x60]);
    MUSB[0x01] = 0x20; /* SOFTCONN off again */
    say("USB connect done\r\n");
#else
    unsigned budget = 10000000;
    while (!irq_count && --budget) {}
#endif
    hex("USB IRQ count ", irq_count);
    hex("USB IRQ bus ", bus_events);
    hex("USB IRQ tx ", tx_events);
    hex("USB IRQ rx ", rx_events);
    hex("USB IRQ other ", other_irqs);
#ifndef USB_CONNECT
    if (irq_count == 1 && bus_events == 4 && tx_events == 1 && rx_events == 2 && !other_irqs)
        say("USB IRQ stub ok\r\n");
#endif
#endif
    for (;;) {}
}
