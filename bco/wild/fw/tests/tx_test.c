/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The transmit path, from a host record to the MAC and back (src/xport.c): the staged packet and
 * queue descriptor, the rate class per frame type, the AP's ACK ending a unicast frame, retries
 * with the Retry bit and a growing backoff window, the last tries at the basic rate, the data-rate
 * ladder and the receiver's fastest rate capping it, group data at the basic rate and the gap after
 * it (the frame's airtime, DIFS and backoff on top of the margin), the airtime itself, a probe
 * response's single retry, a lost TX-done (a beacon's counted), the record-length guard, and CCMP transmit:
 * the CCMP header and packet number, the cipher engine's control block, the same PN on a retry,
 * and a frame without a key for its peer (also one queued when the peer table is reset) or that
 * the engine cannot encipher, which never goes out; our sequence numbers and Duration fields, and
 * a protected frame's retries changing only what CCMP leaves out of its AAD. The cipher engine
 * itself is hardware: the test sets its completion word. */
#include "core_sim.h"

static const unsigned char TK[16] = {0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
                                     0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f};
static unsigned char body[0x700];

static void setup(int key)
{
    sim_init();
    host_rate0 = 0;
    REG(R_TIMER) = 100000;
    for (unsigned i = 0; i < sizeof body; i++)
        body[i] = i * 5 + 1;
    if (key) {
        unsigned char pr[10] = {0}, p[52] = {0};
        memcpy(pr, BSSID, 6);
        host_cmd(0xb5, 0x12, 0x11, pr, sizeof pr);
        p[8] = 4, p[19] = 16;
        memcpy(p + 12, BSSID, 6), memcpy(p + 20, TK, 16);
        host_cmd(13, 0x12, 1, p, 52);
        pump(4);
    }
}

/* wstation_data's header: data, toDS (+ Protected), RA = BSSID, TA = own, DA. */
static void data_hdr(unsigned char *h, const unsigned char *da, int prot)
{
    hdr(h, 0x08, 0x01 | (prot ? 0x40 : 0), da[0] & 1 ? da : BSSID, OWN, da);
}

/* Queue one frame on frm[2] (the ring wifi.c uses) and run until it is on the MAC. */
static int send(const unsigned char *h, unsigned len, unsigned peer)
{
    host_tx(2, 0, h, body, len, peer, 0x11);
    pump(3);
    return mgmt_tx_pending != 0;
}

static unsigned slots_of_start(void)
{
    return REG(0x674) & 0x3ff;
}

static void unicast_ok(void)
{
    unsigned char h[24];
    unsigned buf, status;
    setup(0);
    data_hdr(h, BSSID, 0);
    REG(R_TIMER) = 0x1234;
    CHECK(send(h, 100, 0), "frame handed to the MAC");
    CHECK(qstate[9] == (unsigned)(uintptr_t)&tx_desc.len && mgmt_tx_pending == 0x12,
          "on queue 9 for ring 2");
    CHECK((REG(0x674) & 0x1820000) == 0x1820000 && slots_of_start() <= 31, "start after a backoff");
    CHECK(tx_desc.hdr == tx_pkt + 0x18 && tx_pkt[0x18] == h[0] && tx_pkt[0x19] == h[1] &&
              !memcmp(tx_pkt + 0x18 + 4, h + 4, 18),
          "header staged (the core sets duration and sequence)");
    CHECK(*(unsigned short *)(tx_pkt + 0x18 + 0x16) == 0, "the first frame's sequence number is 0");
    CHECK(tx_desc.p1 == tx_pkt + 0xc4 && tx_desc.l1 == 100 && !memcmp(tx_pkt + 0xc4, body, 100),
          "body staged");
    CHECK(tx_desc.len == 24 + 100 + 4 && tx_desc.hdrlen == 24 && tx_desc.l2 == 0,
          "descriptor: header + body + FCS");
    CHECK(tx_desc.cls == 12 && tx_desc.rate == 8, "data starts at 54 Mb/s");
    CHECK(!memcmp(tx_pkt, h_tx[0] + 0x94, 0x18), "descriptor copy at the packet head");
    air_tx_done();
    pump(1);
    CHECK(!host_tx_done(2, &buf, &status), "TX-done alone does not end a unicast frame");
    air_ack();
    pump(3);
    CHECK(host_tx_done(2, &buf, &status) && status == 0 && buf == (unsigned)(uintptr_t)h_tx[0],
          "ACKed: record back, delivered, buffer address in word 1");
    CHECK(!tx_busy && !mgmt_tx_pending, "transmitter free");

    /* a management frame goes at the management class */
    hdr(h, 0xb0, 0, BSSID, OWN, BSSID);
    CHECK(send(h, 6, 6), "auth frame sent");
    CHECK(tx_desc.cls == sta_mgmt_cls, "management frame at the basic rate class");
}

static void retries(void)
{
    unsigned char h[24];
    unsigned buf, status, attempts = 0, basic = 0;
    setup(0);
    data_hdr(h, BSSID, 0);
    CHECK(send(h, 64, 0), "sent");
    for (unsigned round = 0; round < 10 && !host_tx_done(2, &buf, &status); round++) {
        if (mgmt_tx_pending) {
            attempts++;
            unsigned n = attempts - 1, cw = n > 5 ? 1023 : (32u << n) - 1;
            CHECK(slots_of_start() <= cw, "attempt %u: backoff %u within CW %u", attempts,
                  slots_of_start(), cw);
            CHECK(attempts == 1 || (tx_pkt[0x19] & 0x08), "attempt %u carries Retry", attempts);
            basic += tx_desc.cls == sta_mgmt_cls;
            air_tx_done();
        }
        REG(R_TIMER) += ACK_WAIT + 20 * 1024;
        pump(1);
    }
    CHECK(attempts == 1 + TX_RETRY, "%u attempts", attempts);
    CHECK(basic == 2, "the last two at the basic rate (%u)", basic);
    CHECK(status == 1, "failed after the retries");
    CHECK(rate_i == 0 && rate_bad == 1, "one bad frame does not step down yet");
    CHECK(send(h, 64, 0), "next frame");
    air_tx_done();
    REG(R_TIMER) += ACK_WAIT + 20 * 1024;
    pump(1);
    air_tx_done();
    air_ack();
    pump(3);
    CHECK(host_tx_done(2, &buf, &status) && status == 0, "delivered on its retry");
    CHECK(rate_i == 1, "two retried frames in a row step down to 36 Mb/s");
    CHECK(send(h, 64, 0) && tx_desc.cls == 13, "next data frame at 36 Mb/s");
    air_tx_done();
    air_ack();
    pump(3);
    host_tx_done(2, &buf, &status);
    for (unsigned i = 0; i < 9; i++) {
        send(h, 64, 0);
        air_tx_done();
        air_ack();
        pump(3);
        host_tx_done(2, &buf, &status);
    }
    CHECK(rate_i == 0, "ten clean frames step back up to 54 Mb/s");
}

static void ladder_floor(void)
{
    unsigned char h[24];
    unsigned buf, status;
    setup(0);
    data_hdr(h, BSSID, 0);
    for (unsigned i = 0; i < 8; i++) {
        send(h, 64, 0);
        air_tx_done();
        REG(R_TIMER) += ACK_WAIT + 20 * 1024;
        pump(1);
        air_tx_done();
        air_ack();
        pump(3);
        host_tx_done(2, &buf, &status);
    }
    CHECK(rate_i == sizeof rate_cls - 1, "floor at 24 Mb/s");
}

/* The gap after a group frame of len bytes (FCS included) at 1 Mb/s that started after slots
 * backoff slots: the margin, DIFS, the slots (20 us each) and its airtime (192 us preamble, then
 * 8 us a byte). */
static unsigned expect_gap(unsigned len, unsigned slots)
{
    return TX_GAP + 50 + 20 * slots + 192 + 8 * len;
}

static void group_and_gap(void)
{
    unsigned char h[24];
    unsigned buf, status, gap;
    setup(0);
    data_hdr(h, BCAST, 0);
    CHECK(send(h, 40, 0), "broadcast sent");
    CHECK(tx_unicast == 0, "no ACK expected");
    CHECK(tx_desc.cls == sta_mgmt_cls, "group data at the basic rate, where every client hears it");
    gap = expect_gap(24 + 40 + 4, slots_of_start());
    CHECK(tx_gap == gap, "gap %u, want %u", tx_gap, gap);
    air_tx_done();
    pump(3);
    CHECK(host_tx_done(2, &buf, &status) && status == 0, "group frame done at its TX-done");
    CHECK(!send(h, 40, 0), "next frame waits for the gap");
    REG(R_TIMER) += gap - 1;
    pump(3);
    CHECK(!mgmt_tx_pending, "not before the gap");
    REG(R_TIMER) += 1;
    pump(3);
    CHECK(mgmt_tx_pending, "sent after %u ticks", gap);

    /* a lost TX-done: a group frame comes back failed after TX_LOST */
    REG(R_TIMER) += TX_LOST - 1;
    pump(1);
    CHECK(!host_tx_done(2, &buf, &status), "still waiting");
    REG(R_TIMER) += 2;
    pump(3);
    CHECK(host_tx_done(2, &buf, &status) && status == 1, "given back as failed");
    CHECK(rec[RC_APBLOST] == 0, "group data is no beacon");

    /* a lost TX-done on a unicast frame that was ACKed: done 3000 ticks after the start */
    REG(R_TIMER) += tx_gap;
    data_hdr(h, BSSID, 0);
    CHECK(send(h, 40, 0), "unicast sent");
    CHECK(tx_gap == 0, "a unicast frame holds nothing back: its ACK ended it");
    air_ack();
    REG(R_TIMER) += 3001;
    pump(3);
    CHECK(host_tx_done(2, &buf, &status) && status == 0, "ACKed frame done without TX-done");
}

/* A beacon of the setup AP (ap_mode): 1 Mb/s, ~1.6 ms on air for a 200-byte body. The next frame
 * waits until it can have left the air, also behind the longest first backoff; the 3 ms that was
 * the whole gap let the next frame overwrite it. A beacon whose TX-done never came is counted. */
static void beacon_gap(void)
{
    unsigned char h[24];
    unsigned buf, status, gap;
    setup(0);
    ap_mode = 1;
    hdr(h, 0x80, 0, BCAST, OWN, OWN);
    CHECK(send(h, 200, 6), "beacon sent");
    CHECK(tx_unicast == 0 && tx_desc.cls == sta_mgmt_cls, "a group frame at the basic rate");
    gap = expect_gap(24 + 200 + 4, slots_of_start());
    CHECK(tx_gap == gap && gap > TX_GAP + 192 + 8 * 228, "gap %u covers its airtime", tx_gap);
    air_tx_done();
    pump(3);
    CHECK(host_tx_done(2, &buf, &status) && status == 0, "done at its TX-done");
    hdr(h, 0x08, 0x02, BCAST, OWN, OWN);
    REG(R_TIMER) += TX_GAP + 1;
    CHECK(!send(h, 40, 6), "the next frame does not stage over the beacon after 3 ms");
    REG(R_TIMER) += gap - TX_GAP - 1;
    pump(3);
    CHECK(mgmt_tx_pending, "but once the beacon can have left the air");
    air_tx_done();
    pump(3);
    host_tx_done(2, &buf, &status);

    /* a beacon without a TX-done */
    REG(R_TIMER) += tx_gap;
    hdr(h, 0x80, 0, BCAST, OWN, OWN);
    CHECK(send(h, 200, 6), "second beacon sent");
    REG(R_TIMER) += TX_LOST + 1;
    pump(3);
    CHECK(host_tx_done(2, &buf, &status) && status == 1 && rec[RC_APBLOST] == 1,
          "a beacon given back after TX_LOST is counted");
    ap_mode = 0;
}

/* The airtime the gap uses never comes out shorter than the exact one, nor much longer. */
static void airtime_bounds(void)
{
    static const unsigned mbps10[16] = {10, 20, 55, 110, 10, 20, 55, 110, 480, 240, 120, 60, 540, 360, 180, 90};
    for (unsigned cls = 0; cls < 16; cls++)
        for (unsigned len = 14; len <= 1600; len += 7) {
            unsigned exact, got = airtime(cls, len);
            if (cls >= 8) {
                unsigned bps = mbps10[cls] * 4 / 10; /* data bits per 4 us symbol */
                exact = 20 + 4 * ((16 + 8 * len + 6 + bps - 1) / bps) + 6;
            } else {
                exact = (cls == 0 || cls == 4 || cls < 4 ? 192 : 96) + (80 * len + mbps10[cls] - 1) / mbps10[cls];
            }
            CHECK(got >= exact && got <= exact + exact / 12 + 8, "class %u, %u bytes: %u us, exact %u",
                  cls, len, got, exact);
        }
    CHECK(airtime(4, 100) == 992, "100 bytes at 1 Mb/s: 992 us");
    CHECK(airtime(11, 100) == 166, "100 bytes at 6 Mb/s: 35 symbols, 166 us");
}

/* In ap_mode unicast data starts at the ladder's rate, or the next one down its receiver supports
 * (the fastest rate of the frame's chain): a setup AP client that hostapd offers 24 Mb/s gets 24,
 * one slower than the ladder the basic rate. */
static void rate_cap(void)
{
    static const struct { unsigned char max, cls; const char *what; } c[] = {
        {0, 12, "no chain: 54 Mb/s"},
        {108, 12, "54 Mb/s receiver: 54"},
        {96, 13, "48 Mb/s receiver: 36"},
        {72, 13, "36 Mb/s receiver: 36"},
        {48, 9, "24 Mb/s receiver (the setup AP's cap): 24"},
        {36, 4, "18 Mb/s receiver: the basic rate"},
        {22, 4, "11b receiver: the basic rate"},
    };
    unsigned char h[24];
    unsigned buf, status;
    for (unsigned i = 0; i < sizeof c / sizeof c[0]; i++) {
        setup(0);
        ap_mode = 1;
        host_rate0 = c[i].max | 0x80; /* the basic bit is ignored */
        data_hdr(h, BSSID, 0);
        CHECK(send(h, 64, 0) && tx_desc.cls == c[i].cls, "%s (class %u)", c[i].what, tx_desc.cls);
    }
    /* the station keeps the ladder: its EAPOL frames carry the AP's lowest rate in byte 4 */
    setup(0);
    host_rate0 = 2;
    data_hdr(h, BSSID, 0);
    CHECK(send(h, 64, 0) && tx_desc.cls == 12, "station: 54 Mb/s whatever byte 4 says");
    /* after a step down the cap still holds, and the last tries are at the basic rate */
    setup(0);
    ap_mode = 1;
    host_rate0 = 48;
    rate_i = 1;
    data_hdr(h, BSSID, 0);
    CHECK(send(h, 64, 0) && tx_desc.cls == 9, "36 Mb/s step, 24 Mb/s receiver: 24");
    rate_i = 0;
    unsigned attempts = 0, basic = 0;
    for (unsigned round = 0; round < 10 && !host_tx_done(2, &buf, &status); round++) {
        if (mgmt_tx_pending) {
            attempts++;
            basic += tx_desc.cls == sta_mgmt_cls;
            air_tx_done();
        }
        REG(R_TIMER) += ACK_WAIT + 20 * 1024;
        pump(1);
    }
    CHECK(attempts == 1 + TX_RETRY && basic == 2, "%u attempts, the last %u at the basic rate", attempts, basic);
    ap_mode = 0;
}

/* A probe response gets one retry, not six: the phone that asked has moved on by then, and the
 * one transmitter waits on it. An authentication response keeps them all. */
static void probe_response_retries(void)
{
    static const unsigned char PHONE[6] = {0x02, 0x99, 0x88, 0x77, 0x66, 0x55};
    unsigned char h[24];
    unsigned buf, status;
    for (unsigned kind = 0; kind < 2; kind++) {
        unsigned attempts = 0;
        setup(0);
        hdr(h, kind ? 0xb0 : 0x50, 0, PHONE, OWN, OWN);
        CHECK(send(h, 40, 6), "sent");
        CHECK(tx_unicast == 1 && tx_desc.cls == sta_mgmt_cls, "unicast management at the basic rate");
        for (unsigned round = 0; round < 10 && !host_tx_done(2, &buf, &status); round++) {
            if (mgmt_tx_pending) {
                attempts++;
                air_tx_done();
            }
            REG(R_TIMER) += ACK_WAIT + 20 * 1024;
            pump(1);
        }
        CHECK(status == 1, "failed without an ACK");
        CHECK(attempts == (kind ? 1 + TX_RETRY : 2), "%s: %u attempts", kind ? "auth" : "probe response",
              attempts);
    }
}

static void record_guard(void)
{
    unsigned char h[24];
    unsigned buf, status;
    setup(0);
    data_hdr(h, BSSID, 0);
    /* body + 0xc4 below 0xc4 is a wrapped length: refused before any DMA */
    host_tx(2, 0, h, body, (unsigned)-1, 0, 0x11);
    pump(3);
    CHECK(!mgmt_tx_pending && host_tx_done(2, &buf, &status) && status == 1,
          "negative length refused");
    /* a refused record never reached the MAC: no gap behind it */
    host_tx(2, 0, h, body, sizeof tx_pkt - 0xc4 + 1, 0, 0x11);
    pump(3);
    CHECK(!mgmt_tx_pending && host_tx_done(2, &buf, &status) && status == 1, "oversize refused");
    CHECK(send(h, sizeof tx_pkt - 0xc4, 0), "the largest body is sent");
    CHECK(tx_desc.l1 == sizeof tx_pkt - 0xc4, "whole body");
}

static void ccmp(void)
{
    unsigned char h[24];
    unsigned buf, status;
    setup(1);
    data_hdr(h, BSSID, 1);
    REG(0xb18) = 0x20;
    CHECK(send(h, 100, 0), "protected frame sent");
    const unsigned char *cc = tx_pkt + 0xbc;
    CHECK(tx_desc.p1 == cc && cc[0] == 1 && cc[1] == 0 && cc[2] == 0 && cc[3] == 0x20 &&
              !cc[4] && !cc[5] && !cc[6] && !cc[7],
          "CCMP header: PN 1, ExtIV, key id 0");
    CHECK(tx_desc.l1 == 8 + 100 + 8 && tx_desc.len == 24 + 8 + 100 + 8 + 4, "header + PN + body + MIC");
    CHECK(tx_pkt[0x19] & 0x40, "protected bit kept");
    CHECK(dma_a[0] == (24u << 24 | 0x10 << 8 | 0x46) && dma_a[1] == 100, "engine: encipher 100 bytes");
    CHECK(dma_a[2] == (unsigned)(uintptr_t)(tx_pkt + 0xc4) && dma_a[3] == dma_a[2] &&
              dma_a[4] == (unsigned)(uintptr_t)(tx_pkt + 0x18),
          "engine: in place, header before it");
    CHECK(dma_a[5] == 1 && dma_a[6] == 0 && !memcmp(&dma_a[10], TK, 16), "engine: PN 1, pairwise key");
    /* a retry is the same frame: same PN, Retry set */
    air_tx_done();
    REG(R_TIMER) += ACK_WAIT + 20 * 1024;
    pump(1);
    CHECK(mgmt_tx_pending && (tx_pkt[0x19] & 0x08) && cc[0] == 1 && dma_a[5] == 1, "retry keeps PN 1");
    air_tx_done();
    air_ack();
    pump(3);
    host_tx_done(2, &buf, &status);
    CHECK(send(h, 100, 0) && cc[0] == 2 && dma_a[5] == 2, "next frame PN 2");
    air_tx_done();
    air_ack();
    pump(3);
    host_tx_done(2, &buf, &status);

    /* Protected but no key for the slot: never in clear; nothing goes out, the record comes back
     * failed and the transmitter is free for the next frame */
    unsigned before = qstate[9];
    qstate[9] = 0;
    REG(R_TIMER) += TX_GAP;
    hdr(h, 0x08, 0x41, BSSID, OWN, BSSID);
    CHECK(!send(h, 50, 3), "a protected frame for a slot without a key is not sent");
    CHECK(qstate[9] == 0, "nothing on the MAC queue");
    CHECK(host_tx_done(2, &buf, &status) && status == 1, "record back as failed (status %u)", status);
    CHECK(!tx_busy, "transmitter free");
    REG(R_TIMER) += TX_GAP;
    CHECK(!send(h, 50, 6) && host_tx_done(2, &buf, &status) && status == 1, "slot 6 (none): same");
    REG(R_TIMER) += TX_GAP;
    CHECK(!send(h, 50, 0xffff) && host_tx_done(2, &buf, &status) && status == 1,
          "a slot past the table: same");
    REG(R_TIMER) += TX_GAP;
    CHECK(send(h, 50, 0) && (tx_pkt[0x19] & 0x40), "slot 0 with its key still goes out enciphered");
    air_tx_done();
    air_ack();
    pump(3);
    host_tx_done(2, &buf, &status);
    qstate[9] = 0;

    /* the engine fails: nothing goes out, the record comes back failed */
    REG(0xb18) = 0;
    data_hdr(h, BSSID, 1);
    CHECK(!send(h, 100, 0), "no frame after an engine failure");
    CHECK(qstate[9] == 0, "nothing on the MAC queue");
    CHECK(host_tx_done(2, &buf, &status) && status == 1, "record back as failed (status %u)", status);
    CHECK(!tx_busy, "transmitter free");
    /* no room for the MIC: same */
    REG(0xb18) = 0x20;
    REG(R_TIMER) += TX_GAP;
    CHECK(!send(h, sizeof tx_pkt - 0xc4 - 7, 0), "a body without room for the MIC is not sent");
    CHECK(host_tx_done(2, &buf, &status) && status == 1, "record back as failed");
    qstate[9] = before;
    REG(R_TIMER) += TX_GAP;
    CHECK(send(h, sizeof tx_pkt - 0xc4 - 8, 0), "the largest protected body is sent");
}

/* A frame the app enciphered itself (TX_RAW, ADR 0136): it goes out as staged, Protected bit and
 * all, without a key in the core and without the engine; a protected frame without the mark and
 * without a key still never goes out. */
static void raw(void)
{
    unsigned char h[24];
    unsigned buf, status;
    setup(0);
    data_hdr(h, BSSID, 1);
    REG(0xb18) = 0;                         /* the engine would fail: it must not run */
    host_desc13 = 1;
    CHECK(send(h, 120, 0), "an enciphered frame goes out without a key in the core");
    host_desc13 = 0;
    CHECK(tx_desc.p1 == tx_pkt + 0xc4 && tx_desc.l1 == 120 && tx_desc.len == 24 + 120 + 4 &&
              !memcmp(tx_pkt + 0xc4, body, 120),
          "body as staged: no CCMP header or MIC added");
    CHECK(tx_pkt[0x19] & 0x40, "protected bit kept");
    CHECK(tx_desc.cls == 12, "data at the data rate");
    air_tx_done();
    air_ack();
    pump(3);
    CHECK(host_tx_done(2, &buf, &status) && status == 0, "delivered");
    /* a management frame enciphered by the app (a protected deauthentication): the same */
    REG(R_TIMER) += TX_GAP;
    hdr(h, 0xc0, 0x40, BSSID, OWN, BSSID);
    host_desc13 = 1;
    CHECK(send(h, 8 + 2 + 8, 6), "a protected management frame goes out");
    host_desc13 = 0;
    CHECK(tx_desc.l1 == 18 && tx_desc.cls == sta_mgmt_cls, "as staged, at the basic rate class");
    CHECK(*(unsigned short *)(tx_pkt + 0x18 + 0x16) == 1 << 4 &&
              (tx_pkt[0x18 + 2] | tx_pkt[0x18 + 3] << 8) == 10 + airtime(resp_class[sta_mgmt_cls], 14),
          "the next sequence number and the basic rate's ACK Duration (outside the app's CCMP AAD)");
    air_tx_done();
    air_ack();
    pump(3);
    host_tx_done(2, &buf, &status);
    /* without the mark, Protected and no key: dropped */
    REG(R_TIMER) += TX_GAP;
    qstate[9] = 0;
    data_hdr(h, BSSID, 1);
    CHECK(!send(h, 50, 0) && qstate[9] == 0, "unmarked protected frame without a key not sent");
    CHECK(host_tx_done(2, &buf, &status) && status == 1, "record back as failed");
}

/* A protected frame queued behind the one in flight when the peer table is reset (JOIN_DONE,
 * drop-all) or its peer deleted: the peer's key went with it, so the frame is dropped and comes
 * back failed, never sent in clear or under the old key. */
static void peer_reset(void)
{
    static const unsigned char jd[6] = {1, 1, 0, 0, 0, 0}, del[8] = {0};
    static const struct { unsigned type; const void *p; unsigned len; const char *what; } cmd[] = {
        {0xb8, jd, sizeof jd, "JOIN_DONE"}, {0xb7, 0, 0, "drop-all"}, {0xb6, del, sizeof del, "PEER_DEL 0"},
    };
    for (unsigned i = 0; i < sizeof cmd / sizeof cmd[0]; i++) {
        unsigned char h[24];
        unsigned buf, status;
        setup(1);
        REG(0xb18) = 0x20;
        data_hdr(h, BSSID, 1);
        CHECK(send(h, 100, 0), "%s: first frame on the MAC", cmd[i].what);
        host_tx(2, 1, h, body, 100, 0, 0x11); /* queued while the first waits for its ACK */
        pump(3);
        CHECK(tx_busy && ring_avail(&l_frm[2], 1) == 1, "%s: second frame queued", cmd[i].what);
        host_cmd(cmd[i].type, 0x12, 11, cmd[i].p, cmd[i].len);
        pump(3);
        CHECK(peer_slot(BSSID) == 6, "%s: the AP left the peer table", cmd[i].what);
        unsigned long long pn;
        CHECK(!sta_txkey(0, &pn), "%s: and its key with it", cmd[i].what);
        qstate[9] = 0;
        air_tx_done();
        air_ack();
        pump(3);
        CHECK(host_tx_done(2, &buf, &status) && status == 0 && buf == (unsigned)(uintptr_t)h_tx[0],
              "%s: the first frame was delivered", cmd[i].what);
        CHECK(host_tx_done(2, &buf, &status) && status == 1 && buf == (unsigned)(uintptr_t)h_tx[1],
              "%s: the queued frame comes back failed (status %u)", cmd[i].what, status);
        CHECK(qstate[9] == 0 && !mgmt_tx_pending && !tx_busy, "%s: and never reached the MAC",
              cmd[i].what);
    }
}

/* Our own sequence numbers: one 12-bit counter across host frames and probe requests, a retry
 * keeps its frame's, it wraps at 4096. The Duration field: SIFS + the ACK at the response rate
 * of the frame's rate for a unicast frame, again for the last tries at the basic rate; 0 for a
 * group frame. */
static void seq_and_duration(void)
{
    unsigned char h[24];
    unsigned buf, status;
    setup(0);
    data_hdr(h, BSSID, 0);
    for (unsigned i = 0; i < 3; i++) {
        CHECK(send(h, 64, 0), "frame %u sent", i);
        CHECK(*(unsigned short *)(tx_pkt + 0x18 + 0x16) == i << 4, "frame %u: sequence %u", i, i);
        unsigned d = tx_pkt[0x18 + 2] | tx_pkt[0x18 + 3] << 8;
        CHECK(d == 10 + airtime(resp_class[12], 14) && d == 60, "unicast at 54 Mb/s: duration %u, the ACK at 6", d);
        air_tx_done();
        air_ack();
        pump(3);
        host_tx_done(2, &buf, &status);
    }
    /* a retried frame keeps its number; the last tries get the basic rate's ACK duration */
    CHECK(send(h, 64, 0), "fourth frame");
    for (unsigned round = 0; round < 10 && !host_tx_done(2, &buf, &status); round++) {
        if (mgmt_tx_pending) {
            unsigned d = tx_pkt[0x18 + 2] | tx_pkt[0x18 + 3] << 8;
            CHECK(*(unsigned short *)(tx_pkt + 0x18 + 0x16) == 3 << 4, "a retry keeps sequence 3");
            CHECK(d == 10 + airtime(resp_class[tx_desc.cls & 15], 14), "duration %u for class %u", d, tx_desc.cls);
            air_tx_done();
        }
        REG(R_TIMER) += ACK_WAIT + 20 * 1024;
        pump(1);
    }
    CHECK(tx_desc.cls == sta_mgmt_cls && (tx_pkt[0x18 + 2] | tx_pkt[0x18 + 3] << 8) == 10 + airtime(resp_class[4], 14),
          "the basic rate's ACK at the end");
    /* a group frame: duration 0, and the next number */
    data_hdr(h, BCAST, 0);
    CHECK(send(h, 40, 0) && !tx_pkt[0x18 + 2] && !tx_pkt[0x18 + 3] &&
              *(unsigned short *)(tx_pkt + 0x18 + 0x16) == 4 << 4,
          "group frame: duration 0, sequence 4");
    air_tx_done();
    pump(3);
    host_tx_done(2, &buf, &status);
    /* it wraps at 4096 */
    tx_seq = 4095;
    REG(R_TIMER) += tx_gap;
    data_hdr(h, BSSID, 0);
    CHECK(send(h, 64, 0) && *(unsigned short *)(tx_pkt + 0x18 + 0x16) == 4095 << 4, "sequence 4095");
    air_tx_done();
    air_ack();
    pump(3);
    host_tx_done(2, &buf, &status);
    CHECK(send(h, 64, 0) && *(unsigned short *)(tx_pkt + 0x18 + 0x16) == 0, "then 0");
}

/* A protected frame is enciphered once, with its sequence number already in the header the
 * engine reads. Its retries, the last ones at the basic rate included, change only the Retry bit
 * and the Duration: CCMP masks the first out of its AAD and leaves the second out, so the frame
 * the AP deciphers is the one the engine enciphered. Sequence number, PN, CCMP header, ciphertext
 * and MIC stay as they were, and the engine never runs again for a retry. */
static void ccmp_retry_header(void)
{
    unsigned char h[24], hdr0[24], ct0[8 + 100 + 8];
    unsigned buf, status, attempts = 0, basic = 0;
    setup(1);
    tx_seq = 0x123;
    data_hdr(h, BSSID, 1);
    REG(0xb18) = 0x20;
    CHECK(send(h, 100, 0), "protected frame sent");
    CHECK(dma_a[4] == (unsigned)(uintptr_t)(tx_pkt + 0x18) &&
              *(unsigned short *)(tx_pkt + 0x18 + 0x16) == 0x123 << 4,
          "the engine reads a header that already holds sequence 0x123");
    memcpy(hdr0, tx_pkt + 0x18, 24);
    memcpy(ct0, tx_pkt + 0xbc, sizeof ct0);
    dma_a[1] = 0;                                    /* the engine writes its length on each run */
    for (unsigned round = 0; round < 10 && !host_tx_done(2, &buf, &status); round++) {
        if (mgmt_tx_pending) {
            const unsigned char *x = tx_pkt + 0x18;
            attempts++;
            basic += tx_desc.cls == sta_mgmt_cls;
            CHECK(x[0] == hdr0[0] && (x[1] & ~0x08) == hdr0[1] && !memcmp(x + 4, hdr0 + 4, 20),
                  "attempt %u: only Retry and Duration differ", attempts);
            CHECK((x[2] | x[3] << 8) == 10 + airtime(resp_class[tx_desc.cls & 15], 14),
                  "attempt %u: the Duration of its rate", attempts);
            CHECK(!memcmp(tx_pkt + 0xbc, ct0, sizeof ct0), "attempt %u: CCMP header, ciphertext, MIC kept",
                  attempts);
            air_tx_done();
        }
        REG(R_TIMER) += ACK_WAIT + 20 * 1024;
        pump(1);
    }
    CHECK(attempts == 1 + TX_RETRY && basic == 2, "%u attempts, %u at the basic rate", attempts, basic);
    CHECK(dma_a[1] == 0, "no retry ran the engine again");
    CHECK(tx_seq == 0x124, "the frame and its retries took one sequence number");
}

int main(void)
{
    raw();
    unicast_ok();
    retries();
    ladder_floor();
    group_and_gap();
    beacon_gap();
    airtime_bounds();
    rate_cap();
    seq_and_duration();
    probe_response_retries();
    record_guard();
    ccmp();
    ccmp_retry_header();
    peer_reset();
    CHECK(irq_depth == 0, "critical sections balanced");
    return done("tx");
}
