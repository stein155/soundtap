/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Wi-Fi station of the BCO network card (ADR 0031). Boots our own WiLD core (bco/wild/fw, ADR 0031,
 * linked in by wild_own.S), then runs scan, join, the WPA2-PSK 4-way and the data path from the
 * main loop, never blocking it for more than a few milliseconds. Every core command and frame
 * layout here was proven by the one-shot bring-up (ADR 0031, the former wild.c): boot and
 * handshake, rings, scan (0xc1/0xc2/0xe4), join (0xbc, JOIN_DONE), the auth and assoc frames,
 * PEER_ADD, the key commands and data frames both ways (DHCP over WPA2, 2026-10-06).
 * 2.4 GHz and WPA2-PSK (CCMP) only. */
#include "board.h"
#include "netcard.h"
#include "bcolink.h"
#include "wifi.h"
#include "wscan.h"
#include "wring.h"
#include "wstation.h"
#include "wpa.h"

#define WSRAM ((volatile unsigned char *)0x30000000)
#define WREG ((volatile unsigned *)0x30200000)
#define MB 0x3001fde4u
#define MB16(o) (*(volatile unsigned short *)(MB + (o)))
#define MB32(o) (*(volatile unsigned *)(MB + (o)))
#define CLK ((volatile unsigned *)0xe0007000)
#define DOORBELL() (WREG[0x210 / 4] |= 4)

/* GCC may emit these for aggregate copies even freestanding; nothing else here provides them. */
void *memcpy(void *d, const void *s, unsigned n)
{
    unsigned char *o = d;
    const unsigned char *i = s;
    while (n--)
        *o++ = *i++;
    return d;
}

void *memset(void *d, int c, unsigned n)
{
    unsigned char *o = d;
    while (n--)
        *o++ = (unsigned char)c;
    return d;
}

static unsigned crc32(const unsigned char *p, unsigned n)
{
    unsigned c = ~0u;
    while (n--) {
        c ^= *p++;
        for (int k = 0; k < 8; k++)
            c = c >> 1 ^ (0xedb88320u & -(c & 1));
    }
    return ~c;
}

/* ~1 ms busy wait; only used while booting the core, before the tick runs. */
static void ms(unsigned n)
{
    for (volatile unsigned i = 0; i < n * 2400; i++) {}
}

static unsigned wait_msg(unsigned timeout_ms)
{
    for (unsigned t = 0; t < timeout_ms; t++) {
        if (MB16(0x10e) == 1)
            return MB16(0x10c);
        ms(1);
    }
    return 0;
}

/* Descriptor value for id (an SRAM offset), 0 if absent. */
static unsigned desc(unsigned id)
{
    for (unsigned o = MB32(0x110); o < 0x20000 && WSRAM[o] != 0xff; o += 8)
        if (WSRAM[o] == id)
            return *(volatile unsigned *)(0x30000000 + o + 4);
    return 0;
}

/* The MIB elements the app touches, at their offsets in the core's MIB blocks (descriptor 6 =
 * group 19, 7 = group 20). Stock's metadata table (0x603bc15c) gave the same offsets, but only
 * a stock first stage puts it in SDRAM: read through it, a Bose-free image would follow whatever
 * an earlier boot left there and hang. */
#define MIB_STATION_ID 0x03 /* 19:00 dot11StationID */
#define MIB_RSSI 0x30       /* 20:11 Rssi */

static void copy(volatile unsigned char *d, const volatile unsigned char *s, unsigned n)
{
    while (n--)
        *d++ = *s++;
}

static int same(const volatile unsigned char *a, const unsigned char *b, unsigned n)
{
    while (n--)
        if (*a++ != *b++)
            return 0;
    return 1;
}

/* ---- rings and buffers (layout: stacks.h) ---- */

static volatile struct wring tx, rx, frm[4], sup;

#define RXBUF 0x6c0u /* stock pool list 1 + 8: frames arrive still enciphered (CCMP header + MIC) */
#define NRXBUF 7
/* TX: 8 + 0xc4 descriptor + LLC/SNAP + 1500, from UMR0 +0x4100 (RX ends +0x3f40, rings at +0x7220). */
#define TXFOFF 0x4100u
#define TXFBUF 0x6c0u
#define NTXFBUF 6
static unsigned rxfree[NRXBUF], nfree, rxfbase;
static unsigned txfbuf[NTXFBUF], txnfree, txfbase, txbusy[NTXFBUF];
unsigned wifi_rx_frames, wifi_tx_frames, wifi_tx_drop, wifi_rx_drop, wifi_tx_ok, wifi_tx_failed;
#ifdef WILD_OWN
/* RX diagnostics, for when the hardware finds fewer networks than the emulator: these count,
 * per scan, how far frames get: records pulled off the
 * supply ring, frames inside the pool window, and beacons/probe-responses matched. rx_cmds
 * counts the core's own scan events. scan_step prints one line when a scan finishes. */
static unsigned dg_sup3, dg_frame, dg_bcn, dg_evt;
static unsigned dg_presp;             /* probe responses (fc 0x50): proof our probe TX egressed on HW */
static unsigned dg_cmdrec;           /* total kind-1 event records seen from the core */
/* The two bok[] probes below count frames from one AP, the test AP: put its BSSID there for a
 * probe run. The 02:00:00:00:00:a0 placeholder matches no real AP, so both counters stay 0. */
static unsigned dg_bok;              /* test AP beacon probe: low16 = count received, high16 = last RSSI byte */
static unsigned dg_bokraw;           /* test AP pre-filter: low16 = count, [23:16] = fc low byte, [31:24] = descriptor b[0x94] */
static unsigned dg_nonbcn;           /* non-beacon/presp frames delivered to host (auth/assoc/data during join) */
static unsigned dg_nbmark;           /* first non-beacon frame: fc<<16 | b[0x94]<<8 | b[0x97] (descriptor markers vs rx_station gate) */
static unsigned dg_rxsub;            /* first non-beacon that passed rx_station layout gate: sub<<8 | (bssid-match<<1|da-match) */
static unsigned char dg_evseq[16];   /* the first 16 event types, to see what the core sends */
static unsigned dg_nev;
static unsigned dg_rxleak;           /* sup-ring completions whose buffer failed the recycle guard (leaked, not returned to rxfree) */
#endif

static void rings(unsigned host_umr)
{
    volatile struct wring *p = (void *)(0x30000000 + desc(15));
    wr_init(p, 0x508600, 0x508600, 1000, 4, 2);
    wr_init(&tx, 0x508600, host_umr + 0x8600, 1000, 4, 2);
    wr_link(&tx, p, 0, 1, 1);
    p = (void *)(0x30000000 + desc(16));
    wr_init(p, 0x507220, 0x507220, 1250, 4, 2);
    wr_init(&rx, 0x507220, host_umr + 0x7220, 1250, 4, 2);
    wr_link(&rx, p, 0, 1, 2);
    for (unsigned i = 0; i < 4; i++) {
        unsigned b = 0x509600 + i * 28 * 0x18;
        p = (void *)(0x30000000 + desc(12) + i * sizeof(struct wring));
        wr_init(p, b, b, 28, 0x18, 5);
        wr_init(&frm[i], b, host_umr + (b & 0xffff), 28, 0x18, 5);
        wr_link(&frm[i], p, 1, 3, 2);
    }
    p = (void *)(0x30000000 + desc(13));
    wr_init(p, 0x50a080, 0x50a080, 8, 0x18, 4);
    wr_init(&sup, 0x50a080, host_umr + 0xa080, 8, 0x18, 4);
    wr_link(&sup, p, 1, 3, 2);
    rxfbase = host_umr + 0x1000;
    for (nfree = 0; nfree < NRXBUF; nfree++)
        rxfree[nfree] = rxfbase + nfree * RXBUF;
    txfbase = host_umr + TXFOFF;
    for (txnfree = 0; txnfree < NTXFBUF; txnfree++)
        txfbuf[txnfree] = txfbase + txnfree * TXFBUF;
}

static void rx_refill(void)
{
    unsigned n = 0;
    while (nfree && wr_avail(&sup, 0)) {
        volatile unsigned *e = wr_reserve(&sup, 0, 1);
        e[1] = rxfree[--nfree];
        wr_commit(&sup, 0, 1);
        n++;
    }
    if (n)
        DOORBELL();
}

static void tx_reclaim(void)
{
    unsigned ok = 0, failed = 0;
    for (unsigned c = 0; c < 4; c++) {
        while (wr_avail(&frm[c], 3)) {
            volatile unsigned *e = wr_reserve(&frm[c], 3, 1);
            unsigned buf = e[1], off = buf - txfbase;
            wr_commit(&frm[c], 3, 1);
            if (off < NTXFBUF * TXFBUF && off % TXFBUF == 0 && txbusy[off / TXFBUF] &&
                txnfree < NTXFBUF) {
                txbusy[off / TXFBUF] = 0;
                txfbuf[txnfree++] = buf;
            }
        }
        wr_tx_status(&frm[c], &ok, &failed);
    }
    wifi_tx_ok += ok, wifi_tx_failed += failed;
}

/* ---- station state ---- */

static unsigned char own[6];
static unsigned g19, g20;
static unsigned state = WIFI_IDLE, now;
static struct bss cur;                 /* the network joined or being joined */
static int have_cur;
static unsigned char rates[8];         /* management/EAPOL rate vector: the AP's lowest rate */
static unsigned char data_rates[8];    /* data frames: the AP's rates fastest first (wstation_rate_chain) */
static unsigned char pmk[32], seed[32];

/* MIB 20:11 Rssi (S8, read-only): the core's RSSI of the BSS we are joined to, in dBm. The own
 * WiLD core never writes that MIB, so it reads 0 there; the level of the AP's last beacon
 * in the scan table, which rx_frames keeps refreshing while joined, stands in for it. */
static signed char link_rssi(void)
{
    signed char v = g20 ? (signed char)WSRAM[g20 + MIB_RSSI] : 0;
    unsigned i;
    if (v < 0)
        return v;
    for (i = 0; i < nbss; i++)
        if (same(bsstab[i].bssid, cur.bssid, 6))
            return bsstab[i].rssi;
    return 0;
}

static struct wpa_sm sm;
static int keyed;                      /* PTK installed in the core */
static unsigned keys_due;              /* WPA_RX_INSTALL_PTK/GTK not yet in the command ring */

/* join sub-steps */
enum { J_SCAN, J_SETUP, J_AUTH, J_ASSOC, J_4WAY };
static unsigned jstep, jtries, deadline, join_pending, corebd, rxauthok, rxassocok, kicked;
static unsigned last_beacon, beacons_up;

/* scan: pass 0 passive (0xc1) channels 1..13, pass 1 wildcard probe (0xc2), then 0xe4 */
static unsigned scan_pass, scan_chan, scan_next;
#define DWELL_MS 200u   /* per-channel app budget; must exceed the core listen (~112 ms) + its tail */
#define NCHAN 13u

#define PEER_AP 0u
#define PEER_NONE 6u
#define CMD_PEER_ADD 0xb5
#define CMD_PEER_DEL 0xb6
#define CMD_JOIN_DONE 0xb8
#define MGMT_MODULE 0x13

static int due(unsigned t)
{
    return (int)(now - t) >= 0;
}

/* ---- keys (stock builders 0x60113268/3e0/3424/36e8: types 11..14, module 18, flag 1) ---- */

static void put32(unsigned char *p, unsigned v)
{
    p[0] = v, p[1] = v >> 8, p[2] = v >> 16, p[3] = v >> 24;
}

/* CCMP pairwise (type 13) or group (type 11) key; key == 0 deletes (types 14 / 12). */
static int set_key(int pairwise, unsigned idx, const unsigned char *key)
{
    union { unsigned w[13]; unsigned char b[52]; } p;
    unsigned type, len;
    for (unsigned i = 0; i < 13; i++)
        p.w[i] = 0;
    if (pairwise) {
        type = key ? 13 : 14, len = key ? 52 : 6;
        if (key) {
            put32(p.b + 8, 4), p.b[19] = 16;
            copy(p.b + 20, key, 16);
        }
        copy(p.b + (key ? 12 : 0), cur.bssid, 6);
    } else {
        type = key ? 11 : 12, len = key ? 40 : 2;
        if (key) {
            put32(p.b, 4), p.b[4] = idx, p.b[7] = 16;
            copy(p.b + 8, key, 16);
        } else {
            p.b[0] = idx;
        }
    }
    if (!wr_cmd_checked(&tx, type, 18, 1, p.w, len))
        return -1;
    DOORBELL();
    return 0;
}

/* ---- join body (0xbc): BSSID, capability, channel, 13-byte rate block, HT u16s, band ---- */

static void build_join_bc(unsigned char bc[28], const unsigned char bssid[6], unsigned chan)
{
    static const unsigned char tail[13] = {0x0c, 0x82, 0x84, 0x8b, 0x0c, 0x12, 0x96,
                                           0x18, 0x24, 0x30, 0x48, 0x60, 0x6c};
    for (unsigned i = 0; i < 28; i++)
        bc[i] = 0;
    copy(bc, bssid, 6);
    bc[6] = 0x64; /* capability as captured from stock */
    bc[8] = chan;
    copy(bc + 9, tail, 13);
    bc[26] = 6; /* band selector: 2.4 GHz */
}

/* ---- TX ---- */

static unsigned tx_buf(void)
{
    tx_reclaim();
    if (!txnfree || !wr_avail(&frm[2], 0))
        return 0;
    return txfbuf[txnfree - 1];
}

/* Record = stock 0x600584b0: buffer, {1, len+0xc4}, peer slot, destination. The core enciphers a
 * protected frame on its cipher engine (ADR 0031). */
static void tx_push(unsigned addr, unsigned body_len, unsigned peer, unsigned dest)
{
    txnfree--;
    txbusy[(addr - txfbase) / TXFBUF] = 1;
    volatile unsigned *e = wr_reserve(&frm[2], 0, 1);
    e[0] = addr, e[1] = 0, e[2] = 1u | (body_len + 0xc4) << 16;
    e[3] = 0, e[4] = peer | dest << 16, e[5] = 0;
    wr_commit(&frm[2], 0, 1);
    DOORBELL();
    wifi_tx_frames++;
}

static int send_auth(void)
{
    unsigned a = tx_buf();
    if (!a || !wstation_auth((volatile uint8_t *)a, TXFBUF, own, cur.bssid, rates))
        return 0;
    tx_push(a, 6, PEER_NONE, 0xffff);
    return 1;
}

static int send_assoc(void)
{
    unsigned a = tx_buf();
    int body;
    if (!a)
        return 0;
    body = wstation_assoc((volatile uint8_t *)a, TXFBUF, own, cur.bssid, rates,
                          (const char *)cur.ssid, cur.ssidlen);
    if (body <= 0)
        return 0;
    tx_push(a, (unsigned)body, PEER_NONE, 0xffff);
    return 1;
}

/* EAPOL-Key to the AP. Once a PTK is in the core it goes out Protected, as stock sends it. */
static int send_eapol(const unsigned char *f, unsigned n)
{
    unsigned a = tx_buf();
    int body;
    if (!a)
        return 0;
    body = wstation_eapol((volatile uint8_t *)a, TXFBUF, own, cur.bssid, rates, f, n);
    if (body <= 0)
        return 0;
    if (keyed) {
        volatile unsigned char *b = (volatile unsigned char *)a;
        b[8 + 0x19] |= 0x40, b[0x94 + 0x19] |= 0x40;
    }
    tx_push(a, (unsigned)body, PEER_AP, 0x11);
    return 1;
}

static int ap_tx_eth(const unsigned char *f, unsigned n);

/* An Ethernet frame from the SCM's wlan0: only our own source address, only when keyed. */
int radio_tx_eth(const unsigned char *f, unsigned n)
{
    unsigned a, et = f[12] << 8 | f[13];
    int body;
    if (state == WIFI_AP)
        return ap_tx_eth(f, n);
    if (state != WIFI_UP || !same(f + 6, own, 6) || et == WPA_EAPOL_ETHERTYPE ||
        !(a = tx_buf())) {
        wifi_tx_drop++;
        return -1;
    }
    body = wstation_data((volatile uint8_t *)a, TXFBUF, own, cur.bssid, f, data_rates, et, f + 14,
                         n - 14);
    if (body <= 0) {
        wifi_tx_drop++;
        return -1;
    }
    tx_push(a, (unsigned)body, PEER_AP, 0x11);
    return 0;
}

/* ---- RX ---- */

static unsigned char eapframe[WPA_EAPOL_RX_MAX];
static unsigned eappend;
static unsigned char eth[14 + 1500];

static unsigned le16(const volatile unsigned char *p)
{
    return p[0] | p[1] << 8;
}

/* A received data frame: EAPOL to the supplicant, everything else to the SCM as an Ethernet
 * frame from sa to da (station: addr1/addr3, access point: addr3/addr2). */
static void rx_data(const volatile unsigned char *da, const volatile unsigned char *sa,
                    const volatile unsigned char *body, unsigned n)
{
    unsigned et, pl;
    if (n < 8 || body[0] != 0xaa || body[1] != 0xaa || body[2] != 3 || body[3] || body[4] ||
        body[5])
        return;
    et = body[6] << 8 | body[7];
    pl = n - 8;
    if (et == WPA_EAPOL_ETHERTYPE) {
        unsigned k = 4 + (body[10] << 8 | body[11]);
        if (pl >= 4 && k <= pl && k <= sizeof eapframe) {
            copy(eapframe, body + 8, k);
            eappend = k;
        }
        return;
    }
    if ((state != WIFI_UP && state != WIFI_AP) || same(sa, own, 6))
        return; /* not keyed yet, or our own broadcast coming back */
    /* the core leaves trailing bytes past the payload: cut IP to its own length */
    if (et == 0x0800 && pl >= 20 && (body[8 + 2] << 8 | body[8 + 3]) <= pl)
        pl = body[8 + 2] << 8 | body[8 + 3];
    else if (et == 0x86dd && pl >= 40 && 40u + (body[8 + 4] << 8 | body[8 + 5]) <= pl)
        pl = 40 + (body[8 + 4] << 8 | body[8 + 5]);
    if (pl > 1500) {
        wifi_rx_drop++;
        return;
    }
    copy(eth, da, 6);
    copy(eth + 6, sa, 6);
    eth[12] = et >> 8, eth[13] = et;
    copy(eth + 14, body + 8, pl);
    wifi_rx_frames++;
    netcard_from_port(BCOLINK_CH_WIFI, eth, 14 + pl);
}

/* One received non-beacon frame. Header at +0xac, body at +0xcc (measured layout only). */
#define RX_DECIPHERED 1   /* record byte 7: the core deciphered this frame (bco/wild/fw xport.c) */
static void rx_station(const volatile unsigned char *b, unsigned len)
{
    const volatile unsigned char *h = b + 0xac, *body = b + 0xcc;
    unsigned fc, sub;
    if (!have_cur || len < 0xcc || b[0x94] != 0x18 || b[0x97] != 0xc4)
        return;
    fc = le16(h), sub = fc & 0xfc;
    if (!same(h + 10, cur.bssid, 6))
        return;
    if ((fc & 0x0c) == 0) { /* management, addressed to us */
        if (!same(h + 4, own, 6) || (fc & 0x0300) || len < 0xcc + 2)
            return;
        if (sub == 0xc0 || sub == 0xa0) /* deauth / disassoc */
            kicked = 1;
        else if (sub == 0xb0 && len >= 0xcc + 6 && le16(body) == 0 && le16(body + 2) == 2 &&
                 le16(body + 4) == 0)
            rxauthok = 1;
        else if (sub == 0x10 && len >= 0xcc + 4 && le16(body + 2) == 0)
            rxassocok = 1;
        return;
    }
    /* data from the AP (fromDS), to us or a group address, with a body */
    if ((fc & 0x0c) != 8 || (fc & 0x0300) != 0x0200 || (sub & 0x40) ||
        !(same(h + 4, own, 6) || (h[4] & 1)))
        return;
    /* The core deciphers protected frames (ADR 0031) and marks them in record byte 7. Once keyed,
     * take nothing else: a frame still protected had no key, an unmarked one came in clear. */
    if ((fc & 0x4000) || (keyed && b[0x94 + 7] != RX_DECIPHERED)) {
        wifi_rx_drop++;
        return;
    }
    rx_data(h + 4, h + 16, body, len - 0xcc);
}

/* ---- access point (ADR 0031): open only, BSSID = own MAC ---- */

static struct client {
    unsigned char mac[6], state, rates[8]; /* state 1 authenticated, 2 associated */
    unsigned seen;
} cl[WIFI_AP_CLIENTS];
static unsigned char ap_body[WIFI_AP_BODY_MAX];
static unsigned ap_len, ap_ready, ap_deadline, ap_probes, ap_rx;
static const unsigned char ap_mrates[8] = {2, 2, 2, 2, 2, 2, 2, 2}; /* 1 Mb/s: every client hears it */

/* A client's core peer slot is its index: unicast to a client goes through its slot, as the
 * station's frames go through PEER_AP; a PEER_NONE frame goes to the BSS, which is us. */
static void ap_peer(struct client *c, unsigned cmd)
{
    unsigned char pr[10] = {0};
    copy(pr, c->mac, 6);
    pr[6] = (unsigned char)(c - cl);
    wr_cmd_checked(&tx, cmd, 0x12, 0x11, pr, sizeof pr);
    DOORBELL();
}

static void ap_drop(struct client *c)
{
    if (c->state)
        ap_peer(c, CMD_PEER_DEL);
    c->state = 0;
}

/* to a client (its peer slot) or, with c = 0, to every client */
static int ap_send(struct client *c, unsigned fc, const unsigned char *a1, const unsigned char *a3,
                   const unsigned char *r, unsigned et, const unsigned char *body, unsigned blen)
{
    unsigned a = tx_buf();
    int n;
    if (!a || (n = wframe((volatile uint8_t *)a, TXFBUF, fc, a1, own, a3, r, et, body, blen)) <= 0) {
        wifi_tx_drop++;
        return 0;
    }
    if (c)
        tx_push(a, (unsigned)n, (unsigned)(c - cl), 0x11);
    else
        tx_push(a, (unsigned)n, PEER_NONE, 0xffff);
    return 1;
}

/* An IE of the beacon body (after timestamp, interval, capability), 0 if absent. */
static const unsigned char *ap_ie(unsigned id)
{
    for (unsigned o = 12; o + 2 <= ap_len && o + 2 + ap_body[o + 1] <= ap_len; o += 2 + ap_body[o + 1])
        if (ap_body[o] == id)
            return ap_body + o;
    return 0;
}

static struct client *ap_client(const volatile unsigned char *mac, int add)
{
    struct client *old = 0;
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
        if (cl[i].state && same(mac, cl[i].mac, 6))
            return &cl[i];
    if (!add)
        return 0;
    for (unsigned i = 0; i < WIFI_AP_CLIENTS && !old; i++)
        if (!cl[i].state)
            old = &cl[i];
    if (!old) /* full: the longest silent client goes */
        for (unsigned i = 0, age = 0; i < WIFI_AP_CLIENTS; i++)
            if (now - cl[i].seen >= age)
                age = now - cl[i].seen, old = &cl[i];
    ap_drop(old);
    old->seen = now;
    return old;
}

/* A non-beacon frame while the access point runs. Header at +0xac, body at +0xcc. */
static void rx_ap(const volatile unsigned char *b, unsigned len)
{
    const volatile unsigned char *h = b + 0xac, *body = b + 0xcc;
    unsigned fc, sub, n;
    struct client *c;
    if (!ap_ready || len < 0xcc || b[0x94] != 0x18 || b[0x97] != 0xc4 || (h[10] & 1))
        return;
    fc = le16(h), sub = fc & 0xfc, n = len - 0xcc;
    ap_rx++;
    if ((fc & 0x0c) == 0) {
        if (sub == 0x40) { /* the core answers probes from the beacon template */
            ap_probes++;
            return;
        }
        if (!same(h + 4, own, 6) || !same(h + 16, own, 6))
            return;
        c = ap_client(h + 10, sub == 0xb0);
        if (!c)
            return;
        c->seen = now;
        if (sub == 0xb0 && n >= 6 && le16(body) == 0 && le16(body + 2) == 1) {
            static const unsigned char ok[6] = {0, 0, 2, 0, 0, 0}; /* open system, seq 2, success */
            if (!c->state) {
                copy(c->mac, (const unsigned char *)h + 10, 6);
                ap_peer(c, CMD_PEER_ADD);
                c->state = 1;
            }
            ap_send(c, 0xb0, c->mac, own, ap_mrates, 0, ok, sizeof ok);
        } else if ((sub == 0x00 || sub == 0x20) && c->state) {
            unsigned skip = sub ? 10 : 4, o = 6;
            unsigned char rsp[6 + 2 + 8 + 2 + 255];
            const unsigned char *r1 = ap_ie(1), *r50 = ap_ie(50);
            unsigned char ies[256];
            unsigned il = n > skip ? n - skip : 0;
            if (il > sizeof ies)
                il = sizeof ies;
            copy(ies, body + skip, il);
            if (!wstation_rate_chain(ies, il, c->rates))
                return;
            c->state = 2;
            rsp[0] = ap_body[10], rsp[1] = ap_body[11]; /* capability */
            rsp[2] = rsp[3] = 0;                         /* success */
            rsp[4] = (unsigned char)(1 + (c - cl)), rsp[5] = 0xc0;
            if (r1 && r1[1] <= 8)
                copy(rsp + o, r1, 2 + r1[1]), o += 2 + r1[1];
            if (r50 && r50[1] <= 255 - 10)
                copy(rsp + o, r50, 2 + r50[1]), o += 2 + r50[1];
            ap_send(c, sub + 0x10, c->mac, own, ap_mrates, 0, rsp, o);
        } else if (sub == 0xa0 || sub == 0xc0) {
            ap_drop(c);
        }
        return;
    }
    /* data to the distribution system (toDS) from an associated client; null frames only refresh */
    if ((fc & 0x0c) != 8 || (fc & 0x0300) != 0x0100 || !same(h + 4, own, 6) ||
        !(c = ap_client(h + 10, 0)) || c->state != 2)
        return;
    c->seen = now;
    if (!(sub & 0x40))
        rx_data(h + 16, h + 10, body, n);
}

/* An Ethernet frame from the SCM's wlan1: fromDS to an associated client or to every client. */
static int ap_tx_eth(const unsigned char *f, unsigned n)
{
    struct client *c = 0;
    if (!ap_ready || !same(f + 6, own, 6) || (!(f[0] & 1) && (!(c = ap_client(f, 0)) || c->state != 2)) ||
        !ap_send(c, 0x0208, f, f + 6, c ? c->rates : ap_mrates, f[12] << 8 | f[13], f + 14, n - 14)) {
        wifi_tx_drop++;
        return -1;
    }
    return 0;
}

static void rx_frames(void)
{
    while (wr_avail(&sup, 3)) {
        volatile unsigned *e = wr_reserve(&sup, 3, 1);
        unsigned buf = e[0], len = e[2] >> 16;
#ifdef WILD_OWN
        dg_sup3++;
#endif
        if (len > RXBUF)
            len = RXBUF;
        if (buf - rxfbase < NRXBUF * RXBUF && (buf - rxfbase) % RXBUF == 0 && nfree < NRXBUF) {
            const volatile unsigned char *b = (const volatile unsigned char *)buf;
            unsigned fc = le16(b + 0xac);
            /* Supply length excludes the eight-byte pool header (stock subtracts 0xc4). */
            unsigned n = len < RXBUF - 8 ? len + 8 : RXBUF;
#ifdef WILD_OWN
            dg_frame++;
            {   /* any delivered frame from the test AP, BEFORE the fc/0x94 filter: splits
                 * never-delivered (PHY/core, dg_bokraw==0) from delivered-but-filtered.
                 * low16 = count, [23:16] = fc low byte, [31:24] = descriptor byte b[0x94]. */
                static const unsigned char bok[6] = {0x02, 0x00, 0x00, 0x00, 0x00, 0xa0};
                if (n >= 0xac + 24 && same(b + 0xac + 16, bok, 6))
                    dg_bokraw = ((dg_bokraw & 0xffff) + 1) | ((fc & 0xff) << 16) |
                                ((unsigned)b[0x94] << 24);
            }
#endif
            if (n > 0xac + 38 && ((fc & 0xfc) == 0x80 || (fc & 0xfc) == 0x50) && b[0x94] == 0x18) {
                /* beacon / probe response: the scan table, and the AP's heartbeat. Match the
                 * subtype only (mask the flag byte): a Retry-flagged probe response (fc 0x0850)
                 * passes the core subtype filter (REG(0x630)&0xfc) and must pass here too. */
#ifdef WILD_OWN
                dg_bcn++;
                if ((fc & 0xfc) == 0x50)
                    dg_presp++;
                {   /* does the test AP's beacon (BSSID bok, ch1) reach RX at all? proves
                     * AGC-capture (never received, co-channel with the -33 dBm iPhone) vs a
                     * store/parse bug (received but not in bsstab). */
                    static const unsigned char bok[6] = {0x02, 0x00, 0x00, 0x00, 0x00, 0xa0};
                    if (same(b + 0xac + 16, bok, 6))   /* low16 = beacon count, high16 = last RSSI byte */
                        dg_bok = ((dg_bok & 0xffff) + 1) | ((unsigned)(unsigned char)b[0xac + RXRSSI] << 16);
                }
#endif
                bss_add((const unsigned char *)b + 0xac, n - 0xac, scan_chan, (signed char)b[0xac + RXRSSI]);
                if (have_cur && same(b + 0xac + 16, cur.bssid, 6))
                    last_beacon = now, beacons_up++;
            } else if (state == WIFI_AP) {
                rx_ap(b, n);
            } else {
#ifdef WILD_OWN
                dg_nonbcn++;
                if (!dg_nbmark)
                    dg_nbmark = fc << 16 | b[0x94] << 8 | b[0x97];
                if (!dg_rxsub && n >= 0xcc && b[0x94] == 0x18 && b[0x97] == 0xc4) {
                    const volatile unsigned char *h = b + 0xac;
                    dg_rxsub = ((le16(h) & 0xfc) << 8)
                             | (same(h + 10, cur.bssid, 6) << 1)
                             | same(h + 4, own, 6);
                }
#endif
                rx_station(b, n);
            }
            rxfree[nfree++] = buf;
        }
#ifdef WILD_OWN
        else
            dg_rxleak++;   /* completion whose buffer didn't match the pool: lost from rxfree */
#endif
        wr_commit(&sup, 3, 1);
    }
    rx_refill();
}

/* Command ring from the core: only event 0xbd (BSS set up) matters; skip the rest. */
static void rx_cmds(void)
{
    while (wr_avail(&rx, 1)) {
        unsigned w = wr_get(&rx, 1), kind = w & 0xff, len = w >> 16, words;
        if (kind == 1 && len >= 8 && len < 2400)
            words = 2 + (len - 8 + 3) / 4;
        else if (kind == 2 && len < 2400)
            words = (len + 3) / 4;
        else
            continue;
        for (unsigned t = 0; wr_avail(&rx, 1) < words; t++)
            if (t > 100000)
                return; /* the rest of the record never came; the next poll resyncs on it */
        if (kind == 1) {
            unsigned type = wr_get(&rx, 1) & 0xffff;
            words--;
            if (type == 0xbd && len == 8)
                corebd++;
#ifdef WILD_OWN
            if (type == 0xc3 || type == 0xc4) {
                dg_evt++; /* the core's own scan progress / done events */
                if (words >= 7) {                   /* payload = {rxfiq, rxkeep, chan, vco, rxfcs, rxdwell} THIS channel */
                    wr_get(&rx, 1), words--;        /* module|src word */
                    hex(type == 0xc3 ? "core scan P rxfiq " : "core scan A rxfiq ", wr_get(&rx, 1)), words--;
                    hex("core scan rxkeep ", wr_get(&rx, 1)), words--;
                    hex("core scan chan ", wr_get(&rx, 1)), words--;
                    hex("core scan vco ", wr_get(&rx, 1)), words--;   /* bit31=good-initial; [23:16]=lockfail; [15:0]=m */
                    hex("core scan rxfcs ", wr_get(&rx, 1)), words--; /* frame-in with good FCS */
                    hex("core scan rxdwell ", wr_get(&rx, 1)), words--; /* frame-in during listen dwell (4|6) */
                }
            }
            dg_cmdrec++;
            if (dg_nev < sizeof dg_evseq)
                dg_evseq[dg_nev++] = type;
#endif
        }
        while (words--)
            wr_get(&rx, 1);
    }
}

/* ---- scan ---- */

/* passes per scan. On hardware the pure-listen (passive) pass receives nothing: the core's RX/AGC
 * only wakes once a frame has been transmitted, so every captured frame comes from an active pass
 * (probe-req TX, then listen). All passes are therefore active: each is a fresh probe on the
 * channel, which both keeps RX alive and gives a weak AP (the test AP, -56 dBm next to a -33 dBm
 * hotspot on ch1) several independent chances to answer a directed probe. */
#define SCAN_PASSES 2u
#define SCAN_ALL_ACTIVE 1

static void scan_step(void)
{
    static const unsigned char rate_block[13] = {0x0c, 0x02, 0x04, 0x0b, 0x12, 0x96, 0x24,
                                                 0x48, 0x6c, 0x0c, 0x18, 0x30, 0x60};
    if (!due(scan_next))
        return;
    if (scan_pass == SCAN_PASSES) { /* 0xe4 was sent a dwell ago: done */
        state = WIFI_IDLE;
        scan_chan = 0;
#ifdef WILD_OWN
        hex("scan sup3 ", dg_sup3);
        hex("scan frame ", dg_frame);
        hex("scan bcn ", dg_bcn);
        hex("scan presp ", dg_presp);
        hex("scan evt ", dg_evt);
        hex("scan bshort ", dg_bss_short);
        hex("scan bnossid ", dg_bss_nossid);
        hex("scan nbss ", nbss);
        hex("scan bok ", dg_bok);   /* test AP beacons received this far: low16 count, high16 RSSI (0 = AGC-deaf) */
        hex("scan bokraw ", dg_bokraw);   /* test AP pre-filter: low16 count (0 = never delivered to app = PHY/core) */
        /* which APs the own core actually parsed this scan: SSID (8 bytes), len/chan/rssi.
         * proves whether the test AP reaches bsstab at all, and how strong. */
        for (unsigned i = 0; i < nbss && i < 16; i++) {
            const struct bss *bs = &bsstab[i];
            hex("scan ssidA ", (unsigned)bs->ssid[0] << 24 | bs->ssid[1] << 16 |
                               bs->ssid[2] << 8 | bs->ssid[3]);
            hex("scan ssidB ", (unsigned)bs->ssid[4] << 24 | bs->ssid[5] << 16 |
                               bs->ssid[6] << 8 | bs->ssid[7]);
            hex("scan ssidM ", (unsigned)bs->ssidlen << 24 | bs->chan << 16 |
                               ((unsigned)(unsigned char)bs->rssi << 8) | (bs->seen & 0xff));
        }
#endif
        return;
    }
    if (++scan_chan > NCHAN) {
        scan_chan = 0;
        if (++scan_pass == SCAN_PASSES) {
            wr_cmd(&tx, 0xe4, 0x13, 2, 0, 0);
            DOORBELL();
            scan_next = now + 200;
            return;
        }
        scan_chan = 1;
    }
    if (!SCAN_ALL_ACTIVE && scan_pass < SCAN_PASSES - 1) { /* passive beacon-listen passes (disabled: RX is deaf without a TX) */
        unsigned p[2] = {scan_chan | 250u << 16, 6};
        wr_cmd(&tx, 0xc1, 0x0013, 1, p, 6);
    } else {
        unsigned w[16];
        unsigned char *pr = (unsigned char *)w;
        for (unsigned j = 0; j < 64; j++)
            pr[j] = j < 6 ? 0xff : 0;
        pr[0x27] = scan_chan;
        copy(pr + 0x28, rate_block, 13);
        /* listen ~112 ms per channel (0x36, units of 1024 ticks ~= ms) so a full ~102 ms beacon
         * interval is covered and a weak AP is caught by its beacon, not only by a probe response;
         * extend ~30 ms (0x38) each time a probe response keeps the dwell alive. */
        pr[0x36] = 110, pr[0x38] = 30, pr[0x3c] = 6;
        wr_cmd(&tx, 0xc2, 0x0013, 2, w, 62);
    }
    DOORBELL();
    scan_next = now + DWELL_MS;
}

static void scan_begin(void)
{
    /* Age the table instead of wiping it: drop BSSes not heard in the last BSS_KEEP scans, keep the
     * rest so an intermittently-heard target survives into the join decision. */
    unsigned w = 0;
    scan_gen++;
    for (unsigned i = 0; i < nbss; i++)
        if ((unsigned short)(scan_gen - bsstab[i].gen) < BSS_KEEP) {
            if (w != i)
                bsstab[w] = bsstab[i];
            w++;
        }
    nbss = w;
#ifdef WILD_OWN
    dg_sup3 = dg_frame = dg_bcn = dg_presp = dg_evt = dg_bok = dg_bokraw = 0;   /* per-scan so a capture is readable */
    dg_bss_short = dg_bss_nossid = 0;
#endif
    scan_pass = 0, scan_chan = 0, scan_next = now;
    state = WIFI_SCANNING;
}

int wifi_scan(int start)
{
#ifdef WILD_OWN
    /* diag: every driver T_SCAN call — start flag, current state, nbss, join_pending — to see
     * why cfg80211 gets 0 BSSes while the core's own scan finds APs. */
    hex("scanreq ", (unsigned)start << 24 | (state & 0xff) << 16 | (join_pending & 1) << 12 |
                    (nbss & 0xff));
#endif
    if (state == WIFI_AP)
        return (int)nbss; /* the radio stays on the AP channel: the scan made before it */
    if (state == WIFI_SCANNING)
        return -1;
    if (start && (state == WIFI_IDLE || state == WIFI_FAILED) && !join_pending) {
        scan_begin();
        return -1;
    }
    return (int)nbss;
}

int wifi_bss(unsigned i, struct wifi_bss *o)
{
    if (i >= nbss)
        return -1;
    const struct bss *b = &bsstab[i];
    o->ssid_len = b->ssidlen;
    copy(o->ssid, b->ssid, b->ssidlen);
    copy(o->bssid, b->bssid, 6);
    o->chan = b->chan;
    o->sec = b->rsn ? 1 : b->wpa ? 2 : 0;
    /* the joined BSS: the core's own level (MIB 20:11); the rest: their last beacon */
    o->rssi = have_cur && state == WIFI_UP && same(cur.bssid, b->bssid, 6) ? link_rssi() : b->rssi;
    return 0;
}

/* ---- join ---- */

/* The best-heard RSN BSS with this SSID on 2.4 GHz, -1 if none. */
static int pick(void)
{
    int best = -1;
    for (unsigned i = 0; i < nbss; i++) {
        const struct bss *b = &bsstab[i];
        if (b->ssidlen != cur.ssidlen || !same(b->ssid, cur.ssid, cur.ssidlen) || !b->rsn ||
            b->chan < 1 || b->chan > NCHAN)
            continue;
        if (best < 0 || b->seen > bsstab[best].seen)
            best = (int)i;
    }
    return best;
}

/* Lowest rate the AP advertises: the one frame rate it is sure to decode. */
static unsigned lowest_rate(const struct bss *b)
{
    const unsigned char *ie = b->ies, *end = ie + b->ielen;
    unsigned lo = 0;
    for (; ie + 2 <= end && ie + 2 + ie[1] <= end; ie += 2 + ie[1])
        if (ie[0] == 1 || ie[0] == 50)
            for (unsigned r = 0; r < ie[1]; r++) {
                unsigned v = ie[2 + r] & 0x7f;
                if (v && (!lo || v < lo))
                    lo = v;
            }
    return lo;
}

static void fail(void)
{
#ifdef WILD_OWN
    /* Join diagnostics, for a join that does not complete: how far it got — core BSS setup,
     * auth/assoc responses, deauth, TX counters and the 4-way state — so the dead step is
     * clear without a sniffer. */
    hex("join jstep ", jstep);
    hex("join corebd ", corebd);
    hex("join authassockick ", rxauthok << 16 | rxassocok << 8 | kicked);
    hex("join nonbcn ", dg_nonbcn);          /* non-beacon frames (auth/assoc/data) delivered during join */
    hex("join nbmark ", dg_nbmark);          /* first: fc<<16 | b[0x94]<<8 | b[0x97]: did a mgmt frame arrive, with what descriptor */
    hex("join rxsub ", dg_rxsub);            /* first passing layout gate: sub<<8 | bssidmatch<<1 | damatch */
    hex("join txok/fail ", wifi_tx_ok << 16 | (wifi_tx_failed & 0xffff));
    hex("join txframes ", wifi_tx_frames);
    hex("join 4way/keyed ", sm.state << 8 | keyed);
    hex("join cmdrec ", dg_cmdrec);
    for (unsigned i = 0; i < dg_nev; i += 4) /* the event-type stream, four per word */
        hex("join evseq ", dg_evseq[i] | dg_evseq[i + 1] << 8 | dg_evseq[i + 2] << 16 |
                           (unsigned)dg_evseq[i + 3] << 24);
#endif
    state = WIFI_FAILED;
    join_pending = 0;
}

static void join_attempt(void)
{
    unsigned char bc[28];
    int i = pick();
    unsigned lo;
    if (i < 0 || !(lo = lowest_rate(&bsstab[i])) ||
        !wstation_rate_chain(bsstab[i].ies, bsstab[i].ielen, data_rates)) {
        fail();
        return;
    }
    copy(cur.bssid, bsstab[i].bssid, 6);
    cur.chan = bsstab[i].chan;
    for (unsigned r = 0; r < 8; r++)
        rates[r] = lo;
    keyed = 0, keys_due = 0, rxauthok = rxassocok = kicked = 0, eappend = 0;
    build_join_bc(bc, cur.bssid, cur.chan);
    corebd = 0;
    wr_cmd_checked(&tx, 0xbc, MGMT_MODULE, 11, bc, sizeof bc);
    DOORBELL();
    jstep = J_SETUP, deadline = now + 1000;
}

static void retry(void)
{
    if (++jtries >= 3)
        fail();
    else
        join_attempt();
}

static void snonce(unsigned char out[32])
{
    unsigned char mix[6 + 16];
    unsigned v[4] = {now, wifi_rx_frames, wifi_tx_frames, WREG[0x580 / 4]};
    copy(mix, own, 6);
    for (unsigned i = 0; i < 16; i++)
        mix[6 + i] = v[i / 4] >> 8 * (i % 4);
    wpa_sha1_prf(seed, 32, "SoundTap SNonce", mix, sizeof mix, out, 32);
}

static void join_step(void)
{
    if (kicked && jstep >= J_AUTH) {
        retry();
        return;
    }
    switch (jstep) {
    case J_SCAN:
        return; /* waiting for the scan; wifi_poll starts the attempt */
    case J_SETUP: { /* 0xbd, or a second, then JOIN_DONE and the auth request */
        unsigned char jd[6] = {1, 1, 0, 0, 0, 0}; /* privacy, open the AC queues */
        if (!corebd && !due(deadline))
            return;
        if (!wr_cmd_checked(&tx, CMD_JOIN_DONE, 0x12, 11, jd, sizeof jd))
            return; /* ring full: still J_SETUP, the next poll tries again */
        DOORBELL();
        if (!send_auth()) {
            retry();
            return;
        }
        jstep = J_AUTH, deadline = now + 2000;
        return;
    }
    case J_AUTH:
        if (rxauthok) { /* associate at once: the AP ages the auth state out within ~1 s */
            if (!send_assoc()) {
                retry();
                return;
            }
            jstep = J_ASSOC, deadline = now + 1500;
        } else if (due(deadline)) {
            retry();
        }
        return;
    case J_ASSOC:
        if (rxassocok) {
            unsigned char pr[10] = {0};
            unsigned char sn[32];
            copy(pr, cur.bssid, 6);
            pr[6] = PEER_AP;
            /* Ring full: still J_ASSOC, the next poll tries again until the assoc deadline. A lost
             * PEER_ADD left the core without the AP's slot, so the PTK installed nothing. */
            if (!wr_cmd_checked(&tx, CMD_PEER_ADD, 0x12, 0x11, pr, sizeof pr)) {
                if (due(deadline))
                    retry();
                return;
            }
            DOORBELL();
            snonce(sn);
            wpa_sm_init(&sm, pmk, cur.bssid, own, sn);
            jstep = J_4WAY, deadline = now + 8000;
        } else if (due(deadline)) {
            retry();
        }
        return;
    case J_4WAY:
        if (sm.state == WPA_DONE && keyed && !keys_due) {
            state = WIFI_UP;
            last_beacon = now, beacons_up = 0;
        } else if (due(deadline)) {
            retry();
        }
        return;
    }
}

/* Hand the keys the supplicant produced to the core, PTK before GTK. A full command ring
 * leaves a key due, tried again on the next poll instead of being lost. */
static void install_keys(void)
{
    if ((keys_due & WPA_RX_INSTALL_PTK) && !set_key(1, 0, sm.ptk + 32))
        keys_due &= ~WPA_RX_INSTALL_PTK, keyed = 1;
    if ((keys_due & WPA_RX_INSTALL_GTK) && !(keys_due & WPA_RX_INSTALL_PTK) &&
        (sm.gtk_len != 16 || !set_key(0, (unsigned)sm.gtk_idx, sm.gtk)))
        keys_due &= ~WPA_RX_INSTALL_GTK;
}

/* Feed a received EAPOL-Key frame to the supplicant: replies first, then key installs. */
static void eapol(void)
{
    unsigned char out[WPA_EAPOL_MAX];
    size_t ol = 0;
    int r;
    install_keys();
    /* a Message 1 that beats the assoc wait stays pending until the supplicant is set up */
    if (!eappend || !(state == WIFI_UP || (state == WIFI_JOINING && jstep == J_4WAY)))
        return;
    r = wpa_sm_rx(&sm, eapframe, eappend, out, &ol);
    eappend = 0;
    if (r & WPA_RX_FAIL)
        return;
    if (r & WPA_RX_SEND)
        send_eapol(out, (unsigned)ol);
    keys_due |= (unsigned)r & (WPA_RX_INSTALL_PTK | WPA_RX_INSTALL_GTK);
    install_keys();
}

/* Drop the association locally: no frame goes on air, the core forgets the peer and its keys. */
static void drop_link(void)
{
    if (have_cur && (state == WIFI_UP || state == WIFI_JOINING)) {
        unsigned char pd[8] = {0};
        copy(pd, cur.bssid, 6);
        pd[6] = PEER_AP;
        if (keyed) {
            set_key(1, 0, 0);
            set_key(0, (unsigned)(sm.gtk_idx & 3), 0);
        }
        wr_cmd_checked(&tx, CMD_PEER_DEL, 0x12, 0x11, pd, sizeof pd);
        DOORBELL();
    }
    keyed = 0, keys_due = 0;
    for (unsigned i = 0; i < sizeof sm; i++)
        ((volatile unsigned char *)&sm)[i] = 0;
}

int wifi_join(const unsigned char *ssid, unsigned ssid_len, const unsigned char k[32],
              const unsigned char s[32])
{
    if (ssid_len < 1 || ssid_len > 32 || state == WIFI_AP)
        return -1;
    drop_link();
    copy(cur.ssid, ssid, ssid_len);
    cur.ssidlen = ssid_len;
    copy(pmk, k, 32);
    copy(seed, s, 32);
    have_cur = 1, jtries = 0;
    join_pending = 1, jstep = J_SCAN;
    if (state != WIFI_SCANNING) {
        if (pick() >= 0) {
            state = WIFI_JOINING, join_pending = 0;
            join_attempt();
        } else {
            scan_begin();
        }
    }
    return 0;
}

void wifi_leave(void)
{
    drop_link();
    for (unsigned i = 0; i < 32; i++)
        pmk[i] = seed[i] = 0;
    have_cur = 0, join_pending = 0;
    if (state != WIFI_SCANNING && state != WIFI_AP)
        state = WIFI_IDLE;
}

/* Stock's AP start (0x6003bfb0 case 0x5b, 0x60073c90, 0x60073e84): the beacon head (fixed
 * fields and the IEs before TIM) as 0xe2 and the IEs after TIM as 0xe3, each {0, 0, le16 len,
 * bytes}, then 0xbc with our own MAC as BSSID, the beacon interval and the channel. The core then
 * beacons, inserting the TIM, and answers probe requests from the same template. */
static int ap_template(unsigned type, const unsigned char *p, unsigned n)
{
    static unsigned char t[4 + 252];
    if (n > sizeof t - 4)
        return 0;
    t[0] = t[1] = 0, t[2] = n, t[3] = n >> 8;
    copy(t + 4, p, n);
    return wr_cmd_checked(&tx, type, MGMT_MODULE, 0x10, t, 4 + n);
}

int wifi_ap_start(unsigned chan, unsigned bint, const unsigned char *body, unsigned len)
{
    unsigned char bc[28];
    unsigned h = 12, t;
    if (chan < 1 || chan > NCHAN || bint < 20 || bint > 1000 || len < 12 || len > sizeof ap_body ||
        state == WIFI_SCANNING)
        return -1;
    while (h + 2 <= len && h + 2 + body[h + 1] <= len && body[h] != 5)
        h += 2 + body[h + 1];
    t = h + 2 <= len && body[h] == 5 ? h + 2 + body[h + 1] : h; /* the tail starts after TIM */
    if (h > 252 || t > len || len - t > 252)
        return -1;
    wifi_leave();
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
        ap_drop(&cl[i]);
    copy(ap_body, body, len);
    ap_len = len, ap_ready = 0;
    if (!ap_template(0xe2, body, h) || (t < len && !ap_template(0xe3, body + t, len - t)))
        return -1;
    build_join_bc(bc, own, chan);
    bc[6] = bint, bc[7] = bint >> 8; /* beacon interval (a station passes its AP's, 100) */
    corebd = 0;
    wr_cmd_checked(&tx, 0xbc, MGMT_MODULE, 11, bc, sizeof bc);
    DOORBELL();
    ap_deadline = now + 1000;
    state = WIFI_AP;
    return 0;
}

void wifi_ap_stop(void)
{
    if (state == WIFI_AP)
        state = WIFI_IDLE;
    ap_ready = 0;
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
        ap_drop(&cl[i]);
}

void wifi_ap_counters(unsigned c[WIFI_AP_COUNTERS])
{
    unsigned v[WIFI_AP_COUNTERS] = {ap_ready, ap_probes, ap_rx, wifi_tx_ok, wifi_tx_failed,
                                    wifi_tx_drop, wifi_rx_frames, corebd};
    copy((unsigned char *)c, (unsigned char *)v, sizeof v);
}

unsigned wifi_ap_clients(unsigned char macs[][6])
{
    unsigned n = 0;
    for (unsigned i = 0; state == WIFI_AP && i < WIFI_AP_CLIENTS; i++)
        if (cl[i].state == 2)
            copy(macs[n++], cl[i].mac, 6);
    return n;
}

static void ap_step(void)
{
    if (!ap_ready) {
        unsigned char jd[6] = {0, 1, 0, 0, 0, 0}; /* no privacy, open the AC queues */
        if (!corebd && !due(ap_deadline))
            return;
        if (!wr_cmd_checked(&tx, CMD_JOIN_DONE, 0x12, 11, jd, sizeof jd))
            return; /* ring full: the next poll tries again */
        DOORBELL();
        ap_ready = 1;
    }
}

unsigned wifi_state(void)
{
    return state;
}

int wifi_current(struct wifi_bss *o)
{
    if (!have_cur) {
        o->ssid_len = o->chan = o->sec = 0, o->rssi = 0;
        for (unsigned i = 0; i < 6; i++)
            o->bssid[i] = 0;
        return 0;
    }
    o->ssid_len = cur.ssidlen;
    copy(o->ssid, cur.ssid, cur.ssidlen);
    copy(o->bssid, cur.bssid, 6);
    o->chan = cur.chan, o->sec = 1, o->rssi = state == WIFI_UP ? link_rssi() : 0;
    return 1;
}

void wifi_poll(unsigned t)
{
    now = t;
#ifdef WILD_OWN
    {   /* one line per state change: [31:24]=state [23:16]=jstep [15:8]=sm.state
         * [2:0]=rxauthok|rxassocok|keyed. shows exactly how far an own-core join gets. */
        static unsigned last = 0xffffffff;
        unsigned tr = (unsigned)state << 24 | jstep << 16 | (unsigned)sm.state << 8 |
                      (unsigned)(rxauthok << 2 | rxassocok << 1 | keyed);
        if (tr != last)
            last = tr, hex("join trace ", tr);
    }
    {   /* sup-ring health: does the host keep RX buffers supplied to the core? pinpoints the
         * data-RX starvation (core rxnoslot) as host-side (nfree drained / buffers leaked) vs
         * core-side (supply present but never completed back on ch3). byte: nfree | avail0 |
         * avail3 | leak. */
        static unsigned slast;
        if (due(slast + 3000)) {
            slast = now;
            hex("sup nfree/av0/av3/leak ", (nfree & 0xff) << 24 | (wr_avail(&sup, 0) & 0xff) << 16 |
                                           (wr_avail(&sup, 3) & 0xff) << 8 | (dg_rxleak & 0xff));
            /* running counts, low 16 bits: frames the core completed back on ch3 | frames
             * passed up to the host; then transmits ACKed | failed. Two heartbeats apart
             * they show whether a dead LAN still has a radio under it. */
            hex("sup core/rx ", (dg_sup3 & 0xffff) << 16 | (wifi_rx_frames & 0xffff));
            hex("sup txok/fail ", (wifi_tx_ok & 0xffff) << 16 | (wifi_tx_failed & 0xffff));
        }
    }
#endif
    if (MB16(0x10e) == 1)
        MB16(0x10e) = 0;
    rx_frames();
    rx_cmds();
    tx_reclaim();
    eapol();
    switch (state) {
    case WIFI_SCANNING:
        scan_step();
        if (state != WIFI_SCANNING && join_pending) {
            join_pending = 0;
            state = WIFI_JOINING;
            join_attempt();
        }
        break;
    case WIFI_JOINING:
        join_step();
        break;
    case WIFI_AP:
        ap_step();
        break;
    case WIFI_UP:
        /* The AP sent us away, or went quiet (only judged once its beacons were seen at all). */
        if (kicked || (beacons_up && due(last_beacon + 8000))) {
            hex("link lost kicked ", kicked);
            drop_link();
            state = WIFI_FAILED;
        }
#ifdef WILD_OWN
        /* TX-starvation self-heal: if every TX buffer is outstanding and the core has reported no
         * TX completion for 3 s, the TX ring is wedged (a frame the MAC never finished). Left alone
         * it starves all TX, including the next join's EAPOL, so wifi can never recover. Reboot the
         * core to a clean slate and fail the link so the SCM rejoins. Dev-phase guard for the
         * data-egress frontier; a healthy link completes TX constantly and never trips this. */
        else {
            static unsigned last_prog, last_prog_t;
            unsigned prog = wifi_tx_ok + wifi_tx_failed;
            if (prog != last_prog || txnfree)
                last_prog = prog, last_prog_t = now;
            else if (!txnfree && due(last_prog_t + 3000)) {
                hex("link lost tx wedge ", prog);
                drop_link();
                wifi_reset_core();
                state = WIFI_FAILED;
                last_prog_t = now;
            }
        }
#endif
        break;
    }
}

#ifdef WILD_OWN
/* Identity probe of our own core (bco/wild/fw), linked in by wild_own.S: boot it, note what its
 * ready message says, then let wifi_init reset the core and boot it for real. mgmt.c reports the
 * note in HELLO's version string. */
extern const unsigned char wild_own[], wild_own_end[];
unsigned wild_own_type, wild_own_desc;

static void wild_own_probe(void)
{
    CLK[0x40 / 4] &= ~0x8000u;
    *(volatile unsigned *)0xe0013028 &= ~1u;
    CLK[0x20 / 4] = 2;
    CLK[0x20 / 4] = 0x80000;
    ms(6);
    CLK[0x24 / 4] = 0x80000;
    for (unsigned i = 0; i < (unsigned)(wild_own_end - wild_own); i++)
        WSRAM[i] = wild_own[i];
    MB16(0x10e) = 0;
    WREG[0x584 / 4] |= 0x8000;
    CLK[0x24 / 4] = 2;
    wild_own_type = wait_msg(3000);
    wild_own_desc = MB32(0x110);
    MB16(0x10e) = 0;
    hex("wild own type ", wild_own_type); /* also in the flasher's transcript */
    hex("wild own desc ", wild_own_desc);
    /* identify the WiLD silicon: the IDs our core read into its id1 block (reg0 = core silicon
     * ID, rf0 = RF transceiver ID/rev). Reads over the host window, so the real chip answers. */
    if (wild_own_type == 4) {
        unsigned b = 0x30000000 + desc(1);
        hex("wild id reg0 ", *(volatile unsigned *)(b + 0x04));
        hex("wild id mb218 ", *(volatile unsigned *)(b + 0x08));
        hex("wild id reg600 ", *(volatile unsigned *)(b + 0x10));
        hex("wild id reg734 ", *(volatile unsigned *)(b + 0x18));
        hex("wild id regb00 ", *(volatile unsigned *)(b + 0x1c));
        hex("wild id rf0 ", *(volatile unsigned *)(b + 0x20));
    }
}
#endif

/* Load a WiLD core image into the core's SRAM, boot it, and run the full operational handshake
 * (stock SuluWlanDevice::create 0x60113c7c and adapter init 0x60065db0): ready message, host-memory
 * window probe, rings, the second mailbox handshake, the MIB descriptors and the pre-scan class.
 * Returns 0 once the core is operational, negative if it never answered as expected. The caller can
 * then fall back to another image, so a core that fails to come up never leaves the speaker without
 * its LAN. */
static int core_bring_up(const unsigned char *img, unsigned len)
{
    unsigned umr;
    CLK[0x40 / 4] &= ~0x8000u;
    *(volatile unsigned *)0xe0013028 &= ~1u;
    CLK[0x20 / 4] = 2;
    CLK[0x20 / 4] = 0x80000;
    ms(6);
    CLK[0x24 / 4] = 0x80000;
    for (unsigned i = 0; i < len; i++)
        WSRAM[i] = img[i];
    MB16(0x10e) = 0;
    WREG[0x584 / 4] |= 0x8000;
    CLK[0x24 / 4] = 2;
    if (wait_msg(3000) != 4)
        return -2;
    /* UMR0: 0x500000 is the uncached alias of 0x400000 when the probe word shows through */
    *(volatile unsigned *)0x00407ff0 = 0x5a5aa5a5;
    umr = *(volatile unsigned *)0x00507ff0 == 0x5a5aa5a5 ? 0x00500000 : 0x00400000;
    rings(umr);
    WREG[0x580 / 4] = WREG[0x580 / 4];
    MB16(0x10e) = 0;
    MB16(0) = 4; /* host done; the core answers with type 4 again, not the 8 stock waits for */
    MB16(2) = 1;
    wait_msg(3000);
    MB16(0x10e) = 0;
    rx_refill();
    /* dot11StationID (MIB 19:00) holds the device MAC, or the core never answers for us */
    g19 = desc(6);
    /* MIB group 20 (statistics) is descriptor 7: stock 0x6003e3ac fills its MIB base array
     * slot g*4 from ids 1..7 as groups 0,13,14,15,16,19,20 */
    g20 = desc(7);
    if (!g19 || !g20)
        return -3;
    copy(WSRAM + g19 + MIB_STATION_ID, own, 6);
    /* pre-scan init: class 0x0c x4, as stock does before every join */
    for (unsigned k = 0; k < 4; k++)
        wr_cmd(&tx, 0x0c, 0x12, 1, &k, 2);
    DOORBELL();
    return 0;
}

int wifi_init(const unsigned char mac[6])
{
    copy(own, mac, 6);
#ifdef WILD_OWN
    wild_own_probe();   /* records the core's identity for HELLO, then resets to reload below */
    return core_bring_up(wild_own, (unsigned)(wild_own_end - wild_own));
#else
    return -1;
#endif
}

/* Reboot the WiLD core from scratch (our core image), same path wifi_init uses, with the station
 * state already cleared by the caller. A sticky MAC/RX wedge only clears on a fresh core boot, so
 * this gives the SCM a radio reset (driven from T_FORGET) without re-flashing the DM870 app.
 * dev-phase iteration aid; it is the same ~1 s bring-up wifi_init runs at boot. */
#ifdef WILD_OWN
/* Runtime core image (dev-phase iteration aid): the SCM streams a WiLD build into this staging
 * area (mgmt WILD_WRITE) and boots it (WILD_BOOT) without re-flashing the app. Free SDRAM below
 * the mode stacks (stacks.h). The embedded image stays the fallback. */
#define WILD_RAM ((unsigned char *)0x60e80000)
#define WILD_RAM_MAX 0x10000u
static unsigned wild_ram_len;

int wifi_wild_write(unsigned off, const unsigned char *p, unsigned n)
{
    if (off > WILD_RAM_MAX || n > WILD_RAM_MAX - off)
        return -1;
    copy(WILD_RAM + off, p, n);
    return 0;
}

/* Dump n bytes of the core's SRAM (host window 0x30000000, core offset off) to the UART, so new
 * core-side diagnostics need only a runtime core reload, not an app flash. */
int wifi_wild_peek(unsigned off, unsigned n)
{
    if (off >= 0x40000 || n > 0x400 || n > 0x40000 - off)
        return -1;
    for (unsigned i = 0; i < n; i += 4)
        hex("peek ", *(volatile unsigned *)(0x30000000u + ((off + i) & ~3u)));
    return 0;
}

int wifi_wild_boot(unsigned len, unsigned crc)
{
    if (!len || len > WILD_RAM_MAX || crc32(WILD_RAM, len) != crc) {
        hex("wild ram bad crc ", len <= WILD_RAM_MAX ? crc32(WILD_RAM, len) : 0);
        return -1;
    }
    wild_ram_len = len;
    wifi_leave();
    return wifi_reset_core();
}
#endif

int wifi_reset_core(void)
{
    state = WIFI_IDLE;
#ifdef WILD_OWN
    if (wild_ram_len) {
        int r = core_bring_up(WILD_RAM, wild_ram_len);
        hex("wild ram boot ", (unsigned)r << 16 | (wild_ram_len & 0xffff));
        if (r == 0)
            return 0;
        wild_ram_len = 0; /* a dead image falls back once, not on every reset */
    }
    return core_bring_up(wild_own, (unsigned)(wild_own_end - wild_own));
#else
    return -1;
#endif
}
