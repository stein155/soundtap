/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Management messages, carried on BCO link channel 0 (ADR 0031). Pure request -> reply.
 * Message: u8 ver=1, u8 type, u16 seq, u16 len (network order), payload. Replies: type | 0x80, same seq. */
#define MGMT_HDR 6
#define MGMT_MAX 1536

/* req/rsp: whole messages. Returns the reply length, 0 when the request is malformed. */
unsigned mgmt_handle(const unsigned char *p, unsigned n, unsigned char *r);
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

/* Link events (BCOLINK_CAP_EVENTS, ADR 0135). EVENTS (15) switches them on (payload 1) or off (0);
 * its reply is status, on (MGMT_EVENTS_REPLY_LEN bytes). While on, the app sends every event of
 * the station unasked as type 15 | 0x80, seq 0:
 *     status 0, event, le16 reason, bssid[6], channel, rssi (i8)
 * event: MGMT_EV_*; reason: the 802.11 reason code of a deauthentication or disassociation, else 0;
 * bssid, channel and rssi: the access point lost, or the one roamed to. Turning events on drops
 * what the station queued before. */
#define MGMT_EVENT_LEN 12
#define MGMT_EVENTS_REPLY_LEN 2
#define MGMT_EV_DEAUTH 1u      /* the access point deauthenticated us */
#define MGMT_EV_DISASSOC 2u    /* the access point disassociated us */
#define MGMT_EV_BEACON_LOSS 3u /* no beacon from the access point for WIFI_BEACON_LOSS_MS */
#define MGMT_EV_TX_WEDGE 4u    /* the transmitter wedged; the core was rebooted */
#define MGMT_EV_ROAMED 5u      /* now joined to another access point of the same network */
#define MGMT_EV_ROAM_FAILED 6u /* a roam did not complete: the link is down */
extern unsigned mgmt_events; /* events are on */
/* The event due now, as a whole message in r (MGMT_HDR + MGMT_EVENT_LEN bytes), or 0.
 * mgmt_event_lost() after an event the link could not take, so the next call sends it again. */
unsigned mgmt_event_due(unsigned char *r);
void mgmt_event_lost(void);

/* SCAN's flags (payload byte 0): 1 starts a scan; 2 (BCOLINK_CAP_SECURITY) asks for entries with
 * their security detail, MGMT_SCAN_DETAIL_LEN bytes after the security byte: RSN group cipher
 * suite type, RSN pairwise set, RSN AKM set, RSN capabilities' low byte, WPA IE group, WPA IE
 * pairwise set (wifi.h WIFI_SET_*). STATUS carries the same bytes after its security byte. */
#define MGMT_SCAN_START 0x1u
#define MGMT_SCAN_DETAIL 0x2u
#define MGMT_SCAN_DETAIL_LEN 6

/* SET_PROFILE's tail (BCOLINK_CAP_ROAM): after the 32-byte seed, flags and bssid[6]. Without the
 * tail the app joins any BSSID of the SSID and never roams, as an app before ADR 0135 did. */
#define MGMT_PROFILE_TAIL 7
#define MGMT_PROFILE_BSSID_FIXED 0x1u /* join this BSSID only (cfg80211 connect's bssid) */
#define MGMT_PROFILE_BSSID_HINT 0x2u  /* prefer this BSSID for the first join (bssid_hint) */
#define MGMT_PROFILE_ROAM 0x4u        /* roam within the SSID (ignored with BSSID_FIXED) */

/* SET_REGDOM (16, BCOLINK_CAP_REGDOM): country[2], le16 channels, le16 passive, i8 max TX power
 * in dBm. Bit n-1 of channels allows channel n (1..13); a passive channel is only listened on,
 * never probed and never used for the access point. Reply: status, enforced (1 when the radio
 * holds the TX power limit; 0: the limit is recorded, the core has no TX power control yet). */
#define MGMT_REGDOM_LEN 7

/* NV_INFO (17, BCOLINK_CAP_NVINFO): read-only. Reply: status (1 = no PersParam sector), count, then
 * count entries of key (FourCC as stored, le32), le16 value length, and the value's first 4 bytes
 * when the value is at most 4 bytes long (else zeros): no longer value, and so no passphrase or
 * PMK, ever leaves the BCO. For the bench check of the antenna setting (ADR 0135). */
#define MGMT_NV_ENTRY 10
#define MGMT_NV_MAX 64
const volatile unsigned char *mgmt_nv(void); /* the PersParam sector, 0 when the build has none */
