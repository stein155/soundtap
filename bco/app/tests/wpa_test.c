/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Known-answer + end-to-end tests for the WPA2-PSK 4-way supplicant (src/wpa.c).
 * Run via tests/run-all.sh. No hardware, no secrets: all vectors are public (FIPS-180,
 * RFC 2202, RFC 3394, IEEE 802.11i PRF and PBKDF2) and a self-built AP side drives a full
 * handshake. */
#include "../src/wpa.c"
/* Byte-wise AES-128 encrypt for the AP side of the tests; the BCO itself only decrypts. */
static void aes128_enc(const uint8_t key[16], const uint8_t in[16], uint8_t out[16])
{
    static const uint8_t sr[16] = {0, 5, 10, 15, 4, 9, 14, 3, 8, 13, 2, 7, 12, 1, 6, 11};
    uint8_t rk[176], s[16], t[16];
    unsigned round, i;
    aes128_expand(key, rk);
    for (i = 0; i < 16; i++)
        s[i] = in[i] ^ rk[i];
    for (round = 1; round <= 10; round++) {
        for (i = 0; i < 16; i++)
            t[i] = sbox[s[sr[i]]];
        for (i = 0; i < 16; i += 4) {
            uint8_t a0 = t[i], a1 = t[i + 1], a2 = t[i + 2], a3 = t[i + 3];
            if (round == 10)
                break;
            t[i] = mul(a0, 2) ^ mul(a1, 3) ^ a2 ^ a3;
            t[i + 1] = a0 ^ mul(a1, 2) ^ mul(a2, 3) ^ a3;
            t[i + 2] = a0 ^ a1 ^ mul(a2, 2) ^ mul(a3, 3);
            t[i + 3] = mul(a0, 3) ^ a1 ^ a2 ^ mul(a3, 2);
        }
        for (i = 0; i < 16; i++)
            s[i] = t[i] ^ rk[round * 16 + i];
    }
    cp(out, s, 16);
}
#include <stdio.h>
#include <string.h>

static int fails;
static void check(int ok, const char *name)
{
    printf("%-28s %s\n", name, ok ? "ok" : "FAIL");
    if (!ok)
        fails++;
}
static int hexeq(const uint8_t *a, const char *h)
{
    size_t i, n = strlen(h) / 2;
    for (i = 0; i < n; i++) {
        unsigned b;
        sscanf(h + 2 * i, "%2x", &b);
        if (a[i] != (uint8_t)b)
            return 0;
    }
    return 1;
}

/* aes128_enc, aes128_expand, sbox, mul, cp come from src/wpa.c (included above). */
static void aes_wrap(const uint8_t kek[16], unsigned n, const uint8_t *plain, uint8_t *cipher)
{
    uint8_t a[8], b[16], r[64 * 8];
    unsigned i, j, round;
    for (i = 0; i < 8; i++)
        a[i] = 0xa6;
    cp(r, plain, n * 8);
    for (round = 0; round < 6; round++)
        for (i = 1; i <= n; i++) {
            uint64_t t = (uint64_t)n * round + i;
            cp(b, a, 8);
            cp(b + 8, r + (i - 1) * 8, 8);
            aes128_enc(kek, b, b);
            cp(a, b, 8);
            for (j = 0; j < 8; j++)
                a[j] ^= (uint8_t)(t >> (56 - 8 * j));
            cp(r + (i - 1) * 8, b + 8, 8);
        }
    cp(cipher, a, 8);
    cp(cipher + 8, r, n * 8);
}

int main(void)
{
    uint8_t out[32];

    /* --- SHA-1 (FIPS 180, "abc") --- */
    wpa_sha1((const uint8_t *)"abc", 3, out);
    check(hexeq(out, "a9993e364706816aba3e25717850c26c9cd0d89d"), "sha1(abc)");

    /* --- HMAC-SHA1 (RFC 2202 case 1) --- */
    {
        uint8_t key[20];
        memset(key, 0x0b, 20);
        wpa_hmac_sha1(key, 20, (const uint8_t *)"Hi There", 8, out);
        check(hexeq(out, "b617318655057264e28bc0b6fb378c8ef146be00"), "hmac-sha1 rfc2202#1");
    }

    /* --- IEEE 802.11 PRF-512 (the PRF test vectors of the 802.11i amendment, cases 1 and 2;
     * prefix "prefix") --- */
    {
        uint8_t key[20], prf[64];
        memset(key, 0x0b, 20);
        wpa_sha1_prf(key, 20, "prefix", (const uint8_t *)"Hi There", 8, prf, 64);
        check(hexeq(prf, "bcd4c650b30b9684951829e0d75f9d54b862175ed9f00606e17d8da35402ffee"
                         "75df78c3d31e0f889f012120c0862beb67753e7439ae242edb8373698356cf5a"),
              "prf-512 802.11i#1");
        wpa_sha1_prf((const uint8_t *)"Jefe", 4, "prefix",
                     (const uint8_t *)"what do ya want for nothing?", 28, prf, 64);
        check(hexeq(prf, "51f4de5b33f249adf81aeb713a3c20f4fe631446fabdfa58244759ae58ef9009"
                         "a99abf4eac2ca5fa87e692c440eb40023e7babb206d61de7b92f41529092b8fc"),
              "prf-512 802.11i#2");
        wpa_sha1_prf((const uint8_t *)"Jefe", 4, "prefix",
                     (const uint8_t *)"what do ya want for nothing?", 28, prf, 48);
        check(hexeq(prf, "51f4de5b33f249adf81aeb713a3c20f4fe631446fabdfa58244759ae58ef9009"
                         "a99abf4eac2ca5fa87e692c440eb4002"),
              "prf-384 is its prefix");
    }

    /* --- AES-128 decrypt (FIPS-197 C.1 inverse) --- */
    {
        uint8_t k[16], ct[16];
        for (int i = 0; i < 16; i++)
            k[i] = i, ct[i] = 0;
        /* FIPS-197 C.1: enc(00112233...ff) under key 000102..0f = 69c4e0d86a7b0430d8cdb78070b4c55a */
        uint8_t pt[16] = {0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
                          0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff};
        aes128_enc(k, pt, ct);
        check(hexeq(ct, "69c4e0d86a7b0430d8cdb78070b4c55a"), "aes128 enc fips-197");
        wpa_aes128_dec(k, ct, out);
        check(hexeq(out, "00112233445566778899aabbccddeeff"), "aes128 dec fips-197");
    }

    /* --- RFC 3394 4.1 unwrap --- */
    {
        uint8_t kek[16], cipher[24], plain[16];
        for (int i = 0; i < 16; i++)
            kek[i] = i;
        uint8_t c[24] = {0x1f, 0xa6, 0x8b, 0x0a, 0x81, 0x12, 0xb4, 0x47, 0xae, 0xf3, 0x4b, 0xd8,
                         0xfb, 0x5a, 0x7b, 0x82, 0x9d, 0x3e, 0x86, 0x23, 0x71, 0xd2, 0xcf, 0xe5};
        memcpy(cipher, c, 24);
        int ok = wpa_aes_unwrap(kek, 2, cipher, plain);
        check(ok && hexeq(plain, "00112233445566778899aabbccddeeff"), "aes-unwrap rfc3394");
    }

    /* --- full 4-way: we are the supplicant, the test drives the AP --- */
    {
        struct wpa_sm sm;
        uint8_t pmk[32], aa[6] = {0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
                         spa[6] = {0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff};
        uint8_t anonce[32], snonce[32], ptk[48], m1[99], m2[WPA_EAPOL_MAX], m3[160], m4[WPA_EAPOL_MAX];
        uint8_t gtk[16];
        size_t olen;
        int i, r;

        for (i = 0; i < 32; i++)
            pmk[i] = i, anonce[i] = 0x40 + i, snonce[i] = 0x80 + i;
        for (i = 0; i < 16; i++)
            gtk[i] = 0xc0 + i;

        /* AP's expected PTK (same inputs -> same derivation) */
        wpa_pmk_to_ptk(pmk, aa, spa, anonce, snonce, ptk);

        wpa_sm_init(&sm, pmk, aa, spa, snonce);

        /* Message 1: ACK, no MIC, carries ANonce */
        memset(m1, 0, sizeof m1);
        m1[0] = 2, m1[1] = 3;
        m1[3] = 95;
        m1[4] = 2;
        m1[O_KEYINFO] = 0x00, m1[O_KEYINFO + 1] = 0x8a; /* ver2 | pairwise | ack */
        m1[O_KEYLEN] = 0, m1[O_KEYLEN + 1] = 16;
        m1[O_REPLAY + 7] = 1;
        memcpy(m1 + O_NONCE, anonce, 32);
        r = wpa_sm_rx(&sm, m1, sizeof m1, m2, &olen);
        check(r == WPA_RX_SEND, "msg1 -> send msg2");
        check(memcmp(sm.tptk, ptk, 48) == 0, "ptk derived == AP ptk");
        check(memcmp(m2 + O_NONCE, snonce, 32) == 0, "msg2 carries snonce");
        check(be16(m2 + O_KEYINFO) == (2 | KI_PAIRWISE | KI_MIC), "msg2 key info");
        /* AP verifies msg2 MIC under KCK */
        {
            uint8_t v[20], saved[16];
            memcpy(saved, m2 + O_MIC, 16);
            memset(m2 + O_MIC, 0, 16);
            wpa_hmac_sha1(ptk, 16, m2, olen, v);
            check(memcmp(v, saved, 16) == 0, "msg2 MIC verifies");
            memcpy(m2 + O_MIC, saved, 16);
        }

        /* Message 3: AP wraps a GTK KDE under KEK, MICs the frame */
        {
            uint8_t kde[24], wrapped[32];
            unsigned kdlen, total;
            uint8_t mic[20];
            memset(kde, 0, sizeof kde);
            /* GTK KDE: dd len 00-0f-ac 01 keyid res GTK(16) ; data len = 6+16 = 22 -> kde len 22 */
            kde[0] = 0xdd;
            kde[1] = 22;
            kde[2] = 0x00, kde[3] = 0x0f, kde[4] = 0xac, kde[5] = 0x01;
            kde[6] = 2; /* key id 2, tx 0 */
            memcpy(kde + 8, gtk, 16);
            /* pad plaintext to 8-byte multiple (24 already multiple of 8) -> n=3 */
            aes_wrap(ptk + 16, 3, kde, wrapped); /* 24 in -> 32 out */
            kdlen = 32;
            total = O_KD + kdlen;
            memset(m3, 0, sizeof m3);
            m3[0] = 2, m3[1] = 3;
            m3[2] = (uint8_t)((total - 4) >> 8), m3[3] = (uint8_t)(total - 4);
            m3[4] = 2;
            wbe16(m3 + O_KEYINFO, 2 | KI_PAIRWISE | KI_MIC | KI_ACK | KI_INSTALL | KI_SECURE |
                                      KI_ENCRYPTED);
            m3[O_KEYLEN] = 0, m3[O_KEYLEN + 1] = 16;
            m3[O_REPLAY + 7] = 2;
            memcpy(m3 + O_NONCE, anonce, 32);
            wbe16(m3 + O_KDLEN, kdlen);
            memcpy(m3 + O_KD, wrapped, kdlen);
            wpa_hmac_sha1(ptk, 16, m3, total, mic);
            memcpy(m3 + O_MIC, mic, 16);

            /* A GTK that fails the key-wrap integrity check under a valid outer MIC
             * fails the frame; no key is taken and no Message 4 goes out */
            {
                uint8_t bad[sizeof m3];
                memcpy(bad, m3, total);
                bad[O_KD + 9] ^= 1;
                memset(bad + O_MIC, 0, 16);
                wpa_hmac_sha1(ptk, 16, bad, total, mic);
                memcpy(bad + O_MIC, mic, 16);
                r = wpa_sm_rx(&sm, bad, total, m4, &olen);
                check(r == WPA_RX_FAIL && sm.state == WPA_GOT_MSG1 && sm.ptk[0] == 0 &&
                          memcmp(sm.ptk, sm.ptk + 1, 47) == 0,
                      "msg3 with a corrupt wrapped GTK fails, nothing installed");
            }

            r = wpa_sm_rx(&sm, m3, total, m4, &olen);
        }
        check((r & WPA_RX_SEND) && (r & WPA_RX_INSTALL_PTK) && (r & WPA_RX_INSTALL_GTK) &&
                  (r & WPA_RX_DONE),
              "msg3 -> msg4 + install both");
        check(sm.gtk_len == 16 && memcmp(sm.gtk, gtk, 16) == 0, "gtk unwrapped == AP gtk");
        check(sm.gtk_idx == 2, "gtk key id");
        /* AP verifies msg4 MIC */
        {
            uint8_t v[20], saved[16];
            memcpy(saved, m4 + O_MIC, 16);
            memset(m4 + O_MIC, 0, 16);
            wpa_hmac_sha1(ptk, 16, m4, olen, v);
            check(memcmp(v, saved, 16) == 0, "msg4 MIC verifies");
        }
        check(memcmp(sm.ptk, ptk, 48) == 0, "ptk taken after msg3");

        /* msg3 resent (our msg4 lost): answered again, nothing reinstalled; a replay is dropped */
        {
            unsigned total = be16(m3 + 2) + 4;
            uint8_t mic[20];
            r = wpa_sm_rx(&sm, m3, total, m4, &olen);
            check(r == WPA_RX_FAIL, "replayed msg3 dropped");
            m3[O_REPLAY + 7] = 3;
            memset(m3 + O_MIC, 0, 16);
            wpa_hmac_sha1(ptk, 16, m3, total, mic);
            memcpy(m3 + O_MIC, mic, 16);
            r = wpa_sm_rx(&sm, m3, total, m4, &olen);
            check(r == WPA_RX_SEND, "resent msg3 -> msg4 only");
        }

        /* group rekey: group msg1 with a new GTK at key id 1 -> group msg2 + install */
        {
            uint8_t kde[24], wrapped[32], g1[160], g2[WPA_EAPOL_MAX], mic[20];
            unsigned total = O_KD + 32;
            memset(kde, 0, sizeof kde);
            kde[0] = 0xdd, kde[1] = 22, kde[2] = 0x00, kde[3] = 0x0f, kde[4] = 0xac, kde[5] = 0x01;
            kde[6] = 1;
            for (i = 0; i < 16; i++)
                kde[8 + i] = 0xe0 + i;
            aes_wrap(ptk + 16, 3, kde, wrapped);
            memset(g1, 0, sizeof g1);
            g1[0] = 2, g1[1] = 3, g1[2] = (uint8_t)((total - 4) >> 8), g1[3] = (uint8_t)(total - 4);
            g1[4] = 2;
            wbe16(g1 + O_KEYINFO, 2 | KI_MIC | KI_ACK | KI_SECURE | KI_ENCRYPTED);
            g1[O_KEYLEN + 1] = 16;
            g1[O_REPLAY + 7] = 4;
            wbe16(g1 + O_KDLEN, 32);
            memcpy(g1 + O_KD, wrapped, 32);
            wpa_hmac_sha1(ptk, 16, g1, total, mic);
            memcpy(g1 + O_MIC, mic, 16);
            r = wpa_sm_rx(&sm, g1, total, g2, &olen);
            check(r == (WPA_RX_SEND | WPA_RX_INSTALL_GTK), "group msg1 -> msg2 + gtk");
            check(sm.gtk_idx == 1 && sm.gtk[0] == 0xe0, "rekeyed gtk/idx");
            check(be16(g2 + O_KEYINFO) == (2 | KI_MIC | KI_SECURE) && g2[O_REPLAY + 7] == 4,
                  "group msg2 key info/replay");
            {
                uint8_t v[20], saved[16];
                memcpy(saved, g2 + O_MIC, 16);
                memset(g2 + O_MIC, 0, 16);
                wpa_hmac_sha1(ptk, 16, g2, olen, v);
                check(memcmp(v, saved, 16) == 0, "group msg2 MIC verifies");
            }
            r = wpa_sm_rx(&sm, g1, total, g2, &olen);
            check(r == WPA_RX_FAIL, "replayed group msg1 dropped");
        }

        /* a forged msg1 (no MIC) while keyed must not change the keys in use */
        m1[O_REPLAY + 7] = 5;
        r = wpa_sm_rx(&sm, m1, sizeof m1, m2, &olen);
        check(r == WPA_RX_SEND && memcmp(sm.ptk, ptk, 48) == 0, "rekey msg1 keeps ptk");
        check(memcmp(sm.snonce, snonce, 32) != 0, "rekey gets a fresh snonce");
    }

    /* --- a corrupted MIC on msg3 is rejected --- */
    {
        struct wpa_sm sm;
        uint8_t pmk[32], aa[6] = {1, 2, 3, 4, 5, 6}, spa[6] = {7, 8, 9, 10, 11, 12};
        uint8_t anonce[32], snonce[32], m1[99], m2[WPA_EAPOL_MAX], m3[120], m4[WPA_EAPOL_MAX];
        size_t olen;
        int i, r;
        for (i = 0; i < 32; i++)
            pmk[i] = i, anonce[i] = i, snonce[i] = 0x90 + i;
        wpa_sm_init(&sm, pmk, aa, spa, snonce);
        memset(m1, 0, sizeof m1);
        m1[0] = 2, m1[1] = 3, m1[3] = 95, m1[4] = 2;
        wbe16(m1 + O_KEYINFO, 2 | KI_PAIRWISE | KI_ACK);
        m1[O_KEYLEN + 1] = 16;
        memcpy(m1 + O_NONCE, anonce, 32);
        wpa_sm_rx(&sm, m1, sizeof m1, m2, &olen);
        memset(m3, 0, sizeof m3);
        m3[0] = 2, m3[1] = 3, m3[3] = 95, m3[4] = 2;
        wbe16(m3 + O_KEYINFO, 2 | KI_PAIRWISE | KI_MIC | KI_ACK | KI_SECURE);
        m3[O_KEYLEN + 1] = 16;
        memcpy(m3 + O_NONCE, anonce, 32);
        wbe16(m3 + O_KDLEN, 0); /* bogus/zero MIC */
        m3[O_REPLAY + 7] = 1;
        r = wpa_sm_rx(&sm, m3, O_KD, m4, &olen);
        check(r == WPA_RX_FAIL, "msg3 bad MIC rejected");
    }

    {   /* PBKDF2 PMK known-answer (IEEE 802.11i test vector: "password" / "IEEE"). */
        uint8_t pmk[32];
        wpa_pbkdf2((const uint8_t *)"password", 8, (const uint8_t *)"IEEE", 4, pmk);
        check(hexeq(pmk, "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e"),
              "pbkdf2 pmk password/IEEE");
    }

    {   /* GTK KDE after the RSN IE: the walk must skip 0x30 and still find 0xdd (regression: the
         * old walk broke on the first non-0xdd byte and never installed the group key). */
        uint8_t kd[64], gtk[32];
        unsigned gl = 0, i;
        int gi = -1, gt = -1;
        /* RSN IE (0x30 len=20 ...) then GTK KDE: dd 16 00 0f ac 01 keyid=1 res 16xGTK, then pad */
        memset(kd, 0, sizeof kd);
        kd[0] = 0x30, kd[1] = 20;             /* RSN IE, 20 bytes body */
        unsigned p = 2 + 20;
        kd[p] = 0xdd, kd[p + 1] = 22;         /* GTK KDE, 22 bytes body */
        kd[p + 2] = 0x00, kd[p + 3] = 0x0f, kd[p + 4] = 0xac, kd[p + 5] = 0x01;
        kd[p + 6] = 0x01;                     /* key id 1, tx 0 */
        for (i = 0; i < 16; i++)
            kd[p + 8 + i] = 0xa0 + i;         /* the 16-byte GTK */
        check(wpa_extract_gtk(kd, 64, gtk, &gl, &gi, &gt) == 1, "gtk found after rsn ie");
        check(gl == 16 && gi == 1, "gtk len/idx correct");
        check(gtk[0] == 0xa0 && gtk[15] == 0xaf, "gtk bytes correct");
        /* key data with only an RSN IE and no GTK KDE -> not found */
        memset(kd, 0, sizeof kd);
        kd[0] = 0x30, kd[1] = 20;
        gl = 99;
        check(wpa_extract_gtk(kd, 64, gtk, &gl, &gi, &gt) == 0, "no gtk when kde absent");
    }

    {   /* FIPS-197 AES-128 known-answer, to anchor aes128_enc (CCM's only primitive). */
        static const uint8_t k[16] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15};
        static const uint8_t pt[16] = {0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
                                       0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff};
        uint8_t ct[16];
        aes128_enc(k, pt, ct);
        check(hexeq(ct, "69c4e0d86a7b0430d8cdb78070b4c55a"), "aes128 fips-197 kat");
    }

    printf(fails ? "\n%d FAILED\n" : "\nall passed\n", fails);
    return fails ? 1 : 0;
}
