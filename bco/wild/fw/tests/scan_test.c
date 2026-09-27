/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Scan and join (src/scan.c): the rate tables, the ACK-rate classes a join derives from the BSS's
 * rate set, the probe request it builds from the host's payload (read back with an independent
 * IE walker), the passive and active dwell state machines driven by the MAC timer FIQ, the
 * length guards on the host's commands (the join's rate count too), and the probe backoff
 * generator. Payloads follow
 * bco/app/src/wifi.c (build_join_bc, the 0xc2 probe body). */
#include "core_sim.h"

/* wifi.c build_join_bc(): BSSID, capability, channel, 13-byte rate block, band 6 (2.4 GHz). */
static void join_body(unsigned char bc[28], unsigned chan, const unsigned char *rates)
{
    memset(bc, 0, 28);
    memcpy(bc, BSSID, 6);
    bc[6] = 0x64; /* interval low byte as wifi.c sends it (bc[6..7] read as the interval) */
    bc[8] = chan;
    memcpy(bc + 9, rates, 13);
    bc[26] = 6;
}

static const unsigned char WIFI_RATES[13] = {0x0c, 0x82, 0x84, 0x8b, 0x0c, 0x12, 0x96,
                                             0x18, 0x24, 0x30, 0x48, 0x60, 0x6c};

static void rate_tables(void)
{
    static const unsigned char r[12] = {2, 4, 11, 22, 12, 18, 24, 36, 48, 72, 96, 108};
    static const unsigned char lng[12] = {4, 1, 2, 3, 11, 15, 10, 14, 9, 13, 8, 12};
    static const unsigned char shrt[12] = {4, 5, 6, 7, 11, 15, 10, 14, 9, 13, 8, 12};
    for (unsigned i = 0; i < 12; i++) {
        CHECK(rate_code(r[i], 1) == lng[i], "rate %u long preamble -> %u", r[i], rate_code(r[i], 1));
        CHECK(rate_code(r[i], 0) == shrt[i], "rate %u short preamble -> %u", r[i], rate_code(r[i], 0));
        CHECK(rate_code(r[i] | 0x80, 0) == shrt[i], "basic bit ignored for %u", r[i]);
    }
    for (unsigned v = 0; v < 256; v++) {
        unsigned known = 0;
        for (unsigned i = 0; i < 12; i++)
            known |= (v & 0x7f) == r[i];
        if (!known)
            CHECK(rate_code(v, 1) == 5 && rate_code(v, 0) == 5, "unknown rate %u -> 5", v);
    }
    /* the data rates the transmit path steps through are 54, 36 and 24 Mb/s */
    CHECK(rate_cls[0] == rate_code(108, 1) && rate_cls[1] == rate_code(72, 1) &&
              rate_cls[2] == rate_code(48, 1) && sizeof rate_cls == 3,
          "TX data rate ladder 54/36/24");
}

static void resp_class_is(const unsigned short *want, const char *what)
{
    for (unsigned i = 0; i < 16; i++)
        CHECK(resp_class[i] == want[i], "%s: resp_class[%u] = %u, want %u", what, i, resp_class[i],
              want[i]);
}

static void join_cases(void)
{
    unsigned char bc[28];
    /* wifi.c's set: basic 1, 2, 5.5, 11 Mb/s, no basic OFDM rate: OFDM ACKs at the defaults */
    static const unsigned short want_wifi[16] = {4, 1, 2, 3, 4, 5, 6, 7, 9, 9, 10, 11, 9, 9, 10, 15};
    /* basic 1, 6, 12, 24 Mb/s: each OFDM rate answers at the highest basic one at or below it */
    static const unsigned char ofdm_basic[13] = {4, 0x82, 0x8c, 0x98, 0xb0};
    static const unsigned short want_ofdm[16] = {4, 4, 4, 4, 4, 4, 4, 4, 9, 9, 10, 11, 9, 9, 10, 11};

    sim_init();
    nev = 0, tunes = 0;
    join_body(bc, 6, WIFI_RATES);
    scan_cmd(C_JOIN, bc, 27); /* 27 is the shortest join body read */
    CHECK(tunes == 1 && tune_band == 6 && tune_chan == 6, "join tunes band 6 channel 6");
    CHECK(joined == 1 && sta_mgmt_cls == 4, "joined, 2.4 GHz management class");
    CHECK(REG(0x6b0) == 8, "slot time 9 us");
    CHECK(!(REG(0x604) & 1), "receiver on for a beaconing BSS");
    resp_class_is(want_wifi, "wifi.c rate set");
    pump(4);
    host_events();
    CHECK(nev == 1 && ev[0].type == 0xbd && ev[0].module == 8 && ev[0].src == 0x13,
          "join reports 0xbd to module 8");

    join_body(bc, 1, ofdm_basic);
    bc[6] = bc[7] = 0;
    bc[26] = 4;
    scan_cmd(C_JOIN, bc, 28);
    resp_class_is(want_ofdm, "OFDM basic set");
    CHECK(REG(0x604) & 1, "no beacon interval: receiver stays off");
    CHECK(sta_mgmt_cls == 10, "band code 4 uses class 10");

    /* a join while a scan dwells ends the scan first (0xc3), then 0xbd */
    sim_init();
    nev = 0;
    dwell_state = 4;
    join_body(bc, 6, WIFI_RATES);
    scan_cmd(C_JOIN, bc, 28);
    pump(4);
    host_events();
    CHECK(nev == 2 && ev[0].type == 0xc3 && ev[1].type == 0xbd && dwell_state == 0,
          "scan ended before the join reply");

    /* a rate count past the 13-byte block reads only its 12 rates (the body sits in a heap block
     * of exactly 27 bytes, so ASan sees a read beyond it) */
    {
        unsigned char *p = malloc(27);
        join_body(bc, 6, WIFI_RATES);
        memcpy(p, bc, 27);
        p[9] = 255;
        sim_init();
        scan_cmd(C_JOIN, p, 27);
        resp_class_is(want_wifi, "count 255 = the 12 rates");
        p[9] = 13;
        scan_cmd(C_JOIN, p, 27);
        resp_class_is(want_wifi, "count 13 = the 12 rates");
        free(p);
    }

    /* too short: nothing happens */
    sim_init();
    joined = 0, tunes = 0;
    scan_cmd(C_JOIN, bc, 26);
    CHECK(!joined && !tunes, "26-byte join ignored");
}

/* Walk the IEs of a frame body; 1 if they end exactly at len. */
static int ie_walk(const unsigned char *ie, unsigned len, const unsigned char **ssid, unsigned *ssidlen,
                   unsigned char *rates, unsigned *nrates)
{
    unsigned at = 0;
    *ssid = 0, *nrates = 0;
    while (at + 2 <= len) {
        unsigned id = ie[at], l = ie[at + 1];
        if (at + 2 + l > len)
            return 0;
        if (id == 0)
            *ssid = ie + at + 2, *ssidlen = l;
        else if (id == 1 || id == 50)
            memcpy(rates + *nrates, ie + at + 2, l), *nrates += l;
        at += 2 + l;
    }
    return at == len;
}

static void probe_case(unsigned ssidlen, unsigned nrates, unsigned band)
{
    unsigned char p[0x3d], rates[32];
    const unsigned char *ssid;
    unsigned sl, nr, want_ssid = ssidlen > 32 ? 32 : ssidlen, want_rates = nrates > 12 ? 12 : nrates;
    memset(p, 0, sizeof p);
    memcpy(p, BSSID, 6);
    p[6] = ssidlen;
    for (unsigned i = 0; i < 32; i++)
        p[7 + i] = 'a' + i % 26;
    p[0x27] = 11, p[0x28] = nrates;
    for (unsigned i = 0; i < 12; i++)
        p[0x29 + i] = 0x82 + i;
    put_le16(p + 0x36, 110), put_le16(p + 0x38, 30), put_le16(p + 0x3a, 2), p[0x3c] = band;

    sim_init();
    tunes = 0;
    REG(R_TIMER) = 0x3fffff0; /* deadlines wrap at 26 bits */
    scan_cmd(C_SCAN_PROBE, p, sizeof p);
    const unsigned char *f = probe_desc.hdr;
    CHECK(tunes == 1 && tune_band == band && tune_chan == 11 && rx_chan == 11, "probe tunes ch 11");
    CHECK(qstate[9] == (unsigned)(uintptr_t)&probe_desc.len, "probe on the management queue");
    CHECK(f == probe_frame && probe_desc.hdrlen == 24 && f[0] == 0x40, "probe request header");
    CHECK(!memcmp(f + 4, BCAST, 6) && !memcmp(f + 10, OWN, 6) && !memcmp(f + 16, BSSID, 6),
          "probe addresses: broadcast, own, BSSID");
    CHECK(probe_desc.p1 == f + 24 && probe_desc.len == 24 + probe_desc.l1 + 4,
          "descriptor length = header + IEs + FCS");
    CHECK(24 + probe_desc.l1 <= sizeof probe_frame, "probe fits its buffer");
    CHECK(ie_walk(f + 24, probe_desc.l1, &ssid, &sl, rates, &nr), "IEs well formed (%u, %u)",
          ssidlen, nrates);
    CHECK(ssid && sl == want_ssid && !memcmp(ssid, p + 7, sl), "SSID IE (%u -> %u)", ssidlen, sl);
    CHECK(nr == want_rates && !memcmp(rates, p + 0x29, nr), "rates (%u -> %u)", nrates, nr);
    if (want_rates > 8)
        CHECK(f[24 + 2 + sl + 1] == 8, "8 rates, the rest extended");
    CHECK(probe_desc.cls == (band == 4 ? 10 : 4) && probe_desc.rate == 8, "probe rate class");
    CHECK(dwell_state == 7 && (REG(0x6b8) & 4), "probe wait armed");
    CHECK(REG(0x6c4) == ((0x3fffff0u + (2u << 10) + 5000 + 2000) & 0x3ffffff), "probe wait deadline");
    CHECK((probe_backoff & ~0x1fu) == 0x1820000, "backoff within CWmin");
}

static void dwell_fire(void)
{
    REG(0x6b4) = 4;
    REG(0x620) = 0x800;
    mac_fiq();
    REG(0x620) = 0;
    REG(0x6b4) = 0;
    scan_poll();
}

static void active_scan(void)
{
    unsigned char p[0x3d] = {0}, f[64];
    memcpy(p, BSSID, 6);
    p[0x27] = 1, p[0x28] = 1, p[0x29] = 0x82;
    put_le16(p + 0x36, 4), put_le16(p + 0x38, 2), put_le16(p + 0x3a, 1), p[0x3c] = 6;
    sim_init();
    nev = 0;
    dg_rxfiq = 99;
    REG(R_TIMER) = 1000;
    scan_cmd(C_SCAN_PROBE, p, sizeof p);
    CHECK(dg_rxfiq == 0, "channel 1 starts a fresh scan's counters");
    scan_poll();
    CHECK(dwell_state == 7, "nothing moves before the timer fires");
    REG(R_TIMER) = 3000;
    dwell_fire();
    CHECK(dwell_state == 6 && REG(0x6c4) == 3000 + (4u << 10) + 5120, "probe wait -> listen");
    CHECK(REG(0x674) == probe_backoff && REG(0x6bc) == 4 && (REG(0x6b8) & 4),
          "probe retry with backoff, timer re-armed");
    /* a probe response from the BSS is kept, ACKed and extends the listen */
    unsigned before = REG(0x6c4), kept = rxq_in;
    hdr(f, 0x50, 0, OWN, BSSID, BSSID);
    memset(f + 24, 0, 12);
    air_rx(f, 24 + 12 + 4, ST_IN | ST_ADDR);
    CHECK(rxq_in == kept + 1, "probe response kept while listening");
    CHECK(REG(0x698) == (unsigned)(uintptr_t)&ack_desc && !memcmp(ack_desc.p1, BSSID, 6),
          "probe response ACKed to its sender");
    CHECK(dwell_state == 6 && REG(0x6c4) == before + (2u << 10), "listen extended");
    REG(R_TIMER) = 9000;
    dwell_fire();
    CHECK(dwell_state == 8 && REG(0x6c4) == 9000 + 5000, "listen -> tail");
    hdr(f, 0x50, 0, OWN, BSSID, BSSID);
    kept = rxq_in;
    air_rx(f, 24 + 12 + 4, ST_IN | ST_ADDR);
    CHECK(rxq_in == kept, "no probe response kept in the tail");
    dwell_fire();
    CHECK(dwell_state == 0 && rx_chan == 0 && (REG(0x604) & 1), "scan stopped, receiver off");
    pump(4);
    host_events();
    struct event *e = find_event(0xc4);
    CHECK(e && e->module == 2 && e->src == 0x13 && e->len == 24, "active scan ends with 0xc4");
    CHECK(e && e->payload[8] == 1, "its snapshot names channel 1");
}

static void passive_scan(void)
{
    unsigned char p[5] = {6, 0, 3, 0, 6}, f[64];
    sim_init();
    nev = 0, tunes = 0;
    dg_rxkeep = 5;
    REG(R_TIMER) = 100;
    scan_cmd(C_SCAN_CHANNEL, p, 4);
    CHECK(!tunes && dwell_state == 0, "4-byte scan command ignored");
    scan_cmd(C_SCAN_CHANNEL, p, 5);
    CHECK(dg_rxkeep == 5, "channel 6 keeps the counters (not a fresh scan)");
    CHECK(tunes == 1 && tune_chan == 6 && rx_chan == 6 && dwell_state == 4, "listen on channel 6");
    CHECK(REG(0x6c4) == 100 + (3u << 10) + 5000, "dwell deadline");
    unsigned kept = rxq_in;
    hdr(f, 0x80, 0, BCAST, BSSID, BSSID);
    air_rx(f, 24 + 12 + 4, ST_IN | ST_ADDR);
    CHECK(rxq_in == kept + 1, "beacon kept while listening");
    hdr(f, 0x50, 0, OWN, BSSID, BSSID);
    air_rx(f, 24 + 12 + 4, ST_IN | ST_ADDR);
    CHECK(rxq_in == kept + 1, "probe response not kept in a passive dwell");
    dwell_fire();
    CHECK(dwell_state == 5, "listen -> tail");
    dwell_fire();
    pump(4);
    host_events();
    CHECK(find_event(0xc3) && dwell_state == 0 && rx_chan == 0, "passive scan ends with 0xc3");
    CHECK(!find_event(0xc4), "and nothing else");
    scan_cmd(0x99, p, 5);
    CHECK(tunes == 1, "unknown scan command ignored");
}

static void backoff_generator(void)
{
    uint32_t x = 0;
    rand_state = 0;
    for (unsigned i = 0; i < 1000; i++) {
        x = x * 1103515245u + 12345u;
        CHECK(lcg() == x, "lcg step %u", i);
    }
}

int main(void)
{
    rate_tables();
    join_cases();
    probe_case(0, 0, 6);
    probe_case(4, 4, 6);
    probe_case(32, 8, 6);
    probe_case(33, 9, 6);
    probe_case(255, 12, 4);
    probe_case(32, 255, 6);
    active_scan();
    passive_scan();
    backoff_generator();
    CHECK(irq_depth == 0, "critical sections balanced");
    return done("scan");
}
