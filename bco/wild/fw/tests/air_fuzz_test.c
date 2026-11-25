/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Fuzz the code that reads frames from the air (run-all.sh builds it under ASan and UBSan): the
 * MAC FIQ's keep/ACK filter and receive-area pointer (src/mac.c), the hand-over queue with frames
 * the MAC overwrote, and rx_deliver's header, CCMP header, packet number and length handling
 * (src/xport.c). Frames start from beacons, probe responses, management frames, ACKs and plain or
 * protected data, then get bytes flipped, lengths changed (up to beyond the receive area) and the
 * latched frame control and status bits varied, across scan states, before and after the join,
 * with and without host buffers and with every cipher-engine outcome. The seed is fixed, so a
 * failure repeats. Checked besides memory safety: the queue never holds more than it can, the
 * read pointer stays aligned and below the wrap, a delivered record stays inside its host buffer
 * and is well formed, a deciphered frame comes up with the protected bit clear, and no receive
 * packet number ever moves backwards. */
#include "core_sim.h"

static const unsigned char TK[16] = {0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
                                     0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f};
#define ITER 40000u

static unsigned char fbuf[0x4000]; /* at least the receive area: air_put copies what fits there */
static unsigned held[NRX], nheld; /* buffers the host keeps for a while */
static unsigned long long last_pn[5];
static unsigned delivered, deciphered, kept_max;

static void install_keys(void)
{
    unsigned char pr[10] = {0}, p[52] = {0};
    memcpy(pr, BSSID, 6);
    host_cmd(0xb5, 0x12, 0x11, pr, sizeof pr);
    p[8] = 4, p[19] = 16;
    memcpy(p + 12, BSSID, 6), memcpy(p + 20, TK, 16);
    host_cmd(13, 0x12, 1, p, 52);
    for (unsigned k = 0; k < 4; k += 2) {
        memset(p, 0, sizeof p);
        p[0] = 4, p[4] = k, p[7] = 16;
        memcpy(p + 8, TK, 16);
        host_cmd(11, 0x12, 1, p, 40);
    }
    pump(4);
}

/* A starting frame of each kind; returns its length with FCS. */
static unsigned template(unsigned kind)
{
    unsigned n = 0, filler = rnd() % 300;
    const unsigned char *a1 = rnd() & 1 ? OWN : BCAST;
    switch (kind) {
    case 0: /* beacon / probe response: fixed fields then SSID, rates, DS, RSN IEs */
    case 1:
        n = hdr(fbuf, kind ? 0x50 : 0x80, 0, kind ? OWN : BCAST, BSSID, BSSID);
        memset(fbuf + n, 0, 12), n += 12;
        fbuf[n++] = 0, fbuf[n++] = 4, memcpy(fbuf + n, "Home", 4), n += 4;
        fbuf[n++] = 1, fbuf[n++] = 8;
        for (unsigned i = 0; i < 8; i++)
            fbuf[n++] = 0x82 + i;
        fbuf[n++] = 3, fbuf[n++] = 1, fbuf[n++] = 6;
        fbuf[n++] = 48, fbuf[n++] = 20;
        for (unsigned i = 0; i < 20; i++)
            fbuf[n++] = rnd();
        return n + 4;
    case 2: /* management for the station */
        n = hdr(fbuf, (rnd() % 16) << 4, 0, a1, BSSID, BSSID);
        break;
    case 3: /* ACK */
        fbuf[0] = 0xd4, fbuf[1] = 0, fbuf[2] = fbuf[3] = 0;
        memcpy(fbuf + 4, rnd() & 1 ? OWN : BSSID, 6);
        return 14;
    case 4: /* plain data */
        n = hdr(fbuf, 0x08, 0x02, a1, BSSID, BSSID);
        break;
    default: { /* protected data, PN near the key's last one */
        unsigned keyid = rnd() % 4;
        unsigned long long base = a1 == BCAST ? last_pn[1 + keyid] : last_pn[0];
        unsigned long long pn = base + rnd() % 8 - (base >= 2 ? 2 : 0); /* replays included */
        n = hdr(fbuf, 0x08, 0x42, a1, rnd() % 8 ? BSSID : OWN, BSSID);
        fbuf[n++] = pn, fbuf[n++] = pn >> 8, fbuf[n++] = 0, fbuf[n++] = 0x20 | keyid << 6;
        fbuf[n++] = pn >> 16, fbuf[n++] = pn >> 24, fbuf[n++] = pn >> 32, fbuf[n++] = pn >> 40;
        filler += 8;
        break;
    }
    }
    for (unsigned i = 0; i < filler; i++)
        fbuf[n++] = rnd();
    return n + 4;
}

static void check_record(const struct rxd *d)
{
    const unsigned char *r = d->buf + 0x94;
    CHECK(d->end + 8 <= RXBUF && d->end >= 0x94 + 24 + 24, "record end %#x", d->end);
    CHECK(r[0] == 24 && r[1] == 24 && r[2] == 48 && r[3] == 0xc4 && r[7] <= 1, "record header");
    if (r[7]) {
        deciphered++;
        CHECK((d->buf[0xac] & 0x0c) == 0x08 && !(d->buf[0xad] & 0x40),
              "deciphered data comes up in clear");
    }
    CHECK(d->peer <= 6, "peer slot %u", d->peer);
    delivered++;
}

static void host_side(void)
{
    struct rxd d;
    while (host_rx(&d)) {
        check_record(&d);
        unsigned i = (unsigned)((d.buf - h_rx[0]) / RXBUF);
        if (rnd() % 4 == 0 && nheld < NRX)
            held[nheld++] = i; /* keep it: the core runs short of buffers */
        else
            host_supply(i);
    }
    if (nheld && rnd() % 3 == 0)
        host_supply(held[--nheld]);
}

static void check_pn(void)
{
    unsigned long long *p;
    if (sta_rxkey(0, 0, 0, &p)) {
        CHECK(*p >= last_pn[0], "pairwise PN went back %llx -> %llx", last_pn[0], *p);
        last_pn[0] = *p;
    }
    for (unsigned k = 0; k < 4; k++)
        if (sta_rxkey(0, 1, k, &p)) {
            CHECK(*p >= last_pn[1 + k], "group %u PN went back", k);
            last_pn[1 + k] = *p;
        }
}

int main(void)
{
    static const unsigned engine_end[] = {0x200000, 0x200000, 0x200000, 0x240000, 0x210000, 0x220000};
    sim_init();
    for (unsigned i = 0; i < NRX; i++)
        host_supply(i);
    install_keys();
    pump(2);
    for (unsigned it = 0; it < ITER && !fails; it++) {
        if (rnd() % 64 == 0) {
            static const unsigned states[] = {0, 0, 4, 6, 5, 7, 8};
            dwell_state = states[rnd() % 7];
            joined = rnd() % 4 != 0;
            rx_chan = rnd() % 15;
            REG(0xb18) = rnd() % 400 ? engine_end[rnd() % 6] : 0; /* rarely: the engine never ends */
        }
        unsigned len = template(rnd() % 7);
        for (unsigned m = rnd() % 6; m--;)
            fbuf[rnd() % (len + 8 < sizeof fbuf ? len + 8 : sizeof fbuf)] ^= 1u << (rnd() % 8);
        switch (rnd() % 10) {
        case 0:
            len = rnd() % 0x1000; /* anything the length field can say */
            break;
        case 1:
            len = rnd() % 48;
            break;
        case 2:
            len += rnd() % 64 - 32;
            break;
        }
        len &= 0xffff;
        unsigned st = ST_IN | (rnd() % 8 ? ST_ADDR : 0) | (rnd() % 16 ? 0 : ST_FCS) |
                      (rnd() % 32 ? 0 : 0x800);
        unsigned fc = rnd() % 16 ? fbuf[0] : rnd();
        air_put(fbuf, len, (signed char)rnd(), rnd() % 16);
        REG(0x6b4) = rnd() & 4;
        air_fiq(fc, st);
        if (dwell_fired)
            scan_poll();
        if (rnd() % 50 == 0 && rxq_in != rxq_out) /* the MAC wrote over a queued frame */
            put_le16((unsigned char *)rxq[(rxq_in - 1) % RXQ], rnd() % 0x1000);

        CHECK(rxq_in - rxq_out <= RXQ, "queue holds %u", rxq_in - rxq_out);
        CHECK(REG(0x644) < RX_WRAP && !(REG(0x644) & 7), "read pointer %#x", REG(0x644));
        if (rxq_in - rxq_out > kept_max)
            kept_max = rxq_in - rxq_out;
        if (rnd() % 3 == 0) {
            pump(2);
            host_side();
            check_pn();
        }
    }
    pump(8);
    host_side();
    printf("air fuzz: %u frames, %u delivered, %u deciphered, queue peak %u\n", ITER, delivered,
           deciphered, kept_max);
    CHECK(delivered > ITER / 20 && deciphered > 100, "the fuzz reaches delivery and decipher");
    CHECK(irq_depth == 0, "critical sections balanced");
    return done("air_fuzz");
}
