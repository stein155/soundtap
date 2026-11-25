/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Channel RF tune: program the frequency synthesiser and VCO for one channel, then run the VCO
 * cap/seed calibration. Clean-room C of the stock tune chain (stock 0x78dc -> 0x8b90 -> 0x8088 ->
 * 0x8ff8 -> 0x84c0 / 0x8404). The RF programming tables below are hardware facts transcribed
 * verbatim from the silicon's ROM tables (the way cond[] in rf.c is); they are channel<->register
 * maps, not code. rf_read/rf_write/rf_rmw/rf_page and the RF bus retry live in rf.c. */
#include "hw.h"

#define R_RF_RDATA_REG REG(0xa0c)

/* Per-channel 6-byte synth entry (stock ps block + 0x36, read on the real ST20 gen1):
 *   [0..1] synth word (reg 0x28, u16 LE)   [2] band/divider byte   [3] signed VCO seed (reg 0x24)
 *   [4..5] oscillator reference (0x16fc at reset; the cal overwrites it with the measured count).
 * Kept in RAM: the calibration updates [3] and [4..5] per channel, as stock does in its ps copy. */
static unsigned char synth[14][6] = {
    {0x70,0x3d,0x30,0xf8,0xfc,0x16}, {0x0a,0x57,0x30,0xf9,0xfc,0x16},
    {0xa3,0x70,0x30,0xf9,0xfc,0x16}, {0x3d,0x8a,0x31,0xfa,0xfc,0x16},
    {0xd7,0xa3,0x31,0xfa,0xfc,0x16}, {0x70,0xbd,0x31,0xfb,0xfc,0x16},
    {0x0a,0xd7,0x31,0xfc,0xfc,0x16}, {0xa3,0xf0,0x31,0xfc,0xfc,0x16},
    {0x3d,0x0a,0x31,0xfd,0xfc,0x16}, {0xd7,0x23,0x31,0xfd,0xfc,0x16},
    {0x70,0x3d,0x31,0xfe,0xfc,0x16}, {0x0a,0x57,0x31,0xff,0xfc,0x16},
    {0xa3,0x70,0x31,0xff,0xfc,0x16}, {0x14,0xae,0x32,0x01,0xfc,0x16},
};
/* Channel records {channel, bandmask=0x06, 0x00, lo, hi} (stock ROM 0x11f72). */
static const unsigned char chanfreq[14][5] = {
    {1,6,0,0xf8,0x7e}, {2,6,0,0xf9,0x7f}, {3,6,0,0xf9,0x7f}, {4,6,0,0xfa,0x70},
    {5,6,0,0xfa,0x70}, {6,6,0,0xfb,0x71}, {7,6,0,0xfc,0x71}, {8,6,0,0xfc,0x72},
    {9,6,0,0xfd,0x72}, {10,6,0,0xfd,0x73}, {11,6,0,0xfe,0x73}, {12,6,0,0xff,0x73},
    {13,6,0,0xff,0x74}, {14,6,0,0x01,0x74},
};
/* Synth coefficient tables (stock ROM).  A,B indexed [sclass*3 + band];  C,E,G,H indexed
 * [sclass*6 + ip*3 + band];  D,F indexed [ip*3 + band].  band 0..2, sclass 0..2, ip 0/1. */
static const unsigned char T_a[9]  = {4,3,1, 4,3,1, 5,3,1};
static const unsigned char T_b[9]  = {3,3,1, 4,3,1, 3,3,1};
static const unsigned char T_c[24] = {6,4,4,3,3,2, 7,5,3,5,3,3, 7,5,3,4,3,2, 0,7,7,0,7,6};
static const unsigned char T_d[6]  = {6,4,3, 5,5,4};
static const unsigned char T_e[24] = {4,2,2,7,6,4, 4,2,2,7,7,4, 4,3,2,7,7,6, 6,4,4,3,3,2};
static const unsigned char T_f[6]  = {2,2,2, 6,3,2};
static const unsigned char T_g[24] = {0,7,7,0,7,6, 0,7,7,0,7,7, 0,7,7,0,0,7, 0,4,0,0,2,0};
static const unsigned char T_h[24] = {0,4,0,0,2,0, 0,5,1,0,4,0, 0,5,0,0,0,0, 4,3,1,4,3,1};
/* mclass class regs 0x20..0x24 (stock ROM 0x1233c): cols [4..5] of each row for band!=0,
 * cols [0..1] of rows 0..1 for band==0. */
static const unsigned short cls_hi[5] = {0x0316,0x1615,0x0714,0x1e13,0x0f13};
static const unsigned short cls_lo[2] = {0x0f14,0x0e13};

/* RF transceiver shadows (stock keeps them in the ps block; the bits cannot be read back).
 * Seeded to the page-0 register values rf_init leaves on the chip (verified against the stock
 * trace: reg 0x24=0x80, 0x23=0x63c9, 0x25=0x8a00, reg 0x01=0x1c0b). */
static unsigned short s_r24 = 0x80, s_r23 = 0x63c9, s_r25 = 0x8a00, s_r01 = 0x1c0b; /* +0x30,+0x34,+0x32,+0x2e */
static unsigned char cur_band;                    /* ps[10]: last band programmed (0 at reset) */
static unsigned char cur_chan;                    /* ps[11]: chan index of the current entry */

/* diag: last VCO cal result for the current channel. bit31 = locked on the initial cap (no recal);
 * else [31:24]=chan idx, [23:16]=verify count_loop (0 = locked, else lock failures), [15:0]=last m.
 * lets a scan capture show which 2.4 GHz channels the own core is deaf on (cal never locks). */
volatile unsigned dg_vco;

/* Oscillator reference the cal targets (ps +0x20).  Stock measures it during scan prep (the
 * fill routine) just before the tune; that step is the next milestone, so default it to the
 * per-channel stored reference (0x16fc) which keeps the cal on its in-range path. */
int tune_osc_ref = 0x16fc;

/* 0x8ff8 / inline sweeps: step reg 0x24 toward target one bit at a time, bits 0x20..0x01. */
static void step24(int target)
{
    unsigned diff = (s_r24 ^ (unsigned)target) & 0x3f;
    for (unsigned bit = 0x20; bit; bit >>= 1)
        if (diff & bit) {
            s_r24 ^= bit;
            rf_write(0x24, s_r24);
        }
}

/* 0x8088: write the three synth class registers (0x33, 0x0c, 0x30) for the current channel.
 * Returns 0 when every RF write succeeded (stock retries the whole set otherwise; rf_write
 * already retries the bus, so one pass is enough). */
static int synth_pll(unsigned band, unsigned sclass)
{
    const unsigned char *cf = chanfreq[cur_chan];
    unsigned ip = (cur_band == 0) ? 1u : 0u;
    unsigned A = T_a[sclass * 3 + band], B = T_b[sclass * 3 + band];
    unsigned C = T_c[sclass * 6 + ip * 3 + band], D = T_d[ip * 3 + band];
    unsigned E = T_e[sclass * 6 + ip * 3 + band], F = T_f[ip * 3 + band];
    unsigned r33 = ((A << 8) & 0x700) | ((B << 12) & 0x7000) | ((C << 4) & 0x70) | (D & 7);
    unsigned r0c = ((E << 13) & 0xe000) | ((F << 10) & 0x1c00) | (cf[4] & 0xf0) | 0x200;
    unsigned r30 = ((cf[2] >> 4) << 12) & 0x7000;
    if (!(sclass == 1 && band == 1)) {
        unsigned G = T_g[sclass * 6 + ip * 3 + band], H = T_h[sclass * 6 + ip * 3 + band];
        r30 |= (G & 7) | ((H << 4) & 0x70);
    }
    if (!rf_write(0x33, r33)) return 1; /* cppcheck-suppress knownConditionTrueFalse ; the test's fake rf_write never fails */
    if (!rf_write(0x0c, r0c)) return 1; /* cppcheck-suppress knownConditionTrueFalse ; as above */
    return rf_write(0x30, r30) ? 0 : 1; /* cppcheck-suppress knownConditionTrueFalse ; as above */
}

/* 0x8404: step reg 0x24 to seed, start a cap measurement (reg 0x25 bit 0x1000), poll reg 0x26
 * until it signals done, restore reg 0x25.  Returns 1 when the RF bus failed or the measurement
 * did not signal done within RF_BUS_WAIT (the seed search then stops); else reads reg 0x26 (low
 * 12 bits) into *out and returns 0. */
static int cap_meas(int seed, unsigned short *out, unsigned cap)
{
    (void)cap;
    step24(seed);
    rf_write(0x25, s_r25 | 0x1000);
    int ok;
    unsigned r, t0 = REG(R_TIMER);
    do {
        ok = rf_xfer(0x26);        /* RF read: kick a cap measurement */
        r = R_RF_RDATA_REG;
    } while (!(r & 0x1000) && ok && ((REG(R_TIMER) - t0) & 0x3ffffff) < RF_BUS_WAIT);
    rf_write(0x25, s_r25);
    if (!ok || !(r & 0x1000))
        return 1;
    *out = rf_read(0x26) & ~0xf000u;
    return 0;
}

/* One block of up to 10 cap writes; counts how often reg RDATA bit 1 stays clear.  Returns
 * 0 (count was 0), -1 (count non-zero), or +1 (an RF write failed early). */
static int count_loop(unsigned cap)
{
    unsigned count = 0;
    (void)cap;
    for (unsigned i = 0; i < 10; i++) {
        int ok = rf_xfer(6);       /* RF read: sample the lock detector */
        unsigned r = R_RF_RDATA_REG;
        if (!(r & 2))
            count = (count + 1) & 0xff;
        if (!ok)
            return 1;
    }
    return count ? -1 : 0;
}

/* 0x84c0: VCO cap + seed calibration for the current channel. */
static void vco_cal(unsigned cap)
{
    unsigned char *entry = synth[cur_chan];
    int ref = tune_osc_ref;
    int tgt = (short)(entry[4] | entry[5] << 8);
    int err = ref - tgt;
    if (err < 0)
        err = -err;
    if (err < 0x180 && count_loop(cap) == 0) {
        dg_vco = 0x80000000u | (unsigned)cur_chan << 24; /* initial cap already good */
        return;
    }

    /* the full VCO recalibration (stock 0x8580) is the next milestone.  Stock does, in
     * order (verified in the scan trace, RF txns #5332..#5411): reg02 mode ramp, reg14 strobes, an
     * osc re-measurement (reg38/37/0f/39), a synth_pll re-run with the re-measured class, more reg14
     * strobes, then the cap/seed search below, then a verify count_loop.  Only the cap/seed search
     * is modelled here, so the tune matches stock bit-for-bit up to the recal (5331 RF txns). */

    /* recal: open the cap DAC and binary-walk the VCO seed to centre reg 0x26 near 0xa00. */
    s_r23 = (s_r23 & ~0x7007u) | 5;
    rf_write(0x23, s_r23);
    s_r24 |= 0x3000;
    rf_write(0x24, s_r24);

    int seed = (signed char)entry[3];
    unsigned short m = 0;
    int final = seed;
    if (!cap_meas(seed, &m, cap) && m != 0) {
        int prev;
        if (m >= 0xa00) {
            for (;;) {                    /* measurement high: lower the seed */
                prev = m;
                if (seed <= -32)          /* the 6-bit seed's floor, as 31 is its top below */
                    break;
                seed = (signed char)(seed - 1);
                final = seed;
                if (cap_meas(seed, &m, cap) || m == 0)
                    break;
                if (m <= 0xa00) {         /* bracketed: pick the closer side */
                    if ((0xa00 - (int)m) < ((int)prev - 0xa00))
                        step24(seed);
                    break;
                }
            }
        } else {
            for (;;) {                    /* measurement low: raise the seed */
                prev = m;
                if (seed == 31)
                    break;
                seed = (signed char)(seed + 1);
                final = seed;
                if (cap_meas(seed, &m, cap) || m == 0)
                    break;
                if (m >= 0xa00) {
                    if (((int)m - 0xa00) < (0xa00 - prev))
                        step24(seed);
                    break;
                }
            }
        }
        entry[3] = (unsigned char)final;
    }

    s_r24 &= ~0x3000u;                     /* 0x8734 cleanup */
    rf_write(0x24, s_r24);
    s_r23 = (s_r23 & ~0x7007u) | 0x6001;
    rf_write(0x23, s_r23);
    entry[4] = ref & 0xff, entry[5] = (ref >> 8) & 0xff;
    delay(10);
    int v = count_loop(cap);               /* verify pass */
    dg_vco = (unsigned)cur_chan << 24 | ((unsigned)(v & 0xff) << 16) | m;
}

/* 0x8b90: program the synth for channel index chanidx on band (0..2), then calibrate. */
static void tune_chan(unsigned chanidx, unsigned band, unsigned sclass)
{
    const unsigned char *cf = chanfreq[chanidx];
    const unsigned char *entry = synth[chanidx];
    cur_chan = chanidx;

    unsigned f = ((cf[4] << 8) & 0xf00) | ((cf[2] & 8) ? 0x4000u : 0u);
    s_r24 = (s_r24 & ~0x4f00u) | (f & 0x4f00);
    rf_write(0x24, s_r24);
    step24((signed char)entry[3]);
    rf_write(0x2f, (cf[2] & 7) | 0x1070);
    rf_page(2);
    rf_rmw(0x20, 0x8f, 0xff);
    rf_page(0);

    if (cur_band != band) {
        cur_band = band;
        if (band == 0) {
            rf_page(6);
            for (int r = 1; r >= 0; r--)
                rf_write(0x20 + r, cls_lo[r]);
            rf_page(2);
            rf_write(0x2e, 0x1810);
            rf_write(0x21, 0x3b);
            rf_page(0);
            rf_rmw(2, 8, 8);
        } else {
            if (band == 1) {
                s_r01 = (s_r01 & ~0x30u) | 0x20;
                rf_write(1, s_r01);
                REG(0x904) = 2;
            } else {
                s_r01 &= ~0x30u;
                rf_write(1, s_r01);
                REG(0x904) = 0;
                rf_page(6);
                for (int r = 4; r >= 0; r--)
                    rf_write(0x20 + r, cls_hi[r]);
            }
            rf_page(2);
            rf_write(0x2e, 0x1414);
            rf_write(0x21, 0x3f);
            rf_page(7);
            REG(0x908) |= 0x80000000u;
            rf_rmw(0x2a, 0xbc00, 0xff00);
            rf_page(0);
            rf_rmw(0x0d, 0x8000, 0x8000);
        }
    }

    while (synth_pll(band, sclass))
        ;
    rf_write(0x29, entry[2] & 0x7f);
    rf_write(0x28, entry[0] | entry[1] << 8);
    vco_cal(0);
}

/* 0x78dc: map the band code to a band index, find the channel's table row, tune it. */
void rf_tune(unsigned bandcode, unsigned chan)
{
    unsigned band = bandcode == 2 ? 1 : bandcode == 5 ? 1 : bandcode == 6 ? 2 : 0;
    unsigned mask = 1u << band, idx = 0xff;
    for (unsigned i = 0; i < 14; i++)
        if ((chanfreq[i][1] & mask) && chanfreq[i][0] == chan) {
            idx = i;
            break;
        }
    if (idx == 0xff)
        return;
    tune_chan(idx, band, rfcal.sclass);
}
