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
    state = WIFI_JOINING, fail_step = fail_code = 0;
    join_attempt();
    assert(state == WIFI_FAILED && fail_step == WIFI_FAIL_SECURITY);
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
    return 0;
}
