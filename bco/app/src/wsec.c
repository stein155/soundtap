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

/* ============================ CCMP (software) ============================ */
/* The nonce and the AAD of IEEE 802.11-2020 12.5.3.3.3-4 for a frame without QoS or a fourth
 * address: FC with Retry, PwrMgt and MoreData cleared and Protected set (a data frame's subtype
 * bits 4-6 cleared too), A1-A3, and the sequence control with only the fragment number left. */
static void ws_ccmp_init(const uint8_t tk[16], const uint8_t hdr[24], uint64_t pn, unsigned n,
                         uint8_t a0[16], uint8_t mac[16])
{
    uint8_t aad[2 + 22 + 8], b[16];
    unsigned i, j;
    int mgmt = (hdr[0] & 0x0c) == 0;
    a0[0] = 0x01;
    a0[1] = (uint8_t)(mgmt ? 0x10 : 0); /* nonce flags: priority 0, Management */
    for (i = 0; i < 6; i++)
        a0[2 + i] = hdr[10 + i];
    for (i = 0; i < 6; i++)
        a0[8 + i] = (uint8_t)(pn >> (40 - 8 * i));
    a0[14] = a0[15] = 0;
    /* B0: flags (Adata, M = 8, L = 2), the nonce, the length */
    b[0] = 0x59;
    for (i = 1; i < 14; i++)
        b[i] = a0[i];
    b[14] = (uint8_t)(n >> 8), b[15] = (uint8_t)n;
    wpa_aes128_enc(tk, b, mac);
    aad[0] = 0, aad[1] = 22;
    aad[2] = (uint8_t)(hdr[0] & (mgmt ? 0xff : 0x8f));
    aad[3] = (uint8_t)((hdr[1] & ~0x38) | 0x40);
    for (i = 0; i < 18; i++)
        aad[4 + i] = hdr[4 + i];
    aad[22] = hdr[22] & 0x0f, aad[23] = 0;
    for (i = 24; i < sizeof aad; i++)
        aad[i] = 0;
    for (j = 0; j < sizeof aad; j += 16) {
        for (i = 0; i < 16; i++)
            mac[i] ^= aad[j + i];
        wpa_aes128_enc(tk, mac, mac);
    }
}

/* CBC-MAC over the payload, and CTR over payload and MIC: encrypt or decrypt in place. */
static void ws_ccmp_run(const uint8_t tk[16], uint8_t a0[16], uint8_t mac[16], uint8_t *d, unsigned n,
                        int enc, uint8_t tag[8])
{
    uint8_t s[16];
    unsigned i, k;
    for (k = 0; k * 16 < n; k++) {
        unsigned m = n - k * 16 < 16 ? n - k * 16 : 16;
        a0[14] = (uint8_t)((k + 1) >> 8), a0[15] = (uint8_t)(k + 1);
        wpa_aes128_enc(tk, a0, s);
        for (i = 0; i < m; i++) {
            if (enc)
                mac[i] ^= d[k * 16 + i], d[k * 16 + i] ^= s[i];
            else
                d[k * 16 + i] ^= s[i], mac[i] ^= d[k * 16 + i];
        }
        wpa_aes128_enc(tk, mac, mac);
    }
    a0[14] = a0[15] = 0;
    wpa_aes128_enc(tk, a0, s);
    for (i = 0; i < 8; i++)
        tag[i] = mac[i] ^ s[i];
}

unsigned wsec_ccmp_protect(const uint8_t tk[16], const uint8_t hdr[24], uint64_t pn, uint8_t *f, unsigned n)
{
    uint8_t a0[16], mac[16];
    f[0] = (uint8_t)pn, f[1] = (uint8_t)(pn >> 8), f[2] = 0, f[3] = 0x20;
    f[4] = (uint8_t)(pn >> 16), f[5] = (uint8_t)(pn >> 24), f[6] = (uint8_t)(pn >> 32), f[7] = (uint8_t)(pn >> 40);
    ws_ccmp_init(tk, hdr, pn, n, a0, mac);
    ws_ccmp_run(tk, a0, mac, f + WSEC_CCMP_HDR, n, 1, f + WSEC_CCMP_HDR + n);
    return n + WSEC_CCMP_HDR + WSEC_CCMP_MIC;
}

uint64_t wsec_ccmp_pn(const uint8_t *f)
{
    return f[0] | (uint64_t)f[1] << 8 | (uint64_t)ws_get32(f + 4) << 16;
}

int wsec_ccmp_open(const uint8_t tk[16], const uint8_t hdr[24], uint8_t *f, unsigned n)
{
    uint8_t a0[16], mac[16], tag[8];
    unsigned m, diff = 0;
    if (n < WSEC_CCMP_HDR + WSEC_CCMP_MIC || !(f[3] & 0x20))
        return WSEC_BAD;
    m = n - WSEC_CCMP_HDR - WSEC_CCMP_MIC;
    ws_ccmp_init(tk, hdr, wsec_ccmp_pn(f), m, a0, mac);
    ws_ccmp_run(tk, a0, mac, f + WSEC_CCMP_HDR, m, 0, tag);
    for (unsigned i = 0; i < 8; i++)
        diff |= tag[i] ^ f[WSEC_CCMP_HDR + m + i];
    return diff ? WSEC_MIC_FAIL : (int)m;
}

/* ============================ BIP-CMAC-128 ============================ */
/* MIC = AES-CMAC(IGTK, FC (Retry, PwrMgt, MoreData cleared) || A1 || A2 || A3 || body with the
 * MMIE's MIC zeroed)[0..7], the message gathered in one buffer for wpa_cmac. */
static int ws_bip_mic(const uint8_t igtk[16], const uint8_t hdr[24], const uint8_t *body, unsigned n,
                      uint8_t mic[8])
{
    uint8_t m[20 + 512], full[16];
    unsigned i;
    if (n > sizeof m - 20 || n < WSEC_MMIE_LEN)
        return 0;
    m[0] = hdr[0], m[1] = (uint8_t)(hdr[1] & ~0x38);
    for (i = 0; i < 18; i++)
        m[2 + i] = hdr[4 + i];
    for (i = 0; i < n; i++)
        m[20 + i] = i >= n - 8 ? 0 : body[i];
    wpa_cmac(igtk, m, 20 + n, full);
    for (i = 0; i < 8; i++)
        mic[i] = full[i];
    return 1;
}

unsigned wsec_bip_protect(const uint8_t igtk[16], unsigned keyid, uint64_t ipn, const uint8_t hdr[24],
                          uint8_t *body, unsigned n)
{
    uint8_t *e = body + n;
    unsigned i;
    e[0] = 76, e[1] = 16, e[2] = (uint8_t)keyid, e[3] = (uint8_t)(keyid >> 8);
    for (i = 0; i < 6; i++)
        e[4 + i] = (uint8_t)(ipn >> (8 * i));
    if (!ws_bip_mic(igtk, hdr, body, n + WSEC_MMIE_LEN, e + 10))
        return 0;
    return n + WSEC_MMIE_LEN;
}

int wsec_bip_check(const uint8_t igtk[16], const uint8_t hdr[24], const uint8_t *body, unsigned n,
                   unsigned *keyid, uint64_t *ipn)
{
    const uint8_t *e;
    uint8_t mic[8];
    unsigned i, diff = 0;
    if (n < WSEC_MMIE_LEN)
        return 0;
    e = body + n - WSEC_MMIE_LEN;
    if (e[0] != 76 || e[1] != 16 || !ws_bip_mic(igtk, hdr, body, n, mic))
        return 0;
    for (i = 0; i < 8; i++)
        diff |= mic[i] ^ e[10 + i];
    *keyid = e[2] | e[3] << 8;
    *ipn = 0;
    for (i = 0; i < 6; i++)
        *ipn |= (uint64_t)e[4 + i] << (8 * i);
    return !diff;
}
