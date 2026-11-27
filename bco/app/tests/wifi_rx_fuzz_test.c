/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Fuzz the receive paths of the real wifi.c (run-all.sh builds this one under ASan and UBSan, and
 * without PIE, as wifi_join_test.c): rx_station (the station's management and data frames),
 * rx_ap (the setup access point's probe, authentication, association and data frames) and
 * rx_data (LLC/SNAP, EAPOL, the IP length cut), then the supplicant on whatever EAPOL got through.
 * Frames are built close to valid (descriptor markers, our addresses, LLC/SNAP and EAPOL headers)
 * so the fuzz gets past the first checks, then mutated and cut at random lengths. Every frame sits
 * in a heap block of exactly its length, so any read past it is caught. The seed is fixed: a
 * failure repeats. air_fuzz_test.c covers the parsers below these (scan table, rates, 4-way).
 * Nothing here talks to a speaker. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>

#pragma GCC diagnostic ignored "-Wpointer-to-int-cast"
#pragma GCC diagnostic ignored "-Wint-to-pointer-cast"
/* wifi.c brings its own memcpy/memset for the freestanding build; keep the C library's here. */
#define memcpy wifi_memcpy
#define memset wifi_memset
#include "../src/wifi.c"
#undef memcpy
#undef memset
#include "../src/wpa.c"
#include "../src/wsec.c"

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

static unsigned to_scm, to_scm_bytes;
void netcard_from_port(unsigned ch, const unsigned char *f, unsigned n)
{
    volatile unsigned char sink = 0;
    CHECK(ch == BCOLINK_CH_WIFI && n >= 14 && n <= 14 + 1500, "frame to the SCM: %u bytes", n);
    for (unsigned i = 0; i < n; i++) /* every byte handed on is readable */
        sink ^= f[i];
    (void)sink;
    to_scm++, to_scm_bytes += n;
}

__attribute__((constructor)) static void map_wreg(void)
{
    static int probe;
    /* the WiLD registers: the doorbell the AP's peer commands and the key commands ring; and the
     * UART page: a Michael MIC failure says so in one line (board.h say) */
    static const unsigned long at[2] = {(unsigned long)WREG, 0xb1001000ul};
    for (int i = 0; i < 2; i++)
        if (mmap((void *)at[i], 0x1000, PROT_READ | PROT_WRITE,
                 MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0) != (void *)at[i]) {
            perror("wifi_rx_fuzz: map the WiLD registers and the UART");
            exit(2);
        }
    if ((uintptr_t)&probe >> 32) {
        fputs("wifi_rx_fuzz: globals above 4 GiB; build with -no-pie\n", stderr);
        exit(2);
    }
}

static uint32_t fz = 0x5eed0bc0u;
static uint32_t rnd(void)
{
    fz ^= fz << 13, fz ^= fz >> 17, fz ^= fz << 5;
    return fz;
}

static void mutate(unsigned char *b, unsigned len, unsigned flips)
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
static unsigned char *exact(const unsigned char *src, unsigned len)
{
    unsigned char *p = malloc(len ? len : 1);
    if (len)
        memcpy(p, src, len);
    return p;
}

/* The command ring and the transmit buffers, fresh: the fuzz never runs out of either. */
static unsigned cmdbuf[512];
static unsigned char txmem[NTXFBUF * TXFBUF] __attribute__((aligned(8)));
static unsigned frmbuf[28 * 6];
static void rings_fresh(void)
{
    memset(cmdbuf, 0, sizeof cmdbuf);
    wr_init(&tx, 0, (uintptr_t)cmdbuf, sizeof cmdbuf / 4, 4, 2);
    memset(frmbuf, 0, sizeof frmbuf);
    wr_init(&frm[2], 0, (uintptr_t)frmbuf, 28, 0x18, 5);
    txfbase = (unsigned)(uintptr_t)txmem;
    for (txnfree = 0; txnfree < NTXFBUF; txnfree++)
        txfbuf[txnfree] = txfbase + txnfree * TXFBUF, txbusy[txnfree] = 0;
}

static const unsigned char OWN[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55};
static const unsigned char BSS[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x01};

/* One of: our address, the broadcast address, the BSS, a client of ours, anything. */
static void addr(unsigned char *a, const unsigned char *peer)
{
    switch (rnd() % 6) {
    case 0:
    case 1:
        memcpy(a, OWN, 6);
        break;
    case 2:
        memset(a, 0xff, 6);
        break;
    case 3:
        memcpy(a, peer, 6);
        break;
    case 4:
        memcpy(a, BSS, 6), a[5] = rnd() % 8; /* a client: 02:aa:bb:cc:dd:0x */
        break;
    default:
        for (unsigned i = 0; i < 6; i++)
            a[i] = rnd();
    }
}

/* A body: LLC/SNAP with EAPOL, IPv4 or IPv6, IEs (rates, SSID), or noise. */
static unsigned body(unsigned char *b, unsigned room)
{
    unsigned n = rnd() % room, k = rnd() % 5;
    for (unsigned i = 0; i < n; i++)
        b[i] = rnd();
    if (k < 3 && n >= 8) {
        static const unsigned short et[3] = {0x888e, 0x0800, 0x86dd};
        b[0] = 0xaa, b[1] = 0xaa, b[2] = 3, b[3] = b[4] = b[5] = 0;
        b[6] = et[k] >> 8, b[7] = (unsigned char)et[k];
        if (k == 0 && n >= 12) { /* EAPOL: version, type 3, a length near the truth */
            unsigned l = rnd() % 4 ? n - 12 : rnd() % 600;
            b[8] = 2, b[9] = 3, b[10] = l >> 8, b[11] = (unsigned char)l;
        } else if (k == 1 && n >= 12) {
            unsigned l = rnd() % 4 ? n - 8 : rnd() % 2000;
            b[8] = 0x45, b[10] = l >> 8, b[11] = (unsigned char)l;
        } else if (k == 2 && n >= 14) {
            unsigned l = rnd() % 4 ? (n >= 48 ? n - 48 : 0) : rnd() % 2000;
            b[8] = 0x60, b[12] = l >> 8, b[13] = (unsigned char)l;
        }
    } else if (k == 3 && n >= 12) { /* fixed fields, then IEs */
        unsigned o = rnd() % 2 ? 4 : 10;
        while (o + 2 < n) {
            unsigned l = rnd() % 12;
            b[o] = rnd() % 3 ? (rnd() % 2 ? 1 : 0) : rnd() % 64;
            b[o + 1] = l;
            o += 2 + l;
        }
    }
    return n;
}

/* A frame as the core hands it over: RX descriptor markers, header at +0xac, body at +0xcc. */
static unsigned frame(unsigned char *f, unsigned size, const unsigned char *peer)
{
    static const unsigned short fcs[] = {0x0000, 0x0010, 0x0020, 0x0040, 0x00a0, 0x00b0, 0x00c0,
                                         0x0208, 0x0108, 0x0248, 0x4208, 0x0288, 0x0308};
    unsigned fc = fcs[rnd() % (sizeof fcs / sizeof fcs[0])];
    memset(f, 0, 0xcc);
    for (unsigned i = 0; i < 0x94; i++)
        f[i] = rnd();
    f[0x94] = rnd() % 16 ? 0x18 : rnd();
    f[0x97] = rnd() % 16 ? 0xc4 : rnd();
    f[0x94 + 7] = rnd() % 2 ? RX_DECIPHERED : rnd() % 4;
    f[0xac] = (unsigned char)fc, f[0xad] = (unsigned char)(fc >> 8);
    addr(f + 0xac + 4, peer), addr(f + 0xac + 10, peer), addr(f + 0xac + 16, peer);
    unsigned n = body(f + 0xcc, size - 0xcc);
    unsigned len = 0xcc + n;
    mutate(f + 0x94, len - 0x94, rnd() % 4);
    if (rnd() % 8 == 0)
        len = rnd() % (len + 1);
    else if (rnd() % 4 == 0)
        len = 0xcc + rnd() % (n + 1);
    return len;
}

static void check_state(void)
{
    unsigned char macs[WIFI_AP_CLIENTS][6];
    CHECK(eappend <= sizeof eapframe, "eapol pending %u", eappend);
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
        CHECK(cl[i].state <= 2, "client %u state %u", i, cl[i].state);
    CHECK(wifi_ap_clients(macs) <= WIFI_AP_CLIENTS, "clients");
    CHECK(txnfree <= NTXFBUF, "tx buffers %u", txnfree);
}

/* The station: joined or in the 4-way, keyed or not; EAPOL that got through goes to the
 * supplicant. */
static void fuzz_station(void)
{
    static const unsigned char pmk0[32] = {7}, sn0[32] = {9};
    unsigned char f[0xcc + 400];
    unsigned kicks = 0, eap = 0;
    for (unsigned it = 0; it < 30000 && !fails; it++) {
        if (it % 64 == 0) {
            rings_fresh();
            wpa_sm_init(&sm, pmk0, BSS, OWN, sn0);
            state = rnd() % 2 ? WIFI_UP : WIFI_JOINING, jstep = J_4WAY;
            keyed = rnd() % 2, ap_keyed = keyed && rnd() % 2, keys_due = 0, kicked = 0;
        }
        unsigned len = frame(f, sizeof f, BSS);
        if (len >= 0xcc && rnd() % 2) { /* aimed at us: from the BSS, data from the DS */
            memcpy(f + 0xac + 10, BSS, 6), memcpy(f + 0xac + 16, BSS, 6);
            if (rnd() % 2)
                f[0xac] = 0x08, f[0xad] = 0x02;
            if (len >= 0xcc + 12 && rnd() % 2) { /* LLC/SNAP, EAPOL-Key, a length near the truth */
                static const unsigned char llc[10] = {0xaa, 0xaa, 3, 0, 0, 0, 0x88, 0x8e, 2, 3};
                unsigned l = rnd() % 4 ? len - 0xcc - 12 : rnd() % 600;
                memcpy(f + 0xcc, llc, sizeof llc), f[0xcc + 10] = l >> 8, f[0xcc + 11] = (unsigned char)l;
            }
        }
        unsigned char *p = exact(f, len);
        rx_station(p, len);
        free(p);
        kicks += kicked, kicked = 0;
        if (eappend) {
            eap++;
            if (rnd() % 4 == 0)
                rings_fresh();
            eapol(); /* the supplicant on what came through (and any reply it sends) */
        }
        check_state();
    }
    CHECK(kicks > 100, "deauthentications reach the station (%u)", kicks);
    CHECK(eap > 1000, "EAPOL reaches the supplicant (%u)", eap);
}

/* The setup access point: probes, authentications from many addresses, associations, data. */
static void fuzz_ap(void)
{
    static const unsigned char beacon[] = {0, 0, 0, 0, 0, 0, 0, 0, 100, 0, 0x01, 0x00,
                                           0, 8, 'S', 'o', 'u', 'n', 'd', 'T', 'a', 'p',
                                           1, 4, 0x82, 0x84, 0x8b, 0x96,
                                           50, 4, 0x0c, 0x12, 0x18, 0x24,
                                           3, 1, 6,
                                           5, 4, 0, 1, 0, 0};
    unsigned char f[0xcc + 400], peer[6];
    unsigned joined = 0;
    rings_fresh();
    have_cur = 0, state = WIFI_IDLE, now = 1000;
    CHECK(wifi_ap_start(6, 100, beacon, sizeof beacon) == 0, "AP starts");
    corebd = 1;
    ap_step();
    CHECK(ap_ready, "AP ready");
    for (unsigned it = 0; it < 40000 && !fails; it++) {
        if (it % 32 == 0)
            rings_fresh();
        now += rnd() % 4 ? rnd() % 50 : rnd() % (2 * AP_IDLE_MS);
        memcpy(peer, BSS, 6), peer[5] = rnd() % 10; /* a few clients, some of them again */
        unsigned len = frame(f, sizeof f, peer);
        if (len >= 0xcc && rnd() % 2) { /* aimed at the AP: to and from our BSSID */
            static const unsigned char auth[6] = {0, 0, 1, 0, 0, 0};
            static const unsigned char assoc[10] = {0x01, 0x00, 0x0a, 0x00, 1, 4, 0x82, 0x84, 0x8b, 0x96};
            unsigned sub = f[0xac] & 0xfc;
            memcpy(f + 0xac + 4, OWN, 6), memcpy(f + 0xac + 16, OWN, 6);
            if (rnd() % 2)
                memcpy(f + 0xac + 10, peer, 6);
            if (!(f[0xac] & 0x0c) && sub == 0xb0 && len >= 0xcc + 6 && rnd() % 2)
                memcpy(f + 0xcc, auth, sizeof auth);           /* open system, seq 1 */
            if (!(f[0xac] & 0x0c) && (sub == 0x00 || sub == 0x20) && rnd() % 2) {
                unsigned ies = sub ? 10 : 4; /* a reassociation names its current AP first */
                if (len >= 0xcc + ies + 6)
                    memcpy(f + 0xcc, assoc, 4), memcpy(f + 0xcc + ies, assoc + 4, 6);
            }
            if (rnd() % 4 == 0)
                mutate(f + 0xac, len - 0xac, 1);
        }
        unsigned char *p = exact(f, len);
        rx_ap(p, len);
        free(p);
        for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
            joined += cl[i].state == 2;
        check_state();
    }
    wifi_ap_stop();
    CHECK(joined > 1000, "clients associate (%u)", joined);
}

/* rx_data alone: any addresses, any body. */
static void fuzz_data(void)
{
    unsigned char b[1600], da[6], sa[6];
    unsigned before = to_scm;
    for (unsigned it = 0; it < 30000 && !fails; it++) {
        state = rnd() % 3 ? WIFI_UP : rnd() % 2 ? WIFI_AP : WIFI_JOINING;
        addr(da, BSS), addr(sa, BSS);
        unsigned n = body(b, sizeof b);
        mutate(b, n, rnd() % 4);
        if (rnd() % 8 == 0)
            n = rnd() % (n + 1);
        unsigned char *p = exact(b, n);
        rx_data(da, sa, p, n);
        free(p);
        CHECK(eappend <= sizeof eapframe, "eapol pending %u", eappend);
        eappend = 0;
    }
    CHECK(to_scm - before > 1000, "frames reach the SCM (%u)", to_scm - before);
}

/* Frames under the app's own keys (RX_SOFT, ADR 0136): TKIP pairwise and group frames from the AP,
 * valid, then mutated and cut, through rx_station's software decipher (sw_open, wsec.c). Intact
 * ones reach the SCM; Michael failures may end the link (the countermeasures), which each round
 * starts over from. */
static void fuzz_soft(void)
{
    static const unsigned char llc[8] = {0xaa, 0xaa, 3, 0, 0, 0, 0x08, 0x00};
    unsigned char f[0xcc + 8 + 8 + 300 + 12], key[32];
    unsigned before = to_scm, mics = 0;
    for (unsigned i = 0; i < 32; i++)
        key[i] = (unsigned char)(i * 9 + 1);
    for (unsigned it = 0; it < 20000 && !fails; it++) {
        if (it % 64 == 0) {
            rings_fresh();
            state = WIFI_UP, keyed = 1, have_cur = 1, cm_on = 0, mic_fail_seen = 0, now += 70000;
            sm.ptk_set = 1;
            sw_pair.cipher = WSEC_TKIP, sw_grp[1].cipher = WSEC_TKIP, sw_grp[2].cipher = WSEC_WEP104;
            memcpy(sw_pair.key, key, 32), memcpy(sw_grp[1].key, key, 32), memcpy(sw_grp[2].key, key, 13);
            sw_pair.rxtsc = sw_grp[1].rxtsc = 0;
        }
        int group = rnd() % 2;
        unsigned n = 8 + rnd() % 300;
        memset(f, 0, 0xcc);
        f[0x94] = 0x18, f[0x97] = 0xc4, f[0x94 + 7] = RX_SOFT;
        f[0xac] = 0x08, f[0xad] = 0x42;
        if (group)
            memset(f + 0xac + 4, 0xff, 6);
        else
            memcpy(f + 0xac + 4, OWN, 6);
        memcpy(f + 0xac + 10, BSS, 6), memcpy(f + 0xac + 16, BSS, 6);
        memcpy(f + 0xcc + 8, llc, 8);
        for (unsigned i = 8; i < n; i++)
            f[0xcc + 8 + i] = (unsigned char)rnd();
        /* now and then under the wrong Michael key: ICV good, MIC bad; a quarter WEP under key 2 */
        unsigned len;
        if (rnd() % 4 == 0) {
            memmove(f + 0xcc + 4, f + 0xcc + 8, n);
            len = 0xcc + wsec_wep_protect(key, 13, 2, it, f + 0xcc, n);
        } else {
            len = 0xcc + wsec_tkip_protect(key, key + (rnd() % 16 ? 16 : 24), group, it + 1, BSS,
                                           f + 0xac + 4, BSS, f + 0xcc, n);
        }
        if (rnd() % 3 == 0)
            mutate(f + 0xcc, len - 0xcc, 1 + rnd() % 3);
        if (rnd() % 8 == 0)
            len = 0xcc + rnd() % (len - 0xcc + 1);
        unsigned char *p = exact(f, len);
        rx_station(p, len);
        free(p);
        mics += mic_fail_seen, mic_fail_seen = 0;
        if (state != WIFI_UP)
            it |= 63; /* the countermeasures ended the link: next round */
    }
    CHECK(to_scm - before > 1000, "TKIP and WEP frames reach the SCM (%u)", to_scm - before);
    CHECK(mics > 0, "Michael failures seen (%u)", mics);
}

/* Robust management frames under PMF and SAE frames (ADR 0136): protected deauthentications,
 * disassociations and SA Queries under CCMP, deauthentications to everybody with an MMIE, SAE
 * authentication frames while SAE runs, valid and then mutated and cut, through rx_station's
 * mgmt_open, bip_open and SAE paths. */
static void fuzz_pmf(void)
{
    static const unsigned char subs[3] = {0xc0, 0xa0, 0xd0};
    unsigned char f[0xcc + 8 + 64 + 8 + WSEC_MMIE_LEN], tk[16], gk[16], fbody[64];
    unsigned kicks = 0, queued = 0;
    for (unsigned i = 0; i < 16; i++)
        tk[i] = (unsigned char)(i * 3), gk[i] = (unsigned char)(i * 5 + 1);
    for (unsigned it = 0; it < 20000 && !fails; it++) {
        if (it % 64 == 0) {
            rings_fresh();
            state = WIFI_UP, keyed = 1, have_cur = 1, kicked = 0, pmf = 1, saq_on = 0;
            memcpy(mgmt_tk, tk, 16), mgmt_rxpn = 0, memcpy(igtk, gk, 16), igtk_id = 4, ipn = 0;
        }
        unsigned kind = rnd() % 4, sub = subs[rnd() % 3], n = 2 + rnd() % 40, len;
        for (unsigned i = 0; i < n; i++)
            fbody[i] = (unsigned char)rnd();
        if (sub == 0xd0)
            fbody[0] = 8, fbody[1] = rnd() % 3;
        memset(f, 0, 0xcc);
        f[0x94] = 0x18, f[0x97] = 0xc4;
        memcpy(f + 0xac + 10, BSS, 6), memcpy(f + 0xac + 16, BSS, 6);
        if (kind == 3) { /* SAE while it runs */
            state = WIFI_JOINING, jstep = J_SAE;
            f[0xac] = 0xb0, memcpy(f + 0xac + 4, OWN, 6);
            fbody[0] = 3, fbody[1] = 0;
            memcpy(f + 0xcc, fbody, n);
            len = 0xcc + n;
        } else if (kind == 2) { /* to everybody, with an MMIE */
            f[0xac] = (unsigned char)(sub == 0xd0 ? 0xc0 : sub), memset(f + 0xac + 4, 0xff, 6);
            memcpy(f + 0xcc, fbody, n);
            len = 0xcc + wsec_bip_protect(gk, 4, it + 1, f + 0xac, f + 0xcc, n);
        } else { /* to us, under CCMP (or now and then in the clear) */
            f[0xac] = (unsigned char)sub, f[0xad] = 0x40, memcpy(f + 0xac + 4, OWN, 6);
            memcpy(f + 0xcc + 8, fbody, n);
            len = 0xcc + wsec_ccmp_protect(tk, f + 0xac, it + 1, f + 0xcc, n);
            if (kind == 1 && rnd() % 2)
                f[0xad] = 0, len = 0xcc + n, memcpy(f + 0xcc, fbody, n);
        }
        if (rnd() % 3 == 0)
            mutate(f + 0xac, len - 0xac, 1 + rnd() % 3);
        if (rnd() % 8 == 0)
            len = 0xcc + rnd() % (len - 0xcc + 1);
        unsigned char *p = exact(f, len);
        rx_station(p, len);
        free(p);
        kicks += kicked, kicked = 0;
        unsigned char m[1 + WIFI_SAE_MAX];
        while (wifi_sae_next(m, sizeof m) > 0)
            queued++;
        if (state != WIFI_UP && state != WIFI_JOINING)
            it |= 63;
    }
    CHECK(kicks > 100, "protected deauthentications reach the station (%u)", kicks);
    CHECK(queued > 100, "SAE frames go to the SCM (%u)", queued);
}

int main(void)
{
    copy(own, OWN, 6);
    copy(cur.bssid, BSS, 6), copy(cur.ssid, (const unsigned char *)"Home", 4), cur.ssidlen = 4, have_cur = 1;
    fuzz_station();
    fuzz_ap();
    have_cur = 1;
    fuzz_data();
    fuzz_soft();
    fuzz_pmf();
    printf("wifi rx fuzz: %s (%u frames to the SCM)\n", fails ? "FAILED" : "ok", to_scm);
    return fails != 0;
}
