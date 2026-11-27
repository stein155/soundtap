/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* WPA-PSK and WPA2-PSK 4-way and group handshake supplicant for the own BCO app (ADR 0031,
 * ADR 0136).
 *
 * The WiLD core has no crypto (static RE 2026-10-04: no SHA1/AES constants in the blob); it only
 * runs the CCMP cipher once it is handed a raw PTK/GTK via the module-18 set-key commands. So the
 * key derivation -- the 4-way EAPOL-Key exchange -- is host software, exactly as stock BCO stage2
 * does it. This module is pure: no I/O, no globals, no libc. It takes the precomputed PMK plus the
 * handshake inputs and produces the derived keys and the outgoing EAPOL-Key frames; wifi.c owns the
 * data path (feed received EAPOL in, send the produced EAPOL out, install the keys via set_key).
 *
 * Station role only. It never acts as an authenticator and never wraps key data; it only unwraps
 * the GTK the AP sends. RSN (WPA2, EAPOL-Key descriptor 2) and WPA (the WPA IE, descriptor 254),
 * each with pairwise CCMP (key descriptor version 2: HMAC-SHA1 MIC, AES key wrap) or TKIP (version
 * 1: HMAC-MD5 MIC, RC4 key data); the group cipher is the AP's, CCMP or TKIP. The app deciphers TKIP
 * itself (wsec.c). wpa_sm_config sets what the association negotiated. */
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
/* The AES S-box, which TKIP's key mixing derives its own from (wsec.c). */
uint8_t wpa_aes_sbox(unsigned i);
/* FIPS 180-4 SHA-256, HMAC-SHA256 and IEEE 802.11's KDF (the SHA-256 AKMs' PTK). */
void wpa_sha256(const uint8_t *msg, size_t len, uint8_t out[32]);
void wpa_hmac_sha256(const uint8_t *key, size_t key_len, const uint8_t *data, size_t data_len,
                     uint8_t out[32]);
void wpa_kdf_sha256(const uint8_t *key, size_t key_len, const char *label, const uint8_t *ctx,
                    size_t ctx_len, uint8_t *out, size_t out_len);
/* AES-128 single-block encrypt and AES-CMAC (RFC 4493). */
void wpa_aes128_enc(const uint8_t key[16], const uint8_t in[16], uint8_t out[16]);
void wpa_cmac(const uint8_t key[16], const uint8_t *msg, size_t len, uint8_t mac[16]);
/* RFC 1321 MD5 and RFC 2104 HMAC-MD5: key descriptor version 1's MIC. */
void wpa_md5(const uint8_t *msg, size_t len, uint8_t out[16]);
void wpa_hmac_md5(const uint8_t *key, size_t key_len, const uint8_t *data, size_t data_len,
                  uint8_t out[16]);
/* RC4 over buf after dropping the first skip bytes of its stream (version 1's key data: skip 256;
 * WEP and TKIP frames: skip 0, wsec.c). */
void wpa_rc4_skip(const uint8_t *key, unsigned klen, unsigned skip, uint8_t *buf, unsigned n);
/* AES-128 single-block decrypt (needed only for RFC 3394 unwrap). */
void wpa_aes128_dec(const uint8_t key[16], const uint8_t in[16], uint8_t out[16]);
/* RFC 3394 AES key unwrap. cipher is (n+1)*8 bytes, plain is n*8. Returns 1 on IV match. */
int wpa_aes_unwrap(const uint8_t kek[16], unsigned n, const uint8_t *cipher, uint8_t *plain);

/* Extract the GTK KDE from msg3's decrypted key data (skips the leading RSN IE and any non-GTK
 * element). Returns 1 and fills gtk/len/idx/tx, or 0 if absent. Exposed for the host self-test. */
int wpa_extract_gtk(const uint8_t *kd, unsigned plen, uint8_t gtk[32], unsigned *gtk_len,
                    int *gtk_idx, int *gtk_tx);

/* What the association negotiated: protocol, cipher suite types and AKM suite type. */
enum { WPA_PROTO_RSN = 1, WPA_PROTO_WPA = 2 };
enum { WPA_CIPHER_TKIP = 2, WPA_CIPHER_CCMP = 4 };
enum { WPA_AKM_PSK = 2, WPA_AKM_PSK_SHA256 = 6, WPA_AKM_SAE = 8 };
/* RSN capabilities (low byte): management frame protection required, capable (ADR 0136). */
#define WPA_CAP_MFPR 0x40u
#define WPA_CAP_MFPC 0x80u

/* The security IE this station sends in its association request and in Message 2 (the AP compares
 * the two): an RSN IE (WPA_RSN_IE_LEN bytes: version 1, the group, one pairwise suite, one AKM,
 * capabilities) or a WPA IE (WPA_WPA_IE_LEN: the same under 00-50-F2, no capabilities). Returns
 * its length. */
#define WPA_RSN_IE_LEN 22
#define WPA_WPA_IE_LEN 24
#define WPA_IE_MAX 26
unsigned wpa_own_ie(uint8_t *out, unsigned proto, unsigned group, unsigned pairwise, unsigned akm,
                    unsigned caps);
/* The RSN IE with pairwise CCMP, AKM PSK and no capabilities for the given group (2 TKIP, 4 CCMP). */
void wpa_rsn_ie(uint8_t out[WPA_RSN_IE_LEN], unsigned group);

/* WPA PMK from passphrase + SSID: PBKDF2-HMAC-SHA1(pass, ssid, 4096, 32). */
void wpa_pbkdf2(const uint8_t *pass, size_t pass_len, const uint8_t *ssid, size_t ssid_len,
                uint8_t pmk[32]);

/* --- PTK derivation --- */
/* PTK = PRF(PMK, "Pairwise key expansion", min(AA,SPA)||max||min(ANonce,SNonce)||max), 48 bytes
 * under CCMP, 64 under TKIP. ptk layout: KCK = ptk[0..15], KEK = ptk[16..31], TK = ptk[32..]
 * (TKIP: the TK, then the MIC key the AP sends with, then the one the station sends with). */
void wpa_pmk_to_ptk(const uint8_t pmk[32], const uint8_t aa[6], const uint8_t spa[6],
                    const uint8_t anonce[32], const uint8_t snonce[32], uint8_t *ptk, unsigned ptk_len);
/* The same under the SHA-256 AKMs (PSK-SHA256, SAE): IEEE 802.11's KDF-SHA256. */
void wpa_pmk_to_ptk_sha256(const uint8_t pmk[32], const uint8_t aa[6], const uint8_t spa[6],
                           const uint8_t anonce[32], const uint8_t snonce[32], uint8_t *ptk, unsigned ptk_len);

/* --- 4-way state machine --- */
enum { WPA_IDLE, WPA_GOT_MSG1, WPA_DONE };

struct wpa_sm {
    uint8_t pmk[32];
    uint8_t aa[6];  /* authenticator (AP / BSSID) MAC */
    uint8_t spa[6]; /* supplicant (our) MAC */
    uint8_t snonce[32];
    uint8_t anonce[32];
    uint8_t ptk[64];  /* keys in use */
    uint8_t tptk[64]; /* from the last Message 1, taken once Message 3 proves it */
    uint8_t gtk[32];
    unsigned gtk_len;
    int gtk_idx;
    int gtk_tx;
    uint8_t gtk_rsc[6]; /* the Key RSC that came with gtk: its last packet number, little-endian */
    uint8_t gtk_set;    /* gtk was handed out for install (a later frame with the same GTK is not) */
    int state;
    uint8_t replay[8];    /* the replay counter of the frame answered last, echoed in the reply */
    uint8_t rx_replay[8]; /* the counter of the last frame whose MIC verified; later ones count up */
    uint8_t rx_replay_set;
    uint8_t ptk_set;   /* a handshake completed: ptk is in use (group key messages verify under it) */
    uint8_t group;     /* group cipher suite type in our IE: 4 CCMP (wpa_sm_init), 2 TKIP */
    uint8_t req_replay[8]; /* the counter of our last request (a MIC failure report) */
    uint8_t proto, pairwise, akm; /* WPA_PROTO_*, WPA_CIPHER_*, WPA_AKM_* (wpa_sm_config) */
    uint8_t ver;       /* key descriptor version: 1 under pairwise TKIP, 2 under CCMP, 3 under
                        * PSK-SHA256, 0 (AKM-defined: CMAC and AES key wrap) under SAE */
    uint8_t mfp;       /* management frame protection negotiated: an IGTK comes with the GTK */
    uint8_t igtk[16], ipn[6], igtk_set;
    int igtk_idx;      /* 4 or 5 */
    uint8_t ptk_len;   /* 48 under CCMP, 64 under TKIP */
    uint8_t ie[WPA_IE_MAX], ielen; /* our IE, as in the association request */
};

/* Set up once per connect, after association. snonce must be 32 random bytes (caller supplies;
 * the handshake's freshness depends on it). It starts as RSN, CCMP/CCMP, AKM PSK; wpa_sm_config
 * then sets what the association negotiated (and with it the IE Message 2 repeats). */
void wpa_sm_init(struct wpa_sm *sm, const uint8_t pmk[32], const uint8_t aa[6],
                 const uint8_t spa[6], const uint8_t snonce[32]);
void wpa_sm_config(struct wpa_sm *sm, unsigned proto, unsigned pairwise, unsigned group, unsigned akm,
                   unsigned caps);

/* Result bits from wpa_sm_rx (WPA_RX_INSTALL_IGTK: igtk, igtk_idx and ipn are new). A GTK or IGTK
 * the same as the one handed out before is not installed again (the KRACK group-key class: a
 * reinstall would set its receive counter back); a new GTK comes with its Key RSC in gtk_rsc. */
#define WPA_RX_INSTALL_IGTK 0x20
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

/* A Michael MIC failure report (TKIP) for the AP, MICed under the PTK in use; pairwise when the
 * failure was under the pairwise key. Returns its length (WPA_EAPOL_MAX at most), 0 before a
 * handshake completed. */
size_t wpa_mic_failure(struct wpa_sm *sm, int pairwise, uint8_t *out);

#define WPA_EAPOL_ETHERTYPE 0x888e
#define WPA_EAPOL_RX_MAX 256 /* largest received EAPOL-Key frame taken */
#define WPA_EAPOL_MAX 155 /* 4 (802.1X) + 95 (key hdr) + up to ~56 (msg2 IE / msg4 none) */

#endif
