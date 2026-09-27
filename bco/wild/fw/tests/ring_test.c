/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Rings (src/ring.c), the structure every host<->core transfer goes through: channel ownership
 * after init, counts across the wrap, contiguous claims, copies that wrap, and a randomised run
 * against a model (every element passes the channels in order, none lost or duplicated). */
#include "host.h"

#include "../src/ring.c"

static unsigned buf[64];

static void basics(void)
{
    struct ring r;
    unsigned slot = 0x1234;
    ring_init(&r, (unsigned)(uintptr_t)buf, 8, 4, 2);
    CHECK(ring_avail(&r, 0) == 7 && ring_avail(&r, 1) == 0, "init: c0 owns all but one slot");
    CHECK(ring_space(&r, 0) == 7, "init: c0 contiguous space %u", ring_space(&r, 0));
    CHECK(!ring_take(&r, 1, 1, &slot) && slot == 0x1234, "empty channel: no take, slot untouched");
    CHECK(ring_take(&r, 0, 5, &slot) && slot == (unsigned)(uintptr_t)&buf[1], "take 5 from slot 1");
    ring_commit(&r, 0, 5);
    CHECK(ring_avail(&r, 1) == 5 && ring_avail(&r, 0) == 2, "commit hands 5 to c1");
    /* c0 has slots 6, 7 (to the wrap): a claim of 3 contiguous ones must fail */
    CHECK(ring_space(&r, 0) == 2, "c0 space to the wrap");
    CHECK(!ring_take(&r, 0, 3, &slot), "no claim across the wrap");
    CHECK(ring_take(&r, 1, 5, &slot) && slot == (unsigned)(uintptr_t)&buf[1], "c1 takes them");
    ring_commit(&r, 1, 5); /* back to c0: last channel wraps to 0 */
    CHECK(ring_avail(&r, 0) == 7 && ring_avail(&r, 1) == 0, "all back with c0");
    CHECK(ring_take(&r, 0, 2, &slot) && slot == (unsigned)(uintptr_t)&buf[6], "slots 6, 7");
    CHECK(r.ch[0].pos == 0, "pos wraps to 0");
    CHECK(ring_space(&r, 0) == 5, "c0 now 0..4 contiguous (%u)", ring_space(&r, 0));
    CHECK(irq_depth == 0, "critical sections balanced");
}

static void copy_wrap(void)
{
    struct ring r;
    unsigned in[6] = {11, 22, 33, 44, 55, 66}, out[6] = {0}, slot;
    ring_init(&r, (unsigned)(uintptr_t)buf, 8, 4, 2);
    memset(buf, 0, sizeof buf);
    ring_take(&r, 0, 5, &slot), ring_commit(&r, 0, 5);
    ring_take(&r, 1, 5, &slot), ring_commit(&r, 1, 5); /* positions now at 6 */
    ring_copy(&r, 0, 6, in, 1);                        /* slots 6, 7, 0, 1, 2, 3 */
    CHECK(buf[6] == 11 && buf[7] == 22 && buf[0] == 33 && buf[3] == 66, "copy in wraps");
    CHECK(ring_avail(&r, 1) == 6, "copy commits to the next channel");
    ring_copy(&r, 1, 6, out, 0);
    CHECK(!memcmp(in, out, sizeof in), "copy out wraps back in order");
    CHECK(ring_avail(&r, 0) == 7, "and commits back");
    CHECK(irq_depth == 0, "critical sections balanced");
}

/* Random takes and commits on 2..5 channels against a model. Channel 0 stamps a sequence number
 * into each element; every later channel must see the numbers in order. */
static void model(void)
{
    for (unsigned round = 0; round < 200; round++) {
        struct ring r;
        unsigned nch = 2 + rnd() % 4, size = 2 + rnd() % 30, esize = 4;
        unsigned held[5] = {0}, next[5] = {0}, stamp = 0;
        ring_init(&r, (unsigned)(uintptr_t)buf, size, esize, nch);
        for (unsigned step = 0; step < 400; step++) {
            unsigned c = rnd() % nch, total = 0;
            if (rnd() & 1) {
                unsigned space = ring_space(&r, c), want = 1 + rnd() % 4, slot;
                int ok = ring_take(&r, c, want, &slot);
                CHECK(ok == (space >= want), "take %u with %u contiguous", want, space);
                if (ok) {
                    for (unsigned k = 0; k < want; k++) {
                        unsigned *el = (unsigned *)(uintptr_t)(slot + k * esize);
                        CHECK((uintptr_t)el >= (uintptr_t)buf && (uintptr_t)el < (uintptr_t)(buf + size),
                              "slot inside the ring");
                        if (c == 0)
                            *el = stamp++;
                        else
                            CHECK(*el == next[c], "channel %u sees %u, wants %u", c, *el, next[c]);
                        if (c)
                            next[c]++;
                        held[c]++;
                    }
                }
            } else if (held[c]) {
                unsigned n = 1 + rnd() % held[c];
                ring_commit(&r, c, n);
                held[c] -= n;
            }
            for (unsigned k = 0; k < nch; k++)
                total += ring_avail(&r, k) + held[k];
            CHECK(total == size - 1, "slots conserved: %u of %u", total, size - 1);
            if (fails)
                return;
        }
    }
    CHECK(irq_depth == 0, "critical sections balanced");
}

int main(void)
{
    basics();
    copy_wrap();
    model();
    return done("ring");
}
