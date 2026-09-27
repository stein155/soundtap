/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Host transport: the rings the host shares with us, the host-memory DMA engine that moves ring
 * contents between host memory and core SRAM, the command consumer and the event producer.
 * Ring roles (host side: bco/app/src/wifi.c rings()):
 *   cmd  host produces c0, we take c1        (words)
 *   evt  we produce c0, host takes c1         (words)
 *   frm  4 access classes, 5 channels         (0x18-byte descriptors)
 *   sup  RX buffer supply, 4 channels          (0x18-byte descriptors)
 * Each host ring has a local twin in SRAM; one DMA job moves a run of elements between them. */
#include "hw.h"

#define MB16(o) (*(volatile unsigned short *)(0x1fde4u + (o)))
/* While the host holds this at 1, nothing travels to it (core-to-host jobs and events wait). */
#define HOST_HOLD (*(volatile unsigned short *)0x1fddeu)
#define HOST_FLAG2 (*(volatile unsigned short *)0x1fddcu)

#define R_IRQ_STATUS 0x200
#define R_IRQ_SET2   0x210
#define R_DMA_CTL    0x504 /* bit 0: core to host; bit 1: go */
#define R_DMA_LOCAL  0x508
#define R_DMA_HOST   0x50c
#define R_DMA_LEN    0x510
#define R_DMA_STATUS 0x514 /* bit 1: error */
#define R_DMA_ACK    0x518 /* 1: done, 2: error */

static struct ring *h_cmd, *h_evt, *h_frm, *h_sup; /* host-written shared headers */
static struct ring l_cmd, l_evt, l_frm[4], l_sup;  /* our twins */
static unsigned cmd_buf[100], evt_buf[750], frm_buf[4][20 * 6], sup_buf[8 * 6];

struct job {
    unsigned local, host, len, to_host;
    void (*done)(void);
    struct ring *hr, *lr; /* on completion: commit n on hr channel hc and lr channel lc */
    unsigned hc, lc, n;
    int (*next)(void);    /* instead of a full schedule after this job */
};
static struct job job;
volatile unsigned rec[RC_N] __attribute__((aligned(4)));
static volatile int dma_busy;

void xport_init(const struct hostblocks *hb)
{
    h_cmd = hb->cmd, h_evt = hb->evt, h_frm = hb->frm, h_sup = hb->sup;
    ring_init(&l_cmd, (unsigned)cmd_buf, 100, 4, 2);
    ring_init(&l_evt, (unsigned)evt_buf, 750, 4, 2);
    for (unsigned i = 0; i < 4; i++)
        ring_init(&l_frm[i], (unsigned)frm_buf[i], 20, 0x18, 5);
    ring_init(&l_sup, (unsigned)sup_buf, 8, 0x18, 4);
    HOST_FLAG2 = (unsigned)rec >> 2; /* diag: the app finds the flight recorder here */
    HOST_HOLD = 0;
}

/* Data waiting for the host: mailbox message 8, no need to wait for the previous one. */
static void tell_host(void)
{
    MB16(0x10c) = 8;
    MB16(0x10e) = 1;
    REG(R_HOST_IRQ) = 0x8000;
}

/* One job between host ring h (channel hc) and our ring l (channel lc), as many elements as
 * both have contiguously; 0 when either has none. */
static int xfer(struct ring *h, unsigned hc, struct ring *l, unsigned lc, int to_host)
{
    unsigned n = ring_space(h, hc), m = ring_space(l, lc), slot;
    if (!n || !m)
        return 0;
    if (m < n)
        n = m;
    ring_take(l, lc, n, &job.local);
    ring_take(h, hc, n, &slot);
    job.host = slot - h->base + h->core_base;
    job.len = n * l->esize;
    job.to_host = to_host;
    job.done = to_host ? tell_host : 0;
    job.hr = h, job.hc = hc, job.lr = l, job.lc = lc, job.n = n;
    job.next = 0;
    return 1;
}

/* A received frame to the host (stock 0x3dac record, engine copy, host DMA): it goes into the
 * next supplied buffer at +0x94 as a 24-byte record {24, header length, header + 24, 0xc4,
 * rssi, channel, rate code}, the header, 8 bytes of cipher-header room, then the body (host
 * wifi.c: header at +0xac, body at +0xcc); the buffer's sup descriptor returns with {buffer,
 * -, (end - 8) << 16, -, peer slot}. A protected data frame is deciphered on the cipher engine
 * (ADR 0031) with the transmitter's pairwise key or the group key its key id names, its PN must
 * be above the key's last one, and it goes up as plaintext: protected bit clear, no CCMP header
 * or MIC, record byte 7 = RX_DECIPHERED. A frame that fails any of that is dropped here. The
 * engine works on word-aligned buffers, so header and body are staged before it runs (receive-area
 * frames are not aligned). */
#define RX_DECIPHERED 1
static unsigned char rx_stage[24 + 0x6b8] __attribute__((aligned(8)));
static unsigned *rx_slot, rx_end, rx_peer;


static void rx_done(void)
{
    unsigned w;
    unsigned short *h = (unsigned short *)rx_slot;
    rx_slot[0] = rx_slot[1];
    h[5] = rx_end;
    h[8] = rx_peer;   /* transmitter's peer slot (stock 0x2ff8 lookup of addr2); 6 = none */
    ((unsigned char *)rx_slot)[0x14] = 0;
    ring_commit(&l_sup, 1, 1);
    ring_take(&l_sup, 2, 1, &w);    /* channel 2 is a pass-through on our side */
    ring_commit(&l_sup, 2, 1);
}

static int rx_deliver(void)
{
    /* stock rate index -> rate code (0x120f4), a hardware fact */
    static const unsigned char rate[16] = {0x00, 0x04, 0x0b, 0x16, 0x02, 0x04, 0x0b, 0x16,
                                           0x60, 0x30, 0x18, 0x0c, 0x6c, 0x48, 0x24, 0x12};
    const unsigned char *e = rx_peek(), *h;
    unsigned n, slot, dec = 0;
    unsigned long long pn = 0, *last = 0;
    if (!e)
        return 0;
    n = *(const unsigned short *)e - 4;
    h = e + 8;
    rx_peer = peer_slot(h + 10);        /* addr2 = the frame's transmitter */
    if ((h[0] & 0x0c) == 0x08 && (h[1] & 0x40)) {
        const unsigned char *cc = h + 24, *tk = 0;
        if (n >= 24 + 8 + 8 + 1 && n - 16 <= 0x6c0 - 0x94 - 24 - 8 && (cc[3] & 0x20)) {
            tk = sta_rxkey(rx_peer, h[4] & 1, cc[3] >> 6, &last);
            pn = cc[0] | cc[1] << 8 |
                 (unsigned long long)(cc[4] | cc[5] << 8 | cc[6] << 16 | (unsigned)cc[7] << 24) << 16;
        }
        if (tk && pn > *last) {
            copy_bytes(rx_stage + 24, h, 24);
            copy_bytes(rx_stage + 56, cc + 8, n - 32);
        }
        if (!tk || pn <= *last || !mac_ccmp_rx(rx_stage + 24, rx_stage + 56, rx_stage + 56, n - 40, tk, pn)) {
            rx_pop();
            return 0;
        }
        n -= 16, dec = 1;
    }
    if (!ring_take(&l_sup, 1, 1, &slot))
        return 0;                       /* the frame stays queued and is deciphered again */
    if (dec)
        *last = pn;
    if (n > 0x6c0 - 0x94 - 24 - 8)       /* host RX buffer (wifi.c RXBUF) */
        n = 0x6c0 - 0x94 - 24 - 8;
    for (unsigned i = 0; i < 24; i++)
        rx_stage[i] = 0;
    rx_stage[0] = 24;
    rx_stage[1] = 24;               /* management header */
    rx_stage[2] = 24 + 24;
    rx_stage[3] = 0xc4;
    rx_stage[4] = e[4];
    rx_stage[5] = rx_chan;
    rx_stage[6] = rate[e[6] & 15];
    rx_stage[7] = dec ? RX_DECIPHERED : 0;
    copy_bytes(rx_stage + 24, h, 24);
    if (dec)
        rx_stage[24 + 1] &= ~0x40u; /* plaintext now */
    for (unsigned i = 0; i < 8; i++)
        rx_stage[48 + i] = 0;       /* cipher-header room (IV); empty */
    if (!dec)
        copy_bytes(rx_stage + 56, h + 24, n - 24);
    rx_pop();
    rx_slot = (unsigned *)slot;
    rx_end = 0x94 + 24 + n;
    job.local = (unsigned)rx_stage;
    job.host = rx_slot[1] + 0x94;
    job.len = 24 + n + 8;
    job.to_host = 1, job.done = rx_done, job.next = 0, job.n = 0;
    return 1;
}

/* ---- host-frame transmit (frm rings, access-class queues 0..7) ----
 * The host puts a frame in one of its buffers (bco/app/src/wstation.h layout: a software
 * descriptor at buf+8, the body at buf+0xcc, a 0x38-byte copy of the descriptor at buf+0x94) and
 * a record on frm[q] channel 0: {buf, -, 1 | (body + 0xc4) << 16, -, peer | dest << 16, -}.
 * We stage buf+0x94 into a local packet (descriptor copy then body), build the on-air header from
 * the descriptor's frame-control/address fields and hand the queue descriptor to the MAC.  One
 * frame in flight across all queues (the MAC sends one at a time). A protected frame is enciphered
 * on the cipher engine with the destination's pairwise CCMP key (ADR 0031) before it goes out
 * (tx_stage_done).
 * Medium access, retries and the data rate: ADR 0031 (tx_kick, ACK_LATE, rate_cls).
 * single fragment; fragmentation, RTS/CTS and group-key TX are later milestones. */
volatile unsigned tx_done_q;
static unsigned char tx_pkt[0xc4 + 0x600];  /* [0..0x38] descriptor, on-air header at +0x18, body at +0xc4 */
static unsigned tx_q, tx_rec, tx_t0;
#define TX_LOST 200000 /* R_TIMER ticks (~1 us): a frame and its retries are done well within this */
static volatile int tx_busy;
/* The TX-done FIQ fires ~2 us after the start, when the MAC takes the descriptor, not when the
 * frame is on air (the AP's ACK comes 2-13 ms later). A frame staged in between overwrote the one
 * still waiting: ~40% of TCP segments never reached the AP. So a unicast host frame is done when
 * the AP's ACK to us arrives (mac.c sets tx_acked). Without it after ACK_WAIT the frame goes out
 * again, as the same frame with the Retry bit (same sequence number and CCMP PN; CCMP masks the
 * bit out of its AAD), up to TX_RETRY times; then it fails and TCP resends it. A 30 min soak on
 * the ST20 needed 242 retries and lost no frame (2026-10-20). Measured under load: 98 % of
 * ACKs arrive within 0.5 ms of the TX-done and every TX-done within 5 ms of the start; retrying
 * sooner after the TX-done than ACK_WAIT from the start tripled the retries.
 * A group frame gets no ACK: the next one waits TX_GAP (3 ms measured best). */
#define ACK_WAIT 5000 /* R_TIMER ticks from the start of an attempt; an ACK takes ~0.3 ms, 30 ms
                       * here cut a download on the ST20 to a third */
#define TX_RETRY 4
#define ACK_LATE 1000 /* R_TIMER ticks after the TX-done: an ACK at the data rate comes ~0.55 ms after
                       * it, later than 1 ms in 4 of 3000 frames; with the backoff a retry
                       * this early cost no extra retries and cut their wait from 5 ms to 2 ms */
/* Data rate: PHY rate code (4 = 1 Mb/s DSSS; OFDM 11/15/10/14/9/13/8/12 = 6..54 Mb/s). A frame
 * starts at rate_cls[rate_i]; two frames in a row that needed a retry step down, ten that went
 * through at once step up. The loss of an upload on the ST20 is the same ~13 % at 24, 36 and
 * 54 Mb/s (collisions, not signal), and 54 Mb/s took 4 MB up in 4-5 s against 6-7 s.
 * 24 Mb/s is the floor: below it the frame outlasts ACK_LATE; then the basic rate takes over. */
static const unsigned char rate_cls[] = {12, 13, 9};
static unsigned rate_i, rate_ok, rate_bad;
#define TX_GAP 3000
volatile unsigned tx_acked;
static unsigned tx_free_t, tx_unicast, tx_wait;   /* tx_unicast: attempts of the frame in flight, 0 = group */
static struct {
    unsigned z0;
    unsigned short len, z1, rate, cls;
    unsigned hdrlen;
    unsigned char *hdr;
    unsigned l1;
    unsigned char *p1;
    unsigned l2, p2;
    unsigned char z2[0x24];
} tx_desc;

/* Start an attempt of the staged frame on queue 9 after a random backoff (802.11 DCF): slots in
 * [0, CW], CW = CWmin on the first attempt and doubled per retry up to 1023. Without it every
 * frame went out at once, into a busy medium: 15-50 % of the frames of an upload needed a retry
 *. The backoff needs the slot time join sets in 0x6b0. The ACK wait counts from the
 * start, so it grows by the backoff (9 us slots, plus margin for a busy medium). */
static void tx_kick(void)
{
    unsigned n = tx_unicast > 1 ? tx_unicast - 1 : 0;
    unsigned cw = n > 5 ? 1023 : ((OP_CWMIN + 1u) << n) - 1;
    unsigned slots = lcg() >> 16 & (cw > 1023 ? 1023 : cw);
    tx_wait = ACK_WAIT + 20 * slots;
    tx_t0 = REG(R_TIMER);
    mgmt_tx_pending = 0x10 | tx_q;
    mac_send_mgmt(&tx_desc.len, 0x20000 | slots);
}

/* Staging DMA finished: assemble the packet and start the MAC (stock a14c + 0x113f0 + 0x1197c). */
static void post_event(unsigned type, unsigned module, unsigned src, const void *payload,
                       unsigned len);
static void tx_complete(unsigned status);

static void tx_stage_done(void)
{
    unsigned body = (((unsigned *)tx_rec)[2] >> 16) - 0xc4;
    unsigned char *h = tx_pkt + 0x18;
    copy_bytes(tx_pkt, tx_pkt + 0x8c, 0x38);           /* descriptor to the packet head */
    ring_commit(&l_frm[tx_q], 1, 1);                   /* record: channel 1 -> 2 */
    *(unsigned short *)(h + 0x16) = REG(R_TIMER) << 4; /* sequence control */
    tx_desc.rate = 8;                                   /* management/EAPOL basic rate (ps0+0x24) */
    tx_desc.cls = sta_mgmt_cls;                         /* PHY rate-class: must match the (working) probe path, not 0 */
    if ((h[0] & 0x0c) == 0x08)
        tx_desc.cls = rate_cls[rate_i];                 /* data at the current rate, the first retries too */
    tx_desc.hdrlen = 24;
    tx_desc.hdr = h;
    tx_desc.l2 = 0;
    /* Protected data frame: encipher as CCMP with the pairwise key of the destination peer (the
     * record's peer slot) on the cipher engine. The 8-byte CCMP header goes just before the body,
     * the engine turns the body into ciphertext + an 8-byte MIC in place. Group/broadcast TX
     * (group keys) is a later milestone. */
    if (h[1] & 0x40) {
        unsigned long long pn = 0;
        const unsigned char *tk = sta_txkey(((unsigned *)tx_rec)[4] & 0xffff, &pn);
        unsigned char *cc = tx_pkt + 0xbc;             /* 8-byte CCMP header before the body */
        /* No key for the slot, no room for the MIC or no engine result: never send plaintext;
         * drop the frame and return its record as failed at once, as frm_in_data does with a
         * record it refuses, so the TX ring keeps moving. (Left to xport_poll, it went back as
         * delivered.) A frame marked Protected without a key once went out in clear with the bit
         * cleared, so a key the core never installed (a lost PEER_ADD) sent data unencrypted. */
        if (tk) {
            cc[0] = pn, cc[1] = pn >> 8, cc[2] = 0, cc[3] = 0x20;   /* ExtIV, key id 0 */
            cc[4] = pn >> 16, cc[5] = pn >> 24, cc[6] = pn >> 32, cc[7] = pn >> 40;
        }
        if (!tk || body > sizeof tx_pkt - 0xc4 - 8 || !mac_ccmp_tx(h, tx_pkt + 0xc4, body, tk, pn)) {
            ring_commit(&l_frm[tx_q], 2, 1);
            tx_unicast = 0;
            tx_done_q = 0x10 | tx_q;
            tx_complete(1);
            return;
        }
        tx_desc.l1 = 8 + body + 8;
        tx_desc.p1 = cc;
        tx_desc.len = 24 + 8 + body + 8 + 4;
        goto start;
    }
    tx_desc.l1 = body;
    tx_desc.p1 = tx_pkt + 0xc4;
    tx_desc.len = 24 + body + 4;                        /* header + body + FCS */
start:
    ring_commit(&l_frm[tx_q], 2, 1);                   /* record: channel 2 -> 3 */
    /* EXPERIMENT: the AC/EDCA queues (0x650 + 4q) never raise a TX-done FIQ on hardware (no medium
     * scheduler), but the mgmt queue (9, 0x674) does. Route the host frame over queue 9 and map its
     * completion back to this frame's ring. Queue 9 is also the scan-probe queue, safe
     * only because scan is idle once joined; the proper fix is the EDCA medium scheduler. */
    tx_unicast = !(h[4] & 1);
    tx_acked = 0;
    tx_kick();
}

/* Stage one queued host frame into tx_pkt (host buf+0x94 -> tx_pkt+0x8c), then tx_stage_done. */
static int frm_in_data(void)
{
    unsigned q;
    if (tx_busy || ((REG(R_TIMER) - tx_free_t) & 0x3ffffff) < TX_GAP)
        return 0;
    for (q = 0; q < 4; q++)
        if (ring_avail(&l_frm[q], 1)) {
            unsigned *e;
            ring_take(&l_frm[q], 1, 1, &tx_rec);
            e = (unsigned *)tx_rec;
            tx_q = q;
            tx_busy = 1;
            /* A record whose length does not fit tx_pkt is refused, never staged: a negative
             * (e[2] >> 16) - 0x8c is a DMA of ~4 GB over the whole core. It goes back as failed. */
            if ((e[2] >> 16) < 0xc4 || (e[2] >> 16) > sizeof tx_pkt) {
                ring_commit(&l_frm[q], 1, 1);
                ring_commit(&l_frm[q], 2, 1);
                tx_unicast = 0;
                tx_done_q = 0x10 | q;
                tx_complete(1);
                return 0;
            }
            job.local = (unsigned)tx_pkt + 0x8c;
            job.host = e[0] + 0x94;
            job.len = (e[2] >> 16) - 0x8c;
            job.to_host = 0, job.done = tx_stage_done, job.next = 0, job.n = 0;
            return 1;
        }
    return 0;
}

/* A host TX finished (FIQ flagged it): return its record with an ok status (stock 0x112f4). */
static void tx_complete(unsigned status)
{
    unsigned q = tx_done_q & 0xf;
    tx_done_q = 0;
    ((unsigned *)tx_rec)[3] = status;                   /* 0: delivered */
    if ((tx_pkt[0x18] & 0x0c) != 0x08)                 /* only data frames steer the data rate */
        ;
    else if (tx_unicast == 1 && tx_acked) {
        rate_bad = 0;
        if (++rate_ok >= 10 && rate_i)
            rate_i--, rate_ok = 0;
    } else if (tx_unicast) {
        rate_ok = 0;
        if (++rate_bad >= 2 && rate_i < sizeof rate_cls - 1)
            rate_i++, rate_bad = 0;
    }
    ring_commit(&l_frm[q], 3, 1);                      /* record: channel 3 -> 4, picked up by schedule */
    tx_free_t = REG(R_TIMER) - (tx_unicast ? TX_GAP : 0);
    tx_unicast = 0;
    tx_busy = 0;
}

/* diag: count which source schedule() picked (recorder slots RC_WIN + k) */
static int win(unsigned k)
{
    rec[RC_WIN + k]++;
    return 1;
}

/* Completed TX descriptors back to the host, with their buffer address restored: the app frees
 * by word 1 (wifi.c tx_reclaim), and word 0 still holds the host buffer address from submit, so
 * copy it into word 1. (Was e[0]=e[1], which zeroed both and starved the TX pool.) */
static int tx_return(void)
{
    for (unsigned i = 0; i < 4; i++) {
        unsigned n = ring_space(&l_frm[i], 4), m = ring_space(&h_frm[i], 2), e, slot;
        if (!n || !m)
            continue;
        if (m < n)
            n = m;
        for (unsigned k = 0; k < n; k++) {
            ring_take(&l_frm[i], 4, 1, &e);
            if (!k)
                job.local = e;
            ((unsigned *)e)[1] = ((unsigned *)e)[0];
        }
        ring_take(&h_frm[i], 2, n, &slot);
        job.host = slot - h_frm[i].base + h_frm[i].core_base;
        job.len = n * 0x18, job.to_host = 1, job.done = tell_host, job.next = 0;
        job.hr = &h_frm[i], job.hc = 2, job.lr = &l_frm[i], job.lc = 4, job.n = n;
        return 1;
    }
    return 0;
}

/* Fixed priority: host commands, then the TX path (completions, descriptors each followed straight
 * by their payload), then RX frames to the host, RX buffer supply, returned RX buffers, events.
 * TX goes first: it is one frame in flight and a few dozen records a second, while RX with 15 host
 * buffers can keep the DMA busy without a gap; RX first starved TX for seconds (ST20 "tx wedge"). */
static int schedule(void)
{
    unsigned i;
    rec[RC_SCHED]++;
    if (xfer(h_cmd, 1, &l_cmd, 0, 0))
        return win(0);
    if (HOST_HOLD != 1 && tx_return())
        return win(6);
    for (i = 0; i < 4; i++)
        if (xfer(&h_frm[i], 1, &l_frm[i], 0, 0)) {
            job.next = frm_in_data;
            return win(3);
        }
    if (frm_in_data())
        return win(4);
    if (HOST_HOLD != 1 && rx_deliver())
        return win(1);
    if (xfer(h_sup, 1, &l_sup, 0, 0))
        return win(2);
    if (HOST_HOLD == 1)
        return 0;
    if (xfer(h_sup, 2, &l_sup, 3, 1))
        return win(5);
    if (xfer(h_evt, 0, &l_evt, 1, 1))
        return win(7);
    return 0;
}

static void dma_go(void)
{
    rec[RC_DMAGO]++;
    REG(R_DMA_LOCAL) = job.local;
    REG(R_DMA_HOST) = job.host;
    REG(R_DMA_LEN) = job.len;
    if (job.to_host)
        REG(R_DMA_CTL) |= 1;
    else
        REG(R_DMA_CTL) &= ~1u;
    REG(R_DMA_CTL) |= 2;
}

static void dma_done(void)
{
    rec[RC_DMADONE]++;
    if (REG(R_DMA_STATUS) & 2) {
        /* A failed transfer must not wedge the engine: our fw is always-on, so leaving dma_busy
         * set (the stock one-shot behaviour) freezes all ring service forever. ACK the error, drop
         * this job's uncommitted slots, clear busy and let the next poll reschedule. */
        REG(R_DMA_ACK) = 2;
        dma_busy = 0;
        return;
    }
    REG(R_DMA_ACK) = 1;
    irq_off();
    if (job.n) {
        ring_commit(job.hr, job.hc, job.n);
        ring_commit(job.lr, job.lc, job.n);
    }
    irq_on();
    if (job.done)
        job.done();
    if (job.next ? job.next() : schedule())
        dma_go();
    else
        dma_busy = 0;
}

void xport_irq(void)
{
    unsigned st = REG(R_IRQ_STATUS);
    rec[RC_IRQIN]++, rec[RC_IRQST] = st;
    if (st & 1) rec[RC_ST1]++;
    if (st & 2) rec[RC_ST2]++;
    if (st & 0x10) rec[RC_ST10]++;
    REG(0x148) = reg148 |= 0x2000;
    if (st & 1) {
        /* the FIQ's request: the frames it queued go out from xport_poll. The same line carries
         * the MAC timer's wrap (0x620 bit 6, each 2^26 ticks; stock 0xde8c acks it with 0x40 after
         * advancing its epoch): left unacked it holds the IRQ up for good, millions of re-entries a
         * second that starve the main loop. Nothing here keeps a timer epoch. */
        if (REG(0x620) & 0x40) {
            REG(0x628) = 0x40;
            REG(0x628) = 0;
        }
        REG(0x628) = 0x100;
        REG(0x628) = 0;
    } else if (st & 0x10) {
    } else if (st & 2) {
        rec[RC_ST2DONE]++;
        dma_done();
    } else if (st & 4) {
        REG(R_IRQ_SET2) |= 4;
    }
    REG(0x148) = reg148 &= ~0x2000u;
    rec[RC_IRQOUT]++;
}

/* An event record for the host: {1, -, len + 8} {type} {module | src << 16} payload. A record
 * goes in whole or not at all: with no room for all of it (the host has stopped taking events)
 * it is dropped and counted in rec[RC_EVDROP]. (Committing the header regardless handed the host
 * slots that were never written and moved the ring's limit past its own position.) */
static void post_event(unsigned type, unsigned module, unsigned src, const void *payload,
                       unsigned len)
{
    unsigned w, words = (len + 3) >> 2;
    irq_off();
    if (ring_avail(&l_evt, 0) < 3 + words) {
        rec[RC_EVDROP]++;
        irq_on();
        return;
    }
    ring_take(&l_evt, 0, 1, &w);
    ((unsigned char *)w)[0] = 1;
    ((unsigned short *)w)[1] = len + 8;
    ring_take(&l_evt, 0, 1, &w);
    ((unsigned short *)w)[0] = type;
    ring_take(&l_evt, 0, 1, &w);
    ((unsigned short *)w)[0] = module, ((unsigned short *)w)[1] = src;
    ring_commit(&l_evt, 0, 3);
    if (len)
        ring_copy(&l_evt, 0, words, (void *)payload, 1);
    irq_on();
}

/* Messages between modules; the host is module 1, and anything not ours goes to it. */
void route(unsigned type, unsigned module, unsigned src, const void *payload, unsigned len)
{
    rec[RC_CMD] = type | (module & 0xff) << 16;    /* diag: command in progress (bit 31 = done) */
    switch (module & 0xff) {
    case MOD_STA:
        sta_cmd(type, payload, len, module, src);
        break;
    case 0x13:
        scan_cmd(type, payload, len);
        break;
    case 0x17:
        break; /* power-save (0x17) module is a later milestone */
    default:
        if (HOST_HOLD != 1)
            post_event(type, module, src, payload, len);
    }
    rec[RC_CMD] |= 1u << 31;
}

/* Command records on the cmd ring: {kind, -, len} then len bytes; for kind 1 {type}
 * {module | src << 16} and len - 8 payload bytes. A record is taken whole or not at all. A record
 * that cannot be one (another kind: kind 2, a raw block for a module, never comes from wifi.c; a
 * len below the 8 bytes of type and module; a payload over cmd_copy, which wifi.c's wr_cmd never
 * sends since it caps the payload at 256 bytes) is skipped by its declared length and counted in
 * rec[RC_CMDBAD]. It is skipped as its words arrive: a len beyond our ring could never be waited
 * for whole. (len - 8 of a short record was a payload of ~4 GB copied over the core, and a long
 * payload that wrapped our ring overran cmd_copy.) */
static unsigned cmd_state, cmd_kind, cmd_len, cmd_skip, cmd_copy[76];

static void commands(void)
{
    unsigned avail = ring_avail(&l_cmd, 1), w;
    while (avail) {
        if (cmd_skip) {
            unsigned n = ring_space(&l_cmd, 1);
            if (n > cmd_skip)
                n = cmd_skip;
            ring_take(&l_cmd, 1, n, &w);
            ring_commit(&l_cmd, 1, n);
            cmd_skip -= n;
            avail = ring_avail(&l_cmd, 1);
            continue;
        }
        if (!cmd_state) {
            ring_take(&l_cmd, 1, 1, &w);
            cmd_kind = ((unsigned char *)w)[0];
            cmd_len = ((unsigned short *)w)[1];
            ring_commit(&l_cmd, 1, 1);
            avail--;
            if (cmd_kind != 1 || cmd_len < 8 || cmd_len - 8 > sizeof cmd_copy) {
                cmd_skip = (cmd_len + 3) >> 2;
                rec[RC_CMDBAD]++;
                continue;
            }
            cmd_state = 1;
        }
        if (avail * 4 < cmd_len)
            return;
        {
            unsigned type, module, src, plen = cmd_len - 8, words = (plen + 3) >> 2;
            ring_take(&l_cmd, 1, 1, &w);
            type = ((unsigned short *)w)[0];
            ring_take(&l_cmd, 1, 1, &w);
            module = ((unsigned short *)w)[0], src = ((unsigned short *)w)[1];
            ring_commit(&l_cmd, 1, 2);
            if (plen > ring_space(&l_cmd, 1) * 4) {
                ring_copy(&l_cmd, 1, words, cmd_copy, 0);
                route(type, module, src, cmd_copy, plen);
            } else {
                ring_take(&l_cmd, 1, words, &w);
                route(type, module, src, (void *)w, plen);
                ring_commit(&l_cmd, 1, words);
            }
        }
        cmd_state = 0;
        avail = ring_avail(&l_cmd, 1);
    }
}

void xport_poll(void)
{
    rec[RC_POLL]++, rec[RC_PHASE] = 1;
    rec[RC_LIMPOS] = (h_frm[2].ch[1].lim & 0xffff) << 16 | (h_frm[2].ch[1].pos & 0xffff);
    rec[RC_DMABUSY] = dma_busy;
    rec[RC_PHASE] = 2;
    if (tx_done_q && tx_unicast && !tx_acked) {
        if (((REG(R_TIMER) - tx_t0) & 0x3ffffff) <= tx_wait &&
            ((REG(R_TIMER) - tx_done_t) & 0x3ffffff) <= (tx_desc.cls != sta_mgmt_cls ? ACK_LATE : 3000))
            ;
        else if (tx_unicast++ > TX_RETRY)
            tx_complete(1);
        else {
            tx_pkt[0x18 + 1] |= 0x08;                  /* Retry */
            if (tx_unicast > TX_RETRY - 1)
                tx_desc.cls = sta_mgmt_cls;             /* last tries at the basic rate */
            tx_done_q = 0;
            tx_kick();
        }
    } else if (tx_done_q)
        tx_complete(0);
    else if (mgmt_tx_pending && (((REG(R_TIMER) - tx_t0) & 0x3ffffff) > TX_LOST ||
                                 (tx_unicast && ((REG(R_TIMER) - tx_t0) & 0x3ffffff) >
                                                    (tx_acked ? 3000 : tx_wait)))) {
        /* Under load the MAC sometimes raises no TX-done for a host frame on queue 9 (measured:
         * every TX buffer stays out until the app resets the core). Give the record
         * back as failed; TCP resends. SWP takes the pending flag from a late FIQ atomically. */
        unsigned p;
#ifdef __arm__
        __asm__ volatile("swp %0, %1, [%2]" : "=&r"(p) : "r"(0), "r"(&mgmt_tx_pending) : "memory");
#else   /* the host unit tests (tests/run-all.sh) */
        p = __atomic_exchange_n(&mgmt_tx_pending, 0, __ATOMIC_SEQ_CST);
#endif
        if (p) {
            tx_done_q = p;
            /* A unicast frame needs no TX-done: the AP's ACK delivered it, and without an ACK it
             * takes the retry path above on the next poll. ~7 % of frames under load raised no
             * TX-done and each waited TX_LOST, 200 ms with the one TX slot held. */
            if (!tx_unicast || tx_acked)
                tx_complete(!tx_acked);
        }
    }
    rec[RC_PHASE] = 3;
    if (!dma_busy && schedule()) {
        dma_busy = 1;
        dma_go();
    }
    rec[RC_PHASE] = 4;
    commands();
    rec[RC_PHASE] = 5;
}
