/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include <assert.h>
#include <string.h>
#include "../src/wstation.h"

int main(void)
{
    const uint8_t own[6] = {2, 1, 2, 3, 4, 5}, ap[6] = {2, 6, 7, 8, 9, 10};
    const uint8_t rates[8] = {2, 2, 2, 2, 2, 2, 2, 2};
    uint8_t buf[WSTATION_AUTH_SIZE + 1];
    memset(buf, 0xa5, sizeof buf);
    assert(wstation_auth(buf, sizeof buf - 1, own, ap, rates));
    assert(buf[sizeof buf - 1] == 0xa5);
    assert(buf[0] == 0x0a && buf[3] == 0x74 && buf[8 + 0x18] == 0xb0);
    assert(!memcmp(buf + 8 + 0x1c, ap, 6));
    assert(!memcmp(buf + 8 + 0x22, own, 6));
    assert(!memcmp(buf + 8 + 0x28, ap, 6));
    assert(!memcmp(buf + 0x94, buf + 8, 0x38));
    const uint8_t body[6] = {0, 0, 1, 0, 0, 0};
    assert(!memcmp(buf + 8 + 0xc4, body, 6));
    assert(!wstation_auth(buf, WSTATION_AUTH_SIZE - 1, own, ap, rates));
    assert(!wstation_auth(buf, sizeof buf, own, own, rates));
    const uint8_t multicast[6] = {1}, zero[6] = {0};
    assert(!wstation_auth(buf, sizeof buf, own, multicast, rates));
    assert(!wstation_auth(buf, sizeof buf, zero, ap, rates));
    const uint8_t ies[] = {1, 2, 0x82, 12, 50, 1, 24};
    assert(wstation_has_1mbps(ies, sizeof ies));
    assert(!wstation_has_1mbps(ies, sizeof ies - 1));
    assert(!wstation_has_1mbps(ies + 4, 3));
    assert(!wstation_has_1mbps(0, 1));

    /* association request */
    uint8_t ab[8 + 0xc4 + 76];
    const char *ss = "HomeNet";
    unsigned sl = 7;
    memset(ab, 0xa5, sizeof ab);
    int blen = wstation_assoc(ab, sizeof ab, own, ap, rates, ss, sl);
    assert(blen == (int)(4 + (2 + sl) + 10 + 6 + 22));
    const uint8_t *d = ab + 8, *p = d + 0xc4;
    assert(d[0x18] == 0x00);                        /* FC: assoc request */
    assert(!memcmp(d + 0x1c, ap, 6) && !memcmp(d + 0x22, own, 6) && !memcmp(d + 0x28, ap, 6));
    assert(p[0] == 0x11 && p[1] == 0 && p[2] == 1 && p[3] == 0); /* cap + listen */
    assert(p[4] == 0 && p[5] == sl && !memcmp(p + 6, ss, sl));   /* SSID IE */
    assert(p[6 + sl] == 1 && p[7 + sl] == 8);                    /* supported rates IE */
    assert(p[6 + sl + 10] == 50 && p[7 + sl + 10] == 4);         /* ext rates IE */
    assert(p[6 + sl + 16] == 0x30 && p[7 + sl + 16] == 0x14);    /* RSN IE */
    assert(!memcmp(ab + 0x94, ab + 8, 0x38));                    /* header save */
    /* every legal SSID length fits, 21..32 bytes included */
    const char *ss32 = "0123456789abcdefghijklmnopqrstuv";
    assert(wstation_assoc(ab, sizeof ab, own, ap, rates, ss32, 20) == 4 + 22 + 10 + 6 + 22);
    assert(wstation_assoc(ab, sizeof ab, own, ap, rates, ss32, 21) == 4 + 23 + 10 + 6 + 22);
    assert(wstation_assoc(ab, sizeof ab, own, ap, rates, ss32, 32) == 76);
    assert(!wstation_assoc(ab, sizeof ab, own, ap, rates, ss, 33));   /* ssid too long */
    assert(!wstation_assoc(ab, 8 + 0xc4 + 10, own, ap, rates, ss, sl)); /* buffer too small */
    assert(!wstation_assoc(ab, sizeof ab, own, own, rates, ss, sl));  /* own == bssid */

    /* data frames: stock encapsulation layout */
    uint8_t db[8 + 0xc4 + 64];
    const uint8_t bc[6] = {0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, ip[4] = {0x45, 0, 0, 20};
    memset(db, 0xa5, sizeof db);
    assert(wstation_data(db, sizeof db, own, ap, bc, rates, 0x0800, ip, 4) == 12);
    d = db + 8, p = d + 0xc4;
    assert(d[0] == 0x18 && d[1] == 0x18 && d[2] == 0x32 && d[3] == 0xc4);
    assert(d[0x18] == 0x08 && d[0x19] == 0x41);                  /* data, toDS | Protected */
    assert(d[0x12] == 0 && d[0x13] == 0 && d[0x14] == 4);
    assert(!memcmp(d + 0x1c, ap, 6) && !memcmp(d + 0x22, own, 6) && !memcmp(d + 0x28, bc, 6));
    assert(p[0] == 0xaa && p[2] == 3 && p[6] == 0x08 && p[7] == 0x00 && !memcmp(p + 8, ip, 4));
    assert(!memcmp(db + 0x94, db + 8, 0x38));
    assert(wstation_eapol(db, sizeof db, own, ap, rates, ip, 4) == 12);
    assert(d[0x19] == 0x01 && d[0x12] == 1 && !memcmp(d + 0x28, ap, 6)); /* EAPOL: clear */
    assert(!wstation_data(db, sizeof db, own, ap, bc, rates, 0x0800, ip, 64)); /* too small */
    return 0;
}
