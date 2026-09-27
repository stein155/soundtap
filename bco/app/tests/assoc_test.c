/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Host unit test for the WiLD management-frame TX command encoding (ADR 0031).
 *
 * Exercises the real kind-1 record encoder (wring.h) against the bytes captured read-only from
 * the stock host's TX ring during a join (BSSID swapped for a dummy). wifi.c itself touches
 * hardware registers and cannot be compiled on the host, so the two command bodies and the
 * wr_cmd call are reproduced here exactly as wifi.c builds them; this test is what fails if that
 * encoding drifts from the captured stock traffic. No framework: assert + main.
 *
 *   cc -Wall -Werror -o assoc_test assoc_test.c && ./assoc_test
 */
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "../src/wring.h"
#include "../src/wstation.h"

/* --- mirror of wifi.c's command builders (keep in sync; this test guards the bytes) --- */
struct auth_cmd {
    uint16_t algo;
    uint8_t trans;
    uint8_t param;
    uint16_t status;
};
static const unsigned char assoc_tail[22] = {
    0x06, 0x0c, 0x82, 0x84, 0x8b, 0x0c, 0x12, 0x96, 0x18, 0x24, 0x30,
    0x48, 0x60, 0x6c, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x06, 0x0e,
};
#define WILD_ASSOC_CAP_DEFAULT 0x0064

static void build_assoc(unsigned char body[30], const uint8_t bssid[6], uint16_t cap)
{
    memcpy(body, bssid, 6);
    body[6] = cap, body[7] = cap >> 8;
    memcpy(body + 8, assoc_tail, sizeof assoc_tail);
}

/* --- a host-owned TX ring over a plain buffer (no core peer needed to emit) --- */
static unsigned buf[256];
static struct wring ring;

static void ring_reset(void)
{
    memset(buf, 0, sizeof buf);
    memset(&ring, 0, sizeof ring);
    ring.base = (uintptr_t)buf;
    ring.size = 256;
    ring.esize = 4;
    ring.nch = 5;
    ring.ch[0].shared = 0;
    ring.ch[0].lim = 200;
    ring.ch[0].pos = 0;
}

static void expect(const unsigned *want, unsigned n, const char *what)
{
    for (unsigned i = 0; i < n; i++)
        if (buf[i] != want[i]) {
            printf("%s word %u: got 0x%08x want 0x%08x\n", what, i, buf[i], want[i]);
            assert(0);
        }
}

int main(void)
{
    const uint8_t bssid[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55};

    /* Auth-request: class 0xde, module 0x13, flag 11, body {00 00 01 14 00 00}. */
    struct auth_cmd auth = {0, 1, 0x14, 0};
    const unsigned char want_auth_body[6] = {0x00, 0x00, 0x01, 0x14, 0x00, 0x00};
    assert(memcmp(&auth, want_auth_body, 6) == 0); /* struct layout matches captured bytes */
    static const unsigned want_auth[] = {
        1u | (6 + 8) << 16, 0xde, 0x13 | 11u << 16, /* kind-1 header */
        0x14010000, 0x00000000,                     /* body, little-endian */
    };
    ring_reset();
    wr_cmd(&ring, 0xde, 0x13, 11, &auth, sizeof auth);
    expect(want_auth, sizeof want_auth / sizeof want_auth[0], "auth");

    /* Assoc-request: class 0xc9, module 0x13, flag 11, body = BSSID + cap + rate tail. */
    unsigned char assoc[30];
    build_assoc(assoc, bssid, WILD_ASSOC_CAP_DEFAULT);
    static const unsigned char want_assoc_body[30] = {
        0x02, 0x11, 0x22, 0x33, 0x44, 0x55, 0x64, 0x00, 0x06, 0x0c, 0x82, 0x84, 0x8b, 0x0c, 0x12,
        0x96, 0x18, 0x24, 0x30, 0x48, 0x60, 0x6c, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x06, 0x0e,
    };
    assert(memcmp(assoc, want_assoc_body, 30) == 0);
    unsigned want_assoc[3 + 8];
    want_assoc[0] = 1u | (30 + 8) << 16;
    want_assoc[1] = 0xc9;
    want_assoc[2] = 0x13 | 11u << 16;
    memset(&want_assoc[3], 0, 8 * 4);
    memcpy(&want_assoc[3], want_assoc_body, 30);
    ring_reset();
    wr_cmd(&ring, 0xc9, 0x13, 11, assoc, sizeof assoc);
    expect(want_assoc, 3 + 8, "assoc");

    /* data rate chain: fastest first, eight slots, last repeated */
    {
        static const uint8_t ies[] = {0, 2, 'a', 'b', 1, 8, 0x82, 0x84, 0x8b, 0x96, 0x0c, 0x12, 0x18, 0x24,
                                      50, 4, 0x30, 0x48, 0x60, 0x6c};
        static const uint8_t want[8] = {0x6c, 0x60, 0x48, 0x30, 0x24, 0x18, 0x16, 0x12};
        static const uint8_t few[] = {1, 3, 0x84, 0x8b, 0x96};
        static const uint8_t want_few[8] = {0x16, 0x0b, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04};
        uint8_t got[8];
        assert(wstation_rate_chain(ies, sizeof ies, got) && !memcmp(got, want, 8));
        assert(wstation_rate_chain(few, sizeof few, got) && !memcmp(got, want_few, 8));
        assert(!wstation_rate_chain(ies, 4, got));
    }
    { /* AP fromDS data frame: a1 = DA, a2 = own, a3 = SA, SNAP before the payload */
        static const uint8_t own[6] = {0, 0x0c, 0x8a, 1, 2, 3}, sta[6] = {2, 0, 0, 0, 0, 7};
        static const uint8_t r[8] = {2, 2, 2, 2, 2, 2, 2, 2}, pl[3] = {9, 8, 7};
        static uint8_t f[8 + 0xc4 + 16];
        assert(wframe(f, sizeof f, 0x0208, sta, own, own, r, 0x0800, pl, 3) == 11);
        assert(f[8 + 2] == 0x32 && f[8 + 0x18] == 8 && f[8 + 0x19] == 2 && f[8 + 0x1c + 5] == 7 &&
               f[8 + 0x22 + 5] == 3 && f[8 + 0x28 + 5] == 3 && f[8 + 0xc4] == 0xaa &&
               f[8 + 0xc4 + 6] == 8 && f[8 + 0xc4 + 10] == 7 && !memcmp(f + 0x94, f + 8, 0x38));
        assert(wframe(f, sizeof f, 0x50, sta, own, own, r, 0, pl, 3) == 3 && f[8 + 2] == 0x30 &&
               f[8 + 0xc4] == 9);
        assert(!wframe(f, sizeof f, 0x50, sta, own, own, r, 0, pl, 20)); /* too big for buf */
    }
    printf("ok: auth + assoc command encoding matches captured stock bytes\n");
    return 0;
}
