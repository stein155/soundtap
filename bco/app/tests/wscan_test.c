/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Host unit test for the scan table (wscan.h): a beacon's RSSI from its RX descriptor, which RSN IEs
 * this station can join, hidden SSIDs, and a full table evicting the entry heard longest ago, with
 * room always made for the profile's network.
 *
 *   cc -Wall -Werror -Isrc -o wscan_test tests/wscan_test.c && ./wscan_test
 */
#include <assert.h>
#include <stdio.h>
#include <string.h>

#include "../src/wscan.h"

/* One frame as the core hands it over: descriptor bytes up to the header, then header + body
 * (fixed fields, then the IEs given). */
static unsigned frame(unsigned char *buf, unsigned char fc, unsigned char last, signed char rssi,
                      const unsigned char *ies, unsigned n)
{
    unsigned char *h = buf + 0x14;
    memset(buf, 0, 0x14 + RXBODY + 12 + n);
    buf[0x14 + RXRSSI] = (unsigned char)rssi;
    h[0] = fc;
    memcpy(h + 16, "\x02\x00\x00\x00\x00", 5), h[21] = last;
    memcpy(h + RXBODY + 12, ies, n);
    return RXBODY + 12 + n;
}

static unsigned beacon(unsigned char *buf, unsigned char last, signed char rssi)
{
    static const unsigned char ssid[] = {0, 4, 'H', 'o', 'm', 'e', 3, 1, 6};
    return frame(buf, 0x80, last, rssi, ssid, sizeof ssid);
}

static int rsn(const unsigned char *ie, unsigned n, unsigned char *grp)
{
    return rsn_parse(ie, n, grp);
}

static void rsn_cases(void)
{
    unsigned char g;
    /* WPA2-PSK, CCMP/CCMP */
    static const unsigned char wpa2[] = {1, 0, 0, 0x0f, 0xac, 4, 1, 0, 0, 0x0f, 0xac, 4,
                                         1, 0, 0, 0x0f, 0xac, 2, 0, 0};
    assert(rsn(wpa2, sizeof wpa2, &g) == 1 && g == 4);
    /* WPA/WPA2 mixed: TKIP group, TKIP+CCMP pairwise */
    static const unsigned char mixed[] = {1, 0, 0, 0x0f, 0xac, 2, 2, 0, 0, 0x0f, 0xac, 2, 0, 0x0f,
                                          0xac, 4, 1, 0, 0, 0x0f, 0xac, 2, 0, 0};
    assert(rsn(mixed, sizeof mixed, &g) == 1 && g == 2);
    /* WPA2/WPA3 transition: PSK + SAE, MFPC set, MFPR clear */
    static const unsigned char trans[] = {1, 0, 0, 0x0f, 0xac, 4, 1, 0, 0, 0x0f, 0xac, 4, 2, 0, 0,
                                          0x0f, 0xac, 2, 0, 0x0f, 0xac, 8, 0x80, 0};
    assert(rsn(trans, sizeof trans, &g) == 1 && g == 4);
    /* WPA3 only: SAE */
    static const unsigned char sae[] = {1, 0, 0, 0x0f, 0xac, 4, 1, 0, 0, 0x0f, 0xac, 4,
                                        1, 0, 0, 0x0f, 0xac, 8, 0xc0, 0};
    assert(rsn(sae, sizeof sae, &g) == 0);
    /* PSK but management frame protection required (MFPR, bit 6) */
    static const unsigned char mfpr[] = {1, 0, 0, 0x0f, 0xac, 4, 1, 0, 0, 0x0f, 0xac, 4,
                                         1, 0, 0, 0x0f, 0xac, 2, 0x40, 0};
    assert(rsn(mfpr, sizeof mfpr, &g) == 0);
    /* TKIP pairwise only */
    static const unsigned char tkip[] = {1, 0, 0, 0x0f, 0xac, 2, 1, 0, 0, 0x0f, 0xac, 2,
                                         1, 0, 0, 0x0f, 0xac, 2, 0, 0};
    assert(rsn(tkip, sizeof tkip, &g) == 0);
    /* enterprise (802.1X) */
    static const unsigned char eap[] = {1, 0, 0, 0x0f, 0xac, 4, 1, 0, 0, 0x0f, 0xac, 4,
                                        1, 0, 0, 0x0f, 0xac, 1, 0, 0};
    assert(rsn(eap, sizeof eap, &g) == 0);
    /* truncated after the pairwise list: the AKM defaults to 802.1X */
    assert(rsn(wpa2, 12, &g) == 0);
    /* a count past the end, a wrong version */
    static const unsigned char lie[] = {1, 0, 0, 0x0f, 0xac, 4, 9, 0, 0, 0x0f, 0xac, 4};
    assert(rsn(lie, sizeof lie, &g) == 0);
    static const unsigned char v2[] = {2, 0, 0, 0x0f, 0xac, 4};
    assert(rsn(v2, sizeof v2, &g) == 0);
}

/* A beacon from BSSID 02:00:00:00:<hi>:<lo> for ssid. */
static unsigned beacon_from(unsigned char *buf, unsigned id, const char *ssid, unsigned n)
{
    unsigned char *h = buf + 0x14;
    memset(buf, 0, 0x14 + RXBODY + 12 + 2 + n);
    buf[0x14 + RXRSSI] = (unsigned char)-60;
    h[0] = 0x80;
    h[16] = 2, h[20] = (unsigned char)(id >> 8), h[21] = (unsigned char)id;
    h[RXBODY + 12] = 0, h[RXBODY + 13] = (unsigned char)n;
    memcpy(h + RXBODY + 14, ssid, n);
    return RXBODY + 12 + 2 + n;
}

static void table_cases(void)
{
    unsigned char buf[0x14 + 128];
    unsigned len;
    nbss = 0, scan_gen = 10;
    /* fill the table in scan 10 */
    for (unsigned i = 0; i < NBSS; i++) {
        len = beacon_from(buf, 100 + i, "neighbour", 9);
        assert(bss_add(buf + 0x14, len, 1, -60));
    }
    assert(nbss == NBSS);
    /* still scan 10: every entry is current, a stranger is dropped */
    len = beacon_from(buf, 999, "Home", 4);
    assert(!bss_add(buf + 0x14, len, 1, -60) && nbss == NBSS);
    /* scan 11: entry 3 heard again, the rest not yet; the newcomer takes an old slot */
    scan_gen = 11;
    len = beacon_from(buf, 103, "neighbour", 9);
    assert(bss_add(buf + 0x14, len, 1, -60));
    len = beacon_from(buf, 999, "Home", 4);
    assert(bss_add(buf + 0x14, len, 1, -60) && nbss == NBSS);
    unsigned found = 0;
    for (unsigned i = 0; i < nbss; i++) {
        found += bsstab[i].ssidlen == 4 && !memcmp(bsstab[i].ssid, "Home", 4) && bsstab[i].seen == 1;
        assert(!(bsstab[i].bssid[4] == 0 && bsstab[i].bssid[5] == 103) || bsstab[i].gen == 11);
    }
    assert(found == 1);
    nbss = 0;
}

static int in_table(unsigned id)
{
    for (unsigned i = 0; i < nbss; i++)
        if (bsstab[i].bssid[4] == (unsigned char)(id >> 8) && bsstab[i].bssid[5] == (unsigned char)id)
            return (int)i;
    return -1;
}

/* Dense housing: 40 neighbours on the low channels fill the table in one scan before the owner's
 * access point on channel 13 is heard. The profile's SSID (and the one the SCM's SCAN named) gets
 * in anyway, in the weakest neighbour's slot; a stranger still does not, and no stranger takes a
 * wanted entry's slot. */
static void wanted_cases(void)
{
    unsigned char buf[0x14 + 128];
    unsigned len;
    nbss = 0, scan_gen = 20;
    bss_want(0, (const unsigned char *)"Home", 4);
    for (unsigned i = 0; i < NBSS; i++) {
        len = beacon_from(buf, 200 + i, "neighbour", 9);
        assert(bss_add(buf + 0x14, len, 1, (signed char)(-40 - (i == 7 ? 50 : i % 20))));
    }
    assert(nbss == NBSS);
    len = beacon_from(buf, 300, "stranger", 8);
    assert(!bss_add(buf + 0x14, len, 1, -30) && in_table(300) < 0);
    len = beacon_from(buf, 999, "Home", 4);
    assert(bss_add(buf + 0x14, len, 13, -75) && nbss == NBSS);
    assert(in_table(999) >= 0 && in_table(207) < 0);           /* the weakest neighbour (-90 dBm) went */
    len = beacon_from(buf, 998, "Home", 4);                     /* a second access point of it */
    assert(bss_add(buf + 0x14, len, 13, -80) && in_table(998) >= 0 && in_table(999) >= 0);
    /* a later scan: the old entries may go, but never to a stranger at the cost of "Home" */
    scan_gen = 21;
    for (unsigned i = 0; i < NBSS; i++) {
        len = beacon_from(buf, 400 + i, "other", 5);
        bss_add(buf + 0x14, len, 1, -50);
    }
    assert(in_table(999) >= 0 && in_table(998) >= 0 && nbss == NBSS);
    /* the directed scan's SSID counts too; FORGET and a SCAN without one clear them */
    bss_want(1, (const unsigned char *)"Hid", 3);
    len = beacon_from(buf, 500, "Hid", 3);
    assert(bss_add(buf + 0x14, len, 6, -70) && in_table(500) >= 0);
    bss_want(1, 0, 0), bss_want(0, 0, 0);
    assert(!bss_wanted((const unsigned char *)"Home", 4) && !bss_wanted((const unsigned char *)"Hid", 3));
    nbss = 0;
}

int main(void)
{
    unsigned char buf[0x14 + 128];
    unsigned len = beacon(buf, 1, -67);
    assert(bss_add(buf + 0x14, len, 1, (signed char)buf[0x14 + RXRSSI]) == 1);
    assert(nbss == 1 && bsstab[0].rssi == -67 && bsstab[0].chan == 6);
    len = beacon(buf, 1, -71);
    bss_add(buf + 0x14, len, 1, (signed char)buf[0x14 + RXRSSI]);
    assert(nbss == 1 && bsstab[0].rssi == -71); /* the last beacon's level */
    len = beacon(buf, 1, 0);
    bss_add(buf + 0x14, len, 1, 0);
    assert(bsstab[0].rssi == -71); /* no level in this descriptor: keep the one we had */

    /* a hidden network: its beacons leave the SSID out (empty, or NUL bytes); the response to a
     * directed probe names it, and later beacons must not take the name away again */
    static const unsigned char empty[] = {0, 0, 3, 1, 11}, nuls[] = {0, 3, 0, 0, 0, 3, 1, 11};
    static const unsigned char named[] = {0, 3, 'L', 'a', 'b', 3, 1, 11};
    len = frame(buf, 0x80, 2, -60, empty, sizeof empty);
    assert(bss_add(buf + 0x14, len, 11, -60) == 1 && nbss == 2 && bsstab[1].ssidlen == 0);
    len = frame(buf, 0x50, 2, -58, named, sizeof named); /* probe response */
    bss_add(buf + 0x14, len, 11, -58);
    assert(bsstab[1].ssidlen == 3 && !memcmp(bsstab[1].ssid, "Lab", 3) && bsstab[1].rssi == -58);
    len = frame(buf, 0x80, 2, -61, empty, sizeof empty);
    bss_add(buf + 0x14, len, 11, -61);
    assert(bsstab[1].ssidlen == 3 && !memcmp(bsstab[1].ssid, "Lab", 3) && bsstab[1].rssi == -61);
    len = frame(buf, 0x80, 2, -62, nuls, sizeof nuls);
    bss_add(buf + 0x14, len, 11, -62);
    assert(bsstab[1].ssidlen == 3 && !memcmp(bsstab[1].ssid, "Lab", 3) && bsstab[1].seen == 4);
    /* a network that is renamed still takes its new name */
    len = beacon(buf, 2, -62);
    bss_add(buf + 0x14, len, 6, -62);
    assert(bsstab[1].ssidlen == 4 && !memcmp(bsstab[1].ssid, "Home", 4));

    /* a slot freed by ageing starts empty: no SSID, level or count of the BSS it held */
    nbss = 1;
    len = frame(buf, 0x80, 3, 0, empty, sizeof empty);
    assert(bss_add(buf + 0x14, len, 11, 0) == 1 && nbss == 2);
    assert(bsstab[1].ssidlen == 0 && bsstab[1].rssi == 0 && bsstab[1].seen == 1);
    rsn_cases();
    table_cases();
    wanted_cases();
    puts("wscan ok");
    return 0;
}
