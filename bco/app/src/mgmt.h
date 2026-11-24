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

/* Flow control (BCOLINK_CAP_FLOW, ADR 0031). FLOW (13) switches credit reports on (payload 1) or
 * off (0); its reply, and every report the app sends unasked (type 13 | 0x80, seq 0), is
 *     status 0, free, total, on, le32 taken
 * free = Wi-Fi transmit buffers the SCM may fill now, total = the buffers there are, on = reports
 * are on, taken = channel-1 messages the app has taken off the link since it started (accepted or
 * dropped). The driver may have (its channel-1 messages sent) - taken < free in flight. */
#define MGMT_FLOW_LEN 8
#define MGMT_FLOW_HEARTBEAT_MS 100u /* a report at least this often while reports are on */
extern unsigned mgmt_flow;  /* reports are on */
extern unsigned mgmt_taken; /* channel-1 messages taken off the link (netcard.c counts them) */
/* The report due now, as a whole message in r (MGMT_HDR + MGMT_FLOW_LEN bytes), or 0: buffers came
 * free that the driver cannot know of yet, or the heartbeat is due. mgmt_flow_lost() after a report
 * the link could not take, so the next call sends one again. */
unsigned mgmt_flow_due(unsigned char *r);
void mgmt_flow_lost(void);
/* LINK_STATS (14, BCOLINK_CAP_STATS): status, then MGMT_STATS_N le32 counters since the app started:
 * Wi-Fi frames acknowledged, failed on air after the retries, dropped before the air (no free
 * transmit buffer, not joined), messages the link dropped (netcard_dropped), and frames from the SCM
 * the radio took (netcard_to_wifi). */
#define MGMT_STATS_N 5
