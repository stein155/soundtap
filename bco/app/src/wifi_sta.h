/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Station-only Wi-Fi driver interface for the own BCO app (ADR 0031).
 * The WiLD core is a lower MAC: the missing host management layer must be supplied before join.
 * Deliberately absent: AP, monitor and injection mode, any call that sends a caller-chosen
 * frame, any source address but our own, and deauth with a target. Slot order follows the stock sulu ops table (research notes, section 3.8). */
#ifndef WIFI_STA_H
#define WIFI_STA_H

#include <stddef.h>
#include <stdint.h>

#define WIFI_ETH_ALEN 6

enum wifi_key_alg {
    WIFI_KEY_NONE,
    WIFI_KEY_WEP,
    WIFI_KEY_TKIP,
    WIFI_KEY_CCMP,
};

struct wifi_sta_ops {
    int (*init)(void);
    void (*deinit)(void);
    int (*get_mac_addr)(uint8_t mac[WIFI_ETH_ALEN]);
    /* Scan for the configured profile; results come from get_scan_results. */
    int (*scan)(const uint8_t *ssid, size_t ssid_len);
    int (*get_scan_results)(void *buf, size_t size);
    /* Not implemented until the host management layer exists. */
    int (*associate)(const uint8_t bssid[WIFI_ETH_ALEN], const uint8_t *ie, size_t ie_len,
                     int pairwise_cipher, int group_cipher);
    /* Current connection only: no target address parameter, by design. */
    int (*deauthenticate)(uint16_t reason);
    int (*disassociate)(uint16_t reason);
    int (*set_key)(int alg, int key_idx, int set_tx, const uint8_t *seq, size_t seq_len,
                   const uint8_t *key, size_t key_len, int pairwise);
    int (*get_bssid)(uint8_t bssid[WIFI_ETH_ALEN]);
    int (*get_ssid)(uint8_t *ssid, size_t size); /* returns length */
    /* Data path: Ethernet-style (dst, ethertype, payload); the driver builds the 802.11 header
     * with our own MAC as SA and the current BSSID, never a caller-built frame. */
    int (*tx_eth)(const uint8_t dst[WIFI_ETH_ALEN], uint16_t ethertype, const void *payload,
                  size_t len);
};

extern const struct wifi_sta_ops wifi_sta_wild_ops;

#endif
