/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Frame protection the WiLD core's engine does not run for SoundTap (ADR 0136): WEP and TKIP, in
 * software on the BCO app, where the keys already are. Pure and freestanding like wpa.c: no I/O, no
 * globals the caller sees, no libc. wifi.c owns the keys, the replay counters and the frames.
 *
 * A protected body on air is the cipher header, the ciphertext and its trailer:
 *   WEP   IV[3], key id << 6;              data, ICV (4)                        (+8 bytes)
 *   TKIP  TSC1, (TSC1 | 0x20) & 0x7f, TSC0, key id << 6 | 0x20 (ExtIV), TSC2-5;
 *         MSDU, Michael MIC (8), ICV (4)                                        (+20 bytes)
 * The ICV is CRC-32 over the plaintext, little endian, enciphered with it. One MPDU per MSDU: the
 * station neither sends nor reassembles fragments, so Michael always covers the whole MSDU. */
#ifndef WSEC_H
#define WSEC_H

#include <stddef.h>
#include <stdint.h>

#define WSEC_WEP_HDR 4u
#define WSEC_TKIP_HDR 8u
#define WSEC_ICV 4u
#define WSEC_MIC 8u
#define WSEC_WEP_OVERHEAD (WSEC_WEP_HDR + WSEC_ICV)
#define WSEC_TKIP_OVERHEAD (WSEC_TKIP_HDR + WSEC_MIC + WSEC_ICV)

/* Cipher suite types (IEEE 802.11 00-0F-AC:n), as the key installs name them. */
enum { WSEC_WEP40 = 1, WSEC_TKIP = 2, WSEC_CCMP = 4, WSEC_WEP104 = 5 };

/* What opening a protected body found. */
enum { WSEC_BAD = -1, WSEC_ICV_FAIL = -2, WSEC_MIC_FAIL = -3 };

/* --- primitives (exposed for the host known-answer tests) --- */
/* RC4: XOR the keystream of key (klen 1..32 bytes) over buf. */
void wsec_rc4(const uint8_t *key, unsigned klen, uint8_t *buf, unsigned n);
/* IEEE 802.3 CRC-32 (zlib's crc32), the WEP and TKIP ICV. */
uint32_t wsec_crc32(const uint8_t *p, unsigned n);
/* Michael (IEEE 802.11-2020 12.5.2.3) over DA, SA, priority, three zero bytes and the MSDU. */
void wsec_michael(const uint8_t key[8], const uint8_t da[6], const uint8_t sa[6], unsigned prio,
                  const uint8_t *msdu, unsigned n, uint8_t mic[8]);
/* Michael over a bare message, as IEEE 802.11's test vectors give it. */
void wsec_michael_raw(const uint8_t key[8], const uint8_t *msg, unsigned n, uint8_t mic[8]);
/* TKIP's per-packet RC4 key from the temporal key, the transmitter address and the 48-bit TSC
 * (phase 1 over TSC2-5, phase 2 over TSC0-1, IEEE 802.11-2020 12.5.2.5). */
void wsec_tkip_mix(const uint8_t tk[16], const uint8_t ta[6], uint64_t tsc, uint8_t rc4key[16]);

/* --- WEP --- */
/* Encipher the n plaintext bytes at f + WSEC_WEP_HDR in place: the IV header goes at f, the ICV
 * after the data. key is 5 or 13 bytes, keyid 0..3, iv the 24-bit IV. Returns n + 8. */
unsigned wsec_wep_protect(const uint8_t *key, unsigned klen, unsigned keyid, uint32_t iv, uint8_t *f,
                          unsigned n);
/* Decipher a WEP body (IV header, ciphertext, ICV: n bytes) in place. Returns the plaintext length,
 * the plaintext at f + WSEC_WEP_HDR, or WSEC_BAD (too short, an ExtIV bit) or WSEC_ICV_FAIL. */
int wsec_wep_open(const uint8_t *key, unsigned klen, uint8_t *f, unsigned n);

/* --- TKIP --- */
/* The key id of a WEP or TKIP header, and whether it is TKIP's (the ExtIV bit). */
unsigned wsec_keyid(const uint8_t *f);
int wsec_extiv(const uint8_t *f);
/* The TSC of a TKIP header. */
uint64_t wsec_tkip_tsc(const uint8_t *f);
/* Encipher the n-byte MSDU at f + WSEC_TKIP_HDR in place with TSC tsc: the header goes at f, the
 * Michael MIC (mic_key, over da, sa, priority 0) and the ICV after the MSDU. ta is the
 * transmitter (addr2). Returns n + 20. */
unsigned wsec_tkip_protect(const uint8_t tk[16], const uint8_t mic_key[8], unsigned keyid, uint64_t tsc,
                           const uint8_t ta[6], const uint8_t da[6], const uint8_t sa[6], uint8_t *f,
                           unsigned n);
/* Decipher a TKIP body (n bytes) in place and check ICV and MIC. The caller checks the TSC
 * (wsec_tkip_tsc) against its replay counter first and takes it only when this succeeds.
 * Returns the MSDU length, the MSDU at f + WSEC_TKIP_HDR, or WSEC_BAD, WSEC_ICV_FAIL, or
 * WSEC_MIC_FAIL: a frame whose ICV held and whose Michael MIC did not, the one that counts
 * towards the countermeasures. */
int wsec_tkip_open(const uint8_t tk[16], const uint8_t mic_key[8], const uint8_t ta[6], const uint8_t da[6],
                   const uint8_t sa[6], uint8_t *f, unsigned n);

#endif
