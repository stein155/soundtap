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

/* A PersParam sector for NV_INFO: the MAC, a one-byte setting and a 32-byte secret. */
#include "persparam.h"
static uint8_t nv[PERSPARAM_SIZE];
static unsigned nv_at = 0x20, nv_present = 1;
static void nv_rec(uint32_t key, const uint8_t *val, unsigned vl)
{
    unsigned rl = 16 + 4 + vl + 4;
    const uint32_t w[5] = {rl, vl, 0, 4, key};
    for (unsigned i = 0; i < 20; i++)
        nv[nv_at + i] = w[i / 4] >> 8 * (i % 4);
    memcpy(nv + nv_at + 20, val, vl);
    memset(nv + nv_at + 20 + vl, 0, 4);
    nv_at += (rl + 3) & ~3u; /* records start word-aligned */
}
const volatile unsigned char *mgmt_nv(void)
{
    if (nv_at == 0x20) {
        memset(nv, 0xff, sizeof nv);
        memcpy(nv + 0x10, "PersParam", 9);
        nv_rec(PP_KEY('M', 'A', 'C', '0'), (const uint8_t *)"\x00\x0c\x8a\x01\x02\x03\x00\x00", 8);
        nv_rec(PP_KEY('A', 'N', 'T', 'X'), (const uint8_t *)"\x01", 1);
        uint8_t secret[32];
        memset(secret, 0x5a, sizeof secret);
        nv_rec(PP_KEY('W', 'P', 'S', 'K'), secret, sizeof secret);
        nv_rec(PP_KEY('A', 'N', 'T', 'X'), (const uint8_t *)"\x02", 1); /* the newest value counts */
    }
    return nv_present ? nv : 0;
}
int main(void)
{
    unsigned char r[MGMT_MAX]; unsigned n;
    memcpy(mgmt_wifi_mac, "\x00\x0c\x8a\x01\x02\x03", 6);
    mgmt_uptime = 0x01020304;
    n = ask(1, 0, 0, r); /* HELLO: status, ver_len, ver, wifi mac, state, uptime, proto, caps */
    assert(n == MGMT_HDR + 24 && r[1] == 0x81 && r[3] == 8 && r[5] == 24 && r[6] == 0 && r[7] == 6 &&
           !memcmp(r + 8, "link-2", 6) && !memcmp(r + 14, mgmt_wifi_mac, 6) && r[21] == 4 &&
           r[24] == 1 && r[25] == BCOLINK_PROTO && le32(r + 26) == BCOLINK_CAPS &&
           (BCOLINK_CAPS & (BCOLINK_CAP_AP | BCOLINK_CAP_REPLAY | BCOLINK_CAP_FLOW | BCOLINK_CAP_STATS |
                            BCOLINK_CAP_EVENTS | BCOLINK_CAP_ROAM | BCOLINK_CAP_REGDOM |
                            BCOLINK_CAP_SCAN_SSID | BCOLINK_CAP_NVINFO | BCOLINK_CAP_APDIAG |
                            BCOLINK_CAP_SECURITY | BCOLINK_CAP_WPA3 | BCOLINK_CAP_APBEACON)) == BCOLINK_CAPS &&
           BCOLINK_CAPS == 0x1fff);
    n = ask(5, 0, 0, r); assert(r[6] == 0 && r[7] == WIFI_IDLE && r[8] == 0 && n == MGMT_HDR + 16 + MGMT_SCAN_DETAIL_LEN &&
                                r[17] == WIFI_FAIL_NONE && r[18] == 0 && r[19] == 0 && r[20] == 0);
    /* a refused association: the step and the AP's status code (le16) follow the BSS */
    extern unsigned fake_fail_step, fake_fail_code;
    fake_fail_step = WIFI_FAIL_ASSOC, fake_fail_code = 0x0131;
    ask(5, 0, 0, r); assert(r[17] == WIFI_FAIL_ASSOC && r[18] == 0x31 && r[19] == 0x01);
    fake_fail_step = fake_fail_code = 0;

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
    /* an entry the last scan did not hear (kept in the table for the join) is not listed */
    {
        extern unsigned fake_stale;
        fake_nbss = 3, fake_stale = 1u << 1;
        n = ask(2, 0, 0, r);
        assert(r[7] == 2 && n == MGMT_HDR + 2 + 2 * 13 && !memcmp(r + 9, "ap0", 3) && !memcmp(r + 22, "ap2", 3));
        fake_stale = 0;
    }
    fake_nbss = 24;
    n = ask(2, 0, 0, r); assert(r[7] == 24 && n <= MGMT_MAX);
    /* a full table of 32-byte SSIDs (a beacon flood) is more than one reply holds: the list stops
     * before the entry that would not fit, the count matches, nothing lands past the buffer */
    {
        static struct { unsigned char r[MGMT_MAX], guard[64]; } big;
        extern unsigned fake_ssid_max;
        memset(big.guard, 0x5c, sizeof big.guard);
        fake_ssid_max = 1, fake_nbss = 40;
        n = ask(2, 0, 0, big.r);
        unsigned at = MGMT_HDR + 2, k = 0;
        while (at < n)
            at += 1 + big.r[at] + 6 + 1 + 1 + 1, k++;
        assert(big.r[6] == 0 && big.r[7] == 36 && k == 36 && at == n && n == MGMT_HDR + 2 + 36 * 42 &&
               n <= MGMT_MAX && (big.r[4] << 8 | big.r[5]) == n - MGMT_HDR);
        for (unsigned i = 0; i < sizeof big.guard; i++)
            assert(big.guard[i] == 0x5c);
        /* with the security detail (MGMT_SCAN_DETAIL, a poll {2}): 6 bytes more per entry, fewer fit */
        const unsigned char det = MGMT_SCAN_DETAIL;
        n = ask(2, &det, 1, big.r);
        at = MGMT_HDR + 2, k = 0;
        while (at < n)
            at += 1 + big.r[at] + 6 + 1 + 1 + 1 + MGMT_SCAN_DETAIL_LEN, k++;
        assert(big.r[6] == 0 && big.r[7] == k && at == n && k == (MGMT_MAX - MGMT_HDR - 2) / 48 &&
               n <= MGMT_MAX && !fake_scanning);
        for (unsigned i = 0; i < sizeof big.guard; i++)
            assert(big.guard[i] == 0x5c);
        fake_ssid_max = 0;
        fake_nbss = 1;
        n = ask(2, &det, 1, r);
        assert(n == MGMT_HDR + 2 + 13 + MGMT_SCAN_DETAIL_LEN && r[21] == 0 && r[22] == 0); /* the fake's detail: zeros */
    }
    /* SCAN naming an SSID (BCOLINK_CAP_SCAN_SSID): the directed probe looks for it */
    extern unsigned char fake_scan_ssid[32];
    extern unsigned fake_scan_ssid_len;
    const unsigned char dir[] = {1, 3, 'H', 'i', 'd'};
    ask(2, dir, sizeof dir, r); assert(fake_scan_ssid_len == 3 && !memcmp(fake_scan_ssid, "Hid", 3));
    ask(2, dir, 4, r); assert(fake_scan_ssid_len == 0);                /* cut short: none */
    ask(2, dir, 2, r); assert(fake_scan_ssid_len == 0);
    fake_scan_ssid_len = 99;
    ask(2, 0, 0, r); assert(fake_scan_ssid_len == 99);                  /* a poll leaves it */
    ask(2, &one, 1, r); assert(fake_scan_ssid_len == 0);                /* a start without one clears it */
    fake_scanning = 0;

    /* profile: ssid, security 1, the 32-byte PMK, the 32-byte seed */
    unsigned char sp[1 + 4 + 2 + 32 + 32] = {4, 't', 'e', 's', 't', 1, 32};
    memset(sp + 7, 0xab, 32), memset(sp + 39, 0xcd, 32);
    extern unsigned fake_join_flags;
    extern unsigned char fake_join_bssid[6];
    ask(3, sp, sizeof sp, r);
    assert(r[6] == 0 && fake_joins == 1 && fake_ssid_len == 4 && fake_pmk[31] == 0xab &&
           fake_seed[0] == 0xcd && fake_join_flags == 0xff);             /* no tail: no BSSID, no roaming */
    /* the reply got lost: the driver resends with the same seq and must not join twice */
    unsigned char first[MGMT_MAX]; memcpy(first, r, MGMT_HDR + 1);
    n = again(3, sp, sizeof sp, r);
    assert(fake_joins == 1 && n == MGMT_HDR + 1 && !memcmp(r, first, n));
    again(5, 0, 0, r); assert(r[7] == WIFI_JOINING); /* a query with a stale seq still runs */
    /* the target's RSN group cipher comes last (2 = TKIP, a mixed-mode router) */
    extern unsigned fake_grp;
    fake_grp = 2;
    n = ask(5, 0, 0, r);
    assert(r[7] == WIFI_JOINING && r[8] == 4 && !memcmp(r + 9, "test", 4) && r[19] == 6 &&
           (signed char)r[20] == -50 && n == MGMT_HDR + 15 + 5 + MGMT_SCAN_DETAIL_LEN && r[24] == 2 && r[25] == WIFI_BSS_RSN &&
           r[26] == 2 && r[27] == WIFI_SET_CCMP && r[28] == WIFI_SET_PSK);
    fake_grp = 0;
    for (unsigned i = 0; i + 4 <= n; i++) assert(memcmp(r + i, "\xab\xab\xab\xab", 4)); /* PMK never echoed */
    ask(3, sp, sizeof sp - 1, r); assert(r[6] == 1);              /* no seed */
    /* the tail (BCOLINK_CAP_ROAM): flags and the BSSID */
    unsigned char spt[sizeof sp + MGMT_PROFILE_TAIL];
    memcpy(spt, sp, sizeof sp);
    spt[sizeof sp] = MGMT_PROFILE_BSSID_HINT | MGMT_PROFILE_ROAM;
    memcpy(spt + sizeof sp + 1, "\x02\x00\x00\x00\x00\x42", 6);
    ask(3, spt, sizeof spt, r);
    assert(r[6] == 0 && fake_joins == 2 && fake_join_flags == (MGMT_PROFILE_BSSID_HINT | MGMT_PROFILE_ROAM) &&
           fake_join_bssid[5] == 0x42);
    ask(3, spt, sizeof spt - 1, r); assert(r[6] == 1 && fake_joins == 2); /* a short tail */
    sp[5] = 0; ask(3, sp, sizeof sp, r); assert(r[6] == 2);       /* open with a key: unsupported */
    sp[5] = 7; ask(3, sp, sizeof sp, r); assert(r[6] == 2);       /* an unknown security */
    sp[5] = 1, sp[0] = 0; ask(3, sp, sizeof sp, r); assert(r[6] == 1); /* empty ssid */
    ask(3, sp, 0, r); assert(r[6] == 1);                           /* no payload at all */
    assert(fake_joins == 2);
    /* an open network (security 0): no key, the seed */
    extern unsigned fake_sec, fake_key_len;
    unsigned char so[1 + 4 + 2 + 32] = {4, 'o', 'p', 'e', 'n', 0, 0};
    memset(so + 7, 0xcd, 32);
    ask(3, so, sizeof so, r);
    assert(r[6] == 0 && fake_joins == 3 && fake_sec == 0 && fake_key_len == 0 && fake_seed[0] == 0xcd);
    so[6] = 1; ask(3, so, sizeof so, r); assert(r[6] == 1);       /* a key length past the end */
    sp[0] = 4;
    unsigned char sk[sizeof sp - 16];                              /* PSK with a 16-byte key */
    memcpy(sk, sp, 7), sk[6] = 16, memset(sk + 7, 0xab, 16), memset(sk + 23, 0xcd, 32);
    ask(3, sk, sizeof sk, r); assert(r[6] == 2 && fake_joins == 3);
    /* SAE (security 3, no key) and the SCM's side of it (BCOLINK_CAP_WPA3) */
    unsigned char ss[1 + 3 + 2 + 32] = {3, 'w', 'p', '3', 3, 0};
    memset(ss + 6, 0xcd, 32);
    ask(3, ss, sizeof ss, r); assert(r[6] == 0 && fake_sec == 3 && fake_key_len == 0);
    ss[5] = 32; ask(3, ss, sizeof ss, r); assert(r[6] == 1); /* a key length past the end */
    extern unsigned char fake_sae_out[600], fake_sae_tx[600], fake_sae_pmk[32], fake_sae_bssid[6];
    extern unsigned fake_sae_out_len, fake_sae_tx_len, fake_sae_status, fake_sae_pmks;
    const unsigned char ea[8] = {0, 0, 2, 0, 0, 0, 0, 0x42};
    ask(18, ea, sizeof ea, r); assert(r[6] == 0 && fake_sae_status == 0 && fake_sae_bssid[5] == 0x42);
    ask(18, ea, 7, r); assert(r[6] == 1);
    const unsigned char commit[8] = {3, 0, 1, 0, 0, 0, 19, 0};
    ask(19, commit, sizeof commit, r); assert(r[6] == 0 && fake_sae_tx_len == 8 && !memcmp(fake_sae_tx, commit, 8));
    ask(19, commit, 0, r); assert(r[6] == 1);
    unsigned char sp20[6 + 32];
    memset(sp20, 0x77, sizeof sp20);
    n = ask(20, sp20, sizeof sp20, r);
    assert(r[6] == 0 && fake_sae_pmks == 1 && fake_sae_pmk[31] == 0x77 && n == MGMT_HDR + 1); /* never echoed */
    ask(20, sp20, 37, r); assert(r[6] == 1 && fake_sae_pmks == 1);
    /* the station's messages go up unasked: EXT_AUTH (start, bssid, ssid) and AUTH_FRAME */
    static unsigned char up[MGMT_HDR + 1 + 24 + MGMT_SAE_MAX];
    fake_sae_out[0] = WIFI_SAE_START, memcpy(fake_sae_out + 1, "\x02\x00\x00\x00\x00\x42", 6);
    fake_sae_out[7] = 3, memcpy(fake_sae_out + 8, "wp3", 3), fake_sae_out_len = 11;
    n = mgmt_sae_due(up);
    assert(n == MGMT_HDR + 12 && up[1] == (18 | 0x80) && up[2] == 0 && up[3] == 0 && up[6] == 0 &&
           up[7] == MGMT_EXT_AUTH_START && up[13] == 0x42 && up[14] == 3 && !memcmp(up + 15, "wp3", 3));
    assert(!mgmt_sae_due(up));
    mgmt_sae_lost(); /* the link refused it: the same message again */
    assert(mgmt_sae_due(up) == n && up[7] == MGMT_EXT_AUTH_START);
    fake_sae_out[0] = WIFI_SAE_FRAME;
    memset(fake_sae_out + 1, 0xb0, 24 + 8), fake_sae_out_len = 1 + 24 + 8;
    n = mgmt_sae_due(up);
    assert(n == MGMT_HDR + 1 + 24 + 8 && up[1] == (19 | 0x80) && up[6] == 0 && up[7] == 0xb0);
    ask(4, 0, 0, r); assert(r[6] == 0 && fake_leaves == 1);
    assert(fake_resets == 0); /* FORGET leaves the network; the core keeps running */
    ask(5, 0, 0, r); assert(r[7] == WIFI_IDLE && r[8] == 0);
    /* AP: channel 6, 100 TU, a 40-byte body; status lists the clients; stop */
    extern unsigned fake_ap_chan, fake_ap_bint, fake_ap_len, fake_ap_stops;
    unsigned char ap[3 + 40] = {6, 0, 100};
    ask(6, ap, sizeof ap, r);
    assert(r[6] == 0 && fake_ap_chan == 6 && fake_ap_bint == 100 && fake_ap_len == 40);
    n = ask(8, 0, 0, r);
    assert(r[6] == 0 && r[7] == 1 && r[13] == 0x77 &&
           n == MGMT_HDR + 8 + 4 * MGMT_AP_COUNTERS + 4 * MGMT_AP_DIAG + MGMT_AP_CLIENT + 4 * MGMT_AP_EXTRA +
                    MGMT_AP_CLIENT2 + 4 * MGMT_AP_EXTRA2 + MGMT_AP_CLIENT3 &&
           r[14] == 0x11 && r[18] == 0x22);
    /* the diagnostics tail (BCOLINK_CAP_APDIAG), then the one client's block */
    const unsigned char *t = r + 14 + 4 * MGMT_AP_COUNTERS, *k = t + 4 * MGMT_AP_DIAG;
    assert(le32(t) == 0x1000 && le32(t + 4 * (MGMT_AP_DIAG - 1)) == 0x1000 + MGMT_AP_DIAG - 1);
    assert(k[0] == 1 && k[1] == 2 && (signed char)k[2] == -45 && k[3] == 0x6c && le32(k + 4) == 250 &&
           le32(k + 8) == 7 && le32(k + 12) == 3 && le32(k + 16) == 5 && le32(k + 20) == 1);
    assert(le32(k + MGMT_AP_CLIENT) == 0x2000 && le32(k + MGMT_AP_CLIENT + 4 * (MGMT_AP_EXTRA - 1)) == 0x2006);
    const unsigned char *k2 = k + MGMT_AP_CLIENT + 4 * MGMT_AP_EXTRA;
    assert(le32(k2) == 0x31 && k2[4] == 0x12 && k2[5] == 0 && k2[6] == 0x30 && k2[7] == 0x12);
    /* then the last block (failures by kind, lost beacons, the core's kinds) and the client's nulls */
    const unsigned char *x2 = k2 + MGMT_AP_CLIENT2, *k3 = x2 + 4 * MGMT_AP_EXTRA2;
    assert(le32(x2) == 0x3000 && le32(x2 + 4 * (MGMT_AP_EXTRA2 - 1)) == 0x3000 + MGMT_AP_EXTRA2 - 1);
    assert(le32(k3) == 711 && le32(k3 + 4) == 40 && le32(k3 + 8) == 2 && le32(k3 + 12) == 35);
    /* AP_BEACON: hostapd's update replaces the body while the AP runs */
    extern unsigned fake_ap_beacons;
    n = ask(MGMT_AP_BEACON, ap + 3, 30, r);
    assert(n == MGMT_HDR + 1 && r[1] == (MGMT_AP_BEACON | 0x80) && r[6] == 0 && fake_ap_beacons == 1 &&
           fake_ap_len == 30);
    ask(7, 0, 0, r); assert(r[6] == 0 && fake_ap_stops == 1);
    ask(MGMT_AP_BEACON, ap + 3, 30, r); assert(r[6] == 1 && fake_ap_beacons == 1); /* no AP: refused */
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

    /* EVENTS: nothing unasked until the driver asks; then each event once, a lost one again */
    extern struct wifi_event fake_evq[8];
    extern unsigned fake_evn;
    unsigned char evm[MGMT_HDR + MGMT_EVENT_LEN];
    fake_evq[0] = (struct wifi_event){.type = WIFI_EV_BEACON_LOSS};
    fake_evn = 1;
    assert(!mgmt_events && mgmt_event_due(evm) == 0 && fake_evn == 1);
    n = ask(15, &on, 1, r);
    assert(n == MGMT_HDR + MGMT_EVENTS_REPLY_LEN && r[1] == (15 | 0x80) && r[6] == 0 && r[7] == 1 &&
           mgmt_events && fake_evn == 0);                             /* older events dropped */
    again(15, &off, 1, r);                                             /* a resend: the kept reply */
    assert(mgmt_events && r[7] == 1);
    assert(mgmt_event_due(evm) == 0);
    fake_evq[0] = (struct wifi_event){.type = WIFI_EV_DEAUTH, .bssid = {2, 0, 0, 0, 0, 9}, .chan = 11,
                                      .reason = 0x0103, .rssi = -61};
    fake_evq[1] = (struct wifi_event){.type = WIFI_EV_ROAMED, .bssid = {2, 0, 0, 0, 0, 10}, .chan = 1};
    fake_evn = 2;
    assert(mgmt_event_due(evm) == MGMT_HDR + MGMT_EVENT_LEN);
    assert(evm[0] == 1 && evm[1] == (15 | 0x80) && evm[2] == 0 && evm[3] == 0 && evm[5] == MGMT_EVENT_LEN &&
           evm[6] == 0 && evm[7] == MGMT_EV_DEAUTH && evm[8] == 3 && evm[9] == 1 && evm[15] == 9 &&
           evm[16] == 11 && (signed char)evm[17] == -61);
    mgmt_event_lost();                                                 /* the link could not take it */
    assert(mgmt_event_due(evm) && evm[7] == MGMT_EV_DEAUTH);
    assert(mgmt_event_due(evm) && evm[7] == MGMT_EV_ROAMED && evm[15] == 10);
    assert(mgmt_event_due(evm) == 0);
    ask(15, &bad, 1, r); assert(r[6] == 1);
    ask(15, &off, 1, r); assert(r[6] == 0 && r[7] == 0 && !mgmt_events);

    /* SET_REGDOM: country, channels, passive, dBm; the reply says the limit is not enforced */
    extern unsigned char fake_cc[2];
    extern unsigned fake_chans, fake_passive;
    extern int fake_dbm;
    const unsigned char rd[MGMT_REGDOM_LEN] = {'N', 'L', 0xff, 0x1f, 0x00, 0x18, 20};
    n = ask(16, rd, sizeof rd, r);
    assert(n == MGMT_HDR + 2 && r[6] == 0 && r[7] == 0 && !memcmp(fake_cc, "NL", 2) &&
           fake_chans == 0x1fff && fake_passive == 0x1800 && fake_dbm == 20);
    ask(16, rd, sizeof rd - 1, r); assert(r[6] == 1);                 /* short */
    const unsigned char rd0[MGMT_REGDOM_LEN] = {'N', 'L', 0, 0, 0, 0, 20};
    ask(16, rd0, sizeof rd0, r); assert(r[6] == 1 && fake_chans == 0x1fff); /* no channel: refused */

    ask(99, 0, 0, r); assert(r[6] == 0xff && r[1] == (99 | 0x80));
    unsigned char q[8] = {2, 1, 0, 0, 0, 0}; assert(mgmt_handle(q, 6, r) == 0); /* wrong version */
    q[0] = 1, q[5] = 9; assert(mgmt_handle(q, 8, r) == 0);                      /* length past the end */
    assert(mgmt_handle(q, 3, r) == 0);                                            /* short */
    /* NV_INFO: keys and lengths; a value only when it is at most 4 bytes */
    n = ask(17, 0, 0, r);
    assert(r[1] == (17 | 0x80) && r[6] == 0 && r[7] == 3 && n == MGMT_HDR + 2 + 3 * MGMT_NV_ENTRY);
    assert(le32(r + 8) == PP_KEY('M', 'A', 'C', '0') && r[12] == 8 && r[13] == 0 && !le32(r + 14));
    assert(le32(r + 18) == PP_KEY('A', 'N', 'T', 'X') && r[22] == 1 && r[24] == 2 && !r[25]);
    assert(le32(r + 28) == PP_KEY('W', 'P', 'S', 'K') && r[32] == 32 && !le32(r + 34));
    for (unsigned i = 0; i + 4 <= n; i++) assert(memcmp(r + i, "\x5a\x5a\x5a\x5a", 4)); /* the long secret never */
    nv_present = 0;
    n = ask(17, 0, 0, r); assert(n == MGMT_HDR + 2 && r[6] == 1 && r[7] == 0);
    puts("mgmt ok");
    return 0;
}
