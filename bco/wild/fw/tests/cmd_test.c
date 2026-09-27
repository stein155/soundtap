/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The command ring from the host (src/xport.c commands): well-formed records reach their module
 * and records that cannot be ones are skipped by their declared length and counted, leaving the
 * ring in step for the next record: a len below the 8 bytes of type and module, a payload over
 * the copy buffer (also one that wraps our ring), a len beyond our whole ring, and another kind.
 * The host is trusted; these are the bounds that keep a bad record from writing over the core.
 * And the event ring the other way: an event goes in whole or is dropped and counted. */
#include "core_sim.h"

/* Raw words on the host's command ring, as a record of any shape. */
static void host_raw(const unsigned *w, unsigned n)
{
    unsigned slot;
    for (unsigned i = 0; i < n; i++) {
        if (!ring_take(&H_cmd, 0, 1, &slot)) {
            CHECK(0, "host cmd ring full");
            return;
        }
        *(unsigned *)(uintptr_t)slot = w[i];
        ring_commit(&H_cmd, 0, 1);
    }
}

/* A record of kind, declared len and n body words of filler. */
static void host_record(unsigned kind, unsigned len, unsigned n)
{
    unsigned w[300];
    w[0] = kind | len << 16;
    for (unsigned i = 0; i < n; i++)
        w[1 + i] = 0xb5b5b5b5u; /* would read as PEER_ADD if the ring lost step */
    host_raw(w, 1 + n);
}

/* PEER_ADD of mac into slot: the probe that the ring is still in step after a bad record. */
static void peer_add(const unsigned char *mac, unsigned slot)
{
    unsigned char pr[10] = {0};
    memcpy(pr, mac, 6), pr[6] = slot;
    host_cmd(0xb5, 0x12, 0x11, pr, sizeof pr);
}

static int in_step(const unsigned char *mac, unsigned slot)
{
    peer_add(mac, slot);
    pump(40);
    return peer_slot(mac) == slot;
}

static unsigned char big[320]; /* a payload; 320 bytes is the most host_cmd builds */
static const unsigned char MAC[4][6] = {
    {0x02, 0, 0, 0, 0, 1}, {0x02, 0, 0, 0, 0, 2}, {0x02, 0, 0, 0, 0, 3}, {0x02, 0, 0, 0, 0, 4}};

static void records(void)
{
    sim_init();
    CHECK(in_step(MAC[0], 0) && rec[RC_CMDBAD] == 0, "a well-formed record");

    /* len below 8: was len - 8 = ~4 GB copied into cmd_copy */
    host_record(1, 4, 1);
    host_record(1, 0, 0);
    host_record(1, 7, 2);
    CHECK(in_step(MAC[1], 1) && rec[RC_CMDBAD] == 3, "short records skipped (%u)", rec[RC_CMDBAD]);

    /* the largest payload the copy buffer holds still goes through */
    sim_init();
    host_cmd(0x99, 0x01, 0x12, big, sizeof cmd_copy); /* to the host: echoed as an event */
    pump(40);
    nev = 0;
    host_events();
    CHECK(rec[RC_CMDBAD] == 0 && find_event(0x99) && find_event(0x99)->len == sizeof cmd_copy,
          "a %u-byte payload is routed", (unsigned)sizeof cmd_copy);

    /* a payload over the copy buffer, placed so that it wraps our 100-word ring: was copied
     * whole into cmd_copy */
    sim_init();
    for (unsigned i = 0; i < 5; i++)
        peer_add(MAC[0], 0); /* 6 words each: the next record starts at word 31 */
    pump(20);
    host_cmd(0x99, 0x01, 0x12, big, sizeof cmd_copy + 16);
    CHECK(in_step(MAC[2], 2) && rec[RC_CMDBAD] == 1, "an oversized wrapping payload is skipped");
    nev = 0;
    host_events();
    CHECK(!find_event(0x99), "and never routed");

    /* a len beyond our whole ring: was waited for forever, every later command with it */
    sim_init();
    host_record(1, 4 * 250, 250);
    CHECK(in_step(MAC[3], 3) && rec[RC_CMDBAD] == 1, "a record longer than our ring is skipped");

    /* kind 2 (never sent by wifi.c): skipped with its body, not read as records */
    sim_init();
    host_record(2, 8, 2);
    host_record(0, 12, 3);
    CHECK(in_step(MAC[0], 0) && rec[RC_CMDBAD] == 2, "other kinds skipped with their body");
    CHECK(peer_slot((const unsigned char *)"\xb5\xb5\xb5\xb5\xb5\xb5") == 6, "filler never parsed");
    CHECK(irq_depth == 0, "critical sections balanced");
}

/* The event ring to the host: an event goes in whole or not at all. While the host takes
 * nothing, our 750-word ring holds 749 words: 249 three-word events; the rest are dropped and
 * counted, and the ring's channels stay consistent. Once the host takes them again, every
 * record it gets is whole. */
static void events(void)
{
    static const unsigned char pl[8] = {1, 2, 3, 4, 5, 6, 7, 8};
    sim_init();
    for (unsigned i = 0; i < 300; i++)
        post_event(0x100 + i, 1, 0x12, 0, 0);
    CHECK(ring_avail(&l_evt, 1) == 747 && ring_avail(&l_evt, 0) == 2,
          "249 events queued (%u words), 2 words free (%u)", ring_avail(&l_evt, 1),
          ring_avail(&l_evt, 0));
    CHECK(rec[RC_EVDROP] == 51, "51 dropped (%u)", rec[RC_EVDROP]);
    post_event(0x400, 1, 0x12, pl, sizeof pl); /* needs 5 words: dropped whole */
    CHECK(rec[RC_EVDROP] == 52 && ring_avail(&l_evt, 0) == 2, "a payload event does not fit");
    nev = 0;
    pump(40);
    host_events();
    CHECK(nev == 249, "the host gets 249 events (%u)", nev);
    for (unsigned i = 0; i < 63; i++) /* host_events keeps 63, then overwrites the last */
        CHECK(ev[i].type == 0x100 + i && ev[i].len == 0 && ev[i].module == 1, "event %u whole", i);
    post_event(0x401, 1, 0x12, pl, sizeof pl);
    nev = 0;
    pump(40);
    host_events();
    CHECK(nev == 1 && ev[0].type == 0x401 && ev[0].len == 8 && !memcmp(ev[0].payload, pl, 8),
          "room again: the next event goes through");
    CHECK(irq_depth == 0, "critical sections balanced");
}

int main(void)
{
    records();
    events();
    return done("cmd");
}
