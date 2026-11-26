/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Host test of the management server (ADR 0031 messages on BCO link channel 0). */
#include <assert.h>
#include <string.h>
#include <stdio.h>
#include "bcolink.h"
#include "mgmt.h"
#include "wifi.h"
extern unsigned fake_state, fake_scanning, fake_nbss, fake_joins, fake_leaves, fake_ssid_len;
extern unsigned char fake_ssid[32], fake_pmk[32], fake_seed[32];
extern unsigned fake_tx_free, fake_resets;
/* netcard.c's counters LINK_STATS reports; netcard.c itself is not linked into this test */
unsigned netcard_dropped = 0x404, netcard_to_wifi = 0x505;
static unsigned le32(const unsigned char *p) { return p[0] | p[1] << 8 | p[2] << 16 | (unsigned)p[3] << 24; }
static unsigned seq = 7;
/* Each request gets the next seq, as the driver sends them; again() resends with the same seq. */
static unsigned send(unsigned char t, const unsigned char *pl, unsigned pn, unsigned char *r)
{
    unsigned char q[MGMT_HDR + 600] = {1, t, seq >> 8, seq, pn >> 8, pn};
    if (pn)
        memcpy(q + MGMT_HDR, pl, pn);
    return mgmt_handle(q, MGMT_HDR + pn, r);
}
static unsigned ask(unsigned char t, const unsigned char *pl, unsigned pn, unsigned char *r)
{
    seq++;
    return send(t, pl, pn, r);
}
#define again send
int main(void)
{
    unsigned char r[MGMT_MAX]; unsigned n;
    memcpy(mgmt_wifi_mac, "\x00\x0c\x8a\x01\x02\x03", 6);
    mgmt_uptime = 0x01020304;
    n = ask(1, 0, 0, r); /* HELLO: status, ver_len, ver, wifi mac, state, uptime, proto, caps */
    assert(n == MGMT_HDR + 24 && r[1] == 0x81 && r[3] == 8 && r[5] == 24 && r[6] == 0 && r[7] == 6 &&
           !memcmp(r + 8, "link-2", 6) && !memcmp(r + 14, mgmt_wifi_mac, 6) && r[21] == 4 &&
           r[24] == 1 && r[25] == BCOLINK_PROTO &&
           r[26] == (BCOLINK_CAP_AP | BCOLINK_CAP_REPLAY | BCOLINK_CAP_FLOW | BCOLINK_CAP_STATS) &&
           r[27] == 0 && r[29] == 0);
    n = ask(5, 0, 0, r); assert(r[6] == 0 && r[7] == WIFI_IDLE && r[8] == 0 && n == MGMT_HDR + 11);

    /* scan: start -> still scanning (3), poll -> results */
    unsigned char one = 1;
    ask(2, &one, 1, r); assert(r[6] == 3 && r[7] == 0 && fake_scanning);
    ask(2, 0, 0, r); assert(r[6] == 3);
    fake_scanning = 0, fake_nbss = 2;
    n = ask(2, 0, 0, r);
    assert(r[6] == 0 && r[7] == 2 && n == MGMT_HDR + 2 + 2 * 13);
    assert(r[8] == 3 && !memcmp(r + 9, "ap0", 3) && r[12 + 5] == 0x10 && r[18] == 1 &&
           (signed char)r[19] == -40 && r[20] == 1);                   /* entry 0 */
    assert(r[21] == 3 && !memcmp(r + 22, "ap1", 3) && r[31] == 2);     /* entry 1 */
    fake_nbss = 24;
    n = ask(2, 0, 0, r); assert(r[7] == 24 && n <= MGMT_MAX);

    /* profile: ssid, security 1, the 32-byte PMK, the 32-byte seed */
    unsigned char sp[1 + 4 + 2 + 32 + 32] = {4, 't', 'e', 's', 't', 1, 32};
    memset(sp + 7, 0xab, 32), memset(sp + 39, 0xcd, 32);
    ask(3, sp, sizeof sp, r);
    assert(r[6] == 0 && fake_joins == 1 && fake_ssid_len == 4 && fake_pmk[31] == 0xab &&
           fake_seed[0] == 0xcd);
    /* the reply got lost: the driver resends with the same seq and must not join twice */
    unsigned char first[MGMT_MAX]; memcpy(first, r, MGMT_HDR + 1);
    n = again(3, sp, sizeof sp, r);
    assert(fake_joins == 1 && n == MGMT_HDR + 1 && !memcmp(r, first, n));
    again(5, 0, 0, r); assert(r[7] == WIFI_JOINING); /* a query with a stale seq still runs */
    n = ask(5, 0, 0, r);
    assert(r[7] == WIFI_JOINING && r[8] == 4 && !memcmp(r + 9, "test", 4) && r[19] == 6 &&
           (signed char)r[20] == -50);
    for (unsigned i = 0; i + 4 <= n; i++) assert(memcmp(r + i, "\xab\xab\xab\xab", 4)); /* PMK never echoed */
    ask(3, sp, sizeof sp - 1, r); assert(r[6] == 1);              /* no seed */
    sp[5] = 0; ask(3, sp, sizeof sp, r); assert(r[6] == 2);       /* open: unsupported */
    sp[5] = 1, sp[0] = 0; ask(3, sp, sizeof sp, r); assert(r[6] == 1); /* empty ssid */
    assert(fake_joins == 1);
    ask(4, 0, 0, r); assert(r[6] == 0 && fake_leaves == 1);
    assert(fake_resets == 0); /* FORGET leaves the network; the core keeps running */
    ask(5, 0, 0, r); assert(r[7] == WIFI_IDLE && r[8] == 0);
    /* AP: channel 6, 100 TU, a 40-byte body; status lists the clients; stop */
    extern unsigned fake_ap_chan, fake_ap_bint, fake_ap_len, fake_ap_stops;
    unsigned char ap[3 + 40] = {6, 0, 100};
    ask(6, ap, sizeof ap, r);
    assert(r[6] == 0 && fake_ap_chan == 6 && fake_ap_bint == 100 && fake_ap_len == 40);
    n = ask(8, 0, 0, r);
    assert(r[6] == 0 && r[7] == 1 && r[13] == 0x77 && n == MGMT_HDR + 8 + 4 * WIFI_AP_COUNTERS &&
           r[14] == 0x11 && r[18] == 0x22);
    ask(7, 0, 0, r); assert(r[6] == 0 && fake_ap_stops == 1);
    ask(8, 0, 0, r); assert(r[7] == 0);
    ap[0] = 14; ask(6, ap, sizeof ap, r); assert(r[6] == 1);       /* no channel 14 */
    ask(6, ap, 2, r); assert(r[6] == 1);                           /* short */
    n = ask(12, 0, 0, r); assert(n == MGMT_HDR + 1 && r[1] == (12 | 0x80) && r[6] == 1); /* no wired port in this build */
    /* LINK_STATS: tx ok, failed, dropped, link dropped, to the radio */
    n = ask(14, 0, 0, r);
    assert(n == MGMT_HDR + 1 + 4 * MGMT_STATS_N && r[1] == (14 | 0x80) && r[6] == 0 &&
           le32(r + 7) == 0x101 && le32(r + 11) == 0x202 && le32(r + 15) == 0x303 &&
           le32(r + 19) == 0x404 && le32(r + 23) == 0x505);

    /* FLOW: off until the driver asks; the reply is a report: status, free, total, on, taken */
    unsigned char rep[MGMT_HDR + MGMT_FLOW_LEN];
    assert(!mgmt_flow && mgmt_flow_due(rep) == 0);
    unsigned char on = 1, bad = 2;
    n = ask(13, &bad, 1, r); assert(n == MGMT_HDR + 1 && r[6] == 1 && !mgmt_flow);
    mgmt_taken = 40, fake_tx_free = 6;
    n = ask(13, &on, 1, r);
    assert(n == MGMT_HDR + MGMT_FLOW_LEN && r[1] == (13 | 0x80) && r[5] == MGMT_FLOW_LEN &&
           r[6] == 0 && r[7] == 6 && r[8] == WIFI_TX_BUFS && r[9] == 1 && le32(r + 10) == 40 && mgmt_flow);
    assert(mgmt_flow_due(rep) == 0);                                   /* nothing new */
    mgmt_taken = 44, fake_tx_free = 2;                                 /* four frames took four buffers */
    assert(mgmt_flow_due(rep) == 0);                                   /* the driver counted them */
    mgmt_taken = 46;                                                   /* two more, dropped: no buffer used */
    assert(mgmt_flow_due(rep) == MGMT_HDR + MGMT_FLOW_LEN);           /* 2 free, the driver thinks 0 */
    assert(rep[0] == 1 && rep[1] == (13 | 0x80) && rep[2] == 0 && rep[3] == 0 && rep[5] == MGMT_FLOW_LEN &&
           rep[7] == 2 && le32(rep + 10) == 46);
    assert(mgmt_flow_due(rep) == 0);
    fake_tx_free = 3;                                                  /* a buffer came back */
    assert(mgmt_flow_due(rep) && rep[7] == 3);
    mgmt_flow_lost();                                                  /* the link could not take it */
    assert(mgmt_flow_due(rep) && rep[7] == 3);
    mgmt_uptime += MGMT_FLOW_HEARTBEAT_MS - 1;
    assert(mgmt_flow_due(rep) == 0);
    mgmt_uptime += 1;                                                  /* the heartbeat */
    assert(mgmt_flow_due(rep) && rep[7] == 3);
    unsigned char off = 0;
    ask(13, &off, 1, r); assert(r[6] == 0 && r[9] == 0 && !mgmt_flow);
    fake_tx_free = 6, mgmt_uptime += 1000;
    assert(mgmt_flow_due(rep) == 0);                                   /* off: no reports */

    n = ask(99, 0, 0, r); assert(r[6] == 0xff && r[1] == (99 | 0x80));
    unsigned char q[8] = {2, 1, 0, 0, 0, 0}; assert(mgmt_handle(q, 6, r) == 0); /* wrong version */
    q[0] = 1, q[5] = 9; assert(mgmt_handle(q, 8, r) == 0);                      /* length past the end */
    assert(mgmt_handle(q, 3, r) == 0);                                            /* short */
    puts("mgmt ok");
    return 0;
}
