/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Channel tune (src/tune.c) over a model of the RF transceiver's serial bus: the channel and band
 * tables, which band codes and channels tune at all, the synthesiser words each channel programs,
 * the band switch, and the VCO seed search against a model whose measurement crosses 0xa00 at a
 * known seed, including one that never crosses (a dead or saturated VCO) and one whose measurement
 * never signals done, which must both end. */
#include "host.h"

#include "../src/tune.c"

struct rfcal rfcal;

/* ---- the RF bus model: every write logged; reads of the lock detector (6) and the cap
 * measurement (0x26) come from the model, the rest read back the last value written. ---- */
static unsigned page;
static unsigned short rf[8][64];
static struct { unsigned char page, reg; unsigned short val; } wlog[8192];
static unsigned nlog, nreads;
static int locked = 1;
static int vco_target = 0;         /* seed at which the measurement crosses 0xa00 */
static int vco_mode;               /* 0 model, 1 stuck high, 2 stuck low, 3 never done */

static int seed6(void)
{
    int s = s_r24 & 0x3f;
    return s & 0x20 ? s - 64 : s;
}

static unsigned model_read(unsigned reg)
{
    if (reg == 6)
        return locked ? 2 : 0;
    if (reg == 0x26) {
        int m = 0xa00 + (seed6() - vco_target) * 0x60;
        if (vco_mode == 1)
            m = 0xfff;
        else if (vco_mode == 2)
            m = 1;
        m = m < 1 ? 1 : m > 0xfff ? 0xfff : m;
        return (vco_mode == 3 ? 0 : 0x1000) | m;
    }
    return rf[reg < 0x20 ? 0 : page & 7][reg & 0x3f];
}

#define BUS_TICKS 50 /* R_TIMER ticks one modelled bus transaction takes */
int rf_xfer(unsigned cmd)
{
    if (++nreads > 1000000) {
        printf("FAIL: the tune does not end (%u RF reads)\n", nreads);
        exit(1);
    }
    REG(R_RF_RDATA) = model_read(cmd & 0x3f);
    REG(R_TIMER) += BUS_TICKS; /* the free-running timer moves on while the bus works */
    return 1;
}

unsigned rf_read(unsigned reg)
{
    rf_xfer(reg);
    return REG(R_RF_RDATA) & 0xffff;
}

int rf_write(unsigned reg, unsigned val)
{
    if (nlog < sizeof wlog / sizeof wlog[0])
        wlog[nlog].page = page, wlog[nlog].reg = reg, wlog[nlog].val = val, nlog++;
    if (reg == 0x1f)
        page = val;
    rf[reg < 0x20 ? 0 : page & 7][reg & 0x3f] = val;
    return 1;
}

void rf_rmw(unsigned reg, unsigned val, unsigned mask)
{
    unsigned x = rf_read(reg);
    rf_write(reg, (val & mask) | (x & ~mask));
}

void rf_page(unsigned p)
{
    rf_write(0x1f, p);
}

void delay(unsigned ticks)
{
    (void)ticks;
}

/* The last value written to reg on pg, or -1. */
static int last_write(unsigned pg, unsigned reg)
{
    for (unsigned i = nlog; i--;)
        if (wlog[i].reg == reg && (reg < 0x20 || wlog[i].page == pg))
            return wlog[i].val;
    return -1;
}

static unsigned writes_to(unsigned pg, unsigned reg)
{
    unsigned n = 0;
    for (unsigned i = 0; i < nlog; i++)
        n += wlog[i].reg == reg && (reg < 0x20 || wlog[i].page == pg);
    return n;
}

static void tables(void)
{
    for (unsigned i = 0; i < 14; i++) {
        CHECK(chanfreq[i][0] == i + 1 && chanfreq[i][1] == 6, "channel record %u", i + 1);
        CHECK(synth[i][4] == 0xfc && synth[i][5] == 0x16, "reference 0x16fc for channel %u", i + 1);
    }
}

static void no_tune(void)
{
    static const unsigned chans[] = {0, 15, 36, 255};
    static const unsigned codes[] = {0, 1, 3, 4, 7};
    for (unsigned i = 0; i < sizeof chans / sizeof chans[0]; i++) {
        nlog = 0;
        rf_tune(6, chans[i]);
        CHECK(nlog == 0, "channel %u: no RF write", chans[i]);
    }
    for (unsigned i = 0; i < sizeof codes / sizeof codes[0]; i++) {
        nlog = 0;
        rf_tune(codes[i], 6);
        CHECK(nlog == 0, "band code %u (band 0 has no channel here): no RF write", codes[i]);
    }
}

static void channels(void)
{
    tune_osc_ref = 0x16fc;
    locked = 1;
    for (unsigned ch = 1; ch <= 14; ch++) {
        nlog = 0;
        rf_tune(6, ch);
        unsigned i = ch - 1;
        CHECK(cur_chan == i && cur_band == 2, "channel %u selected on band 2", ch);
        CHECK(last_write(0, 0x28) == (synth[i][0] | synth[i][1] << 8), "channel %u synth word", ch);
        CHECK(last_write(0, 0x29) == (synth[i][2] & 0x7f), "channel %u divider", ch);
        CHECK(last_write(0, 0x2f) == ((chanfreq[i][2] & 7) | 0x1070), "channel %u reg 0x2f", ch);
        CHECK(dg_vco == (0x80000000u | i << 24), "channel %u locks on the stored seed", ch);
        CHECK((s_r24 & 0x3f) == (synth[i][3] & 0x3f), "channel %u seed in reg 0x24", ch);
        CHECK(page == 0, "back on page 0");
    }
}

static void synth_words(void)
{
    /* band 2 (code 6), channel 6, calibration class 0 and 1; band 1 (code 2), class 1 */
    static const struct { unsigned code, sclass, r33, r0c, r30; } c[] = {
        {6, 0, 0x1143, 0x4a70, 0x0007},
        {6, 1, 0x1133, 0x4a70, 0x0017},
        {2, 1, 0x3354, 0x4a70, 0x0000},
    };
    for (unsigned k = 0; k < sizeof c / sizeof c[0]; k++) {
        rfcal.sclass = c[k].sclass;
        nlog = 0;
        rf_tune(c[k].code, 6);
        CHECK(last_write(0, 0x33) == (int)c[k].r33, "case %u reg 0x33 %#x", k, last_write(0, 0x33));
        CHECK(last_write(0, 0x0c) == (int)c[k].r0c, "case %u reg 0x0c %#x", k, last_write(0, 0x0c));
        CHECK(last_write(0, 0x30) == (int)c[k].r30, "case %u reg 0x30 %#x", k, last_write(0, 0x30));
    }
    rfcal.sclass = 0;
}

static void band_switch(void)
{
    cur_band = 0;
    nlog = 0;
    rf_tune(6, 1);
    CHECK(writes_to(6, 0x20) == 1 && last_write(6, 0x24) == cls_hi[4], "band 2: class regs on page 6");
    CHECK(REG(0x904) == 0 && (REG(0x908) & 0x80000000u), "band 2 MAC side");
    CHECK((s_r01 & 0x30) == 0, "band 2 clears reg 1 bits 4-5");
    nlog = 0;
    rf_tune(6, 11);
    CHECK(writes_to(6, 0x20) == 0, "same band: no switch");
    nlog = 0;
    rf_tune(2, 11);
    CHECK(REG(0x904) == 2 && (s_r01 & 0x30) == 0x20 && cur_band == 1, "band 1 (code 2)");
    rf_tune(5, 11);
    CHECK(cur_band == 1, "code 5 is band 1 too");
}

/* The VCO seed search when the stored reference is off (or the lock detector fails). */
static void recal(int start, int target, int mode)
{
    unsigned i = 5; /* channel 6 */
    synth[i][3] = (unsigned char)start;
    synth[i][4] = 0xfc, synth[i][5] = 0x16;
    vco_target = target, vco_mode = mode;
    tune_osc_ref = 0x16fc + 0x200;
    nreads = 0;
    rf_tune(6, 6);
    int final = (signed char)synth[i][3];
    CHECK(synth[i][4] == 0xfc && synth[i][5] == 0x18, "reference updated to the measured one");
    CHECK(!(s_r24 & 0x3000) && (s_r23 & 0x7007) == 0x6001, "cap DAC closed again");
    CHECK(dg_vco >> 24 == i, "diag names the channel");
    if (mode == 0) {
        /* the walk stops on the first seed past the crossing */
        CHECK(final == target || final == target - 1 || final == target + 1,
              "start %d target %d: seed %d next to the crossing", start, target, final);
    } else if (mode == 1) {
        CHECK(final >= -32 && final <= 31, "stuck high from %d: seed %d stays a 6-bit value", start,
              final);
    } else if (mode == 2) {
        CHECK(final == 31, "stuck low: the walk stops at 31 (%d)", final);
    } else {
        /* the first cap measurement never signals done: it gives up after RF_BUS_WAIT and the
         * search does not run (the seed stays, the diag shows no measurement) */
        CHECK(final == start && (dg_vco & 0xffff) == 0, "never done: seed %d kept", final);
        CHECK(nreads < RF_BUS_WAIT / BUS_TICKS + 1000, "and the tune ends (%u RF reads)", nreads);
    }
    tune_osc_ref = 0x16fc;
    vco_mode = 0;
}

int main(void)
{
    tables();
    no_tune();
    channels();
    synth_words();
    band_switch();
    recal(-8, 5, 0);
    recal(10, -12, 0);
    recal(0, 0, 0);
    recal(20, 31, 0);
    recal(-30, -32, 0);
    recal(3, 0, 2);
    recal(3, 0, 1);
    recal(-31, 0, 1);
    recal(7, 0, 3);
    locked = 0; /* a channel that does not lock is recalibrated even on the stored reference */
    nreads = 0;
    synth[5][3] = 0;
    vco_target = 4;
    rf_tune(6, 6);
    CHECK(!(dg_vco >> 31) && (signed char)synth[5][3] >= 3, "unlocked channel recalibrated (%d)",
          (signed char)synth[5][3]);
    return done("tune");
}
