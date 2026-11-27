/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The receive path, from the air to a host buffer: the MAC FIQ's keep/ACK filter (src/mac.c) by
 * frame type, address filter, FCS, scan state and length; the receive-area read pointer and its
 * wrap; frames the MAC overwrote; the record the host gets (src/xport.c rx_deliver) with its rate,
 * level, channel and peer slot; the cut to the host buffer; and the CCMP receive path: packet
 * number extraction, replay, MIC failure, engine timeout, key selection (pairwise by transmitter,
 * group by key id) and its length bounds. The cipher engine itself is hardware: the test sets its
 * completion word and checks the control block the core hands it. */
#include "core_sim.h"

static int soft; /* sta_rxkey: the app deciphers under this key (ADR 0136) */

static const unsigned char TK[16] = {0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
                                     0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f};
static const unsigned char GTK[16] = {0xe0, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7,
                                      0xe8, 0xe9, 0xea, 0xeb, 0xec, 0xed, 0xee, 0xef};
static const unsigned char OTHER[6] = {0x02, 0x99, 0x99, 0x99, 0x99, 0x99};
static unsigned char fbuf[0x1000];

static void setup(int with_keys)
{
    sim_init();
    memset(h_rx, 0x5a, sizeof h_rx);
    for (unsigned i = 0; i < NRX; i++)
        host_supply(i);
    if (with_keys) {
        unsigned char pr[10] = {0}, p[52] = {0};
        memcpy(pr, BSSID, 6);
        host_cmd(0xb5, 0x12, 0x11, pr, sizeof pr);
        p[8] = 4, p[19] = 16;
        memcpy(p + 12, BSSID, 6), memcpy(p + 20, TK, 16);
        host_cmd(13, 0x12, 1, p, 52);
        memset(p, 0, sizeof p);
        p[0] = 4, p[4] = 1, p[7] = 16;
        memcpy(p + 8, GTK, 16);
        host_cmd(11, 0x12, 1, p, 40);
    }
    pump(4);
}

/* One delivered frame, its buffer handed straight back to the core. */
static int delivered(struct rxd *d)
{
    pump(4);
    if (!host_rx(d))
        return 0;
    unsigned i = (unsigned)((d->buf - h_rx[0]) / RXBUF);
    CHECK(d->buf == h_rx[i], "record names a supplied buffer");
    CHECK(d->end + 8 <= RXBUF, "frame end %#x inside the host buffer", d->end);
    for (unsigned k = 0; k < NRX; k++)
        if (k != i)
            CHECK(h_rx[k][0] == 0x5a || h_rx[k][0] == 24, "no write into buffer %u", k);
    return 1;
}

/* An ACK went out since the last look (send_ack's start strobe). */
static int acked(void)
{
    int a = REG(0x670) == 0x800000;
    REG(0x670) = 0;
    return a;
}

static void recycle(const struct rxd *d)
{
    unsigned i = (unsigned)((d->buf - h_rx[0]) / RXBUF);
    memset(h_rx[i], 0x5a, RXBUF);
    host_supply(i);
}

static unsigned mgmt_frame(unsigned fc, const unsigned char *a1, unsigned body)
{
    hdr(fbuf, fc, 0, a1, BSSID, BSSID);
    for (unsigned i = 0; i < body; i++)
        fbuf[24 + i] = i * 7 + 3;
    return 24 + body + 4;
}

/* Data from the AP (FromDS), protected with CCMP header for pn/keyid when prot; body bytes
 * then (if prot) an 8-byte MIC. Returns the length with FCS. */
static unsigned data_frame(const unsigned char *a1, const unsigned char *a2, int prot, unsigned keyid,
                           unsigned long long pn, unsigned body)
{
    unsigned n = hdr(fbuf, 0x08, 0x02 | (prot ? 0x40 : 0), a1, a2, BSSID);
    if (prot) {
        fbuf[n++] = pn, fbuf[n++] = pn >> 8, fbuf[n++] = 0, fbuf[n++] = 0x20 | keyid << 6;
        fbuf[n++] = pn >> 16, fbuf[n++] = pn >> 24, fbuf[n++] = pn >> 32, fbuf[n++] = pn >> 40;
    }
    for (unsigned i = 0; i < body; i++)
        fbuf[n++] = i * 13 + 1;
    if (prot)
        for (unsigned i = 0; i < 8; i++)
            fbuf[n++] = 0xc0 + i;
    return n + 4;
}

static void keep_filter(void)
{
    struct rxd d;
    /* management subtypes kept for the station: (re)assoc req/resp, ATIM, disassoc, auth,
     * deauth, action; not probe req/resp or beacons outside a scan, not action-no-ack */
    for (unsigned sub = 0; sub < 16; sub++) {
        unsigned fc = sub << 4, want = (0x3e0fu >> sub) & 1;
        setup(0);
        unsigned len = mgmt_frame(fc, OWN, 6);
        air_rx(fbuf, len, ST_IN | ST_ADDR);
        CHECK((rxq_in == 1) == want, "mgmt subtype %u kept=%u", sub, rxq_in);
        /* a unicast probe response is ACKed even when it is not kept */
        CHECK(acked() == (want || sub == 5), "mgmt subtype %u ACKed", sub);
        if (want && delivered(&d)) {
            const unsigned char *r = d.buf + 0x94;
            CHECK(r[0] == 24 && r[1] == 24 && r[2] == 48 && r[3] == 0xc4 && r[7] == 0, "record");
            CHECK(r[4] == (unsigned char)-60 && r[5] == 0 && r[6] == 0x0c, "level, channel, rate");
            CHECK(!memcmp(d.buf + 0xac, fbuf, 24), "header at +0xac");
            CHECK(!memcmp(d.buf + 0xcc, fbuf + 24, 6), "body at +0xcc");
            CHECK(d.end == 0x94 + 24 + len - 4, "end %#x", d.end);
            CHECK(d.peer == 6, "no peer slot for an unknown transmitter");
        }
    }
    /* the app's access point (ap_mode): probe requests are kept, a wildcard one without the
     * address filter and not ACKed; outside ap_mode they are not */
    setup(0);
    ap_mode = 1;
    unsigned len = mgmt_frame(0x40, BCAST, 6);
    air_rx(fbuf, len, ST_IN);
    CHECK(rxq_in == 1 && !acked(), "ap_mode: wildcard probe request kept, not ACKed");
    len = mgmt_frame(0x40, OWN, 6);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(rxq_in == 2, "ap_mode: directed probe request kept");
    ap_mode = 0;
    setup(0);
    len = mgmt_frame(0x40, BCAST, 6);
    air_rx(fbuf, len, ST_IN);
    CHECK(rxq_in == 0, "station: probe request not kept");

    setup(0);
    len = mgmt_frame(0xb0, OWN, 6);
    air_rx(fbuf, len, ST_IN | ST_ADDR | ST_FCS);
    CHECK(rxq_in == 0 && !acked(), "FCS error: neither kept nor ACKed");
    air_rx(fbuf, len, ST_IN);
    CHECK(rxq_in == 0 && !acked(), "not for us: neither kept nor ACKed");
    air_rx(fbuf, 27, ST_IN | ST_ADDR);
    CHECK(rxq_in == 0 && !acked(), "27 bytes: too short");
    air_put(fbuf, len, -60, 11);
    air_fiq(0xa0, ST_IN | ST_ADDR);
    CHECK(rxq_in == 0, "frame control differs from the latched one: not kept");
    air_rx(fbuf, sizeof rxarea - RX_WRAP + 1, ST_IN | ST_ADDR);
    CHECK(rxq_in == 0, "longer than the area past the wrap: not kept");
    air_rx(fbuf, sizeof rxarea - RX_WRAP, ST_IN | ST_ADDR);
    CHECK(rxq_in == 1, "longest frame kept");

    /* the ACK to our frame in flight */
    setup(0);
    air_ack();
    CHECK(tx_acked == 1 && rxq_in == 0, "ACK to us noticed, not kept");
    tx_acked = 0;
    {
        unsigned char a[10] = {0xd4, 0};
        memcpy(a + 4, OTHER, 6);
        air_put(a, 14, -50, 11);
        air_fiq(0xd4, ST_IN);
    }
    CHECK(tx_acked == 0, "ACK to someone else ignored");

    /* data: only once joined; unicast kept and ACKed, group kept without the address filter and
     * never ACKed; QoS and null data not kept */
    setup(0);
    len = data_frame(OWN, BSSID, 0, 0, 0, 20);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(rxq_in == 0 && !acked(), "data before the join dropped");
    joined = 1;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(rxq_in == 1 && acked(), "unicast data kept and ACKed");
    REG(0x670) = 0;
    len = data_frame(BCAST, BSSID, 0, 0, 0, 20);
    air_rx(fbuf, len, ST_IN);
    CHECK(rxq_in == 2 && !acked(), "broadcast data kept, not ACKed");
    len = data_frame(OWN, BSSID, 0, 0, 0, 0);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(rxq_in == 2, "null data (no body) not kept");
    len = data_frame(OWN, BSSID, 0, 0, 0, 20);
    fbuf[0] = 0x88;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(rxq_in == 2, "QoS data not kept");

    /* a full queue: unicast data is still ACKed, management is not */
    fbuf[0] = 0x08;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(rxq_in - rxq_out == RXQ, "queue full");
    REG(0x670) = 0;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(rxq_in - rxq_out == RXQ && acked(), "unicast data ACKed with the queue full");
    REG(0x670) = 0;
    len = mgmt_frame(0xb0, OWN, 6);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(!acked(), "management not ACKed with the queue full");
    for (unsigned i = 0; i < RXQ; i++) {
        CHECK(delivered(&d), "queued frame %u delivered", i);
        recycle(&d);
    }
    CHECK(!delivered(&d), "nothing more");
}

/* Unicast data of every kind is ACKed once joined, kept or not: null-data and QoS-null (the AP's
 * inactivity probe, disassociation if unanswered), QoS data (otherwise retried without end).
 * Only non-QoS data with a body goes to the host. Group-addressed ones are never ACKed. */
static void ack_unicast_data(void)
{
    struct rxd d;
    unsigned len;
    setup(0);
    joined = 1;

    len = data_frame(OWN, BSSID, 0, 0, 0, 0);
    CHECK(len == 28, "null-data is 24 + FCS (%u)", len);
    fbuf[0] = 0x48;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(acked() && rxq_in == 0, "unicast null-data ACKed, not kept");
    len = data_frame(OWN, BSSID, 0, 0, 0, 2); /* QoS control 0: TID 0, Normal Ack */
    fbuf[0] = 0xc8;
    fbuf[24] = fbuf[25] = 0;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(acked() && rxq_in == 0, "unicast QoS-null ACKed, not kept");
    len = data_frame(OWN, BSSID, 0, 0, 0, 40);
    fbuf[0] = 0x88;
    fbuf[24] = 5, fbuf[25] = 0;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(acked() && rxq_in == 0, "unicast QoS data ACKed, not kept");
    fbuf[24] = 0x25; /* ack policy No Ack */
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(!acked() && rxq_in == 0, "QoS No Ack policy: not ACKed");
    fbuf[24] = 0x65; /* ack policy Block Ack, without an agreement */
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(!acked() && rxq_in == 0, "QoS block-ack policy: not ACKed");
    data_frame(OWN, BSSID, 0, 0, 0, 0); /* cut to 28 bytes below: no room for the QoS control */
    fbuf[0] = 0xc8;
    air_rx(fbuf, 28, ST_IN | ST_ADDR);
    CHECK(!acked() && rxq_in == 0, "QoS-null without its QoS control: not ACKed");
    len = data_frame(OWN, BSSID, 0, 0, 0, 0);
    fbuf[0] = 0x58; /* CF-Ack + null: not answered */
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(!acked() && rxq_in == 0, "CF subtype not ACKed");

    /* group-addressed: no ACK, and the null kinds are not kept */
    for (unsigned k = 0; k < 3; k++) {
        static const unsigned char fcs[3] = {0x48, 0xc8, 0x88};
        len = data_frame(BCAST, BSSID, 0, 0, 0, fcs[k] == 0x48 ? 0 : 20);
        fbuf[0] = fcs[k];
        air_rx(fbuf, len, ST_IN | ST_ADDR);
        CHECK(!acked() && rxq_in == 0, "group fc %#x: neither ACKed nor kept", fcs[k]);
    }

    /* before the join none of them is ACKed */
    joined = 0;
    len = data_frame(OWN, BSSID, 0, 0, 0, 0);
    fbuf[0] = 0x48;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(!acked(), "null-data before the join not ACKed");
    pump(4);
    CHECK(!delivered(&d), "nothing reached the host ring");
}

/* A deauth or disassoc to everybody from the joined BSS reaches the host without an ACK; from
 * another BSS, before the join or in ap_mode it does not. */
static void group_leave(void)
{
    struct rxd d;
    unsigned len;
    for (unsigned k = 0; k < 2; k++) {
        unsigned fc = k ? 0xa0 : 0xc0;
        setup(0);
        len = mgmt_frame(fc, BCAST, 2);
        air_rx(fbuf, len, ST_IN);
        CHECK(rxq_in == 0, "fc %#x before the join: not kept", fc);
        joined = 1;
        memcpy(join_bssid, BSSID, 6);
        air_rx(fbuf, len, ST_IN);
        CHECK(rxq_in == 1 && !acked(), "fc %#x to everybody from our BSS: kept, not ACKed", fc);
        int got = delivered(&d);
        CHECK(got && !memcmp(d.buf + 0xac, fbuf, 24), "fc %#x delivered", fc);
        if (got)
            recycle(&d);
        hdr(fbuf, fc, 0, BCAST, OTHER, OTHER);
        air_rx(fbuf, len, ST_IN);
        CHECK(rxq_in == 1, "fc %#x from another BSS: not kept", fc);
        hdr(fbuf, fc, 0, BCAST, OTHER, BSSID);
        air_rx(fbuf, len, ST_IN);
        CHECK(rxq_in == 1, "fc %#x with another transmitter: not kept", fc);
        hdr(fbuf, fc, 0, BCAST, BSSID, BSSID);
        ap_mode = 1;
        air_rx(fbuf, len, ST_IN);
        CHECK(rxq_in == 1, "fc %#x in ap_mode: not kept", fc);
        ap_mode = 0;
        /* a scan lets group frames through the address filter: still never ACKed */
        air_rx(fbuf, len, ST_IN | ST_ADDR);
        CHECK(rxq_in == 2 && !acked(), "fc %#x through the filter: kept, not ACKed", fc);
    }
    /* other group management frames from our BSS stay out */
    setup(0);
    joined = 1;
    memcpy(join_bssid, BSSID, 6);
    len = mgmt_frame(0xd0, BCAST, 6);
    air_rx(fbuf, len, ST_IN);
    CHECK(rxq_in == 0, "group action frame not kept");
    /* the join records the BSSID */
    {
        unsigned char bc[28] = {0};
        memcpy(bc, OTHER, 6);
        bc[8] = 6, bc[26] = 6;
        scan_cmd(C_JOIN, bc, 28);
        CHECK(!memcmp(join_bssid, OTHER, 6) && joined && !ap_mode, "join keeps the BSSID");
    }
}

static void read_pointer(void)
{
    struct rxd d;
    setup(0);
    unsigned len = mgmt_frame(0xb0, OWN, 50); /* 24 + 50 + 4 = 78 */
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(REG(0x644) == ((4 + 78 + 7) & ~7u), "pointer past the frame, 8-aligned: %#x", REG(0x644));
    CHECK(REG(0x604) & 0x100, "MAC interrupt requested");
    CHECK(delivered(&d), "first frame");
    CHECK(REG(0x678) == ((8 + 78 - 4 - 1) & ~7u), "MAC may write up to the first frame's end");
    recycle(&d);
    REG(0x644) = RX_WRAP - 8;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(REG(0x644) == 0, "pointer wraps to 0 past %#x", RX_WRAP);
    CHECK(delivered(&d) && !memcmp(d.buf + 0xac, fbuf, 24), "frame at the end of the area");
    CHECK(REG(0x678) == RX_WRAP - 8, "limit held below the wrap after the last frame");
    recycle(&d);

    /* the MAC wrote over a queued frame (its length changed): dropped, not delivered */
    setup(0);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    put_le16((unsigned char *)rxarea, 300);
    CHECK(delivered(&d) && d.end == 0x94 + 24 + 74, "the intact second frame only");
    recycle(&d);
    CHECK(!delivered(&d), "overwritten frame dropped");
}

static void record_fields(void)
{
    struct rxd d;
    static const unsigned char rates[12] = {2, 4, 11, 22, 12, 18, 24, 36, 48, 72, 96, 108};
    /* each rate's MAC code round-trips through the record's rate byte */
    for (unsigned lp = 0; lp < 2; lp++)
        for (unsigned i = 0; i < 12; i++) {
            setup(0);
            unsigned len = mgmt_frame(0xb0, OWN, 2);
            air_put(fbuf, len, -71, rate_code(rates[i], lp));
            air_fiq(0xb0, ST_IN | ST_ADDR);
            if (!delivered(&d))
                CHECK(0, "rate %u not delivered", rates[i]);
            else
                CHECK(d.buf[0x94 + 6] == rates[i] && d.buf[0x94 + 4] == (unsigned char)-71,
                      "rate %u (code %u) -> record %u", rates[i], rate_code(rates[i], lp),
                      d.buf[0x94 + 6]);
        }
    /* channel while scanning, peer slot of a known transmitter */
    setup(1);
    rx_chan = 9;
    dwell_state = 4;
    unsigned len = mgmt_frame(0x80, BCAST, 12);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(delivered(&d) && d.buf[0x94 + 5] == 9 && d.peer == 0, "channel 9, AP slot 0");

    /* the largest data frame is cut to the host buffer */
    setup(0);
    joined = 1;
    len = data_frame(OWN, BSSID, 0, 0, 0, sizeof rxarea - RX_WRAP - 28);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(rxq_in == 1, "kept");
    CHECK(delivered(&d) && d.end == RXBUF - 8, "cut to the host buffer (%#x)", d.end);
    CHECK(!memcmp(d.buf + 0xcc, fbuf + 24, RXBUF - 0xcc), "body up to the cut");
}

static int deliver_ccmp(struct rxd *d, unsigned st, unsigned long long pn, unsigned body)
{
    unsigned len = data_frame(OWN, BSSID, 1, 0, pn, body);
    REG(0xb18) = st;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    return delivered(d);
}

static void ccmp(void)
{
    struct rxd d;
    unsigned long long *last;
    setup(1);
    joined = 1;
    sta_rxkey(0, 0, 0, &last, &soft);

    CHECK(deliver_ccmp(&d, 0x200000, 0x0000a1b2c3d4e5f6ull & 0xffffffffffffull, 50), "deciphered");
    unsigned long long pn = 0xa1b2c3d4e5f6ull;
    CHECK(d.buf[0x94 + 7] == 1, "record says deciphered");
    CHECK(d.buf[0xac + 1] == 0x02, "protected bit cleared (FromDS kept)");
    CHECK(!memcmp(d.buf + 0xcc, fbuf + 32, 50) && d.end == 0x94 + 24 + 24 + 50,
          "body without CCMP header or MIC");
    CHECK(*last == pn, "receive PN advanced to %llx", *last);
    CHECK(dma_b[0] == (24u << 24 | 0x10 << 8 | 0x40) && dma_b[1] == 50, "engine: decipher 50 bytes");
    CHECK(dma_b[2] == (unsigned)(uintptr_t)(rx_stage + 56) && dma_b[3] == dma_b[2] &&
              dma_b[4] == (unsigned)(uintptr_t)(rx_stage + 24),
          "engine: in place, header staged before it");
    CHECK(dma_b[5] == (unsigned)pn && dma_b[6] == (unsigned)(pn >> 32), "engine: PN split");
    CHECK(!memcmp(&dma_b[10], TK, 16), "engine: the AP's pairwise key");
    CHECK(!memcmp(rx_stage + 56 + 50, fbuf + 32 + 50, 8), "MIC staged after the data");
    recycle(&d);

    CHECK(!deliver_ccmp(&d, 0x200000, pn, 50), "replayed PN dropped");
    CHECK(!deliver_ccmp(&d, 0x200000, pn - 1, 50), "older PN dropped");
    CHECK(!deliver_ccmp(&d, 0x240000, pn + 1, 50), "MIC failure dropped");
    CHECK(*last == pn, "a failed MIC does not move the PN");
    CHECK(!deliver_ccmp(&d, 0x210000, pn + 1, 50), "engine error bit dropped");
    CHECK(!deliver_ccmp(&d, 0, pn + 1, 50), "engine that never finishes: dropped");
    CHECK(deliver_ccmp(&d, 0x200000, pn + 1, 50), "next PN accepted");
    recycle(&d);
    CHECK(deliver_ccmp(&d, 0x200000, 0xffffffffffffull, 1), "1-byte body at the top PN");
    CHECK(d.end == 0x94 + 24 + 24 + 1, "one byte");
    recycle(&d);
    *last = 0;

    /* no ExtIV, unknown transmitter, length bounds */
    unsigned len = data_frame(OWN, BSSID, 1, 0, 5, 20);
    fbuf[24 + 3] = 0;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(!delivered(&d), "CCMP header without ExtIV dropped");
    len = data_frame(OWN, OTHER, 1, 0, 6, 20);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(!delivered(&d), "no pairwise key for an unknown transmitter");
    CHECK(!deliver_ccmp(&d, 0x200000, 7, 0), "no data at all: dropped");
    unsigned maxn = RXBUF - 0x94 - 24 - 8 + 16; /* n = len - 4 */
    CHECK(deliver_ccmp(&d, 0x200000, 8, maxn - 40), "largest protected frame");
    CHECK(d.end == RXBUF - 8, "fills the host buffer exactly (%#x)", d.end);
    recycle(&d);
    CHECK(!deliver_ccmp(&d, 0x200000, 9, maxn - 39), "one byte more: dropped");

    /* group: broadcast, key id 1 -> group key 1; key id 2 has none */
    len = data_frame(BCAST, BSSID, 1, 1, 3, 30);
    REG(0xb18) = 0x200000;
    air_rx(fbuf, len, ST_IN);
    CHECK(delivered(&d) && !memcmp(&dma_b[10], GTK, 16), "group frame under group key 1");
    recycle(&d);
    sta_rxkey(0, 1, 1, &last, &soft);
    CHECK(*last == 3, "group PN tracked separately");
    len = data_frame(BCAST, BSSID, 1, 2, 4, 30);
    air_rx(fbuf, len, ST_IN);
    CHECK(!delivered(&d), "no group key 2");

    /* no host buffer: the frame waits, its PN unused; then it is deciphered again, once */
    setup(1);
    joined = 1;
    for (unsigned i = 0; i < NRX; i++) {
        CHECK(deliver_ccmp(&d, 0x200000, 10 + i, 10), "fill buffer %u", i);
    }
    sta_rxkey(0, 0, 0, &last, &soft);
    CHECK(!deliver_ccmp(&d, 0x200000, 20, 10), "no buffer: not delivered yet");
    CHECK(*last == 13 && rxq_in - rxq_out == 1, "still queued, PN unused");
    recycle(&d); /* one buffer back: the wait ends */
    CHECK(delivered(&d) && *last == 20, "delivered once the host gives a buffer");
    recycle(&d);
    CHECK(!delivered(&d), "and only once");
}

/* The setup access point (ap_mode): a client's frames carry our address as A1. A toDS frame
 * is kept and ACKed whether or not the address filter flags it, and AP_STATUS's counters see
 * both; null-data and PS-Poll are kept for the power-management state, QoS data goes up as
 * plain data; group data from other networks on the channel and frames for others are not
 * kept. A station keeps its old rule: nothing to it without the filter's flag. */
static void ap_clients(void)
{
    static const unsigned char PHONE[6] = {0x02, 0x99, 0x88, 0x77, 0x66, 0x55};
    struct rxd d;
    unsigned n;
    setup(0);
    joined = ap_mode = 1;
    n = hdr(fbuf, 0x08, 0x01, OWN, PHONE, BCAST); /* a DHCP discover: toDS, to everybody */
    for (unsigned i = 0; i < 40; i++)
        fbuf[n++] = i + 1;
    air_rx(fbuf, n + 4, ST_IN);
    CHECK(rxq_in == 1 && acked(), "ap_mode: toDS data without the filter flag kept and ACKed");
    CHECK(rec[RC_APACK] == 1 && rec[RC_APACKLAST] == (resp_class[11] | 11u << 8 | 0x08u << 16),
          "the ACK counted with its rate class, the received rate index and frame control");
    CHECK(rec[RC_APIN] == 1 && rec[RC_APNOFILT] == 1 && rec[RC_APKEEP] == 1, "ap counters");
    CHECK(rec[RC_APDATA] == 1 && rec[RC_APDRATE] == (11u | 0xc4u << 8 | 0x08u << 16) && !rec[RC_APNOROOM],
          "a client's data frame counted with its rate index, level and frame control");
    CHECK(delivered(&d), "delivered");
    CHECK(!memcmp(d.buf + 0xac, fbuf, 24) && !memcmp(d.buf + 0xcc, fbuf + 24, 40), "header and body");
    CHECK(d.end == 0x94 + 24 + 24 + 40, "end %#x", d.end);
    recycle(&d);
    air_rx(fbuf, n + 4, ST_IN | ST_ADDR);
    CHECK(rxq_in == 2 && acked() && rec[RC_APIN] == 2 && rec[RC_APNOFILT] == 1, "with the flag too");
    CHECK(delivered(&d), "delivered");
    recycle(&d);

    /* null-data with the power-management bit: kept (no body) and ACKed */
    n = hdr(fbuf, 0x48, 0x11, OWN, PHONE, OWN);
    air_rx(fbuf, n + 4, ST_IN);
    CHECK(rxq_in == 3 && acked(), "null-data kept and ACKed");
    CHECK(delivered(&d) && d.buf[0xac] == 0x48 && d.buf[0xad] == 0x11 && d.end == 0x94 + 48, "null-data record");
    recycle(&d);

    /* QoS data: up without its QoS control, as plain data */
    n = hdr(fbuf, 0x88, 0x01, OWN, PHONE, BCAST);
    fbuf[n++] = 0, fbuf[n++] = 0; /* QoS control: TID 0, Normal Ack */
    for (unsigned i = 0; i < 20; i++)
        fbuf[n++] = 0xa0 + i;
    air_rx(fbuf, n + 4, ST_IN);
    CHECK(rxq_in == 4 && acked(), "QoS data kept and ACKed");
    CHECK(delivered(&d) && d.buf[0xac] == 0x08 && !memcmp(d.buf + 0xac + 1, fbuf + 1, 23), "plain data header");
    CHECK(!memcmp(d.buf + 0xcc, fbuf + 26, 20) && d.end == 0x94 + 48 + 20, "body after the QoS control");
    recycle(&d);

    /* PS-Poll: 16 bytes (AID, BSSID, TA), kept and ACKed, padded to the header */
    memset(fbuf, 0xee, 32);
    fbuf[0] = 0xa4, fbuf[1] = 0x10, fbuf[2] = 1, fbuf[3] = 0xc0;
    memcpy(fbuf + 4, OWN, 6), memcpy(fbuf + 10, PHONE, 6);
    air_rx(fbuf, 20, ST_IN);
    CHECK(rxq_in == 5 && acked(), "PS-Poll kept and ACKed");
    CHECK(delivered(&d) && !memcmp(d.buf + 0xac, fbuf, 16) && d.end == 0x94 + 48, "PS-Poll record");
    for (unsigned i = 16; i < 32; i++)
        CHECK(d.buf + 0xac + i < d.buf + 0xcc ? d.buf[0xac + i] == 0 : 1, "padding byte %u", i);
    recycle(&d);

    /* a frame to us with a bad FCS: neither kept nor ACKed, counted with its rate and level */
    n = hdr(fbuf, 0x08, 0x01, OWN, PHONE, BCAST);
    air_put(fbuf, n + 20 + 4, -30, 12);
    air_fiq(0x08, ST_IN | ST_FCS);
    CHECK(rxq_in == 5 && !acked() && rec[RC_APFCS] == 1 && rec[RC_APFRATE] == (12u | 0xe2u << 8 | 0x08u << 16),
          "damaged frame to us counted, not kept");
    air_put(fbuf, n + 20 + 4, -30, 12);
    air_fiq(0x08, ST_IN | ST_FCS);
    CHECK(rec[RC_APFCS] == 2, "each one");

    /* not ours: another network's broadcast, a frame for someone else */
    n = hdr(fbuf, 0x08, 0x02, BCAST, OTHER, OTHER);
    for (unsigned i = 0; i < 20; i++)
        fbuf[n++] = i;
    air_rx(fbuf, n + 4, ST_IN);
    CHECK(rxq_in == 5 && !acked(), "ap_mode: other networks' group data not kept");
    n = hdr(fbuf, 0x08, 0x01, OTHER, PHONE, BCAST);
    air_rx(fbuf, n + 20 + 4, ST_IN);
    CHECK(rxq_in == 5 && !acked() && rec[RC_APIN] == 5, "ap_mode: data for another BSS not kept");

    /* the app stops its access point (0xb7): ap_mode and the BSS end */
    host_cmd(0xb7, 0x12, 0x11, 0, 0);
    pump(4);
    CHECK(!ap_mode && !joined, "0xb7 ends ap_mode");
    joined = 1;
    /* a station: toDS-looking data to us without the flag stays out, as before */
    ap_mode = 0;
    n = hdr(fbuf, 0x08, 0x01, OWN, PHONE, BCAST);
    air_rx(fbuf, n + 20 + 4, ST_IN);
    CHECK(rxq_in == 5 && !acked(), "station: nothing without the filter flag");
    joined = ap_mode = 1;
    /* the frames to us by kind, and those the address filter missed: the data frames (two
     * DHCP, one QoS), the null-data, the PS-Poll (other); the damaged ones are not counted */
    CHECK(rec[RC_APTYPE + AP_DATA] == 3 && rec[RC_APTYPE + AP_NULL] == 1 && rec[RC_APTYPE + AP_OTHER] == 1 &&
              !rec[RC_APTYPE + AP_RTS] && !rec[RC_APTYPE + AP_ACK] && !rec[RC_APTYPE + AP_PROBE],
          "frames to us by kind");
    CHECK(rec[RC_APNFTYPE + AP_DATA] == 2 && rec[RC_APNFTYPE + AP_NULL] == 1 && rec[RC_APNFTYPE + AP_OTHER] == 1,
          "the filter's misses by kind");
    /* an RTS and an ACK to us, a QoS-null, a directed probe request; the RTS passes the filter */
    memset(fbuf, 0, 32);
    fbuf[0] = 0xb4, memcpy(fbuf + 4, OWN, 6), memcpy(fbuf + 10, PHONE, 6);
    air_rx(fbuf, 20, ST_IN | ST_ADDR);
    fbuf[0] = 0xd4;
    air_rx(fbuf, 14, ST_IN);
    n = hdr(fbuf, 0xc8, 0x11, OWN, PHONE, OWN);
    fbuf[n++] = 0, fbuf[n++] = 0;
    air_rx(fbuf, n + 4, ST_IN);
    n = hdr(fbuf, 0x40, 0, OWN, PHONE, OWN);
    air_rx(fbuf, n + 4, ST_IN);
    CHECK(rec[RC_APTYPE + AP_RTS] == 1 && rec[RC_APTYPE + AP_ACK] == 1 && rec[RC_APTYPE + AP_NULL] == 2 &&
              rec[RC_APTYPE + AP_PROBE] == 1 && rec[RC_APIN] == 9,
          "RTS, ACK, QoS-null and probe request counted by kind");
    CHECK(!rec[RC_APNFTYPE + AP_RTS] && rec[RC_APNFTYPE + AP_ACK] == 1 && rec[RC_APNFTYPE + AP_NULL] == 2 &&
              rec[RC_APNFTYPE + AP_PROBE] == 1 && rec[RC_APNOFILT] == 7,
          "and the filter's misses");
    ap_mode = 0, joined = 0;
}

/* Frames under the app's keys (ADR 0136) go up as they came: TKIP unicast under the soft pairwise
 * key, TKIP group under a soft group key, WEP (no ExtIV) under a default key in the group table,
 * cipher header and trailer kept, protected bit set, record byte 7 = RX_SOFT. A frame for a key id
 * nobody installed is still dropped, and the largest TKIP frame is not cut. */
static void soft_frames(void)
{
    struct rxd d;
    unsigned char pr[10] = {0}, p[52] = {0};
    setup(0);
    joined = 1;
    memcpy(pr, BSSID, 6);
    host_cmd(0xb5, 0x12, 0x11, pr, sizeof pr);
    p[8] = 2, p[19] = 16;
    memcpy(p + 12, BSSID, 6);
    host_cmd(13, 0x12, 1, p, 52);              /* TKIP pairwise */
    memset(p, 0, sizeof p);
    p[0] = 2, p[4] = 1, p[7] = 16;
    host_cmd(11, 0x12, 1, p, 40);              /* TKIP group key 1 */
    memset(p, 0, sizeof p);
    p[0] = 5, p[4] = 3, p[7] = 16;
    host_cmd(11, 0x12, 1, p, 40);              /* WEP-104 default key 3 */
    pump(4);
    REG(0xb18) = 0;                            /* the engine must not run */

    unsigned len = data_frame(OWN, BSSID, 1, 0, 0x10203, 40);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(delivered(&d) && d.buf[0x94 + 7] == 2, "TKIP unicast up as RX_SOFT");
    CHECK(d.buf[0xac + 1] == 0x42, "protected bit kept");
    CHECK(!memcmp(d.buf + 0xcc, fbuf + 24, 8 + 40 + 8) && d.end == 0x94 + 24 + 24 + 8 + 40 + 8,
          "body as on air: header, ciphertext, trailer");
    recycle(&d);
    len = data_frame(BCAST, BSSID, 1, 1, 5, 30);
    air_rx(fbuf, len, ST_IN);
    CHECK(delivered(&d) && d.buf[0x94 + 7] == 2, "TKIP group key 1 up as RX_SOFT");
    recycle(&d);
    len = data_frame(BCAST, BSSID, 1, 2, 6, 30);
    air_rx(fbuf, len, ST_IN);
    CHECK(!delivered(&d), "group key id 2: nobody's, dropped");
    /* WEP: IV, key id 3 without ExtIV, unicast and broadcast both under default key 3 */
    len = data_frame(OWN, BSSID, 1, 3, 7, 20);
    fbuf[24 + 3] = 3 << 6;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(delivered(&d) && d.buf[0x94 + 7] == 2 && d.buf[0xcc + 3] == 0xc0, "WEP unicast up as RX_SOFT");
    recycle(&d);
    len = data_frame(BCAST, BSSID, 1, 3, 8, 20);
    fbuf[24 + 3] = 3 << 6;
    air_rx(fbuf, len, ST_IN);
    CHECK(delivered(&d) && d.buf[0x94 + 7] == 2, "WEP broadcast up as RX_SOFT");
    recycle(&d);
    len = data_frame(OWN, BSSID, 1, 0, 9, 20);
    fbuf[24 + 3] = 0;
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(!delivered(&d), "WEP key id 0: no default key, dropped");
    /* the largest TKIP frame: 8 + 1500 + 8 SNAP and IP, MIC 8 + ICV 4 (data_frame's trailer is 8) */
    len = data_frame(OWN, BSSID, 1, 0, 10, 1508 + 4);
    air_rx(fbuf, len, ST_IN | ST_ADDR);
    CHECK(delivered(&d) && d.end == 0x94 + 24 + 24 + 1528, "a 1528-byte TKIP body fits (%#x)", d.end);
    recycle(&d);
}

int main(void)
{
    soft_frames();
    keep_filter();
    ap_clients();
    ack_unicast_data();
    group_leave();
    read_pointer();
    record_fields();
    ccmp();
    CHECK(irq_depth == 0, "critical sections balanced");
    return done("rx");
}
