/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Wi-Fi station of the BCO network card (ADR 0031). Boots our own WiLD core (bco/wild/fw, ADR 0031,
 * linked in by wild_own.S), then runs scan, join, the WPA2-PSK 4-way and the data path from the
 * main loop, never blocking it for more than a few milliseconds. Every core command and frame
 * layout here was proven by the one-shot bring-up (ADR 0031, the former wild.c): boot and
 * handshake, rings, scan (0xc1/0xc2/0xe4), join (0xbc, JOIN_DONE), the auth and assoc frames,
 * PEER_ADD, the key commands and data frames both ways (DHCP over WPA2, 2026-10-06).
 * 2.4 GHz and WPA2-PSK only: pairwise CCMP, the AP's group cipher (CCMP, or TKIP from a mixed-mode
 * router, whose group key the core cannot use: unicast works, its broadcasts are dropped). */
#include "board.h"
#include "netcard.h"
#include "bcolink.h"
#include "wifi.h"
#include "wscan.h"
#include "wring.h"
#include "wstation.h"
#include "wpa.h"
#include "wsec.h"

#define WSRAM ((volatile unsigned char *)0x30000000)
#define WREG ((volatile unsigned *)0x30200000)
#define MB 0x3001fde4u
#define MB16(o) (*(volatile unsigned short *)(MB + (o)))
#define MB32(o) (*(volatile unsigned *)(MB + (o)))
#define CLK ((volatile unsigned *)0xe0007000)
#define DOORBELL() (WREG[0x210 / 4] |= 4)

/* GCC may emit these for aggregate copies even freestanding; nothing else here provides them. */
/* cppcheck-suppress unusedFunction ; GCC calls it for aggregate copies */
void *memcpy(void *d, const void *s, unsigned n)
{
    unsigned char *o = d;
    const unsigned char *i = s;
    while (n--)
        *o++ = *i++;
    return d;
}

/* cppcheck-suppress unusedFunction ; GCC calls it for aggregate initialisation */
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

/* RX: stock pool list 1 + 8, and room for a TKIP frame, which arrives with its 20 bytes of IV, MIC
 * and ICV for the app to decipher (ADR 0136; the core's RXBUF_HOST): 7 x 0x6e0 from +0x1000 end at
 * +0x4020, below the TX buffers. */
#define RXBUF 0x6e0u
#define NRXBUF 7
_Static_assert(0x1000u + NRXBUF * RXBUF <= 0x4100u, "RX buffers run into the TX buffers");
/* TX: 8 + 0xc4 descriptor + LLC/SNAP + 1500 + the CCMP header and MIC the core adds in place, or
 * the 20 bytes of a TKIP frame the app enciphered, from UMR0 +0x4100 (rings at +0x7220): 7 x 0x6d0
 * end at +0x70b0, and an eighth would not fit below the rings (the SRAM budget is in stacks.h). */
#define TXFOFF 0x4100u
#define TXFBUF 0x6d0u
#define NTXFBUF WIFI_TX_BUFS
_Static_assert(TXFOFF + NTXFBUF * TXFBUF <= 0x7220u, "TX buffers run into the command rings");
static unsigned rxfree[NRXBUF], nfree, rxfbase;
static unsigned txfbuf[NTXFBUF], txnfree, txfbase, txbusy[NTXFBUF], txready;
/* The TX buffer of an EAPOL-Key frame the core enciphers under the PTK in use (a rekey's
 * Message 4), until the core hands it back: the new PTK waits for it (install_keys). */
static unsigned eapol_buf;
unsigned wifi_rx_frames, wifi_tx_frames, wifi_tx_drop, wifi_rx_drop, wifi_tx_ok, wifi_tx_failed;
#ifdef BCO_DEBUG_TRACE
/* RX diagnostics (the trace build only, build-app.sh link-own-trace: every line goes out on the UART,
 * which busy-waits per character and starves USB and the RX rings meanwhile), for when the hardware finds fewer networks than the emulator: these count,
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
    for (txnfree = 0; txnfree < NTXFBUF; txnfree++) {
        txfbuf[txnfree] = txfbase + txnfree * TXFBUF;
        txbusy[txnfree] = 0;
    }
    eapol_buf = 0;
    txready = 1;
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

static void ap_tx_report(unsigned rec4, unsigned bad);

static void tx_reclaim(void)
{
    for (unsigned c = 0; c < 4; c++) {
        while (wr_avail(&frm[c], 3)) {
            const volatile unsigned *e = wr_reserve(&frm[c], 3, 1);
            unsigned buf = e[1], off = buf - txfbase;
            wr_commit(&frm[c], 3, 1);
            if (buf == eapol_buf)
                eapol_buf = 0;
            if (off < NTXFBUF * TXFBUF && off % TXFBUF == 0 && txbusy[off / TXFBUF] &&
                txnfree < NTXFBUF) {
                txbusy[off / TXFBUF] = 0;
                txfbuf[txnfree++] = buf;
            }
        }
        /* the TX reports (wring.h wr_tx_status), each also to the setup AP's client it was for */
        for (unsigned n = wr_avail(&frm[c], 4); n--;) {
            const volatile unsigned *e = wr_reserve(&frm[c], 4, 1);
            unsigned bad = (e[3] & 0xffff) != 0;
            if (bad)
                wifi_tx_failed++;
            else
                wifi_tx_ok++;
            ap_tx_report(e[4], bad);
            wr_commit(&frm[c], 4, 1);
        }
    }
}

/* A background scan's hold on the SCM's frames (ADR 0135): TX_DRAIN gives no new credit while the
 * buffers empty before the radio leaves its channel, TX_OFF also drops what comes while it is away
 * (it would go out on the wrong channel). */
enum { TX_OPEN, TX_DRAIN, TX_OFF };
static unsigned tx_hold;

unsigned wifi_tx_free(void)
{
    if (!txready || tx_hold)
        return 0;
    tx_reclaim();
    return txnfree > 1 ? txnfree - 1 : 0;
}

void wifi_tx_counters(unsigned c[3])
{
    c[0] = wifi_tx_ok, c[1] = wifi_tx_failed, c[2] = wifi_tx_drop;
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
static unsigned prof_sec; /* the profile's security, WIFI_SEC_* (SET_PROFILE) */
/* A WEP profile (ADR 0136): the key and its index, the authentication asked for (WIFI_WEP_*), the
 * next IV (from the SCM's random seed, then counting up per frame), and the authentication
 * algorithm the join tries now (0 open system, 1 shared key) with the shared key's challenge. */
static unsigned char wep_key[13], wep_len, wep_idx, wep_auth;
static unsigned wep_iv, auth_alg, challenge_due;
static unsigned char challenge[253];
static unsigned challenge_len;
#define AUTH_SHARED_KEY 1
#define STATUS_ALG_UNSUPPORTED 13

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

/* The joined access point's level averaged over its beacons (each moves it 1/8 of the way), in
 * 1/16 dBm, for the roaming decisions (ADR 0135); 0 until its first beacon after the join. */
static int rssi_avg16;

static void rssi_track(signed char r)
{
    if (r < 0)
        rssi_avg16 = rssi_avg16 ? rssi_avg16 + (16 * r - rssi_avg16) / 8 : 16 * r;
}

static int cur_rssi(void)
{
    return rssi_avg16 ? rssi_avg16 / 16 : link_rssi();
}

static struct wpa_sm sm;
static int keyed;                      /* PTK installed in the core */
static int ap_keyed;                   /* and the AP uses it: a unicast frame from it deciphered */
static unsigned keys_due;              /* WPA_RX_INSTALL_PTK/GTK not yet in the command ring */

/* The keys the app enciphers and deciphers itself (WEP, TKIP; ADR 0136), the core holding only a
 * mark for them (set_key). A TKIP key is the TK, then the MIC key from the AP (bytes 16-23, which
 * the station receives with) and the one to it (24-31, which it sends with). tsc: the last TSC
 * sent (pairwise) or accepted (both); a fresh key starts at 0, as the core's CCMP PN does. */
static struct swkey {
    unsigned char cipher; /* WSEC_*, 0 = none */
    unsigned char key[32];
    unsigned long long tsc, rxtsc;
} sw_pair, sw_grp[4];
/* A frame's body while the app enciphers or deciphers it: up to a TKIP header, LLC/SNAP and 1500
 * bytes, the Michael MIC and the ICV. */
static unsigned char secbuf[WSEC_TKIP_HDR + 8 + 1500 + WSEC_MIC + WSEC_ICV];
/* TKIP countermeasures (IEEE 802.11-2020 12.5.2.4): when a Michael MIC failed last, and until when
 * no TKIP network is joined after two failures within a minute. */
static unsigned mic_fail_t, mic_fail_seen, cm_until, cm_on;
#define CM_WINDOW_MS 60000u
#define CM_HOLD_MS 60000u

/* Management frame protection (IEEE 802.11w, ADR 0136), once the 4-way negotiated it: the PTK's
 * TK for the robust unicast management frames the app protects and opens in software (CCMP with
 * the nonce's Management flag), their packet numbers (sent from MGMT_PN0 up, so they never meet
 * the core's data PNs under the same key; received checked apart from data, as the standard
 * keeps them), the IGTK for BIP on group-addressed ones, and an SA Query in flight after an
 * unprotected deauthentication or disassociation. */
static unsigned pmf;
static unsigned char mgmt_tk[16], igtk[16];
static unsigned long long mgmt_pn, mgmt_rxpn, ipn;
static unsigned igtk_id;
static unsigned saq_on, saq_deadline, saq_id, saq_sub, saq_reason;
#define MGMT_PN0 (1ull << 40)
#define SAQ_MS 1000u /* wpa_supplicant's SA Query limit: no protected answer in this long, the link is gone */

/* WPA3-SAE (ADR 0136): messages for the SCM (wifi_sae_next), and how the external authentication
 * stands: the PMK the SCM sent for the access point being joined, and its result. */
#define SAE_Q 4u
static unsigned char saeq[SAE_Q][1 + WIFI_SAE_MAX];
static unsigned short saeq_len[SAE_Q];
static unsigned saeq_in, saeq_out, sae_pmk, sae_result; /* sae_result: 0 waiting, 1 done, 2 refused */
#define SAE_MS 15000u /* SAE's commit and confirm, with anti-clogging, well inside this */
#define AUTH_SAE 3

/* join sub-steps */
enum { J_SCAN, J_SETUP, J_AUTH, J_ASSOC, J_4WAY, J_KEYS, J_SAE };
static unsigned jstep, jtries, deadline, join_pending, corebd, rxauthok, rxassocok, kicked;
static unsigned kick_sub, kick_reason; /* the deauthentication (0xc0) or disassociation (0xa0) and its reason */
/* Why the last join failed, for STATUS (wifi_fail): the step and the AP's status or reason code. */
static unsigned fail_step, fail_code;
#define JOIN_TRIES 5u /* auth/assoc attempts per join: a band-steering AP ignores the first few */
static unsigned last_beacon, beacons_up;

/* scan: pass 0 passive (0xc1) channels 1..13, pass 1 wildcard probe (0xc2), then 0xe4 */
static unsigned scan_pass, scan_chan, scan_next;
#define DWELL_MS 200u   /* per-channel app budget; must exceed the core listen (~112 ms) + its tail */
#define NCHAN 13u
#define LISTEN_MS 110u  /* a passive listen per channel (units of 1024 ticks ~= ms), as a probe listens */

/* ---- regulatory domain (SET_REGDOM, ADR 0135) ---- */

#define CHANS_ALL ((1u << NCHAN) - 1)
static unsigned reg_chans = CHANS_ALL, reg_passive;
static unsigned char reg_cc[2] = {'0', '0'};
static int reg_dbm = 20;

/* A channel the station may use, and one it may also probe on (and beacon on as the AP). */
static int chan_ok(unsigned c)
{
    return c >= 1 && c <= NCHAN && (reg_chans >> (c - 1) & 1);
}

static int chan_active(unsigned c)
{
    return chan_ok(c) && !(reg_passive >> (c - 1) & 1);
}

/* The next allowed channel after c, 0 when none is left. */
static unsigned next_chan(unsigned c)
{
    while (++c <= NCHAN)
        if (chan_ok(c))
            return c;
    return 0;
}

int wifi_set_regdom(const unsigned char cc[2], unsigned chans, unsigned passive, int max_dbm)
{
    if (!(chans & CHANS_ALL) || max_dbm < 0 || max_dbm > 30)
        return -1;
    reg_cc[0] = cc[0], reg_cc[1] = cc[1];
    reg_chans = chans & CHANS_ALL;
    reg_passive = passive & reg_chans;
    reg_dbm = max_dbm;
    /* The core has no TX power control we know of: the limit is recorded, not applied (ADR 0135).
     * Never guess an RF register for it. */
    return 0;
}

/* ---- link events (ADR 0135) ---- */

#define NEVQ 4u
static struct wifi_event evq[NEVQ];
static unsigned evhead, evcount;

int wifi_event_next(struct wifi_event *o)
{
    if (!evcount)
        return 0;
    *o = evq[evhead];
    evhead = (evhead + 1) % NEVQ, evcount--;
    return 1;
}

/* Queue an event about the access point in cur; a full queue gives up its oldest. */
static void post(unsigned type, unsigned reason)
{
    struct wifi_event *e;
    if (evcount == NEVQ)
        evhead = (evhead + 1) % NEVQ, evcount--;
    e = &evq[(evhead + evcount++) % NEVQ];
    e->type = (unsigned char)type, e->reason = (unsigned short)reason;
    copy(e->bssid, cur.bssid, 6);
    e->chan = cur.chan;
    e->rssi = (signed char)cur_rssi();
}

/* ---- roaming within the SSID (ADR 0135), like wpa_supplicant's bgscan simple ----
 * While the averaged level of the access point stays below ROAM_THRESHOLD the station scans in
 * the background, one channel per visit away from its own, and goes back between visits so the
 * link keeps running. It stops looking once the level is ROAM_HYSTERESIS above the threshold. A
 * scan that finds no access point of the SSID at least ROAM_DELTA stronger doubles the time to
 * the next, up to BG_LONG_MS. Above the threshold it never leaves its channel. */
#define ROAM_THRESHOLD (-70)
#define ROAM_HYSTERESIS 5
#define ROAM_DELTA 8
#define BG_SHORT_MS 30000u
#define BG_LONG_MS 300000u
#define BG_DRAIN_MS 50u  /* at most this long for the transmit buffers to empty before leaving */
#define BG_HOME_MS 500u  /* on the own channel between two visits */
enum { BG_IDLE, BG_HOME, BG_DRAIN, BG_OFF };
static unsigned prof_flags, roam_on, roaming, weak;
static unsigned char prof_bssid[6], roam_bssid[6];
static unsigned bg_phase, bg_chan, bg_until, bg_next, bg_last, bg_interval, bg_scanned, off_since;

/* The station is (again) joined: a fresh level average and no background scan yet. A roam that
 * got here is reported. */
static void link_up(void)
{
    rssi_avg16 = 0, weak = 0;
    bg_phase = BG_IDLE, tx_hold = TX_OPEN;
    bg_interval = BG_SHORT_MS;
    if (roaming)
        post(WIFI_EV_ROAMED, 0);
    roaming = 0;
}

#define PEER_AP 0u
#define PEER_NONE 6u
#define CMD_PEER_ADD 0xb5
#define CMD_PEER_DEL 0xb6
#define CMD_JOIN_DONE 0xb8
#define CMD_PEER_CLEAR 0xb7
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

/* Pairwise (type 13) or group (type 11) key of cipher suite type cipher; key == 0 deletes (types
 * 14 / 12). A CCMP key goes to the core's engine. Any other (WEP, TKIP) is the app's (ADR 0136):
 * the install names the cipher and carries no key bytes, so the core only knows to pass the frames
 * under it up undeciphered; the key itself stays here (sw_pair, sw_grp). */
static int set_key(int pairwise, unsigned idx, const unsigned char *key, unsigned cipher)
{
    union { unsigned w[13]; unsigned char b[52]; } p;
    unsigned type, len;
    for (unsigned i = 0; i < 13; i++)
        p.w[i] = 0;
    if (pairwise) {
        type = key ? 13 : 14, len = key ? 52 : 6;
        if (key) {
            put32(p.b + 8, cipher), p.b[19] = 16;
            if (cipher == WSEC_CCMP)
                copy(p.b + 20, key, 16);
        }
        copy(p.b + (key ? 12 : 0), cur.bssid, 6);
    } else {
        type = key ? 11 : 12, len = key ? 40 : 2;
        if (key) {
            put32(p.b, cipher), p.b[4] = idx, p.b[7] = 16;
            if (cipher == WSEC_CCMP)
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

/* The first authentication frame: open system (wstation_auth), or for WEP's shared key algorithm
 * 1, transaction 1. */
static int send_auth(void)
{
    unsigned a = tx_buf();
    static const unsigned char shared1[6] = {AUTH_SHARED_KEY, 0, 1, 0, 0, 0};
    if (!a)
        return 0;
    if (auth_alg == AUTH_SHARED_KEY ?
            wframe((volatile uint8_t *)a, TXFBUF, 0xb0, cur.bssid, own, cur.bssid, rates, 0, shared1, 6) <= 0 :
            !wstation_auth((volatile uint8_t *)a, TXFBUF, own, cur.bssid, rates))
        return 0;
    tx_push(a, 6, PEER_NONE, 0xffff);
    return 1;
}

/* Shared key authentication's third frame: algorithm 1, transaction 3, status 0 and the AP's
 * challenge text, enciphered with the WEP key (IEEE 802.11-2020 12.3.3.3): the Protected bit set,
 * sent TX_RAW, as the app enciphered it. */
static int send_challenge(void)
{
    unsigned a = tx_buf(), n = 6 + 2 + challenge_len, len;
    unsigned char *p = secbuf + WSEC_WEP_HDR;
    volatile unsigned char *b = (volatile unsigned char *)a;
    if (!a)
        return 0;
    p[0] = AUTH_SHARED_KEY, p[1] = 0, p[2] = 3, p[3] = 0, p[4] = 0, p[5] = 0;
    p[6] = 16, p[7] = (unsigned char)challenge_len;
    copy(p + 8, challenge, challenge_len);
    len = wsec_wep_protect(wep_key, wep_len, wep_idx, wep_iv++, secbuf, n);
    if (wframe(b, TXFBUF, 0x40b0, cur.bssid, own, cur.bssid, rates, 0, secbuf, len) <= 0)
        return 0;
    b[8 + 0x13] = b[0x94 + 0x13] = 1; /* TX_RAW */
    tx_push(a, len, PEER_NONE, 0xffff);
    return 1;
}

/* The RSN capabilities this station asks for: MFPC when management frame protection is on, and
 * MFPR when the access point requires it. */
static unsigned rsn_caps(void)
{
    return cur.mfp ? WPA_CAP_MFPC | (cur.rcaps & WPA_CAP_MFPR) : 0;
}

static int send_assoc(void)
{
    unsigned a = tx_buf();
    int body;
    if (!a)
        return 0;
    /* an open network: neither a security IE nor the Privacy bit; a PSK one the RSN or WPA IE of
     * what join_attempt chose */
    unsigned char ie[WPA_IE_MAX];
    unsigned ielen = 0;
    if (prof_sec == WIFI_SEC_PSK || prof_sec == WIFI_SEC_SAE)
        ielen = wpa_own_ie(ie, cur.proto, cur.grp, cur.pair, cur.akm, rsn_caps());
    body = wstation_assoc((volatile uint8_t *)a, TXFBUF, own, cur.bssid, rates,
                          (const char *)cur.ssid, cur.ssidlen, cur.caps, ie, ielen,
                          prof_sec != WIFI_SEC_OPEN);
    if (body <= 0)
        return 0;
    tx_push(a, (unsigned)body, PEER_NONE, 0xffff);
    return 1;
}

/* ---- WPA3-SAE through the SCM, and protected management frames (ADR 0136) ---- */

/* A message for the SCM (wifi_sae_next): the newest SAE_Q are kept. */
static void sae_queue(unsigned kind, const unsigned char *p, unsigned n)
{
    unsigned i = saeq_in % SAE_Q;
    if (n > WIFI_SAE_MAX)
        return;
    saeq[i][0] = (unsigned char)kind;
    copy(saeq[i] + 1, p, n);
    saeq_len[i] = (unsigned short)(1 + n);
    if (saeq_in - saeq_out == SAE_Q)
        saeq_out++;
    saeq_in++;
}

/* Ask the SCM to run SAE with the access point being joined (or to stop): its BSSID and SSID. */
static void sae_start(unsigned kind)
{
    unsigned char m[6 + 1 + 32];
    copy(m, cur.bssid, 6);
    m[6] = cur.ssidlen;
    copy(m + 7, cur.ssid, cur.ssidlen);
    if (kind == WIFI_SAE_START)
        sae_pmk = 0, sae_result = 0;
    sae_queue(kind, m, 7u + cur.ssidlen);
}

int wifi_sae_next(unsigned char *o, unsigned max)
{
    unsigned i = saeq_out % SAE_Q, n;
    if (saeq_in == saeq_out || saeq_len[i] > max)
        return 0;
    n = saeq_len[i];
    copy(o, saeq[i], n);
    saeq_out++;
    return (int)n;
}

/* wpa_supplicant's SAE frame (algorithm 3) to the access point being joined, as it built the body. */
int wifi_sae_tx(const unsigned char *body, unsigned n)
{
    unsigned a;
    if (state != WIFI_JOINING || jstep != J_SAE || n < 6 || n > WIFI_SAE_MAX || body[0] != AUTH_SAE || body[1])
        return -1;
    if (!(a = tx_buf()) || wframe((volatile uint8_t *)a, TXFBUF, 0xb0, cur.bssid, own, cur.bssid, rates, 0,
                                  body, n) <= 0)
        return -1;
    tx_push(a, n, PEER_NONE, 0xffff);
    return 0;
}

int wifi_set_pmk(const unsigned char bssid[6], const unsigned char pmk_in[32])
{
    if (prof_sec != WIFI_SEC_SAE || !have_cur || !same(bssid, cur.bssid, 6))
        return -1;
    copy(pmk, pmk_in, 32);
    sae_pmk = 1;
    return 0;
}

int wifi_sae_done(unsigned status, const unsigned char bssid[6])
{
    if (state != WIFI_JOINING || jstep != J_SAE || !same(bssid, cur.bssid, 6))
        return -1;
    sae_result = status ? 2 : 1;
    if (status)
        fail_step = WIFI_FAIL_AUTH, fail_code = status;
    return 0;
}

/* A robust management frame to the AP under PMF: action, deauthentication or disassociation,
 * enciphered by the app as CCMP under the PTK's TK, sent TX_RAW. */
static int send_protected(unsigned fc, const unsigned char *body, unsigned n)
{
    unsigned a = tx_buf(), len;
    unsigned char hdr[24];
    volatile unsigned char *b = (volatile unsigned char *)a;
    if (!a || !pmf || n > 64)
        return 0;
    for (unsigned i = 0; i < 24; i++)
        hdr[i] = 0;
    hdr[0] = (unsigned char)fc, hdr[1] = 0x40;
    copy(hdr + 4, cur.bssid, 6), copy(hdr + 10, own, 6), copy(hdr + 16, cur.bssid, 6);
    copy(secbuf + WSEC_CCMP_HDR, body, n);
    /* The core writes the sequence number from its timer after this; CCMP's AAD masks it. */
    len = wsec_ccmp_protect(mgmt_tk, hdr, ++mgmt_pn, secbuf, n);
    if (wframe(b, TXFBUF, fc | 0x4000, cur.bssid, own, cur.bssid, rates, 0, secbuf, len) <= 0)
        return 0;
    b[8 + 0x13] = b[0x94 + 0x13] = 1; /* TX_RAW */
    tx_push(a, len, PEER_NONE, 0xffff);
    return 1;
}

/* An unprotected deauthentication or disassociation under PMF may be forged: ask the AP over a
 * protected SA Query whether it still has the association; no answer within SAQ_MS ends the link
 * with the frame's reason (IEEE 802.11-2020 11.13). */
static void saq_start(unsigned sub, unsigned reason)
{
    unsigned char q[4] = {8, 0, 0, 0};
    if (saq_on)
        return;
    saq_id = (now ^ WREG[0x580 / 4]) & 0xffff;
    q[2] = (unsigned char)saq_id, q[3] = (unsigned char)(saq_id >> 8);
    saq_on = 1, saq_deadline = now + SAQ_MS, saq_sub = sub, saq_reason = reason;
    send_protected(0xd0, q, 4);
}

/* An SA Query that got no protected answer within SAQ_MS: the AP did send us away. */
static void saq_check(void)
{
    if (saq_on && due(saq_deadline))
        saq_on = 0, kicked = 1, kick_sub = saq_sub, kick_reason = saq_reason,
        fail_step = WIFI_FAIL_KICKED, fail_code = saq_reason;
}

/* Open a protected management frame from the AP (the body at body, n bytes) into secbuf: CCMP
 * under the PTK's TK, its PN above the last one. Returns the plaintext's length, -1 to drop. */
static int mgmt_open(const volatile unsigned char *h, const volatile unsigned char *body, unsigned n)
{
    unsigned char hh[24];
    unsigned long long pn;
    int r;
    if (n < WSEC_CCMP_HDR + WSEC_CCMP_MIC || n > sizeof secbuf)
        return -1;
    copy(hh, h, 24);
    copy(secbuf, body, n);
    pn = wsec_ccmp_pn(secbuf);
    if (pn <= mgmt_rxpn || (r = wsec_ccmp_open(mgmt_tk, hh, secbuf, n)) < 0)
        return -1;
    mgmt_rxpn = pn;
    return r;
}

/* A group-addressed robust management frame under PMF: it must end in an MMIE whose MIC holds
 * under the IGTK, with a newer IPN. Returns the body's length without the MMIE, -1 to drop. */
static int bip_open(const volatile unsigned char *h, const volatile unsigned char *body, unsigned n)
{
    unsigned char hh[24];
    unsigned keyid;
    uint64_t got;
    if (n > sizeof secbuf || n < WSEC_MMIE_LEN)
        return -1;
    copy(hh, h, 24);
    copy(secbuf, body, n);
    if (!wsec_bip_check(igtk, hh, secbuf, n, &keyid, &got) || keyid != igtk_id || got <= ipn)
        return -1;
    ipn = got;
    return (int)(n - WSEC_MMIE_LEN);
}

/* A data frame to the AP under the app's own pairwise key (TKIP, ADR 0136): the MSDU (LLC/SNAP for
 * et, then p) enciphered in secbuf, sent as TX_RAW so the core passes it as it is. The TSC counts up
 * per frame; a retry is the core's, of the same frame. Returns the body length, 0 on reject. */
static int sw_data(unsigned a, const unsigned char *da, unsigned et, const unsigned char *p, unsigned n)
{
    int wep = prof_sec == WIFI_SEC_WEP;
    unsigned char *m = secbuf + (wep ? WSEC_WEP_HDR : WSEC_TKIP_HDR);
    unsigned len;
    if ((!wep && sw_pair.cipher != WSEC_TKIP) || n > 1500)
        return 0;
    m[0] = 0xaa, m[1] = 0xaa, m[2] = 3, m[3] = m[4] = m[5] = 0;
    m[6] = (unsigned char)(et >> 8), m[7] = (unsigned char)et;
    copy(m + 8, p, n);
    if (wep) /* WEP: the profile's key and index, the next IV */
        len = wsec_wep_protect(wep_key, wep_len, wep_idx, wep_iv++ & 0xffffff, secbuf, 8 + n);
    else
        len = wsec_tkip_protect(sw_pair.key, sw_pair.key + 24, 0, ++sw_pair.tsc, own, da, own, secbuf,
                                8 + n);
    return wstation_raw((volatile uint8_t *)a, TXFBUF, own, cur.bssid, da, data_rates,
                        et == WPA_EAPOL_ETHERTYPE, secbuf, len);
}

/* EAPOL-Key to the AP. Once the AP uses the PTK it goes out Protected, as stock sends it (a later
 * rekey's messages). Before that it goes in the clear, also when the PTK is already in our core:
 * the AP installs its key only on our Message 4, so when that got lost and it resends Message 3,
 * the second Message 4 must be readable without the key. An AP accepts EAPOL in the clear at any
 * time (802.1X control port), so the clear one is never the wrong choice.
 * The core enciphers a Protected frame when it stages it, after the key commands queued before
 * (xport.c schedule): a rekey's Message 4 followed at once by the new PTK went out under the new
 * TK, which the AP installs only on that Message 4, so it resent Message 3 and then dropped us.
 * Its buffer is kept in eapol_buf and the new PTK waits until the core hands it back (as Linux
 * drivers wait for pending 802.1X frames before a key install). Under TKIP the app enciphers
 * here, before the new key exists. */
static int send_eapol(const unsigned char *f, unsigned n)
{
    unsigned a = tx_buf();
    int body;
    if (!a)
        return 0;
    /* under the app's own pairwise key (TKIP) the app enciphers it, once the AP uses the key */
    if (keyed && ap_keyed && sw_pair.cipher && prof_sec != WIFI_SEC_WEP)
        body = sw_data(a, cur.bssid, WPA_EAPOL_ETHERTYPE, f, n);
    else
        body = wstation_eapol((volatile uint8_t *)a, TXFBUF, own, cur.bssid, rates, f, n);
    if (body <= 0)
        return 0;
    if (keyed && ap_keyed && !sw_pair.cipher) {
        volatile unsigned char *b = (volatile unsigned char *)a;
        b[8 + 0x19] |= 0x40, b[0x94 + 0x19] |= 0x40;
        eapol_buf = a;
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
    if (state != WIFI_UP || tx_hold == TX_OFF || !same(f + 6, own, 6) || et == WPA_EAPOL_ETHERTYPE ||
        !(a = tx_buf())) {
        wifi_tx_drop++;
        return -1;
    }
    body = sw_pair.cipher || prof_sec == WIFI_SEC_WEP ? sw_data(a, f, et, f + 14, n - 14) :
           wstation_data((volatile uint8_t *)a, TXFBUF, own, cur.bssid, f, data_rates, et, f + 14,
                         n - 14, prof_sec != WIFI_SEC_OPEN);
    if (body <= 0) {
        wifi_tx_drop++;
        return -1;
    }
    tx_push(a, (unsigned)body, PEER_AP, 0x11);
    return 0;
}

/* ---- RX ---- */

static const unsigned char ap_bcast[6] = {0xff, 0xff, 0xff, 0xff, 0xff, 0xff}; /* every station */
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
        /* the 802.1X header (version, type, length) first: its length is read only once it is there */
        unsigned k = pl >= 4 ? 4 + (body[10] << 8 | body[11]) : ~0u;
        if (k <= pl && k <= sizeof eapframe) {
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
#define RX_SOFT 2         /* record byte 7: under a key the app deciphers (ADR 0136) */
#define REASON_MIC_FAILURE 14 /* IEEE 802.11 reason: Michael MIC failure (countermeasures) */

static void drop_link(void);

/* A deauthentication to the AP with an IEEE 802.11 reason code. */
static void send_deauth(unsigned reason)
{
    unsigned a = tx_buf();
    const unsigned char r[2] = {(unsigned char)reason, (unsigned char)(reason >> 8)};
    if (a && wframe((volatile uint8_t *)a, TXFBUF, 0xc0, cur.bssid, own, cur.bssid, rates, 0, r, 2) > 0)
        tx_push(a, 2, PEER_NONE, 0xffff);
}

/* A Michael MIC failure under the TKIP key (pairwise or group) with a good ICV: report it to the
 * AP, and on the second within CM_WINDOW_MS start the countermeasures (IEEE 802.11-2020
 * 12.5.2.4): deauthenticate with reason 14, drop the link and join no TKIP network for CM_HOLD_MS. */
static void mic_failure(int pairwise)
{
    unsigned char out[WPA_EAPOL_MAX];
    size_t n = wpa_mic_failure(&sm, pairwise, out);
    int second = mic_fail_seen && (int)(now - mic_fail_t) < (int)CM_WINDOW_MS;
    hex("tkip mic failure ", (unsigned)pairwise << 8 | (unsigned)second);
    if (n)
        send_eapol(out, (unsigned)n);
    mic_fail_seen = 1, mic_fail_t = now;
    if (!second)
        return;
    send_deauth(REASON_MIC_FAILURE);
    drop_link();
    cm_on = 1, cm_until = now + CM_HOLD_MS;
    kicked = 1, fail_step = WIFI_FAIL_KICKED, fail_code = REASON_MIC_FAILURE;
    if (state == WIFI_UP)
        post(WIFI_EV_DEAUTH, REASON_MIC_FAILURE);
    state = WIFI_FAILED, join_pending = 0;
}

/* Open a data frame from the AP that came up under one of the app's keys (RX_SOFT): copy its body
 * (n bytes) into secbuf and decipher it there. h is the 802.11 header (TA addr2; DA addr1 and SA
 * addr3, the frame coming from the DS). A TKIP frame must carry a TSC above its key's last one,
 * which it then takes. Returns the plaintext's length, *off its offset in secbuf, or -1 to drop. */
static int sw_open(const volatile unsigned char *h, const volatile unsigned char *body, unsigned n,
                   unsigned *off)
{
    unsigned char hh[24];
    struct swkey *k;
    unsigned long long tsc;
    int r;
    if (n < WSEC_WEP_OVERHEAD || n > sizeof secbuf)
        return -1;
    copy(hh, h, 24);
    copy(secbuf, body, n);
    if (!wsec_extiv(secbuf)) { /* WEP: a default key, unicast and group alike */
        k = &sw_grp[wsec_keyid(secbuf)];
        if (k->cipher != WSEC_WEP40 && k->cipher != WSEC_WEP104)
            return -1;
        r = wsec_wep_open(k->key, k->cipher == WSEC_WEP40 ? 5 : 13, secbuf, n);
        if (r < 0)
            return -1;
        *off = WSEC_WEP_HDR;
        return r;
    }
    k = (hh[4] & 1) ? &sw_grp[wsec_keyid(secbuf)] : &sw_pair;
    if (k->cipher != WSEC_TKIP || n < WSEC_TKIP_OVERHEAD)
        return -1;
    tsc = wsec_tkip_tsc(secbuf);
    if (tsc <= k->rxtsc)
        return -1; /* a replay, or a frame from before the key */
    r = wsec_tkip_open(k->key, k->key + 16, hh + 10, hh + 4, hh + 16, secbuf, n);
    if (r == WSEC_MIC_FAIL)
        mic_failure(k == &sw_pair);
    if (r < 0)
        return -1;
    k->rxtsc = tsc;
    *off = WSEC_TKIP_HDR;
    return r;
}

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
        /* A deauthentication or disassociation may also go to everyone: an AP that restarts or
         * shuts its BSS down sends one broadcast instead of one per station. From our BSS (addr2
         * and addr3) it ends the link as a unicast one does. */
        int all = (sub == 0xc0 || sub == 0xa0) && same(h + 4, ap_bcast, 6) && same(h + 16, cur.bssid, 6);
        if ((!all && !same(h + 4, own, 6)) || (fc & 0x0300) || len < 0xcc + 2)
            return;
        /* WPA3-SAE: the access point's authentication frames go to the SCM, whose wpa_supplicant
         * runs SAE (ADR 0136) */
        if (sub == 0xb0 && !(fc & 0x4000) && len >= 0xcc + 6 && le16(body) == AUTH_SAE) {
            unsigned char fr[24 + WIFI_SAE_MAX - 24];
            /* the header, then the body (the record keeps 8 bytes of cipher room between them) */
            if (state == WIFI_JOINING && jstep == J_SAE && 24 + len - 0xcc <= sizeof fr) {
                copy(fr, h, 24);
                copy(fr + 24, body, len - 0xcc);
                sae_queue(WIFI_SAE_FRAME, fr, 24 + len - 0xcc);
            }
            return;
        }
        /* Under PMF (ADR 0136) a robust frame from the AP (deauthentication, disassociation,
         * action) counts only protected: unicast under CCMP, group-addressed with BIP. An
         * unprotected deauthentication or disassociation starts an SA Query instead, an
         * unprotected action frame is dropped; any other protected frame is dropped too. */
        if (pmf && (sub == 0xc0 || sub == 0xa0 || sub == 0xd0)) {
            int m = all ? bip_open(h, body, len - 0xcc) : (fc & 0x4000) ? mgmt_open(h, body, len - 0xcc) : -2;
            if (m == -2 && sub != 0xd0)
                saq_start(sub, le16(body));
            if (m < 0)
                return;
            body = secbuf + (all ? 0 : WSEC_CCMP_HDR), len = 0xcc + (unsigned)m;
            if (len < 0xcc + 2)
                return;
        } else if (fc & 0x4000) {
            return;
        }
        if (sub == 0xd0 && pmf && len >= 0xcc + 4 && body[0] == 8) { /* SA Query (protected) */
            if (body[1] == 0) {        /* the AP asks: answer with its transaction id */
                const unsigned char r[4] = {8, 1, body[2], body[3]};
                send_protected(0xd0, r, 4);
            } else if (body[1] == 1 && saq_on && le16(body + 2) == saq_id)
                saq_on = 0;            /* it still has us: the unprotected frame was forged */
            return;
        }
        if (sub == 0xc0 || sub == 0xa0) { /* deauth / disassoc: keep the reason */
            kicked = 1, kick_sub = sub, kick_reason = le16(body);
            fail_step = WIFI_FAIL_KICKED, fail_code = le16(body);
        } else if (sub == 0xb0 && len >= 0xcc + 6 && le16(body) == auth_alg && !(fc & 0x4000)) {
            /* Open system answers transaction 2; shared key's 2 carries the challenge text and
             * its 4 the result (IEEE 802.11-2020 12.3.3) */
            unsigned seq = le16(body + 2), st = le16(body + 4);
            if (st) { /* refused: the AP's status code says why */
                fail_step = WIFI_FAIL_AUTH, fail_code = st;
            } else if (seq == 2 && auth_alg != AUTH_SHARED_KEY) {
                rxauthok = 1;
            } else if (seq == 2 && len >= 0xcc + 8 && body[6] == 16 && body[7] &&
                       len >= 0xcc + 8 + body[7]) {
                challenge_len = body[7];
                copy(challenge, body + 8, challenge_len);
                challenge_due = 1;
            } else if (seq == 4 && auth_alg == AUTH_SHARED_KEY) {
                rxauthok = 1;
            }
        } else if (sub == 0x10 && len >= 0xcc + 4) {
            if (le16(body + 2) == 0)
                rxassocok = 1;
            else
                fail_step = WIFI_FAIL_ASSOC, fail_code = le16(body + 2);
        }
        return;
    }
    /* data from the AP (fromDS), to us or a group address, with a body */
    if ((fc & 0x0c) != 8 || (fc & 0x0300) != 0x0200 || (sub & 0x40) ||
        !(same(h + 4, own, 6) || (h[4] & 1)))
        return;
    /* The core deciphers protected frames (ADR 0031) and marks them in record byte 7. Once keyed,
     * take nothing else: a frame still protected had no key, an unmarked one came in clear. One
     * exception: EAPOL to us in the clear (802.1X control port). An AP that missed our Message 4
     * has no key yet and resends Message 3 unprotected; the supplicant checks its MIC. */
    /* A frame under one of the app's keys (TKIP, ADR 0136) the app deciphers itself, and from then
     * on it counts as the core's deciphered ones do. */
    int clear_eapol = same(h + 4, own, 6) && len >= 0xcc + 8 && body[6] == 0x88 && body[7] == 0x8e;
    int dec = b[0x94 + 7] == RX_DECIPHERED;
    unsigned n = len - 0xcc, off = 0;
    const volatile unsigned char *pl = body;
    if ((fc & 0x4000) && b[0x94 + 7] == RX_SOFT) {
        int m = sw_open(h, body, n, &off);
        if (m < 0) {
            wifi_rx_drop++;
            return;
        }
        pl = secbuf + off, n = (unsigned)m, dec = 1;
    } else if (fc & 0x4000) {
        wifi_rx_drop++;
        return;
    }
    if (keyed && !dec && !clear_eapol) {
        wifi_rx_drop++;
        return;
    }
    if (keyed && dec && same(h + 4, own, 6))
        ap_keyed = 1; /* a group frame proves only the GTK, which the AP uses regardless */
    rx_data(h + 4, h + 16, pl, n);
}

/* ---- access point (ADR 0031): open only, BSSID = own MAC ---- */

/* Frames held for clients that doze (802.11 power save): a client that sets the power-management
 * bit listens only for beacons, so unicast frames for it wait here and its bit in the beacons' TIM
 * says so; it comes back for them with a PS-Poll (one frame each) or by clearing the bit (all of
 * them). While any client dozes, group frames wait for the next beacon, which is a DTIM beacon
 * with its group bit set, and follow it. Without this a phone that dozed after its DHCP request
 * never heard the answer. AP_HOLD frames per queue: a full queue loses its oldest frame. */
#define AP_HOLD 4u
struct hold {
    unsigned char head, n;
};
static struct held {
    unsigned short n;
    unsigned char f[14 + 1500];
} ap_held[WIFI_AP_CLIENTS + 1][AP_HOLD] /* the last row is the group frames' */
#ifdef NETCARD_HW
    /* 42 KB: in SDRAM past the image (app.ld .noinit), not in the image as zeros; only the
     * counts say what is in it */
    __attribute__((section(".noinit")))
#endif
    ;

static struct client {
    unsigned char mac[6], state, rates[8]; /* state 1 authenticated, 2 associated */
    unsigned char dozing, rate; /* rate code of its last frame (500 kb/s units) */
    signed char rssi;           /* and its level */
    struct hold h;
    unsigned seen, rx_data, rx_null, tx_ok, tx_failed;
    unsigned retries, repeats, seq; /* frames with Retry, repeated sequence numbers, the last one */
} cl[WIFI_AP_CLIENTS];
static struct hold ap_gh;              /* group frames held */
static unsigned char ap_body[WIFI_AP_BODY_MAX];
static unsigned ap_len, ap_ready, ap_deadline, ap_probes, ap_rx;
static unsigned ap_bint, ap_beacons;   /* beacon interval (TU), beacons sent */
static unsigned ap_t0;                 /* ms when the BSS came up: its TSF 0 */
static unsigned long long ap_tbtt;     /* TSF (us) of the next beacon, a multiple of the interval */
static unsigned ap_tim;                /* the TIM's offset in ap_body, 0 if it has none */
static unsigned ap_first, ap_hdrop;    /* ms from AP_START to the first beacon; held frames lost */
static const unsigned char ap_mrates[8] = {2, 2, 2, 2, 2, 2, 2, 2}; /* 1 Mb/s: every client hears it */

/* The core's ap_mode counters in its flight recorder, which HOST_FLAG2 (core 0x1fddc) locates:
 * bco/wild/fw/src/hw.h RC_APIN, RC_APNOFILT, RC_APKEEP (the core asserts these indices). */
#define CORE_RC_APIN 29u
#define CORE_RC_N 8u

/* An associated client heard within WIFI_AP_GONE_MS. One silent longer left without a word: it
 * is not listed (soundtapd's hold then sees the setup AP idle) and holds no group frame back. */
static int ap_fresh(const struct client *c)
{
    return c->state == 2 && now - c->seen < WIFI_AP_GONE_MS;
}

static void ap_tx_report(unsigned rec4, unsigned bad)
{
    unsigned slot = rec4 & 0xffff;
    if (state != WIFI_AP || rec4 >> 16 != 0x11 || slot >= WIFI_AP_CLIENTS || !cl[slot].state)
        return;
    if (bad)
        cl[slot].tx_failed++;
    else
        cl[slot].tx_ok++;
}

/* A client's core peer slot is its index: unicast to a client goes through its slot, as the
 * station's frames go through PEER_AP; a PEER_NONE frame goes to the BSS, which is us. */
static void ap_peer(const struct client *c, unsigned cmd)
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
    c->state = c->dozing = 0;
    c->h.n = 0;
    c->rx_data = c->rx_null = c->tx_ok = c->tx_failed = 0;
    c->retries = c->repeats = c->seq = 0;
}

/* to a client (its peer slot) or, with c = 0, to every client */
static int ap_send(const struct client *c, unsigned fc, const unsigned char *a1, const unsigned char *a3,
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

#define AP_IDLE_MS 300000u /* an associated client silent this long may lose its slot (hostapd's
                            * ap_max_inactivity) */

/* The client with this address; with add, a slot for a new one (an authentication), 0 if none. */
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
    /* Full: the longest silent client that is only authenticated goes, or an associated one that
     * has been silent past AP_IDLE_MS (it left without a deauthentication). An associated client
     * that is still there keeps its slot: else an authentication from any made-up address would
     * throw the owner's phone off the setup network. None to spare: 0, the caller refuses. */
    if (!old) {
        for (unsigned i = 0, age = 0; i < WIFI_AP_CLIENTS; i++)
            if ((cl[i].state == 1 || now - cl[i].seen >= AP_IDLE_MS) && (!old || now - cl[i].seen >= age))
                age = now - cl[i].seen, old = &cl[i];
        if (!old)
            return 0;
    }
    ap_drop(old);
    old->seen = now;
    return old;
}

/* The timestamp field of the AP's beacons and probe responses: its TSF, in microseconds since
 * the BSS came up. The core keeps no TSF for us. */
static void ap_stamp(unsigned char *b, unsigned long long us)
{
    for (unsigned i = 0; i < 8; i++)
        b[i] = (unsigned char)(us >> 8 * i);
}

/* The TSF now, to the millisecond, and the ms at which the TSF reaches us. */
static unsigned long long ap_tsf(void)
{
    return (unsigned long long)(now - ap_t0) * 1000u;
}

static unsigned ap_ms(unsigned long long us)
{
    return ap_t0 + (unsigned)((us + 999u) / 1000u);
}

/* A frame from the SCM held in row q (a client's index, or WIFI_AP_CLIENTS for group frames). */
static void ap_hold(unsigned q, struct hold *h, const unsigned char *f, unsigned n)
{
    if (h->n == AP_HOLD) {
        h->head = (unsigned char)((h->head + 1) % AP_HOLD), h->n--;
        ap_hdrop++;
    }
    struct held *s = &ap_held[q][(h->head + h->n) % AP_HOLD];
    s->n = (unsigned short)n;
    copy(s->f, f, n);
    h->n++;
}

/* Up to max held frames of row q out, fromDS to c (0: to everybody), More Data on each but the
 * row's last. What finds no transmit buffer stays for the next poll. */
static void ap_release(unsigned q, struct hold *h, const struct client *c, unsigned max)
{
    while (h->n && max-- && tx_buf()) {
        const struct held *s = &ap_held[q][h->head];
        unsigned fc = 0x0208 | (h->n > 1 ? 0x2000 : 0);
        if (!ap_send(c, fc, s->f, s->f + 6, c ? c->rates : ap_mrates, s->f[12] << 8 | s->f[13],
                     s->f + 14, s->n - 14u))
            return;
        h->head = (unsigned char)((h->head + 1) % AP_HOLD), h->n--;
    }
}

static int ap_anyone_dozing(void)
{
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
        if (ap_fresh(&cl[i]) && cl[i].dozing)
            return 1;
    return 0;
}

/* A client's power-management bit, from a data or null-data frame of it: waking up (bit clear)
 * releases what was held for it. */
static void ap_pm(struct client *c, unsigned fc)
{
    unsigned dozing = fc >> 12 & 1;
    c->dozing = (unsigned char)dozing;
    if (!dozing)
        ap_release((unsigned)(c - cl), &c->h, c, AP_HOLD);
}

/* One beacon: hostapd's body behind a broadcast header, at 1 Mb/s so every client hears it. The
 * core has no TBTT timer of its own (bco/wild/fw scan.c join), so ap_step sends one every beacon
 * interval from the main loop. Every beacon is a DTIM beacon (count 0, period 1): its TIM has the
 * group bit when group frames are held, which then follow it, and the bit of each client (AID =
 * slot + 1, one octet from offset 0) that has frames held. */
static void ap_beacon(void)
{
    static unsigned char b[WIFI_AP_BODY_MAX];
    copy(b, ap_body, ap_len);
    ap_stamp(b, ap_tbtt);
    if (ap_tim) {
        unsigned pvb = 0;
        for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
            if (cl[i].state == 2 && cl[i].h.n)
                pvb |= 2u << i;
        b[ap_tim + 2] = 0, b[ap_tim + 3] = 1, b[ap_tim + 4] = ap_gh.n ? 1 : 0;
        b[ap_tim + 5] = (unsigned char)pvb;
    }
    if (ap_send(0, 0x80, ap_bcast, own, ap_mrates, 0, b, ap_len)) {
        if (!ap_beacons++)
            ap_first = now - (ap_deadline - 1000);
        ap_release(WIFI_AP_CLIENTS, &ap_gh, 0, AP_HOLD);
    }
}

/* The probe request's SSID IE is a wildcard (length 0) or our SSID. */
static int ap_probe_for_us(const volatile unsigned char *ies, unsigned n)
{
    const unsigned char *our = ap_ie(0);
    for (unsigned o = 0; o + 2 <= n && o + 2 + ies[o + 1] <= n; o += 2 + ies[o + 1])
        if (ies[o] == 0)
            return !ies[o + 1] || (our && our[1] == ies[o + 1] && same(ies + o + 2, our + 2, our[1]));
    return 0;
}

/* A probe response to sa: the beacon body without its TIM (IEEE 802.11-2020 9.3.3.10). */
static void ap_probe_rsp(const volatile unsigned char *sa, const volatile unsigned char *ies,
                         unsigned n)
{
    static unsigned char r[WIFI_AP_BODY_MAX];
    unsigned char to[6];
    unsigned o = 12, k = 12;
    if (!ap_probe_for_us(ies, n) || (sa[0] & 1))
        return;
    copy(to, sa, 6);
    copy(r, ap_body, 12);
    while (o + 2 <= ap_len && o + 2 + ap_body[o + 1] <= ap_len) {
        unsigned l = 2 + ap_body[o + 1];
        if (ap_body[o] != 5) /* TIM: beacons only */
            copy(r + k, ap_body + o, l), k += l;
        o += l;
    }
    ap_stamp(r, ap_tsf());
    ap_send(0, 0x50, to, own, ap_mrates, 0, r, k);
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
    if (sub == 0xa4) { /* PS-Poll (the core pads it to a header): one held frame to its sender */
        if (same(h + 4, own, 6) && (c = ap_client(h + 10, 0)) && c->state == 2) {
            c->seen = now, c->dozing = 1, c->rssi = (signed char)b[0x94 + 4], c->rate = b[0x94 + 6];
            ap_release((unsigned)(c - cl), &c->h, c, 1);
        }
        return;
    }
    if ((fc & 0x0c) == 0) {
        if (sub == 0x40) { /* probe request: answer a wildcard one or one for our SSID */
            ap_probes++;
            ap_probe_rsp(h + 10, body, n);
            return;
        }
        if (!same(h + 4, own, 6) || !same(h + 16, own, 6))
            return;
        unsigned auth = sub == 0xb0 && n >= 6 && le16(body) == 0 && le16(body + 2) == 1; /* open system, seq 1 */
        c = ap_client(h + 10, auth);
        if (!c && auth) { /* every slot holds an associated client: status 17, too many stations */
            static const unsigned char full[6] = {0, 0, 2, 0, 17, 0};
            unsigned char to[6];
            copy(to, h + 10, 6);
            ap_send(0, 0xb0, to, own, ap_mrates, 0, full, sizeof full);
            return;
        }
        if (!c)
            return;
        c->seen = now;
        if (auth) {
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
    c->seen = now, c->rssi = (signed char)b[0x94 + 4], c->rate = b[0x94 + 6];
    unsigned seq = le16(h + 22);
    if (fc & 0x0800)
        c->retries++;
    if (seq >> 4 == c->seq >> 4 && (c->rx_data || c->rx_null))
        c->repeats++;
    c->seq = seq;
    if (sub & 0x40) {
        c->rx_null++;
    } else {
        c->rx_data++;
        rx_data(h + 16, h + 10, body, n);
    }
    ap_pm(c, fc);
}

/* An Ethernet frame from the SCM's wlan1: fromDS to an associated client or to every client;
 * held while that client dozes (group frames: while any client dozes), and behind frames still
 * held for it, so their order holds. */
static int ap_tx_eth(const unsigned char *f, unsigned n)
{
    struct client *c = 0;
    if (!ap_ready || !same(f + 6, own, 6) || (!(f[0] & 1) && (!(c = ap_client(f, 0)) || c->state != 2))) {
        wifi_tx_drop++;
        return -1;
    }
    if (c ? c->dozing || c->h.n : ap_gh.n || ap_anyone_dozing()) {
        ap_hold(c ? (unsigned)(c - cl) : WIFI_AP_CLIENTS, c ? &c->h : &ap_gh, f, n);
        return 0;
    }
    if (!ap_send(c, 0x0208, f, f + 6, c ? c->rates : ap_mrates, f[12] << 8 | f[13], f + 14, n - 14)) {
        wifi_tx_drop++;
        return -1;
    }
    return 0;
}

static void rx_frames(void)
{
    while (wr_avail(&sup, 3)) {
        const volatile unsigned *e = wr_reserve(&sup, 3, 1);
        unsigned buf = e[0], len = e[2] >> 16;
#ifdef BCO_DEBUG_TRACE
        dg_sup3++;
#endif
        if (len > RXBUF)
            len = RXBUF;
        if (buf - rxfbase < NRXBUF * RXBUF && (buf - rxfbase) % RXBUF == 0 && nfree < NRXBUF) {
            const volatile unsigned char *b = (const volatile unsigned char *)buf;
            unsigned fc = le16(b + 0xac);
            /* Supply length excludes the eight-byte pool header (stock subtracts 0xc4). */
            unsigned n = len < RXBUF - 8 ? len + 8 : RXBUF;
#ifdef BCO_DEBUG_TRACE
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
#ifdef BCO_DEBUG_TRACE
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
                if (have_cur && same(b + 0xac + 16, cur.bssid, 6)) {
                    last_beacon = now, beacons_up++;
                    if (state == WIFI_UP)
                        rssi_track((signed char)b[0xac + RXRSSI]);
                }
            } else if (state == WIFI_AP) {
                rx_ap(b, n);
            } else {
#ifdef BCO_DEBUG_TRACE
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
#ifdef BCO_DEBUG_TRACE
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
#ifdef BCO_DEBUG_TRACE
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

/* passes per scan (the last one directed at the profile's SSID, if any). On hardware the pure-listen (passive) pass receives nothing: the core's RX/AGC
 * only wakes once a frame has been transmitted, so every captured frame comes from an active pass
 * (probe-req TX, then listen). All passes are therefore active: each is a fresh probe on the
 * channel, which both keeps RX alive and gives a weak AP (the test AP, -56 dBm next to a -33 dBm
 * hotspot on ch1) several independent chances to answer a directed probe. */
#define SCAN_PASSES 2u
#define SCAN_ALL_ACTIVE 1

/* The SSID the SCM's SCAN asked the directed pass to probe for (ADR 0135), 0 = the profile's. */
static unsigned char scan_ssid[32];
static unsigned scan_ssidlen;

void wifi_scan_ssid(const unsigned char *ssid, unsigned n)
{
    scan_ssidlen = ssid && n <= 32 ? n : 0;
    copy(scan_ssid, ssid, scan_ssidlen);
    bss_want(1, scan_ssid, scan_ssidlen);
}

/* One probe request on chan ([6] SSID length, [7..] SSID, the core's probe_req; n = 0 is the
 * wildcard), then its listen; the answers reach the scan table through rx_frames. */
static void probe_on(unsigned chan, const unsigned char *ssid, unsigned n)
{
    static const unsigned char rate_block[13] = {0x0c, 0x02, 0x04, 0x0b, 0x12, 0x96, 0x24,
                                                 0x48, 0x6c, 0x0c, 0x18, 0x30, 0x60};
    unsigned w[16];
    unsigned char *pr = (unsigned char *)w;
    for (unsigned j = 0; j < 64; j++)
        pr[j] = j < 6 ? 0xff : 0;
    if (n && n <= 32) {
        pr[6] = (unsigned char)n;
        copy(pr + 7, ssid, n);
    }
    pr[0x27] = (unsigned char)chan;
    copy(pr + 0x28, rate_block, 13);
    /* listen ~112 ms per channel (0x36, units of 1024 ticks ~= ms) so a full ~102 ms beacon
     * interval is covered and a weak AP is caught by its beacon, not only by a probe response;
     * extend ~30 ms (0x38) each time a probe response keeps the dwell alive. */
    pr[0x36] = LISTEN_MS, pr[0x38] = 30, pr[0x3c] = 6;
    wr_cmd(&tx, 0xc2, 0x0013, 2, w, 62);
    DOORBELL();
}

/* A listen without a probe (0xc1: channel, dwell, band), for a channel the regulatory domain
 * allows only passively. */
static void listen_on(unsigned chan)
{
    const unsigned p[2] = {chan | LISTEN_MS << 16, 6};
    wr_cmd(&tx, 0xc1, 0x0013, 1, p, 6);
    DOORBELL();
}

static void scan_step(void)
{
    if (!due(scan_next))
        return;
    if (scan_pass == SCAN_PASSES) { /* 0xe4 was sent a dwell ago: done */
        state = WIFI_IDLE;
        scan_chan = 0;
#ifdef BCO_DEBUG_TRACE
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
    if (!(scan_chan = next_chan(scan_chan))) { /* the regulatory mask's channels, in order */
        if (++scan_pass == SCAN_PASSES) {
            wr_cmd(&tx, 0xe4, 0x13, 2, 0, 0);
            DOORBELL();
            scan_next = now + 200;
            return;
        }
        scan_chan = next_chan(0);
    }
    if (!SCAN_ALL_ACTIVE && scan_pass < SCAN_PASSES - 1) { /* passive beacon-listen passes (disabled: RX is deaf without a TX) */
        const unsigned p[2] = {scan_chan | 250u << 16, 6};
        wr_cmd(&tx, 0xc1, 0x0013, 1, p, 6);
        DOORBELL();
    } else if (!chan_active(scan_chan)) {
        listen_on(scan_chan); /* a passive channel of the regulatory domain: no probe on it */
    } else if (scan_pass == SCAN_PASSES - 1 && scan_ssidlen) {
        /* The last pass probes for one SSID: the one the SCM's SCAN named, else the profile's. A
         * hidden network answers only a probe that names it. The first pass stays a wildcard,
         * so the list still holds every network around. */
        probe_on(scan_chan, scan_ssid, scan_ssidlen);
    } else if (scan_pass == SCAN_PASSES - 1 && have_cur && cur.ssidlen <= 32) {
        probe_on(scan_chan, cur.ssid, cur.ssidlen);
    } else {
        probe_on(scan_chan, 0, 0);
    }
    scan_next = now + DWELL_MS;
}

/* A new scan generation. Age the table instead of wiping it: drop BSSes not heard in the last
 * BSS_KEEP scans, keep the rest so an intermittently-heard target survives into the join decision. */
static void age_table(void)
{
    unsigned w = 0;
    scan_gen++;
    for (unsigned i = 0; i < nbss; i++)
        if ((unsigned short)(scan_gen - bsstab[i].gen) < BSS_KEEP) {
            if (w != i)
                bsstab[w] = bsstab[i];
            w++;
        }
    nbss = w;
}

static void scan_begin(void)
{
    age_table();
#ifdef BCO_DEBUG_TRACE
    dg_sup3 = dg_frame = dg_bcn = dg_presp = dg_evt = dg_bok = dg_bokraw = 0;   /* per-scan so a capture is readable */
    dg_bss_short = dg_bss_nossid = 0;
#endif
    scan_pass = 0, scan_chan = 0, scan_next = now;
    state = WIFI_SCANNING;
}

int wifi_scan(int start)
{
#ifdef BCO_DEBUG_TRACE
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

/* What a BSS offers, WIFI_BSS_*. */
static unsigned bss_sec(const struct bss *b)
{
    return b->rsn ? WIFI_BSS_RSN : b->wpa ? WIFI_BSS_WPA : (b->caps & 0x10) ? WIFI_BSS_WEP : WIFI_BSS_OPEN;
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
    o->sec = bss_sec(b);
    o->grp = b->grp;
    o->rpair = b->rpair, o->rakm = b->rakm, o->rcaps = b->rcaps, o->wgrp = b->wgrp, o->wpair = b->wpair;
    /* the joined BSS: the core's own level (MIB 20:11); the rest: their last beacon */
    o->rssi = have_cur && state == WIFI_UP && same(cur.bssid, b->bssid, 6) ? link_rssi() : b->rssi;
    return 0;
}

/* ---- join ---- */

/* a heard above b: the stronger last beacon (a BSS without a level counts as the weakest), on a
 * tie the one heard in the later scan. */
static int stronger(const struct bss *a, const struct bss *b)
{
    int ra = a->rssi < 0 ? a->rssi : -128, rb = b->rssi < 0 ? b->rssi : -128;
    if (ra != rb)
        return ra > rb;
    return (unsigned short)(scan_gen - a->gen) < (unsigned short)(scan_gen - b->gen);
}

/* A BSS with the profile's SSID on a channel the regulatory domain allows. A passive channel
 * counts: the access point was heard there, and a station may answer the BSS it heard. */
static int named(const struct bss *b)
{
    return b->ssidlen == cur.ssidlen && same(b->ssid, cur.ssid, cur.ssidlen) && chan_ok(b->chan);
}

/* What join_attempt joins b with under a PSK profile, as wpa_supplicant picks it: RSN over WPA,
 * and in either pairwise CCMP over TKIP; the group is the access point's. 0 when neither IE lets
 * this station join (wscan.h rsn_parse, wpa_parse). */
static int choose(const struct bss *b, unsigned *proto, unsigned *pair, unsigned *grp)
{
    if (prof_sec == WIFI_SEC_SAE) { /* SAE: RSN, CCMP, PMF capable (WPA3 requires it) */
        if (!b->rsn || !(b->rakm & BSS_SAE) || !(b->rpair & BSS_CCMP) || b->grp != WPA_CIPHER_CCMP ||
            !(b->rcaps & WPA_CAP_MFPC))
            return 0;
        *proto = WPA_PROTO_RSN, *pair = WPA_CIPHER_CCMP, *grp = b->grp;
        return 1;
    }
    if (b->rsnok)
        *proto = WPA_PROTO_RSN, *pair = (b->rpair & BSS_CCMP) ? WPA_CIPHER_CCMP : WPA_CIPHER_TKIP,
        *grp = b->grp;
    else if (b->wpaok)
        *proto = WPA_PROTO_WPA, *pair = (b->wpair & BSS_CCMP) ? WPA_CIPHER_CCMP : WPA_CIPHER_TKIP,
        *grp = b->wgrp;
    else
        return 0;
    return 1;
}

/* A named BSS this station can join with the profile's security: an open network (no Privacy
 * bit, capability bit 4) for an open profile; for a PSK profile one whose RSN or WPA IE it can join
 * (choose). During the TKIP countermeasures no network that would use TKIP, pairwise or group. */
static int match(const struct bss *b)
{
    unsigned proto, pair, grp;
    if (!named(b))
        return 0;
    if (prof_sec == WIFI_SEC_OPEN)
        return !(b->caps & 0x10) && !b->rsn && !b->wpa;
    if (prof_sec == WIFI_SEC_WEP) /* the Privacy bit without an RSN or WPA IE */
        return (b->caps & 0x10) && !b->rsn && !b->wpa;
    /* SAE and PSK both take what choose takes: SAE a WPA3 or transition network, PSK its PSK side */
    if (!choose(b, &proto, &pair, &grp))
        return 0;
    return !(cm_on && !due(cm_until) && (pair == WPA_CIPHER_TKIP || grp == WPA_CIPHER_TKIP));
}

/* The SSID is there, but only with security this station cannot join with the profile (WPA3
 * only, management frame protection required, no CCMP, or another security than the profile's),
 * so the failure says so instead of "not found". */
static int seen_other(void)
{
    for (unsigned i = 0; i < nbss; i++)
        if (named(&bsstab[i]) && !match(&bsstab[i]))
            return 1;
    return 0;
}

/* The BSS to join, -1 if none: during a roam its target only, with a fixed BSSID that one only,
 * else the hinted BSSID when it was heard, else the strongest match. */
static int pick(void)
{
    int best = -1, hint = -1;
    for (unsigned i = 0; i < nbss; i++) {
        const struct bss *b = &bsstab[i];
        if (!match(b) || (roaming && !same(b->bssid, roam_bssid, 6)) ||
            ((prof_flags & WIFI_BSSID_FIXED) && !same(b->bssid, prof_bssid, 6)))
            continue;
        if ((prof_flags & WIFI_BSSID_HINT) && same(b->bssid, prof_bssid, 6))
            hint = (int)i;
        if (best < 0 || stronger(b, &bsstab[best]))
            best = (int)i;
    }
    return !roaming && hint >= 0 ? hint : best;
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
    /* One line per failed join, for support: the step it ended in and whether the AP answered the
     * authentication and association or sent us away. */
    hex("join failed ", jstep << 16 | rxauthok << 8 | rxassocok << 4 | kicked);
#ifdef BCO_DEBUG_TRACE
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
    if (roaming)
        post(WIFI_EV_ROAM_FAILED, 0); /* the old link is gone too: the SCM learns it now */
    roaming = 0;
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
        if (i < 0)
            fail_step = seen_other() ? WIFI_FAIL_SECURITY : WIFI_FAIL_NOT_FOUND, fail_code = 0;
        fail();
        return;
    }
    copy(cur.bssid, bsstab[i].bssid, 6);
    cur.chan = bsstab[i].chan;
    cur.caps = bsstab[i].caps; /* the association request mirrors its short slot and preamble */
    {   /* and carries the RSN or WPA IE with the ciphers chosen */
        unsigned proto = 0, pair = 0, grp = 0;
        choose(&bsstab[i], &proto, &pair, &grp);
        cur.proto = (unsigned char)proto, cur.pair = (unsigned char)pair, cur.grp = (unsigned char)grp;
        /* the AKM wpa_supplicant would take: SAE for an SAE profile, PSK-SHA256 over PSK; PMF
         * whenever the access point can and the pairwise cipher is CCMP */
        cur.akm = prof_sec == WIFI_SEC_SAE ? WPA_AKM_SAE :
                  proto == WPA_PROTO_RSN && (bsstab[i].rakm & BSS_PSK256) ? WPA_AKM_PSK_SHA256 : WPA_AKM_PSK;
        cur.mfp = proto == WPA_PROTO_RSN && pair == WPA_CIPHER_CCMP && (bsstab[i].rcaps & WPA_CAP_MFPC);
    }
    cur.rsn = bsstab[i].rsn, cur.wpa = bsstab[i].wpa; /* what STATUS reports it offers */
    cur.rpair = bsstab[i].rpair, cur.rakm = bsstab[i].rakm, cur.rcaps = bsstab[i].rcaps;
    cur.wgrp = bsstab[i].wgrp, cur.wpair = bsstab[i].wpair;
    for (unsigned r = 0; r < 8; r++)
        rates[r] = lo;
    keyed = ap_keyed = 0, keys_due = 0, rxauthok = rxassocok = kicked = 0, eappend = 0;
    eapol_buf = 0;
    /* WEP shared key asks with algorithm 1 at once; "auto" starts with open system */
    auth_alg = prof_sec == WIFI_SEC_WEP && wep_auth == WIFI_WEP_SHARED ? AUTH_SHARED_KEY :
               prof_sec == WIFI_SEC_SAE ? AUTH_SAE : 0;
    challenge_due = 0, sae_result = 0;
    build_join_bc(bc, cur.bssid, cur.chan);
    corebd = 0;
    wr_cmd_checked(&tx, 0xbc, MGMT_MODULE, 11, bc, sizeof bc);
    DOORBELL();
    jstep = J_SETUP, deadline = now + 1000;
}

static void retry(void)
{
    if (++jtries >= JOIN_TRIES)
        fail();
    else
        join_attempt();
}

static void snonce(unsigned char out[32])
{
    unsigned char mix[6 + 16];
    const unsigned v[4] = {now, wifi_rx_frames, wifi_tx_frames, WREG[0x580 / 4]};
    copy(mix, own, 6);
    for (unsigned i = 0; i < 16; i++)
        mix[6 + i] = v[i / 4] >> 8 * (i % 4);
    wpa_sha1_prf(seed, 32, "SoundTap SNonce", mix, sizeof mix, out, 32);
}

/* The join completed: the handshake, or for an open network the association. */
static void join_done(void)
{
    state = WIFI_UP;
    fail_step = fail_code = 0;
    last_beacon = now, beacons_up = 0;
    link_up();
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
        unsigned char jd[6] = {prof_sec != WIFI_SEC_OPEN, 1, 0, 0, 0, 0}; /* privacy, open the AC queues */
        if (!corebd && !due(deadline))
            return;
        if (!wr_cmd_checked(&tx, CMD_JOIN_DONE, 0x12, 11, jd, sizeof jd))
            return; /* ring full: still J_SETUP, the next poll tries again */
        DOORBELL();
        if (prof_sec == WIFI_SEC_SAE) { /* the SCM runs SAE (sae_start); then J_SAE */
            sae_start(WIFI_SAE_START);
            jstep = J_SAE, deadline = now + SAE_MS;
            return;
        }
        if (!send_auth()) {
            retry();
            return;
        }
        jstep = J_AUTH, deadline = now + 2000;
        return;
    }
    case J_SAE: /* the SCM's SAE: done once it sent the PMK and the result, then associate */
        if (sae_result == 1 && sae_pmk) {
            rxauthok = 1, jstep = J_AUTH;
            join_step();
        } else if (sae_result == 2 || due(deadline)) {
            if (sae_result != 2)
                fail_step = WIFI_FAIL_AUTH_TIMEOUT, fail_code = 0;
            sae_start(WIFI_SAE_ABORT);
            retry();
        }
        return;
    case J_AUTH:
        /* WEP shared key: answer the challenge, then wait for transaction 4. With "auto" an AP
         * that refuses open system for its algorithm gets shared key, in the same attempt. */
        if (challenge_due) {
            challenge_due = 0;
            if (!send_challenge())
                retry();
            return;
        }
        if (!rxauthok && fail_step == WIFI_FAIL_AUTH && fail_code == STATUS_ALG_UNSUPPORTED &&
            prof_sec == WIFI_SEC_WEP && wep_auth == WIFI_WEP_AUTO && auth_alg != AUTH_SHARED_KEY) {
            auth_alg = AUTH_SHARED_KEY, fail_step = fail_code = 0;
            if (!send_auth())
                retry();
            deadline = now + 2000;
            return;
        }
        if (rxauthok) { /* associate at once: the AP ages the auth state out within ~1 s */
            if (!send_assoc()) {
                retry();
                return;
            }
            jstep = J_ASSOC, deadline = now + 1500;
        } else if (due(deadline)) {
            if (fail_step != WIFI_FAIL_AUTH)
                fail_step = WIFI_FAIL_AUTH_TIMEOUT, fail_code = 0;
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
            if (prof_sec == WIFI_SEC_OPEN) { /* no handshake: the link is up */
                join_done();
                return;
            }
            /* WEP: the key is the app's (ADR 0136); the core only learns that frames under its
             * index are, and the link is up. A full ring tries again on the next poll. */
            if (prof_sec == WIFI_SEC_WEP) {
                sw_grp[wep_idx].cipher = 0;
                if (set_key(0, wep_idx, wep_key, wep_len == 5 ? WSEC_WEP40 : WSEC_WEP104)) {
                    jstep = J_KEYS;
                    return;
                }
                copy(sw_grp[wep_idx].key, wep_key, wep_len);
                sw_grp[wep_idx].cipher = wep_len == 5 ? WSEC_WEP40 : WSEC_WEP104;
                keyed = ap_keyed = 1;
                join_done();
                return;
            }
            snonce(sn);
            wpa_sm_init(&sm, pmk, cur.bssid, own, sn);
            /* Message 2 repeats the IE of the request */
            wpa_sm_config(&sm, cur.proto, cur.pair, cur.grp, cur.akm, rsn_caps());
            jstep = J_4WAY, deadline = now + 8000;
        } else if (due(deadline)) {
            if (fail_step != WIFI_FAIL_ASSOC)
                fail_step = WIFI_FAIL_ASSOC_TIMEOUT, fail_code = 0;
            retry();
        }
        return;
    case J_KEYS: /* a WEP key the full command ring did not take */
        if (!set_key(0, wep_idx, wep_key, wep_len == 5 ? WSEC_WEP40 : WSEC_WEP104)) {
            copy(sw_grp[wep_idx].key, wep_key, wep_len);
            sw_grp[wep_idx].cipher = wep_len == 5 ? WSEC_WEP40 : WSEC_WEP104;
            keyed = ap_keyed = 1;
            join_done();
        } else if (due(deadline)) {
            retry();
        }
        return;
    case J_4WAY:
        if (sm.state == WPA_DONE && keyed && !keys_due) {
            join_done();
        } else if (due(deadline)) {
            /* no Message 1, or Message 3 never verified: almost always a wrong password */
            if (fail_step != WIFI_FAIL_KICKED)
                fail_step = WIFI_FAIL_HANDSHAKE, fail_code = 0;
            retry();
        }
        return;
    }
}

/* Hand the keys the supplicant produced to the core, PTK before GTK. A full command ring
 * leaves a key due, tried again on the next poll instead of being lost. */
static void install_keys(void)
{
    /* the IGTK stays in the app: BIP runs in software (ADR 0136) */
    if ((keys_due & WPA_RX_INSTALL_IGTK) && sm.igtk_set) {
        copy(igtk, sm.igtk, 16);
        igtk_id = (unsigned)sm.igtk_idx, ipn = 0;
        for (unsigned i = 0; i < 6; i++)
            ipn |= (unsigned long long)sm.ipn[i] << (8 * i);
        keys_due &= ~WPA_RX_INSTALL_IGTK;
    }
    /* A CCMP PTK's TK goes to the engine; a TKIP one (TK and both MIC keys) stays here. Not
     * while a Message 4 under the old key is still in the core (send_eapol). */
    if ((keys_due & WPA_RX_INSTALL_PTK) && !eapol_buf &&
        !set_key(1, 0, sm.ptk + 32, sm.pairwise == WPA_CIPHER_TKIP ? WSEC_TKIP : WSEC_CCMP)) {
        keys_due &= ~WPA_RX_INSTALL_PTK, keyed = 1;
        /* PMF: the same TK protects the robust management frames, in software */
        pmf = sm.mfp && sm.pairwise == WPA_CIPHER_CCMP;
        if (pmf)
            copy(mgmt_tk, sm.ptk + 32, 16), mgmt_pn = MGMT_PN0, mgmt_rxpn = 0;
        sw_pair.cipher = 0;
        if (sm.pairwise == WPA_CIPHER_TKIP) {
            copy(sw_pair.key, sm.ptk + 32, 32);
            sw_pair.tsc = sw_pair.rxtsc = 0;
            sw_pair.cipher = WSEC_TKIP;
        }
    }
    /* A CCMP group key goes to the engine. A TKIP one (32 bytes: a WPA/WPA2 mixed router keeps
     * TKIP for its broadcasts) stays here and the core only learns that its frames are the app's
     * (ADR 0136), so mDNS, ARP and DHCP broadcasts come through. A key of any other length is
     * not installed. */
    if ((keys_due & WPA_RX_INSTALL_GTK) && !(keys_due & WPA_RX_INSTALL_PTK)) {
        unsigned idx = (unsigned)sm.gtk_idx & 3;
        int ccmp = sm.group == 4 && sm.gtk_len == 16, tkip = sm.group == 2 && sm.gtk_len == 32;
        if (!ccmp && !tkip) {
            keys_due &= ~WPA_RX_INSTALL_GTK;
        } else if (!set_key(0, idx, sm.gtk, ccmp ? WSEC_CCMP : WSEC_TKIP)) {
            keys_due &= ~WPA_RX_INSTALL_GTK;
            sw_grp[idx].cipher = 0;
            if (tkip) {
                copy(sw_grp[idx].key, sm.gtk, 32);
                sw_grp[idx].rxtsc = 0;
                sw_grp[idx].cipher = WSEC_TKIP;
            }
        }
    }
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
    keys_due |= (unsigned)r & (WPA_RX_INSTALL_PTK | WPA_RX_INSTALL_GTK | WPA_RX_INSTALL_IGTK);
    install_keys();
}

/* Drop the association locally: no frame goes on air, the core forgets the peer and its keys. */
static void drop_link(void)
{
    if (have_cur && (state == WIFI_UP || state == WIFI_JOINING)) {
        unsigned char pd[8] = {0};
        copy(pd, cur.bssid, 6);
        pd[6] = PEER_AP;
        if (keyed && prof_sec == WIFI_SEC_WEP) {
            set_key(0, wep_idx, 0, 0);
        } else if (keyed) {
            set_key(1, 0, 0, 0);
            if (sm.gtk_idx >= 0)
                set_key(0, (unsigned)(sm.gtk_idx & 3), 0, 0);
        }
        wr_cmd_checked(&tx, CMD_PEER_DEL, 0x12, 0x11, pd, sizeof pd);
        DOORBELL();
    }
    keyed = ap_keyed = 0, keys_due = 0, eapol_buf = 0;
    for (unsigned i = 0; i < sizeof sm; i++)
        ((volatile unsigned char *)&sm)[i] = 0;
    for (unsigned i = 0; i < sizeof sw_pair; i++)
        ((volatile unsigned char *)&sw_pair)[i] = 0;
    for (unsigned i = 0; i < sizeof sw_grp; i++)
        ((volatile unsigned char *)sw_grp)[i] = 0;
    pmf = saq_on = 0;
    for (unsigned i = 0; i < 16; i++)
        ((volatile unsigned char *)mgmt_tk)[i] = ((volatile unsigned char *)igtk)[i] = 0;
}

/* ---- background scan and roam (ADR 0135) ---- */

/* The next allowed channel after c other than the own one, 0 when none is left. */
static unsigned next_visit(unsigned c)
{
    while ((c = next_chan(c)) && c == cur.chan) {}
    return c;
}

/* The strongest access point of the SSID heard since this background scan began (on the own
 * channel or on a visit), other than the joined one; -1 if none. */
static int roam_candidate(void)
{
    int best = -1;
    for (unsigned i = 0; i < nbss; i++) {
        const struct bss *b = &bsstab[i];
        if (b->gen != scan_gen || b->rssi >= 0 || !match(b) || same(b->bssid, cur.bssid, 6) ||
            ((prof_flags & WIFI_BSSID_FIXED) && !same(b->bssid, prof_bssid, 6)))
            continue;
        if (best < 0 || stronger(b, &bsstab[best]))
            best = (int)i;
    }
    return best;
}

/* Leave the access point (locally, nothing on air) and join bsstab[i] with the same profile: the
 * same PMK, a fresh 4-way. */
static void roam_to(int i)
{
    copy(roam_bssid, bsstab[i].bssid, 6);
    drop_link();
    roaming = 1, jtries = 0, state = WIFI_JOINING;
    bg_phase = BG_IDLE, tx_hold = TX_OPEN;
    join_attempt();
}

/* A background scan visited every channel: roam when it found an access point at least
 * ROAM_DELTA stronger, else wait twice as long for the next one. */
static void bg_finish(void)
{
    int i = roam_candidate();
    bg_phase = BG_IDLE, bg_last = now, bg_scanned = 1;
    if (i >= 0 && bsstab[i].rssi >= cur_rssi() + ROAM_DELTA) {
        roam_to(i);
        return;
    }
    bg_interval = bg_interval * 2 < BG_LONG_MS ? bg_interval * 2 : BG_LONG_MS;
    bg_next = now + bg_interval;
}

/* Back on the own channel: the core's join (0xbc) retunes the radio and its receive filter to the
 * access point and keeps the peer and its keys (JOIN_DONE, which would drop them, is not sent).
 * Beacon loss does not count the time away. 0 while the command ring is full. */
static int go_home(void)
{
    unsigned char bc[28];
    build_join_bc(bc, cur.bssid, cur.chan);
    if (!wr_cmd_checked(&tx, 0xbc, MGMT_MODULE, 11, bc, sizeof bc))
        return 0;
    DOORBELL();
    scan_chan = 0;
    if ((int)(off_since - last_beacon) >= 0)
        last_beacon += now - off_since;
    tx_hold = TX_OPEN;
    return 1;
}

/* One step of the background scan, from wifi_poll while the link is up. */
static void bg_step(void)
{
    int r = cur_rssi();
    if (!roam_on)
        return;
    if (!weak && r < ROAM_THRESHOLD) {
        weak = 1, bg_interval = BG_SHORT_MS;
        bg_next = bg_scanned && !due(bg_last + BG_SHORT_MS) ? bg_last + BG_SHORT_MS : now;
    } else if (weak && r >= ROAM_THRESHOLD + ROAM_HYSTERESIS) {
        weak = 0;
    }
    switch (bg_phase) {
    case BG_IDLE:
        if (weak && due(bg_next)) {
            age_table(); /* a new generation: what this scan hears */
            bg_chan = 0, bg_phase = BG_HOME, bg_until = now;
        }
        return;
    case BG_HOME:
        if (!due(bg_until))
            return;
        if (!weak) { /* strong again: stop looking */
            bg_phase = BG_IDLE;
            return;
        }
        if (!(bg_chan = next_visit(bg_chan))) {
            bg_finish();
            return;
        }
        bg_phase = BG_DRAIN, bg_until = now + BG_DRAIN_MS, tx_hold = TX_DRAIN;
        return;
    case BG_DRAIN: /* no new credit; leave once the buffers are back, or after BG_DRAIN_MS */
        tx_reclaim();
        if (txnfree < NTXFBUF && !due(bg_until))
            return;
        if (chan_active(bg_chan))
            probe_on(bg_chan, cur.ssid, cur.ssidlen); /* directed: only the SSID's access points answer */
        else
            listen_on(bg_chan);
        scan_chan = bg_chan, off_since = now;
        bg_phase = BG_OFF, bg_until = now + DWELL_MS, tx_hold = TX_OFF;
        return;
    case BG_OFF:
        if (due(bg_until) && go_home())
            bg_phase = BG_HOME, bg_until = now + BG_HOME_MS;
        return;
    }
}

int wifi_join(const unsigned char *ssid, unsigned ssid_len, unsigned sec, const unsigned char *key,
              unsigned key_len, const unsigned char s[32], unsigned flags, const unsigned char bssid[6])
{
    if (ssid_len < 1 || ssid_len > 32 || state == WIFI_AP ||
        !((sec == WIFI_SEC_OPEN && !key_len) || (sec == WIFI_SEC_PSK && key_len == 32) ||
          (sec == WIFI_SEC_WEP && (key_len == 7 || key_len == 15) && key[0] < 4 && key[1] <= WIFI_WEP_AUTO) ||
          (sec == WIFI_SEC_SAE && !key_len)))
        return -1;
    drop_link();
    copy(cur.ssid, ssid, ssid_len);
    cur.ssidlen = ssid_len;
    cur.grp = cur.rsn = cur.wpa = cur.proto = cur.pair = 0; /* STATUS reports nothing of an earlier network until one is picked */
    cur.rpair = cur.rakm = cur.rcaps = cur.wgrp = cur.wpair = 0;
    cur.caps = 0;
    bss_want(0, ssid, ssid_len);
    for (unsigned i = 0; i < 32; i++)
        pmk[i] = sec == WIFI_SEC_PSK ? key[i] : 0;
    for (unsigned i = 0; i < sizeof wep_key; i++)
        wep_key[i] = sec == WIFI_SEC_WEP && i < key_len - 2 ? key[2 + i] : 0;
    wep_len = sec == WIFI_SEC_WEP ? (unsigned char)(key_len - 2) : 0;
    wep_idx = sec == WIFI_SEC_WEP ? key[0] : 0, wep_auth = sec == WIFI_SEC_WEP ? key[1] : 0;
    prof_sec = sec;
    copy(seed, s, 32);
    wep_iv = seed[0] | seed[1] << 8 | (unsigned)seed[2] << 16;
    prof_flags = bssid ? flags & (WIFI_BSSID_FIXED | WIFI_BSSID_HINT | WIFI_ROAM) : 0;
    if (bssid)
        copy(prof_bssid, bssid, 6);
    roam_on = (prof_flags & WIFI_ROAM) && !(prof_flags & WIFI_BSSID_FIXED);
    roaming = 0, bg_phase = BG_IDLE, tx_hold = TX_OPEN, bg_scanned = 0;
    have_cur = 1, jtries = 0;
    fail_step = fail_code = 0;
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
    for (unsigned i = 0; i < sizeof wep_key; i++)
        wep_key[i] = 0;
    wep_len = 0;
    bss_want(0, 0, 0);
    have_cur = 0, join_pending = 0;
    prof_flags = roam_on = roaming = 0, bg_phase = BG_IDLE, tx_hold = TX_OPEN;
    if (state != WIFI_SCANNING && state != WIFI_AP)
        state = WIFI_IDLE;
}

/* AP start: 0xbc with our own MAC as BSSID, the beacon interval and the channel tunes the core and
 * puts it in ap_mode (bco/wild/fw scan.c), where it passes us probe requests; once it reports the
 * BSS (0xbd, or after a second) JOIN_DONE opens its queues and ap_step starts beaconing. Stock's
 * core beacons from a template (0xe2/0xe3) on its own TBTT timer; ours has neither, so the beacons
 * come from here. The body must be a well-formed IE list with an SSID. */
int wifi_ap_start(unsigned chan, unsigned bint, const unsigned char *body, unsigned len)
{
    unsigned char bc[28];
    unsigned o = 12, ssid = 0;
    if (!chan_active(chan) || bint < 20 || bint > 1000 || len < 12 || len > sizeof ap_body ||
        state == WIFI_SCANNING)
        return -1;
    unsigned tim = 0;
    while (o + 2 <= len && o + 2 + body[o + 1] <= len) {
        ssid |= body[o] == 0;
        if (body[o] == 5 && body[o + 1] >= 4)
            tim = o;
        o += 2 + body[o + 1];
    }
    if (o != len || !ssid)
        return -1;
    wifi_leave();
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
        ap_drop(&cl[i]);
    copy(ap_body, body, len);
    ap_len = len, ap_ready = 0, ap_bint = bint, ap_beacons = 0, ap_tim = tim;
    ap_first = ap_hdrop = 0, ap_gh.n = 0;
    build_join_bc(bc, own, chan);
    bc[6] = bint, bc[7] = bint >> 8; /* beacon interval (a station passes its AP's, 100) */
    corebd = 0;
    if (!wr_cmd_checked(&tx, 0xbc, MGMT_MODULE, 11, bc, sizeof bc))
        return -1;
    DOORBELL();
    ap_deadline = now + 1000;
    state = WIFI_AP;
    return 0;
}

void wifi_ap_stop(void)
{
    if (state == WIFI_AP) {
        state = WIFI_IDLE;
        /* every peer goes, and with them the core's ap_mode (bco/wild/fw sta.c) */
        if (wr_cmd_checked(&tx, CMD_PEER_CLEAR, 0x12, 0x11, 0, 0))
            DOORBELL();
    }
    ap_ready = 0, ap_gh.n = 0;
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
        ap_drop(&cl[i]);
}

void wifi_ap_counters(unsigned c[WIFI_AP_COUNTERS])
{
    unsigned v[WIFI_AP_COUNTERS] = {ap_ready, ap_probes, ap_rx, wifi_tx_ok, wifi_tx_failed,
                                    wifi_tx_drop, wifi_rx_frames, ap_beacons};
    copy((unsigned char *)c, (unsigned char *)v, sizeof v);
}

unsigned wifi_ap_clients(unsigned char macs[][6])
{
    unsigned n = 0;
    for (unsigned i = 0; state == WIFI_AP && i < WIFI_AP_CLIENTS; i++)
        if (ap_fresh(&cl[i]))
            copy(macs[n++], cl[i].mac, 6);
    return n;
}

void wifi_ap_diag(unsigned d[WIFI_AP_DIAG])
{
    unsigned a = (unsigned)MB16(-8) << 2; /* HOST_FLAG2: the core's flight recorder, 0 before */
    d[0] = ap_first;
    for (unsigned i = 0; i < 3; i++)
        d[1 + i] = a && a < 0x1fd00u ? *(const volatile unsigned *)(WSRAM + a + 4 * (CORE_RC_APIN + i)) : 0;
    d[4] = ap_hdrop;
}

void wifi_ap_extra(unsigned d[WIFI_AP_EXTRA])
{
    unsigned a = (unsigned)MB16(-8) << 2;
    for (unsigned i = 0; i < WIFI_AP_EXTRA; i++)
        d[i] = a && a < 0x1fd00u ? *(const volatile unsigned *)(WSRAM + a + 4 * (CORE_RC_APIN + 3 + i)) : 0;
}

unsigned wifi_ap_client_info(struct wifi_ap_client o[WIFI_AP_CLIENTS])
{
    unsigned n = 0;
    for (unsigned i = 0; state == WIFI_AP && i < WIFI_AP_CLIENTS; i++) {
        const struct client *c = &cl[i];
        if (!ap_fresh(c))
            continue;
        copy(o[n].mac, c->mac, 6);
        o[n].dozing = c->dozing, o[n].held = c->h.n, o[n].rssi = c->rssi, o[n].rate = c->rate;
        o[n].idle_ms = now - c->seen, o[n].rx_data = c->rx_data, o[n].rx_null = c->rx_null;
        o[n].tx_ok = c->tx_ok, o[n].tx_failed = c->tx_failed;
        o[n].retries = c->retries, o[n].repeats = c->repeats, o[n].seq = c->seq;
        n++;
    }
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
        ap_t0 = now, ap_tbtt = 0;
    }
    /* what is still held for a client that woke up, and group frames once nobody dozes, go as
     * transmit buffers free up */
    for (unsigned i = 0; i < WIFI_AP_CLIENTS; i++)
        if (cl[i].state == 2 && !cl[i].dozing && cl[i].h.n)
            ap_release(i, &cl[i].h, &cl[i], AP_HOLD);
    if (ap_gh.n && !ap_anyone_dozing())
        ap_release(WIFI_AP_CLIENTS, &ap_gh, 0, AP_HOLD);
    /* One beacon at each target beacon transmission time: a TSF that is a multiple of the
     * interval (1 TU = 1024 us), stamped with that TSF. A dozing phone wakes at those times to
     * hear it. Before, beacons went every 102 ms instead of 102.4 and carried the ms clock, so
     * they drifted 0.4 ms a beacon against the times a phone wakes at (it missed most of them).
     * A late poll sends one, not a burst: the missed times are skipped. */
    if (due(ap_ms(ap_tbtt))) {
        ap_beacon();
        do
            ap_tbtt += ap_bint * 1024ull;
        while (due(ap_ms(ap_tbtt)));
    }
}

unsigned wifi_state(void)
{
    return state;
}

void wifi_fail(unsigned *step, unsigned *code)
{
    *step = fail_step, *code = fail_code;
}

int wifi_current(struct wifi_bss *o)
{
    if (!have_cur) {
        o->ssid_len = o->chan = o->sec = o->grp = 0, o->rssi = 0;
        o->rpair = o->rakm = o->rcaps = o->wgrp = o->wpair = 0;
        for (unsigned i = 0; i < 6; i++)
            o->bssid[i] = 0;
        return 0;
    }
    o->ssid_len = cur.ssidlen;
    copy(o->ssid, cur.ssid, cur.ssidlen);
    copy(o->bssid, cur.bssid, 6);
    o->chan = cur.chan, o->sec = bss_sec(&cur), o->rssi = state == WIFI_UP ? link_rssi() : 0;
    o->grp = cur.grp; /* the target's from join_attempt on, 0 before */
    o->rpair = cur.rpair, o->rakm = cur.rakm, o->rcaps = cur.rcaps, o->wgrp = cur.wgrp, o->wpair = cur.wpair;
    return 1;
}

void wifi_poll(unsigned t)
{
    now = t;
#ifdef BCO_DEBUG_TRACE
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
        /* The AP sent us away, or went quiet (only judged once its beacons were seen at all, and
         * not while a background scan has the radio on another channel). The SCM hears of it at
         * once (ADR 0135). */
        saq_check();
        if (kicked || (beacons_up && bg_phase != BG_OFF && due(last_beacon + WIFI_BEACON_LOSS_MS))) {
            hex("link lost kicked ", kicked);
            if (kicked)
                post(kick_sub == 0xc0 ? WIFI_EV_DEAUTH : WIFI_EV_DISASSOC, kick_reason);
            else
                post(WIFI_EV_BEACON_LOSS, 0);
            drop_link();
            state = WIFI_FAILED;
            bg_phase = BG_IDLE, tx_hold = TX_OPEN;
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
            else if (due(last_prog_t + 3000)) {
                hex("link lost tx wedge ", prog);
                post(WIFI_EV_TX_WEDGE, 0);
                drop_link();
                bg_phase = BG_IDLE, tx_hold = TX_OPEN;
                wifi_reset_core();
                state = WIFI_FAILED;
                last_prog_t = now;
            }
        }
#endif
        if (state == WIFI_UP)
            bg_step();
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
#ifdef BCO_DEBUG_TRACE
    if (wild_own_type == 4) {
        unsigned b = 0x30000000 + desc(1);
        hex("wild id reg0 ", *(volatile unsigned *)(b + 0x04));
        hex("wild id mb218 ", *(volatile unsigned *)(b + 0x08));
        hex("wild id reg600 ", *(volatile unsigned *)(b + 0x10));
        hex("wild id reg734 ", *(volatile unsigned *)(b + 0x18));
        hex("wild id regb00 ", *(volatile unsigned *)(b + 0x1c));
        hex("wild id rf0 ", *(volatile unsigned *)(b + 0x20));
    }
#endif
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
 * state already cleared by the caller. A sticky MAC wedge only clears on a fresh core boot: the
 * TX-wedge self-heal in wifi_poll and WILD_BOOT call it, FORGET does not. It is the same ~1 s
 * bring-up wifi_init runs at boot, busy waits included. */
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
