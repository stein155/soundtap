/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Fake station for the host tests of mgmt.c: records what the management server asked for. */
#include <string.h>
#include "wifi.h"

unsigned fake_state, fake_scanning, fake_nbss, fake_joins, fake_leaves;
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
    memcpy(o->bssid, "\x02\x00\x00\x00\x00\x10", 6), o->bssid[5] += i;
    o->chan = 1 + i, o->rssi = -40 - (int)i, o->sec = 1;
    return 0;
}
int wifi_join(const unsigned char *ssid, unsigned ssid_len, const unsigned char k[32],
              const unsigned char s[32])
{
    memcpy(fake_ssid, ssid, ssid_len), fake_ssid_len = ssid_len;
    memcpy(fake_pmk, k, 32), memcpy(fake_seed, s, 32);
    fake_joins++, fake_state = WIFI_JOINING;
    return 0;
}
void wifi_leave(void) { fake_leaves++, fake_state = WIFI_IDLE, fake_ssid_len = 0; }
int wifi_reset_core(void) { fake_state = WIFI_IDLE; return 0; }
int wifi_current(struct wifi_bss *o)
{
    memset(o, 0, sizeof *o);
    if (!fake_ssid_len)
        return 0;
    o->ssid_len = fake_ssid_len, memcpy(o->ssid, fake_ssid, fake_ssid_len);
    memcpy(o->bssid, "\x02\x00\x00\x00\x00\x10", 6), o->chan = 6, o->rssi = -50;
    return 1;
}

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

/* Transmit buffers and counters (flow control and LINK_STATS). */
unsigned fake_tx_free = WIFI_TX_BUFS - 1;
unsigned wifi_tx_free(void) { return fake_tx_free; }
void wifi_tx_counters(unsigned c[3]) { c[0] = 0x101, c[1] = 0x202, c[2] = 0x303; }
