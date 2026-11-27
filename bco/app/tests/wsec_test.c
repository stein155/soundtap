/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* WEP and TKIP in software (src/wsec.c, ADR 0136) against known answers: RC4 (the classic
 * 0123456789abcdef vector and RFC 6229), CRC-32 (the standard check value), Michael (the IEEE 802.11
 * chained vectors, as the kernel's crypto self-test carries them), TKIP's key mixing (IEEE 802.11's
 * test vectors 1 and 2), and whole WEP-40, WEP-104 and TKIP bodies computed by an independent
 * reference (Python's zlib CRC-32 and an RC4, Michael and key mixing transcribed from Linux's
 * mac80211). Then the failures: a flipped bit fails the ICV, a wrong Michael key fails the MIC with
 * the ICV good (the case that counts for the countermeasures), and malformed headers are refused.
 * No hardware. */
#include <assert.h>
#include <stdio.h>
#include <string.h>

#include "../src/wpa.c"
#include "../src/wsec.c"

static void hex_eq(const uint8_t *a, const uint8_t *b, unsigned n, const char *what)
{
    if (memcmp(a, b, n)) {
        printf("%s mismatch\n", what);
        for (unsigned i = 0; i < n; i++)
            printf("%02x", a[i]);
        printf("\n");
        assert(0);
    }
}

static void rc4_vectors(void)
{
    const uint8_t k1[8] = {0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef};
    uint8_t b1[8];
    const uint8_t c1[8] = {0x75, 0xb7, 0x87, 0x80, 0x99, 0xe0, 0xc5, 0x96};
    memcpy(b1, k1, 8);
    wsec_rc4(k1, 8, b1, 8);
    hex_eq(b1, c1, 8, "rc4 classic");
    /* RFC 6229, 40-bit key 0102030405, keystream offset 0 */
    const uint8_t k2[5] = {1, 2, 3, 4, 5};
    const uint8_t c2[16] = {0xb2, 0x39, 0x63, 0x05, 0xf0, 0x3d, 0xc0, 0x27,
                            0xcc, 0xc3, 0x52, 0x4a, 0x0a, 0x11, 0x18, 0xa8};
    uint8_t b2[16] = {0};
    wsec_rc4(k2, 5, b2, 16);
    hex_eq(b2, c2, 16, "rc4 rfc6229");
    printf("rc4 ok\n");
}

static void crc_vectors(void)
{
    assert(wsec_crc32((const uint8_t *)"123456789", 9) == 0xcbf43926u);
    assert(wsec_crc32((const uint8_t *)"", 0) == 0);
    printf("crc32 ok\n");
}

static void michael_vectors(void)
{
    static const struct { const char *key, *msg, *mic; } v[] = {
        {"\x00\x00\x00\x00\x00\x00\x00\x00", "", "\x82\x92\x5c\x1c\xa1\xd1\x30\xb8"},
        {"\x82\x92\x5c\x1c\xa1\xd1\x30\xb8", "M", "\x43\x47\x21\xca\x40\x63\x9b\x3f"},
        {"\x43\x47\x21\xca\x40\x63\x9b\x3f", "Mi", "\xe8\xf9\xbe\xca\xe9\x7e\x5d\x29"},
        {"\xe8\xf9\xbe\xca\xe9\x7e\x5d\x29", "Mic", "\x90\x03\x8f\xc6\xcf\x13\xc1\xdb"},
        {"\x90\x03\x8f\xc6\xcf\x13\xc1\xdb", "Mich", "\xd5\x5e\x10\x05\x10\x12\x89\x86"},
        {"\xd5\x5e\x10\x05\x10\x12\x89\x86", "Michael", "\x0a\x94\x2b\x12\x4e\xca\xa5\x46"},
    };
    for (unsigned i = 0; i < sizeof v / sizeof v[0]; i++) {
        uint8_t mic[8];
        wsec_michael_raw((const uint8_t *)v[i].key, (const uint8_t *)v[i].msg,
                         (unsigned)strlen(v[i].msg), mic);
        hex_eq(mic, (const uint8_t *)v[i].mic, 8, "michael");
    }
    /* the header form is the bare form over DA, SA, priority and three zero bytes */
    const uint8_t key[8] = {1, 2, 3, 4, 5, 6, 7, 8}, da[6] = {9, 9, 9, 9, 9, 9}, sa[6] = {7, 7, 7, 7, 7, 7};
    uint8_t msg[16 + 5], a[8], b[8];
    memcpy(msg, da, 6), memcpy(msg + 6, sa, 6), memset(msg + 12, 0, 4), memcpy(msg + 16, "hello", 5);
    wsec_michael(key, da, sa, 0, (const uint8_t *)"hello", 5, a);
    wsec_michael_raw(key, msg, sizeof msg, b);
    hex_eq(a, b, 8, "michael header form");
    printf("michael ok\n");
}

static void tkip_mix_vectors(void)
{
    const uint8_t tk[16] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15};
    const uint8_t ta[6] = {0x10, 0x22, 0x33, 0x44, 0x55, 0x66};
    const uint8_t k0[16] = {0x00, 0x20, 0x00, 0x33, 0xea, 0x8d, 0x2f, 0x60,
                            0xca, 0x6d, 0x13, 0x74, 0x23, 0x4a, 0x66, 0x0b};
    const uint8_t k1[16] = {0x00, 0x20, 0x01, 0x90, 0xff, 0xdc, 0x31, 0x43,
                            0x89, 0xa9, 0xd9, 0xd0, 0x74, 0xfd, 0x20, 0xaa};
    uint8_t key[16];
    wsec_tkip_mix(tk, ta, 0, key);
    hex_eq(key, k0, 16, "tkip mix vector 1");
    wsec_tkip_mix(tk, ta, 1, key);
    hex_eq(key, k1, 16, "tkip mix vector 2");
    printf("tkip key mixing ok\n");
}

static const uint8_t SNAP_IP[8] = {0xaa, 0xaa, 0x03, 0, 0, 0, 0x08, 0x00};

static void wep_vectors(void)
{
    static const uint8_t k40[5] = {1, 2, 3, 4, 5};
    static const uint8_t k104[13] = {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13};
    static const uint8_t c40[36] = {
        0x0c, 0x0b, 0x0a, 0x80, 0x29, 0x85, 0x3e, 0xce, 0x92, 0x60, 0x13, 0xe4, 0xba, 0x67, 0xcb, 0xc6,
        0x26, 0x29, 0xaa, 0x72, 0x24, 0xed, 0x0b, 0x4c, 0x30, 0xb0, 0x61, 0x15, 0x7e, 0x3c, 0x22, 0x1d,
        0xb8, 0xbe, 0x7c, 0xe9};
    static const uint8_t c104[36] = {
        0x0c, 0x0b, 0x0a, 0x80, 0x22, 0xaa, 0x16, 0xbc, 0x05, 0xc8, 0x88, 0x2c, 0xc6, 0xb0, 0x06, 0xf3,
        0x93, 0x8f, 0x91, 0x7e, 0xa8, 0xc2, 0xcd, 0x1f, 0x3a, 0xb6, 0x55, 0x61, 0x08, 0xd1, 0x38, 0x75,
        0x7f, 0x6b, 0x46, 0xe8};
    const struct { const uint8_t *key, *ct; unsigned klen; } v[2] = {{k40, c40, 5}, {k104, c104, 13}};
    for (unsigned t = 0; t < 2; t++) {
        uint8_t f[64], plain[28];
        memcpy(plain, SNAP_IP, 8);
        for (unsigned i = 0; i < 20; i++)
            plain[8 + i] = (uint8_t)i;
        memcpy(f + 4, plain, sizeof plain);
        assert(wsec_wep_protect(v[t].key, v[t].klen, 2, 0x0a0b0c, f, sizeof plain) == 36);
        hex_eq(f, v[t].ct, 36, "wep protect");
        assert(wsec_keyid(f) == 2 && !wsec_extiv(f));
        assert(wsec_wep_open(v[t].key, v[t].klen, f, 36) == (int)sizeof plain);
        hex_eq(f + 4, plain, sizeof plain, "wep open");
        /* a flipped bit: the ICV fails */
        memcpy(f, v[t].ct, 36);
        f[10] ^= 0x01;
        assert(wsec_wep_open(v[t].key, v[t].klen, f, 36) == WSEC_ICV_FAIL);
        /* the wrong key: the ICV fails too */
        memcpy(f, v[t].ct, 36);
        assert(wsec_wep_open(k40, 5, f, 36) == (t ? WSEC_ICV_FAIL : (int)sizeof plain));
    }
    uint8_t f[16] = {0};
    assert(wsec_wep_open(k40, 5, f, 7) == WSEC_BAD);      /* shorter than IV and ICV */
    f[3] = 0x20;
    assert(wsec_wep_open(k40, 5, f, 16) == WSEC_BAD);     /* an ExtIV header is TKIP's */
    assert(wsec_wep_open(k40, 0, f, 16) == WSEC_BAD);
    printf("wep ok\n");
}

static void tkip_vectors(void)
{
    static const uint8_t tk[16] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15};
    static const uint8_t mic_key[8] = {0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff};
    static const uint8_t ta[6] = {0x10, 0x22, 0x33, 0x44, 0x55, 0x66};
    static const uint8_t da[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x01};
    static const uint8_t want[60] = {
        0x78, 0x78, 0x9a, 0x60, 0x56, 0x34, 0x12, 0x00, 0xe8, 0x7d, 0x52, 0xd8, 0xd5, 0xd0, 0x4a, 0xe0,
        0x48, 0x9b, 0x5a, 0xc9, 0xaf, 0xf4, 0x8e, 0xe7, 0xf0, 0x9f, 0x18, 0x39, 0x9d, 0x15, 0x52, 0x0a,
        0xb5, 0xc3, 0x2c, 0x90, 0x10, 0xf3, 0xd3, 0x21, 0x41, 0xf3, 0xed, 0xf8, 0xfb, 0x9e, 0x34, 0xa1,
        0x9a, 0x61, 0x1d, 0xf7, 0x35, 0x78, 0xfb, 0x95, 0x79, 0xd0, 0x87, 0x7f};
    uint8_t msdu[40], f[80];
    memcpy(msdu, SNAP_IP, 8);
    for (unsigned i = 0; i < 32; i++)
        msdu[8 + i] = (uint8_t)i;
    const uint64_t tsc = 0x123456789aull;
    memcpy(f + 8, msdu, sizeof msdu);
    assert(wsec_tkip_protect(tk, mic_key, 1, tsc, ta, da, ta, f, sizeof msdu) == 60);
    hex_eq(f, want, 60, "tkip protect");
    assert(wsec_keyid(f) == 1 && wsec_extiv(f) && wsec_tkip_tsc(f) == tsc);
    assert(wsec_tkip_open(tk, mic_key, ta, da, ta, f, 60) == (int)sizeof msdu);
    hex_eq(f + 8, msdu, sizeof msdu, "tkip open");
    /* a flipped ciphertext bit: the ICV fails (no MIC failure: it does not count) */
    memcpy(f, want, 60);
    f[20] ^= 0x80;
    assert(wsec_tkip_open(tk, mic_key, ta, da, ta, f, 60) == WSEC_ICV_FAIL);
    /* another TSC in the header: another RC4 key, the ICV fails */
    memcpy(f, want, 60);
    f[2] ^= 1;
    assert(wsec_tkip_open(tk, mic_key, ta, da, ta, f, 60) == WSEC_ICV_FAIL);
    /* the ICV holds but the MIC does not (a forged frame from someone with the TK, or another
     * DA): this is the failure the countermeasures count */
    static const uint8_t wrong[8] = {0};
    memcpy(f + 8, msdu, sizeof msdu);
    wsec_tkip_protect(tk, wrong, 1, tsc, ta, da, ta, f, sizeof msdu);
    assert(wsec_tkip_open(tk, mic_key, ta, da, ta, f, 60) == WSEC_MIC_FAIL);
    memcpy(f, want, 60);
    static const uint8_t other_da[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x02};
    assert(wsec_tkip_open(tk, mic_key, ta, other_da, ta, f, 60) == WSEC_MIC_FAIL);
    /* malformed */
    memcpy(f, want, 60);
    assert(wsec_tkip_open(tk, mic_key, ta, da, ta, f, 19) == WSEC_BAD);
    f[3] &= ~0x20;
    assert(wsec_tkip_open(tk, mic_key, ta, da, ta, f, 60) == WSEC_BAD);
    /* an empty MSDU still carries MIC and ICV */
    assert(wsec_tkip_protect(tk, mic_key, 0, 1, ta, da, ta, f, 0) == 20);
    assert(wsec_tkip_open(tk, mic_key, ta, da, ta, f, 20) == 0);
    printf("tkip ok\n");
}

/* CCMP in software for protected management frames and BIP-CMAC-128 (PMF, ADR 0136), against
 * bodies computed by an independent reference (pyca/cryptography's AES-CCM and AES-CMAC with the
 * nonce, AAD and MMIE of IEEE 802.11-2020 12.5.3 and 12.5.4, as Linux's mac80211 builds them). */
static void pmf_vectors(void)
{
    static const uint8_t a1[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55}, a2[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x01};
    static const uint8_t want_deauth[18] = {0x05, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x01, 0x64,
                                            0xea, 0xe0, 0x7b, 0xa3, 0xd8, 0x72, 0x1d, 0x1f, 0x45};
    static const uint8_t want_saq[20] = {0x06, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x00, 0x97, 0x1d,
                                         0x31, 0xa2, 0xdf, 0x43, 0xce, 0x3a, 0x53, 0x29, 0xe1, 0xd7};
    static const uint8_t want_bip[20] = {0x03, 0x00, 0x4c, 0x10, 0x04, 0x00, 0xa1, 0x00, 0x00, 0x00,
                                         0x00, 0x00, 0xa9, 0xe9, 0x00, 0x8a, 0x58, 0x7b, 0x19, 0x57};
    uint8_t tk[16], igtk[16], hdr[24], f[64];
    for (unsigned i = 0; i < 16; i++)
        tk[i] = (uint8_t)(0x40 + i), igtk[i] = (uint8_t)(0x60 + i);
    /* a protected deauthentication, reason 7, PN 0x010000000005 */
    memset(hdr, 0, sizeof hdr);
    hdr[0] = 0xc0, hdr[1] = 0x40;
    memcpy(hdr + 4, a1, 6), memcpy(hdr + 10, a2, 6), memcpy(hdr + 16, a2, 6);
    hdr[22] = 0x30, hdr[23] = 0x12;
    f[8] = 7, f[9] = 0;
    assert(wsec_ccmp_protect(tk, hdr, 0x010000000005ull, f, 2) == 18);
    hex_eq(f, want_deauth, 18, "ccmp deauth");
    assert(wsec_ccmp_pn(f) == 0x010000000005ull);
    hdr[1] |= 0x08; /* a retry: the AAD masks the bit, the frame still opens */
    assert(wsec_ccmp_open(tk, hdr, f, 18) == 2 && f[8] == 7);
    memcpy(f, want_deauth, 18);
    f[9] ^= 1;
    assert(wsec_ccmp_open(tk, hdr, f, 18) == WSEC_MIC_FAIL);
    memcpy(f, want_deauth, 18);
    hdr[22] ^= 0x10; /* another sequence number: masked, still opens */
    assert(wsec_ccmp_open(tk, hdr, f, 18) == 2);
    memcpy(f, want_deauth, 18);
    hdr[4] ^= 1;     /* another receiver: the AAD differs */
    assert(wsec_ccmp_open(tk, hdr, f, 18) == WSEC_MIC_FAIL);
    hdr[4] ^= 1;
    assert(wsec_ccmp_open(tk, hdr, f, 15) == WSEC_BAD);
    /* an SA Query request, transaction 0x1234, PN 6 */
    hdr[0] = 0xd0, hdr[1] = 0x40, hdr[22] = 0x40;
    f[8] = 8, f[9] = 0, f[10] = 0x34, f[11] = 0x12;
    assert(wsec_ccmp_protect(tk, hdr, 6, f, 4) == 20);
    hex_eq(f, want_saq, 20, "ccmp sa query");
    /* BIP: a deauthentication to everybody, reason 3, key id 4, IPN 0xa1 */
    uint8_t body[2 + WSEC_MMIE_LEN] = {3, 0};
    memset(hdr, 0, sizeof hdr);
    hdr[0] = 0xc0;
    memset(hdr + 4, 0xff, 6), memcpy(hdr + 10, a2, 6), memcpy(hdr + 16, a2, 6);
    hdr[22] = 0x50, hdr[23] = 0x12;
    assert(wsec_bip_protect(igtk, 4, 0xa1, hdr, body, 2) == 20);
    hex_eq(body, want_bip, 20, "bip mmie");
    unsigned keyid = 0;
    uint64_t ipn = 0;
    assert(wsec_bip_check(igtk, hdr, body, 20, &keyid, &ipn) && keyid == 4 && ipn == 0xa1);
    hdr[1] = 0x08; /* Retry masked */
    assert(wsec_bip_check(igtk, hdr, body, 20, &keyid, &ipn));
    body[0] = 4;   /* another reason: the MIC fails */
    assert(!wsec_bip_check(igtk, hdr, body, 20, &keyid, &ipn));
    body[0] = 3, body[2] = 0xdd;
    assert(!wsec_bip_check(igtk, hdr, body, 20, &keyid, &ipn)); /* not an MMIE */
    assert(!wsec_bip_check(igtk, hdr, body, 17, &keyid, &ipn));
    printf("ccmp and bip ok\n");
}

int main(void)
{
    rc4_vectors();
    crc_vectors();
    michael_vectors();
    tkip_mix_vectors();
    wep_vectors();
    tkip_vectors();
    pmf_vectors();
    printf("wsec: all passed\n");
    return 0;
}
