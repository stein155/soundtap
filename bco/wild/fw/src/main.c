/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Our WiLD WLAN-core firmware: boot to the ready message (milestone 1). */
#include "hw.h"

#define MB16(o) (*(volatile unsigned short *)(0x1fde4u + (o)))
#define MB32(o) (*(volatile unsigned *)(0x1fde4u + (o)))

void dma_init(void);
void fiq_on(void);

/* Host-visible blocks, found by the host through the descriptor table (ids as bco/app/src/wifi.c
 * uses them). Each starts with a 3-byte host/core lock; zero = free. */
static struct {
    unsigned char id1[0x70], id2[0x4b0], id3[0x178], id4[0x10], id5[0x28], id6[0xa0],
        id7[0x204], id8[0x38], id10[8], id11[0x58], id12[0x8e0], id13[0x10c], id14[0x200],
        id15[0x34], id16[0x34], id17[0x38], id20[0x200], id21[0x220];
} __attribute__((aligned(4))) blk;

static struct { unsigned id, ptr; } desc_table[21];

/* id6 = operation parameters; the host writes dot11StationID (MIB 19:00, the device MAC) after
 * its lock (wifi.c). */
const unsigned char *const opparams = blk.id6;

static void put16(unsigned char *p, unsigned v) { *(unsigned short *)p = v; }
static void put32(unsigned char *p, unsigned v) { *(unsigned *)p = v; }

static void blocks_init(void)
{
    put32(blk.id1 + 0x04, REG(0x000));
    put32(blk.id1 + 0x08, MB32(0x218));
    put32(blk.id1 + 0x10, REG(0x600));
    put32(blk.id1 + 0x18, REG(0x734));
    put32(blk.id1 + 0x1c, REG(0xb00));
    put32(blk.id4 + 0x04, 0x900);
    blk.id4[0x08] = 1;
    put32(blk.id4 + 0x0c, 0x80000);
    /* id6: operation parameters (CWmin 31, CWmax 1023, AIFSN 2) */
    put32(blk.id6 + 0x0c, 0x900);
    put32(blk.id6 + 0x10, 7);
    put32(blk.id6 + 0x14, 4);
    blk.id6[0x20] = 100;
    put32(blk.id6 + 0x24, 8);
    put16(blk.id6 + 0x26, 0x1f);
    put16(blk.id6 + 0x38, 0x1f);
    put16(blk.id6 + 0x3a, 0x3ff);
    put16(blk.id6 + 0x4c, 0x3ff);
    blk.id6[0x4e] = 2;
}

static void desc_init(void)
{
    static const unsigned char ids[] = {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 20, 21};
    static const unsigned char *const ptr[] = {blk.id1, blk.id2, blk.id3, blk.id4, blk.id5, blk.id6, blk.id7,
                                  blk.id8, 0, blk.id10, blk.id11, blk.id12, blk.id13, blk.id14,
                                  blk.id15, blk.id16, blk.id17, 0, blk.id20, blk.id21};
    unsigned i;
    for (i = 0; i < sizeof ids; i++)
        desc_table[i].id = ids[i], desc_table[i].ptr = (unsigned)ptr[i];
    desc_table[i].id = 0xff;
}

static void post(unsigned type, unsigned arg)
{
    while (MB16(0x10e) == 1) {
    }
    MB16(0x10c) = type;
    MB32(0x110) = arg;
    MB16(0x10e) = 1;
    REG(R_HOST_IRQ) = 0x8000;
}

/* The RF never completed a power-up transaction (1) or an oscillator measurement (2): tell the
 * host with message type 0xe instead of the ready type 4 and stay parked. The app's bring-up takes
 * any type but 4 as a core that did not come up (wifi.c core_bring_up, its failure path) and
 * shows the type and argument in HELLO's version string ("wild=e:00000001"). */
#define MSG_RF_FAILED 0xe
static void rf_failed(unsigned step)
{
    post(MSG_RF_FAILED, step);
    for (;;)
        REG(0x08c) = 0x1234; /* watchdog, as the main loop keeps it */
}

void main(void)
{
    static struct hostblocks hb;
    REG(R_IRQ_CLEAR) = 0xffffffff;
    REG(R_FIQ_CLEAR) = 0xffffffff;
    REG(0x148) = reg148 = 0;
    REG(0x14c) = 0x0803e0e0;
    if (!rf_power_up())
        rf_failed(1);
    REG(0xa88) = 10;
    REG(0xa94) = 10;
    REG(0xa80) = 15;
    blocks_init();
    dma_init();
    mac_init();
    put32(blk.id1 + 0x20, rf_read(0));
    if (!rf_init())
        rf_failed(2);
    REG(R_IRQ_CLEAR) = 0xffffffff;
    REG(R_FIQ_CLEAR) = 0xffffffff;
    REG(R_FIQ_ENABLE) = 1;
    REG(R_IRQ_ENABLE) = 0x13;
    fiq_on();
    REG(0x51c) = 3;
    desc_init();
    hb.cmd = (struct ring *)blk.id15, hb.evt = (struct ring *)blk.id16;
    hb.frm = (struct ring *)blk.id12, hb.sup = (struct ring *)blk.id13;
    xport_init(&hb);
    sta_init(blk.id2);
    post(4, (unsigned)desc_table);
    /* the host answers with type 4 once its side is set up */
    for (;;) {
        if (MB16(2) == 1) {
            unsigned t = MB16(0);
            MB16(2) = 0;
            if (t == 4)
                break;
        }
    }
    for (;;) {
        REG(0x08c) = 0x1234; /* watchdog */
        REG(0x08c) = 0x1234;
        xport_poll();
        scan_poll();
        irq_off();                       /* the IRQ also updates reg148 (0x2000) */
        REG(0x148) = reg148 &= ~0x40u;
        irq_on();
        REG(0x08c) = 0x1234;
    }
}
