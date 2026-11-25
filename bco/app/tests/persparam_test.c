/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Host self-test for the PersParam lookup (persparam.h), on a synthetic sector. */
#include "../src/persparam.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>

static uint8_t sec[0x200];
static unsigned at = 0x20;

static void put32(uint8_t *p, uint32_t v) { p[0] = v, p[1] = v >> 8, p[2] = v >> 16, p[3] = v >> 24; }

static void rec(uint32_t key, const uint8_t *val, unsigned vl)
{
    unsigned rl = 16 + 4 + vl + 4;
    put32(sec + at, rl), put32(sec + at + 4, vl), put32(sec + at + 8, 0), put32(sec + at + 12, 4);
    put32(sec + at + 16, key);
    memcpy(sec + at + 20, val, vl);
    put32(sec + at + 20 + vl, 0);
    at += rl;
}

int main(void)
{
    uint8_t mac[6];
    const uint8_t z[8] = {0}, m[8] = {0x00, 0x0c, 0x8a, 1, 2, 3, 0, 0}, x[4] = {1, 2, 3, 4};
    memset(sec, 0xff, sizeof sec);
    put32(sec, 0x80808080);
    memcpy(sec + 0x10, "PersParam", 9);
    assert(!pp_wifi_mac(sec, sizeof sec, mac));          /* empty log */
    rec(PP_KEY('M', 'A', 'C', '0'), z, 8);
    assert(!memcmp(sec + 0x30, "0CAM", 4));              /* key byte order as on the stock NOR */
    assert(!pp_wifi_mac(sec, sizeof sec, mac));          /* zero MAC is unusable */
    rec(PP_KEY('S', 'A', 'F', 'E'), x, 4);
    rec(PP_KEY('M', 'A', 'C', '0'), m, 8);
    rec(PP_KEY('M', 'A', 'C', '1'), z, 8);
    assert(pp_wifi_mac(sec, sizeof sec, mac) && !memcmp(mac, m, 6)); /* newest record wins */
    uint8_t v[4];
    assert(pp_get(sec, sizeof sec, PP_KEY('S', 'A', 'F', 'E'), v, 4) == 4 && !memcmp(v, x, 4));
    assert(!pp_get(sec, sizeof sec, PP_KEY('N', 'O', 'N', 'E'), v, 4));
    sec[0x20] = 3;                                         /* corrupt first length */
    assert(!pp_wifi_mac(sec, sizeof sec, mac));
    sec[0x20] = 28;
    sec[0x10] = 'X';                                       /* not a PersParam sector */
    assert(!pp_wifi_mac(sec, sizeof sec, mac));
    puts("persparam ok");
    return 0;
}
