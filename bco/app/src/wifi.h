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
    unsigned char grp; /* RSN group cipher suite type: 2 TKIP, 4 CCMP, 0 unknown (STATUS only) */
};

int wifi_init(const unsigned char mac[6]);  /* boot the core (blocking, ~1 s); 0 when up */
void wifi_poll(unsigned t);                  /* call from the main loop */

unsigned wifi_state(void);
/* start = 1 begins a scan when the radio is free. Returns -1 while one runs, else the number of
 * cached results (wifi_bss 0..n-1). Connected, the cache is returned without leaving the channel. */
int wifi_scan(int start);
/* The SSID the next scan's directed pass probes for (a hidden network answers only a probe that
 * names it); n = 0 leaves it to the profile's SSID, as before. Set before wifi_scan(1). */
void wifi_scan_ssid(const unsigned char *ssid, unsigned n);
int wifi_bss(unsigned i, struct wifi_bss *o);
/* Join a WPA2-PSK network: the 32-byte PMK and 32 random bytes from the SCM for the SNonce.
 * flags (WIFI_BSSID_*, WIFI_ROAM) and bssid come from SET_PROFILE's tail (ADR 0135); flags 0
 * joins the strongest access point of the SSID and never roams.
 * Returns 0 when the join started (state goes JOINING, then UP or FAILED). */
#define WIFI_BSSID_FIXED 0x1u /* only this BSSID */
#define WIFI_BSSID_HINT 0x2u  /* this BSSID first, when it is heard */
#define WIFI_ROAM 0x4u        /* roam to a clearly stronger access point of the SSID */
int wifi_join(const unsigned char *ssid, unsigned ssid_len, const unsigned char k[32],
              const unsigned char s[32], unsigned flags, const unsigned char bssid[6]);
void wifi_leave(void);

/* Link events (ADR 0135): what happened to the link, queued for the SCM. type is WIFI_EV_*;
 * reason the 802.11 reason code of a deauthentication or disassociation, else 0. */
enum { WIFI_EV_DEAUTH = 1, WIFI_EV_DISASSOC, WIFI_EV_BEACON_LOSS, WIFI_EV_TX_WEDGE, WIFI_EV_ROAMED,
       WIFI_EV_ROAM_FAILED };
struct wifi_event {
    unsigned char type, bssid[6], chan;
    unsigned short reason;
    signed char rssi;
};
#define WIFI_BEACON_LOSS_MS 8000u /* no beacon from the joined access point this long: link lost */
/* The oldest queued event into o; 0 when there is none. The queue keeps the newest few. */
int wifi_event_next(struct wifi_event *o);

/* Regulatory domain (ADR 0135): country, the channels allowed (bit n-1 = channel n), those of
 * them only listened on (no probe, no access point), and the TX power limit in dBm. Returns -1
 * when refused (no channel left, a limit out of range), 1 when the radio holds the power limit,
 * 0 when it is only recorded. Until it is set every channel 1..13 is allowed and probed. */
int wifi_set_regdom(const unsigned char cc[2], unsigned chans, unsigned passive, int max_dbm);
/* Reboot the WiLD core (same ~1 s bring-up as wifi_init), clearing a sticky radio wedge without a
 * DM870 re-flash: wifi.c's TX-wedge self-heal and WILD_BOOT; the caller leaves the station link
 * first. FORGET does not reboot the core. */
int wifi_reset_core(void);
int wifi_wild_write(unsigned off, const unsigned char *p, unsigned n);
int wifi_wild_boot(unsigned len, unsigned crc);
int wifi_wild_peek(unsigned off, unsigned n);
/* The network joined or being joined; 0 (and an empty entry) when idle. */
int wifi_current(struct wifi_bss *o);

/* Transmit buffers (flow control, ADR 0031): WIFI_TX_BUFS in all, shared by the station and the
 * access point. wifi_tx_free() is how many the SCM may fill now: the free ones less one kept for the
 * app's own frames (authentication, association, EAPOL), 0 before the core is up. */
#define WIFI_TX_BUFS 7u
unsigned wifi_tx_free(void);
/* Frames acknowledged, failed on air after the retries, dropped before the air (no free buffer,
 * not joined, a frame the station cannot send). */
void wifi_tx_counters(unsigned c[3]);

/* Why the last join failed (STATUS reports it after the BSS): the step, and the AP's IEEE 802.11
 * status code (auth, assoc) or reason code (kicked) where it sent one. 0, 0 after a join that
 * succeeded or before any. */
enum {
    WIFI_FAIL_NONE,
    WIFI_FAIL_NOT_FOUND,     /* no BSS with this SSID heard */
    WIFI_FAIL_SECURITY,      /* only with security this station cannot join (WPA3 only, PMF required) */
    WIFI_FAIL_AUTH,          /* authentication refused: code = status */
    WIFI_FAIL_AUTH_TIMEOUT,  /* no authentication response */
    WIFI_FAIL_ASSOC,         /* association refused: code = status */
    WIFI_FAIL_ASSOC_TIMEOUT, /* no association response */
    WIFI_FAIL_HANDSHAKE,     /* the 4-way handshake did not complete: usually a wrong password */
    WIFI_FAIL_KICKED,        /* deauthenticated or disassociated: code = reason */
};
void wifi_fail(unsigned *step, unsigned *code);

/* Open access point (setup AP, ADR 0031): channel 1..13, beacon interval in TU, and the beacon
 * body the SCM's hostapd built (timestamp, interval, capability, IEs including TIM). Leaves any
 * station link first. The app sends the beacons from that body every interval, answers probe
 * requests with it (without the TIM), and answers open-system authentication and
 * (re)association; the core only tunes the channel and passes the frames (ADR 0031). */
#define WIFI_AP_BODY_MAX 512u
#define WIFI_AP_CLIENTS 6u /* one core peer slot each, 0..5 */
int wifi_ap_start(unsigned chan, unsigned bint, const unsigned char *body, unsigned len);
void wifi_ap_stop(void);
/* Associated clients: copies up to WIFI_AP_CLIENTS MACs, returns the count. */
unsigned wifi_ap_clients(unsigned char macs[][6]);
/* Counters for AP_STATUS: AP up (0/1), probe requests heard, frames received while the AP runs,
 * TX acknowledged, TX failed, TX dropped, data frames to the SCM, beacons sent. */
#define WIFI_AP_COUNTERS 8u
void wifi_ap_counters(unsigned c[WIFI_AP_COUNTERS]);
/* AP_STATUS's tail (BCOLINK_CAP_APDIAG): ms from AP_START to the first beacon (valid once a
 * beacon was sent), then the core's ap_mode counters: frames to our address, those of them its
 * address filter missed, a client's data frames kept for the app; then frames held for dozing
 * clients that were dropped because the hold was full. */
#define WIFI_AP_DIAG 5u
void wifi_ap_diag(unsigned d[WIFI_AP_DIAG]);
/* One associated client: dozing (power-management bit set), frames held for it, ms since it was
 * last heard, data and null-data frames from it, unicast frames to it ACKed and lost. */
struct wifi_ap_client {
    unsigned char mac[6], dozing, held;
    unsigned idle_ms, rx_data, rx_null, tx_ok, tx_failed;
};
unsigned wifi_ap_client_info(struct wifi_ap_client o[WIFI_AP_CLIENTS]);

#endif
