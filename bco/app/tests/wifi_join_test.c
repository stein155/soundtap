/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The join and the setup access point with the real wifi.c on the host.
 *
 * The join's association step (src/wifi.c join_step, J_ASSOC):
 * once the AP has accepted the association, PEER_ADD must reach the core's command ring before the
 * 4-way starts, since the core installs the PTK only for a MAC in its peer table. With the command
 * ring full the step waits and tries again on the next poll, and gives the attempt up at the assoc
 * deadline; it never goes on to the 4-way without PEER_ADD. A network seen only with security this
 * station cannot join fails as such, and a refused authentication or association keeps the AP's
 * status code for STATUS.
 *
 * The access point: our core has no beacon timer, so wifi.c sends a beacon every interval once the
 * BSS is up, and answers a wildcard probe request with the beacon body minus its TIM. With every
 * client slot held by an associated client, an authentication from a new address is refused
 * (status 17) instead of throwing one of them off; only an authenticated-only client, or one
 * silent past AP_IDLE_MS, gives up its slot.
 *
 * The link: a deauthentication or disassociation sent to everyone by our access point ends the
 * link as a unicast one does. Message 4 answering a resent Message 3 goes in the clear, as the
 * first did, until a unicast frame the core deciphered shows the AP uses the PTK; the resent
 * Message 3 itself comes in the clear and is let through to the supplicant. A rekey's Message 4,
 * which the core enciphers under the PTK in use, goes out before the new PTK reaches the core.
 *
 * wifi.c keeps addresses in 32-bit words, so the test links without PIE (globals below 4 GiB, as
 * bco/wild/fw/tests/host.h does) and maps the one page of WiLD registers the step touches (the
 * doorbell and the timer it mixes into the SNonce) at its address, and the UART page. Nothing here
 * talks to a speaker. */
#include <assert.h>
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

/* What went to the SCM: the last frame, its channel and how many. */
static unsigned char scm[14 + 1500];
static unsigned scm_n, scm_ch, scm_count;
void netcard_from_port(unsigned ch, const unsigned char *f, unsigned n)
{
    scm_ch = ch, scm_n = n < sizeof scm ? n : sizeof scm, scm_count++;
    memcpy(scm, f, scm_n);
}

__attribute__((constructor)) static void map_wreg(void)
{
    static int probe;
    /* the WiLD registers, and the UART page: a failed join says so in one line (board.h say);
     * the core's SRAM page with the mailbox (HOST_FLAG2) and its first page (a flight recorder) */
    static const unsigned long at[4] = {(unsigned long)WREG, 0xb1001000ul, 0x3001f000ul, 0x30000000ul};
    for (int i = 0; i < 4; i++)
        if (mmap((void *)at[i], 0x1000, PROT_READ | PROT_WRITE,
                 MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0) != (void *)at[i]) {
            perror("wifi_join: map the WiLD registers and the UART");
            exit(2);
        }
    if ((uintptr_t)&probe >> 32) {
        fputs("wifi_join: globals above 4 GiB; build with -no-pie\n", stderr);
        exit(2);
    }
}

static unsigned cmdbuf[64];
static const unsigned char AP[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x01};

/* The host side of the command ring with room for `words` more words. */
static void cmd_ring(unsigned words)
{
    memset(cmdbuf, 0, sizeof cmdbuf);
    wr_init(&tx, 0, (uintptr_t)cmdbuf, words + 1, 4, 2);
}

/* The AP answered the association: the state join_step finds in J_ASSOC. */
static void associated(void)
{
    prof_sec = WIFI_SEC_PSK;
    copy(cur.bssid, AP, 6);
    state = WIFI_JOINING, jstep = J_ASSOC, rxassocok = 1, kicked = 0;
    now = 1000, deadline = now + 1500;
}

/* Host-side TX frame buffers and the management frame ring (channel 0 is the host's). */
static unsigned char txmem[NTXFBUF * TXFBUF] __attribute__((aligned(8)));
static unsigned frmbuf[28 * 6];
static void tx_frames(void)
{
    memset(frmbuf, 0, sizeof frmbuf);
    wr_init(&frm[2], 0, (uintptr_t)frmbuf, 28, 0x18, 5);
    txfbase = (unsigned)(uintptr_t)txmem;
    for (txnfree = 0; txnfree < NTXFBUF; txnfree++)
        txfbuf[txnfree] = txfbase + txnfree * TXFBUF, txbusy[txnfree] = 0;
}

/* The frame in the TX buffer the last tx_push used: descriptor at +8, body at +8+0xc4. */
static const unsigned char *last_frame(void)
{
    return (const unsigned char *)(uintptr_t)txfbuf[txnfree];
}

/* A management frame as the core hands it to the app: header at +0xac, body at +0xcc. */
static unsigned char rxf[0xcc + 64];
static unsigned rx_mgmt(unsigned fc, const unsigned char *a1, const unsigned char *a2,
                        const unsigned char *a3, const unsigned char *body, unsigned n)
{
    memset(rxf, 0, sizeof rxf);
    rxf[0x94] = 0x18, rxf[0x97] = 0xc4;
    rxf[0xac] = (unsigned char)fc, rxf[0xad] = (unsigned char)(fc >> 8);
    memcpy(rxf + 0xac + 4, a1, 6), memcpy(rxf + 0xac + 10, a2, 6), memcpy(rxf + 0xac + 16, a3, 6);
    memcpy(rxf + 0xcc, body, n);
    return 0xcc + n;
}

static void security_and_status(void)
{
    static const unsigned char own_mac[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55};
    copy(own, own_mac, 6);
    /* only a WPA3/PMF-required BSS carries the SSID: the join fails on security, not "not found" */
    nbss = 1;
    memset(&bsstab[0], 0, sizeof bsstab[0]);
    memcpy(bsstab[0].bssid, AP, 6), memcpy(bsstab[0].ssid, "Home", 4);
    bsstab[0].ssidlen = 4, bsstab[0].chan = 6, bsstab[0].rsn = 1, bsstab[0].rsnok = 0;
    memcpy(cur.ssid, "Home", 4), cur.ssidlen = 4, have_cur = 1;
    state = WIFI_JOINING, fail_step = fail_code = 0, prof_sec = WIFI_SEC_PSK;
    join_attempt();
    assert(state == WIFI_FAILED && fail_step == WIFI_FAIL_SECURITY);
    /* an open profile does not join an RSN network either */
    prof_sec = WIFI_SEC_OPEN, state = WIFI_JOINING;
    join_attempt();
    assert(state == WIFI_FAILED && fail_step == WIFI_FAIL_SECURITY);
    prof_sec = WIFI_SEC_PSK;
    nbss = 0;
    state = WIFI_JOINING;
    join_attempt();
    assert(fail_step == WIFI_FAIL_NOT_FOUND);

    /* a refused association: status 31 (robust management frame policy) is kept for STATUS */
    copy(cur.bssid, AP, 6);
    rxassocok = rxauthok = 0;
    unsigned char ar[6] = {0x11, 0x00, 31, 0, 0, 0};
    rx_station(rxf, rx_mgmt(0x10, own_mac, AP, AP, ar, sizeof ar));
    assert(!rxassocok && fail_step == WIFI_FAIL_ASSOC && fail_code == 31);
    /* a refused authentication: algorithm 0, transaction 2, status 1 */
    unsigned char au[6] = {0, 0, 2, 0, 1, 0};
    rx_station(rxf, rx_mgmt(0xb0, own_mac, AP, AP, au, sizeof au));
    assert(!rxauthok && fail_step == WIFI_FAIL_AUTH && fail_code == 1);
    unsigned st, code;
    wifi_fail(&st, &code);
    assert(st == WIFI_FAIL_AUTH && code == 1);
    have_cur = 0;
    printf("ok: a join fails on security, a refusal keeps the AP's status code\n");
}

static void access_point(void)
{
    /* timestamp 8, interval 100, capability ESS, SSID "SoundTap", rates, DS 6, TIM */
    static const unsigned char body[] = {0, 0, 0, 0, 0, 0, 0, 0, 100, 0, 0x01, 0x00,
                                         0, 8, 'S', 'o', 'u', 'n', 'd', 'T', 'a', 'p',
                                         1, 4, 0x82, 0x84, 0x8b, 0x96,
                                         3, 1, 6,
                                         5, 4, 0, 1, 0, 0};
    static const unsigned char phone[6] = {0x02, 0x99, 0x88, 0x77, 0x66, 0x55};
    static const unsigned char bad[] = {0, 0, 0, 0, 0, 0, 0, 0, 100, 0, 1, 0, 1, 9, 0x82};
    cmd_ring(32);
    tx_frames();
    state = WIFI_IDLE, now = 5000;
    assert(wifi_ap_start(6, 100, bad, sizeof bad) == -1);      /* IE runs past the end */
    assert(wifi_ap_start(6, 100, body, 12) == -1);             /* no SSID */
    assert(wifi_ap_start(6, 100, body, sizeof body) == 0 && state == WIFI_AP);
    assert(cmdbuf[2] == 0xbc);                                 /* the BSS on our own address */
    assert(!memcmp(cmdbuf + 4, own, 6));
    ap_step();
    assert(!ap_ready && ap_beacons == 0);                      /* the core has not set it up yet */
    corebd = 1;
    ap_step();
    assert(ap_ready && ap_beacons == 1);
    const unsigned char *f = last_frame(), *d = f + 8, *b = d + 0xc4;
    assert(d[0x18] == 0x80);                                   /* beacon */
    assert(!memcmp(d + 0x1c, "\xff\xff\xff\xff\xff\xff", 6) && !memcmp(d + 0x22, own, 6) &&
           !memcmp(d + 0x28, own, 6));                         /* to everybody, BSSID = own */
    assert(!memcmp(b + 8, body + 8, sizeof body - 8));         /* hostapd's body, TIM included */
    now += 50;
    ap_step();
    assert(ap_beacons == 1);                                   /* not before the interval */
    now += 53;
    ap_step();
    assert(ap_beacons == 2);                                   /* 100 TU = 102.4 ms */
    f = last_frame(), d = f + 8, b = d + 0xc4;
    assert(b[0] == (102400 & 0xff) && b[1] == (102400 >> 8 & 0xff) && b[2] == 102400 >> 16 && !b[3]);
    /* the target times stay multiples of 102.4 ms: the 11th beacon is due at 1024 ms, not 1020 */
    for (unsigned k = 3; k <= 10; k++) {
        now = ap_t0 + (k - 1) * 1024 / 10 + 1;
        tx_frames(); /* the test never reclaims a transmit buffer */
        ap_step();
    }
    assert(ap_beacons == 10 && ap_tbtt == 10 * 102400ull);
    now = ap_t0 + 1023;
    ap_step();
    assert(ap_beacons == 10);
    now = ap_t0 + 1024;
    tx_frames();
    ap_step();
    assert(ap_beacons == 11);
    f = last_frame(), d = f + 8, b = d + 0xc4;
    assert((b[0] | b[1] << 8 | b[2] << 16) == 1024000);        /* stamped with its target time */
    now += 5000;
    tx_frames();
    ap_step();
    assert(ap_beacons == 12);                                  /* late: one beacon, not a burst */
    ap_step();
    assert(ap_beacons == 12 && ap_tbtt % 102400 == 0);

    /* a wildcard probe request: a probe response to the phone, the body without the TIM */
    unsigned char pq[] = {0, 0, 1, 2, 0x82, 0x84};
    unsigned before = wifi_tx_frames;
    rx_ap(rxf, rx_mgmt(0x40, ap_bcast, phone, ap_bcast, pq, sizeof pq));
    assert(ap_probes == 1 && wifi_tx_frames == before + 1);
    f = last_frame(), d = f + 8, b = d + 0xc4;
    assert(d[0x18] == 0x50 && !memcmp(d + 0x1c, phone, 6) && !memcmp(d + 0x28, own, 6));
    assert(!memcmp(b + 8, body + 8, sizeof body - 8 - 6));     /* everything up to the TIM */
    /* a probe for another network gets no answer */
    unsigned char other[] = {0, 4, 'N', 'e', 'x', 't'};
    rx_ap(rxf, rx_mgmt(0x40, ap_bcast, phone, ap_bcast, other, sizeof other));
    assert(ap_probes == 2 && wifi_tx_frames == before + 1);
    /* one for our SSID does */
    unsigned char ours[] = {0, 8, 'S', 'o', 'u', 'n', 'd', 'T', 'a', 'p'};
    rx_ap(rxf, rx_mgmt(0x40, own, phone, own, ours, sizeof ours));
    assert(wifi_tx_frames == before + 2);
    wifi_ap_stop();
    assert(state == WIFI_IDLE);
    printf("ok: the access point beacons every interval and answers probe requests\n");
}

/* A data frame from the AP (fromDS) to us carrying an EAPOL frame, as the core hands it over;
 * deciphered marks it as the core marks a frame it deciphered (RX_DECIPHERED). */
static unsigned char rxd[0xcc + 8 + 160];
static unsigned rx_eapol(const unsigned char *e, unsigned n, int deciphered)
{
    static const unsigned char llc[8] = {0xaa, 0xaa, 3, 0, 0, 0, 0x88, 0x8e};
    memset(rxd, 0, sizeof rxd);
    rxd[0x94] = 0x18, rxd[0x97] = 0xc4, rxd[0x94 + 7] = deciphered ? RX_DECIPHERED : 0;
    rxd[0xac] = 0x08, rxd[0xad] = 0x02;
    memcpy(rxd + 0xac + 4, own, 6), memcpy(rxd + 0xac + 10, AP, 6), memcpy(rxd + 0xac + 16, AP, 6);
    memcpy(rxd + 0xcc, llc, 8);
    memcpy(rxd + 0xcc + 8, e, n);
    return 0xcc + 8 + n;
}

/* A resent Message 3 (counter ctr) MICed under the PTK in use: what the AP sends when our Message 4
 * got lost. Key data is not looked at on this path. */
static unsigned resent_msg3(unsigned char *m, unsigned ctr)
{
    unsigned char mic[20];
    memset(m, 0, O_KD);
    m[0] = 2, m[1] = 3, m[3] = O_KD - 4, m[4] = 2;
    wbe16(m + O_KEYINFO, 2 | KI_PAIRWISE | KI_MIC | KI_ACK | KI_INSTALL | KI_SECURE | KI_ENCRYPTED);
    m[O_KEYLEN + 1] = 16, m[O_REPLAY + 7] = (unsigned char)ctr;
    wpa_hmac_sha1(sm.ptk, 16, m, O_KD, mic);
    memcpy(m + O_MIC, mic, 16);
    return O_KD;
}

static void link_frames(void)
{
    static const unsigned char own_mac[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55};
    static const unsigned char other[6] = {0x02, 0x77, 0x77, 0x77, 0x77, 0x77};
    static const unsigned char all[6] = {0xff, 0xff, 0xff, 0xff, 0xff, 0xff};
    unsigned char reason[2] = {3, 0}, m[O_KD] = {0};
    copy(own, own_mac, 6);
    copy(cur.bssid, AP, 6), have_cur = 1;
    state = WIFI_UP, kicked = 0, rxauthok = 0, fail_step = fail_code = 0;

    /* deauthentication / disassociation to everyone: only from our BSS, only those two subtypes */
    rx_station(rxf, rx_mgmt(0xc0, all, AP, other, reason, sizeof reason));
    assert(!kicked);                                           /* BSSID (addr3) not ours */
    rx_station(rxf, rx_mgmt(0xc0, all, other, AP, reason, sizeof reason));
    assert(!kicked);                                           /* transmitter (addr2) not ours */
    unsigned char au[6] = {0, 0, 2, 0, 0, 0};
    rx_station(rxf, rx_mgmt(0xb0, all, AP, AP, au, sizeof au));
    assert(!rxauthok);                                         /* a broadcast authentication: no */
    rx_station(rxf, rx_mgmt(0xa0, all, AP, AP, (const unsigned char *)"\x08\x00", 2));
    assert(kicked && kick_sub == 0xa0 && kick_reason == 8 && fail_step == WIFI_FAIL_KICKED);
    kicked = 0;
    rx_station(rxf, rx_mgmt(0xc0, all, AP, AP, reason, sizeof reason));
    assert(kicked && kick_sub == 0xc0 && kick_reason == 3 && fail_code == 3);
    kicked = 0;
    printf("ok: a deauthentication or disassociation to everyone from our BSS ends the link\n");

    /* the 4-way is done on our side: PTK in the core, Message 4 sent; the AP never got it */
    static const unsigned char pmk0[32] = {1}, sn0[32] = {2};
    wpa_sm_init(&sm, pmk0, AP, own, sn0);
    for (unsigned i = 0; i < 48; i++)
        sm.ptk[i] = (unsigned char)(0x60 + i);
    sm.state = WPA_DONE, sm.ptk_set = 1, sm.rx_replay_set = 1, sm.rx_replay[7] = 2;
    keyed = 1, ap_keyed = 0, keys_due = 0, eappend = 0;
    cmd_ring(32);
    tx_frames();
    unsigned before = wifi_tx_frames, drops = wifi_rx_drop;
    rx_station(rxd, rx_eapol(m, resent_msg3(m, 3), 0));       /* in the clear: the AP has no key */
    assert(eappend == O_KD && wifi_rx_drop == drops);
    eapol();
    assert(wifi_tx_frames == before + 1);
    const unsigned char *d = last_frame() + 8;
    assert(d[0x18] == 0x08 && !(d[0x19] & 0x40));              /* Message 4 again, in the clear */
    /* a clear frame that is not EAPOL is still dropped once keyed */
    unsigned len = rx_eapol(m, resent_msg3(m, 4), 0);
    rxd[0xcc + 7] = 0x00;                                       /* ethertype 0x8800 */
    rx_station(rxd, len);
    assert(wifi_rx_drop == drops + 1 && !eappend);
    /* a group frame the core deciphered proves only the GTK: still in the clear */
    len = rx_eapol(m, resent_msg3(m, 4), 1);
    rxd[0xac + 4] = 0x01;
    rx_station(rxd, len);
    assert(!ap_keyed);
    eappend = 0;
    /* a unicast frame it deciphered: the AP uses the PTK, so a later rekey's EAPOL is protected */
    rx_station(rxd, rx_eapol(m, resent_msg3(m, 4), 1));
    assert(ap_keyed && eappend == O_KD);
    tx_frames();
    eapol();
    d = last_frame() + 8;
    assert(d[0x18] == 0x08 && (d[0x19] & 0x40));
    keyed = ap_keyed = 0, have_cur = 0, state = WIFI_IDLE;
    printf("ok: Message 4 for a resent Message 3 goes in the clear until the AP uses the key\n");
}

/* The core hands a TX buffer back: its record moves through the core's channels 1 and 2 to the
 * host's channel 3 (with the buffer in word 1, as xport.c tx_return writes it) and a report. */
static void core_returns_frames(void)
{
    while (wr_avail(&frm[2], 1)) {
        volatile unsigned *e = wr_reserve(&frm[2], 1, 1);
        e[1] = e[0], e[3] = 0;
        wr_commit(&frm[2], 1, 1);
        wr_reserve(&frm[2], 2, 1);
        wr_commit(&frm[2], 2, 1);
    }
}

/* The type of the command record at word w of the command ring, 0 if none was written there. */
static unsigned cmd_at(unsigned w)
{
    return (cmdbuf[w] & 0xff) == 1 ? cmdbuf[w + 1] & 0xffff : 0;
}

/* A PTK rekey under CCMP: the core enciphers a frame when it stages it, after the commands queued
 * before it, so Message 4 queued Protected with the new PTK's install behind it went out under the
 * new TK, which the AP installs only on that Message 4. The new PTK must reach the command ring
 * only once Message 4's buffer is back. (WPA1 with CCMP: its Message 3 carries no wrapped GTK,
 * the path to the key install is the same as RSN's.) */
static void rekey_msg4_old_key(void)
{
    static const unsigned char own_mac[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55};
    static const unsigned char pmk0[32] = {3}, sn0[32] = {4};
    unsigned char m[O_KD] = {0}, anonce[32], old_tk[16], mic[20];
    copy(own, own_mac, 6);
    copy(cur.bssid, AP, 6), have_cur = 1;
    prof_sec = WIFI_SEC_PSK, state = WIFI_UP, kicked = 0;
    wpa_sm_init(&sm, pmk0, AP, own, sn0);
    wpa_sm_config(&sm, WPA_PROTO_WPA, WPA_CIPHER_CCMP, WPA_CIPHER_CCMP, WPA_AKM_PSK, 0);
    for (unsigned i = 0; i < 48; i++)
        sm.ptk[i] = (unsigned char)(0x30 + i);
    memcpy(old_tk, sm.ptk + 32, 16);
    sm.state = WPA_DONE, sm.ptk_set = 1, sm.rx_replay_set = 1, sm.rx_replay[7] = 4;
    keyed = ap_keyed = 1, keys_due = 0, eappend = 0, eapol_buf = 0;
    memset(&sw_pair, 0, sizeof sw_pair);
    cmd_ring(32);
    tx_frames();

    /* Message 1 of the rekey, under the PTK in use: Message 2 goes out Protected */
    for (unsigned i = 0; i < 32; i++)
        anonce[i] = (unsigned char)(0xc0 + i);
    m[0] = 2, m[1] = 3, m[3] = O_KD - 4, m[4] = DESC_WPA;
    wbe16(m + O_KEYINFO, 2 | KI_PAIRWISE | KI_ACK);
    m[O_KEYLEN + 1] = 16, m[O_REPLAY + 7] = 5;
    memcpy(m + O_NONCE, anonce, 32);
    rx_station(rxd, rx_eapol(m, O_KD, 1));
    eapol();
    assert(sm.state == WPA_GOT_MSG1 && (last_frame()[8 + 0x19] & 0x40));
    core_returns_frames();
    tx_reclaim();
    assert(!eapol_buf && txnfree == NTXFBUF);

    /* Message 3 under the new PTK's KCK: Message 4 goes out Protected, the new PTK waits */
    memset(m, 0, sizeof m);
    m[0] = 2, m[1] = 3, m[3] = O_KD - 4, m[4] = DESC_WPA;
    wbe16(m + O_KEYINFO, 2 | KI_PAIRWISE | KI_INSTALL | KI_ACK | KI_MIC);
    m[O_KEYLEN + 1] = 16, m[O_REPLAY + 7] = 6;
    memcpy(m + O_NONCE, anonce, 32);
    wpa_hmac_sha1(sm.tptk, 16, m, O_KD, mic);
    memcpy(m + O_MIC, mic, 16);
    unsigned at = tx.ch[0].pos, room = wr_avail(&tx, 0);
    rx_station(rxd, rx_eapol(m, O_KD, 1));
    eapol();
    assert(sm.state == WPA_DONE && memcmp(sm.ptk + 32, old_tk, 16) != 0);
    const unsigned char *d = last_frame() + 8;
    assert(d[0x18] == 0x08 && (d[0x19] & 0x40));               /* Message 4, Protected */
    assert(eapol_buf == txfbuf[txnfree] && (keys_due & WPA_RX_INSTALL_PTK));
    assert(wr_avail(&tx, 0) == room && !cmd_at(at));           /* no key behind it in the ring */
    /* polls while the core still holds it change nothing */
    eapol();
    install_keys();
    assert(wr_avail(&tx, 0) == room && (keys_due & WPA_RX_INSTALL_PTK));
    /* the core enciphered and sent it (under the old TK, the only one it has) and gives it back */
    core_returns_frames();
    tx_reclaim(); /* the next poll: reclaim, then the EAPOL step */
    eapol();
    assert(!eapol_buf && !(keys_due & WPA_RX_INSTALL_PTK) && keyed);
    assert(cmd_at(at) == 13 && !memcmp((unsigned char *)&cmdbuf[at + 3] + 20, sm.ptk + 32, 16));
    keyed = ap_keyed = 0, have_cur = 0, state = WIFI_IDLE;
    memset(&sm, 0, sizeof sm);
    printf("ok: a rekey's Message 4 goes out under the old key, the new PTK after it\n");
}

/* Six phones associated with the setup network: a seventh address is refused, nobody is thrown
 * off; an authenticated-only client or one silent past AP_IDLE_MS makes room. */
static void ap_full(void)
{
    static const unsigned char body[] = {0, 0, 0, 0, 0, 0, 0, 0, 100, 0, 0x01, 0x00,
                                         0, 8, 'S', 'o', 'u', 'n', 'd', 'T', 'a', 'p',
                                         1, 4, 0x82, 0x84, 0x8b, 0x96};
    static const unsigned char au[6] = {0, 0, 1, 0, 0, 0};
    static const unsigned char as[] = {0x01, 0x00, 0x0a, 0x00, 1, 4, 0x82, 0x84, 0x8b, 0x96};
    unsigned char phone[6] = {0x02, 0x50, 0, 0, 0, 0}, macs[WIFI_AP_CLIENTS][6];
    cmd_ring(63);
    tx_frames();
    state = WIFI_IDLE, now = 20000;
    assert(wifi_ap_start(6, 100, body, sizeof body) == 0);
    corebd = 1;
    ap_step();
    assert(ap_ready);
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++) {
        phone[5] = (unsigned char)i;
        tx_frames();
        rx_ap(rxf, rx_mgmt(0xb0, own, phone, own, au, sizeof au));
        rx_ap(rxf, rx_mgmt(0x00, own, phone, own, as, sizeof as));
        now += 10;
    }
    assert(wifi_ap_clients(macs) == WIFI_AP_CLIENTS);
    /* a made-up address: refused with status 17, every phone stays */
    static const unsigned char stranger[6] = {0x02, 0xde, 0xad, 0xbe, 0xef, 0x01};
    tx_frames();
    rx_ap(rxf, rx_mgmt(0xb0, own, stranger, own, au, sizeof au));
    const unsigned char *d = last_frame() + 8, *b = d + 0xc4;
    assert(d[0x18] == 0xb0 && !memcmp(d + 0x1c, stranger, 6) && b[2] == 2 && b[4] == 17 && b[5] == 0);
    assert(wifi_ap_clients(macs) == WIFI_AP_CLIENTS && !ap_client(stranger, 0));
    /* phone 2 left without a word: silent past AP_IDLE_MS, its slot goes to the newcomer */
    now += 1000;
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
        cl[i].seen = now;
    cl[2].seen = now - AP_IDLE_MS;
    tx_frames();
    rx_ap(rxf, rx_mgmt(0xb0, own, stranger, own, au, sizeof au));
    d = last_frame() + 8, b = d + 0xc4;
    assert(d[0x18] == 0xb0 && b[4] == 0 && ap_client(stranger, 0) == &cl[2] && cl[2].state == 1);
    assert(wifi_ap_clients(macs) == WIFI_AP_CLIENTS - 1);
    /* the newcomer only authenticated: the next new address takes its slot, the phones stay */
    static const unsigned char third[6] = {0x02, 0xde, 0xad, 0xbe, 0xef, 0x02};
    tx_frames();
    rx_ap(rxf, rx_mgmt(0xb0, own, third, own, au, sizeof au));
    assert(ap_client(third, 0) == &cl[2] && !ap_client(stranger, 0));
    assert(wifi_ap_clients(macs) == WIFI_AP_CLIENTS - 1);
    wifi_ap_stop();
    printf("ok: a full access point refuses a new address instead of dropping a phone\n");
}

/* The setup AP's data path with a phone on it (ST20, 2026-11-27: the phone joined and got no
 * address). Its DHCP request (toDS, to everybody) goes to the SCM as an Ethernet broadcast from
 * the phone; the answer goes fromDS, unicast at its rates or to everybody. Once the phone dozes
 * (power-management bit in a null-data frame) unicast frames for it wait, its AID bit shows in
 * the beacon's TIM, a PS-Poll fetches one with More Data while more wait, and waking up fetches
 * the rest; group frames wait for the next beacon, which carries the group bit, and follow it.
 * AP_STATUS's counters see all of it. */
static void ap_data(void)
{
    static const unsigned char body[] = {0, 0, 0, 0, 0, 0, 0, 0, 100, 0, 0x01, 0x00,
                                         0, 8, 'S', 'o', 'u', 'n', 'd', 'T', 'a', 'p',
                                         1, 4, 0x82, 0x84, 0x8b, 0x96,
                                         3, 1, 6,
                                         5, 4, 0, 2, 0, 0}; /* the TIM as soundtap_bco adds it */
    static const unsigned char au[6] = {0, 0, 1, 0, 0, 0};
    static const unsigned char as[] = {0x01, 0x00, 0x0a, 0x00, 1, 4, 0x82, 0x84, 0x8b, 0x96};
    static const unsigned char phone[6] = {0x02, 0x99, 0x88, 0x77, 0x66, 0x55};
    static unsigned char df[0xcc + 64];
    const unsigned char *d;
    unsigned before;
    cmd_ring(63);
    tx_frames();
    state = WIFI_IDLE, now = 40000;
    assert(wifi_ap_start(6, 100, body, sizeof body) == 0 && ap_tim == sizeof body - 6);
    corebd = 1, now += 3;
    ap_step();
    assert(ap_ready && ap_beacons == 1);
    unsigned dg[WIFI_AP_DIAG];
    wifi_ap_diag(dg);
    assert(dg[0] == 3 && dg[1] == 0 && dg[4] == 0); /* first beacon 3 ms after AP_START; no recorder yet */
    d = last_frame() + 8;
    assert(d[0xc4 + ap_tim + 2] == 0 && d[0xc4 + ap_tim + 3] == 1 && d[0xc4 + ap_tim + 4] == 0 &&
           d[0xc4 + ap_tim + 5] == 0); /* a DTIM beacon, nothing held */
    rx_ap(rxf, rx_mgmt(0xb0, own, phone, own, au, sizeof au));
    rx_ap(rxf, rx_mgmt(0x00, own, phone, own, as, sizeof as));
    struct client *c = ap_client(phone, 0);
    assert(c == &cl[0] && c->state == 2);

    /* its DHCP discover: toDS, A1 = us, A3 = everybody; to the SCM from the phone */
    static const unsigned char llc[8] = {0xaa, 0xaa, 3, 0, 0, 0, 0x08, 0x00};
    memset(df, 0, sizeof df);
    df[0x94] = 0x18, df[0x97] = 0xc4, df[0x94 + 4] = (unsigned char)-41, df[0x94 + 6] = 0x6c; /* 54 Mb/s */
    df[0xac] = 0x08, df[0xad] = 0x01;
    memcpy(df + 0xac + 4, own, 6), memcpy(df + 0xac + 10, phone, 6), memcpy(df + 0xac + 16, ap_bcast, 6);
    memcpy(df + 0xcc, llc, 8);
    df[0xcc + 8] = 0x45, df[0xcc + 8 + 3] = 28; /* IPv4, 28 bytes: header + UDP */
    scm_count = 0;
    rx_ap(df, 0xcc + 8 + 28);
    assert(scm_count == 1 && scm_ch == BCOLINK_CH_WIFI && scm_n == 14 + 28);
    assert(!memcmp(scm, ap_bcast, 6) && !memcmp(scm + 6, phone, 6) && scm[12] == 0x08 && scm[13] == 0);
    assert(c->rx_data == 1 && !c->dozing && c->rssi == -41 && c->rate == 0x6c);
    /* the same frame again, with Retry: it missed our ACK */
    df[0xad] = 0x09, df[0xac + 22] = 0x50, df[0xac + 23] = 0x01;
    c->seq = 0x150;
    rx_ap(df, 0xcc + 8 + 28);
    assert(c->retries == 1 && c->repeats == 1 && c->seq == 0x150 && c->rx_data == 2);
    df[0xad] = 0x01, df[0xac + 22] = 0x60;
    rx_ap(df, 0xcc + 8 + 28);
    assert(c->retries == 1 && c->repeats == 1 && c->seq == 0x160);

    /* the offer, unicast to it: fromDS, A1 = phone, A2 = BSSID = A3 = us, its peer slot */
    static unsigned char pkt[14 + 300];
    memset(pkt, 0x5a, sizeof pkt);
    memcpy(pkt, phone, 6), memcpy(pkt + 6, own, 6), pkt[12] = 0x08, pkt[13] = 0;
    before = wifi_tx_frames;
    assert(radio_tx_eth(pkt, sizeof pkt) == 0 && wifi_tx_frames == before + 1);
    d = last_frame() + 8;
    assert(d[0x18] == 0x08 && d[0x19] == 0x02 && !memcmp(d + 0x1c, phone, 6) && !memcmp(d + 0x22, own, 6) &&
           !memcmp(d + 0x28, own, 6) && !memcmp(d + 0xc4 + 8, pkt + 14, 300));
    /* and to everybody */
    memcpy(pkt, ap_bcast, 6);
    assert(radio_tx_eth(pkt, sizeof pkt) == 0 && wifi_tx_frames == before + 2);
    d = last_frame() + 8;
    assert(d[0x18] == 0x08 && d[0x19] == 0x02 && !memcmp(d + 0x1c, ap_bcast, 6));
    /* a frame not from us is not sent */
    memcpy(pkt + 6, phone, 6);
    assert(radio_tx_eth(pkt, sizeof pkt) == -1);
    memcpy(pkt + 6, own, 6);

    /* reports from the core count for the client they were for */
    ap_tx_report(0x11u << 16, 0), ap_tx_report(0x11u << 16, 1); /* slot 0 */
    ap_tx_report(PEER_NONE | 0xffffu << 16, 1);
    assert(c->tx_ok == 1 && c->tx_failed == 1);

    /* it dozes: null-data with the power-management bit */
    rx_ap(rxf, rx_mgmt(0x48 | 0x1100, own, phone, own, au, 0));
    assert(c->dozing && c->rx_null == 1);
    tx_frames();
    before = wifi_tx_frames;
    memcpy(pkt, phone, 6);
    for (unsigned i = 0; i < 3; i++) {
        pkt[14] = (unsigned char)i;
        assert(radio_tx_eth(pkt, sizeof pkt) == 0);
    }
    assert(wifi_tx_frames == before && c->h.n == 3); /* held, not sent */
    memcpy(pkt, ap_bcast, 6);
    pkt[14] = 0x77;
    assert(radio_tx_eth(pkt, sizeof pkt) == 0 && wifi_tx_frames == before && ap_gh.n == 1);
    /* the beacon: its AID bit, the group bit; the group frame right behind it */
    now = ap_ms(ap_tbtt);
    ap_step();
    assert(wifi_tx_frames == before + 2 && ap_gh.n == 0);
    const unsigned char *bcn = (const unsigned char *)(uintptr_t)txfbuf[txnfree + 1] + 8, *g = last_frame() + 8;
    assert(bcn[0x18] == 0x80 && bcn[0xc4 + ap_tim + 4] == 1 && bcn[0xc4 + ap_tim + 5] == 2);
    assert(g[0x18] == 0x08 && g[0x19] == 0x02 && !memcmp(g + 0x1c, ap_bcast, 6) && g[0xc4 + 8] == 0x77);
    /* a PS-Poll: the oldest held frame, More Data since two more wait */
    memset(rxf, 0, 0xcc);
    rxf[0x94] = 0x18, rxf[0x97] = 0xc4, rxf[0xac] = 0xa4, rxf[0xad] = 0x10, rxf[0xae] = 1, rxf[0xaf] = 0xc0;
    memcpy(rxf + 0xac + 4, own, 6), memcpy(rxf + 0xac + 10, phone, 6);
    rx_ap(rxf, 0xcc);
    d = last_frame() + 8;
    assert(wifi_tx_frames == before + 3 && c->h.n == 2 && c->dozing);
    assert(d[0x18] == 0x08 && d[0x19] == 0x22 && !memcmp(d + 0x1c, phone, 6) && d[0xc4 + 8] == 0);
    /* a full hold loses its oldest frame */
    for (unsigned i = 3; i < 6; i++) {
        pkt[14] = (unsigned char)i;
        memcpy(pkt, phone, 6);
        assert(radio_tx_eth(pkt, sizeof pkt) == 0);
    }
    assert(c->h.n == AP_HOLD && ap_hdrop == 1);
    /* awake again: everything held goes, in order, More Data on all but the last */
    tx_frames();
    before = wifi_tx_frames;
    rx_ap(rxf, rx_mgmt(0x48 | 0x0100, own, phone, own, au, 0));
    assert(!c->dozing && c->h.n == 0 && wifi_tx_frames == before + AP_HOLD && c->rx_null == 2);
    for (unsigned i = 0; i < AP_HOLD; i++) {
        const unsigned char *f = (const unsigned char *)(uintptr_t)txfbuf[txnfree + AP_HOLD - 1 - i] + 8;
        assert(f[0xc4 + 8] == 2 + i && f[0x19] == (i < AP_HOLD - 1 ? 0x22 : 0x02));
    }
    /* awake, nothing held: straight out again */
    memcpy(pkt, phone, 6);
    assert(radio_tx_eth(pkt, sizeof pkt) == 0 && wifi_tx_frames == before + AP_HOLD + 1);

    /* AP_STATUS: the client's counters and the core's ap_mode counters through HOST_FLAG2 */
    struct wifi_ap_client info[WIFI_AP_CLIENTS];
    now += 40;
    c->seen = now - 40;
    assert(wifi_ap_client_info(info) == 1 && !memcmp(info[0].mac, phone, 6) && !info[0].dozing &&
           info[0].held == 0 && info[0].idle_ms == 40 && info[0].rx_data == 3 && info[0].rx_null == 2 &&
           info[0].tx_ok == 1 && info[0].tx_failed == 1 && info[0].retries == 1 &&
           info[0].repeats == 2); /* the two null-data frames above both carry sequence 0 */
    volatile unsigned *corerec = (volatile unsigned *)(uintptr_t)(0x30000000u + 0x100);
    corerec[CORE_RC_APIN] = 9, corerec[CORE_RC_APIN + 1] = 4, corerec[CORE_RC_APIN + 2] = 8;
    MB16(-8) = 0x100 >> 2;
    wifi_ap_diag(dg);
    assert(dg[0] == 3 && dg[1] == 9 && dg[2] == 4 && dg[3] == 8 && dg[4] == 1);
    unsigned ex[WIFI_AP_EXTRA];
    for (unsigned i = 0; i < WIFI_AP_EXTRA; i++)
        corerec[CORE_RC_APIN + 3 + i] = 100 + i;
    wifi_ap_extra(ex);
    assert(ex[0] == 100 && ex[WIFI_AP_EXTRA - 1] == 100 + WIFI_AP_EXTRA - 1);
    MB16(-8) = 0;
    wifi_ap_extra(ex);
    assert(ex[0] == 0);
    assert(info[0].rssi == c->rssi && info[0].rate == c->rate); /* of its last frame */

    /* a phone silent past WIFI_AP_GONE_MS left without a word: not listed, and while it
     * last dozed it no longer holds group frames back */
    c->dozing = 1, c->seen = now - WIFI_AP_GONE_MS;
    unsigned char macs[WIFI_AP_CLIENTS][6];
    assert(wifi_ap_client_info(info) == 0 && wifi_ap_clients(macs) == 0 && !ap_anyone_dozing());
    memcpy(pkt, ap_bcast, 6);
    before = wifi_tx_frames;
    assert(radio_tx_eth(pkt, sizeof pkt) == 0 && wifi_tx_frames == before + 1 && ap_gh.n == 0);
    c->seen = now - WIFI_AP_GONE_MS + 1;
    assert(wifi_ap_client_info(info) == 1 && ap_anyone_dozing());

    /* it authenticates again while it was dozing (it left without a word and came back): it
     * starts awake, the frames held for its earlier association are dropped, and frames for it
     * go straight out instead of waiting for a PS-Poll that never comes */
    tx_frames();
    memcpy(pkt, phone, 6);
    before = wifi_tx_frames;
    assert(radio_tx_eth(pkt, sizeof pkt) == 0 && c->h.n == 1 && wifi_tx_frames == before);
    unsigned lost = ap_hdrop;
    rx_ap(rxf, rx_mgmt(0xb0, own, phone, own, au, sizeof au));
    assert(!c->dozing && c->h.n == 0 && ap_hdrop == lost + 1 && wifi_tx_frames == before + 1);
    assert(radio_tx_eth(pkt, sizeof pkt) == 0 && wifi_tx_frames == before + 2);
    /* the same for a reassociation (its body carries the current AP's address too) */
    static const unsigned char ras[] = {0x01, 0x00, 0x0a, 0x00, 0x02, 0x11, 0x22, 0x33, 0x44, 0x55,
                                        1, 4, 0x82, 0x84, 0x8b, 0x96};
    rx_ap(rxf, rx_mgmt(0x48 | 0x1100, own, phone, own, au, 0));
    assert(c->dozing && radio_tx_eth(pkt, sizeof pkt) == 0 && c->h.n == 1);
    before = wifi_tx_frames;
    rx_ap(rxf, rx_mgmt(0x20, own, phone, own, ras, sizeof ras));
    assert(c->state == 2 && !c->dozing && c->h.n == 0 && ap_hdrop == lost + 2);
    assert(wifi_tx_frames == before + 1 && last_frame()[8 + 0x18] == 0x30); /* the response */

    /* stopping the AP drops every peer in the core, which ends its ap_mode */
    cmd_ring(16);
    wifi_ap_stop();
    assert(cmdbuf[2] == CMD_PEER_CLEAR && wifi_ap_client_info(info) == 0);
    printf("ok: the setup AP carries a phone's DHCP both ways and holds frames while it dozes\n");
}

/* The core hands every queued frame back with this status (0 delivered, 1 failed). */
static void core_returns_status(unsigned status)
{
    while (wr_avail(&frm[2], 1)) {
        volatile unsigned *e = wr_reserve(&frm[2], 1, 1);
        e[1] = e[0], e[3] = status;
        wr_commit(&frm[2], 1, 1);
        wr_reserve(&frm[2], 2, 1);
        wr_commit(&frm[2], 2, 1);
    }
}

/* The setup AP as the ST20 runs it (hostapd's st20Rates: 1-11 Mb/s basic, OFDM up to 24 Mb/s): a
 * phone's data rates are those it shares with the AP, so its unicast data goes at 24 Mb/s at most;
 * its null-data are timed and counted by power-management bit; failed transmits count by kind; the
 * core's frames by kind and lost beacons reach AP_STATUS. */
static void ap_rates_and_diag(void)
{
    static const unsigned char body[] = {0, 0, 0, 0, 0, 0, 0, 0, 100, 0, 0x01, 0x00,
                                         0, 8, 'S', 'o', 'u', 'n', 'd', 'T', 'a', 'p',
                                         1, 8, 0x82, 0x84, 0x8b, 0x96, 0x0c, 0x12, 0x18, 0x24,
                                         3, 1, 6,
                                         50, 1, 0x30,
                                         5, 4, 0, 1, 0, 0};
    static const unsigned char au[6] = {0, 0, 1, 0, 0, 0};
    /* an iPhone's: 1-11 Mb/s and 6-24 Mb/s in IE 1, 36-54 Mb/s in IE 50 */
    static const unsigned char as[] = {0x01, 0x00, 0x0a, 0x00, 1, 8, 0x82, 0x84, 0x8b, 0x96, 0x0c, 0x12, 0x18, 0x24,
                                       50, 4, 0x30, 0x48, 0x60, 0x6c};
    static const unsigned char as54[] = {0x01, 0x00, 0x0a, 0x00, 1, 1, 0x6c};
    static const unsigned char phone[6] = {0x02, 0x99, 0x88, 0x77, 0x66, 0x55};
    static const unsigned char shared[8] = {48, 36, 24, 22, 18, 12, 11, 4};
    static unsigned char pkt[14 + 100];
    cmd_ring(63);
    tx_frames();
    state = WIFI_IDLE, now = 60000;
    assert(wifi_ap_start(6, 100, body, sizeof body) == 0);
    corebd = 1, now += 3;
    ap_step();
    assert(ap_ready && ap_beacons == 1);
    rx_ap(rxf, rx_mgmt(0xb0, own, phone, own, au, sizeof au));
    rx_ap(rxf, rx_mgmt(0x00, own, phone, own, as, sizeof as));
    struct client *c = ap_client(phone, 0);
    assert(c && c->state == 2 && !memcmp(c->rates, shared, 8)); /* 24 Mb/s first, not 54 */
    memset(pkt, 0x5a, sizeof pkt);
    memcpy(pkt, phone, 6), memcpy(pkt + 6, own, 6), pkt[12] = 0x08, pkt[13] = 0;
    assert(radio_tx_eth(pkt, sizeof pkt) == 0);
    assert(last_frame()[8 + 4] == 48 && !memcmp(last_frame() + 8 + 4, shared, 8)); /* the core's cap */
    /* a client that shares no rate with the AP gets the AP's own chain */
    rx_ap(rxf, rx_mgmt(0x00, own, phone, own, as54, sizeof as54));
    assert(c->state == 2 && !memcmp(c->rates, shared, 8));
    rx_ap(rxf, rx_mgmt(0x00, own, phone, own, as, sizeof as));

    /* null-data: every ~711 ms with the PM bit, a retransmission of one, then one awake */
    struct wifi_ap_client info[WIFI_AP_CLIENTS];
    now = 70000;
    rx_ap(rxf, rx_mgmt(0x48 | 0x1100, own, phone, own, au, 0));
    assert(wifi_ap_client_info(info) == 1 && info[0].null_gap_ms == 0 && info[0].null_pm1 == 1);
    now += 711;
    rx_ap(rxf, rx_mgmt(0x48 | 0x1100, own, phone, own, au, 0));
    now += 2;
    rx_ap(rxf, rx_mgmt(0x48 | 0x1900, own, phone, own, au, 0)); /* Retry: the same null again */
    assert(wifi_ap_client_info(info) == 1 && info[0].null_gap_ms == 711 && info[0].null_gaps_700_730 == 1);
    now += 729;
    rx_ap(rxf, rx_mgmt(0xc8 | 0x1100, own, phone, own, au, 0)); /* a QoS-null 731 ms on: outside */
    assert(wifi_ap_client_info(info) == 1 && info[0].null_gap_ms == 731 && info[0].null_gaps_700_730 == 1);
    now += 300;
    rx_ap(rxf, rx_mgmt(0x48 | 0x0100, own, phone, own, au, 0));
    assert(wifi_ap_client_info(info) == 1 && info[0].null_gap_ms == 300 && info[0].null_pm1 == 4 &&
           info[0].null_pm0 == 1 && info[0].null_gaps_700_730 == 1 && info[0].rx_null == 5);

    /* failed transmits by kind: a beacon, a probe response, a data frame */
    tx_frames();
    unsigned failed = wifi_tx_failed, b0 = tx_fail_beacon, p0 = tx_fail_presp, o0 = tx_fail_other;
    now = ap_ms(ap_tbtt);
    ap_step();
    unsigned char pq[] = {0, 0};
    rx_ap(rxf, rx_mgmt(0x40, ap_bcast, phone, ap_bcast, pq, sizeof pq));
    assert(radio_tx_eth(pkt, sizeof pkt) == 0);
    core_returns_status(1);
    tx_reclaim();
    assert(wifi_tx_failed == failed + 3 && tx_fail_beacon == b0 + 1 && tx_fail_presp == p0 + 1 &&
           tx_fail_other == o0 + 1);
    assert(txnfree == NTXFBUF); /* every buffer back */

    /* AP_STATUS's last block: those three, then the core's lost beacons and its kinds */
    unsigned x[WIFI_AP_EXTRA2];
    volatile unsigned *corerec = (volatile unsigned *)(uintptr_t)(0x30000000u + 0x100);
    for (unsigned i = 0; i <= 2 * WIFI_AP_KINDS; i++)
        corerec[CORE_RC_APTYPE + i] = 300 + i;
    MB16(-8) = 0x100 >> 2;
    wifi_ap_extra2(x);
    assert(x[0] == tx_fail_beacon && x[1] == tx_fail_presp && x[2] == tx_fail_other);
    assert(x[3] == 300 + 2 * WIFI_AP_KINDS);              /* RC_APBLOST */
    assert(x[4] == 300 && x[4 + WIFI_AP_KINDS] == 300 + WIFI_AP_KINDS && x[WIFI_AP_EXTRA2 - 1] == 300 + 2 * WIFI_AP_KINDS - 1);
    MB16(-8) = 0;
    wifi_ap_extra2(x);
    assert(x[3] == 0 && x[4] == 0 && x[0] == tx_fail_beacon);
    wifi_ap_stop();
    printf("ok: the setup AP sends a phone data at the rates both have, and reports its nulls and failures\n");
}

/* An open network: the association request has no security IE and no Privacy bit, JOIN_DONE
 * says no privacy, the link is up once the AP associated (no handshake), data goes out without the
 * Protected bit and frames in the clear come in. A PSK profile does not join it. */
static void open_network(void)
{
    static const unsigned char open_ap[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x07};
    static const unsigned char seed0[32] = {0};
    static const unsigned char ies[] = {0, 4, 'C', 'a', 'f', 'e', 1, 4, 0x82, 0x84, 0x8b, 0x96, 3, 1, 6};
    cmd_ring(63);
    tx_frames();
    nbss = 1;
    memset(&bsstab[0], 0, sizeof bsstab[0]);
    memcpy(bsstab[0].bssid, open_ap, 6), memcpy(bsstab[0].ssid, "Cafe", 4);
    bsstab[0].ssidlen = 4, bsstab[0].chan = 6, bsstab[0].caps = 0x0401, bsstab[0].rssi = -50;
    memcpy(bsstab[0].ies, ies, sizeof ies), bsstab[0].ielen = sizeof ies;
    state = WIFI_IDLE, now = 30000;
    /* a PSK profile: only an open network has the SSID, so the join fails on security */
    unsigned char k[32];
    memset(k, 1, sizeof k);
    assert(wifi_join((const unsigned char *)"Cafe", 4, WIFI_SEC_PSK, k, 32, seed0, 0, 0) == 0);
    assert(state == WIFI_SCANNING);   /* nothing it can join yet: it scans; then the attempt */
    state = WIFI_JOINING;
    join_attempt();
    assert(state == WIFI_FAILED && fail_step == WIFI_FAIL_SECURITY);
    /* an open profile with a key, or a PSK one without, is refused */
    assert(wifi_join((const unsigned char *)"Cafe", 4, WIFI_SEC_OPEN, k, 32, seed0, 0, 0) == -1);
    assert(wifi_join((const unsigned char *)"Cafe", 4, WIFI_SEC_PSK, k, 0, seed0, 0, 0) == -1);
    state = WIFI_IDLE;
    assert(wifi_join((const unsigned char *)"Cafe", 4, WIFI_SEC_OPEN, 0, 0, seed0, 0, 0) == 0);
    assert(state == WIFI_JOINING && jstep == J_SETUP && same(cur.bssid, open_ap, 6));
    corebd = 1;
    join_step();
    assert(jstep == J_AUTH);
    /* JOIN_DONE (0xb8) went out with privacy 0 */
    int seen = 0;
    for (unsigned i = 1; i + 3 < 64; i++)
        if (cmdbuf[i] == CMD_JOIN_DONE && cmdbuf[i + 1] == (0x12 | 11u << 16)) {
            assert(((unsigned char *)(cmdbuf + i + 2))[0] == 0);
            seen = 1;
        }
    assert(seen);
    rxauthok = 1;
    join_step();
    assert(jstep == J_ASSOC);
    const unsigned char *d = last_frame() + 8, *b = d + 0xc4;
    assert(d[0x18] == 0x00 && b[0] == 0x01 && b[1] == 0x04);  /* ESS + short slot, no Privacy */
    unsigned blen = 4 + 6 + 10 + 6, rec = 0;
    for (unsigned i = 0; i < 28; i++)                        /* the frame's ring record */
        if (frmbuf[i * 6] == txfbuf[txnfree])
            rec = frmbuf[i * 6 + 2] >> 16;
    assert(rec == blen + 0xc4 && b[blen - 6] == 50);         /* no RSN IE after the rates */
    rxassocok = 1;
    join_step();
    assert(state == WIFI_UP);
    struct wifi_bss o;
    wifi_current(&o);
    assert(o.sec == WIFI_BSS_OPEN && o.grp == 0);
    /* a frame from the SCM goes out in the clear */
    unsigned char frame[14 + 20] = {0};
    memcpy(frame, open_ap, 6), memcpy(frame + 6, own, 6), frame[12] = 0x08;
    tx_frames();
    assert(radio_tx_eth(frame, sizeof frame) == 0);
    d = last_frame() + 8;
    assert(d[0x18] == 0x08 && d[0x19] == 0x01);
    /* a data frame in the clear from the AP reaches the SCM; a protected one does not */
    static unsigned char rxo[0xcc + 8 + 20];
    static const unsigned char llc[8] = {0xaa, 0xaa, 3, 0, 0, 0, 0x08, 0x00};
    memset(rxo, 0, sizeof rxo);
    rxo[0x94] = 0x18, rxo[0x97] = 0xc4;
    rxo[0xac] = 0x08, rxo[0xad] = 0x02;
    memcpy(rxo + 0xac + 4, own, 6), memcpy(rxo + 0xac + 10, open_ap, 6), memcpy(rxo + 0xac + 16, open_ap, 6);
    memcpy(rxo + 0xcc, llc, 8);
    rxo[0xcc + 8] = 0x45, rxo[0xcc + 8 + 3] = 20;
    unsigned before = wifi_rx_frames;
    rx_station(rxo, sizeof rxo);
    assert(wifi_rx_frames == before + 1);
    rxo[0xad] |= 0x40;
    rx_station(rxo, sizeof rxo);
    assert(wifi_rx_frames == before + 1);
    wifi_leave();
    nbss = 0;
    printf("ok: an open network joins without a handshake and carries data in the clear\n");
}

/* A data frame from the AP that the core passed up under one of the app's keys (RX_SOFT): the
 * MSDU (LLC/SNAP IPv4 and 20 bytes) enciphered as TKIP under tk and mic_key with TSC tsc, to da. */
static unsigned char rxt[0xcc + 8 + 8 + 20 + 12];
static unsigned rx_tkip(const unsigned char *da, const unsigned char *tk, const unsigned char *mic_key,
                        unsigned keyid, unsigned long long tsc)
{
    static const unsigned char llc[8] = {0xaa, 0xaa, 3, 0, 0, 0, 0x08, 0x00};
    memset(rxt, 0, sizeof rxt);
    rxt[0x94] = 0x18, rxt[0x97] = 0xc4, rxt[0x94 + 7] = RX_SOFT;
    rxt[0xac] = 0x08, rxt[0xad] = 0x42;
    memcpy(rxt + 0xac + 4, da, 6), memcpy(rxt + 0xac + 10, AP, 6), memcpy(rxt + 0xac + 16, AP, 6);
    unsigned char *m = rxt + 0xcc + WSEC_TKIP_HDR;
    memcpy(m, llc, 8);
    m[8] = 0x45, m[8 + 3] = 20;
    return 0xcc + wsec_tkip_protect(tk, mic_key, keyid, tsc, AP, da, AP, rxt + 0xcc, 8 + 20);
}

/* TKIP in the app (ADR 0136): a mixed-mode router's TKIP group key is installed as the app's (the
 * core gets the cipher and no key bytes) and its broadcasts reach the SCM, a replayed TSC does not;
 * under a TKIP pairwise key frames go out enciphered by the app (TX_RAW, the STA-to-AP MIC key, the
 * TSC counting up) and come in deciphered; a Michael failure is reported to the AP, and a second
 * within a minute deauthenticates with reason 14 and keeps TKIP networks out for a minute. */
static void tkip_frames(void)
{
    static const unsigned char own_mac[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55};
    unsigned char gtk[32], ptk[32];
    for (unsigned i = 0; i < 32; i++)
        gtk[i] = (unsigned char)(0xa0 + i), ptk[i] = (unsigned char)(0x40 + i);
    copy(own, own_mac, 6);
    cmd_ring(63);
    tx_frames();
    memset(&sm, 0, sizeof sm);
    copy(cur.bssid, AP, 6), have_cur = 1, state = WIFI_UP, prof_sec = WIFI_SEC_PSK, now = 40000;
    keyed = 1, ap_keyed = 0;
    /* the group key from the handshake: TKIP, index 2 */
    sm.group = 2, sm.gtk_len = 32, sm.gtk_idx = 2;
    memcpy(sm.gtk, gtk, 32);
    sm.gtk_rsc[0] = 6; /* the Key RSC: the AP's broadcasts up to TSC 6 came before the key */
    keys_due = WPA_RX_INSTALL_GTK;
    install_keys();
    assert(!keys_due && sw_grp[2].cipher == WSEC_TKIP && !memcmp(sw_grp[2].key, gtk, 32));
    assert(sw_grp[2].rxtsc == 6);
    int seen = 0;
    for (unsigned i = 1; i + 12 < 64; i++)
        if (cmdbuf[i] == 11 && cmdbuf[i + 1] == (18u | 1u << 16)) {
            const unsigned char *pl = (const unsigned char *)(cmdbuf + i + 2);
            assert(pl[0] == WSEC_TKIP && pl[4] == 2 && pl[24] == 6); /* the RSC goes along */
            for (unsigned k = 0; k < 16; k++)
                assert(pl[8 + k] == 0); /* the key stays in the app */
            seen = 1;
        }
    assert(seen);
    /* a broadcast under it reaches the SCM; the same TSC again does not, nor one up to the RSC */
    unsigned before = wifi_rx_frames;
    rx_station(rxt, rx_tkip(ap_bcast, gtk, gtk + 16, 2, 6));
    assert(wifi_rx_frames == before);
    rx_station(rxt, rx_tkip(ap_bcast, gtk, gtk + 16, 2, 7));
    assert(wifi_rx_frames == before + 1);
    rx_station(rxt, rx_tkip(ap_bcast, gtk, gtk + 16, 2, 7));
    assert(wifi_rx_frames == before + 1);
    rx_station(rxt, rx_tkip(ap_bcast, gtk, gtk + 16, 2, 8));
    assert(wifi_rx_frames == before + 2);
    /* a key id without a key: dropped */
    rx_station(rxt, rx_tkip(ap_bcast, gtk, gtk + 16, 1, 9));
    assert(wifi_rx_frames == before + 2);

    /* a TKIP pairwise key: TX enciphered by the app, the TSC from 1 */
    sw_pair.cipher = WSEC_TKIP, sw_pair.tsc = sw_pair.rxtsc = 0;
    memcpy(sw_pair.key, ptk, 32);
    unsigned char frame[14 + 28] = {0};
    memcpy(frame, AP, 6), memcpy(frame + 6, own, 6), frame[12] = 0x08;
    for (unsigned i = 14; i < sizeof frame; i++)
        frame[i] = (unsigned char)i;
    for (unsigned t = 1; t <= 2; t++) {
        tx_frames();
        assert(radio_tx_eth(frame, sizeof frame) == 0);
        const unsigned char *d = last_frame() + 8;
        unsigned char body[8 + 8 + 28 + 12];
        assert(d[0x18] == 0x08 && d[0x19] == 0x41 && d[0x13] == 1 && last_frame()[0x94 + 0x13] == 1);
        memcpy(body, d + 0xc4, sizeof body);
        assert(wsec_extiv(body) && wsec_tkip_tsc(body) == t);
        assert(wsec_tkip_open(ptk, ptk + 24, own, AP, own, body, sizeof body) == 8 + 28);
        assert(body[8] == 0xaa && body[8 + 6] == 0x08 && !memcmp(body + 16, frame + 14, 28));
    }
    /* and RX under it, with the AP-to-STA MIC key */
    before = wifi_rx_frames;
    rx_station(rxt, rx_tkip(own, ptk, ptk + 16, 0, 1));
    assert(wifi_rx_frames == before + 1 && ap_keyed);
    /* EAPOL once the AP uses the key: enciphered by the app too */
    tx_frames();
    unsigned char e[O_KD] = {2, 3};
    assert(send_eapol(e, sizeof e));
    assert(last_frame()[8 + 0x13] == 1 && last_frame()[8 + 0x12] == 1);

    /* a Michael failure (ICV good, MIC key wrong): reported to the AP, nothing to the SCM */
    sm.ptk_set = 1;
    tx_frames();
    rx_station(rxt, rx_tkip(ap_bcast, gtk, ptk, 2, 20));
    assert(wifi_rx_frames == before + 1 && mic_fail_seen && state == WIFI_UP);
    unsigned char rep[8 + 8 + O_KD + 12];
    memcpy(rep, last_frame() + 8 + 0xc4, sizeof rep);
    assert(wsec_tkip_open(ptk, ptk + 24, own, AP, own, rep, sizeof rep) == 8 + O_KD);
    unsigned ki = be16(rep + 16 + O_KEYINFO);
    assert((ki & (KI_ERROR | KI_REQUEST | KI_MIC)) == (KI_ERROR | KI_REQUEST | KI_MIC) && !(ki & KI_PAIRWISE));
    /* the second within a minute: deauthentication 14, link down, TKIP networks kept out */
    now += 30000;
    tx_frames();
    rx_station(rxt, rx_tkip(ap_bcast, gtk, ptk, 2, 21));
    assert(state == WIFI_FAILED && cm_on && fail_step == WIFI_FAIL_KICKED && fail_code == 14);
    const unsigned char *d = last_frame() + 8;
    assert(d[0x18] == 0xc0 && d[0xc4] == 14 && d[0xc5] == 0);
    assert(!sw_pair.cipher && !sw_grp[2].cipher && !keyed);
    nbss = 1;
    memset(&bsstab[0], 0, sizeof bsstab[0]);
    memcpy(bsstab[0].bssid, AP, 6), memcpy(bsstab[0].ssid, "Home", 4);
    bsstab[0].ssidlen = 4, bsstab[0].chan = 6, bsstab[0].rsn = 1, bsstab[0].rsnok = 1, bsstab[0].grp = 2;
    memcpy(cur.ssid, "Home", 4), cur.ssidlen = 4;
    state = WIFI_JOINING;
    join_attempt();
    assert(state == WIFI_FAILED && fail_step == WIFI_FAIL_SECURITY);
    now += CM_HOLD_MS;
    assert(match(&bsstab[0]));
    cm_on = 0, mic_fail_seen = 0, nbss = 0, have_cur = 0, state = WIFI_IDLE;
    printf("ok: TKIP group and pairwise frames in the app, MIC failures and countermeasures\n");
}

/* Which security a PSK profile joins with (ADR 0136), as wpa_supplicant picks it: RSN over WPA, in
 * either pairwise CCMP over TKIP. The association request carries that IE, Message 2 repeats it,
 * and a TKIP PTK stays in the app. */
static void choose_security(void)
{
    static const unsigned char rates_ies[] = {0, 4, 'H', 'o', 'm', 'e', 1, 4, 0x82, 0x84, 0x8b, 0x96, 3, 1, 6};
    static const struct {
        unsigned char rsnok, rpair, grp, wpaok, wpair, wgrp, proto, pair, group;
        const char *what;
    } v[] = {
        {1, BSS_CCMP | BSS_TKIP, 2, 1, BSS_TKIP, 2, WPA_PROTO_RSN, WPA_CIPHER_CCMP, 2, "mixed: RSN, CCMP"},
        {1, BSS_TKIP, 2, 0, 0, 0, WPA_PROTO_RSN, WPA_CIPHER_TKIP, 2, "WPA2 with TKIP only"},
        {0, 0, 0, 1, BSS_TKIP, 2, WPA_PROTO_WPA, WPA_CIPHER_TKIP, 2, "WPA only, TKIP"},
        {0, 0, 0, 1, BSS_CCMP | BSS_TKIP, 2, WPA_PROTO_WPA, WPA_CIPHER_CCMP, 2, "WPA only, CCMP offered"},
    };
    unsigned char k[32] = {1}, sn0[32] = {0};
    for (unsigned t = 0; t < sizeof v / sizeof v[0]; t++) {
        cmd_ring(63);
        tx_frames();
        nbss = 1;
        memset(&bsstab[0], 0, sizeof bsstab[0]);
        memcpy(bsstab[0].bssid, AP, 6), memcpy(bsstab[0].ssid, "Home", 4);
        bsstab[0].ssidlen = 4, bsstab[0].chan = 6, bsstab[0].caps = 0x0411, bsstab[0].rssi = -50;
        bsstab[0].rsn = v[t].rsnok, bsstab[0].rsnok = v[t].rsnok, bsstab[0].rpair = v[t].rpair;
        bsstab[0].grp = v[t].grp, bsstab[0].rakm = BSS_PSK;
        bsstab[0].wpa = v[t].wpaok, bsstab[0].wpaok = v[t].wpaok, bsstab[0].wpair = v[t].wpair;
        bsstab[0].wgrp = v[t].wgrp, bsstab[0].wakm = BSS_PSK;
        memcpy(bsstab[0].ies, rates_ies, sizeof rates_ies), bsstab[0].ielen = sizeof rates_ies;
        state = WIFI_IDLE, now = 50000 + t * 1000;
        assert(wifi_join((const unsigned char *)"Home", 4, WIFI_SEC_PSK, k, 32, sn0, 0, 0) == 0);
        assert(state == WIFI_JOINING);
        if (cur.proto != v[t].proto || cur.pair != v[t].pair || cur.grp != v[t].group) {
            printf("%s: proto %u pair %u grp %u\n", v[t].what, cur.proto, cur.pair, cur.grp);
            assert(0);
        }
        corebd = 1;
        join_step();
        rxauthok = 1;
        join_step();
        assert(jstep == J_ASSOC);
        unsigned char ie[WPA_IE_MAX];
        unsigned n = wpa_own_ie(ie, v[t].proto, v[t].group, v[t].pair, WPA_AKM_PSK, 0);
        const unsigned char *b = last_frame() + 8 + 0xc4;
        assert(b[0] == 0x11 && !memcmp(b + 4 + 6 + 10 + 6, ie, n)); /* Privacy; the IE after the rates */
        rxassocok = 1;
        join_step();
        assert(jstep == J_4WAY && sm.proto == v[t].proto && sm.pairwise == v[t].pair &&
               sm.ielen == n && !memcmp(sm.ie, ie, n));
        /* the PTK: CCMP to the engine, TKIP into the app */
        for (unsigned i = 0; i < 64; i++)
            sm.ptk[i] = (unsigned char)i;
        keys_due = WPA_RX_INSTALL_PTK;
        install_keys();
        assert(keyed && (v[t].pair == WPA_CIPHER_TKIP ? sw_pair.cipher == WSEC_TKIP && !memcmp(sw_pair.key, sm.ptk + 32, 32)
                                                    : !sw_pair.cipher));
        wifi_leave();
        assert(!sw_pair.cipher);
    }
    nbss = 0;
    printf("ok: a PSK profile picks RSN over WPA and CCMP over TKIP, and says so in its IE\n");
}

/* WEP (ADR 0136): shared key authentication answers the AP's challenge enciphered under the
 * profile's key (TX_RAW, Protected), the association carries Privacy and no IE, the key goes to
 * the core as a mark for its index, data goes out under WEP with the IV counting up and comes in
 * deciphered; with "auto" an AP that refuses open system (status 13) gets shared key. */
static void wep_network(void)
{
    static const unsigned char wap[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x0e};
    static const unsigned char ies[] = {0, 3, 'O', 'l', 'd', 1, 4, 0x82, 0x84, 0x8b, 0x96, 3, 1, 1};
    unsigned char prof[2 + 13], seed0[32] = {0x10, 0x20, 0x30};
    for (unsigned t = 0; t < 2; t++) {
        cmd_ring(63);
        tx_frames();
        nbss = 1;
        memset(&bsstab[0], 0, sizeof bsstab[0]);
        memcpy(bsstab[0].bssid, wap, 6), memcpy(bsstab[0].ssid, "Old", 3);
        bsstab[0].ssidlen = 3, bsstab[0].chan = 1, bsstab[0].caps = 0x0011, bsstab[0].rssi = -60;
        memcpy(bsstab[0].ies, ies, sizeof ies), bsstab[0].ielen = sizeof ies;
        prof[0] = 1, prof[1] = t ? WIFI_WEP_AUTO : WIFI_WEP_SHARED;
        for (unsigned i = 0; i < 13; i++)
            prof[2 + i] = (unsigned char)(0x41 + i);
        state = WIFI_IDLE, now = 60000 + 10000 * t;
        assert(wifi_join((const unsigned char *)"Old", 3, WIFI_SEC_WEP, prof, 3, seed0, 0, 0) == -1); /* key short */
        prof[0] = 4;
        assert(wifi_join((const unsigned char *)"Old", 3, WIFI_SEC_WEP, prof, 15, seed0, 0, 0) == -1); /* index */
        prof[0] = 1;
        assert(wifi_join((const unsigned char *)"Old", 3, WIFI_SEC_WEP, prof, 15, seed0, 0, 0) == 0);
        assert(state == WIFI_JOINING && same(cur.bssid, wap, 6) && wep_len == 13 && wep_idx == 1);
        corebd = 1;
        join_step();
        const unsigned char *d = last_frame() + 8, *b = d + 0xc4;
        if (t) { /* auto: open system first, refused with status 13 */
            assert(d[0x18] == 0xb0 && b[0] == 0 && b[2] == 1);
            unsigned char refuse[6] = {0, 0, 2, 0, 13, 0};
            rx_station(rxf, rx_mgmt(0xb0, own, wap, wap, refuse, sizeof refuse));
            tx_frames();
            join_step();
            d = last_frame() + 8, b = d + 0xc4;
        }
        assert(d[0x18] == 0xb0 && b[0] == 1 && b[1] == 0 && b[2] == 1 && b[4] == 0); /* shared key, seq 1 */
        /* the challenge: transaction 2, IE 16 with 128 bytes */
        unsigned char ch[6 + 2 + 128] = {1, 0, 2, 0, 0, 0, 16, 128};
        for (unsigned i = 0; i < 128; i++)
            ch[8 + i] = (unsigned char)(i * 3 + 7);
        static unsigned char rxc[0xcc + sizeof ch];
        memset(rxc, 0, sizeof rxc);
        rxc[0x94] = 0x18, rxc[0x97] = 0xc4, rxc[0xac] = 0xb0;
        memcpy(rxc + 0xac + 4, own, 6), memcpy(rxc + 0xac + 10, wap, 6), memcpy(rxc + 0xac + 16, wap, 6);
        memcpy(rxc + 0xcc, ch, sizeof ch);
        rx_station(rxc, sizeof rxc);
        assert(challenge_due && challenge_len == 128);
        tx_frames();
        join_step();
        d = last_frame() + 8, b = d + 0xc4;
        assert(d[0x18] == 0xb0 && d[0x19] == 0x40 && d[0x13] == 1 && last_frame()[0x94 + 0x13] == 1);
        unsigned char body3[4 + 6 + 2 + 128 + 4];
        memcpy(body3, b, sizeof body3);
        assert(wsec_keyid(body3) == 1 && !wsec_extiv(body3));
        assert(wsec_wep_open(prof + 2, 13, body3, sizeof body3) == 6 + 2 + 128);
        assert(body3[4] == 1 && body3[6] == 3 && body3[8] == 0 && body3[10] == 16 && body3[11] == 128 &&
               !memcmp(body3 + 12, ch + 8, 128));
        /* transaction 4: authenticated; the association carries Privacy and no IE */
        unsigned char ok4[6] = {1, 0, 4, 0, 0, 0};
        rx_station(rxf, rx_mgmt(0xb0, own, wap, wap, ok4, sizeof ok4));
        assert(rxauthok);
        tx_frames();
        join_step();
        d = last_frame() + 8, b = d + 0xc4;
        assert(jstep == J_ASSOC && d[0x18] == 0x00 && (b[0] & 0x10));
        rxassocok = 1;
        cmd_ring(63);
        join_step();
        assert(state == WIFI_UP && keyed && sw_grp[1].cipher == WSEC_WEP104 && !memcmp(sw_grp[1].key, prof + 2, 13));
        int seen = 0;
        for (unsigned i = 1; i + 12 < 64; i++)
            if (cmdbuf[i] == 11 && cmdbuf[i + 1] == (18u | 1u << 16)) {
                const unsigned char *pl = (const unsigned char *)(cmdbuf + i + 2);
                assert(pl[0] == WSEC_WEP104 && pl[4] == 1 && !pl[8]);
                seen = 1;
            }
        assert(seen);
        /* data out under WEP, the IV counting up from the seed */
        unsigned char frame[14 + 24] = {0};
        memcpy(frame, wap, 6), memcpy(frame + 6, own, 6), frame[12] = 0x08;
        unsigned iv0 = wep_iv;
        for (unsigned k = 0; k < 2; k++) {
            tx_frames();
            assert(radio_tx_eth(frame, sizeof frame) == 0);
            d = last_frame() + 8;
            unsigned char bd[4 + 8 + 24 + 4];
            memcpy(bd, d + 0xc4, sizeof bd);
            assert(d[0x19] == 0x41 && d[0x13] == 1);
            assert((bd[0] | bd[1] << 8 | bd[2] << 16) == ((iv0 + k) & 0xffffff));
            assert(wsec_wep_open(prof + 2, 13, bd, sizeof bd) == 8 + 24 && bd[4] == 0xaa && bd[10] == 0x08);
        }
        /* data in: WEP under key 1 from the AP reaches the SCM; under key 2 it does not */
        static unsigned char rxw[0xcc + 4 + 8 + 20 + 4];
        static const unsigned char llc[8] = {0xaa, 0xaa, 3, 0, 0, 0, 0x08, 0x00};
        for (unsigned keyid = 1; keyid <= 2; keyid++) {
            memset(rxw, 0, sizeof rxw);
            rxw[0x94] = 0x18, rxw[0x97] = 0xc4, rxw[0x94 + 7] = RX_SOFT;
            rxw[0xac] = 0x08, rxw[0xad] = 0x42;
            memcpy(rxw + 0xac + 4, own, 6), memcpy(rxw + 0xac + 10, wap, 6), memcpy(rxw + 0xac + 16, wap, 6);
            memcpy(rxw + 0xcc + 4, llc, 8);
            rxw[0xcc + 12] = 0x45, rxw[0xcc + 15] = 20;
            wsec_wep_protect(prof + 2, 13, keyid, 0x123456, rxw + 0xcc, 8 + 20);
            unsigned before = wifi_rx_frames;
            rx_station(rxw, sizeof rxw);
            assert(wifi_rx_frames == before + (keyid == 1));
        }
        wifi_leave();
        assert(!wep_len && !sw_grp[1].cipher);
    }
    nbss = 0;
    printf("ok: WEP joins with shared key (also after auto's open system), data both ways\n");
}

/* A management frame from the AP to dst (us or everybody), as the core passes it up. */
static unsigned char rxm[0xcc + 64];
static unsigned rx_from_ap(unsigned fc, const unsigned char *dst, const unsigned char *body, unsigned n)
{
    memset(rxm, 0, sizeof rxm);
    rxm[0x94] = 0x18, rxm[0x97] = 0xc4;
    rxm[0xac] = (unsigned char)fc, rxm[0xad] = (unsigned char)(fc >> 8);
    memcpy(rxm + 0xac + 4, dst, 6), memcpy(rxm + 0xac + 10, AP, 6), memcpy(rxm + 0xac + 16, AP, 6);
    rxm[0xac + 22] = 0x40;
    memcpy(rxm + 0xcc, body, n);
    return 0xcc + n;
}

/* The AP's robust frame under PMF: CCMP under tk with PN pn (unicast), or BIP under the IGTK. */
static unsigned rx_protected(unsigned fc, const unsigned char *body, unsigned n, const unsigned char *tk,
                             unsigned long long pn)
{
    unsigned char f[64];
    unsigned len = rx_from_ap(fc | 0x4000, own, body, n);
    memcpy(f + 8, body, n);
    unsigned m = wsec_ccmp_protect(tk, rxm + 0xac, pn, f, n);
    memcpy(rxm + 0xcc, f, m);
    (void)len;
    return 0xcc + m;
}

/* WPA3 (ADR 0136): an SAE profile asks the SCM for SAE with the AP's BSSID and SSID, passes the
 * AP's SAE frames up and sends the SCM's; with the PMK and the result it associates with AKM 8 and
 * MFPC, and runs the 4-way with that PMK. Under PMF an unprotected deauthentication starts an SA
 * Query (protected) instead of ending the link, which a protected answer stops and silence ends;
 * a protected one (CCMP) ends it, a replayed one does not; a deauthentication to everybody needs a
 * valid MMIE; the AP's SA Query is answered, protected. */
static void wpa3_and_pmf(void)
{
    static const unsigned char own_mac[6] = {0x02, 0x11, 0x22, 0x33, 0x44, 0x55};
    static const unsigned char ies[] = {0, 3, 'w', 'p', '3', 1, 4, 0x82, 0x84, 0x8b, 0x96, 3, 1, 6};
    unsigned char seed0[32] = {0}, msg[600], spmk[32];
    copy(own, own_mac, 6);
    cmd_ring(63);
    tx_frames();
    nbss = 1;
    memset(&bsstab[0], 0, sizeof bsstab[0]);
    memcpy(bsstab[0].bssid, AP, 6), memcpy(bsstab[0].ssid, "wp3", 3);
    bsstab[0].ssidlen = 3, bsstab[0].chan = 6, bsstab[0].caps = 0x0411, bsstab[0].rssi = -50;
    bsstab[0].rsn = 1, bsstab[0].grp = 4, bsstab[0].rpair = BSS_CCMP, bsstab[0].rakm = BSS_SAE, bsstab[0].rcaps = 0xc0;
    memcpy(bsstab[0].ies, ies, sizeof ies), bsstab[0].ielen = sizeof ies;
    state = WIFI_IDLE, now = 90000;
    /* a PSK profile does not join a WPA3-only network: it says so */
    const unsigned char k[32] = {1};
    assert(wifi_join((const unsigned char *)"wp3", 3, WIFI_SEC_PSK, k, 32, seed0, 0, 0) == 0 && state == WIFI_SCANNING);
    state = WIFI_JOINING;
    join_attempt();
    assert(state == WIFI_FAILED && fail_step == WIFI_FAIL_SECURITY);
    state = WIFI_IDLE;
    assert(wifi_join((const unsigned char *)"wp3", 3, WIFI_SEC_SAE, 0, 0, seed0, 0, 0) == 0);
    assert(state == WIFI_JOINING && cur.akm == WPA_AKM_SAE && cur.mfp);
    corebd = 1;
    join_step();
    assert(jstep == J_SAE);
    int n = wifi_sae_next(msg, sizeof msg);
    assert(n == 1 + 6 + 1 + 3 && msg[0] == WIFI_SAE_START && !memcmp(msg + 1, AP, 6) && msg[7] == 3 &&
           !memcmp(msg + 8, "wp3", 3));
    /* the AP's commit goes up whole; another algorithm is not SAE's */
    unsigned char commit[6 + 2 + 32 + 64] = {3, 0, 1, 0, 0, 0, 19, 0};
    rx_station(rxm, rx_from_ap(0xb0, own, commit, sizeof commit));
    n = wifi_sae_next(msg, sizeof msg);
    assert(n == 1 + 24 + (int)sizeof commit && msg[0] == WIFI_SAE_FRAME && msg[1] == 0xb0 && msg[25] == 3);
    /* the SCM's commit goes out to the AP */
    tx_frames();
    assert(wifi_sae_tx(commit, sizeof commit) == 0);
    const unsigned char *d = last_frame() + 8;
    assert(d[0x18] == 0xb0 && !memcmp(d + 0x1c, AP, 6) && d[0xc4] == 3 && d[0xc4 + 2] == 1);
    assert(wifi_sae_tx(ies, 6) == -1);                      /* not an SAE frame body */
    /* the PMK, then the result: association with AKM 8 and MFPC */
    for (unsigned i = 0; i < 32; i++)
        spmk[i] = (unsigned char)(0x90 + i);
    static const unsigned char other[6] = {2, 0, 0, 0, 0, 9};
    assert(wifi_set_pmk(other, spmk) == -1);
    assert(wifi_set_pmk(AP, spmk) == 0 && !memcmp(pmk, spmk, 32));
    assert(wifi_sae_done(0, AP) == 0);
    tx_frames();
    join_step();
    assert(jstep == J_ASSOC);
    d = last_frame() + 8;
    unsigned char ie[WPA_IE_MAX];
    unsigned iel = wpa_own_ie(ie, WPA_PROTO_RSN, 4, 4, WPA_AKM_SAE, WPA_CAP_MFPC | WPA_CAP_MFPR);
    assert(d[0x18] == 0x00 && !memcmp(d + 0xc4 + 4 + 5 + 10 + 6, ie, iel));
    rxassocok = 1;
    join_step();
    assert(jstep == J_4WAY && sm.akm == WPA_AKM_SAE && sm.mfp && sm.ver == 0 && !memcmp(sm.pmk, spmk, 32));
    /* a refused SAE: the attempt fails with the status */
    jstep = J_SAE, sae_result = 0, deadline = now + 1000;
    assert(wifi_sae_done(1, AP) == 0 && sae_result == 2);

    /* ---- PMF, the link up ---- */
    unsigned char tk[16], gk[16];
    for (unsigned i = 0; i < 16; i++)
        tk[i] = (unsigned char)(0x30 + i), gk[i] = (unsigned char)(0x70 + i);
    state = WIFI_UP, keyed = 1, kicked = 0, pmf = 1, saq_on = 0;
    memcpy(mgmt_tk, tk, 16), mgmt_pn = MGMT_PN0, mgmt_rxpn = 0;
    memcpy(igtk, gk, 16), igtk_id = 4, ipn = 10;
    const unsigned char reason[2] = {7, 0};
    /* an unprotected deauthentication: an SA Query, not the end */
    tx_frames();
    rx_station(rxm, rx_from_ap(0xc0, own, reason, 2));
    assert(!kicked && saq_on);
    d = last_frame() + 8;
    unsigned char q[8 + 4 + 8], hq[24];
    memcpy(hq, d + 0x18, 24), memcpy(q, d + 0xc4, sizeof q);
    assert(d[0x18] == 0xd0 && d[0x19] == 0x40 && d[0x13] == 1);
    assert(wsec_ccmp_open(tk, hq, q, sizeof q) == 4 && q[8] == 8 && q[9] == 0 && wsec_ccmp_pn(q) == MGMT_PN0 + 1);
    /* the AP's protected answer with that id: the frame was forged, the link stays */
    const unsigned char ans[4] = {8, 1, q[10], q[11]};
    rx_station(rxm, rx_protected(0xd0, ans, 4, tk, 1));
    assert(!saq_on && !kicked);
    /* again, and no answer: after SAQ_MS the link ends with the frame's reason */
    rx_station(rxm, rx_from_ap(0xc0, own, reason, 2));
    assert(saq_on);
    now += SAQ_MS;
    saq_check();
    assert(kicked && kick_sub == 0xc0 && kick_reason == 7 && !saq_on);
    kicked = 0;
    /* every TX buffer taken: the query waits for one, and SAQ_MS counts from when it went out,
     * so a forged frame while the transmit queue is full is no disconnect */
    unsigned held_free = txnfree, sent = wifi_tx_frames;
    txnfree = 0;
    rx_station(rxm, rx_from_ap(0xc0, own, reason, 2));
    assert(saq_on && !saq_sent && wifi_tx_frames == sent);
    now += 3 * SAQ_MS;
    saq_check();
    assert(!kicked && saq_on && !saq_sent);
    txnfree = held_free;
    saq_check();
    assert(saq_sent && wifi_tx_frames == sent + 1);
    d = last_frame() + 8;
    memcpy(hq, d + 0x18, 24), memcpy(q, d + 0xc4, sizeof q);
    assert(wsec_ccmp_open(tk, hq, q, sizeof q) == 4 && q[8] == 8 && q[9] == 0);
    now += SAQ_MS - 1;
    saq_check();
    assert(!kicked && saq_on);
    const unsigned char ans2[4] = {8, 1, q[10], q[11]};
    rx_station(rxm, rx_protected(0xd0, ans2, 4, tk, 3));
    assert(!saq_on && !kicked);
    /* a protected deauthentication ends it; the same PN again does not */
    rx_station(rxm, rx_protected(0xc0, reason, 2, tk, 4));
    assert(kicked && kick_reason == 7);
    kicked = 0;
    rx_station(rxm, rx_protected(0xc0, reason, 2, tk, 4));
    assert(!kicked);
    /* a deauthentication to everybody: only with a valid MMIE and a newer IPN */
    rx_station(rxm, rx_from_ap(0xc0, ap_bcast, reason, 2));
    assert(!kicked && !saq_on);
    unsigned char bb[2 + WSEC_MMIE_LEN] = {3, 0};
    rx_from_ap(0xc0, ap_bcast, reason, 2);
    wsec_bip_protect(gk, 4, 11, rxm + 0xac, bb, 2);
    unsigned bl = rx_from_ap(0xc0, ap_bcast, bb, sizeof bb);
    rx_station(rxm, bl);
    assert(kicked && kick_reason == 3 && ipn == 11);
    kicked = 0;
    rx_station(rxm, bl);
    assert(!kicked); /* the same IPN */
    /* the AP asks (protected): a protected answer with its id */
    tx_frames();
    const unsigned char ask[4] = {8, 0, 0x34, 0x12};
    rx_station(rxm, rx_protected(0xd0, ask, 4, tk, 5));
    d = last_frame() + 8;
    memcpy(hq, d + 0x18, 24), memcpy(q, d + 0xc4, sizeof q);
    assert(wsec_ccmp_open(tk, hq, q, sizeof q) == 4 && q[8] == 8 && q[9] == 1 && q[10] == 0x34 && q[11] == 0x12);
    /* without PMF a protected management frame is not looked at */
    pmf = 0;
    rx_station(rxm, rx_protected(0xc0, reason, 2, tk, 9));
    assert(!kicked);
    wifi_leave();
    nbss = 0, state = WIFI_IDLE;
    printf("ok: WPA3 over the SCM's SAE, PMF with SA Query, CCMP and BIP on management frames\n");
}

int main(void)
{
    /* a full ring: no PEER_ADD and no 4-way yet; the next poll still has the step */
    cmd_ring(5); /* PEER_ADD is 3 header words + 3 payload words */
    associated();
    join_step();
    assert(jstep == J_ASSOC && state == WIFI_JOINING);
    assert(wr_avail(&tx, 0) == 5);
    for (unsigned i = 0; i < 6; i++)
        assert(cmdbuf[i] == 0); /* nothing half-written */

    /* room again: PEER_ADD for the AP in slot 0, then the 4-way */
    cmd_ring(6);
    join_step();
    assert(jstep == J_4WAY);
    /* a fresh ring's producer starts at slot 1 (wr_init) */
    assert(cmdbuf[1] == (1u | (10 + 8) << 16) && cmdbuf[2] == CMD_PEER_ADD &&
           cmdbuf[3] == (0x12 | 0x11u << 16));
    assert(!memcmp(cmdbuf + 4, AP, 6) && ((unsigned char *)(cmdbuf + 4))[6] == PEER_AP);
    assert(WREG[0x210 / 4] & 4); /* the doorbell rang */

    /* a ring that stays full: the attempt is given up at the assoc deadline (the last attempt
     * fails the join) instead of going on without PEER_ADD */
    cmd_ring(5);
    associated();
    jtries = JOIN_TRIES - 1;
    now = deadline - 1;
    join_step();
    assert(jstep == J_ASSOC && state == WIFI_JOINING);
    now = deadline;
    join_step();
    assert(jstep == J_ASSOC && state == WIFI_FAILED);

    printf("ok: PEER_ADD waits for room in the command ring before the 4-way\n");
    security_and_status();
    access_point();
    ap_full();
    ap_data();
    ap_rates_and_diag();
    link_frames();
    rekey_msg4_old_key();
    open_network();
    tkip_frames();
    choose_security();
    wep_network();
    wpa3_and_pmf();
    return 0;
}
