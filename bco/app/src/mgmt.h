/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Management messages, carried on BCO link channel 0 (ADR 0031). Pure request -> reply.
 * Message: u8 ver=1, u8 type, u16 seq, u16 len (network order), payload. Replies: type | 0x80, same seq. */
#define MGMT_HDR 6
#define MGMT_MAX 1536

/* req/rsp: whole messages. Returns the reply length, 0 when the request is malformed. */
unsigned mgmt_handle(const unsigned char *req, unsigned n, unsigned char *rsp);
extern unsigned char mgmt_wifi_mac[6]; /* the device's Wi-Fi MAC (PersParam MAC0), reported in HELLO */
extern volatile unsigned mgmt_uptime; /* ms since boot, set by the main loop, reported in HELLO */
