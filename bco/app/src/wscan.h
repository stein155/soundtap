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
    signed char rssi; /* dBm from the RX descriptor of its last beacon, 0 = not heard yet */
    unsigned short seen;
    unsigned short gen;  /* scan generation last heard in; older than scan_gen - BSS_KEEP = aged out */
    unsigned short caps;
    unsigned char ies[256];
    unsigned char ielen; /* 255 B cap; keep the RSN IE separately for the assoc request */
};
#define NBSS 24
/* A weak AP (the target, co-channel with a far stronger one) answers a probe only now and then, so
 * it is not in every scan. Keep the table across scans and age entries by scan generation, so a BSS
 * heard in any of the last BSS_KEEP scans stays selectable when the supplicant decides to join. */
#define BSS_KEEP 5u
static struct bss bsstab[NBSS];
static unsigned nbss, bssdrop, scan_gen;
#ifdef WILD_OWN
static unsigned dg_bss_short, dg_bss_nossid; /* bss_add rejects: frame too short / no SSID IE at +44 */
#endif

/* The beacon's level in dBm sits in the RX descriptor 0x14 bytes before the header (buf+0x98):
 * found on hardware 2026-10-09, steady per BSS, same for one router's two BSSIDs. */
#define RXRSSI (-0x14)

static int bss_add(const unsigned char *h, unsigned len, unsigned rxchan, signed char rssi)
{
    const unsigned char *ie = h + RXBODY + 12, *end = h + len;
    unsigned i, chan = rxchan, ssidlen = 0;
    const unsigned char *ssid = 0;
    struct bss *b = 0;
    int rsn = 0, wpa = 0;
    if (len < RXBODY + 12 + 2) {
#ifdef WILD_OWN
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
            rsn = 1;
        else if (ie[0] == 221 && ie[1] >= 4 && ie[2] == 0 && ie[3] == 0x50 && ie[4] == 0xf2 && ie[5] == 1)
            wpa = 1;
        ie += 2 + ie[1];
    }
    if (!ssid) {
#ifdef WILD_OWN
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
        if (nbss == NBSS)
            return ++bssdrop, 0;
        b = &bsstab[nbss++];
        for (i = 0; i < 6; i++)
            b->bssid[i] = h[16 + i];
    }
    for (i = 0; i < ssidlen; i++)
        b->ssid[i] = ssid[i];
    b->ssidlen = ssidlen, b->chan = chan, b->rsn = rsn, b->wpa = wpa, b->seen++, b->gen = scan_gen;
    if (rssi < 0)
        b->rssi = rssi;
    b->caps = h[RXBODY + 10] | h[RXBODY + 11] << 8;
    /* keep the IE block for the association request (RSN IE comes from here) */
    b->ielen = end - (h + RXBODY + 12) > 255 ? 255 : end - (h + RXBODY + 12);
    for (i = 0; i < b->ielen; i++)
        b->ies[i] = h[RXBODY + 12 + i];
    return 1;
}
