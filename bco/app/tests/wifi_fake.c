/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Fake station for the host tests of mgmt.c: records what the management server asked for. */
#include <string.h>
#include "wifi.h"

unsigned fake_state, fake_scanning, fake_nbss, fake_joins, fake_leaves;
unsigned fake_ssid_max; /* 1: every scan entry has a 32-byte SSID, the longest a reply carries */
unsigned char fake_ssid[32], fake_pmk[32], fake_seed[32];
unsigned fake_ssid_len;

unsigned wifi_state(void) { return fake_state; }
int wifi_scan(int start)
{
    if (start && !fake_scanning && fake_state != WIFI_UP)
        fake_scanning = 1;
    return fake_scanning ? -1 : (int)fake_nbss;
}
int wifi_bss(unsigned i, struct wifi_bss *o)
{
    if (i >= fake_nbss)
        return -1;
    memset(o, 0, sizeof *o);
    o->ssid_len = 3, memcpy(o->ssid, "ap0", 3), o->ssid[2] += i;
    if (fake_ssid_max)
        o->ssid_len = 32, memset(o->ssid + 3, 'x', 29);
    memcpy(o->bssid, "\x02\x00\x00\x00\x00\x10", 6), o->bssid[5] += i;
    o->chan = 1 + i, o->rssi = -40 - (int)i, o->sec = 1;
    return 0;
}
unsigned fake_join_flags;
unsigned char fake_join_bssid[6];
int wifi_join(const unsigned char *ssid, unsigned ssid_len, const unsigned char k[32],
              const unsigned char s[32], unsigned flags, const unsigned char bssid[6])
{
    fake_join_flags = bssid ? flags : 0xff; /* 0xff: no tail */
    if (bssid)
        memcpy(fake_join_bssid, bssid, 6);
    memcpy(fake_ssid, ssid, ssid_len), fake_ssid_len = ssid_len;
    memcpy(fake_pmk, k, 32), memcpy(fake_seed, s, 32);
    fake_joins++, fake_state = WIFI_JOINING;
    return 0;
}
void wifi_leave(void) { fake_leaves++, fake_state = WIFI_IDLE, fake_ssid_len = 0; }
unsigned fake_resets;
unsigned fake_grp; /* the group cipher wifi_current reports */
int wifi_reset_core(void) { fake_resets++, fake_state = WIFI_IDLE; return 0; }
int wifi_current(struct wifi_bss *o)
{
    memset(o, 0, sizeof *o);
    if (!fake_ssid_len)
        return 0;
    o->ssid_len = fake_ssid_len, memcpy(o->ssid, fake_ssid, fake_ssid_len);
    memcpy(o->bssid, "\x02\x00\x00\x00\x00\x10", 6), o->chan = 6, o->rssi = -50;
    o->grp = (unsigned char)fake_grp;
    return 1;
}

unsigned fake_fail_step, fake_fail_code;
void wifi_fail(unsigned *step, unsigned *code) { *step = fake_fail_step, *code = fake_fail_code; }

unsigned fake_ap_chan, fake_ap_bint, fake_ap_len, fake_ap_stops;
int wifi_ap_start(unsigned chan, unsigned bint, const unsigned char *body, unsigned len)
{
    if (chan < 1 || chan > 13 || len < 12 || len > WIFI_AP_BODY_MAX)
        return -1;
    fake_ap_chan = chan, fake_ap_bint = bint, fake_ap_len = len, fake_state = WIFI_AP;
    return 0;
}
void wifi_ap_stop(void) { fake_ap_stops++, fake_state = WIFI_IDLE; }
unsigned wifi_ap_clients(unsigned char macs[][6])
{
    if (fake_state != WIFI_AP)
        return 0;
    memcpy(macs[0], "\x02\x00\x00\x00\x00\x77", 6);
    return 1;
}
void wifi_ap_counters(unsigned c[WIFI_AP_COUNTERS])
{
    for (unsigned i = 0; i < WIFI_AP_COUNTERS; i++)
        c[i] = 0x11 * (i + 1);
}
void wifi_ap_diag(unsigned d[WIFI_AP_DIAG])
{
    for (unsigned i = 0; i < WIFI_AP_DIAG; i++)
        d[i] = 0x1000 + i;
}
void wifi_ap_extra(unsigned d[WIFI_AP_EXTRA])
{
    for (unsigned i = 0; i < WIFI_AP_EXTRA; i++)
        d[i] = 0x2000 + i;
}
unsigned wifi_ap_client_info(struct wifi_ap_client o[WIFI_AP_CLIENTS])
{
    if (fake_state != WIFI_AP)
        return 0;
    memcpy(o[0].mac, "\x02\x00\x00\x00\x00\x77", 6);
    o[0].dozing = 1, o[0].held = 2, o[0].rssi = -45, o[0].rate = 0x6c;
    o[0].idle_ms = 250, o[0].rx_data = 7, o[0].rx_null = 3, o[0].tx_ok = 5, o[0].tx_failed = 1;
    return 1;
}

/* Transmit buffers and counters (flow control and LINK_STATS). */
unsigned fake_tx_free = WIFI_TX_BUFS - 1;
unsigned wifi_tx_free(void) { return fake_tx_free; }
void wifi_tx_counters(unsigned c[3]) { c[0] = 0x101, c[1] = 0x202, c[2] = 0x303; }

/* The directed probe's SSID (SCAN's tail). */
unsigned char fake_scan_ssid[32];
unsigned fake_scan_ssid_len = 99;
void wifi_scan_ssid(const unsigned char *ssid, unsigned n)
{
    fake_scan_ssid_len = n;
    if (n)
        memcpy(fake_scan_ssid, ssid, n);
}

/* Link events: a queue the test fills. */
struct wifi_event fake_evq[8];
unsigned fake_evn;
int wifi_event_next(struct wifi_event *o)
{
    if (!fake_evn)
        return 0;
    *o = fake_evq[0];
    memmove(fake_evq, fake_evq + 1, --fake_evn * sizeof *o);
    return 1;
}

/* Regulatory domain. */
unsigned char fake_cc[2];
unsigned fake_chans, fake_passive;
int fake_dbm;
int wifi_set_regdom(const unsigned char cc[2], unsigned chans, unsigned passive, int max_dbm)
{
    if (!(chans & 0x1fff) || max_dbm < 0 || max_dbm > 30)
        return -1;
    memcpy(fake_cc, cc, 2), fake_chans = chans, fake_passive = passive, fake_dbm = max_dbm;
    return 0;
}
