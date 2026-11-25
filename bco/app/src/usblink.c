/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include "board.h"
#include "bcolink.h"
#include "usblink.h"

#define MUSB ((volatile unsigned char *)0xb0000000)
#define R16(o) (*(volatile unsigned short *)(MUSB + (o)))
#define CSR0 0x12
#define CLK ((volatile unsigned *)0xe0007000)

/* Stock's write order from boot to a running MUSB (QEMU trace of the stock image, replayed on hardware 2026-10-03).
 * Width 1 = byte write. The MUSB clock/VBUS fix is e0007700, e0007040 and the final e0007740 = 0x34300. */
static const struct { unsigned a, v, w; } init_seq[] = {
    /* The first stage's PLL writes (0xe0007010/14/00/18/04) are not replayed: the CPU already
     * runs from that PLL, and reprogramming it underneath wedged a Bose-free boot. */
    {0xe0001000, 0xf0f0f335, 4},
    {0xe0013000, 0x102, 4},
    {0xe0013004, 0x703, 4},
    {0xe0013008, 0xf01, 4},
    {0xe001300c, 0x706, 4},
    {0xe0013010, 0x103, 4},
    {0xe0013014, 0x207, 4},
    {0xe0013020, 0x20, 4},
    {0xe0013024, 0x1, 4},
    {0xe0013028, 0x1, 4},
    {0xe001302c, 0x0, 4},
    {0xe0013030, 0x0, 4},
    {0xe0013034, 0x0, 4},
    {0xe0013040, 0x0, 4},
    {0xe0013044, 0x0, 4},
    {0xe0013050, 0x0, 4},
    {0xe0013054, 0x0, 4},
    {0xe0013058, 0x0, 4},
    {0xe001305c, 0x0, 4},
    {0xe0013060, 0x0, 4},
    {0xe0013064, 0x0, 4},
    {0xe0013068, 0x0, 4},
    {0xe001306c, 0x0, 4},
    {0xe0013070, 0x0, 4},
    {0xe0013074, 0x0, 4},
    {0xe0013078, 0x0, 4},
    {0xe001307c, 0x0, 4},
    {0xe0013080, 0x0, 4},
    {0xe0013084, 0x0, 4},
    {0xe0013088, 0x0, 4},
    {0xe001308c, 0x0, 4},
    {0xe0007020, 0x4, 4},
    {0xe0007024, 0x4, 4},
    {0xe0007020, 0x8, 4},
    {0xe0007024, 0x8, 4},
    {0xe0007020, 0x80, 4},
    {0xe0007024, 0x80, 4},
    {0xe0007020, 0x100, 4},
    {0xe0007024, 0x100, 4},
    {0xe0007020, 0x1000, 4},
    {0xe0007024, 0x1000, 4},
    {0xe0007020, 0x10000, 4},
    {0xe0007024, 0x10000, 4},
    {0xe0007020, 0x800000, 4},
    {0xe0007024, 0x800000, 4},
    {0xe0007020, 0x1000000, 4},
    {0xe0007024, 0x1000000, 4},
    {0xe0007020, 0x2000000, 4},
    {0xe0007024, 0x2000000, 4},
    {0xe0007040, 0x200000, 4},
    {0xe00070f0, 0x80000006, 4},
    {0xe0007244, 0x60100, 4},
    {0xe0007248, 0x10002, 4},
    {0xe0007240, 0x80020666, 4},
    {0xe0007248, 0x110002, 4},
    {0xe0007740, 0x30000, 4},
    {0xe00070f0, 0x80000006, 4},
    {0xe00072c4, 0x60100, 4},
    {0xe00072c8, 0x30006, 4},
    {0xe00072c0, 0x800187ff, 4},
    {0xe00072c8, 0x130006, 4},
    {0xe0007348, 0x0, 4},
    {0xe0007050, 0x3f, 4},
    {0xe0010010, 0x1300, 4},
    {0xe0007700, 0x1, 4},
    {0xe001e000, 0x10a7, 4},
    {0xe0010010, 0x1000000, 4},
    {0xe0010010, 0x0, 4},
    {0xe0010010, 0x2000000, 4},
    {0xe0010010, 0x0, 4},
    {0xe0010010, 0x2, 4},
    {0xe0010024, 0x11, 4},
    {0xe0010010, 0x1000000, 4},
    {0xe0010010, 0x0, 4},
    {0xe0010010, 0x2000000, 4},
    {0xe0010010, 0x0, 4},
    {0xe0010004, 0x2, 4},
    {0xe0010100, 0x0, 4},
    {0xe000c024, 0x2, 4},
    {0xe000c024, 0x10, 4},
    {0xe000c024, 0x4, 4},
    {0xe0010100, 0x0, 4},
    {0xe0007040, 0x200000, 4},
    {0xe0013028, 0x0, 4},
    {0xe001b308, 0x0, 4},
    {0xe001b300, 0xc0, 4},
    {0xe001b304, 0x1b1, 4},
    {0xe001b200, 0x0, 4},
    {0xe001b008, 0x1, 4},
    {0xe0012000, 0x2, 4},
    {0xe0012000, 0x20, 4},
    {0xe0012000, 0x0, 4},
    {0xe0012000, 0x0, 4},
    {0xe0010000, 0x0, 4},
    {0xe0010010, 0x10000, 4},
    {0xe0010100, 0x0, 4},
    {0xe0012010, 0x3e, 4},
    {0xe0012010, 0x3e00, 4},
    {0xe0012010, 0x3e0000, 4},
    {0xe0012010, 0x3e000000, 4},
    {0xb000000b, 0xff, 1},
    {0xb0000060, 0x1, 1},
    {0xb0000001, 0x60, 1},
    {0xb0000084, 0x0, 1},
    {0xb0000080, 0x0, 1},
    {0xb000000e, 0x0, 1},
    {0xb000000e, 0x0, 1},
    {0xb000000e, 0x0, 1},
    {0xb0000006, 0x1, 1},
    {0xb0000007, 0x0, 1},
    {0xb000000e, 0x0, 1},
    {0xe0007020, 0x2, 4},
    {0xe0007020, 0x80000, 4},
    {0xe0007024, 0x80000, 4},
    {0xe0007024, 0x2, 4},
};

/* One vendor interface, two bulk endpoints (ADR 0031). No CDC: cdc_ether must leave it alone. */
static const unsigned char dev_desc[18] = {18, 1, 0, 2, 0, 0, 0, 64,
    BCOLINK_USB_VID & 255, BCOLINK_USB_VID >> 8, BCOLINK_USB_PID & 255, BCOLINK_USB_PID >> 8,
    0, 1, 1, 2, 0, 1};
static const unsigned char cfg_desc[32] = {
    9, 2, 32, 0, 1, 1, 0, 0x80, 250,
    9, 4, 0, 0, 2, BCOLINK_USB_CLASS, BCOLINK_USB_SUBCLASS, BCOLINK_USB_PROTO, 0,
    7, 5, 0x81, 2, 0, 2, 0,                         /* bulk IN 512 */
    7, 5, 0x02, 2, 0, 2, 0};                        /* bulk OUT 512 */
static const unsigned char qual_desc[10] = {10, 6, 0, 2, 0, 0, 0, 64, 1, 0};
static const unsigned char lang_desc[4] = {4, 3, 9, 4};

static unsigned char str_buf[40];
static const unsigned char *string_desc(unsigned i)
{
    const char *t = i == 1 ? "SoundTap" : "BCO link";
    unsigned k = 0;
    for (; t[k]; k++) { str_buf[2 + 2 * k] = t[k]; str_buf[3 + 2 * k] = 0; }
    str_buf[0] = 2 + 2 * k, str_buf[1] = 3;
    return str_buf;
}

static void fifo_put(unsigned off, const unsigned char *p, unsigned n)
{
    for (unsigned i = 0; i < n; i++)
        MUSB[off] = p[i];
}

static void ep_config(void)
{
    /* index at 0x0e (always back to 0 for CSR0); FIFO RAM: EP0 0-63, EP1 TX 64.., EP2 RX 576.. */
    MUSB[0x0e] = 1; R16(0x10) = 512; MUSB[0x62] = 6; R16(0x64) = 8;   R16(0x12) = 0x48 | 0x2000; R16(0x12) = 0x40 | 0x2000;
    MUSB[0x0e] = 2; R16(0x14) = 512; MUSB[0x63] = 6; R16(0x66) = 72;  R16(0x16) = 0x90;          R16(0x16) = 0x80;
    MUSB[0x0e] = 0;
}

static unsigned configured, addr_pending;
static const unsigned char *ctl_p;
static unsigned ctl_n;
static unsigned char rx_buf[BCOLINK_HDR + BCOLINK_MAX];
static unsigned rx_n, rx_bad; /* bytes of the transfer so far; rx_bad: drop it at its short packet */
unsigned usblink_rx_dropped, usblink_tx_dropped;

unsigned usblink_ms(void)
{
    static unsigned ms, last;
    unsigned f = R16(0x0c) & 0x7ff; /* FRAME: 11-bit frame number; poll faster than 2 s */
    ms += (f - last) & 0x7ff;
    last = f;
    return ms;
}

void usblink_init(void)
{
    for (unsigned i = 0; i < sizeof init_seq / sizeof init_seq[0]; i++) {
        if (init_seq[i].w == 1)
            *(volatile unsigned char *)init_seq[i].a = init_seq[i].v;
        else
            *(volatile unsigned *)init_seq[i].a = init_seq[i].v;
    }
    CLK[0x740 / 4] = 0x34300;
    MUSB[0x0b] = 0xff;
    MUSB[0x60] = 1;
    MUSB[0x01] = 0x60; /* HSENAB | SOFTCONN */
}

static void ep0_in(const unsigned char *p, unsigned n)
{
    unsigned m = n > 64 ? 64 : n;
    R16(CSR0) = 0x40;
    fifo_put(0x20, p, m);
    ctl_p = p + m, ctl_n = n - m; /* an exact multiple of 64 would need a ZLP, none of ours is */
    R16(CSR0) = 0x02 | (ctl_n ? 0 : 0x08);
}

static void ep0_setup(void)
{
    unsigned char s[8];
    if (R16(CSR0) & 0x10)
        R16(CSR0) = 0x80; /* SETUPEND from the previous transfer */
    unsigned c = MUSB[0x18];
    for (unsigned k = 0; k < c && k < 8; k++)
        s[k] = MUSB[0x20];
    unsigned t = s[0], rq = s[1], v = s[2] | s[3] << 8, l = s[6] | s[7] << 8;
    const unsigned char *p = 0;
    unsigned n = 0;
    if (t == 0x80 && rq == 6) {
        unsigned d = v >> 8;
        if (d == 1) p = dev_desc, n = sizeof dev_desc;
        else if (d == 2) p = cfg_desc, n = sizeof cfg_desc;
        else if (d == 6) p = qual_desc, n = sizeof qual_desc;
        else if (d == 3 && (v & 255) <= 2) p = (v & 255) ? string_desc(v & 255) : lang_desc, n = p[0];
    }
    if (p) {
        ep0_in(p, n > l ? l : n);
    } else if (t == 0 && rq == 5) {             /* SET_ADDRESS, applied after the status stage */
        addr_pending = v & 127;
        R16(CSR0) = 0x40 | 0x08;
    } else if (t == 0 && rq == 9) {             /* SET_CONFIGURATION */
        R16(CSR0) = 0x40 | 0x08;
        configured = v == 1;
        rx_n = rx_bad = 0;
        if (configured)
            ep_config();
    } else if (t == 1 && rq == 0x0b && v == 0) { /* SET_INTERFACE alt 0, the only one */
        R16(CSR0) = 0x40 | 0x08;
    } else {
        R16(CSR0) = 0x40 | 0x20; /* STALL */
    }
}

/* One bulk OUT packet: append it to the transfer; a short packet (< 512, ZLP included) ends it. */
static void rx_packet(void)
{
    unsigned c = R16(0x18);
    for (unsigned k = 0; k < c; k++) {
        unsigned char b = MUSB[0x28];
        if (rx_n < sizeof rx_buf)
            rx_buf[rx_n++] = b;
        else
            rx_bad = 1; /* longer than any valid message */
    }
    R16(0x16) = 0; /* RXPKTRDY off. Not 0x80: that is CLRDATATOG, and it makes the core drop every other packet as a resend */
    if (c == 512)
        return;
    MUSB[0x0e] = 0;
    unsigned ch;
    int len = rx_bad ? -1 : bcolink_parse(rx_buf, rx_n, &ch);
    rx_n = rx_bad = 0;
    if (len < 0)
        usblink_rx_dropped++;
    else
        usblink_rx(ch, rx_buf + BCOLINK_HDR, (unsigned)len);
}

void usblink_poll(void)
{
    unsigned bus = MUSB[0x0a], tx = R16(0x02);
    if (bus & 4) { /* bus reset: back to address 0, endpoints gone */
        MUSB[0x00] = 0;
        configured = addr_pending = 0;
        ctl_n = rx_n = rx_bad = 0;
    }
    if (addr_pending && (tx & 1)) {
        MUSB[0x00] = addr_pending;
        addr_pending = 0;
    }
    if (ctl_n && (tx & 1)) {
        unsigned m = ctl_n > 64 ? 64 : ctl_n;
        fifo_put(0x20, ctl_p, m);
        ctl_p += m, ctl_n -= m;
        R16(CSR0) = 0x02 | (ctl_n ? 0 : 0x08);
    }
    if (configured) {
        MUSB[0x0e] = 2;
        if (R16(0x16) & 1)
            rx_packet();
        MUSB[0x0e] = 0;
    }
    if (!ctl_n && (R16(CSR0) & 1))
        ep0_setup();
}

/* Wait until the bulk IN FIFO is free. busy-wait bounded by a spin count, ~1 ms on this
 * core; a host that stops reading for longer loses the message (counted), never the link. */
static int tx_wait(void)
{
    for (unsigned spin = 0; spin < 100000; spin++)
        if (!(R16(0x12) & 3)) /* TXPKTRDY or FIFONOTEMPTY */
            return 0;
    return -1;
}

int usblink_send(unsigned ch, const unsigned char *p, unsigned n)
{
    unsigned char h[BCOLINK_HDR];
    unsigned sent = 0, room = 512;
    if (!configured || ch >= BCOLINK_NCH || n > BCOLINK_MAX)
        return -1;
    bcolink_put_hdr(h, ch, n);
    MUSB[0x0e] = 1;
    if (tx_wait()) {
        MUSB[0x0e] = 0;
        usblink_tx_dropped++;
        return -1;
    }
    fifo_put(0x24, h, BCOLINK_HDR);
    room -= BCOLINK_HDR;
    while (sent < n) {
        unsigned m = n - sent < room ? n - sent : room;
        fifo_put(0x24, p + sent, m);
        sent += m, room -= m;
        if (!room) { /* a full packet: hand it over and wait for the FIFO */
            R16(0x12) = 1 | 0x2000;
            if (tx_wait()) {
                MUSB[0x0e] = 0;
                usblink_tx_dropped++; /* the host sees a truncated transfer and drops it on length */
                return -1;
            }
            room = 512;
        }
    }
    /* The last packet is short; when the message filled whole packets, room == 512 and this is the ZLP. */
    R16(0x12) = 1 | 0x2000;
    MUSB[0x0e] = 0;
        return 0;
}

/* cppcheck-suppress unusedFunction ; unused; removing it would change the BCO app image the ST20s run */
unsigned usblink_configured(void) { return configured; }
