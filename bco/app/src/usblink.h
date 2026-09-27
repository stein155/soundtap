/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* MUSB device side of the BCO link (ADR 0031): one vendor interface, bulk IN/OUT, polled.
 * Messages are framed by bcolink.h. Hardware proof of the MUSB bring-up: ADR 0031. */
void usblink_init(void);                     /* stock clock/PHY bring-up, then connect */
void usblink_poll(void);                     /* call often (<= ~1 ms): EP0 and bulk OUT */
int usblink_send(unsigned ch, const unsigned char *p, unsigned n); /* 0 sent, -1 dropped */
unsigned usblink_configured(void);
/* Milliseconds from the host's 1 ms USB frames (SOF): the card's clock. Stands still while
 * the bus is suspended or unplugged, which Linux never does to a bound non-hub device. */
unsigned usblink_ms(void);
extern void usblink_rx(unsigned ch, const unsigned char *p, unsigned n); /* the app: one message */
extern unsigned usblink_rx_dropped, usblink_tx_dropped;
