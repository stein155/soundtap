/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Host test of the BCO link framing (bcolink.h) and the per-channel demultiplexer (netcard.c). */
#include <assert.h>
#include <string.h>
#include <stdio.h>
#include "bcolink.h"
#include "mgmt.h"
#include "netcard.h"

static unsigned sent_ch, sent_n, radio_n, link_busy;
static unsigned char sent[BCOLINK_MAX];
int usblink_send(unsigned ch, const unsigned char *p, unsigned n)
{
    if (link_busy)
        return -1;
    sent_ch = ch, sent_n = n, memcpy(sent, p, n);
    return 0;
}
int radio_tx_eth(const unsigned char *f, unsigned n) { (void)f; radio_n = n; return 0; }

int main(void)
{
    unsigned char m[BCOLINK_HDR + BCOLINK_MAX + 4], f[1514] = {0};
    unsigned ch;

    /* framing: header round trip, padding allowed, malformed refused */
    bcolink_put_hdr(m, BCOLINK_CH_WIRED, 1514);
    assert(bcolink_parse(m, BCOLINK_HDR + 1514, &ch) == 1514 && ch == BCOLINK_CH_WIRED);
    assert(bcolink_parse(m, BCOLINK_HDR + 1515, &ch) == 1514);       /* trailing pad byte */
    assert(bcolink_parse(m, BCOLINK_HDR + 1513, &ch) == -1);         /* length past the end */
    bcolink_put_hdr(m, 3, 10);
    assert(bcolink_parse(m, 12, &ch) == -1);                          /* unknown channel */
    bcolink_put_hdr(m, BCOLINK_CH_WIFI, BCOLINK_MAX + 1);
    assert(bcolink_parse(m, sizeof m, &ch) == -1);                    /* above the maximum */
    assert(bcolink_parse(m, 1, &ch) == -1);
    bcolink_put_hdr(m, BCOLINK_CH_WIFI, 0x123);
    assert(m[0] == 0x23 && m[1] == 0x11);                             /* little endian, channel on top */

    /* demux */
    usblink_rx(BCOLINK_CH_WIFI, f, 60);
    assert(radio_n == 60 && netcard_to_wifi == 1);
    usblink_rx(BCOLINK_CH_WIRED, f, 60);                               /* no wired port yet: no carrier */
    assert(netcard_to_wired == 0 && netcard_dropped == 1);
    usblink_rx(BCOLINK_CH_WIFI, f, 13);                                /* runt */
    usblink_rx(BCOLINK_CH_WIFI, f, 1515);                              /* giant */
    assert(netcard_to_wifi == 1 && netcard_dropped == 3);
    unsigned char hello[MGMT_HDR] = {1, 1, 0, 9, 0, 0};
    usblink_rx(BCOLINK_CH_CTRL, hello, sizeof hello);                  /* management answers on channel 0 */
    assert(netcard_mgmt == 1 && sent_ch == BCOLINK_CH_CTRL && sent[1] == 0x81 && sent[3] == 9);
    usblink_rx(BCOLINK_CH_CTRL, f, 3);                                 /* malformed request: no reply */
    assert(netcard_dropped == 4);

    /* ports towards the SCM */
    netcard_from_port(BCOLINK_CH_WIFI, f, 1514);
    assert(sent_ch == BCOLINK_CH_WIFI && sent_n == 1514 && netcard_to_scm == 1);
    netcard_from_port(BCOLINK_CH_CTRL, f, 60);                         /* a port never speaks control */
    link_busy = 1;
    netcard_from_port(BCOLINK_CH_WIRED, f, 60);                        /* link busy: counted, not lost silently */
    assert(netcard_to_scm == 1 && netcard_dropped == 6);
    /* every channel-1 message counts for the driver's credit, taken or dropped */
    assert(mgmt_taken == 3);

    /* flow control: a due report goes out on channel 0; one the link refuses is sent again */
    extern unsigned fake_tx_free;
    link_busy = 0, sent_n = 0;
    netcard_flow_poll();
    assert(sent_n == 0);                                               /* reports are off */
    unsigned char on[MGMT_HDR + 1] = {1, 13, 0, 10, 0, 1, 1};
    usblink_rx(BCOLINK_CH_CTRL, on, sizeof on);                        /* FLOW on */
    assert(sent_ch == BCOLINK_CH_CTRL && sent[1] == (13 | 0x80) && sent[3] == 10 && mgmt_flow);
    sent_n = 0;
    netcard_flow_poll();
    assert(sent_n == 0);                                               /* the reply told it all */
    usblink_rx(BCOLINK_CH_WIFI, f, 60);                                /* one frame, one buffer */
    fake_tx_free--;
    netcard_flow_poll();
    assert(sent_n == 0);
    fake_tx_free++;                                                    /* its buffer came back */
    link_busy = 1;
    netcard_flow_poll();
    link_busy = 0;
    netcard_flow_poll();
    assert(sent_ch == BCOLINK_CH_CTRL && sent_n == MGMT_HDR + MGMT_FLOW_LEN && sent[1] == (13 | 0x80) &&
           sent[2] == 0 && sent[3] == 0 && sent[MGMT_HDR + 1] == fake_tx_free && sent[MGMT_HDR + 4] == 4);
    puts("netcard ok");
    return 0;
}
