/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* BCO network-card demultiplexer (ADR 0031). */
/* Port hooks: return 0 when the Ethernet frame was taken. Weak no-carrier defaults in netcard.c. */
int radio_tx_eth(const unsigned char *f, unsigned n);
int wired_tx_eth(const unsigned char *f, unsigned n);
/* The wired port (wired.c on hardware): bring-up with the MAC to receive for, then poll. */
int wired_init(const unsigned char mac[6]);
void wired_poll(unsigned now_ms);
/* Link hooks (usblink.h on hardware, a fake in the host test). */
int usblink_send(unsigned ch, const unsigned char *p, unsigned n);
void usblink_rx(unsigned ch, const unsigned char *p, unsigned n);
/* A port received frame f: send it to the SCM on channel ch (BCOLINK_CH_WIFI or BCOLINK_CH_WIRED). */
void netcard_from_port(unsigned ch, const unsigned char *f, unsigned n);

extern unsigned netcard_to_wifi, netcard_to_wired, netcard_to_scm, netcard_mgmt, netcard_dropped;
