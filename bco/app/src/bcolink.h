/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* BCO link protocol (ADR 0031): the one USB function between the BCO and the SCM driver soundtap_bco.
 * Every message is a little-endian u16 header followed by its payload:
 *     hdr = channel << 12 | length          (length = payload bytes, at most BCOLINK_MAX)
 * Channel 0 carries the ADR 0031 management messages, 1 the Wi-Fi station's Ethernet frames, 2 the
 * wired port's. One message per USB transfer, ended by a short packet; bytes after `length` are padding.
 * Mirrored by the Linux driver (drivers/net/usb/soundtap_bco.c); keep both in step. */
#ifndef BCOLINK_H
#define BCOLINK_H

#define BCOLINK_HDR      2
#define BCOLINK_MAX      1536u             /* payload; an Ethernet frame without FCS is at most 1514 */
#define BCOLINK_CH_CTRL  0u
#define BCOLINK_CH_WIFI  1u
#define BCOLINK_CH_WIRED 2u
#define BCOLINK_NCH      3u

/* The protocol level HELLO reports, and what the app can do (ADR 0031). Raise the level in the
 * commit that changes a message's layout or meaning; the driver refuses a level it does not speak.
 * os/tests/hardware/test_bcolink_contract.py holds the driver patch to these values. */
#define BCOLINK_PROTO        2u
#define BCOLINK_CAP_AP       0x1u          /* AP_START/AP_STOP/AP_STATUS */
#define BCOLINK_CAP_REPLAY   0x2u          /* a resent state-changing request gets the cached reply */
/* Additions a driver may ignore keep the level and add a capability bit: an older driver never asks
 * for them and an older app never announces them (ADR 0031). */
#define BCOLINK_CAP_FLOW     0x4u          /* FLOW: credit reports of the free Wi-Fi transmit buffers */
#define BCOLINK_CAP_STATS    0x8u          /* LINK_STATS: the transmit and link counters */
/* Standard firmware features (ADR 0135), each one off until the driver uses it. */
#define BCOLINK_CAP_EVENTS   0x10u         /* EVENTS: link events (deauth, disassoc, beacon loss, roamed) */
#define BCOLINK_CAP_ROAM     0x20u         /* SET_PROFILE's BSSID and roaming tail; roaming only when asked */
#define BCOLINK_CAP_REGDOM   0x40u         /* SET_REGDOM: country, channel masks, TX power limit */
#define BCOLINK_CAP_SCAN_SSID 0x80u        /* SCAN {1, ssid_len, ssid}: the directed probe's SSID */
#define BCOLINK_CAP_NVINFO   0x100u        /* NV_INFO: read-only PersParam key list (bench only) */
#define BCOLINK_CAP_APDIAG   0x200u        /* AP_STATUS's tail: setup-AP diagnostics and per-client counters */
/* Every network security stock joined (ADR 0136). */
#define BCOLINK_CAP_SECURITY 0x400u        /* SET_PROFILE security 0 (open), 2 (WEP); SCAN's security detail */
#define BCOLINK_CAP_WPA3     0x800u        /* SAE through the SCM (EXT_AUTH, AUTH_FRAME, SET_PMK), PSK-SHA256, PMF */
#define BCOLINK_CAP_APBEACON 0x1000u       /* AP_BEACON: hostapd's beacon updates while the access point runs */
#define BCOLINK_CAPS (BCOLINK_CAP_AP | BCOLINK_CAP_REPLAY | BCOLINK_CAP_FLOW | BCOLINK_CAP_STATS | \
                      BCOLINK_CAP_EVENTS | BCOLINK_CAP_ROAM | BCOLINK_CAP_REGDOM | BCOLINK_CAP_SCAN_SSID | \
                      BCOLINK_CAP_NVINFO | BCOLINK_CAP_APDIAG | BCOLINK_CAP_SECURITY | BCOLINK_CAP_WPA3 | \
                      BCOLINK_CAP_APBEACON)

#define BCOLINK_USB_VID  0x1209u           /* pid.codes; 0x0001 is its test PID: allocate one before release */
#define BCOLINK_USB_PID  0x0001u
#define BCOLINK_USB_CLASS    0xffu         /* vendor specific */
#define BCOLINK_USB_SUBCLASS 0x52u         /* 'R' */
#define BCOLINK_USB_PROTO    1u

static inline void bcolink_put_hdr(unsigned char *p, unsigned ch, unsigned len)
{
    unsigned h = ch << 12 | len;
    p[0] = h & 0xff, p[1] = h >> 8;
}

/* Parse one received transfer. Returns the payload length and sets *ch, or -1 when the transfer is
 * malformed (short, unknown channel, length past the end or above BCOLINK_MAX). */
static inline int bcolink_parse(const unsigned char *p, unsigned n, unsigned *ch)
{
    if (n < BCOLINK_HDR)
        return -1;
    unsigned h = p[0] | p[1] << 8, len = h & 0xfff;
    *ch = h >> 12;
    if (*ch >= BCOLINK_NCH || len > BCOLINK_MAX || len > n - BCOLINK_HDR)
        return -1;
    return (int)len;
}
#endif
