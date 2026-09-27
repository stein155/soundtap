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
           r[26] == (BCOLINK_CAP_AP | BCOLINK_CAP_REPLAY) && r[27] == 0 && r[29] == 0);
    n = ask(5, 0, 0, r); assert(r[6] == 0 && r[7] == WIFI_IDLE && r[8] == 0 && n == MGMT_HDR + 11);

    /* scan: start -> still scanning (3), poll -> results */
    unsigned char one = 1;
    n = ask(2, &one, 1, r); assert(r[6] == 3 && r[7] == 0 && fake_scanning);
    n = ask(2, 0, 0, r); assert(r[6] == 3);
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
    n = ask(3, sp, sizeof sp, r);
    assert(r[6] == 0 && fake_joins == 1 && fake_ssid_len == 4 && fake_pmk[31] == 0xab &&
           fake_seed[0] == 0xcd);
    /* the reply got lost: the driver resends with the same seq and must not join twice */
    unsigned char first[MGMT_MAX]; memcpy(first, r, MGMT_HDR + 1);
    n = again(3, sp, sizeof sp, r);
    assert(fake_joins == 1 && n == MGMT_HDR + 1 && !memcmp(r, first, n));
    n = again(5, 0, 0, r); assert(r[7] == WIFI_JOINING); /* a query with a stale seq still runs */
    n = ask(5, 0, 0, r);
    assert(r[7] == WIFI_JOINING && r[8] == 4 && !memcmp(r + 9, "test", 4) && r[19] == 6 &&
           (signed char)r[20] == -50);
    for (unsigned i = 0; i + 4 <= n; i++) assert(memcmp(r + i, "\xab\xab\xab\xab", 4)); /* PMK never echoed */
    n = ask(3, sp, sizeof sp - 1, r); assert(r[6] == 1);              /* no seed */
    sp[5] = 0; n = ask(3, sp, sizeof sp, r); assert(r[6] == 2);       /* open: unsupported */
    sp[5] = 1, sp[0] = 0; n = ask(3, sp, sizeof sp, r); assert(r[6] == 1); /* empty ssid */
    assert(fake_joins == 1);
    n = ask(4, 0, 0, r); assert(r[6] == 0 && fake_leaves == 1);
    n = ask(5, 0, 0, r); assert(r[7] == WIFI_IDLE && r[8] == 0);
    /* AP: channel 6, 100 TU, a 40-byte body; status lists the clients; stop */
    extern unsigned fake_ap_chan, fake_ap_bint, fake_ap_len, fake_ap_stops;
    unsigned char ap[3 + 40] = {6, 0, 100};
    n = ask(6, ap, sizeof ap, r);
    assert(r[6] == 0 && fake_ap_chan == 6 && fake_ap_bint == 100 && fake_ap_len == 40);
    n = ask(8, 0, 0, r);
    assert(r[6] == 0 && r[7] == 1 && r[13] == 0x77 && n == MGMT_HDR + 8 + 4 * WIFI_AP_COUNTERS &&
           r[14] == 0x11 && r[18] == 0x22);
    n = ask(7, 0, 0, r); assert(r[6] == 0 && fake_ap_stops == 1);
    n = ask(8, 0, 0, r); assert(r[7] == 0);
    ap[0] = 14; n = ask(6, ap, sizeof ap, r); assert(r[6] == 1);       /* no channel 14 */
    n = ask(6, ap, 2, r); assert(r[6] == 1);                           /* short */
    n = ask(12, 0, 0, r); assert(n == MGMT_HDR + 1 && r[1] == (12 | 0x80) && r[6] == 1); /* no wired port in this build */
    n = ask(99, 0, 0, r); assert(r[6] == 0xff && r[1] == (99 | 0x80));
    unsigned char q[8] = {2, 1, 0, 0, 0, 0}; assert(mgmt_handle(q, 6, r) == 0); /* wrong version */
    q[0] = 1, q[5] = 9; assert(mgmt_handle(q, 8, r) == 0);                      /* length past the end */
    assert(mgmt_handle(q, 3, r) == 0);                                            /* short */
    puts("mgmt ok");
    return 0;
}
