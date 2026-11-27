/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* WPA2-PSK (CCMP) 4-way handshake supplicant for the own BCO app (ADR 0031, step 3).
 *
 * The WiLD core has no crypto (static RE 2026-10-04: no SHA1/AES constants in the blob); it only
 * runs the CCMP cipher once it is handed a raw PTK/GTK via the module-18 set-key commands. So the
 * key derivation -- the 4-way EAPOL-Key exchange -- is host software, exactly as stock BCO stage2
 * does it. This module is pure: no I/O, no globals, no libc. It takes the precomputed PMK plus the
 * handshake inputs and produces the derived keys and the outgoing EAPOL-Key frames; wifi.c owns the
 * data path (feed received EAPOL in, send the produced EAPOL out, install the keys via set_key).
 *
 * Station role only. It never acts as an authenticator and never wraps key data; it only unwraps
 * the GTK the AP sends. Pairwise CCMP only (key descriptor version 2 = HMAC-SHA1 MIC + AES key
 * wrap); the group cipher is the AP's, CCMP or TKIP (a WPA/WPA2 mixed-mode router keeps TKIP for its
 * broadcasts). TKIP pairwise, WEP and descriptor version 1 (HMAC-MD5/RC4) are deliberately not
 * implemented. */
#ifndef WPA_H
#define WPA_H

#include <stddef.h>
#include <stdint.h>

/* --- primitives (exposed for the host self-test) --- */
void wpa_sha1(const uint8_t *msg, size_t len, uint8_t out[20]);
void wpa_hmac_sha1(const uint8_t *key, size_t key_len, const uint8_t *data, size_t data_len,
                   uint8_t out[20]);
/* IEEE 802.11 PRF: out = truncate(HMAC-SHA1(key, label||0x00||data||i), out_len). */
void wpa_sha1_prf(const uint8_t *key, size_t key_len, const char *label, const uint8_t *data,
                  size_t data_len, uint8_t *out, size_t out_len);
/* AES-128 single-block decrypt (needed only for RFC 3394 unwrap). */
void wpa_aes128_dec(const uint8_t key[16], const uint8_t in[16], uint8_t out[16]);
/* RFC 3394 AES key unwrap. cipher is (n+1)*8 bytes, plain is n*8. Returns 1 on IV match. */
int wpa_aes_unwrap(const uint8_t kek[16], unsigned n, const uint8_t *cipher, uint8_t *plain);

/* Extract the GTK KDE from msg3's decrypted key data (skips the leading RSN IE and any non-GTK
 * element). Returns 1 and fills gtk/len/idx/tx, or 0 if absent. Exposed for the host self-test. */
int wpa_extract_gtk(const uint8_t *kd, unsigned plen, uint8_t gtk[32], unsigned *gtk_len,
                    int *gtk_idx, int *gtk_tx);

/* The RSN IE this station sends in its association request and in Message 2 (the AP compares
 * the two): version 1, the given group cipher suite type (2 TKIP, 4 CCMP), pairwise CCMP, AKM PSK,
 * capabilities 0 (no management frame protection). */
#define WPA_RSN_IE_LEN 22
void wpa_rsn_ie(uint8_t out[WPA_RSN_IE_LEN], unsigned group);

/* WPA PMK from passphrase + SSID: PBKDF2-HMAC-SHA1(pass, ssid, 4096, 32). */
void wpa_pbkdf2(const uint8_t *pass, size_t pass_len, const uint8_t *ssid, size_t ssid_len,
                uint8_t pmk[32]);

/* --- PTK derivation --- */
/* PTK = PRF-384(PMK, "Pairwise key expansion", min(AA,SPA)||max||min(ANonce,SNonce)||max).
 * ptk layout: KCK = ptk[0..15], KEK = ptk[16..31], TK = ptk[32..47]. */
void wpa_pmk_to_ptk(const uint8_t pmk[32], const uint8_t aa[6], const uint8_t spa[6],
                    const uint8_t anonce[32], const uint8_t snonce[32], uint8_t ptk[48]);

/* --- 4-way state machine --- */
enum { WPA_IDLE, WPA_GOT_MSG1, WPA_DONE };

struct wpa_sm {
    uint8_t pmk[32];
    uint8_t aa[6];  /* authenticator (AP / BSSID) MAC */
    uint8_t spa[6]; /* supplicant (our) MAC */
    uint8_t snonce[32];
    uint8_t anonce[32];
    uint8_t ptk[48];  /* keys in use */
    uint8_t tptk[48]; /* from the last Message 1, taken once Message 3 proves it */
    uint8_t gtk[32];
    unsigned gtk_len;
    int gtk_idx;
    int gtk_tx;
    int state;
    uint8_t replay[8];    /* the replay counter of the frame answered last, echoed in the reply */
    uint8_t rx_replay[8]; /* the counter of the last frame whose MIC verified; later ones count up */
    uint8_t rx_replay_set;
    uint8_t ptk_set;   /* a handshake completed: ptk is in use (group key messages verify under it) */
    uint8_t group;     /* group cipher suite type in our RSN IE: 4 CCMP (wpa_sm_init), 2 TKIP */
};

/* Set up once per connect, after association. snonce must be 32 random bytes (caller supplies;
 * the handshake's freshness depends on it). */
void wpa_sm_init(struct wpa_sm *sm, const uint8_t pmk[32], const uint8_t aa[6],
                 const uint8_t spa[6], const uint8_t snonce[32]);

/* Result bits from wpa_sm_rx. */
#define WPA_RX_SEND        0x01 /* an EAPOL-Key frame was written to out/out_len, send it to aa */
#define WPA_RX_INSTALL_PTK 0x02 /* install ptk (TK = ptk[32..47]) as the pairwise CCMP key */
#define WPA_RX_INSTALL_GTK 0x04 /* install gtk[0..gtk_len-1] as the group key at gtk_idx */
#define WPA_RX_DONE        0x08 /* handshake complete (msg4 just produced) */
#define WPA_RX_FAIL        0x10 /* malformed / MIC mismatch; frame ignored, nothing produced */

/* Feed one received EAPOL frame (starting at the 802.1X header byte). On msg1 produces msg2, on
 * msg3 produces msg4 plus install flags, on a group msg1 (GTK rekey) group msg2 plus
 * WPA_RX_INSTALL_GTK. A PTK rekey is a new msg1/msg3 pair while DONE. A frame whose replay counter
 * is not above that of the last frame whose MIC verified is dropped; msg1 has no MIC, so it never
 * moves that counter (a resent msg1 with the same counter is answered). out must hold >= 155
 * bytes. Never touches global state. */
int wpa_sm_rx(struct wpa_sm *sm, const uint8_t *in, size_t in_len, uint8_t *out, size_t *out_len);

#define WPA_EAPOL_ETHERTYPE 0x888e
#define WPA_EAPOL_RX_MAX 256 /* largest received EAPOL-Key frame taken */
#define WPA_EAPOL_MAX 155 /* 4 (802.1X) + 95 (key hdr) + up to ~56 (msg2 RSN IE / msg4 none) */

#endif
