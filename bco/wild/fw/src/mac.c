/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* MAC engine: DMA bases and queue setup. The addresses handed to the hardware point at our own
 * buffers; the hardware only needs their sizes and alignment. */
#include "hw.h"

static unsigned dma_a[14], dma_b[14];               /* two 0x38-byte DMA control blocks */
static unsigned qstate[10];                          /* per-queue state words read by the MAC */
volatile unsigned dg_rxfiq, dg_rxkeep;               /* diag: MAC frame-in FIQ count / frames pushed to rxq */
volatile unsigned dg_rxfcs, dg_rxdwell;              /* diag: frame-in with FCS good / arriving in listen dwell (4|6) */
volatile unsigned tx_done_t;                         /* R_TIMER at the last queue-9 TX-done */
volatile unsigned mgmt_tx_pending;                   /* 0x10|ring when a host frame rides queue 9 */

static unsigned long long rxarea[0x3750 / 8];        /* receive area: 8-aligned, 0x3750 bytes */

void dma_init(void)
{
    REG(0x018) |= 2;
    REG(0xb10) = 3;
    REG(0xb08) = (unsigned)dma_a;
    REG(0xb0c) = (unsigned)dma_b;
    REG(0x018) &= ~2u;
}

/* Run the 0xb00 cipher engine on one control block, as stock does (TX block 0x5a10/0x5ec0, start
 * 0x60e8; RX block 0x4634, start 0x48ec; done 0x5240). Block: {ctl = header length << 24 | 0x10
 * (key length) << 8 | mode; data length; source; destination; header; PN low; PN high; -; -; -;
 * 16-byte key}. The engine takes nonce and AAD from the header. 0x018 bit 1 is set while it runs.
 * Channel A (1) enciphers, mode 0x44 | 2 (last fragment): ciphertext + 8-byte MIC to the
 * destination; done = 0xb18 bit 5. Channel B (2) deciphers, mode 0x40: plaintext to the
 * destination, the MIC read after the source; done = 0xb18 bit 21, bits 16-18 are errors (bit 18
 * a MIC mismatch); bit 24 only says B ran last. Writing the channel to 0xb14 acknowledges.
 * Measured on the ST20 (2026-10-22): both bit-exact to software CCMP, a tampered byte sets bit 18.
 * Completion also raises interrupt status bit 4, which xport_irq leaves to us: run with IRQs off,
 * or a run from the main loop (RX) re-enters the IRQ until the core hangs before the ack.
 * Returns 0xb18 at completion, or 0 if the engine did not finish. */
static unsigned engine(unsigned *blk, unsigned ch, unsigned mode, unsigned n, const void *src,
                       void *dst, const void *hdr, const unsigned char *tk, unsigned long long pn)
{
    unsigned i, st = 0, done_bit = ch == 1 ? 0x20 : 0x200000, r18;
    blk[0] = 24u << 24 | 0x10 << 8 | mode;
    blk[1] = n;
    blk[2] = (unsigned)src;
    blk[3] = (unsigned)dst;
    blk[4] = (unsigned)hdr;
    blk[5] = (unsigned)pn;
    blk[6] = (unsigned)(pn >> 32);
    blk[7] = blk[8] = blk[9] = 0;
    copy_bytes(&blk[10], tk, 16);
    irq_off();
    r18 = REG(0x018);
    REG(0x018) = r18 | 2;
    REG(0xb04) = ch;
    for (i = 0; i < 100000 && !((st = REG(0xb18)) & done_bit); i++)
        ;
    REG(0xb14) = ch;
    REG(0x018) = r18;
    irq_on();
    return i < 100000 ? st : 0;
}

/* CCMP-encipher a frame body in place; 0 if the engine failed (never send such a frame). */
int mac_ccmp_tx(const void *hdr, void *body, unsigned n, const unsigned char *tk, unsigned long long pn)
{
    return engine(dma_a, 1, 0x46, n, body, body, hdr, tk, pn) != 0;
}

/* CCMP-decipher n data bytes (the MIC follows them) to out; 1 only if the MIC matched. */
int mac_ccmp_rx(const void *hdr, const void *ct, void *out, unsigned n, const unsigned char *tk,
                unsigned long long pn)
{
    unsigned st = engine(dma_b, 2, 0x40, n, ct, out, hdr, tk, pn);
    return st && !(st & 0x70000);
}

void mac_init(void)
{
    (void)REG(0x6b0);
    REG(0x138) = 0x80;
    REG(0x628) = 0xffffffff;
    REG(0x628) = 0;
    REG(0x61c) = 0x640;
    REG(0x604) |= 0x4000;
    REG(0x604) |= 1;
    for (unsigned i = 0; i < 8; i++)
        qstate[i] = 4;
    qstate[9] = 4;
    REG(0x648) = (unsigned)qstate;
    REG(0x67c) = 0x100a00;
    for (unsigned i = 0; i < 8; i++)
        REG(0x650 + 4 * i) = 0;
    REG(0x674) = 0;
    REG(0x670) = 0;
    REG(0x698) = 4;
    REG(0x64c) = sizeof rxarea;
    REG(0x640) = (unsigned)rxarea;
    REG(0x678) = 0x2e10;
    REG(0x644) = 0;
    REG(0x604) &= ~0x1000u;
    if (REG(0x620) & 0x80000)
        REG(0x604) |= 0x200;
}

/* Receive (stock FIQ 0xa8bc). The MAC writes each frame into rxarea at our read pointer 0x644:
 * {u16 length incl. FCS, -, -, s8 rssi, -, u16 rate index} then the frame. MAC event 0x620
 * bit 2 = frame in (FIQ), 0x40000 = passed the address filter (to us, or anything while a scan
 * listens), 0x400000 = FCS error; 0x630 holds its frame control. A kept frame is
 * queued for xport.c and the read pointer moves past it (stock 0xa83c); the FIQ then asks for
 * the MAC interrupt (0x604 bit 8 strobe, 0x200 bit 0), where the frame goes to the host. */
#define RXQ 4
#define RX_WRAP 0x2e18          /* the pointer restarts at 0 past this (stock 0xa864) */

unsigned rx_chan;               /* channel being scanned; 0 = not listening */
static unsigned rxq[RXQ];
static unsigned short rxq_len[RXQ];     /* each frame's length when queued (rx_peek) */
static volatile unsigned rxq_in, rxq_out;

/* The ACK (stock 0xaa10 + 0xc218): queue 8 from 0x698, started by 0x670 bit 23. The MAC fetches
 * it a SIFS later. Response class by received rate: resp_class (scan.c), the control-response
 * rate rule. A CTS has the ACK's layout (frame control, duration, RA; 14 bytes with FCS) and goes
 * the same way (send_resp). The header is complete before the start: one buffer serves both, and
 * an ACK started before its frame control was written could have gone out as the CTS before it. */
static unsigned char ack_hdr[4];
static struct {
    unsigned short len, z0, rate, cls; /* cppcheck-suppress unusedStructMember ; the MAC reads this descriptor */
    unsigned hdrlen;
    unsigned char *hdr;
    unsigned l1;
    const unsigned char *p1;
    unsigned l2; /* no second segment */ /* cppcheck-suppress unusedStructMember ; the MAC reads this descriptor */
} ack_desc;

static void send_resp(const unsigned char *e, unsigned fc, unsigned dur)
{
    ack_desc.len = 14;
    ack_desc.hdrlen = 4;
    ack_desc.hdr = ack_hdr;
    ack_desc.l1 = 6;
    ack_desc.p1 = e + 8 + 10;                   /* RA = the frame's transmitter (A2, an RTS's TA) */
    ack_desc.cls = resp_class[e[6] & 15] & 15;
    ack_desc.rate = 8;
    ack_hdr[0] = (unsigned char)fc, ack_hdr[1] = 0;
    ack_hdr[2] = (unsigned char)dur, ack_hdr[3] = (unsigned char)(dur >> 8);
    REG(0x698) = (unsigned)&ack_desc;
    REG(0x670) = 0x800000;
}

/* An ACK: duration 0, no fragment bursts. */
static void send_ack(const unsigned char *e)
{
    send_resp(e, 0xd4, 0);
    if (ap_mode) {                              /* diag: what the setup AP acknowledges, and how */
        rec[RC_APACK]++;
        rec[RC_APACKLAST] = ack_desc.cls | (e[6] & 15u) << 8 | (unsigned)e[8] << 16;
    }
}

static int same_mac(const unsigned char *a, const unsigned char *b)
{
    unsigned d = 0;
    for (unsigned i = 0; i < 6; i++)
        d |= a[i] ^ b[i];
    return !d;
}

/* diag: the kind of a frame to us in ap_mode (hw.h AP_*), by its frame control: what a client
 * that associates but never gets an address still sends, and what the address filter misses. */
static unsigned ap_kind(unsigned fc)
{
    if (fc == 0xb4)
        return AP_RTS;
    if (fc == 0xd4)
        return AP_ACK;
    if (fc == 0x40)
        return AP_PROBE;
    if (fc == 0x48 || fc == 0xc8)
        return AP_NULL;
    return (fc & 0x0c) == 0x08 ? AP_DATA : AP_OTHER;
}

void mac_fiq(void)
{
    unsigned st = REG(0x620);
    rec[RC_FIQIN]++;
    /* A frame with a bad FCS is neither ACKed nor kept: the sender then retries it. ACKing it
     * (as when only 0x40000 was checked) lost about half of all large frames on the ST20. */
    if ((st & 0x400004) == 4) {
        dg_rxfiq++;             /* diag: the MAC raised a frame-in, before any keep filtering */
        if (st & 0x40000)
            dg_rxfcs++;         /* diag: this frame-in passed the address filter */
        if (dwell_state == 4 || dwell_state == 6)
            dg_rxdwell++;       /* diag: this frame-in arrived inside a listen dwell */
        unsigned ptr = REG(0x644) & 0xffff, fc = REG(0x630) & 0xfc;
        unsigned char *e = (unsigned char *)rxarea + ptr;
        unsigned len = *(unsigned short *)e;
        /* The AP's ACK to us ends the host frame in flight (xport.c ACK_WAIT). RA at e + 12. */
        if (fc == 0xd4 && e[12] == own_mac[0] && e[13] == own_mac[1] && e[14] == own_mac[2] &&
            e[15] == own_mac[3] && e[16] == own_mac[4] && e[17] == own_mac[5])
            tx_acked = 1;
        /* Scan results: beacons while a passive scan listens (dwell 4, stock 0xb118), probe
         * responses while an active scan listens (dwell 6, stock 0xaab0), which also extends the
         * listen. Management frames for the station (association, authentication, deauth,
         * disassoc, action; stock 0xb244) are kept and ACKed whenever there is room.
         * data frames come with the peer table; beacons after the join stay in stock's
         * core (TSF/TIM) and are dropped here. */
        int mgmt = (fc & 0x0c) == 0 && (0x3e0fu >> (fc >> 4) & 1);
        int group = e[12] & 1;  /* A1 group bit: such frames are never ACKed */
        /* In ap_mode the BSS is our own, and a client's frames to it carry our address as A1:
         * data and null-data to the distribution system (toDS), its management frames, a
         * PS-Poll. Whether the address filter, set up for a station, flags a toDS frame was
         * never measured; a client whose DHCP request got neither kept nor ACKed fell back to
         * a link-local address (ST20, 2026-11-27). So in ap_mode A1 = own counts as passing it. */
        int to_us = ap_mode && !group && same_mac(e + 12, own_mac);
        int hit = (st & 0x40000) || to_us;
        if (to_us) {
            unsigned kind = ap_kind(fc);
            rec[RC_APIN]++;         /* diag: AP_STATUS shows how many frames reached us... */
            rec[RC_APTYPE + kind]++;
            if (!(st & 0x40000)) {
                rec[RC_APNOFILT]++; /* ...and how many of them the address filter missed */
                rec[RC_APNFTYPE + kind]++;
            }
        }
        /* Non-QoS data (type 10, bit 0x80 = QoS clear) from the BSS once joined: it arrives still enciphered
         * and goes to the host like a management frame; the app deciphers it (wifi.c rx_station).
         * HT data, A-MSDU and null-data (no body) are later work. Not in ap_mode: there the
         * group data on the channel is other networks' (our clients send to our address), and
         * keeping it filled the four-frame queue a client's frame needs room in. */
        int data = (fc & 0x8c) == 0x08 && joined && !ap_mode && len > 28;
        /* QoS data from the BSS (fromDS) in the clear: kept too and passed up as plain data,
         * without its QoS control (xport.c rx_deliver), as a setup AP client's. We announce no
         * WMM, so an AP should send us none, but one that does is not lost. Not protected QoS data:
         * its CCMP nonce and AAD take the QoS control's TID, and whether the cipher engine does
         * that with a 26-byte header was never measured (it is ACKed and dropped, as before);
         * not an A-MSDU (QoS control bit 7), which needs HT, which this radio is not shown to do. */
        int qdata = (fc & 0xfc) == 0x88 && joined && !ap_mode && (e[9] & 0x43) == 0x02 &&
                    len > 24 + 2 + 4 && !(e[8 + 24] & 0x80);
        /* A client's data, null-data, QoS data and QoS-null to us (no CF subtypes): all kept,
         * the null frames for their power-management bit (the app buffers for a dozing
         * client), QoS data without its QoS control (xport.c rx_deliver). And its PS-Poll
         * (16 bytes), which asks for one buffered frame. */
        int apdata = to_us && (fc & 0x3c) == 0x08;
        int pspoll = to_us && fc == 0xa4;
        unsigned minlen = pspoll ? 20 : 28;
        /* Every unicast data frame to us is ACKed once joined, kept or not: data, null-data (0x48),
         * QoS data (0x88) and QoS-null (0xc8). APs probe an idle station with null-data and
         * disassociate one that does not ACK it (hostapd after ap_max_inactivity, reason 4), and
         * QoS data that is not ACKed is retried until the AP gives up. Protected QoS data is still
         * dropped here (not kept, qdata above); its ACK only stops the retries. A QoS frame whose ack policy (QoS
         * control bits 5-6, after a 3- or 4-address header) is not Normal Ack is not ACKed:
         * No Ack forbids it, and we hold no block-ack agreement. CF-Ack/CF-Poll subtypes are
         * not answered. */
        unsigned hl = (e[9] & 3) == 3 ? 30 : 24;     /* ToDS and FromDS: 4-address header */
        int ack_data = (fc & 0x3c) == 0x08 && joined && !group &&
                       (!(fc & 0x80) || (len >= hl + 2 + 4 && !(e[8 + hl] & 0x60)));
        /* A deauth or disassoc to everybody from the BSS we joined (both A2 and A3 its BSSID): it
         * ends the association for every station, so the host must see it although it does not
         * pass the address filter. Not ACKed (group). Not in ap_mode, where the BSS is our own. */
        int leave = (fc == 0xc0 || fc == 0xa0) && joined && !ap_mode && group &&
                    same_mac(e + 18, join_bssid) && same_mac(e + 24, join_bssid);
        /* A probe request while the app runs its access point (ap_mode): it answers with a probe
         * response. Wildcard ones are addressed to everybody, so like group data they need not
         * pass the address filter. */
        int probe = fc == 0x40 && ap_mode;
        /* Keep beacons during BOTH a passive (4) and an active (6) scan dwell: on hardware the
         * active-scan probe TX is unreliable, but APs beacon every ~100 ms regardless, so capturing
         * them passively during the active dwell makes the scan robust instead of probe-dependent. */
        int keep = fc == 0x80 ? (dwell_state == 4 || dwell_state == 6)
                 : fc == 0x50 ? dwell_state == 6
                 : (mgmt || data || qdata || probe || apdata || pspoll);
        unsigned room = rxq_in - rxq_out < RXQ;
        if (apdata && !(fc & 0x40)) {   /* diag: a client's frame with a body, its rate and level */
            rec[RC_APDATA]++;
            rec[RC_APDRATE] = e[6] | (unsigned)e[4] << 8 | fc << 16;
            if (!room)
                rec[RC_APNOROOM]++;
        }
        /* Group-addressed data (ARP, mDNS, DHCP broadcasts) never passes the address filter once
         * joined, so 0x40000 is clear; stock keeps it anyway and so do we (not ACKed, below).
         * Without it nobody on the LAN could resolve this speaker. The pointer moves on
         * before the ACK goes out: the MAC writes whatever it receives next at 0x644, and a
         * frame arriving while the ACK was sent landed on the frame just kept (ST20 2026-10-23).
         * The frame must also be the one the MAC latched (0x630) with a length that fits: after
         * such an overwrite the next frame-in found stale bytes at 0x644, and their length sent
         * the pointer anywhere. */
        if ((hit || ((data || qdata || probe || leave) && group)) && keep && room && len >= minlen &&
            len <= sizeof rxarea - RX_WRAP && (e[8] & 0xfc) == fc) {
            rxq[rxq_in % RXQ] = (unsigned)e;
            rxq_len[rxq_in % RXQ] = len;
            rxq_in++;
            dg_rxkeep++;        /* diag: a received frame passed keep and went to the host ring */
            if (apdata)
                rec[RC_APKEEP]++;   /* diag: a client's data frame on its way to the app */
            ptr = ((((ptr + 4 + len) & ~0x30000u) + 7) & ~0x10007u);
            REG(0x644) = ptr >= RX_WRAP ? 0 : ptr;
            REG(0x604) |= 0x100;
        }
        /* Unicast data is ACKed even when the queue is full (802.11: the ACK answers a good FCS,
         * not buffer space) and then dropped; TCP recovers. Withholding the ACK made the AP retry,
         * back off and fall back to slower rates: 30-40 % of a download on the ST20. A group
         * frame is never ACKed, also while a scan lets everything through the address filter. */
        if (hit && !group && len >= minlen && (fc == 0x50 || (mgmt && room) || ack_data || pspoll))
            send_ack(e);
        /* An RTS to us (RA = own address; the setup AP, or the station once joined) gets a CTS a
         * SIFS later, to its TA, at the control-response rate, with the RTS's duration less the
         * SIFS and the CTS itself (IEEE 802.11-2020 9.3.1.3; 0 if that leaves nothing). Without
         * it a client that protects its frames with RTS never sends them. The NAV is not kept, so
         * the CTS goes whatever the medium was reserved for. rec[RC_APTYPE + AP_RTS] shows
         * whether RTS frames reach the FIQ at all. Only when the receive area holds this RTS (its
         * frame control is the latched one): stale bytes there would send a CTS nobody asked for. */
        if (fc == 0xb4 && (ap_mode || joined) && !group && len >= 20 && (e[8] & 0xfc) == fc &&
            same_mac(e + 12, own_mac)) {
            unsigned dur = e[10] | (unsigned)e[11] << 8, cost = 10 + airtime(resp_class[e[6] & 15], 14);
            send_resp(e, 0xc4, dur & 0x8000 || dur <= cost ? 0 : dur - cost);
            rec[RC_CTS]++;
        }
        if (keep && fc == 0x50 && (st & 0x40000))
            dwell_extend();
    } else if ((st & 4) && ap_mode) {
        /* diag: a frame to us that arrived damaged (bad FCS): how often, at what rate and
         * level. A phone whose data never reaches the app shows here whether the radio heard it. */
        const unsigned char *e = (const unsigned char *)rxarea + (REG(0x644) & 0xffff);
        if (same_mac(e + 12, own_mac)) {
            rec[RC_APFCS]++;
            rec[RC_APFRATE] = e[6] | (unsigned)e[4] << 8 | (REG(0x630) & 0xfc) << 16;
        }
    }
    if (st & 0x800) {               /* MAC timers (0x6b4); slot bit 2 = scan dwell (0x6c4) */
        if (REG(0x6b4) & 4) {
            REG(0x6bc) = 4;
            REG(0x6b8) &= ~4u;
            dwell_fired = 1;
        }
        REG(0x604) |= 0x100;
        st &= ~0x800u;
    }
    if (st & 0x200) {               /* frame sent (queue in bits 24..27, status 0x624) */
        unsigned q = (st >> 24) & 0xf;
        if (q < 8)
            tx_done_q = 0x10 | q;   /* host TX completion; the record is returned from xport_poll */
        else if (q == 9 && mgmt_tx_pending) {   /* host frame routed over the mgmt queue completed */
            tx_done_q = mgmt_tx_pending;
            mgmt_tx_pending = 0;
            tx_done_t = REG(R_TIMER);
        }
        REG(0x628) = 0x200;
        REG(0x628) = 0;
        st &= ~0x0f000200u;
    }
    if (st & ~0x100u) {
        REG(0x628) = st & ~0x100u;  /* other FIQ events acked unserved */
        REG(0x628) = 0;
    }
    rec[RC_FIQOUT]++;
}

/* Send one management frame: the MAC reads the queue-9 descriptor pointer from its queue state
 * and starts on the 0x674 bit 23 edge (stock 0xd898). desc = {u16 length incl. FCS, -, u16 rate,
 * u16 rate class, header length, header, segment length, segment, segment length, segment}.
 * backoff = 0 sends at once; 0x20000 | slots contends for the medium first with that many backoff
 * slots (the probe retry, stock scan path). */
void mac_send_mgmt(void *desc, unsigned backoff)
{
    unsigned v = REG(0x674) & ~0x18203ffu;
    qstate[9] = (unsigned)desc;
    REG(0x674) = v;              /* the start is an edge: a retry found the bits still set */
    REG(0x674) = v | 0x1800000 | backoff;
}

/* Host data/management frame on access-class queue q (0..7): the MAC reads the descriptor pointer
 * from its queue state and starts on the 0x650 + 4q bit-23 edge (stock 0x1197c). */
/* cppcheck-suppress unusedFunction ; unused; part of the MAC queue interface in hw.h */
void tx_mac_start(unsigned q, void *desc)
{
    qstate[q] = (unsigned)desc;
    REG(0x650 + 4 * q) |= 0x800000;
}

/* Oldest received frame not yet delivered, or 0. A frame whose length changed since it was
 * queued was overwritten by the MAC and is dropped (rx_deliver trusts the length). */
const unsigned char *rx_peek(void)
{
    while (rxq_in != rxq_out) {
        const unsigned char *e = (const unsigned char *)rxq[rxq_out % RXQ];
        if (*(const unsigned short *)e == rxq_len[rxq_out % RXQ])
            return e;
        rx_pop();
    }
    return 0;
}

/* Done with the oldest frame: the MAC may write up to its end again (0x678, stock 0xea4c). */
void rx_pop(void)
{
    unsigned i = rxq_out % RXQ;
    unsigned end = (rxq[i] + 8 + rxq_len[i] - 4 - REG(0x640) - 1) & ~7u;
    REG(0x678) = end >= RX_WRAP ? RX_WRAP - 8 : end;
    rxq_out++;
}
