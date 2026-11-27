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
 * Message 3 itself comes in the clear and is let through to the supplicant.
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

void netcard_from_port(unsigned ch, const unsigned char *f, unsigned n)
{
    (void)ch, (void)f, (void)n;
}

__attribute__((constructor)) static void map_wreg(void)
{
    static int probe;
    /* the WiLD registers, and the UART page: a failed join says so in one line (board.h say) */
    static const unsigned long at[2] = {(unsigned long)WREG, 0xb1001000ul};
    for (int i = 0; i < 2; i++)
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
    now += 5000;
    ap_step();
    assert(ap_beacons == 3);                                   /* late: one beacon, not a burst */
    ap_step();
    assert(ap_beacons == 3);

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
    keys_due = WPA_RX_INSTALL_GTK;
    install_keys();
    assert(!keys_due && sw_grp[2].cipher == WSEC_TKIP && !memcmp(sw_grp[2].key, gtk, 32));
    int seen = 0;
    for (unsigned i = 1; i + 12 < 64; i++)
        if (cmdbuf[i] == 11 && cmdbuf[i + 1] == (18u | 1u << 16)) {
            const unsigned char *pl = (const unsigned char *)(cmdbuf + i + 2);
            assert(pl[0] == WSEC_TKIP && pl[4] == 2);
            for (unsigned k = 0; k < 16; k++)
                assert(pl[8 + k] == 0); /* the key stays in the app */
            seen = 1;
        }
    assert(seen);
    /* a broadcast under it reaches the SCM; the same TSC again does not */
    unsigned before = wifi_rx_frames;
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
    link_frames();
    open_network();
    tkip_frames();
    choose_security();
    wep_network();
    return 0;
}
