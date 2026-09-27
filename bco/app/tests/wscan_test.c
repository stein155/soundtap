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

/* One beacon as the core hands it over: descriptor bytes up to the header, then header + body. */
static unsigned beacon(unsigned char *buf, unsigned char last, signed char rssi)
{
    static const unsigned char ssid[] = {0, 4, 'H', 'o', 'm', 'e', 3, 1, 6};
    unsigned char *h = buf + 0x14;
    memset(buf, 0, 0x14 + RXBODY + 12 + sizeof ssid);
    buf[0x14 + RXRSSI] = (unsigned char)rssi;
    h[0] = 0x80;
    memcpy(h + 16, "\x02\x00\x00\x00\x00", 5), h[21] = last;
    memcpy(h + RXBODY + 12, ssid, sizeof ssid);
    return RXBODY + 12 + sizeof ssid;
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
    puts("wscan ok");
    return 0;
}
