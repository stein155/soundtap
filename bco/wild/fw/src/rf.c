/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* RF transceiver: serial bus, power-up and calibration. */
#include "hw.h"

void delay(unsigned ticks)
{
    unsigned t0 = REG(R_TIMER);
    while (((REG(R_TIMER) - t0) & 0x3ffffff) < ticks) {
    }
}

/* One bus transaction. A transaction that ends in an error bit is retried after resetting the
 * bus (0x908 bit 0 pulse) and writing RF reg 2 = 8, up to 11 tries. */
int rf_xfer(unsigned cmd)
{
    for (unsigned try = 0; try < 11; try++) {
        REG(R_RF_ACK) = 0xf;
        unsigned t0 = REG(R_TIMER);
        REG(R_RF_CMD) = cmd | 0x8000;
        while (!(REG(R_RF_STATUS) & 0x1f0f) && ((REG(R_TIMER) - t0) & 0x3ffffff) < RF_BUS_WAIT) {
        }
        if ((REG(R_RF_STATUS) & 0x1f0f) == 1)   /* bits 4..7 belong to other units (0x20 = gain block) */
            return 1;
        unsigned v = REG(0x908);
        REG(0x908) = v ^ 1;
        REG(0x908) = v;
        REG(R_RF_ACK) = 0xf;
        REG(R_RF_WDATA) = 8;
        REG(R_RF_CMD) = 0xc002;
    }
    return 0;
}

unsigned rf_read(unsigned reg)
{
    rf_xfer(reg);
    return REG(R_RF_RDATA) & 0xffff;
}

int rf_write(unsigned reg, unsigned val)
{
    REG(R_RF_WDATA) = val;
    return rf_xfer(reg | 0x4000);
}

void rf_rmw(unsigned reg, unsigned val, unsigned mask)
{
    unsigned x = rf_read(reg);
    rf_write(reg, (val & mask) | (x & ~mask));
}

/* RF registers 0x20..0x3f are paged through register 0x1f. */
void rf_page(unsigned page)
{
    rf_write(0x1f, page);
}

/* Raw access without the retry wrapper, as used during power-up: 1 when the status bits came
 * within one bus transaction's time (RF_BUS_WAIT), else 0. */
static int raw_wait(unsigned bits)
{
    unsigned t0 = REG(R_TIMER);
    while ((REG(R_RF_STATUS) & bits) != bits)
        if (((REG(R_TIMER) - t0) & 0x3ffffff) >= RF_BUS_WAIT)
            return 0;
    REG(R_RF_ACK) = bits;
    return 1;
}

/* Kept in sync with register 0x148 (it cannot be read back reliably). */
unsigned reg148;

/* Clock and RF power-up: 1 when the RF answered every raw transaction, 0 when one never
 * completed (main.c then reports the core as failed instead of ready). */
int rf_power_up(void)
{
    int ok;
    REG(0x018) = 0x39;
    rf_read(3);
    rf_page(0);
    REG(R_RF_CMD) = 0x8022;
    if (!raw_wait(1))
        return 0;
    REG(R_RF_WDATA) = (REG(R_RF_RDATA) & 0xff) | 0x1e10;
    REG(R_RF_CMD) = 0xc022;
    if (!raw_wait(1))
        return 0;
    REG(0x098) = 0xa0;
    REG(R_RF_CMD) = 0x8022;
    if (!raw_wait(1))
        return 0;
    if (rf_read(3) == 4)
        return 1;
    REG(0x148) = reg148 |= 0xe0;
    REG(0x148) = reg148 |= 0x8000;
    rf_write(0x20, 0xa30);
    REG(0x148) = reg148 |= 0x2000;
    REG(R_RF_WDATA) = 4;
    REG(R_RF_CMD) = 0xc003;
    ok = raw_wait(3);
    REG(0x148) = reg148 &= ~0x2000u;   /* undo the power-up bits either way */
    if (ok)
        delay(100000);
    REG(0x148) = reg148 &= ~0xe0u;
    return ok;
}

struct rfcal rfcal;

/* One oscillator measurement: start, wait for done (at most RF_MEAS_WAIT), read the count into
 * *m. 0 when it never signalled done; the measurement is stopped either way. */
static int rf_measure(unsigned *m)
{
    unsigned t0;
    int done;
    rf_write(0x38, 0xffff);
    rf_write(0x37, 0x23);
    t0 = REG(R_TIMER);
    while (!(done = rf_read(0x3a) & 1) && ((REG(R_TIMER) - t0) & 0x3ffffff) < RF_MEAS_WAIT) {
    }
    if (done)
        *m = rf_read(0x39);
    rf_write(0x37, 0);
    return done;
}

/* 5-bit two's-complement-ish magnitude as the calibration readout encodes it. */
static unsigned fold5(unsigned v)
{
    v &= 0x1f;
    return v & 0x10 ? (v ^ 0x1f) | 1 : v;
}

/* Search the calibration code where both readout halves settle at 15; average over 8 runs. */
static unsigned rf_cal_search(unsigned mclass)
{
    int sum = 0;
    rf_write(0xf, 1);
    rf_write(8, 2);
    rf_page(6);
    rf_write(0x20, 0);
    rf_page(2);
    rf_write(0x28, 0x7f81);
    rf_rmw(0x20, 0, 0x4000);
    for (unsigned run = 0; run < 8; run++) {
        unsigned c = 12;
        for (; c < 31; c++) {
            rf_write(0x2c, 0x700 | c);
            for (unsigned k = 0; k < 16; k++) {
                rf_write(2, 0);
                rf_write(2, 0x20);
            }
            unsigned r = rf_read(0x2a);
            if (fold5(r) == 15 && fold5(r >> 8) == 15)
                break;
        }
        sum += c;
    }
    rf_page(0);
    rf_write(2, 0);
    rf_write(8, 0);
    if (mclass == 0)
        sum += 8;
    else if (mclass == 2)
        sum -= 10;
    int avg = sum / 8;
    return avg < 20 ? 2 : avg < 26 ? 1 : 0;
}

/* PHY block setup, RF bring-up and calibration (the core's RF init before any channel is set).
 * 1 when done (rfcal.done set); 0 when an oscillator measurement never completed, with the
 * calibration not run (main.c then reports the core as failed instead of ready). */
int rf_init(void)
{
    REG(0x800) = 0xf5800;
    REG(0x808) = 0x59141065;
    REG(0x80c) = 0x8000;
    REG(0x818) = 0x6a6a6a6a;
    REG(0x81c) = 0x6a6a6a6a;
    REG(0x820) = 0x40000;
    REG(0x82c) = 0x193400;
    REG(0x718) = 0x1934;
    REG(0x71c) = 0x300000;
    REG(0x700) = 0xe;
    REG(0x908) &= ~1u;
    REG(0xa20) |= 0x3f000000;
    REG(R_RF_ACK) = 0x1f;
    REG(0xa28) |= 2;
    REG(0xa1c) = 0xc0;

    rf_write(1, 0x1c0b);
    rf_write(0x35, 0);
    rf_write(4, 0);
    REG(0xa28) |= 1;
    rf_rmw(9, 0xc00, 0xc00);
    rf_read(0);
    rf_rmw(0x2f, 0x8000, 0x8000);

    unsigned m1, m2;
    if (!rf_measure(&m1))
        return 0;
    rf_rmw(0x2f, 0, 0x8000);
    if (!rf_measure(&m2))
        return 0;
    rfcal.count = (m1 + m2) >> 3;
    rfcal.delta = (int)((m1 + m2) >> 3) - (int)(m2 >> 2);
    rfcal.mclass = rfcal.delta < -0x2b0 ? 0 : rfcal.delta < 0x350 ? 1 : 2;
    rfcal.sclass = rf_cal_search(rfcal.mclass);

    static const unsigned short cond[][3] = {
        {0x23, 8, 8}, {0x23, 0x40, 0x70}, {0x23, 0x6000, 0x7000}, {0x23, 1, 7},
        {0x24, 0x80, 0x80}, {0x24, 0, 0x3000}, {0x25, 0x8000, 0x8000}, {0x32, 7, 7},
        {7, 0, 2}, {0x23, 0x80, 0x80}, {0x23, 0x200, 0x200}, {0x23, 0x100, 0x100},
        {0x27, 0x2000, 0x2000}, {0x27, 0xc00, 0xc00}, {0x25, 0xa00, 0xfff},
    };
    for (unsigned i = 0; i < sizeof cond / sizeof cond[0]; i++)
        rf_rmw(cond[i][0], cond[i][1], cond[i][2]);
    rfcal.r24 = rf_read(0x24);
    rfcal.r25 = rf_read(0x25);
    rfcal.r23 = rf_read(0x23);
    rf_rmw(0x27, 0x4000, 0x4000);
    rf_rmw(0xe, 0, 0x100);
    rf_rmw(7, 4, 4);

    rf_page(2);
    rf_rmw(0x20, 0xa00, 0x1f00);
    rf_rmw(0x20, 0x4000, 0x4000);
    rf_rmw(0x2c, 8, 0x1f);
    rf_rmw(0x2c, 0x2000, 0x3f00);
    rf_rmw(0x2d, 0x58, 0x7f);
    rf_rmw(0x24, 0x6e, 0xff);
    rf_write(0x22, 0x100);
    rf_rmw(0x20, 0x8f, 0xff);
    rf_page(6);
    for (int i = 4; i >= 0; i--)
        rf_write(0x28 + i, 6 - i);
    rf_page(0);
    rf_write(0xf, 0);
    rf_page(2);
    rf_write(0x30, 2);
    rf_write(0x31, 0x14);
    rf_page(4);
    rf_write(0x20, 0xfff6);
    rf_page(0);
    rf_rmw(7, 0, 0x4000);
    rf_write(0x14, 0);
    rf_rmw(7, 0x2000, 0x2000);
    rf_write(0x10, 0x80);
    rf_page(7);
    rf_write(0x25, 0xab80);
    rf_write(0x26, 0x6666);
    rf_write(0x2a, 0xc2ab);
    rf_write(0x2d, 0x58);
    rf_page(0);
    rfcal.done = 1;
    return 1;
}
