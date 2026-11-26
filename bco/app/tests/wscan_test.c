/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Host unit test for the scan table (wscan.h): a beacon's RSSI from its RX descriptor.
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
    puts("wscan ok");
    return 0;
}
