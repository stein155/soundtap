/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* BCO network-card app (ADR 0031): the USB link to the SCM driver soundtap-bco, demultiplexed per channel.
 * Channel 0 -> management server, 1 -> Wi-Fi station, 2 -> wired port. No bridging, no IP service.
 * Without NETCARD_HW this file is pure logic (host test: tests/netcard_test.c). */
#include "bcolink.h"
#include "mgmt.h"
#include "netcard.h"

unsigned netcard_to_wifi, netcard_to_wired, netcard_to_scm, netcard_mgmt, netcard_dropped;

/* Radio and wired port hooks. Until a port is implemented it takes nothing: no carrier. */
__attribute__((weak)) int radio_tx_eth(const unsigned char *f, unsigned n)
{
    (void)f, (void)n;
    return -1;
}

__attribute__((weak)) int wired_tx_eth(const unsigned char *f, unsigned n)
{
    (void)f, (void)n;
    return -1;
}

static int is_eth(unsigned n)
{
    return n >= 14 && n <= 1514;
}

void usblink_rx(unsigned ch, const unsigned char *p, unsigned n)
{
    if (ch == BCOLINK_CH_WIFI)
        mgmt_taken++; /* every one, taken or dropped: the driver's credit counts them all */
    if (ch == BCOLINK_CH_CTRL) {
        static unsigned char rsp[MGMT_MAX];
        unsigned m = mgmt_handle(p, n, rsp);
        netcard_mgmt++;
        if (!m || usblink_send(BCOLINK_CH_CTRL, rsp, m))
            netcard_dropped++;
    } else if (ch == BCOLINK_CH_WIFI && is_eth(n) && !radio_tx_eth(p, n)) { /* cppcheck-suppress knownConditionTrueFalse ; the weak stub fails, wifi.c replaces it */
        netcard_to_wifi++;
    } else if (ch == BCOLINK_CH_WIRED && is_eth(n) && !wired_tx_eth(p, n)) { /* cppcheck-suppress knownConditionTrueFalse ; the weak stub fails, wired.c replaces it */
        netcard_to_wired++;
    } else {
        netcard_dropped++;
    }
}

/* A frame received by a port, towards the SCM on that port's channel. */
void netcard_from_port(unsigned ch, const unsigned char *f, unsigned n)
{
    if ((ch != BCOLINK_CH_WIFI && ch != BCOLINK_CH_WIRED) || !is_eth(n) || usblink_send(ch, f, n))
        netcard_dropped++;
    else
        netcard_to_scm++;
}

/* Flow control (mgmt.h): send the credit report that is due, once per main-loop pass. */
void netcard_flow_poll(void)
{
    static unsigned char r[MGMT_HDR + MGMT_FLOW_LEN];
    unsigned m = mgmt_flow_due(r);
    if (m && usblink_send(BCOLINK_CH_CTRL, r, m))
        mgmt_flow_lost();
}

/* Link events (mgmt.h, ADR 0135): send the one that is due, once per main-loop pass. */
void netcard_event_poll(void)
{
    static unsigned char r[MGMT_HDR + MGMT_EVENT_LEN];
    unsigned m = mgmt_event_due(r);
    if (m && usblink_send(BCOLINK_CH_CTRL, r, m))
        mgmt_event_lost();
}

#ifdef NETCARD_HW
#include "board.h"
#include "persparam.h"
#include "usblink.h"
#include "wifi.h"

extern void fault_undef(void), fault_swi(void), fault_pabort(void), fault_dabort(void);

/* NV_INFO's PersParam sector: the NOR, read through its memory map as at boot, never written. */
const volatile unsigned char *mgmt_nv(void)
{
    return PERSPARAM_NOR;
}

/* cppcheck-suppress unusedFunction ; app.S calls it */
void app_fault(unsigned kind, unsigned lr, unsigned spsr)
{
    unsigned far, fsr;
    asm volatile("mrc p15, 0, %0, c6, c0, 0" : "=r"(far));
    asm volatile("mrc p15, 0, %0, c5, c0, 0" : "=r"(fsr));
    hex("FAULT kind ", kind);
    hex("FAULT lr ", lr);
    hex("FAULT spsr ", spsr);
    hex("FAULT far ", far);
    hex("FAULT fsr ", fsr);
}

/* D-cache for SDRAM. With the MMU off the ARM926 caches no data, so every load goes to SDRAM:
 * software CCMP takes ~5 ms per 1.5 KB frame and RX stalls behind it. A flat 1:1
 * section map: the 16 MB SDRAM at 0x60000000 cacheable write-through (only the CPU touches it;
 * the core's DMA rings and buffers sit in internal SRAM), everything else uncached as before. */
#define TTB ((volatile unsigned *)0x60f40000) /* 16 KB, above the stacks (stacks.h) */
static void dcache_on(void)
{
    unsigned c;
    for (unsigned i = 0; i < 4096; i++)
        TTB[i] = i << 20 | 3u << 10 | 0x12 | ((i >> 4) == 0x60 ? 8u : 0);   /* AP rw, section, C */
    asm volatile("mcr p15, 0, %0, c7, c6, 0" : : "r"(0));       /* invalidate D-cache */
    asm volatile("mcr p15, 0, %0, c8, c7, 0" : : "r"(0));       /* invalidate TLBs */
    asm volatile("mcr p15, 0, %0, c2, c0, 0" : : "r"((unsigned)TTB));
    asm volatile("mcr p15, 0, %0, c3, c0, 0" : : "r"(3));       /* domain 0: manager */
    asm volatile("mrc p15, 0, %0, c1, c0, 0" : "=r"(c));
    asm volatile("mcr p15, 0, %0, c1, c0, 0" : : "r"(c | 1 | 4)); /* MMU, D-cache */
}

void app_main(void)
{
    int w = -4;
    UART[2] = 2 * ahb_hz() / BAUD - 2;
    low_vectors(fault_undef, fault_swi, fault_pabort, fault_dabort);
    dcache_on();
    /* USB first: its init replays stock's boot, including a WiLD core reset, so the radio
     * boots after it. The SCM's enumeration waits out the ~1 s busy wait below. */
    usblink_init();
    /* Without the device's own MAC the radio stays off: never transmit under a made-up address. */
    if (pp_wifi_mac(PERSPARAM_NOR, PERSPARAM_SIZE, mgmt_wifi_mac))
        w = wifi_init(mgmt_wifi_mac);
    hex("netcard wifi ", (unsigned)w);
    /* The SCM's eth0 sends from the Wi-Fi MAC, locally administered (soundtap_bco). */
    unsigned char eth_mac[6];
    for (unsigned i = 0; i < 6; i++) eth_mac[i] = mgmt_wifi_mac[i];
    eth_mac[0] |= 2;
    hex("netcard wired ", (unsigned)wired_init(eth_mac));
    say("netcard up\r\n");
    for (;;) {
        mgmt_uptime = usblink_ms();
        usblink_poll();
        if (!w)
            wifi_poll(mgmt_uptime);
        netcard_flow_poll();
        netcard_event_poll();
        wired_poll(mgmt_uptime);
    }
}
#endif
