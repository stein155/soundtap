/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Wi-Fi station of the BCO network card (ADR 0031): the WiLD core run as a WPA2-PSK station
 * on 2.4 GHz, bridged to BCO link channel 1 as Ethernet frames under the device's own MAC.
 * wifi.c is the hardware side; mgmt.c only calls the functions below (fakes in the host tests). */
#ifndef WIFI_H
#define WIFI_H

enum { WIFI_IDLE, WIFI_SCANNING, WIFI_JOINING, WIFI_UP, WIFI_FAILED, WIFI_AP };

struct wifi_bss {
    unsigned char ssid_len, ssid[32], bssid[6], chan, sec; /* sec: 0 open, 1 RSN, 2 WPA1 only */
    signed char rssi;
};

int wifi_init(const unsigned char mac[6]);  /* boot the core (blocking, ~1 s); 0 when up */
void wifi_poll(unsigned now_ms);             /* call from the main loop */

unsigned wifi_state(void);
/* start = 1 begins a scan when the radio is free. Returns -1 while one runs, else the number of
 * cached results (wifi_bss 0..n-1). Connected, the cache is returned without leaving the channel. */
int wifi_scan(int start);
int wifi_bss(unsigned i, struct wifi_bss *b);
/* Join a WPA2-PSK network: the 32-byte PMK and 32 random bytes from the SCM for the SNonce.
 * Returns 0 when the join started (state goes JOINING, then UP or FAILED). */
int wifi_join(const unsigned char *ssid, unsigned ssid_len, const unsigned char pmk[32],
              const unsigned char seed[32]);
void wifi_leave(void);
/* Reboot the WiLD core (same ~1 s bring-up as wifi_init), clearing a sticky radio wedge without a
 * DM870 re-flash. Driven from T_FORGET; the caller leaves the station link first. */
int wifi_reset_core(void);
int wifi_wild_write(unsigned off, const unsigned char *p, unsigned n);
int wifi_wild_boot(unsigned len, unsigned crc);
int wifi_wild_peek(unsigned off, unsigned n);
/* The network joined or being joined; 0 (and an empty entry) when idle. */
int wifi_current(struct wifi_bss *b);

/* Transmit buffers (flow control, ADR 0031): WIFI_TX_BUFS in all, shared by the station and the
 * access point. wifi_tx_free() is how many the SCM may fill now: the free ones less one kept for the
 * app's own frames (authentication, association, EAPOL), 0 before the core is up. */
#define WIFI_TX_BUFS 7u
unsigned wifi_tx_free(void);
/* Frames acknowledged, failed on air after the retries, dropped before the air (no free buffer,
 * not joined, a frame the station cannot send). */
void wifi_tx_counters(unsigned c[3]);

/* Open access point (setup AP, ADR 0031): channel 1..13, beacon interval in TU, and the beacon
 * body the SCM's hostapd built (timestamp, interval, capability, IEs including TIM). Leaves any
 * station link first. The core beacons and answers probes from it; open-system authentication
 * and (re)association are answered here. */
#define WIFI_AP_BODY_MAX 512u
#define WIFI_AP_CLIENTS 6u /* one core peer slot each, 0..5 */
int wifi_ap_start(unsigned chan, unsigned bint, const unsigned char *body, unsigned len);
void wifi_ap_stop(void);
/* Associated clients: copies up to WIFI_AP_CLIENTS MACs, returns the count. */
unsigned wifi_ap_clients(unsigned char macs[][6]);
/* Counters for AP_STATUS: AP up (0/1), probe requests heard, frames received while the AP runs,
 * TX acknowledged, TX failed, TX dropped, data frames to the SCM, core BSS-ready events. */
#define WIFI_AP_COUNTERS 8u
void wifi_ap_counters(unsigned c[WIFI_AP_COUNTERS]);

#endif
