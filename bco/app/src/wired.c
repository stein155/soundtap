/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The ST20's wired port: the BCO's Cadence MACB at 0xe0017000 and an SMSC PHY (ID
 * 0007:c0f1) at MDIO address 0. Register layout, descriptors and the MDIO frame are the public
 * Cadence MACB ones; base, module ID, switch-matrix value and the PHY reset GPIOs are hardware
 * facts read from the stock app (docs/st20 BCO hardware map, section 7). The code is ours.
 *
 * Polled like the rest of the card: wired_poll from the main loop moves received frames to the
 * SCM on BCOLINK_CH_WIRED and follows the PHY's link; wired_tx_eth queues the SCM's frames. */
#include "bcolink.h"
#include "netcard.h"
#include "usblink.h"

#define MACB 0xe0017000u
#define R(o) (*(volatile unsigned *)(MACB + (o)))
#define NCR 0x00   /* b2 RE, b3 TE, b4 MPE (management port), b9 TSTART */
#define NCFGR 0x04 /* b0 100 Mbit/s, b1 full duplex, b6 multicast hash, b8 1536-byte frames,
                    * b11:10 MDC divider (2 as found), b17 drop the FCS */
#define NSR 0x08   /* b2: MDIO idle */
#define TSR 0x14
#define RBQP 0x18
#define TBQP 0x1c
#define RSR 0x20
#define ISR 0x24
#define IDR 0x2c
#define MAN 0x34   /* MDIO frame */
#define HRB 0x90
#define HRT 0x94
#define SA1B 0x98
#define SA1T 0x9c
#define MID 0xfc   /* module ID; bits 19:16 = 1 on this part */
#define MTX_ETH (*(volatile unsigned *)0xe0013024) /* switch matrix, Ethernet group; stock 0x01 */
/* GPIO pins 0..19, one bit each, write-1 registers: +0 input, +4 output, +8 drive high, +0xc drive
 * low; reads: +0 the input mask, +8 the levels. Layout from the stock app's GPIO driver. */
#define GPIO(o) (*(volatile unsigned *)(0xffff0400u + (o)))

#define NCFGR_BASE (1u << 6 | 1u << 8 | 2u << 10 | 1u << 17)
#define RX_N 64        /* 128-byte buffers, the MACB's fixed size: 8 KB, as stock */
#define TX_N 2
#define TX_USED (1u << 31)
#define TX_WRAP (1u << 30)
#define TX_LAST (1u << 15)
/* Descriptors and buffers in internal SRAM, as stock's: the MACB's DMA does not reach SDRAM, and
 * it sees SRAM through the 0x500000 window (stock's queue pointers read 0x5065xx). The WiLD rings
 * end at SRAM 0xa140 (stacks.h); this uses 0xa200..0xd020, below the boot stage at 0xd9e8. */
#define SRAM_CPU 0x00400000u
#define SRAM_DMA 0x00500000u
#define RXD_OFF 0xa200u
#define RXB_OFF 0xa400u
#define TXD_OFF 0xc400u
#define TXB_OFF 0xc420u
#define RXD ((volatile unsigned *)(SRAM_CPU + RXD_OFF))     /* RX_N x {addr|wrap|used, status} */
#define RXB ((volatile unsigned char *)(SRAM_CPU + RXB_OFF))
#define TXD ((volatile unsigned *)(SRAM_CPU + TXD_OFF))     /* TX_N x {addr, ctrl} */
#define TXB ((volatile unsigned char *)(SRAM_CPU + TXB_OFF)) /* TX_N x 1536 */

static int up = -1;             /* -1 no port, 0 no link, 1 link */
static unsigned phy, rx_i, tx_i, next_ms;
unsigned wired_rx, wired_tx, wired_rx_bad;

static unsigned char *put32(unsigned char *o, unsigned v)
{
    for (int i = 0; i < 32; i += 8) *o++ = v >> i;
    return o;
}

/* A clause-22 MDIO read: 0x6002 start/read/turnaround, phy 27:23, reg 22:18. -1 when the bus
 * never goes idle (no clock to the block, or no MDIO at all). */
static int mdio_read(unsigned p, unsigned reg)
{
    R(MAN) = 0x60020000u | p << 23 | reg << 18;
    for (unsigned t = 0; t < 200000; t++)
        if (R(NSR) & 4)
            return R(MAN) & 0xffff;
    return -1;
}

static void wait_ms(unsigned ms)
{
    for (unsigned t = usblink_ms(); usblink_ms() - t < ms;)
        ;
}

/* Stock's Ethernet bring-up (HWCom): GPIO 12 is the PHY's reset and GPIO 6 is driven low with it;
 * 12 low for 10 ms, then high, and 100 ms for the PHY to leave reset. Proven on the ST20: the PHY
 * answers at MDIO address 0 afterwards, and not before. */
static void eth_reset(void)
{
    GPIO(0xc) = 1u << 12 | 1u << 6;
    GPIO(4) = 1u << 12 | 1u << 6;
    wait_ms(10);
    GPIO(8) = 1u << 12;
    wait_ms(100);
}

/* mac: the address the SCM's eth0 sends from (the Wi-Fi MAC, locally administered). Returns 0
 * when a PHY answers; -1 leaves the port off, and wired_tx_eth then reports no carrier. */
int wired_init(const unsigned char mac[6])
{
    if ((R(MID) >> 16 & 15) != 1) /* not the block we expect there: touch nothing */
        return -1;
    eth_reset();
    if (!(MTX_ETH & 3))
        MTX_ETH = 1;
    R(NCR) = 1u << 4;
    for (phy = 0; phy < 32; phy++) {
        int bmsr = mdio_read(phy, 1);
        if (bmsr < 0)
            return -1;
        if (bmsr != 0 && bmsr != 0xffff)
            break;
    }
    if (phy == 32)
        return -1;
    R(IDR) = 0x3fff;
    (void)R(ISR);
    R(TSR) = 0xff;
    R(RSR) = 0xff;
    for (unsigned i = 0; i < RX_N; i++) {
        RXD[2 * i] = (SRAM_DMA + RXB_OFF + 128 * i) | (i == RX_N - 1 ? 2 : 0);
        RXD[2 * i + 1] = 0;
    }
    for (unsigned i = 0; i < TX_N; i++) {
        TXD[2 * i] = SRAM_DMA + TXB_OFF + 1536 * i;
        TXD[2 * i + 1] = TX_USED | (i == TX_N - 1 ? TX_WRAP : 0);
    }
    rx_i = tx_i = 0;
    R(RBQP) = SRAM_DMA + RXD_OFF;
    R(TBQP) = SRAM_DMA + TXD_OFF;
    R(SA1B) = mac[0] | mac[1] << 8 | mac[2] << 16 | (unsigned)mac[3] << 24;
    R(SA1T) = mac[4] | mac[5] << 8;
    R(HRB) = R(HRT) = ~0u; /* every multicast: mDNS, IPv6 neighbour discovery */
    R(NCFGR) = NCFGR_BASE;
    R(NCR) = 1u << 2 | 1u << 3 | 1u << 4;
    up = 0;
    return 0;
}

/* The link as autonegotiated: speed and duplex into NCFGR. */
static void follow_link(void)
{
    (void)mdio_read(phy, 1); /* BMSR's link bit latches low: the second read is the state now */
    int bmsr = mdio_read(phy, 1);
    int link = bmsr > 0 && bmsr & 4;
    if (link == up)
        return;
    up = link;
    if (!link)
        return;
    int common = mdio_read(phy, 4) & mdio_read(phy, 5);
    unsigned f = NCFGR_BASE;
    if (common & 0x180) /* 100BASE-TX full or half */
        f |= 1u | (common & 0x100 ? 2u : 0);
    else if (common & 0x40)
        f |= 2u;
    R(NCFGR) = f;
}

/* Received frames, buffer by buffer: the MACB sets bit 0 of a descriptor's first word when it
 * filled the buffer, SOF/EOF (status b14/b15) mark a frame's first and last, and the last one
 * carries the frame length (b11:0, FCS dropped). A frame not complete yet is picked up next time. */
static void rx_poll(void)
{
    static unsigned char f[1536];
    static unsigned pos, in_frame;
    for (unsigned k = 0; k < RX_N; k++) {
        volatile unsigned *d = RXD + 2 * rx_i;
        if (!(d[0] & 1))
            break;
        unsigned st = d[1];
        if (st & 1u << 14)
            pos = 0, in_frame = 1;
        if (in_frame && pos + 128 <= sizeof f) {
            const volatile unsigned char *b = RXB + 128 * rx_i;
            for (unsigned i = 0; i < 128; i++) f[pos + i] = b[i];
            pos += 128;
        } else {
            in_frame = 0;
        }
        if (st & 1u << 15) {
            unsigned n = st & 0xfff;
            if (in_frame && n <= pos) {
                netcard_from_port(BCOLINK_CH_WIRED, f, n);
                wired_rx++;
            } else {
                wired_rx_bad++;
            }
            in_frame = 0;
        }
        d[0] &= ~1u;
        rx_i = (rx_i + 1) % RX_N;
    }
    unsigned rsr = R(RSR);
    if (rsr)
        R(RSR) = rsr; /* buffer-not-available / overrun: the ring is free again now */
}

void wired_poll(unsigned now_ms)
{
    if (up < 0)
        return;
    if ((int)(now_ms - next_ms) >= 0) {
        next_ms = now_ms + 250;
        follow_link();
    }
    rx_poll();
}

/* A frame from the SCM: 0 when queued, -1 (dropped) without a link or with the ring full. */
int wired_tx_eth(const unsigned char *f, unsigned n)
{
    if (up != 1 || n > 1514)
        return -1;
    volatile unsigned *d = TXD + 2 * tx_i;
    if (!(d[1] & TX_USED))
        return -1;
    volatile unsigned char *b = TXB + 1536 * tx_i;
    for (unsigned i = 0; i < n; i++) b[i] = f[i];
    for (; n < 60; n++) b[n] = 0;
    d[1] = n | TX_LAST | (tx_i == TX_N - 1 ? TX_WRAP : 0);
    tx_i = (tx_i + 1) % TX_N;
    R(NCR) |= 1u << 9; /* TSTART */
    wired_tx++;
    return 0;
}

/* Reply: le32 module ID, NCR, NCFGR, NSR, TSR, RSR, port state (-1 none, 0 down, 1 up), frames
 * received, sent, bad; then the PHY: u8 address, le16 BMCR, BMSR, ID1, ID2, ANLPAR (none when the
 * port is off). Returns the bytes written. */
int wired_probe(unsigned char *o)
{
    unsigned char *s = o;
    static const unsigned char regs[] = {NCR, NCFGR, NSR, TSR, RSR};
    o = put32(o, R(MID));
    for (unsigned i = 0; i < sizeof regs; i++) o = put32(o, R(regs[i]));
    o = put32(o, (unsigned)up);
    o = put32(o, wired_rx);
    o = put32(o, wired_tx);
    o = put32(o, wired_rx_bad);
    if (up < 0)
        return o - s;
    *o++ = phy;
    static const unsigned char pr[] = {0, 1, 2, 3, 5};
    for (unsigned i = 0; i < sizeof pr; i++) {
        int v = mdio_read(phy, pr[i]);
        *o++ = v, *o++ = v >> 8;
    }
    return o - s;
}
