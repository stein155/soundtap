/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* RF serial bus and calibration helpers (src/rf.c) on the register block as plain memory: a
 * transaction that completes, one that keeps failing (bus reset between the 11 tries, then 0),
 * read, write and read-modify-write encodings, the calibration readout's 5-bit folding, the
 * calibration search's class for a readout that settles at once or never, and the waits on an RF
 * that never signals done (power-up, oscillator measurement), which must fail, not hang. Those
 * run with the free-running timer (R_TIMER) ticking in a second thread, as on the core; an alarm
 * ends a test that hangs anyway. */
#include "host.h"

#include <pthread.h>
#include <unistd.h>

#include "../src/rf.c"

static volatile int ticking;
static void *ticker(void *arg)
{
    (void)arg;
    while (ticking)
        __atomic_fetch_add((unsigned *)(REG_BASE + R_TIMER), 1, __ATOMIC_RELAXED);
    return 0;
}

static pthread_t tick_thread;
static void timer_run(int on)
{
    if (on) {
        ticking = 1;
        if (pthread_create(&tick_thread, 0, ticker, 0)) {
            perror("rf: timer thread");
            exit(2);
        }
    } else {
        ticking = 0;
        pthread_join(tick_thread, 0);
    }
}

/* 1 when at least ticks of R_TIMER passed since t0 */
static int elapsed(unsigned t0, unsigned ticks)
{
    return ((REG(R_TIMER) - t0) & 0x3ffffff) >= ticks;
}

static void dead_rf(void)
{
    unsigned t0, m = 0x5a5a;
    alarm(30);
    timer_run(1);

    /* a raw transaction whose done bit never comes: fails after one bus transaction's time */
    REG(R_RF_STATUS) = 0;
    t0 = REG(R_TIMER);
    CHECK(raw_wait(1) == 0 && elapsed(t0, RF_BUS_WAIT), "raw wait gives up");
    REG(R_RF_STATUS) = 3;
    REG(R_RF_ACK) = 0;
    CHECK(raw_wait(3) == 1 && REG(R_RF_ACK) == 3, "raw wait: done bits acknowledged");

    /* power-up: an RF that never completes a raw transaction fails it */
    REG(R_RF_STATUS) = 0;
    CHECK(rf_power_up() == 0, "power-up fails on a dead RF");
    REG(R_RF_STATUS) = 1;
    REG(R_RF_RDATA) = 4;
    CHECK(rf_power_up() == 1, "power-up: RF already up (reg 3 = 4)");
    REG(R_RF_STATUS) = 3; /* done on bits 0 and 1: the raw waits complete */
    REG(R_RF_RDATA) = 0;
    reg148 = 0;
    CHECK(rf_power_up() == 1 && !(reg148 & 0x20e0), "power-up: full sequence");
    REG(R_RF_STATUS) = 1; /* bit 1 never: the last raw wait (bits 0 and 1) fails */
    CHECK(rf_power_up() == 0 && !(reg148 & 0x20e0), "power-up: last wait fails, bits undone");

    /* the oscillator measurement never signals done (reg 0x3a bit 0): no count, measurement
     * stopped, and rf_init reports the failure without calibrating */
    REG(R_RF_STATUS) = 1;
    REG(R_RF_RDATA) = 0;
    t0 = REG(R_TIMER);
    CHECK(rf_measure(&m) == 0 && m == 0x5a5a && elapsed(t0, RF_MEAS_WAIT), "measurement gives up");
    CHECK(REG(R_RF_WDATA) == 0 && REG(R_RF_CMD) == (0x37 | 0xc000), "and is stopped");
    REG(R_RF_RDATA) = 0x1235;
    CHECK(rf_measure(&m) == 1 && m == 0x1235, "a measurement that is done gives its count");
    rfcal.done = 0;
    REG(R_RF_RDATA) = 0;
    CHECK(rf_init() == 0 && !rfcal.done, "rf_init fails on a measurement that never ends");

    timer_run(0);
    alarm(0);
}

static void bus(void)
{
    REG(R_RF_STATUS) = 1;
    CHECK(rf_xfer(0x23) == 1 && REG(R_RF_CMD) == (0x23 | 0x8000) && REG(R_RF_ACK) == 0xf,
          "a completed transaction");
    REG(R_RF_STATUS) = 0x31; /* done plus other units' bits 4..5: still done */
    CHECK(rf_xfer(0x23) == 1, "bits of other units ignored");
    REG(R_RF_RDATA) = 0x5abcd;
    CHECK(rf_read(7) == 0xabcd && REG(R_RF_CMD) == (7 | 0x8000), "read: low 16 bits");
    CHECK(rf_write(0x2c, 0x1234) == 1 && REG(R_RF_WDATA) == 0x1234 &&
              REG(R_RF_CMD) == (0x2c | 0x4000 | 0x8000),
          "write: data then command with the write bit");
    REG(R_RF_RDATA) = 0xabcd;
    rf_rmw(9, 0xc00, 0xc00);
    CHECK(REG(R_RF_WDATA) == 0xafcd, "read-modify-write %#x", REG(R_RF_WDATA));
    rf_rmw(0x20, 0, 0x4000);
    CHECK(REG(R_RF_WDATA) == 0xabcd, "clearing a clear bit keeps the rest");
    rf_page(6);
    CHECK(REG(R_RF_WDATA) == 6 && REG(R_RF_CMD) == (0x1f | 0xc000), "page select is reg 0x1f");

    REG(0x908) = 0x80000004;
    REG(R_RF_STATUS) = 0x100; /* an error bit, never done */
    CHECK(rf_xfer(0x24) == 0, "a failing transaction gives up");
    CHECK(REG(0x908) == 0x80000004, "bus reset pulse leaves 0x908 as it was");
    CHECK(REG(R_RF_CMD) == 0xc002 && REG(R_RF_WDATA) == 8, "and rewrites RF reg 2 = 8");
    REG(R_RF_STATUS) = 1;
}

static void fold(void)
{
    static const unsigned char want[32] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
                                           15, 15, 13, 13, 11, 11, 9, 9, 7, 7, 5, 5, 3, 3, 1, 1};
    for (unsigned v = 0; v < 32; v++)
        CHECK(fold5(v) == want[v] && fold5(v | 0xe0) == want[v], "fold5(%u) = %u", v, fold5(v));
}

static void cal_search(void)
{
    /* both halves settle at 15 on the first code (12): average 12, +8 or -10 by class */
    REG(R_RF_RDATA) = 0x0f0f;
    for (unsigned m = 0; m < 3; m++)
        CHECK(rf_cal_search(m) == 2, "settles at once, class %u -> 2", m);
    REG(R_RF_RDATA) = 0x1010; /* fold5(0x10) is 15 too */
    CHECK(rf_cal_search(1) == 2, "folded 15 counts");
    /* never settles: every run ends at 31 */
    REG(R_RF_RDATA) = 0x0e0f;
    for (unsigned m = 0; m < 3; m++)
        CHECK(rf_cal_search(m) == 0, "never settles, class %u -> 0", m);
    CHECK(REG(R_RF_WDATA) == 0 && REG(R_RF_CMD) == (8 | 0xc000), "search ends with reg 8 = 0");
}

int main(void)
{
    bus();
    fold();
    cal_search();
    dead_rf();
    return done("rf");
}
