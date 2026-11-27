/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include "bcolink.h"
#include "mgmt.h"
#include "netcard.h"
#include "persparam.h"
#include "wifi.h"

_Static_assert(MGMT_EV_DEAUTH == WIFI_EV_DEAUTH && MGMT_EV_DISASSOC == WIFI_EV_DISASSOC &&
               MGMT_EV_BEACON_LOSS == WIFI_EV_BEACON_LOSS && MGMT_EV_TX_WEDGE == WIFI_EV_TX_WEDGE &&
               MGMT_EV_ROAMED == WIFI_EV_ROAMED && MGMT_EV_ROAM_FAILED == WIFI_EV_ROAM_FAILED,
               "the link's event numbers are the station's");
_Static_assert(MGMT_PROFILE_BSSID_FIXED == WIFI_BSSID_FIXED && MGMT_PROFILE_BSSID_HINT == WIFI_BSSID_HINT &&
               MGMT_PROFILE_ROAM == WIFI_ROAM, "SET_PROFILE's flags are the station's");

/* The management server on top of the station in wifi.c (host tests: a fake wifi). */
unsigned char mgmt_wifi_mac[6];
volatile unsigned mgmt_uptime;

_Static_assert(MGMT_AP_COUNTERS == WIFI_AP_COUNTERS && MGMT_AP_DIAG == WIFI_AP_DIAG &&
               MGMT_AP_CLIENT == 4 + 5 * 4 && MGMT_AP_EXTRA == WIFI_AP_EXTRA,
               "AP_STATUS's layout (mgmt.h) is what case 8 writes");

static unsigned char *put32(unsigned char *o, unsigned v)
{
    for (unsigned i = 0; i < 32; i += 8)
        *o++ = v >> i;
    return o;
}

/* ---- flow control (MGMT_FLOW_LEN in mgmt.h) ---- */
unsigned mgmt_flow, mgmt_taken;
static unsigned rep_free, rep_taken, rep_ms; /* what the last report told the driver, and when */

static unsigned char *put_flow(unsigned char *o)
{
    unsigned f = wifi_tx_free();
    *o++ = 0;
    *o++ = f;
    *o++ = WIFI_TX_BUFS;
    *o++ = mgmt_flow;
    o = put32(o, mgmt_taken);
    rep_free = f, rep_taken = mgmt_taken, rep_ms = mgmt_uptime;
    return o;
}

unsigned mgmt_flow_due(unsigned char *r)
{
    if (!mgmt_flow)
        return 0;
    /* What the driver may still send as far as the app can tell: the last report's free buffers,
     * less what arrived since. Its frames still in flight only make that smaller, and once they are
     * all taken the app's count equals the driver's, so a freed buffer is always reported. */
    unsigned seen = mgmt_taken - rep_taken, known = rep_free > seen ? rep_free - seen : 0;
    if (wifi_tx_free() <= known && mgmt_uptime - rep_ms < MGMT_FLOW_HEARTBEAT_MS)
        return 0;
    put_flow(r + MGMT_HDR);
    r[0] = 1, r[1] = 13 | 0x80, r[2] = 0, r[3] = 0, r[4] = 0, r[5] = MGMT_FLOW_LEN;
    return MGMT_HDR + MGMT_FLOW_LEN;
}

void mgmt_flow_lost(void)
{
    rep_free = 0, rep_ms = mgmt_uptime - MGMT_FLOW_HEARTBEAT_MS;
}

/* ---- link events (MGMT_EVENT_LEN in mgmt.h) ---- */
unsigned mgmt_events;
static struct wifi_event ev;
static unsigned ev_held; /* ev is due again: the link could not take it */

unsigned mgmt_event_due(unsigned char *r)
{
    unsigned char *o = r + MGMT_HDR;
    if (!mgmt_events || (!ev_held && !wifi_event_next(&ev)))
        return 0;
    ev_held = 0;
    *o++ = 0, *o++ = ev.type, *o++ = (unsigned char)ev.reason, *o++ = ev.reason >> 8;
    for (unsigned i = 0; i < 6; i++)
        *o++ = ev.bssid[i];
    *o++ = ev.chan, *o++ = (unsigned char)ev.rssi;
    r[0] = 1, r[1] = 15 | 0x80, r[2] = 0, r[3] = 0, r[4] = 0, r[5] = MGMT_EVENT_LEN;
    return MGMT_HDR + MGMT_EVENT_LEN;
}

void mgmt_event_lost(void)
{
    ev_held = 1;
}

/* The PersParam sector NV_INFO reads (netcard.c maps the NOR on hardware); the weak default has
 * none. */
__attribute__((weak)) const volatile unsigned char *mgmt_nv(void)
{
    return 0;
}

static unsigned char *put_bss(unsigned char *o, const struct wifi_bss *b)
{
    *o++ = b->ssid_len;
    for (unsigned i = 0; i < b->ssid_len; i++) *o++ = b->ssid[i];
    for (unsigned i = 0; i < 6; i++) *o++ = b->bssid[i];
    *o++ = b->chan;
    *o++ = (unsigned char)b->rssi;
    return o;
}

/* The wired port's register and PHY readout (wired.c on hardware). The weak default says the
 * build has no wired port: -1, no bytes. */
__attribute__((weak)) int wired_probe(unsigned char *o)
{
    (void)o;
    return -1;
}

/* The driver resends a request whose reply it missed, with the same seq. Re-running SET_PROFILE or
 * AP_START would join or start twice, so the last state-changing reply is kept and resent instead.
 * Queries simply run again. Only the reply is kept, never the request: SET_PROFILE carries the PMK. */
static unsigned char last_rsp[16];
static unsigned last_len, last_key; /* type << 16 | seq; 0 = nothing kept */

static int changes_state(unsigned type)
{
    return type == 3 || type == 4 || type == 6 || type == 7 || type == 15;
}

unsigned mgmt_handle(const unsigned char *p, unsigned n, unsigned char *r)
{
    if (n < MGMT_HDR)
        return 0;
    unsigned len = p[4] << 8 | p[5];
    if (p[0] != 1 || MGMT_HDR + len > n)
        return 0;
    unsigned key = (unsigned)p[1] << 16 | p[2] << 8 | p[3];
    if (changes_state(p[1]) && key == last_key) {
        for (unsigned i = 0; i < last_len; i++) r[i] = last_rsp[i];
        return last_len;
    }
    unsigned char *o = r + MGMT_HDR; /* reply payload; header filled at the end */
    unsigned char type = p[1], st = 0;
    const unsigned char *a = p + MGMT_HDR;
    switch (type) {
    case 1: /* HELLO: status, ver_len, ver, wifi mac, state, uptime, proto, caps */
        *o++ = 0;
#ifdef WILD_OWN
        { /* "link-2 wild=T:DDDDDDDD": our own core's ready type and descriptor table */
            extern unsigned wild_own_type, wild_own_desc;
            static const char hx[] = "0123456789abcdef";
            *o++ = 22;
            for (const char *v = "link-2 wild="; *v;) *o++ = *v++;
            *o++ = hx[wild_own_type & 15];
            *o++ = ':';
            for (int i = 28; i >= 0; i -= 4) *o++ = hx[wild_own_desc >> i & 15];
        }
#else
        *o++ = 6;
        for (const char *v = "link-2"; *v;) *o++ = *v++;
#endif
        for (unsigned i = 0; i < 6; i++) *o++ = mgmt_wifi_mac[i];
        *o++ = wifi_state();
        for (unsigned i = 0; i < 32; i += 8) /* uptime in ms, le32 */
            *o++ = mgmt_uptime >> i;
        *o++ = BCOLINK_PROTO;
        o = put32(o, BCOLINK_CAPS); /* capabilities, le32 */
        break;
    case 2: { /* SCAN [start [ssid_len ssid]]: status (3 = still scanning), count, entries */
        int start = len >= 1 && a[0] == 1, found;
        if (start) /* the directed probe's SSID (BCOLINK_CAP_SCAN_SSID); none leaves the profile's */
            wifi_scan_ssid(a + 2, len >= 2 && a[1] <= 32 && len >= 2u + a[1] ? a[1] : 0);
        found = wifi_scan(start);
        struct wifi_bss b;
        *o++ = found < 0 ? 3 : 0;
        unsigned char *cnt = o++;
        *cnt = 0;
        /* An entry is up to 43 bytes (put_bss, then sec) and the table holds 40, more than the reply
         * buffer (MGMT_MAX) takes: a beacon flood from anyone in range fills the table. The list
         * stops before the first entry that no longer fits; the count says how many went in. */
        for (int i = 0; i < found && *cnt < 255 && wifi_bss(i, &b) == 0; i++) {
            if ((unsigned)(o - r) + 1u + b.ssid_len + 6 + 1 + 1 + 1 > MGMT_MAX)
                break;
            o = put_bss(o, &b);
            *o++ = b.sec;
            ++*cnt;
        }
        break;
    }
    case 3: { /* SET_PROFILE: ssid_len, ssid, security, pmk_len, pmk, 32-byte seed [, flags, bssid] */
        unsigned base = len >= 1 && len >= 1u + a[0] + 2 ? 1u + a[0] + 2 + a[1 + a[0] + 1] + 32 : 0;
        if (len < 1 || a[0] < 1 || a[0] > 32 || !base || (len != base && len != base + MGMT_PROFILE_TAIL))
            st = 1;
        else if (a[1 + a[0]] != 1 || a[1 + a[0] + 1] != 32)
            st = 2; /* WPA2-PSK with a PMK only */
        else { /* without the tail (an older driver): any BSSID, no roaming */
            const unsigned char *tail = len > base ? a + base : 0; /* cppcheck-suppress knownConditionTrueFalse ; len is base without the tail */
            st = wifi_join(a + 1, a[0], a + 1 + a[0] + 2, a + 1 + a[0] + 2 + 32, tail ? tail[0] : 0,
                           tail ? tail + 1 : 0) ? 1 : 0;
        }
        *o++ = st;
        break;
    }
    case 4: /* FORGET: drop the association and the profile. The core keeps running: a reboot would
             * recalibrate the radio in busy waits that stall USB, and wifi.c already reboots a
             * core whose transmitter wedged. */
        wifi_leave();
        *o++ = 0;
        break;
    case 5: { /* STATUS: status, state, ssid_len, ssid, bssid, channel, rssi, then why the last
               * join failed: step (WIFI_FAIL_*), le16 IEEE status or reason code. The driver reads
               * the failure only when the reply is long enough, so an older driver is unaffected.
               * Last, the BSS's RSN group cipher suite type (2 TKIP, 4 CCMP, 0 unknown): with TKIP
               * the core drops the router's broadcasts (wifi.c install_keys), which the SCM logs.
               * Again read only when present. */
        struct wifi_bss b;
        unsigned step, code;
        *o++ = 0; *o++ = wifi_state();
        wifi_current(&b);
        o = put_bss(o, &b);
        wifi_fail(&step, &code);
        *o++ = step;
        *o++ = code, *o++ = code >> 8;
        *o++ = b.grp;
        break;
    }
    case 6: /* AP_START: channel, beacon interval (TU, network order), beacon body */
        *o++ = len < 3 || wifi_ap_start(a[0], a[1] << 8 | a[2], a + 3, len - 3) ? 1 : 0;
        break;
    case 7: /* AP_STOP */
        wifi_ap_stop();
        *o++ = 0;
        break;
    case 8: { /* AP_STATUS: status, count, client MACs, counters, diagnostics, per client (mgmt.h) */
        struct wifi_ap_client cl[WIFI_AP_CLIENTS];
        unsigned c = wifi_ap_client_info(cl), k[WIFI_AP_COUNTERS], d[WIFI_AP_DIAG];
        *o++ = 0; *o++ = c;
        for (unsigned i = 0; i < c * 6; i++) *o++ = cl[i / 6].mac[i % 6];
        wifi_ap_counters(k);
        for (unsigned i = 0; i < WIFI_AP_COUNTERS; i++) o = put32(o, k[i]);
        wifi_ap_diag(d);
        for (unsigned i = 0; i < WIFI_AP_DIAG; i++) o = put32(o, d[i]);
        for (unsigned i = 0; i < c; i++) {
            const unsigned v[5] = {cl[i].idle_ms, cl[i].rx_data, cl[i].rx_null, cl[i].tx_ok, cl[i].tx_failed};
            *o++ = cl[i].dozing, *o++ = cl[i].held, *o++ = (unsigned char)cl[i].rssi, *o++ = cl[i].rate;
            for (unsigned j = 0; j < 5; j++) o = put32(o, v[j]);
        }
        unsigned x[WIFI_AP_EXTRA];
        wifi_ap_extra(x);
        for (unsigned i = 0; i < WIFI_AP_EXTRA; i++) o = put32(o, x[i]);
        break;
    }
    case 13: /* FLOW: on (1) or off (0); the reply is a credit report (mgmt.h) */
        if (len != 1 || a[0] > 1) {
            *o++ = 1;
            break;
        }
        mgmt_flow = a[0];
        o = put_flow(o);
        break;
    case 14: { /* LINK_STATS: status, MGMT_STATS_N le32 counters (mgmt.h) */
        unsigned k[3];
        wifi_tx_counters(k);
        *o++ = 0;
        o = put32(put32(put32(o, k[0]), k[1]), k[2]);
        o = put32(put32(o, netcard_dropped), netcard_to_wifi);
        break;
    }
    case 15: /* EVENTS: on (1) or off (0); reply status, on. Turning them on drops older events. */
        if (len != 1 || a[0] > 1) {
            *o++ = 1;
            break;
        }
        if (a[0] && !mgmt_events)
            for (ev_held = 0; wifi_event_next(&ev);) {}
        mgmt_events = a[0];
        *o++ = 0, *o++ = (unsigned char)mgmt_events;
        break;
    case 16: { /* SET_REGDOM: country[2], le16 channels, le16 passive, i8 dBm; reply status, enforced */
        int res = len != MGMT_REGDOM_LEN ? -1 :
                  wifi_set_regdom(a, a[2] | a[3] << 8, a[4] | a[5] << 8, (signed char)a[6]);
        *o++ = res < 0, *o++ = res > 0;
        break;
    }
    case 17: { /* NV_INFO: status, count, entries (mgmt.h); read-only */
        static uint32_t keys[MGMT_NV_MAX];
        static unsigned lens[MGMT_NV_MAX];
        const volatile unsigned char *nv = mgmt_nv();
        unsigned count = nv ? pp_keys(nv, PERSPARAM_SIZE, keys, lens, MGMT_NV_MAX) : 0;
        *o++ = !nv, *o++ = (unsigned char)count;
        for (unsigned i = 0; i < count; i++) {
            uint8_t v[4] = {0, 0, 0, 0};
            if (lens[i] <= sizeof v) /* never a longer value: no passphrase or PMK leaves the BCO */
                pp_get(nv, PERSPARAM_SIZE, keys[i], v, sizeof v);
            o = put32(o, keys[i]);
            *o++ = (unsigned char)lens[i], *o++ = (unsigned char)(lens[i] >> 8);
            for (unsigned k = 0; k < sizeof v; k++)
                *o++ = v[k];
        }
        break;
    }
    case 12: { /* WIRED_PROBE: status (1 = no wired port in this build), then wired_probe's bytes */
        int w = wired_probe(o + 1);
        *o = w < 0;               /* cppcheck-suppress knownConditionTrueFalse ; the weak stub fails, wired.c replaces it */
        o += 1 + (w > 0 ? w : 0); /* cppcheck-suppress knownConditionTrueFalse ; the same stub */
        break;
    }
#ifdef WILD_OWN
    case 9: /* WILD_WRITE: be32 offset, bytes into the runtime core-image staging area */
        *o++ = len < 4 || wifi_wild_write((unsigned)a[0] << 24 | a[1] << 16 | a[2] << 8 | a[3],
                                          a + 4, len - 4) ? 1 : 0;
        break;
    case 10: /* WILD_BOOT: be32 length, be32 crc32; boot the staged image (falls back if dead) */
        *o++ = len != 8 || wifi_wild_boot((unsigned)a[0] << 24 | a[1] << 16 | a[2] << 8 | a[3],
                                          (unsigned)a[4] << 24 | a[5] << 16 | a[6] << 8 | a[7]) ? 1 : 0;
        break;
    case 11: /* WILD_PEEK: be32 core offset, be16 length; words go to the UART */
        *o++ = len != 6 || wifi_wild_peek((unsigned)a[0] << 24 | a[1] << 16 | a[2] << 8 | a[3],
                                          a[4] << 8 | a[5]) ? 1 : 0;
        break;
#endif
    default:
        *o++ = 0xff;
        break;
    }
    unsigned plen = o - (r + MGMT_HDR);
    r[0] = 1; r[1] = type | 0x80; r[2] = p[2]; r[3] = p[3]; r[4] = plen >> 8; r[5] = plen;
    if (changes_state(type) && MGMT_HDR + plen <= sizeof last_rsp) {
        for (unsigned i = 0; i < MGMT_HDR + plen; i++) last_rsp[i] = r[i];
        last_len = MGMT_HDR + plen, last_key = key;
    }
    return MGMT_HDR + plen;
}
