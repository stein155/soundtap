/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Fuzz the app's parsers of what the air delivers (run-all.sh builds this one under ASan and
 * UBSan): the scan table's beacon/probe-response IE walk (wscan.h bss_add), the AP rate IEs of the
 * station frames (wstation.h), the GTK KDE walk (wpa_extract_gtk) and the 4-way supplicant on
 * EAPOL-Key frames (wpa_sm_rx), the last both on raw mutated frames and on Message 3 frames whose
 * MIC is recomputed under the real KCK after each mutation, so the fuzz gets past the MIC into the
 * key-data checks. Every input sits in a heap block of exactly its length, so any read past it is
 * caught. The seed is fixed: a failure repeats. Vectors are synthetic; no captures. */
#include <stdlib.h>

#include "../src/wpa.c"
#include "../src/wscan.h"
#include "../src/wstation.h"

#include <stdio.h>
#include <string.h>

static int fails;
#define CHECK(c, ...)                                                                          \
    do {                                                                                       \
        if (!(c)) {                                                                            \
            fails++;                                                                           \
            printf("FAIL %s:%d: ", __FILE__, __LINE__);                                        \
            printf(__VA_ARGS__);                                                               \
            putchar('\n');                                                                     \
        }                                                                                      \
    } while (0)

static uint32_t seed = 0xa1f2e3d4u;
static uint32_t rnd(void)
{
    seed ^= seed << 13, seed ^= seed >> 17, seed ^= seed << 5;
    return seed;
}

static void mutate(uint8_t *b, unsigned len, unsigned flips)
{
    while (len && flips--) {
        unsigned i = rnd() % len;
        switch (rnd() % 4) {
        case 0:
            b[i] = rnd();
            break;
        case 1:
            b[i] = rnd() % 2 ? 0xff : 0;
            break;
        default:
            b[i] ^= 1u << (rnd() % 8);
        }
    }
}

/* A copy in a heap block of exactly len bytes. */
static uint8_t *exact(const uint8_t *src, unsigned len)
{
    uint8_t *p = malloc(len ? len : 1);
    if (len)
        memcpy(p, src, len);
    return p;
}

/* A beacon as bss_add sees it: header, 8 bytes of descriptor room to RXBODY, fixed fields, IEs. */
static unsigned beacon(uint8_t *h)
{
    unsigned n = RXBODY + 12;
    memset(h, 0, n);
    h[0] = 0x80;
    for (unsigned i = 0; i < 6; i++)
        h[16 + i] = i == 5 ? rnd() % 40 : 0x02;
    h[n++] = 0, h[n++] = rnd() % 34;
    for (unsigned i = 0; i < h[n - 1]; i++)
        h[n++] = 'a' + rnd() % 26;
    h[n++] = 1, h[n++] = 8;
    for (unsigned i = 0; i < 8; i++)
        h[n++] = 0x82 + i;
    h[n++] = 3, h[n++] = 1, h[n++] = 1 + rnd() % 13;
    h[n++] = 48, h[n++] = 20;
    for (unsigned i = 0; i < 20; i++)
        h[n++] = rnd();
    h[n++] = 221, h[n++] = 8, h[n++] = 0, h[n++] = 0x50, h[n++] = 0xf2, h[n++] = 1;
    for (unsigned i = 0; i < 4; i++)
        h[n++] = rnd();
    return n;
}

static void fuzz_scan(void)
{
    uint8_t f[600];
    unsigned added = 0;
    for (unsigned it = 0; it < 20000; it++) {
        unsigned len = beacon(f);
        mutate(f + RXBODY + 12, len - RXBODY - 12, rnd() % 8);
        if (rnd() % 8 == 0)
            len = rnd() % sizeof f;
        else if (rnd() % 4 == 0)
            len -= rnd() % (len - RXBODY - 12 + 1);
        uint8_t *h = exact(f, len);
        if (bss_add(h, len, 1 + rnd() % 13, -(signed char)(rnd() % 90)))
            added++;
        free(h);
        CHECK(nbss <= NBSS, "scan table %u", nbss);
        for (unsigned i = 0; i < nbss; i++)
            CHECK(bsstab[i].ssidlen <= 32 && bsstab[i].ielen <= 255, "entry %u", i);
        if (fails)
            return;
        if (rnd() % 500 == 0)
            nbss = 0;
    }
    CHECK(added > 1000, "bss_add accepts most frames (%u)", added);
}

static void fuzz_rates(void)
{
    uint8_t ies[300], chain[8];
    for (unsigned it = 0; it < 20000; it++) {
        unsigned n = 0, len;
        while (n < 200) {
            unsigned l = rnd() % 12, id = rnd() % 3 ? (rnd() % 2 ? 1 : 50) : rnd() % 256;
            ies[n++] = id, ies[n++] = l;
            for (unsigned i = 0; i < l; i++)
                ies[n++] = rnd();
            if (rnd() % 4 == 0)
                break;
        }
        mutate(ies, n, rnd() % 4);
        len = rnd() % 8 ? n : rnd() % (n + 1);
        uint8_t *p = exact(ies, len);
        int one = wstation_has_1mbps(p, len);
        int any = wstation_rate_chain(p, len, chain);
        if (any)
            for (unsigned i = 1; i < 8; i++)
                CHECK(chain[i] <= chain[i - 1] && chain[i] && chain[i] < 0x80, "rate chain order");
        CHECK(one == 0 || one == 1, "has_1mbps");
        free(p);
        if (fails)
            return;
    }
}

static void fuzz_gtk(void)
{
    uint8_t kd[200], gtk[32];
    unsigned found = 0;
    for (unsigned it = 0; it < 20000; it++) {
        unsigned n = 0, len;
        if (rnd() % 2)
            memcpy(kd, rsn_ie, sizeof rsn_ie), n = sizeof rsn_ie;
        unsigned l = 6 + rnd() % 40;
        kd[n++] = 0xdd, kd[n++] = l;
        kd[n++] = 0, kd[n++] = 0x0f, kd[n++] = 0xac, kd[n++] = 1;
        for (unsigned i = 4; i < l; i++)
            kd[n++] = rnd();
        mutate(kd, n, rnd() % 4);
        len = rnd() % 8 ? n : rnd() % (n + 1);
        uint8_t *p = exact(kd, len);
        unsigned gl = 99;
        int gi = -1, gt = -1;
        if (wpa_extract_gtk(p, len, gtk, &gl, &gi, &gt)) {
            found++;
            CHECK(gl <= 32 && gi >= 0 && gi <= 3 && (gt == 0 || gt == 1), "gtk fields");
        }
        free(p);
        if (fails)
            return;
    }
    CHECK(found > 1000, "GTK KDE found in most frames (%u)", found);
}

/* EAPOL-Key frames: Message 1 into a fresh supplicant, then Message 3 shaped frames MICed under
 * its KCK, mutated before the MIC; plus raw mutations of either. */
static void fuzz_eapol(void)
{
    static const uint8_t aa[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
                         spa[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0xee};
    uint8_t pmk[32], snonce[32], m[300], out[WPA_EAPOL_MAX + 64];
    unsigned past_mic = 0;
    for (unsigned i = 0; i < 32; i++)
        pmk[i] = i, snonce[i] = 0x80 + i;
    for (unsigned it = 0; it < 6000; it++) {
        struct wpa_sm sm;
        size_t olen = 0;
        int r;
        wpa_sm_init(&sm, pmk, aa, spa, snonce);
        memset(m, 0, O_KD);
        m[0] = 2, m[1] = 3, m[3] = 95, m[4] = 2;
        m[O_KEYINFO + 1] = 0x8a, m[O_KEYLEN + 1] = 16, m[O_REPLAY + 7] = 1;
        for (unsigned i = 0; i < 32; i++)
            m[O_NONCE + i] = rnd();
        if (rnd() % 4 == 0) { /* a raw mutated Message 1 */
            unsigned len = O_KD + rnd() % 64;
            mutate(m, len, 1 + rnd() % 6);
            uint8_t *p = exact(m, len);
            r = wpa_sm_rx(&sm, p, len, out, &olen);
            free(p);
            CHECK(!(r & WPA_RX_SEND) || olen <= WPA_EAPOL_MAX, "reply fits (%zu)", olen);
            continue;
        }
        r = wpa_sm_rx(&sm, m, O_KD, out, &olen);
        CHECK(r == WPA_RX_SEND && olen <= WPA_EAPOL_MAX, "Message 1 answered");
        /* Message 3: install|ack|mic|secure|encrypted, key data of 16..160 bytes */
        unsigned kdlen = rnd() % 4 ? 8 * (2 + rnd() % 18) : rnd() % 200;
        unsigned len = O_KD + kdlen;
        if (len > sizeof m)
            len = sizeof m, kdlen = len - O_KD;
        m[O_KEYINFO] = 0x13, m[O_KEYINFO + 1] = 0xca;
        m[O_REPLAY + 7] = 2;
        m[O_KDLEN] = kdlen >> 8, m[O_KDLEN + 1] = kdlen;
        for (unsigned i = 0; i < kdlen; i++)
            m[O_KD + i] = rnd();
        mutate(m, len, rnd() % 3);
        int deep = rnd() % 8 != 0;
        if (deep)
            memcpy(m + O_NONCE, sm.anonce, 32);
        if (deep && rnd() % 8) { /* a MIC that verifies, so the key data is looked at */
            uint8_t mic[20];
            memset(m + O_MIC, 0, 16);
            wpa_hmac_sha1(sm.tptk, 16, m, len, mic);
            memcpy(m + O_MIC, mic, 16);
        }
        else
            deep = 0;
        if (rnd() % 16 == 0)
            len = rnd() % (len + 1), deep = 0;
        uint8_t *p = exact(m, len);
        r = wpa_sm_rx(&sm, p, len, out, &olen);
        free(p);
        past_mic += deep && r == WPA_RX_FAIL;
        CHECK(!(r & WPA_RX_INSTALL_PTK), "no PTK from key data that was never wrapped");
        CHECK(sm.ptk[0] == 0 && sm.ptk[47] == 0, "keys in use untouched");
        if (fails)
            return;
    }
    CHECK(past_mic > 1000, "Message 3 frames reach the key-data checks (%u)", past_mic);
}

int main(void)
{
    fuzz_scan();
    fuzz_rates();
    fuzz_gtk();
    fuzz_eapol();
    printf("app air fuzz: %s\n", fails ? "FAILED" : "ok");
    return fails != 0;
}
