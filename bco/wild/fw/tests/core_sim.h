/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The WiLD core's MAC, station, scan and transport sources in one test program, with a simulated
 * host and air around them:
 *   - the host side of the four shared rings (cmd, evt, frm, sup) as bco/app/src/wifi.c sets them
 *     up (sizes from rings()), in host memory that is plain globals here;
 *   - the host DMA engine: a copy between the core's local address and the host address it was
 *     given, followed by the completion interrupt;
 *   - the receiver: a frame written into the receive area where the core's read pointer (0x644)
 *     points, then the MAC's frame-in FIQ;
 *   - the RF tune (tune.c) and delay (rf.c) as stubs: they program only the radio.
 * The cipher engine is registers only, so a test sets its completion word (0xb18). */
#ifndef WILD_CORE_SIM_H
#define WILD_CORE_SIM_H
#include "host.h"

#include "../src/ring.c"
/* A test may watch src/sta.c's copies (an interrupt in the middle of a key install): it defines
 * SIM_STA_COPY as the name of its own copy function, declared before this header. */
#ifdef SIM_STA_COPY
#define copy_bytes SIM_STA_COPY
#endif
#include "../src/sta.c"
#ifdef SIM_STA_COPY
#undef copy_bytes
#endif
#include "../src/mac.c"
#include "../src/scan.c"
#include "../src/xport.c"

/* ---- stubs for what rf.c and tune.c provide ---- */
unsigned reg148;
volatile unsigned dg_vco;
static unsigned tunes, tune_band, tune_chan, delayed;
void rf_tune(unsigned bandcode, unsigned chan)
{
    tunes++, tune_band = bandcode, tune_chan = chan;
}
void delay(unsigned ticks) /* counted, not waited: deadlines stay where the test put the timer */
{
    delayed += ticks;
}

/* ---- host rings (wifi.c rings(): cmd 1000 words, evt 1250, frm 4 x 28, sup 8) ---- */
#define H_CMD_N 1000
#define H_EVT_N 1250
#define H_FRM_N 28
#define H_SUP_N 8
#define RXBUF 0x6e0 /* wifi.c's RX buffer */
#define NRX 4
#define TXFBUF 0x6d0
static unsigned h_cmd_buf[H_CMD_N], h_evt_buf[H_EVT_N], h_frm_buf[4][H_FRM_N * 6],
    h_sup_buf[H_SUP_N * 6];
static unsigned char h_rx[NRX][RXBUF] __attribute__((aligned(8)));
static unsigned char h_tx[4][TXFBUF] __attribute__((aligned(8)));
static struct ring H_cmd, H_evt, H_frm[4], H_sup;

static void host_ring(struct ring *r, void *buf, unsigned size, unsigned esize, unsigned nch)
{
    ring_init(r, (unsigned)(uintptr_t)buf, size, esize, nch);
    r->core_base = r->base; /* the core's DMA addresses are our pointers */
}

/* The host DMA engine fails the next dma_fail_n jobs on the local address dma_fail_local (sim_dma). */
static unsigned dma_jobs, dma_fail_local, dma_fail_n;

/* Power-on state of every module (the core's .bss is zeroed at reset). */
static void sim_reset(void)
{
    memset(pkey, 0, sizeof pkey), memset(gkey, 0, sizeof gkey), memset(peer, 0, sizeof peer);
    privacy = 0;
    memset(rxq, 0, sizeof rxq), memset(rxq_len, 0, sizeof rxq_len);
    rxq_in = rxq_out = 0, rx_chan = 0;
    dg_rxfiq = dg_rxkeep = dg_rxfcs = dg_rxdwell = 0;
    tx_done_t = mgmt_tx_pending = tx_acked = tx_done_q = 0;
    dwell_fired = dwell_state = joined = ap_mode = 0, sta_mgmt_cls = 4;
    memset(join_bssid, 0, sizeof join_bssid);
    probe_listen = probe_extend = probe_backoff = rand_state = 0;
    for (unsigned i = 0; i < 16; i++)
        resp_class[i] = i < 8 ? 5 : 11;
    memset(&job, 0, sizeof job);
    dma_busy = 0, dma_redo = 0, dma_fail_n = 0;
    memset(rx_stage, 0, sizeof rx_stage), rx_slot = 0, rx_end = rx_peer = 0;
    memset(tx_pkt, 0, sizeof tx_pkt);
    tx_q = tx_rec = tx_t0 = 0, tx_busy = 0;
    rate_i = rate_ok = rate_bad = 0;
    tx_free_t = tx_unicast = tx_wait = 0, tx_tries = TX_RETRY;
    tx_gap = tx_slots = tx_seq = 0;
    cmd_state = cmd_kind = cmd_len = cmd_skip = 0;
    memset((void *)rec, 0, sizeof rec);
    tunes = tune_band = tune_chan = delayed = 0;
}

static void sim_init(void)
{
    static struct hostblocks hb;
    sim_reset();
    memset((void *)REG_BASE, 0, 0x1000);
    memset((void *)(MBOX_PAGE + 0xddc), 0, 0x224);
    op_init();
    host_ring(&H_cmd, h_cmd_buf, H_CMD_N, 4, 2);
    host_ring(&H_evt, h_evt_buf, H_EVT_N, 4, 2);
    for (unsigned i = 0; i < 4; i++)
        host_ring(&H_frm[i], h_frm_buf[i], H_FRM_N, 0x18, 5);
    host_ring(&H_sup, h_sup_buf, H_SUP_N, 0x18, 4);
    hb.cmd = &H_cmd, hb.evt = &H_evt, hb.frm = H_frm, hb.sup = &H_sup;
    dma_init();
    mac_init();
    xport_init(&hb);
}

/* The host DMA engine: one job, then its completion interrupt. The next dma_fail_n jobs on the
 * local address dma_fail_local copy nothing and end with the error status instead. */
static void sim_dma(void)
{
    while (dma_busy) {
        void *local = (void *)(uintptr_t)REG(R_DMA_LOCAL), *host = (void *)(uintptr_t)REG(R_DMA_HOST);
        unsigned len = REG(R_DMA_LEN), fail = dma_fail_n && REG(R_DMA_LOCAL) == dma_fail_local;
        if (!(REG(R_DMA_CTL) & 2)) {
            CHECK(0, "DMA job without a start");
            return;
        }
        if (fail)
            dma_fail_n--;
        else if (REG(R_DMA_CTL) & 1)
            memcpy(host, local, len);
        else
            memcpy(local, host, len);
        REG(R_DMA_CTL) &= ~2u;
        REG(R_DMA_STATUS) = fail ? 2 : 0;
        REG(R_IRQ_STATUS) = 2;
        xport_irq();
        REG(R_IRQ_STATUS) = 0;
        dma_jobs++;
    }
}

/* Main-loop iterations with the DMA engine serving every job they start. */
static void pump(unsigned polls)
{
    while (polls--) {
        xport_poll();
        sim_dma();
        CHECK(irq_depth == 0, "critical section left open (%d)", irq_depth);
    }
}

/* A kind-1 command record from the host: {1, -, len + 8} {type} {module | src << 16} payload. */
static void host_cmd(unsigned type, unsigned module, unsigned src, const void *payload, unsigned len)
{
    unsigned w[3 + 80] = {0};
    unsigned n = 3 + (len + 3) / 4, slot;
    w[0] = 1 | (len + 8) << 16, w[1] = type, w[2] = module | src << 16;
    if (len)
        memcpy(w + 3, payload, len);
    for (unsigned i = 0; i < n; i++) {
        if (!ring_take(&H_cmd, 0, 1, &slot)) {
            CHECK(0, "host cmd ring full");
            return;
        }
        *(unsigned *)(uintptr_t)slot = w[i];
        ring_commit(&H_cmd, 0, 1);
    }
}

/* Events the core sent the host, oldest first. */
struct event {
    unsigned type, module, src, len;
    unsigned char payload[64];
};
static struct event ev[64];
static unsigned nev;

static void host_events(void)
{
    unsigned slot;
    while (ring_avail(&H_evt, 1) >= 3) {
        unsigned w[3];
        for (unsigned i = 0; i < 3; i++) {
            ring_take(&H_evt, 1, 1, &slot);
            w[i] = *(unsigned *)(uintptr_t)slot;
            ring_commit(&H_evt, 1, 1);
        }
        struct event *e = &ev[nev < 64 ? nev : 63];
        CHECK((w[0] & 0xff) == 1, "event record kind %u", w[0] & 0xff);
        e->len = (w[0] >> 16) - 8, e->type = w[1] & 0xffff;
        e->module = w[2] & 0xffff, e->src = w[2] >> 16;
        for (unsigned i = 0; i < (e->len + 3) / 4; i++) {
            if (!ring_take(&H_evt, 1, 1, &slot)) {
                CHECK(0, "event payload missing");
                return;
            }
            if (i < 16)
                memcpy(e->payload + 4 * i, (void *)(uintptr_t)slot, 4);
            ring_commit(&H_evt, 1, 1);
        }
        nev++;
    }
}

static struct event *find_event(unsigned type)
{
    for (unsigned i = 0; i < nev && i < 64; i++)
        if (ev[i].type == type)
            return &ev[i];
    return 0;
}

/* ---- receive: host buffers on the sup ring, frames from the air ---- */
static unsigned rx_supplied;

static void host_supply(unsigned i)
{
    unsigned slot;
    if (!ring_take(&H_sup, 0, 1, &slot)) {
        CHECK(0, "host sup ring full");
        return;
    }
    unsigned *e = (unsigned *)(uintptr_t)slot;
    memset(e, 0, 0x18);
    e[1] = (unsigned)(uintptr_t)h_rx[i];
    ring_commit(&H_sup, 0, 1);
    rx_supplied++;
}

/* A delivered frame: the host buffer it went into, its record and its length. */
struct rxd {
    unsigned char *buf;
    unsigned end, peer;
};

/* Take one delivered buffer back from the core, or 0. */
static int host_rx(struct rxd *d)
{
    unsigned slot;
    if (!ring_avail(&H_sup, 3))
        return 0;
    ring_take(&H_sup, 3, 1, &slot);
    unsigned *e = (unsigned *)(uintptr_t)slot;
    d->buf = (unsigned char *)(uintptr_t)e[1];
    d->end = ((unsigned short *)e)[5];
    d->peer = ((unsigned short *)e)[8];
    ring_commit(&H_sup, 3, 1);
    return 1;
}

/* The receive area entry the MAC writes: {u16 length incl. FCS, -, -, s8 rssi, -, u16 rate index}
 * then the frame. Writes what fits in the area (as a MAC with a bounded buffer would); the length
 * field says len whatever it is. Then the frame-in FIQ with event status st. */
#define ST_IN 4u
#define ST_ADDR 0x40000u
#define ST_FCS 0x400000u
static void air_put(const unsigned char *f, unsigned len, signed char rssi, unsigned rate)
{
    unsigned ptr = REG(0x644) & 0xffff;
    unsigned char *e = (unsigned char *)rxarea + ptr;
    unsigned room = sizeof rxarea - ptr - 8, n = len > 4 ? len - 4 : 0;
    put_le16(e, len);
    e[2] = e[3] = 0, e[4] = (unsigned char)rssi, e[5] = 0;
    put_le16(e + 6, rate);
    memcpy(e + 8, f, n < room ? n : room);
}

static void air_fiq(unsigned fc, unsigned st)
{
    REG(0x630) = fc;
    REG(0x620) = st;
    mac_fiq();
    REG(0x620) = 0;
}

static void air_rx(const unsigned char *f, unsigned len, unsigned st)
{
    air_put(f, len, -60, 11);
    air_fiq(f[0], st);
}

/* An 802.11 header: frame control, duration 0, three addresses, sequence 0. */
static unsigned hdr(unsigned char *f, unsigned fc, unsigned flags, const unsigned char *a1,
                    const unsigned char *a2, const unsigned char *a3)
{
    memset(f, 0, 24);
    f[0] = fc, f[1] = flags;
    memcpy(f + 4, a1, 6), memcpy(f + 10, a2, 6), memcpy(f + 16, a3, 6);
    return 24;
}

static const unsigned char BSSID[6] = {0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x01};
static const unsigned char BCAST[6] = {0xff, 0xff, 0xff, 0xff, 0xff, 0xff};

/* ---- transmit: a host frame on frm[q] ---- */
/* Record {buf, -, 1 | (body + 0xc4) << 16, -, peer | dest << 16, -}; the buffer holds the
 * descriptor copy at +0x94 with the on-air header at +0xac and the body at +0xcc (wstation.h). */
static unsigned char host_desc13; /* descriptor byte 0x13 of the next host_tx (1: TX_RAW) */
static unsigned char host_rate0;  /* descriptor byte 4: the fastest rate of its chain, 0 = none */
static void host_tx(unsigned q, unsigned b, const unsigned char *h24, const void *body,
                    unsigned body_len, unsigned peer, unsigned dest)
{
    unsigned slot;
    unsigned char *buf = h_tx[b];
    memset(buf, 0, TXFBUF);
    memcpy(buf + 0xac, h24, 24);
    buf[0x94 + 0x13] = host_desc13;
    buf[0x94 + 4] = host_rate0;
    if (body_len <= TXFBUF - 0xcc)
        memcpy(buf + 0xcc, body, body_len);
    if (!ring_take(&H_frm[q], 0, 1, &slot)) {
        CHECK(0, "host frm ring full");
        return;
    }
    unsigned *e = (unsigned *)(uintptr_t)slot;
    e[0] = (unsigned)(uintptr_t)buf, e[1] = 0, e[2] = 1u | (body_len + 0xc4) << 16;
    e[3] = 0xdead, e[4] = peer | dest << 16, e[5] = 0;
    ring_commit(&H_frm[q], 0, 1);
}

/* A completed TX record back on the host: its buffer and status, or 0. */
static int host_tx_done(unsigned q, unsigned *buf, unsigned *status)
{
    unsigned slot;
    if (!ring_avail(&H_frm[q], 3))
        return 0;
    ring_take(&H_frm[q], 3, 1, &slot);
    const unsigned *e = (unsigned *)(uintptr_t)slot;
    *buf = e[1], *status = e[3];
    ring_commit(&H_frm[q], 3, 1);
    unsigned last = H_frm[q].nch - 1u; /* the host's last channel hands the slot back to 0 */
    ring_take(&H_frm[q], last, 1, &slot);
    ring_commit(&H_frm[q], last, 1);
    return 1;
}

/* The MAC finished sending what queue 9 holds: the TX-done FIQ. */
static void air_tx_done(void)
{
    REG(0x620) = 0x200 | 9u << 24;
    mac_fiq();
    REG(0x620) = 0;
}

/* The AP's ACK to us (14 bytes with FCS). */
static void air_ack(void)
{
    unsigned char f[10] = {0xd4, 0};
    memcpy(f + 4, OWN, 6);
    air_put(f, 14, -50, 11);
    air_fiq(0xd4, ST_IN | ST_ADDR);
}

#endif
