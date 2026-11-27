/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The join and the setup access point with the real wifi.c on the host.
 *
 * The join's association step (src/wifi.c join_step, J_ASSOC):
 * once the AP has accepted the association, PEER_ADD must reach the core's command ring before the
 * 4-way starts, since the core installs the PTK only for a MAC in its peer table. With the command
 * ring full the step waits and tries again on the next poll, and gives the attempt up at the assoc
 * deadline; it never goes on to the 4-way without PEER_ADD. A network seen only with security this
 * station cannot join fails as such, and a refused authentication or association keeps the AP's
 * status code for STATUS.
 *
 * The access point: our core has no beacon timer, so wifi.c sends a beacon every interval once the
 * BSS is up, and answers a wildcard probe request with the beacon body minus its TIM.
 *
 * wifi.c keeps addresses in 32-bit words, so the test links without PIE (globals below 4 GiB, as
 * bco/wild/fw/tests/host.h does) and maps the one page of WiLD registers the step touches (the
 * doorbell and the timer it mixes into the SNonce) at its address, and the UART page. Nothing here
 * talks to a speaker. */
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
    /* the WiLD registers, and the UART page: a failed join says so in one line (board.h say) */
    static const unsigned long at[2] = {(unsigned long)WREG, 0xb1001000ul};
    for (int i = 0; i < 2; i++)
        if (mmap((void *)at[i], 0x1000, PROT_READ | PROT_WRITE,
                 MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0) != (void *)at[i]) {
            perror("wifi_join: map the WiLD registers and the UART");
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

/* Host-side TX frame buffers and the management frame ring (channel 0 is the host's). */
static unsigned char txmem[NTXFBUF * TXFBUF] __attribute__((aligned(8)));
static unsigned frmbuf[28 * 6];
static void tx_frames(void)
{
    memset(frmbuf, 0, sizeof frmbuf);
    wr_init(&frm[2], 0, (uintptr_t)frmbuf, 28, 0x18, 5);
    txfbase = (unsigned)(uintptr_t)txmem;
    for (txnfree = 0; txnfree < NTXFBUF; txnfree++)
        txfbuf[txnfree] = txfbase + txnfree * TXFBUF, txbusy[txnfree] = 0;
}

/* The frame in the TX buffer the last tx_push used: descriptor at +8, body at +8+0xc4. */
static const unsigned char *last_frame(void)
{
    return (const unsigned char *)(uintptr_t)txfbuf[txnfree];
}

/* A management frame as the core hands it to the app: header at +0xac, body at +0xcc. */
static unsigned char rxf[0xcc + 64];
static unsigned rx_mgmt(unsigned fc, const unsigned char *a1, const unsigned char *a2,
                        const unsigned char *a3, const unsigned char *body, unsigned n)
{
    memset(rxf, 0, sizeof rxf);
    rxf[0x94] = 0x18, rxf[0x97] = 0xc4;
    rxf[0xac] = (unsigned char)fc, rxf[0xad] = (unsigned char)(fc >> 8);
    memcpy(rxf + 0xac + 4, a1, 6), memcpy(rxf + 0xac + 10, a2, 6), memcpy(rxf + 0xac + 16, a3, 6);
    memcpy(rxf + 0xcc, body, n);
    return 0xcc + n;
}

static void security_and_status(void)
{
    static const unsigned char own_mac[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55};
    copy(own, own_mac, 6);
    /* only a WPA3/PMF-required BSS carries the SSID: the join fails on security, not "not found" */
    nbss = 1;
    memset(&bsstab[0], 0, sizeof bsstab[0]);
    memcpy(bsstab[0].bssid, AP, 6), memcpy(bsstab[0].ssid, "Home", 4);
    bsstab[0].ssidlen = 4, bsstab[0].chan = 6, bsstab[0].rsn = 1, bsstab[0].rsnok = 0;
    memcpy(cur.ssid, "Home", 4), cur.ssidlen = 4, have_cur = 1;
    state = WIFI_JOINING, fail_step = fail_code = 0;
    join_attempt();
    assert(state == WIFI_FAILED && fail_step == WIFI_FAIL_SECURITY);
    nbss = 0;
    state = WIFI_JOINING;
    join_attempt();
    assert(fail_step == WIFI_FAIL_NOT_FOUND);

    /* a refused association: status 31 (robust management frame policy) is kept for STATUS */
    copy(cur.bssid, AP, 6);
    rxassocok = rxauthok = 0;
    unsigned char ar[6] = {0x11, 0x00, 31, 0, 0, 0};
    rx_station(rxf, rx_mgmt(0x10, own_mac, AP, AP, ar, sizeof ar));
    assert(!rxassocok && fail_step == WIFI_FAIL_ASSOC && fail_code == 31);
    /* a refused authentication: algorithm 0, transaction 2, status 1 */
    unsigned char au[6] = {0, 0, 2, 0, 1, 0};
    rx_station(rxf, rx_mgmt(0xb0, own_mac, AP, AP, au, sizeof au));
    assert(!rxauthok && fail_step == WIFI_FAIL_AUTH && fail_code == 1);
    unsigned st, code;
    wifi_fail(&st, &code);
    assert(st == WIFI_FAIL_AUTH && code == 1);
    have_cur = 0;
    printf("ok: a join fails on security, a refusal keeps the AP's status code\n");
}

static void access_point(void)
{
    /* timestamp 8, interval 100, capability ESS, SSID "SoundTap", rates, DS 6, TIM */
    static const unsigned char body[] = {0, 0, 0, 0, 0, 0, 0, 0, 100, 0, 0x01, 0x00,
                                         0, 8, 'S', 'o', 'u', 'n', 'd', 'T', 'a', 'p',
                                         1, 4, 0x82, 0x84, 0x8b, 0x96,
                                         3, 1, 6,
                                         5, 4, 0, 1, 0, 0};
    static const unsigned char phone[6] = {0x02, 0x99, 0x88, 0x77, 0x66, 0x55};
    static const unsigned char bad[] = {0, 0, 0, 0, 0, 0, 0, 0, 100, 0, 1, 0, 1, 9, 0x82};
    cmd_ring(32);
    tx_frames();
    state = WIFI_IDLE, now = 5000;
    assert(wifi_ap_start(6, 100, bad, sizeof bad) == -1);      /* IE runs past the end */
    assert(wifi_ap_start(6, 100, body, 12) == -1);             /* no SSID */
    assert(wifi_ap_start(6, 100, body, sizeof body) == 0 && state == WIFI_AP);
    assert(cmdbuf[2] == 0xbc);                                 /* the BSS on our own address */
    assert(!memcmp(cmdbuf + 4, own, 6));
    ap_step();
    assert(!ap_ready && ap_beacons == 0);                      /* the core has not set it up yet */
    corebd = 1;
    ap_step();
    assert(ap_ready && ap_beacons == 1);
    const unsigned char *f = last_frame(), *d = f + 8, *b = d + 0xc4;
    assert(d[0x18] == 0x80);                                   /* beacon */
    assert(!memcmp(d + 0x1c, "\xff\xff\xff\xff\xff\xff", 6) && !memcmp(d + 0x22, own, 6) &&
           !memcmp(d + 0x28, own, 6));                         /* to everybody, BSSID = own */
    assert(!memcmp(b + 8, body + 8, sizeof body - 8));         /* hostapd's body, TIM included */
    now += 50;
    ap_step();
    assert(ap_beacons == 1);                                   /* not before the interval */
    now += 53;
    ap_step();
    assert(ap_beacons == 2);                                   /* 100 TU = 102.4 ms */
    now += 5000;
    ap_step();
    assert(ap_beacons == 3);                                   /* late: one beacon, not a burst */
    ap_step();
    assert(ap_beacons == 3);

    /* a wildcard probe request: a probe response to the phone, the body without the TIM */
    unsigned char pq[] = {0, 0, 1, 2, 0x82, 0x84};
    unsigned before = wifi_tx_frames;
    rx_ap(rxf, rx_mgmt(0x40, ap_bcast, phone, ap_bcast, pq, sizeof pq));
    assert(ap_probes == 1 && wifi_tx_frames == before + 1);
    f = last_frame(), d = f + 8, b = d + 0xc4;
    assert(d[0x18] == 0x50 && !memcmp(d + 0x1c, phone, 6) && !memcmp(d + 0x28, own, 6));
    assert(!memcmp(b + 8, body + 8, sizeof body - 8 - 6));     /* everything up to the TIM */
    /* a probe for another network gets no answer */
    unsigned char other[] = {0, 4, 'N', 'e', 'x', 't'};
    rx_ap(rxf, rx_mgmt(0x40, ap_bcast, phone, ap_bcast, other, sizeof other));
    assert(ap_probes == 2 && wifi_tx_frames == before + 1);
    /* one for our SSID does */
    unsigned char ours[] = {0, 8, 'S', 'o', 'u', 'n', 'd', 'T', 'a', 'p'};
    rx_ap(rxf, rx_mgmt(0x40, own, phone, own, ours, sizeof ours));
    assert(wifi_tx_frames == before + 2);
    wifi_ap_stop();
    assert(state == WIFI_IDLE);
    printf("ok: the access point beacons every interval and answers probe requests\n");
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

    /* a ring that stays full: the attempt is given up at the assoc deadline (the last attempt
     * fails the join) instead of going on without PEER_ADD */
    cmd_ring(5);
    associated();
    jtries = JOIN_TRIES - 1;
    now = deadline - 1;
    join_step();
    assert(jstep == J_ASSOC && state == WIFI_JOINING);
    now = deadline;
    join_step();
    assert(jstep == J_ASSOC && state == WIFI_FAILED);

    printf("ok: PEER_ADD waits for room in the command ring before the 4-way\n");
    security_and_status();
    access_point();
    return 0;
}
