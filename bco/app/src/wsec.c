/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* WEP and TKIP frame protection in software (ADR 0136) -- see wsec.h. Pure, freestanding, no libc.
 * The names stay apart from wpa.c's: the host tests build both into one unit. */
#include "wsec.h"
#include "wpa.h"

/* ============================ RC4 ============================ */
void wsec_rc4(const uint8_t *key, unsigned klen, uint8_t *buf, unsigned n)
{
    wpa_rc4_skip(key, klen, 0, buf, n);
}

/* ============================ CRC-32 ============================ */
/* The table is built on first use (1 KiB), as wpa.c builds its inverse S-box: a frame's ICV is
 * then a table lookup per byte instead of eight shifts. */
static uint32_t ws_crc_tab[256];
static int ws_crc_ready;

uint32_t wsec_crc32(const uint8_t *p, unsigned n)
{
    uint32_t c = 0xffffffffu;
    if (!ws_crc_ready) {
        for (unsigned i = 0; i < 256; i++) {
            uint32_t v = i;
            for (unsigned k = 0; k < 8; k++)
                v = v >> 1 ^ (0xedb88320u & (0u - (v & 1)));
            ws_crc_tab[i] = v;
        }
        ws_crc_ready = 1;
    }
    while (n--)
        c = c >> 8 ^ ws_crc_tab[(c ^ *p++) & 0xff];
    return ~c;
}

static void ws_put32(uint8_t *p, uint32_t v)
{
    p[0] = (uint8_t)v, p[1] = (uint8_t)(v >> 8), p[2] = (uint8_t)(v >> 16), p[3] = (uint8_t)(v >> 24);
}

static uint32_t ws_get32(const uint8_t *p)
{
    return p[0] | (uint32_t)p[1] << 8 | (uint32_t)p[2] << 16 | (uint32_t)p[3] << 24;
}

/* ============================ Michael ============================ */
static uint32_t ws_rol(uint32_t v, unsigned s) { return v << s | v >> (32 - s); }

struct ws_mic {
    uint32_t l, r, m;
    unsigned nm; /* bytes gathered in m */
};

static void ws_mic_byte(struct ws_mic *c, uint8_t b)
{
    c->m |= (uint32_t)b << 8 * c->nm;
    if (++c->nm < 4)
        return;
    c->l ^= c->m;
    c->r ^= ws_rol(c->l, 17), c->l += c->r;
    c->r ^= (c->l & 0xff00ff00u) >> 8 | (c->l & 0x00ff00ffu) << 8, c->l += c->r;
    c->r ^= ws_rol(c->l, 3), c->l += c->r;
    c->r ^= ws_rol(c->l, 30), c->l += c->r;
    c->m = 0, c->nm = 0;
}

static void ws_mic_end(struct ws_mic *c, uint8_t mic[8])
{
    /* padding: 0x5a, then four to seven zero bytes up to a whole word */
    ws_mic_byte(c, 0x5a);
    for (unsigned i = 0; i < 4; i++)
        ws_mic_byte(c, 0);
    while (c->nm)
        ws_mic_byte(c, 0);
    ws_put32(mic, c->l);
    ws_put32(mic + 4, c->r);
}

void wsec_michael_raw(const uint8_t key[8], const uint8_t *msg, unsigned n, uint8_t mic[8])
{
    struct ws_mic c = {ws_get32(key), ws_get32(key + 4), 0, 0};
    for (unsigned i = 0; i < n; i++)
        ws_mic_byte(&c, msg[i]);
    ws_mic_end(&c, mic);
}

void wsec_michael(const uint8_t key[8], const uint8_t da[6], const uint8_t sa[6], unsigned prio,
                  const uint8_t *msdu, unsigned n, uint8_t mic[8])
{
    struct ws_mic c = {ws_get32(key), ws_get32(key + 4), 0, 0};
    unsigned i;
    for (i = 0; i < 6; i++)
        ws_mic_byte(&c, da[i]);
    for (i = 0; i < 6; i++)
        ws_mic_byte(&c, sa[i]);
    ws_mic_byte(&c, (uint8_t)prio);
    for (i = 0; i < 3; i++)
        ws_mic_byte(&c, 0);
    for (i = 0; i < n; i++)
        ws_mic_byte(&c, msdu[i]);
    ws_mic_end(&c, mic);
}

/* ============================ TKIP key mixing ============================ */
/* TKIP's S-box is AES's: entry i is 2*S[i] in the high byte and 3*S[i] in the low one (GF(2^8)),
 * so it is derived from wpa.c's table instead of stored again. */
static uint16_t ws_sbox[256];
static int ws_sbox_ready;

static uint16_t ws_s(uint16_t v)
{
    uint16_t hi = ws_sbox[v >> 8];
    return (uint16_t)(ws_sbox[v & 0xff] ^ (uint16_t)(hi << 8 | hi >> 8));
}

static uint16_t ws_le16(const uint8_t *p) { return (uint16_t)(p[0] | p[1] << 8); }

static uint16_t ws_ror1(uint16_t v) { return (uint16_t)(v >> 1 | v << 15); }

void wsec_tkip_mix(const uint8_t tk[16], const uint8_t ta[6], uint64_t tsc, uint8_t rc4key[16])
{
    uint32_t iv32 = (uint32_t)(tsc >> 16);
    uint16_t iv16 = (uint16_t)tsc, k[6];
    unsigned i;
    if (!ws_sbox_ready) {
        for (i = 0; i < 256; i++) {
            uint8_t s = wpa_aes_sbox(i), s2 = (uint8_t)(s << 1 ^ (s >> 7) * 0x1b);
            ws_sbox[i] = (uint16_t)(s2 << 8 | (uint8_t)(s2 ^ s));
        }
        ws_sbox_ready = 1;
    }
    /* phase 1: TA and TSC2-5 into the 80-bit TTAK */
    k[0] = (uint16_t)iv32, k[1] = (uint16_t)(iv32 >> 16);
    k[2] = ws_le16(ta), k[3] = ws_le16(ta + 2), k[4] = ws_le16(ta + 4);
    for (i = 0; i < 8; i++) {
        unsigned j = 2 * (i & 1);
        k[0] = (uint16_t)(k[0] + ws_s(k[4] ^ ws_le16(tk + j)));
        k[1] = (uint16_t)(k[1] + ws_s(k[0] ^ ws_le16(tk + 4 + j)));
        k[2] = (uint16_t)(k[2] + ws_s(k[1] ^ ws_le16(tk + 8 + j)));
        k[3] = (uint16_t)(k[3] + ws_s(k[2] ^ ws_le16(tk + 12 + j)));
        k[4] = (uint16_t)(k[4] + ws_s(k[3] ^ ws_le16(tk + j)) + i);
    }
    /* phase 2: TSC0-1 into the 96-bit PPK */
    k[5] = (uint16_t)(k[4] + iv16);
    for (i = 0; i < 6; i++)
        k[i] = (uint16_t)(k[i] + ws_s(k[(i + 5) % 6] ^ ws_le16(tk + 2 * i)));
    k[0] = (uint16_t)(k[0] + ws_ror1(k[5] ^ ws_le16(tk + 12)));
    k[1] = (uint16_t)(k[1] + ws_ror1(k[0] ^ ws_le16(tk + 14)));
    for (i = 2; i < 6; i++)
        k[i] = (uint16_t)(k[i] + ws_ror1(k[i - 1]));
    rc4key[0] = (uint8_t)(iv16 >> 8);
    rc4key[1] = (uint8_t)(((iv16 >> 8) | 0x20) & 0x7f); /* WEPSeed[1]: keeps out the weak keys */
    rc4key[2] = (uint8_t)iv16;
    rc4key[3] = (uint8_t)((k[5] ^ ws_le16(tk)) >> 1);
    for (i = 0; i < 6; i++)
        rc4key[4 + 2 * i] = (uint8_t)k[i], rc4key[5 + 2 * i] = (uint8_t)(k[i] >> 8);
}

/* ============================ WEP ============================ */
unsigned wsec_wep_protect(const uint8_t *key, unsigned klen, unsigned keyid, uint32_t iv, uint8_t *f,
                          unsigned n)
{
    uint8_t rc4seed[3 + 13];
    unsigned i;
    f[0] = (uint8_t)iv, f[1] = (uint8_t)(iv >> 8), f[2] = (uint8_t)(iv >> 16);
    f[3] = (uint8_t)((keyid & 3) << 6);
    ws_put32(f + WSEC_WEP_HDR + n, wsec_crc32(f + WSEC_WEP_HDR, n));
    for (i = 0; i < 3; i++)
        rc4seed[i] = f[i];
    for (i = 0; i < klen && i < 13; i++)
        rc4seed[3 + i] = key[i];
    wsec_rc4(rc4seed, 3 + i, f + WSEC_WEP_HDR, n + WSEC_ICV);
    return n + WSEC_WEP_OVERHEAD;
}

int wsec_wep_open(const uint8_t *key, unsigned klen, uint8_t *f, unsigned n)
{
    uint8_t rc4seed[3 + 13];
    unsigned i, d;
    if (n < WSEC_WEP_OVERHEAD || (f[3] & 0x20) || !klen || klen > 13)
        return WSEC_BAD;
    d = n - WSEC_WEP_OVERHEAD;
    for (i = 0; i < 3; i++)
        rc4seed[i] = f[i];
    for (i = 0; i < klen; i++)
        rc4seed[3 + i] = key[i];
    wsec_rc4(rc4seed, 3 + klen, f + WSEC_WEP_HDR, d + WSEC_ICV);
    return wsec_crc32(f + WSEC_WEP_HDR, d) == ws_get32(f + WSEC_WEP_HDR + d) ? (int)d : WSEC_ICV_FAIL;
}

/* ============================ TKIP ============================ */
unsigned wsec_keyid(const uint8_t *f)
{
    return f[3] >> 6;
}

int wsec_extiv(const uint8_t *f)
{
    return (f[3] & 0x20) != 0;
}

uint64_t wsec_tkip_tsc(const uint8_t *f)
{
    return f[2] | (uint64_t)f[0] << 8 | (uint64_t)ws_get32(f + 4) << 16;
}

unsigned wsec_tkip_protect(const uint8_t tk[16], const uint8_t mic_key[8], unsigned keyid, uint64_t tsc,
                           const uint8_t ta[6], const uint8_t da[6], const uint8_t sa[6], uint8_t *f,
                           unsigned n)
{
    uint8_t key[16];
    uint8_t *d = f + WSEC_TKIP_HDR;
    wsec_michael(mic_key, da, sa, 0, d, n, d + n);
    ws_put32(d + n + WSEC_MIC, wsec_crc32(d, n + WSEC_MIC));
    wsec_tkip_mix(tk, ta, tsc, key);
    f[0] = key[0], f[1] = key[1], f[2] = key[2];
    f[3] = (uint8_t)((keyid & 3) << 6 | 0x20);
    ws_put32(f + 4, (uint32_t)(tsc >> 16));
    wsec_rc4(key, sizeof key, d, n + WSEC_MIC + WSEC_ICV);
    return n + WSEC_TKIP_OVERHEAD;
}

int wsec_tkip_open(const uint8_t tk[16], const uint8_t mic_key[8], const uint8_t ta[6], const uint8_t da[6],
                   const uint8_t sa[6], uint8_t *f, unsigned n)
{
    uint8_t key[16], mic[WSEC_MIC];
    uint8_t *d = f + WSEC_TKIP_HDR;
    unsigned m, diff = 0;
    if (n < WSEC_TKIP_OVERHEAD || !(f[3] & 0x20))
        return WSEC_BAD;
    m = n - WSEC_TKIP_OVERHEAD;
    wsec_tkip_mix(tk, ta, wsec_tkip_tsc(f), key);
    wsec_rc4(key, sizeof key, d, m + WSEC_MIC + WSEC_ICV);
    if (wsec_crc32(d, m + WSEC_MIC) != ws_get32(d + m + WSEC_MIC))
        return WSEC_ICV_FAIL;
    wsec_michael(mic_key, da, sa, 0, d, m, mic);
    for (unsigned i = 0; i < WSEC_MIC; i++)
        diff |= mic[i] ^ d[m + i];
    return diff ? WSEC_MIC_FAIL : (int)m;
}
