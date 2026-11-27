/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Beacon parser for the scan table (ADR 0031 step b). Reads frames only; the header is found at
 * buf+0xac (hardware 2026-10-02), inside the 0x38-byte RX descriptor at buf+0x94. The body (timestamp
 * 8, interval 2, capability 2, then IEs) starts after that descriptor at buf+0xcc = header + RXBODY,
 * not header + 24 (hardware 2026-10-04: SSID+rates signature at +44 in 560 of 563 frames). Logs stay
 * counters and the owner's own profile. */
#define RXBODY 32
struct bss {
    unsigned char bssid[6], ssid[33], ssidlen, chan, rsn, wpa;
    unsigned char grp;   /* RSN group cipher suite type (00-0F-AC:2 TKIP, :4 CCMP), 0 = other */
    unsigned char rsnok; /* the RSN IE lets this station join: pairwise CCMP, AKM PSK, no PMF required */
    signed char rssi; /* dBm from the RX descriptor of its last beacon, 0 = not heard yet */
    unsigned short seen;
    unsigned short gen;  /* scan generation last heard in; older than scan_gen - BSS_KEEP = aged out */
    unsigned short caps;
    unsigned char ies[256];
    unsigned char ielen; /* 255 B cap; keep the RSN IE separately for the assoc request */
};
/* Dense housing hears 30-40 BSSes on 2.4 GHz (an ISP router alone beacons its main, guest and
 * mesh-backhaul BSSIDs). A full table evicts the entry heard longest ago; when every entry is from
 * this scan a stranger is dropped. Channels are scanned 1..13 in order, so in a crowd the table
 * can fill before a high channel is reached: a BSS of a wanted SSID (bss_want: the profile's and
 * the directed scan's) then takes the weakest stranger's slot, and no stranger ever takes a wanted
 * entry's slot. So the owner's network is not shut out by the neighbours. */
#define NBSS 40
/* A weak AP (the target, co-channel with a far stronger one) answers a probe only now and then, so
 * it is not in every scan. Keep the table across scans and age entries by scan generation, so a BSS
 * heard in any of the last BSS_KEEP scans stays selectable when the supplicant decides to join. */
#define BSS_KEEP 5u
static struct bss bsstab[NBSS];
static unsigned nbss, bssdrop, scan_gen;

/* The SSIDs whose BSSes always get a slot: 0 the profile's, 1 the one the SCM's SCAN named.
 * wifi.c sets them; n = 0 clears one. */
static unsigned char want_ssid[2][32], want_len[2];

static void bss_want(unsigned i, const unsigned char *ssid, unsigned n)
{
    want_len[i] = n <= 32 ? (unsigned char)n : 0;
    for (unsigned k = 0; k < want_len[i]; k++)
        want_ssid[i][k] = ssid[k];
}

static int bss_wanted(const unsigned char *ssid, unsigned n)
{
    for (unsigned i = 0; i < 2; i++) {
        unsigned k = 0;
        if (!want_len[i] || want_len[i] != n)
            continue;
        while (k < n && ssid[k] == want_ssid[i][k])
            k++;
        if (k == n)
            return 1;
    }
    return 0;
}
#ifdef BCO_DEBUG_TRACE
static unsigned dg_bss_short, dg_bss_nossid; /* bss_add rejects: frame too short / no SSID IE at +44 */
#endif

/* The beacon's level in dBm sits in the RX descriptor 0x14 bytes before the header (buf+0x98):
 * found on hardware 2026-10-09, steady per BSS, same for one router's two BSSIDs. */
#define RXRSSI (-0x14)

/* A hidden network's beacon carries an empty SSID, or one of NUL bytes. */
static int ssid_hidden(const unsigned char *s, unsigned n)
{
    while (n--)
        if (*s++)
            return 0;
    return 1;
}

/* A cipher or AKM suite: its type under the IEEE OUI 00-0F-AC, 0x100 for any other OUI. */
static unsigned rsn_suite(const unsigned char *p)
{
    return p[0] == 0x00 && p[1] == 0x0f && p[2] == 0xac ? p[3] : 0x100u;
}

/* An RSN IE's body (IEEE 802.11-2020 9.4.2.24): version 1, group suite, pairwise suites, AKM
 * suites, capabilities. An IE that ends before its AKM list takes the standard's default AKM,
 * 802.1X, so it is never one this station joins; missing capabilities are 0. Sets the group cipher
 * and returns 1 when this station can join: pairwise CCMP offered, AKM PSK offered, management
 * frame protection not required (MFPR, capabilities bit 6; bit 7 is MFPC) and a CCMP or TKIP group
 * cipher. A WPA2/WPA3 transition network (PSK and SAE, MFPC set, MFPR clear) qualifies; WPA3-only
 * or PMF-required does not. */
static int rsn_parse(const unsigned char *p, unsigned n, unsigned char *grp)
{
    unsigned o = 6, cnt, i, g, pccmp = 0, psk = 0, caps = 0;
    *grp = 0;
    if (n < o + 2 || p[0] != 1 || p[1] != 0)
        return 0;
    g = rsn_suite(p + 2);
    cnt = p[o] | p[o + 1] << 8, o += 2;
    if (cnt > (n - o) / 4)
        return 0;
    for (i = 0; i < cnt; i++, o += 4)
        pccmp |= rsn_suite(p + o) == 4;
    if (n < o + 2)
        return 0;
    cnt = p[o] | p[o + 1] << 8, o += 2;
    if (cnt > (n - o) / 4)
        return 0;
    for (i = 0; i < cnt; i++, o += 4)
        psk |= rsn_suite(p + o) == 2;
    if (n >= o + 2)
        caps = p[o] | p[o + 1] << 8;
    if (g != 2 && g != 4)
        return 0;
    *grp = (unsigned char)g;
    return pccmp && psk && !(caps & 0x40);
}

/* A level to compare: a BSS without one counts as the weakest. */
static int bss_level(const struct bss *b)
{
    return b->rssi < 0 ? b->rssi : -128;
}

/* The slot a newcomer takes in a full table, -1 when it is dropped: never a wanted SSID's entry;
 * of the others the one heard in the oldest scan, on a tie the weakest. A stranger never takes
 * one heard in this scan; a wanted newcomer does when every other entry is current. */
static int bss_evict(int wanted)
{
    int old = -1;
    unsigned age = 0;
    for (unsigned i = 0; i < nbss; i++) {
        const struct bss *b = &bsstab[i];
        unsigned a = (unsigned short)(scan_gen - b->gen);
        if (bss_wanted(b->ssid, b->ssidlen) || (!a && !wanted))
            continue;
        if (old < 0 || a > age || (a == age && bss_level(b) < bss_level(&bsstab[old])))
            age = a, old = (int)i;
    }
    return old;
}

static int bss_add(const unsigned char *h, unsigned len, unsigned rxchan, signed char rssi)
{
    const unsigned char *ie = h + RXBODY + 12, *end = h + len;
    unsigned i, chan = rxchan, ssidlen = 0;
    const unsigned char *ssid = 0;
    struct bss *b = 0;
    int rsn = 0, wpa = 0, rsnok = 0;
    unsigned char grp = 0;
    if (len < RXBODY + 12 + 2) {
#ifdef BCO_DEBUG_TRACE
        dg_bss_short++;
#endif
        return 0;
    }
    while (ie + 2 <= end && ie + 2 + ie[1] <= end) {
        if (ie[0] == 0 && ie[1] <= 32)
            ssid = ie + 2, ssidlen = ie[1];
        else if (ie[0] == 3 && ie[1] == 1)
            chan = ie[2];
        else if (ie[0] == 48)
            rsn = 1, rsnok = rsn_parse(ie + 2, ie[1], &grp);
        else if (ie[0] == 221 && ie[1] >= 4 && ie[2] == 0 && ie[3] == 0x50 && ie[4] == 0xf2 && ie[5] == 1)
            wpa = 1;
        ie += 2 + ie[1];
    }
    if (!ssid) {
#ifdef BCO_DEBUG_TRACE
        dg_bss_nossid++;
#endif
        return 0;
    }
    for (i = 0; i < nbss; i++) {
        unsigned k;
        for (k = 0; k < 6 && bsstab[i].bssid[k] == h[16 + k]; k++) {}
        if (k == 6) {
            b = &bsstab[i];
            break;
        }
    }
    if (!b) {
        if (nbss == NBSS) {
            int e = bss_evict(bss_wanted(ssid, ssidlen));
            if (e < 0)
                return ++bssdrop, 0;
            b = &bsstab[e];
        } else {
            b = &bsstab[nbss++];
        }
        for (i = 0; i < 6; i++)
            b->bssid[i] = h[16 + i];
        b->ssidlen = 0, b->rssi = 0, b->seen = 0; /* the slot may hold an aged-out BSS */
    }
    /* A directed probe's response names a hidden network; its next beacon, with the SSID left
     * out, must not take the name away again. */
    if (!ssid_hidden(ssid, ssidlen) || ssid_hidden(b->ssid, b->ssidlen)) {
        for (i = 0; i < ssidlen; i++)
            b->ssid[i] = ssid[i];
        b->ssidlen = ssidlen;
    }
    b->chan = chan, b->rsn = rsn, b->wpa = wpa, b->seen++, b->gen = scan_gen;
    b->grp = grp, b->rsnok = rsnok;
    if (rssi < 0)
        b->rssi = rssi;
    b->caps = h[RXBODY + 10] | h[RXBODY + 11] << 8;
    /* keep the IE block for the association request (RSN IE comes from here) */
    b->ielen = end - (h + RXBODY + 12) > 255 ? 255 : end - (h + RXBODY + 12);
    for (i = 0; i < b->ielen; i++)
        b->ies[i] = h[RXBODY + 12 + i];
    return 1;
}
