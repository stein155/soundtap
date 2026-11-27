/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* WPA2-PSK (CCMP) 4-way supplicant -- see wpa.h. Pure, freestanding, no libc. */
#include "wpa.h"

/* ---- local libc-free helpers (freestanding: no implicit memcpy/memset) ---- */
static void cp(void *d, const void *s, size_t n)
{
    unsigned char *p = d;
    const unsigned char *q = s;
    while (n--)
        *p++ = *q++;
}
static void zero(void *d, size_t n)
{
    unsigned char *p = d;
    while (n--)
        *p++ = 0;
}
static int eq(const void *a, const void *b, size_t n)
{
    const unsigned char *p = a, *q = b;
    while (n--)
        if (*p++ != *q++)
            return 0;
    return 1;
}

/* ============================ SHA-1 (RFC 3174) ============================ */
struct sha1 {
    uint32_t h[5];
    uint64_t len;
    uint8_t buf[64];
    unsigned n;
};
static uint32_t rol(uint32_t v, unsigned s) { return (v << s) | (v >> (32 - s)); }
static void sha1_block(struct sha1 *c, const uint8_t *p)
{
    uint32_t w[80], a, b, d, e;
    unsigned i;
    for (i = 0; i < 16; i++)
        w[i] = (uint32_t)p[4 * i] << 24 | (uint32_t)p[4 * i + 1] << 16 |
               (uint32_t)p[4 * i + 2] << 8 | p[4 * i + 3];
    for (i = 16; i < 80; i++)
        w[i] = rol(w[i - 3] ^ w[i - 8] ^ w[i - 14] ^ w[i - 16], 1);
    a = c->h[0], b = c->h[1], d = c->h[2], e = c->h[3];
    {
        uint32_t cc = c->h[4];
        for (i = 0; i < 80; i++) {
            uint32_t f, k;
            if (i < 20)
                f = (b & d) | (~b & e), k = 0x5a827999;
            else if (i < 40)
                f = b ^ d ^ e, k = 0x6ed9eba1;
            else if (i < 60)
                f = (b & d) | (b & e) | (d & e), k = 0x8f1bbcdc;
            else
                f = b ^ d ^ e, k = 0xca62c1d6;
            uint32_t t = rol(a, 5) + f + cc + k + w[i];
            cc = e, e = d, d = rol(b, 30), b = a, a = t;
        }
        c->h[0] += a, c->h[1] += b, c->h[2] += d, c->h[3] += e, c->h[4] += cc;
    }
}
static void sha1_init(struct sha1 *c)
{
    c->h[0] = 0x67452301, c->h[1] = 0xefcdab89, c->h[2] = 0x98badcfe;
    c->h[3] = 0x10325476, c->h[4] = 0xc3d2e1f0;
    c->len = 0, c->n = 0;
}
static void sha1_update(struct sha1 *c, const uint8_t *p, size_t n)
{
    c->len += (uint64_t)n * 8;
    while (n) {
        unsigned r = 64 - c->n, k = n < r ? (unsigned)n : r;
        cp(c->buf + c->n, p, k);
        c->n += k, p += k, n -= k;
        if (c->n == 64)
            sha1_block(c, c->buf), c->n = 0;
    }
}
static void sha1_final(struct sha1 *c, uint8_t out[20])
{
    uint64_t bits = c->len;
    uint8_t pad = 0x80;
    unsigned i;
    sha1_update(c, &pad, 1);
    pad = 0;
    while (c->n != 56)
        sha1_update(c, &pad, 1);
    for (i = 0; i < 8; i++) {
        uint8_t b = (uint8_t)(bits >> (56 - 8 * i));
        sha1_update(c, &b, 1);
    }
    for (i = 0; i < 5; i++) {
        out[4 * i] = (uint8_t)(c->h[i] >> 24), out[4 * i + 1] = (uint8_t)(c->h[i] >> 16);
        out[4 * i + 2] = (uint8_t)(c->h[i] >> 8), out[4 * i + 3] = (uint8_t)c->h[i];
    }
}
void wpa_sha1(const uint8_t *msg, size_t len, uint8_t out[20])
{
    struct sha1 c;
    sha1_init(&c);
    sha1_update(&c, msg, len);
    sha1_final(&c, out);
}

/* ============================ HMAC-SHA1 (RFC 2104) ============================ */
void wpa_hmac_sha1(const uint8_t *key, size_t key_len, const uint8_t *data, size_t data_len,
                   uint8_t out[20])
{
    uint8_t k[64], ipad[64], opad[64], inner[20];
    struct sha1 c;
    unsigned i;
    zero(k, 64);
    if (key_len > 64) {
        wpa_sha1(key, key_len, k); /* keys longer than the block get hashed first */
    } else {
        cp(k, key, key_len);
    }
    for (i = 0; i < 64; i++)
        ipad[i] = k[i] ^ 0x36, opad[i] = k[i] ^ 0x5c;
    sha1_init(&c);
    sha1_update(&c, ipad, 64);
    sha1_update(&c, data, data_len);
    sha1_final(&c, inner);
    sha1_init(&c);
    sha1_update(&c, opad, 64);
    sha1_update(&c, inner, 20);
    sha1_final(&c, out);
}

void wpa_sha1_prf(const uint8_t *key, size_t key_len, const char *label, const uint8_t *data,
                  size_t data_len, uint8_t *out, size_t out_len)
{
    uint8_t buf[128], digest[20];
    size_t label_len = 0, pos = 0;
    unsigned char i = 0;
    while (label[label_len])
        label_len++;
    /* buf = label || 0x00 || data || i */
    cp(buf, (const uint8_t *)label, label_len);
    buf[label_len] = 0;
    cp(buf + label_len + 1, data, data_len);
    while (pos < out_len) {
        size_t need = out_len - pos;
        buf[label_len + 1 + data_len] = i;
        wpa_hmac_sha1(key, key_len, buf, label_len + 2 + data_len, digest);
        cp(out + pos, digest, need < 20 ? need : 20);
        pos += 20, i++;
    }
}

/* PBKDF2-HMAC-SHA1 for the WPA PMK: PMK = PBKDF2(passphrase, SSID, 4096, 32). Two 20-byte
 * blocks, truncated to 32. 4096 iterations each -- runs once per connect. */
void wpa_pbkdf2(const uint8_t *pass, size_t pass_len, const uint8_t *ssid, size_t ssid_len,
                uint8_t pmk[32])
{
    for (unsigned block = 1; block <= 2; block++) {
        uint8_t salt[36], u[20], t[20];
        size_t sl = ssid_len > 32 ? 32 : ssid_len;
        cp(salt, ssid, sl);
        salt[sl] = 0, salt[sl + 1] = 0, salt[sl + 2] = 0, salt[sl + 3] = (uint8_t)block;
        wpa_hmac_sha1(pass, pass_len, salt, sl + 4, u);
        cp(t, u, 20);
        for (unsigned c = 1; c < 4096; c++) {
            wpa_hmac_sha1(pass, pass_len, u, 20, u);
            for (unsigned i = 0; i < 20; i++)
                t[i] ^= u[i];
        }
        cp(pmk + (block - 1) * 20, t, block == 1 ? 20 : 12);
    }
}

/* ============================ SHA-256 (FIPS 180-4), HMAC-SHA256, KDF ============================ */
/* For the AKMs with SHA-256 (PSK-SHA256 and SAE, ADR 0136): the 802.11 KDF that derives the PTK. */
struct sha256 {
    uint32_t h[8];
    uint64_t len;
    uint8_t buf[64];
    unsigned n;
};
static uint32_t ror32(uint32_t v, unsigned s) { return (v >> s) | (v << (32 - s)); }
static void sha256_block(struct sha256 *c, const uint8_t *p)
{
    static const uint32_t k[64] = {
        0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
        0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
        0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
        0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
        0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
        0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
        0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
        0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2};
    uint32_t w[64], v[8];
    unsigned i;
    for (i = 0; i < 16; i++)
        w[i] = (uint32_t)p[4 * i] << 24 | (uint32_t)p[4 * i + 1] << 16 | (uint32_t)p[4 * i + 2] << 8 |
               p[4 * i + 3];
    for (i = 16; i < 64; i++) {
        uint32_t s0 = ror32(w[i - 15], 7) ^ ror32(w[i - 15], 18) ^ (w[i - 15] >> 3);
        uint32_t s1 = ror32(w[i - 2], 17) ^ ror32(w[i - 2], 19) ^ (w[i - 2] >> 10);
        w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }
    for (i = 0; i < 8; i++)
        v[i] = c->h[i];
    for (i = 0; i < 64; i++) {
        uint32_t t1 = v[7] + (ror32(v[4], 6) ^ ror32(v[4], 11) ^ ror32(v[4], 25)) +
                      ((v[4] & v[5]) ^ (~v[4] & v[6])) + k[i] + w[i];
        uint32_t t2 = (ror32(v[0], 2) ^ ror32(v[0], 13) ^ ror32(v[0], 22)) +
                      ((v[0] & v[1]) ^ (v[0] & v[2]) ^ (v[1] & v[2]));
        v[7] = v[6], v[6] = v[5], v[5] = v[4], v[4] = v[3] + t1;
        v[3] = v[2], v[2] = v[1], v[1] = v[0], v[0] = t1 + t2;
    }
    for (i = 0; i < 8; i++)
        c->h[i] += v[i];
}
static void sha256_init(struct sha256 *c)
{
    static const uint32_t iv[8] = {0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
                                   0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19};
    for (unsigned i = 0; i < 8; i++)
        c->h[i] = iv[i];
    c->len = 0, c->n = 0;
}
static void sha256_update(struct sha256 *c, const uint8_t *p, size_t n)
{
    c->len += (uint64_t)n * 8;
    while (n) {
        unsigned r = 64 - c->n, k = n < r ? (unsigned)n : r;
        cp(c->buf + c->n, p, k);
        c->n += k, p += k, n -= k;
        if (c->n == 64)
            sha256_block(c, c->buf), c->n = 0;
    }
}
static void sha256_final(struct sha256 *c, uint8_t out[32])
{
    uint64_t bits = c->len;
    uint8_t pad = 0x80;
    unsigned i;
    sha256_update(c, &pad, 1);
    pad = 0;
    while (c->n != 56)
        sha256_update(c, &pad, 1);
    for (i = 0; i < 8; i++) {
        uint8_t b = (uint8_t)(bits >> (56 - 8 * i));
        sha256_update(c, &b, 1);
    }
    for (i = 0; i < 32; i++)
        out[i] = (uint8_t)(c->h[i / 4] >> (24 - 8 * (i % 4)));
}
void wpa_sha256(const uint8_t *msg, size_t len, uint8_t out[32])
{
    struct sha256 c;
    sha256_init(&c);
    sha256_update(&c, msg, len);
    sha256_final(&c, out);
}
/* HMAC-SHA256 over the concatenation of up to three parts (the KDF's counter, label and context
 * go in without being copied together). */
static void hmac_sha256_v(const uint8_t *key, size_t key_len, const uint8_t *const part[3], const size_t plen[3],
                          uint8_t out[32])
{
    uint8_t k[64], pad[64], inner[32];
    struct sha256 c;
    unsigned i;
    zero(k, 64);
    if (key_len > 64)
        wpa_sha256(key, key_len, k);
    else
        cp(k, key, key_len);
    for (i = 0; i < 64; i++)
        pad[i] = k[i] ^ 0x36;
    sha256_init(&c);
    sha256_update(&c, pad, 64);
    for (i = 0; i < 3; i++)
        if (plen[i])
            sha256_update(&c, part[i], plen[i]);
    sha256_final(&c, inner);
    for (i = 0; i < 64; i++)
        pad[i] = k[i] ^ 0x5c;
    sha256_init(&c);
    sha256_update(&c, pad, 64);
    sha256_update(&c, inner, 32);
    sha256_final(&c, out);
}
void wpa_hmac_sha256(const uint8_t *key, size_t key_len, const uint8_t *data, size_t data_len,
                     uint8_t out[32])
{
    const uint8_t *part[3] = {data, 0, 0};
    const size_t plen[3] = {data_len, 0, 0};
    hmac_sha256_v(key, key_len, part, plen, out);
}
/* IEEE 802.11-2020 12.7.1.6.2: HMAC-SHA256(K, i || label || context || length), i and length
 * (in bits) as little-endian u16, as many rounds as length needs. */
void wpa_kdf_sha256(const uint8_t *key, size_t key_len, const char *label, const uint8_t *ctx,
                    size_t ctx_len, uint8_t *out, size_t out_len)
{
    uint8_t buf[2 + 64 + 2], d[32];
    size_t label_len = 0, pos = 0;
    unsigned i = 1;
    while (label[label_len])
        label_len++;
    if (label_len > 64)
        return;
    cp(buf + 2, (const uint8_t *)label, label_len);
    for (; pos < out_len; i++, pos += 32) {
        const uint8_t *part[3];
        size_t plen[3];
        uint8_t len[2] = {(uint8_t)(out_len * 8), (uint8_t)(out_len * 8 >> 8)};
        buf[0] = (uint8_t)i, buf[1] = (uint8_t)(i >> 8);
        part[0] = buf, plen[0] = 2 + label_len;
        part[1] = ctx, plen[1] = ctx_len;
        part[2] = len, plen[2] = 2;
        hmac_sha256_v(key, key_len, part, plen, d);
        cp(out + pos, d, out_len - pos < 32 ? out_len - pos : 32);
    }
}

/* ============================ MD5 (RFC 1321), HMAC-MD5 (RFC 2104) ============================ */
/* Only for WPA's key descriptor version 1 (TKIP pairwise): its EAPOL-Key MIC is HMAC-MD5. */
struct md5 {
    uint32_t h[4];
    uint64_t len;
    uint8_t buf[64];
    unsigned n;
};
static void md5_block(struct md5 *c, const uint8_t *p)
{
    static const uint8_t r[64] = {7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22,
                                  5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20,
                                  4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23,
                                  6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21};
    static const uint32_t k[64] = {
        0xd76aa478, 0xe8c7b756, 0x242070db, 0xc1bdceee, 0xf57c0faf, 0x4787c62a, 0xa8304613, 0xfd469501,
        0x698098d8, 0x8b44f7af, 0xffff5bb1, 0x895cd7be, 0x6b901122, 0xfd987193, 0xa679438e, 0x49b40821,
        0xf61e2562, 0xc040b340, 0x265e5a51, 0xe9b6c7aa, 0xd62f105d, 0x02441453, 0xd8a1e681, 0xe7d3fbc8,
        0x21e1cde6, 0xc33707d6, 0xf4d50d87, 0x455a14ed, 0xa9e3e905, 0xfcefa3f8, 0x676f02d9, 0x8d2a4c8a,
        0xfffa3942, 0x8771f681, 0x6d9d6122, 0xfde5380c, 0xa4beea44, 0x4bdecfa9, 0xf6bb4b60, 0xbebfbc70,
        0x289b7ec6, 0xeaa127fa, 0xd4ef3085, 0x04881d05, 0xd9d4d039, 0xe6db99e5, 0x1fa27cf8, 0xc4ac5665,
        0xf4292244, 0x432aff97, 0xab9423a7, 0xfc93a039, 0x655b59c3, 0x8f0ccc92, 0xffeff47d, 0x85845dd1,
        0x6fa87e4f, 0xfe2ce6e0, 0xa3014314, 0x4e0811a1, 0xf7537e82, 0xbd3af235, 0x2ad7d2bb, 0xeb86d391};
    uint32_t w[16], a = c->h[0], b = c->h[1], cc = c->h[2], d = c->h[3];
    unsigned i;
    for (i = 0; i < 16; i++)
        w[i] = p[4 * i] | (uint32_t)p[4 * i + 1] << 8 | (uint32_t)p[4 * i + 2] << 16 |
               (uint32_t)p[4 * i + 3] << 24;
    for (i = 0; i < 64; i++) {
        uint32_t f, t;
        unsigned g;
        if (i < 16)
            f = (b & cc) | (~b & d), g = i;
        else if (i < 32)
            f = (d & b) | (~d & cc), g = (5 * i + 1) & 15;
        else if (i < 48)
            f = b ^ cc ^ d, g = (3 * i + 5) & 15;
        else
            f = cc ^ (b | ~d), g = (7 * i) & 15;
        t = d, d = cc, cc = b;
        b = b + rol(a + f + k[i] + w[g], r[i]);
        a = t;
    }
    c->h[0] += a, c->h[1] += b, c->h[2] += cc, c->h[3] += d;
}
static void md5_init(struct md5 *c)
{
    c->h[0] = 0x67452301, c->h[1] = 0xefcdab89, c->h[2] = 0x98badcfe, c->h[3] = 0x10325476;
    c->len = 0, c->n = 0;
}
static void md5_update(struct md5 *c, const uint8_t *p, size_t n)
{
    c->len += (uint64_t)n * 8;
    while (n) {
        unsigned r = 64 - c->n, k = n < r ? (unsigned)n : r;
        cp(c->buf + c->n, p, k);
        c->n += k, p += k, n -= k;
        if (c->n == 64)
            md5_block(c, c->buf), c->n = 0;
    }
}
static void md5_final(struct md5 *c, uint8_t out[16])
{
    uint64_t bits = c->len;
    uint8_t pad = 0x80;
    unsigned i;
    md5_update(c, &pad, 1);
    pad = 0;
    while (c->n != 56)
        md5_update(c, &pad, 1);
    for (i = 0; i < 8; i++) {
        uint8_t b = (uint8_t)(bits >> (8 * i));
        md5_update(c, &b, 1);
    }
    for (i = 0; i < 16; i++)
        out[i] = (uint8_t)(c->h[i / 4] >> (8 * (i % 4)));
}
void wpa_md5(const uint8_t *msg, size_t len, uint8_t out[16])
{
    struct md5 c;
    md5_init(&c);
    md5_update(&c, msg, len);
    md5_final(&c, out);
}
void wpa_hmac_md5(const uint8_t *key, size_t key_len, const uint8_t *data, size_t data_len,
                  uint8_t out[16])
{
    uint8_t k[64], pad[64], inner[16];
    struct md5 c;
    unsigned i;
    zero(k, 64);
    if (key_len > 64)
        wpa_md5(key, key_len, k);
    else
        cp(k, key, key_len);
    for (i = 0; i < 64; i++)
        pad[i] = k[i] ^ 0x36;
    md5_init(&c);
    md5_update(&c, pad, 64);
    md5_update(&c, data, data_len);
    md5_final(&c, inner);
    for (i = 0; i < 64; i++)
        pad[i] = k[i] ^ 0x5c;
    md5_init(&c);
    md5_update(&c, pad, 64);
    md5_update(&c, inner, 16);
    md5_final(&c, out);
}

/* ============================ RC4 ============================ */
void wpa_rc4_skip(const uint8_t *key, unsigned klen, unsigned skip, uint8_t *buf, unsigned n)
{
    uint8_t st[256];
    unsigned i, j = 0;
    for (i = 0; i < 256; i++)
        st[i] = (uint8_t)i;
    for (i = 0; i < 256; i++) {
        uint8_t t = st[i];
        j = (j + t + key[i % klen]) & 0xff;
        st[i] = st[j], st[j] = t;
    }
    i = j = 0;
    for (unsigned k = 0; k < skip + n; k++) {
        uint8_t t;
        i = (i + 1) & 0xff;
        t = st[i];
        j = (j + t) & 0xff;
        st[i] = st[j], st[j] = t;
        if (k >= skip)
            buf[k - skip] ^= st[(st[i] + t) & 0xff];
    }
}

/* ============================ AES-128 decrypt (for RFC 3394 unwrap) ============================ */
static const uint8_t sbox[256] = {
    0x63, 0x7c, 0x77, 0x7b, 0xf2, 0x6b, 0x6f, 0xc5, 0x30, 0x01, 0x67, 0x2b, 0xfe, 0xd7, 0xab, 0x76,
    0xca, 0x82, 0xc9, 0x7d, 0xfa, 0x59, 0x47, 0xf0, 0xad, 0xd4, 0xa2, 0xaf, 0x9c, 0xa4, 0x72, 0xc0,
    0xb7, 0xfd, 0x93, 0x26, 0x36, 0x3f, 0xf7, 0xcc, 0x34, 0xa5, 0xe5, 0xf1, 0x71, 0xd8, 0x31, 0x15,
    0x04, 0xc7, 0x23, 0xc3, 0x18, 0x96, 0x05, 0x9a, 0x07, 0x12, 0x80, 0xe2, 0xeb, 0x27, 0xb2, 0x75,
    0x09, 0x83, 0x2c, 0x1a, 0x1b, 0x6e, 0x5a, 0xa0, 0x52, 0x3b, 0xd6, 0xb3, 0x29, 0xe3, 0x2f, 0x84,
    0x53, 0xd1, 0x00, 0xed, 0x20, 0xfc, 0xb1, 0x5b, 0x6a, 0xcb, 0xbe, 0x39, 0x4a, 0x4c, 0x58, 0xcf,
    0xd0, 0xef, 0xaa, 0xfb, 0x43, 0x4d, 0x33, 0x85, 0x45, 0xf9, 0x02, 0x7f, 0x50, 0x3c, 0x9f, 0xa8,
    0x51, 0xa3, 0x40, 0x8f, 0x92, 0x9d, 0x38, 0xf5, 0xbc, 0xb6, 0xda, 0x21, 0x10, 0xff, 0xf3, 0xd2,
    0xcd, 0x0c, 0x13, 0xec, 0x5f, 0x97, 0x44, 0x17, 0xc4, 0xa7, 0x7e, 0x3d, 0x64, 0x5d, 0x19, 0x73,
    0x60, 0x81, 0x4f, 0xdc, 0x22, 0x2a, 0x90, 0x88, 0x46, 0xee, 0xb8, 0x14, 0xde, 0x5e, 0x0b, 0xdb,
    0xe0, 0x32, 0x3a, 0x0a, 0x49, 0x06, 0x24, 0x5c, 0xc2, 0xd3, 0xac, 0x62, 0x91, 0x95, 0xe4, 0x79,
    0xe7, 0xc8, 0x37, 0x6d, 0x8d, 0xd5, 0x4e, 0xa9, 0x6c, 0x56, 0xf4, 0xea, 0x65, 0x7a, 0xae, 0x08,
    0xba, 0x78, 0x25, 0x2e, 0x1c, 0xa6, 0xb4, 0xc6, 0xe8, 0xdd, 0x74, 0x1f, 0x4b, 0xbd, 0x8b, 0x8a,
    0x70, 0x3e, 0xb5, 0x66, 0x48, 0x03, 0xf6, 0x0e, 0x61, 0x35, 0x57, 0xb9, 0x86, 0xc1, 0x1d, 0x9e,
    0xe1, 0xf8, 0x98, 0x11, 0x69, 0xd9, 0x8e, 0x94, 0x9b, 0x1e, 0x87, 0xe9, 0xce, 0x55, 0x28, 0xdf,
    0x8c, 0xa1, 0x89, 0x0d, 0xbf, 0xe6, 0x42, 0x68, 0x41, 0x99, 0x2d, 0x0f, 0xb0, 0x54, 0xbb, 0x16};
uint8_t wpa_aes_sbox(unsigned i)
{
    return sbox[i & 0xff];
}

static uint8_t inv_sbox[256];
static int inv_ready;
static uint8_t xtime(uint8_t x) { return (uint8_t)((x << 1) ^ ((x >> 7) * 0x1b)); }
static uint8_t mul(uint8_t a, uint8_t b)
{
    uint8_t p = 0;
    while (b) {
        if (b & 1)
            p ^= a;
        a = xtime(a);
        b >>= 1;
    }
    return p;
}
static void aes128_expand(const uint8_t key[16], uint8_t rk[176])
{
    static const uint8_t rcon[10] = {1, 2, 4, 8, 16, 32, 64, 128, 0x1b, 0x36};
    unsigned i;
    cp(rk, key, 16);
    for (i = 16; i < 176; i += 4) {
        uint8_t t[4];
        cp(t, rk + i - 4, 4);
        if (i % 16 == 0) {
            uint8_t tmp = t[0];
            t[0] = (uint8_t)(sbox[t[1]] ^ rcon[i / 16 - 1]);
            t[1] = sbox[t[2]], t[2] = sbox[t[3]], t[3] = sbox[tmp];
        }
        rk[i] = rk[i - 16] ^ t[0], rk[i + 1] = rk[i - 15] ^ t[1];
        rk[i + 2] = rk[i - 14] ^ t[2], rk[i + 3] = rk[i - 13] ^ t[3];
    }
}
void wpa_aes128_dec(const uint8_t key[16], const uint8_t in[16], uint8_t out[16])
{
    uint8_t rk[176], s[16];
    unsigned round, i;
    if (!inv_ready) {
        for (i = 0; i < 256; i++)
            inv_sbox[sbox[i]] = (uint8_t)i;
        inv_ready = 1;
    }
    aes128_expand(key, rk);
    cp(s, in, 16);
    for (i = 0; i < 16; i++)
        s[i] ^= rk[160 + i]; /* AddRoundKey(10) */
    for (round = 9; round >= 1; round--) {
        uint8_t t[16];
        /* InvShiftRows + InvSubBytes */
        static const uint8_t isr[16] = {0, 13, 10, 7, 4, 1, 14, 11, 8, 5, 2, 15, 12, 9, 6, 3};
        for (i = 0; i < 16; i++)
            t[i] = inv_sbox[s[isr[i]]];
        for (i = 0; i < 16; i++)
            t[i] ^= rk[round * 16 + i]; /* AddRoundKey */
        /* InvMixColumns */
        for (i = 0; i < 16; i += 4) {
            uint8_t a0 = t[i], a1 = t[i + 1], a2 = t[i + 2], a3 = t[i + 3];
            s[i] = mul(a0, 14) ^ mul(a1, 11) ^ mul(a2, 13) ^ mul(a3, 9);
            s[i + 1] = mul(a0, 9) ^ mul(a1, 14) ^ mul(a2, 11) ^ mul(a3, 13);
            s[i + 2] = mul(a0, 13) ^ mul(a1, 9) ^ mul(a2, 14) ^ mul(a3, 11);
            s[i + 3] = mul(a0, 11) ^ mul(a1, 13) ^ mul(a2, 9) ^ mul(a3, 14);
        }
    }
    {
        static const uint8_t isr[16] = {0, 13, 10, 7, 4, 1, 14, 11, 8, 5, 2, 15, 12, 9, 6, 3};
        uint8_t t[16];
        for (i = 0; i < 16; i++)
            t[i] = inv_sbox[s[isr[i]]];
        for (i = 0; i < 16; i++)
            out[i] = t[i] ^ rk[i]; /* AddRoundKey(0) */
    }
}

int wpa_aes_unwrap(const uint8_t kek[16], unsigned n, const uint8_t *cipher, uint8_t *plain)
{
    uint8_t a[8], b[16];
    unsigned i, j;
    int round;
    static const uint8_t iv[8] = {0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6, 0xa6};
    cp(a, cipher, 8);
    cp(plain, cipher + 8, n * 8);
    for (round = 5; round >= 0; round--) {
        for (i = n; i >= 1; i--) {
            uint64_t t = (uint64_t)n * (unsigned)round + i;
            for (j = 0; j < 8; j++)
                a[j] ^= (uint8_t)(t >> (56 - 8 * j));
            cp(b, a, 8);
            cp(b + 8, plain + (i - 1) * 8, 8);
            wpa_aes128_dec(kek, b, b);
            cp(a, b, 8);
            cp(plain + (i - 1) * 8, b + 8, 8);
        }
    }
    return eq(a, iv, 8);
}

/* ============================ AES-128 encrypt, AES-CMAC (RFC 4493) ============================ */
/* For PMF (ADR 0136): CCMP of the management frames the app protects itself, BIP's MIC, and the
 * EAPOL-Key MIC under the SHA-256 AKMs. */
void wpa_aes128_enc(const uint8_t key[16], const uint8_t in[16], uint8_t out[16])
{
    static const uint8_t sr[16] = {0, 5, 10, 15, 4, 9, 14, 3, 8, 13, 2, 7, 12, 1, 6, 11};
    uint8_t rk[176], st[16], t[16];
    unsigned round, i;
    aes128_expand(key, rk);
    for (i = 0; i < 16; i++)
        st[i] = in[i] ^ rk[i];
    for (round = 1; round <= 10; round++) {
        for (i = 0; i < 16; i++)
            t[i] = sbox[st[sr[i]]];
        for (i = 0; round < 10 && i < 16; i += 4) {
            uint8_t a0 = t[i], a1 = t[i + 1], a2 = t[i + 2], a3 = t[i + 3];
            t[i] = xtime(a0) ^ xtime(a1) ^ a1 ^ a2 ^ a3;
            t[i + 1] = a0 ^ xtime(a1) ^ xtime(a2) ^ a2 ^ a3;
            t[i + 2] = a0 ^ a1 ^ xtime(a2) ^ xtime(a3) ^ a3;
            t[i + 3] = xtime(a0) ^ a0 ^ a1 ^ a2 ^ xtime(a3);
        }
        for (i = 0; i < 16; i++)
            st[i] = t[i] ^ rk[round * 16 + i];
    }
    cp(out, st, 16);
}

static void cmac_double(uint8_t b[16])
{
    uint8_t carry = b[0] >> 7;
    for (unsigned i = 0; i < 15; i++)
        b[i] = (uint8_t)(b[i] << 1 | b[i + 1] >> 7);
    b[15] = (uint8_t)(b[15] << 1 ^ (carry ? 0x87 : 0));
}

void wpa_cmac(const uint8_t key[16], const uint8_t *msg, size_t len, uint8_t mac[16])
{
    uint8_t l[16], x[16], y[16];
    size_t blocks = len ? (len + 15) / 16 : 1, i, j;
    int whole = len && len % 16 == 0;
    zero(l, 16);
    wpa_aes128_enc(key, l, l);
    cmac_double(l);          /* K1 */
    if (!whole)
        cmac_double(l);      /* K2 */
    zero(x, 16);
    for (i = 0; i < blocks; i++) {
        size_t off = i * 16, n = len - off < 16 ? len - off : 16;
        for (j = 0; j < 16; j++)
            y[j] = j < n ? msg[off + j] : (j == n ? 0x80 : 0);
        if (i == blocks - 1)
            for (j = 0; j < 16; j++)
                y[j] ^= l[j];
        for (j = 0; j < 16; j++)
            x[j] ^= y[j];
        wpa_aes128_enc(key, x, x);
    }
    cp(mac, x, 16);
}

/* ============================ PTK ============================ */
static void ptk_data(const uint8_t aa[6], const uint8_t spa[6], const uint8_t anonce[32],
                     const uint8_t snonce[32], uint8_t data[76]);

void wpa_pmk_to_ptk(const uint8_t pmk[32], const uint8_t aa[6], const uint8_t spa[6],
                    const uint8_t anonce[32], const uint8_t snonce[32], uint8_t *ptk, unsigned ptk_len)
{
    uint8_t data[76];
    ptk_data(aa, spa, anonce, snonce, data);
    wpa_sha1_prf(pmk, 32, "Pairwise key expansion", data, 76, ptk, ptk_len);
}

/* Under the SHA-256 AKMs (PSK-SHA256, SAE) the PTK comes from the KDF over the same data. */
void wpa_pmk_to_ptk_sha256(const uint8_t pmk[32], const uint8_t aa[6], const uint8_t spa[6],
                           const uint8_t anonce[32], const uint8_t snonce[32], uint8_t *ptk, unsigned ptk_len)
{
    uint8_t data[76];
    ptk_data(aa, spa, anonce, snonce, data);
    wpa_kdf_sha256(pmk, 32, "Pairwise key expansion", data, 76, ptk, ptk_len);
}

static void ptk_data(const uint8_t aa[6], const uint8_t spa[6], const uint8_t anonce[32],
                     const uint8_t snonce[32], uint8_t data[76])
{
    /* min||max MAC (12) + min||max nonce (64), by byte compare */
    {
        int maccmp = 0, noncecmp = 0;
        unsigned i;
        for (i = 0; i < 6; i++) {
            if (aa[i] != spa[i]) {
                maccmp = aa[i] < spa[i] ? -1 : 1;
                break;
            }
        }
        for (i = 0; i < 32; i++) {
            if (anonce[i] != snonce[i]) {
                noncecmp = anonce[i] < snonce[i] ? -1 : 1;
                break;
            }
        }
        cp(data + 0, maccmp <= 0 ? aa : spa, 6);
        cp(data + 6, maccmp <= 0 ? spa : aa, 6);
        cp(data + 12, noncecmp <= 0 ? anonce : snonce, 32);
        cp(data + 44, noncecmp <= 0 ? snonce : anonce, 32);
    }
}

/* ============================ 4-way state machine ============================ */
/* EAPOL-Key offsets from the 802.1X header byte. */
#define O_KEYINFO 5
#define O_KEYLEN 7
#define O_REPLAY 9
#define O_NONCE 17
#define O_IV 49
#define O_RSC 65
#define O_MIC 81
#define O_KDLEN 97
#define O_KD 99
#define KI_VER 0x0007
#define KI_PAIRWISE 0x0008
#define KI_KEYIDX 0x0030 /* WPA (descriptor 254) group messages: the GTK's key id */
#define KI_INSTALL 0x0040
#define KI_ACK 0x0080
#define KI_MIC 0x0100
#define KI_SECURE 0x0200
#define KI_ERROR 0x0400
#define KI_REQUEST 0x0800
#define KI_ENCRYPTED 0x1000
#define DESC_RSN 2
#define DESC_WPA 254

static uint16_t be16(const uint8_t *p) { return (uint16_t)(p[0] << 8 | p[1]); }
static void wbe16(uint8_t *p, uint16_t v) { p[0] = (uint8_t)(v >> 8), p[1] = (uint8_t)v; }

/* The security IE this station sends (wpa_own_ie): the RSN IE (IEEE 802.11-2020 9.4.2.24) or WPA's
 * vendor IE (00-50-F2 type 1), one pairwise suite, one AKM, and for RSN the capabilities. */
unsigned wpa_own_ie(uint8_t *out, unsigned proto, unsigned group, unsigned pairwise, unsigned akm,
                    unsigned caps)
{
    unsigned o = 0;
    static const uint8_t rsn_oui[3] = {0x00, 0x0f, 0xac}, wpa_oui[3] = {0x00, 0x50, 0xf2};
    const uint8_t *oui = proto == WPA_PROTO_WPA ? wpa_oui : rsn_oui;
    if (proto == WPA_PROTO_WPA) {
        out[o++] = 0xdd, out[o++] = 22;
        cp(out + o, wpa_oui, 3), o += 3, out[o++] = 1;
    } else {
        out[o++] = 0x30, out[o++] = 20;
    }
    out[o++] = 1, out[o++] = 0; /* version 1 */
    cp(out + o, oui, 3), o += 3, out[o++] = (uint8_t)group;
    out[o++] = 1, out[o++] = 0;
    cp(out + o, oui, 3), o += 3, out[o++] = (uint8_t)pairwise;
    out[o++] = 1, out[o++] = 0;
    cp(out + o, oui, 3), o += 3, out[o++] = (uint8_t)akm;
    if (proto != WPA_PROTO_WPA)
        out[o++] = (uint8_t)caps, out[o++] = (uint8_t)(caps >> 8);
    return o;
}

/* The RSN IE of a WPA2-PSK station with pairwise CCMP and the given group (kept for the tests and
 * the access point's needs); wpa_own_ie is the general form. */
void wpa_rsn_ie(uint8_t out[WPA_RSN_IE_LEN], unsigned group)
{
    wpa_own_ie(out, WPA_PROTO_RSN, group == 2 ? 2 : 4, WPA_CIPHER_CCMP, WPA_AKM_PSK, 0);
}

void wpa_sm_init(struct wpa_sm *sm, const uint8_t pmk[32], const uint8_t aa[6], const uint8_t spa[6],
                 const uint8_t snonce[32])
{
    zero(sm, sizeof *sm);
    cp(sm->pmk, pmk, 32);
    cp(sm->aa, aa, 6);
    cp(sm->spa, spa, 6);
    cp(sm->snonce, snonce, 32);
    sm->state = WPA_IDLE;
    sm->gtk_idx = -1;
    sm->igtk_idx = -1;
    wpa_sm_config(sm, WPA_PROTO_RSN, WPA_CIPHER_CCMP, WPA_CIPHER_CCMP, WPA_AKM_PSK, 0);
}

void wpa_sm_config(struct wpa_sm *sm, unsigned proto, unsigned pairwise, unsigned group, unsigned akm,
                   unsigned caps)
{
    sm->proto = (uint8_t)proto, sm->pairwise = (uint8_t)pairwise, sm->group = (uint8_t)group;
    sm->akm = (uint8_t)akm;
    sm->ielen = (uint8_t)wpa_own_ie(sm->ie, proto, group, pairwise, akm, caps);
    /* the key descriptor version: HMAC-MD5 and RC4 under TKIP, HMAC-SHA1 and AES key wrap under
     * CCMP; under PSK-SHA256 version 3 and under SAE 0 (AKM-defined), both AES-CMAC and AES key
     * wrap (IEEE 802.11-2020 12.7.2) */
    sm->ver = akm == WPA_AKM_SAE ? 0 : akm == WPA_AKM_PSK_SHA256 ? 3 : pairwise == WPA_CIPHER_TKIP ? 1 : 2;
    sm->mfp = (caps & WPA_CAP_MFPC) != 0;
    sm->ptk_len = pairwise == WPA_CIPHER_TKIP ? 64 : 48;
}

/* Walk msg3 decrypted key data: it is the RSN IE (0x30..) followed by the GTK KDE (0xdd 00 0f ac 01
 * keyid res GTK..) and then 0-padding. Skip anything that is not the GTK KDE; stop on padding/end.
 * Returns 1 with the GTK filled in, or 0 if no GTK KDE is present. */
int wpa_extract_gtk(const uint8_t *kd, unsigned plen, uint8_t gtk[32], unsigned *gtk_len,
                    int *gtk_idx, int *gtk_tx)
{
    for (unsigned p = 0; p + 2 <= plen;) {
        unsigned l = kd[p + 1];
        if (l == 0 || p + 2 + l > plen) /* padding (0xdd 00 / 00..) or end */
            break;
        if (kd[p] == 0xdd && l >= 6 && kd[p + 2] == 0x00 && kd[p + 3] == 0x0f &&
            kd[p + 4] == 0xac && kd[p + 5] == 0x01) { /* GTK KDE (skip RSN IE & others) */
            unsigned n = l - 6;
            if (n > 32)
                n = 32;
            *gtk_idx = kd[p + 6] & 3;
            *gtk_tx = (kd[p + 6] >> 2) & 1;
            *gtk_len = n;
            cp(gtk, kd + p + 8, n);
            return 1;
        }
        p += 2 + l;
    }
    return 0;
}

/* The IGTK KDE (00-0F-AC:9: key id, IPN, IGTK) of decrypted key data, under management frame
 * protection. Returns 1 with the key taken. */
static int extract_igtk(struct wpa_sm *sm, const uint8_t *kd, unsigned plen)
{
    for (unsigned p = 0; p + 2 <= plen;) {
        unsigned l = kd[p + 1];
        if (l == 0 || p + 2 + l > plen)
            break;
        if (kd[p] == 0xdd && l >= 4 + 2 + 6 + 16 && kd[p + 2] == 0x00 && kd[p + 3] == 0x0f &&
            kd[p + 4] == 0xac && kd[p + 5] == 0x09) {
            unsigned id = kd[p + 6] | kd[p + 7] << 8;
            if (id != 4 && id != 5)
                return 0;
            sm->igtk_idx = (int)id;
            cp(sm->ipn, kd + p + 8, 6);
            cp(sm->igtk, kd + p + 14, 16);
            sm->igtk_set = 1;
            return 1;
        }
        p += 2 + l;
    }
    return 0;
}

/* 1 when the 8-byte big-endian replay counter a is newer than b. */
static int replay_newer(const uint8_t *a, const uint8_t *b)
{
    for (unsigned i = 0; i < 8; i++)
        if (a[i] != b[i])
            return a[i] > b[i];
    return 0;
}

/* The EAPOL-Key MIC of a frame (its MIC field zeroed) under the KCK, by descriptor version. */
static void mic_calc(const struct wpa_sm *sm, const uint8_t kck[16], const uint8_t *f, size_t n,
                     uint8_t mic[16])
{
    uint8_t d[20];
    if (sm->ver == 0 || sm->ver == 3) {
        wpa_cmac(kck, f, n, mic);
        return;
    }
    if (sm->ver == 1)
        wpa_hmac_md5(kck, 16, f, n, d);
    else
        wpa_hmac_sha1(kck, 16, f, n, d);
    cp(mic, d, 16);
}

static int mic_ok(const struct wpa_sm *sm, const uint8_t kck[16], const uint8_t *in, size_t in_len)
{
    uint8_t frame[WPA_EAPOL_RX_MAX], mic[16];
    if (in_len > sizeof frame)
        return 0;
    cp(frame, in, in_len);
    zero(frame + O_MIC, 16);
    mic_calc(sm, kck, frame, in_len, mic);
    return eq(mic, in + O_MIC, 16);
}

/* An EAPOL-Key frame head of ours: protocol version echoed, descriptor type, key info, the key
 * length echoed, the replay counter, zeroed nonce, IV, RSC, ID and MIC; kdlen key data bytes. */
static void head(const struct wpa_sm *sm, const uint8_t *in, uint16_t ki, const uint8_t *replay,
                 unsigned kdlen, uint8_t *out)
{
    zero(out, O_KD);
    out[0] = in ? in[0] : 2, out[1] = 3;
    wbe16(out + 2, (uint16_t)(O_KD - 4 + kdlen));
    out[4] = sm->proto == WPA_PROTO_WPA ? DESC_WPA : DESC_RSN;
    wbe16(out + O_KEYINFO, ki);
    if (in)
        cp(out + O_KEYLEN, in + O_KEYLEN, 2);
    cp(out + O_REPLAY, replay, 8);
    wbe16(out + O_KDLEN, (uint16_t)kdlen);
}

/* A reply without nonce or key data (Message 4, group Message 2), MICed under the current KCK. */
static size_t reply(const struct wpa_sm *sm, const uint8_t *in, uint16_t ki, uint8_t *out)
{
    head(sm, in, (uint16_t)(sm->ver | ki), sm->replay, 0, out);
    mic_calc(sm, sm->ptk, out, O_KD, out + O_MIC);
    return O_KD;
}

/* Decipher an EAPOL-Key frame's key data under kek into kd: AES key wrap (version 2) or, under
 * version 1, RC4 keyed with the frame's IV and the KEK, the first 256 bytes of its stream dropped
 * (IEEE 802.11-2020 12.7.2). Returns the plaintext length, 0 when it does not check out. */
static unsigned key_data(const struct wpa_sm *sm, const uint8_t *kek, const uint8_t *in, unsigned kdlen,
                         uint8_t kd[128])
{
    if (sm->ver == 1) {
        uint8_t key[32];
        if (!kdlen || kdlen > 128)
            return 0;
        cp(key, in + O_IV, 16);
        cp(key + 16, kek, 16);
        cp(kd, in + O_KD, kdlen);
        wpa_rc4_skip(key, 32, 256, kd, kdlen);
        return kdlen;
    }
    if (kdlen < 16 || kdlen % 8 || kdlen > 128 + 8 || !wpa_aes_unwrap(kek, kdlen / 8 - 1, in + O_KD, kd))
        return 0;
    return kdlen - 8;
}

/* RSN: the GTK KDE out of encrypted key data, and under management frame protection the IGTK
 * KDE beside it, which must be there. */
static int unwrap_gtk(struct wpa_sm *sm, const uint8_t *kek, uint16_t ki, const uint8_t *in,
                      uint16_t kdlen)
{
    uint8_t kd[128];
    unsigned n;
    if (!(ki & KI_ENCRYPTED) || !(n = key_data(sm, kek, in, kdlen, kd)))
        return 0;
    if (sm->mfp && !extract_igtk(sm, kd, n))
        return 0;
    return wpa_extract_gtk(kd, n, sm->gtk, &sm->gtk_len, &sm->gtk_idx, &sm->gtk_tx);
}

/* The group keys before a frame's key data replaced them, to tell a new GTK or IGTK from the same
 * one sent again (group_new). */
struct group_keys {
    uint8_t gtk[32], igtk[16], ipn[6], gtk_set, igtk_set;
    unsigned gtk_len;
    int gtk_idx, igtk_idx;
};

static void group_save(const struct wpa_sm *sm, struct group_keys *o)
{
    cp(o->gtk, sm->gtk, 32), cp(o->igtk, sm->igtk, 16), cp(o->ipn, sm->ipn, 6);
    o->gtk_set = sm->gtk_set, o->igtk_set = sm->igtk_set;
    o->gtk_len = sm->gtk_len, o->gtk_idx = sm->gtk_idx, o->igtk_idx = sm->igtk_idx;
}

/* A frame that failed after its key data was read leaves the group keys as they were. */
static void group_restore(struct wpa_sm *sm, const struct group_keys *o)
{
    cp(sm->gtk, o->gtk, 32), cp(sm->igtk, o->igtk, 16), cp(sm->ipn, o->ipn, 6);
    sm->gtk_set = o->gtk_set, sm->igtk_set = o->igtk_set;
    sm->gtk_len = o->gtk_len, sm->gtk_idx = o->gtk_idx, sm->igtk_idx = o->igtk_idx;
}

/* The install bits for the group keys a verified frame brought: only a key other than the one
 * handed out before, as wpa_supplicant does. Installing the same key again would set its receive
 * counter back to the frame's Key RSC or IPN, so frames already received could be replayed (the
 * KRACK group-key class: an AP that resends group Message 1 or Message 3, or an attacker who
 * replays one inside the window). A new GTK takes the frame's Key RSC as its last packet number. */
static int group_new(struct wpa_sm *sm, const struct group_keys *o, const uint8_t *in)
{
    int r = 0;
    if (!o->gtk_set || o->gtk_len != sm->gtk_len || o->gtk_idx != sm->gtk_idx ||
        !eq(o->gtk, sm->gtk, sm->gtk_len)) {
        cp(sm->gtk_rsc, in + O_RSC, 6);
        r |= WPA_RX_INSTALL_GTK;
    }
    sm->gtk_set = 1;
    if (sm->mfp && (!o->igtk_set || o->igtk_idx != sm->igtk_idx || !eq(o->igtk, sm->igtk, 16)))
        r |= WPA_RX_INSTALL_IGTK;
    else
        cp(sm->ipn, o->ipn, 6); /* the same IGTK keeps the IPN it was installed with */
    return r;
}

/* WPA (descriptor 254) group Message 1: the GTK itself as key data, encrypted (RC4 under version
 * 1, AES key wrap under 2) although the Encrypted bit is RSN's only; its length in the key length
 * field and its key id in the key info. */
static int wpa1_gtk(struct wpa_sm *sm, uint16_t ki, const uint8_t *in, uint16_t kdlen)
{
    uint8_t kd[128];
    unsigned n = key_data(sm, sm->ptk + 16, in, kdlen, kd), len = be16(in + O_KEYLEN);
    if (!n || (len != 16 && len != 32) || n < len)
        return 0;
    cp(sm->gtk, kd, len);
    sm->gtk_len = len;
    sm->gtk_idx = (ki & KI_KEYIDX) >> 4;
    sm->gtk_tx = (ki & KI_INSTALL) != 0;
    return 1;
}

/* Michael MIC failure report (IEEE 802.11-2020 12.7.2): an EAPOL-Key frame with Error and Request
 * set, MICed under the KCK, its replay counter above every one used before (the AP checks the
 * requests' counters apart from its own). pairwise: the failure was under the pairwise key. */
size_t wpa_mic_failure(struct wpa_sm *sm, int pairwise, uint8_t *out)
{
    unsigned i;
    if (!sm->ptk_set)
        return 0;
    if (replay_newer(sm->rx_replay, sm->req_replay))
        cp(sm->req_replay, sm->rx_replay, 8);
    for (i = 8; i-- && !++sm->req_replay[i];) {}
    head(sm, 0, (uint16_t)(sm->ver | KI_MIC | KI_ERROR | KI_REQUEST | (pairwise ? KI_PAIRWISE : 0)),
         sm->req_replay, 0, out);
    mic_calc(sm, sm->ptk, out, O_KD, out + O_MIC);
    return O_KD;
}

int wpa_sm_rx(struct wpa_sm *sm, const uint8_t *in, size_t in_len, uint8_t *out, size_t *out_len)
{
    struct group_keys old;
    uint16_t ki, kdlen;
    int wpa1 = sm->proto == WPA_PROTO_WPA;
    if (in_len < O_KD || in[1] != 3 || in[4] != (wpa1 ? DESC_WPA : DESC_RSN)) /* EAPOL-Key */
        return WPA_RX_FAIL;
    ki = be16(in + O_KEYINFO);
    if ((ki & KI_VER) != sm->ver || !(ki & KI_ACK)) /* the version the negotiated cipher names */
        return WPA_RX_FAIL;
    kdlen = be16(in + O_KDLEN);
    if ((size_t)O_KD + kdlen > in_len)
        return WPA_RX_FAIL;
    /* A replayed frame is dropped unanswered: the counter must be above that of the last frame
     * whose MIC verified (IEEE 802.11-2020 12.7.2, as wpa_supplicant does). Only a verified frame
     * moves that counter: Message 1 has no MIC, so a forged one with a huge counter would otherwise
     * shut out the real handshake, and an AP that resends Message 1 with the same counter must
     * still get its Message 2. */
    if (sm->rx_replay_set && !replay_newer(in + O_REPLAY, sm->rx_replay))
        return WPA_RX_FAIL;

    if (!(ki & KI_PAIRWISE)) {
        /* ---- Group Message 1 (WPA's GTK after the 4-way, or a rekey): only once keyed, answered
         * with group Message 2. It verifies under the PTK in use, also while a PTK rekey waits for
         * its Message 3. ---- */
        group_save(sm, &old);
        if (!sm->ptk_set || !(ki & KI_MIC) || !mic_ok(sm, sm->ptk, in, in_len) ||
            !(wpa1 ? wpa1_gtk(sm, ki, in, kdlen) : unwrap_gtk(sm, sm->ptk + 16, ki, in, kdlen))) {
            group_restore(sm, &old);
            return WPA_RX_FAIL;
        }
        cp(sm->replay, in + O_REPLAY, 8);
        cp(sm->rx_replay, in + O_REPLAY, 8);
        *out_len = reply(sm, in, (uint16_t)(KI_MIC | KI_SECURE | (wpa1 ? ki & KI_KEYIDX : 0)), out);
        return WPA_RX_SEND | group_new(sm, &old, in);
    }

    if (!(ki & KI_MIC)) {
        /* ---- Message 1: ANonce in, build Message 2 ----
         * The new PTK waits in tptk until a MICed Message 3 proves it: Message 1 carries no MIC, so
         * a forged one must not replace the keys in use. A rekey (Message 1 while DONE) gets a fresh
         * SNonce derived from the old one and the new ANonce; a resent Message 1 of the same
         * handshake keeps the SNonce and takes the (possibly new) ANonce. Message 2 echoes this
         * Message 1's counter, which is not stored as verified, and carries the IE of the
         * association request (the AP compares the two). */
        if (sm->state == WPA_DONE)
            wpa_sha1_prf(sm->snonce, 32, "SoundTap SNonce", in + O_NONCE, 32, sm->snonce, 32);
        cp(sm->anonce, in + O_NONCE, 32);
        if (sm->akm == WPA_AKM_SAE || sm->akm == WPA_AKM_PSK_SHA256)
            wpa_pmk_to_ptk_sha256(sm->pmk, sm->aa, sm->spa, sm->anonce, sm->snonce, sm->tptk, sm->ptk_len);
        else
            wpa_pmk_to_ptk(sm->pmk, sm->aa, sm->spa, sm->anonce, sm->snonce, sm->tptk, sm->ptk_len);
        cp(sm->replay, in + O_REPLAY, 8);
        head(sm, in, (uint16_t)(sm->ver | KI_PAIRWISE | KI_MIC), sm->replay, sm->ielen, out);
        cp(out + O_NONCE, sm->snonce, 32);
        cp(out + O_KD, sm->ie, sm->ielen);
        mic_calc(sm, sm->tptk, out, O_KD + sm->ielen, out + O_MIC);
        sm->state = WPA_GOT_MSG1;
        *out_len = O_KD + sm->ielen;
        return WPA_RX_SEND;
    }

    /* ---- Message 3: verify MIC, take the GTK (RSN), build Message 4, install ---- */
    if (sm->state == WPA_DONE) {
        /* Our Message 4 got lost and the AP resends Message 3: answer again, install nothing
         * (reinstalling would reset the nonce, the KRACK class). */
        if (!mic_ok(sm, sm->ptk, in, in_len))
            return WPA_RX_FAIL;
        cp(sm->replay, in + O_REPLAY, 8);
        cp(sm->rx_replay, in + O_REPLAY, 8);
        *out_len = reply(sm, in, (uint16_t)(KI_PAIRWISE | KI_MIC | (ki & KI_SECURE)), out);
        return WPA_RX_SEND;
    }
    /* An RSN Message 3 always carries the GTK: key data that does not decipher or holds no GTK
     * fails the frame before any key is taken. WPA's carries its IE in the clear and the GTK
     * follows in a group handshake. */
    group_save(sm, &old);
    if (!eq(in + O_NONCE, sm->anonce, 32) || !(ki & KI_INSTALL) || !mic_ok(sm, sm->tptk, in, in_len) ||
        (!wpa1 && !unwrap_gtk(sm, sm->tptk + 16, ki, in, kdlen))) {
        group_restore(sm, &old);
        return WPA_RX_FAIL;
    }
    cp(sm->ptk, sm->tptk, sizeof sm->ptk);
    cp(sm->replay, in + O_REPLAY, 8);
    cp(sm->rx_replay, in + O_REPLAY, 8);
    sm->rx_replay_set = 1, sm->ptk_set = 1;
    *out_len = reply(sm, in, (uint16_t)(KI_PAIRWISE | KI_MIC | (ki & KI_SECURE)), out);
    sm->state = WPA_DONE;
    return WPA_RX_SEND | WPA_RX_INSTALL_PTK | (wpa1 ? 0 : group_new(sm, &old, in)) | WPA_RX_DONE;
}
