/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Scan and join (router module 0x13).  Stock scan command 0xc1 = stock handler 0x8f574, which in
 * order does: f68c (MAC prep), 0x1029c (MAC scan setup; its first act is the filter reset 0x10600),
 * 0x78dc (rf_tune -> tune.c), 0x10184 (arm the dwell timer, see scan_poll).  Command 0xc2 (stock
 * 0xd5f4) is the active scan: one probe request, then listen for probe responses.  The +80 RF
 * transactions stock emits after this come from the timer-driven VCO monitor in the scan-service loop (stock ~0x6c00:
 * 0x84c0 vco_cal, 0x8ec8 osc/class re-measure, 0x8294 band-class scan), not from the handler.
 *
 * scan_mac_prep below is the clean-room core-MAC register programming that runs before the tune
 * (stock f68c + 0x10600 + 0x1029c + the f574 tail): a fixed hardware init sequence, done with the
 * real read-modify-write operations so it tracks the chip's prior register state, not a replayed
 * value dump.  The register meanings (filter/rate/station-address block at 0x604/0x61c/0x628/0x634/0x638/
 * 0x650../0x674/0x6b0/0x6b8/0x908/0xa2c) are transcribed as hardware facts, like the RF tables in
 * tune.c.  Verified against compare.py: it reproduces stock register writes #15769..#15796, after
 * which the tune's RF programming begins and already matches bit-for-bit. */
#include "hw.h"

#define C_SCAN_CHANNEL 0xc1
#define C_SCAN_PROBE   0xc2
#define C_JOIN         0xbc

/* Scan listens with the basic-rate config (stock reads this from the MAC state ps[0]+0x91; it is 0
 * for a scan, and only index 20 takes the 0x908 bit-2 path).  Kept as a named constant until the
 * MAC-state model lands. */
#define SCAN_RATE_CFG 0u

/* Stock 0x7a30: reset the RX gain block (0xa2c state 0/1 -> 1 with 100-tick settles; status
 * 0xa10 bit 5 acked through 0xa14). */
static void gain_reset(void)
{
    unsigned v = REG(0xa2c);
    if (v == 1) {
        REG(0xa14) = 0x20;
        delay(100);
    }
    if (v <= 1) {
        REG(0xa2c) = 1;
        delay(100);
        if (!(REG(0xa10) & 0x20))
            return;
    } else if (v > 3) {
        return;
    }
    REG(0xa14) = 0x20;
}

/* Stock 0x10600: receive filter reset (also the end of every scan). */
static void filter_reset(void)
{
    REG(0x61c) &= ~4u;
    REG(0x6b8) = 0;
    REG(0x61c) &= ~0x200u;
    REG(0x604) |= 0x2000u;
    REG(0x61c) &= ~0x400u;
    for (unsigned i = 0; i < 8; i++)
        REG(0x650 + i * 4) = 0;               /* address/multicast filter slots */
    REG(0x674) = 0;
    gain_reset();
}

/* Receive filter with our station address (stock 0x10600 + 0x1029c), for scans and the join. */
static void station_filter(void)
{
    filter_reset();
    REG(0x634) = own_mac[0] | own_mac[1] << 8 | own_mac[2] << 16 | (unsigned)own_mac[3] << 24;
    REG(0x638) = own_mac[4] | own_mac[5] << 8;
    REG(0x604) &= ~0x2000u;
    REG(0x628) = 0x400;
    REG(0x628) = 0;
    REG(0x61c) |= 0x400u;
    REG(0x61c) |= 4u;
    REG(0x61c) |= 0x200u;
}

/* The scan filter adds the f574 / d858 tail before the tune; shared by 0xc1 and 0xc2. */
static void scan_filter(void)
{
    station_filter();
    REG(0x674) &= ~0x800000u;
    REG(0x604) &= ~1u;
}

/* f68c: MAC prep (payload band 6 path), before a scan and before a join. */
static void mac_prep(void)
{
    REG(0x62c) = 0x800a070au;                 /* RX descriptor/filter control */
    REG(0x6b0) = SCAN_RATE_CFG - 1u;          /* backoff slot time in us - 1; join sets it */
    if (SCAN_RATE_CFG == 20u)
        REG(0x908) |= 4u;
    else
        REG(0x908) &= ~4u;
}

static void scan_mac_prep(void)
{
    mac_prep();
    scan_filter();
}

/* Active scan (stock 0xd5f4): one probe request. Payload: [0..5] BSSID, [6] SSID length, [7..]
 * SSID, [0x27] channel, [0x28] rate count, [0x29..] rates, [0x36]/[0x38]/[0x3a] u16 timings
 * (units of 1024 ticks), [0x3c] band code. The frame goes out on the management queue right after
 * the tune; stock then waits (p[0x3a] << 10) + 7000 ticks for responses (dwell timer state 7). */
static unsigned char probe_frame[24 + 2 + 32 + 2 + 8 + 2 + 4];
static struct {
    unsigned z0;
    unsigned short len, z1, rate, cls;
    unsigned hdrlen;
    unsigned char *hdr;
    unsigned l1;
    unsigned char *p1;
    unsigned l2;
    unsigned char *p2;
    unsigned char z2[0x24];
} probe_desc;

#define MGMT_RATE 8u   /* stock's management-frame rate code below rate class 8 (ROM ps0+0x24) */

static void probe_req(const unsigned char *p)
{
    unsigned char *f = probe_frame, *ie = f + 24;
    unsigned ssid = p[6] > 32 ? 32 : p[6], n = p[0x28] > 12 ? 12 : p[0x28], k, r = n > 8 ? 8 : n;

    f[0] = 0x40;                               /* probe request; duration, seq stay 0 */
    for (k = 0; k < 6; k++)
        f[4 + k] = 0xff, f[10 + k] = own_mac[k], f[16 + k] = p[k];
    ie[0] = 0, ie[1] = ssid;
    copy_bytes(ie + 2, p + 7, ssid);
    k = 2 + ssid;
    ie[k++] = 1, ie[k++] = r;                  /* supported rates */
    copy_bytes(ie + k, p + 0x29, r);
    k += r;
    if (n > 8) {
        ie[k++] = 0x32, ie[k++] = n - 8;       /* extended supported rates */
        copy_bytes(ie + k, p + 0x29 + 8, n - 8);
        k += n - 8;
    }
    probe_desc.len = 24 + k + 4;
    probe_desc.rate = MGMT_RATE;
    probe_desc.cls = p[0x3c] == 4 ? 10 : 4;
    probe_desc.hdrlen = 24;
    probe_desc.hdr = f;
    probe_desc.l1 = k;
    probe_desc.p1 = ie;
}

/* Dwell: MAC timer slot 2 (deadline 0x6c4, enable 0x6b8 bit 2, stock 0x10184), served from the
 * FIQ (mac.c) and stepped here (stock 0x10dbc). Passive: 4 listen -> 5 tail -> host 0xc3.
 * Active: 7 probe wait -> 6 listen -> 8 tail -> host 0xc4. The 5000-tick tail is the stock ROM
 * timing constant (0x120be + 0x22); 2000 and 5120 are its fixed margins. */
#define DWELL_TAIL 5000u

volatile unsigned dwell_fired;
volatile unsigned dwell_state;
unsigned joined;                 /* 1 after a join: keep and ACK the BSS's data frames */
unsigned sta_mgmt_cls = 4;       /* PHY rate-class for host mgmt/EAPOL TX: 4 = 2.4 GHz basic, set at join */
static unsigned probe_listen, probe_extend, probe_backoff, rand_state;

static void dwell_arm(unsigned state, unsigned deadline)
{
    dwell_state = state;
    REG(0x6bc) = 4;                           /* clear any stale slot-2 status: a fire the FIQ missed
                                                 (0x6b4 bit2 still set) stops the hardware re-triggering,
                                                 which wedges every scan after the first */
    REG(0x6c4) = deadline & 0x3ffffff;
    REG(0x6b8) |= 4;
}

/* A probe response while listening: listen on from the old deadline (stock e7ac + 0xaadc). */
void dwell_extend(void)
{
    REG(0x6b8) &= ~4u;
    dwell_arm(6, REG(0x6c4) + probe_extend);
}

/* Stock 0x2504; one state shared by all stock callers, here only the probe backoff uses it. */
unsigned lcg(void)
{
    return rand_state = rand_state * 0x41c64e6du + 0x3039;
}

extern volatile unsigned dg_rxfiq, dg_rxkeep, dg_rxfcs, dg_rxdwell;   /* mac.c: per-scan RX counters */

static void scan_stop(unsigned event)
{
    extern volatile unsigned dg_vco;            /* tune.c: last VCO cal result for this channel */
    unsigned snap[6] = { dg_rxfiq, dg_rxkeep, rx_chan, dg_vco, dg_rxfcs, dg_rxdwell }; /* did the MAC + RF work THIS channel? */
    dwell_state = 0;
    route(event, 2, 0x13, snap, sizeof snap);
    REG(0x604) |= 1;                          /* receiver off */
    filter_reset();
    rx_chan = 0;
}

void scan_poll(void)
{
    if (!dwell_fired)
        return;
    dwell_fired = 0;
    switch (dwell_state) {
    case 4:
    case 6:
        gain_reset();
        dwell_arm(dwell_state == 4 ? 5 : 8, REG(R_TIMER) + DWELL_TAIL);
        break;
    case 7:
        REG(0x674) = probe_backoff;
        dwell_arm(6, REG(R_TIMER) + probe_listen + SCAN_RATE_CFG * (probe_backoff & 0x3ff) + 5120);
        break;
    case 5:
        scan_stop(0xc3);
        break;
    case 8:
        REG(0x61c) |= 4;
        scan_stop(0xc4);
        break;
    }
}

static unsigned u16le(const unsigned char *p)
{
    return p[0] | p[1] << 8;
}

/* Stock 0xf9b8: a rate (500 kb/s units, basic bit ignored) to the MAC rate code; DSSS rates
 * 2/5.5/11 Mb/s carry the short-preamble flag 4 unless long_pre. */
unsigned rate_code(unsigned r, int long_pre)
{
    static const unsigned char rates[12] = {2, 4, 11, 22, 12, 18, 24, 36, 48, 72, 96, 108};
    static const unsigned char codes[12] = {4, 1, 2, 3, 11, 15, 10, 14, 9, 13, 8, 12};
    r &= 0x7f;
    for (unsigned i = 0; i < 12; i++)
        if (rates[i] == r)
            return codes[i] | (i && i < 4 && !long_pre ? 4 : 0);
    return 5;
}

/* Stock 0xe494: the ACK rate for each received rate code = the highest basic rate of the same
 * modulation at or below it; rates = {count, rate...}, basic rates have bit 7. The join's rate
 * block is 13 bytes, so the count is clamped to 12 (a count up to 255 read past the payload). */
#define JOIN_RATES 12u
unsigned short resp_class[16] = {5, 5, 5, 5, 5, 5, 5, 5, 11, 11, 11, 11, 11, 11, 11, 11};

static void resp_classes(const unsigned char *rates)
{
    static const unsigned char dsss[4] = {2, 4, 11, 22}, ofdm[8] = {96, 48, 24, 12, 108, 72, 36, 18};
    static const unsigned char ofdm_def[8] = {9, 9, 10, 11, 9, 9, 10, 15};
    unsigned i, k, r, n = rates[0] > JOIN_RATES ? JOIN_RATES : rates[0];

    for (i = 1; i < 8; i++)
        resp_class[i] = i;
    for (i = 0; i < 4; i++)
        for (k = n; k--; ) {
            r = rates[1 + k];
            if (r & 0x80 && rate_code(r, 1) < 8 && (r & 0x7f) <= dsss[i]) {
                resp_class[i] = rate_code(r, 1);
                resp_class[i + 4] = rate_code(r, 0);
                break;
            }
        }
    for (i = 0; i < 8; i++) {
        resp_class[8 + i] = ofdm_def[i];
        for (k = n; k--; ) {
            r = rates[1 + k];
            if (r & 0x80 && rate_code(r, 1) >= 8 && (r & 0x7f) <= ofdm[i]) {
                resp_class[8 + i] = rate_code(r, 1);
                break;
            }
        }
    }
}

/* Join (stock 0x10b08 -> 0x10354). Payload: [0..5] BSSID (kept by stock for beacon tracking, not yet
 * needed here), [6..7] beacon interval (1024 ticks),
 * [8] channel, [9..21] {count, rates}, [26] band code. Same MAC prep and station filter as a
 * scan, the BSS's response rates, the tune, then the receiver on (0x604 bit 0 clear) when the
 * BSS beacons. A scan still running ends with 0xc3; the host then gets 0xbd (module 8).
 * stock also arms a TBTT timer (slot 0, 0x618) whose FIQ queues the host's beacon
 * template on queue 9 (AP/IBSS); a station never sets one, so it is not ported. */
static void join(const unsigned char *p)
{
    unsigned interval = u16le(p + 6);
    mac_prep();
    /* Backoff slot time - 1 (us). mac_prep leaves 0xffffffff, with which a queue-9 start with
     * backoff slots never went out (ST20): the slots never elapse. 9 us is the OFDM/ERP
     * short slot. 20 us for a BSS with 11b-only stations (capability bit 10 clear). */
    REG(0x6b0) = 9 - 1;
    station_filter();
    resp_classes(p + 9);
    sta_mgmt_cls = p[26] == 4 ? 10 : 4;   /* same band->class map the working probe uses (probe_req) */
    rf_tune(p[26], p[8]);
    REG(0x61c) |= 0x200;
    if (interval)
        REG(0x604) &= ~1u;
    else
        REG(0x604) |= 1;
    if (dwell_state) {
        dwell_state = 0;
        route(0xc3, 2, 0x13, 0, 0);
    }
    rx_chan = 0;
    joined = 1;
    route(0xbd, 8, 0x13, 0, 0);
}

void scan_cmd(unsigned type, const unsigned char *payload, unsigned len)
{
    if (type == C_SCAN_CHANNEL && len >= 5) {
        if (payload[0] == 1)            /* channel 1 = a fresh scan starts: make rx counters per-scan */
            dg_rxfiq = dg_rxkeep = dg_rxfcs = dg_rxdwell = 0;
        scan_mac_prep();
        /* stock f574: payload[0] = channel, payload[2..3] = dwell (1024 ticks), payload[4] = band
         * code, into the tune (stock 0x78dc). */
        rf_tune(payload[4], payload[0]);
        rx_chan = payload[0];
        dwell_arm(4, REG(R_TIMER) + (u16le(payload + 2) << 10) + DWELL_TAIL);
        /* the timer-driven VCO monitor (~0x6c00) is not ported; see the header. */
    } else if (type == C_SCAN_PROBE && len >= 0x3d) {
        probe_req(payload);
        probe_backoff = (lcg() >> 16 & OP_CWMIN) | 0x1820000;
        probe_listen = u16le(payload + 0x36) << 10;
        probe_extend = u16le(payload + 0x38) << 10;
        if (payload[0x27] == 1)       /* fresh scan: reset the per-scan RX counters (no passive C_SCAN_CHANNEL does it now) */
            dg_rxfiq = dg_rxkeep = dg_rxfcs = dg_rxdwell = 0;
        scan_mac_prep();              /* mac_prep (0x62c RX control) + scan_filter: C_SCAN_CHANNEL no longer runs */
        rf_tune(payload[0x3c], payload[0x27]);
        rx_chan = payload[0x27];
        mac_send_mgmt(&probe_desc.len, 0);
        dwell_arm(7, REG(R_TIMER) + (u16le(payload + 0x3a) << 10) + DWELL_TAIL + 2000);
    } else if (type == C_JOIN && len >= 27) {
        join(payload);
    }
}
