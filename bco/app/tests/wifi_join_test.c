/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The join's association step (src/wifi.c join_step, J_ASSOC) with the real wifi.c on the host:
 * once the AP has accepted the association, PEER_ADD must reach the core's command ring before the
 * 4-way starts, since the core installs the PTK only for a MAC in its peer table. With the command
 * ring full the step waits and tries again on the next poll, and gives the attempt up at the assoc
 * deadline; it never goes on to the 4-way without PEER_ADD.
 *
 * wifi.c keeps addresses in 32-bit words, so the test links without PIE (globals below 4 GiB, as
 * bco/wild/fw/tests/host.h does) and maps the one page of WiLD registers the step touches (the
 * doorbell and the timer it mixes into the SNonce) at its address. Nothing here talks to a speaker. */
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>

#pragma GCC diagnostic ignored "-Wpointer-to-int-cast"
#pragma GCC diagnostic ignored "-Wint-to-pointer-cast"
/* wifi.c brings its own memcpy/memset for the freestanding build; keep the C library's here. */
#define memcpy wifi_memcpy
#define memset wifi_memset
#include "../src/wifi.c"
#undef memcpy
#undef memset
#include "../src/wpa.c"

void netcard_from_port(unsigned ch, const unsigned char *f, unsigned n)
{
    (void)ch, (void)f, (void)n;
}

__attribute__((constructor)) static void map_wreg(void)
{
    static int probe;
    if (mmap((void *)WREG, 0x1000, PROT_READ | PROT_WRITE,
             MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0) != (void *)WREG) {
        perror("wifi_join: map the WiLD registers");
        exit(2);
    }
    if ((uintptr_t)&probe >> 32) {
        fputs("wifi_join: globals above 4 GiB; build with -no-pie\n", stderr);
        exit(2);
    }
}

static unsigned cmdbuf[64];
static const unsigned char AP[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x01};

/* The host side of the command ring with room for `words` more words. */
static void cmd_ring(unsigned words)
{
    memset(cmdbuf, 0, sizeof cmdbuf);
    wr_init(&tx, 0, (uintptr_t)cmdbuf, words + 1, 4, 2);
}

/* The AP answered the association: the state join_step finds in J_ASSOC. */
static void associated(void)
{
    copy(cur.bssid, AP, 6);
    state = WIFI_JOINING, jstep = J_ASSOC, rxassocok = 1, kicked = 0;
    now = 1000, deadline = now + 1500;
}

int main(void)
{
    /* a full ring: no PEER_ADD and no 4-way yet; the next poll still has the step */
    cmd_ring(5); /* PEER_ADD is 3 header words + 3 payload words */
    associated();
    join_step();
    assert(jstep == J_ASSOC && state == WIFI_JOINING);
    assert(wr_avail(&tx, 0) == 5);
    for (unsigned i = 0; i < 6; i++)
        assert(cmdbuf[i] == 0); /* nothing half-written */

    /* room again: PEER_ADD for the AP in slot 0, then the 4-way */
    cmd_ring(6);
    join_step();
    assert(jstep == J_4WAY);
    /* a fresh ring's producer starts at slot 1 (wr_init) */
    assert(cmdbuf[1] == (1u | (10 + 8) << 16) && cmdbuf[2] == CMD_PEER_ADD &&
           cmdbuf[3] == (0x12 | 0x11u << 16));
    assert(!memcmp(cmdbuf + 4, AP, 6) && ((unsigned char *)(cmdbuf + 4))[6] == PEER_AP);
    assert(WREG[0x210 / 4] & 4); /* the doorbell rang */

    /* a ring that stays full: the attempt is given up at the assoc deadline (the last of three
     * attempts fails the join) instead of going on without PEER_ADD */
    cmd_ring(5);
    associated();
    jtries = 2;
    now = deadline - 1;
    join_step();
    assert(jstep == J_ASSOC && state == WIFI_JOINING);
    now = deadline;
    join_step();
    assert(jstep == J_ASSOC && state == WIFI_FAILED);

    printf("ok: PEER_ADD waits for room in the command ring before the 4-way\n");
    return 0;
}
