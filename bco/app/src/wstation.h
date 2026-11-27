/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Fixed station open-system auth, not a caller-selected management-frame API. */
#ifndef WSTATION_H
#define WSTATION_H
#include <stddef.h>
#include <stdint.h>

#define WSTATION_AUTH_SIZE (8 + 0xc4 + 6)

static int wstation_has_1mbps(const uint8_t *ies, size_t len)
{
    int found = 0;
    if (!ies && len)
        return 0;
    for (size_t off = 0; off < len;) {
        if (len - off < 2 || ies[off + 1] > len - off - 2)
            return 0;
        unsigned n = ies[off + 1];
        if (ies[off] == 1 || ies[off] == 50)
            for (unsigned i = 0; i < n; i++)
                found |= (ies[off + 2 + i] & 0x7f) == 2;
        off += 2 + n;
    }
    return found;
}

/* Data-frame rate chain: the AP's rates (IEs 1 and 50, 500 kb/s units) from the fastest down, up to
 * eight, the last repeated, as stock's StaRateSetData (09:09, e.g. 54 48 36 24 18 12 11 9). The core
 * steps down the chain on retries. Returns 0 when the AP lists no rate. */
static int wstation_rate_chain(const uint8_t *ies, size_t len, uint8_t out[8])
{
    unsigned n = 0, prev = 0x80;
    for (;;) { /* next rate below prev: n passes over the IEs, n <= 8 */
        unsigned best = 0;
        for (size_t off = 0; off + 2 <= len && ies[off + 1] <= len - off - 2; off += 2 + ies[off + 1])
            if (ies[off] == 1 || ies[off] == 50)
                for (unsigned i = 0; i < ies[off + 1]; i++) {
                    unsigned v = ies[off + 2 + i] & 0x7f;
                    if (v < prev && v > best)
                        best = v;
                }
        if (!best || n == 8)
            break;
        out[n++] = (uint8_t)(prev = best);
    }
    for (unsigned i = n; n && i < 8; i++)
        out[i] = out[n - 1];
    return n != 0;
}

static int wstation_unicast(const uint8_t mac[6])
{
    unsigned any = 0;
    if (!mac || (mac[0] & 1))
        return 0;
    for (unsigned i = 0; i < 6; i++)
        any |= mac[i];
    return any != 0;
}

/* Stock builder/submit layout: descriptor at alloc+8, body at descriptor+0xc4.
 * Rate is provided by the scanned peer's supported basic rates, never guessed here.
 * Caller supplies the device MAC and its configured BSSID, not arbitrary frame fields. */
static int wstation_auth(volatile uint8_t *buf, size_t size, const uint8_t own[6],
                         const uint8_t bssid[6], const uint8_t rates[8])
{
    if (!buf || size < WSTATION_AUTH_SIZE || !rates ||
        !wstation_unicast(own) || !wstation_unicast(bssid))
        return 0;
    unsigned different = 0;
    for (unsigned i = 0; i < 6; i++)
        different |= own[i] ^ bssid[i];
    if (!different)
        return 0;
    for (unsigned i = 0; i < 8; i++)
        if (!(rates[i] & 0x7f))
            return 0;
    for (unsigned i = 0; i < WSTATION_AUTH_SIZE; i++)
        buf[i] = 0;
    buf[0] = 0x0a, buf[1] = 4, buf[2] = 3, buf[3] = 0x74;
    volatile uint8_t *d = buf + 8;
    d[0] = 0x18, d[1] = 0x18, d[2] = 0x30, d[3] = 0xc4;
    for (unsigned i = 0; i < 8; i++)
        d[4 + i] = rates[i];
    d[0x14] = 4; /* unknown peer policy from stock; no allocated peer claimed */
    d[0x18] = 0xb0; /* open-system auth frame */
    for (unsigned i = 0; i < 6; i++) {
        d[0x1c + i] = bssid[i];
        d[0x22 + i] = own[i];
        d[0x28 + i] = bssid[i];
    }
    d[0xc4 + 2] = 1; /* algorithm 0, transaction 1, status 0 (LE16) */
    for (unsigned i = 0; i < 0x38; i++)
        buf[0x94 + i] = buf[8 + i];
    return 1;
}

/* WPA2-PSK association request. Same descriptor/address/save layout as wstation_auth; FC 0x00
 * (assoc request) and a body of capability + listen interval + SSID + a standard 11g supported-rates
 * set + the RSN IE the caller built (wpa_rsn_ie: the AP's group cipher, pairwise CCMP, AKM PSK).
 * The capability is ESS + Privacy, plus Short Preamble (bit 5) and Short Slot Time (bit 10) where
 * ap_caps, the capability of the AP's beacon or probe response, has them: an ERP station that leaves
 * Short Slot out makes the AP drop the whole BSS to the long slot. No HT or WMM element: the core
 * sends and receives neither HT rates nor QoS data frames, so claiming them would make the AP use
 * frames this station cannot decode. Returns the body length (for the submit record), 0 on
 * reject. */
#define WSTATION_CAP_SHORT_PREAMBLE 0x0020u
#define WSTATION_CAP_SHORT_SLOT 0x0400u
#define WSTATION_ASSOC_HDR 0xc4u
/* The largest body the fixed layout can make: a 32-byte SSID. 64 rejected every SSID
 * over 20 bytes; the caller's buffer size is still checked separately. */
#define WSTATION_ASSOC_MAXBODY (4u + (2u + 32u) + (2u + 8u) + (2u + 4u) + 22u)
#define WSTATION_RSN_LEN 22u
static int wstation_assoc(volatile uint8_t *buf, size_t size, const uint8_t own[6],
                          const uint8_t bssid[6], const uint8_t rates[8],
                          const char *ssid, unsigned ssidlen, uint16_t ap_caps,
                          const uint8_t rsn[WSTATION_RSN_LEN])
{
    static const uint8_t srates[8] = {0x82, 0x84, 0x8b, 0x96, 0x0c, 0x12, 0x18, 0x24};
    static const uint8_t erates[4] = {0x30, 0x48, 0x60, 0x6c};
    if (!buf || !rates || !ssid || !rsn || ssidlen > 32 || rsn[0] != 0x30 ||
        rsn[1] != WSTATION_RSN_LEN - 2 || !wstation_unicast(own) || !wstation_unicast(bssid))
        return 0;
    unsigned body = 4 + (2 + ssidlen) + (2 + 8) + (2 + 4) + WSTATION_RSN_LEN;
    if (body > WSTATION_ASSOC_MAXBODY || size < 8 + WSTATION_ASSOC_HDR + body)
        return 0;
    unsigned different = 0;
    for (unsigned i = 0; i < 6; i++)
        different |= own[i] ^ bssid[i];
    if (!different)
        return 0;
    for (unsigned i = 0; i < 8 + WSTATION_ASSOC_HDR + body; i++)
        buf[i] = 0;
    buf[0] = 0x0a, buf[1] = 4, buf[2] = 3, buf[3] = 0x74;
    volatile uint8_t *d = buf + 8;
    d[0] = 0x18, d[1] = 0x18, d[2] = 0x30, d[3] = 0xc4;
    for (unsigned i = 0; i < 8; i++)
        d[4 + i] = rates[i];
    d[0x14] = 4;
    d[0x18] = 0x00; /* association request frame */
    for (unsigned i = 0; i < 6; i++) {
        d[0x1c + i] = bssid[i];
        d[0x22 + i] = own[i];
        d[0x28 + i] = bssid[i];
    }
    volatile uint8_t *p = d + WSTATION_ASSOC_HDR;
    unsigned cap = 0x0011u | (ap_caps & (WSTATION_CAP_SHORT_PREAMBLE | WSTATION_CAP_SHORT_SLOT));
    p[0] = (uint8_t)cap, p[1] = (uint8_t)(cap >> 8); /* capability: ESS + Privacy (+ short) */
    p[2] = 0x01, p[3] = 0x00; /* listen interval 1 */
    unsigned o = 4;
    p[o++] = 0, p[o++] = (uint8_t)ssidlen;
    for (unsigned i = 0; i < ssidlen; i++)
        p[o++] = (uint8_t)ssid[i];
    p[o++] = 1, p[o++] = 8;
    for (unsigned i = 0; i < 8; i++)
        p[o++] = srates[i];
    p[o++] = 50, p[o++] = 4;
    for (unsigned i = 0; i < 4; i++)
        p[o++] = erates[i];
    for (unsigned i = 0; i < WSTATION_RSN_LEN; i++)
        p[o++] = rsn[i];
    for (unsigned i = 0; i < 0x38; i++)
        buf[0x94 + i] = buf[8 + i];
    return (int)body;
}

/* One LLC/SNAP data frame to the AP, laid out like stock's data encapsulation (0x600568d8 +
 * 0x600580ec, station mode, non-QoS peer): descriptor {0x18, 0x18, 0x32, 0xc4}, FC = data with
 * toDS (+ Protected), d[0x12] = 1 for EAPOL (0x888e), d[0x14] = 4 (non-QoS).
 * The core CCMP-encrypts a Protected frame with the key of the record's peer slot. Stock also
 * marks EAPOL Protected and relies on d[0x12]; we send EAPOL with the bit clear, which is what
 * goes on air during the handshake and is the form proven on hardware.
 * addr3 = da (AP for unicast, ff..ff for a broadcast such as DHCP). body = LLC/SNAP + payload.
 * Returns body length, 0 on reject. */
#define WSTATION_DATA_MAXBODY 1508u /* SNAP + 1500-byte IPv4 MTU */
static int wstation_data(volatile uint8_t *buf, size_t size, const uint8_t own[6],
                         const uint8_t bssid[6], const uint8_t da[6], const uint8_t rates[8],
                         uint16_t ethertype, const uint8_t *payload, unsigned plen)
{
    if (!buf || !rates || !da || !payload || !plen ||
        !wstation_unicast(own) || !wstation_unicast(bssid))
        return 0;
    unsigned body = 8 + plen;
    if (body > WSTATION_DATA_MAXBODY || size < 8 + WSTATION_ASSOC_HDR + body)
        return 0;
    for (unsigned i = 0; i < 8 + WSTATION_ASSOC_HDR; i++)
        buf[i] = 0;
    buf[0] = 0x0a, buf[1] = 4, buf[2] = 3, buf[3] = 0x74;
    volatile uint8_t *d = buf + 8;
    d[0] = 0x18, d[1] = 0x18, d[2] = 0x32, d[3] = 0xc4;
    for (unsigned i = 0; i < 8; i++)
        d[4 + i] = rates[i];
    int eapol = ethertype == 0x888e;
    d[0x12] = (uint8_t)eapol;
    d[0x14] = 4;
    d[0x18] = 0x08, d[0x19] = eapol ? 0x01 : 0x41; /* data; toDS, Protected unless EAPOL */
    for (unsigned i = 0; i < 6; i++) {
        d[0x1c + i] = bssid[i]; /* addr1 = RA = BSSID */
        d[0x22 + i] = own[i];   /* addr2 = TA/SA = own */
        d[0x28 + i] = da[i];    /* addr3 = DA */
    }
    volatile uint8_t *p = d + WSTATION_ASSOC_HDR;
    p[0] = 0xaa, p[1] = 0xaa, p[2] = 3, p[3] = 0, p[4] = 0, p[5] = 0;
    p[6] = (uint8_t)(ethertype >> 8), p[7] = (uint8_t)ethertype;
    for (unsigned i = 0; i < plen; i++)
        p[8 + i] = payload[i];
    for (unsigned i = 0; i < 0x38; i++)
        buf[0x94 + i] = buf[8 + i];
    return (int)body;
}

/* One EAPOL-Key data frame to the AP (4-way msg2/msg4): ethertype 0x888e, addr3 = AP.
 * Thin wrapper over wstation_data. Returns body length, 0 on reject. */
#define WSTATION_EAPOL_MAXBODY 200u
static int wstation_eapol(volatile uint8_t *buf, size_t size, const uint8_t own[6],
                          const uint8_t bssid[6], const uint8_t rates[8],
                          const uint8_t *eapol, unsigned eaplen)
{
    if (eaplen > WSTATION_EAPOL_MAXBODY - 8)
        return 0;
    return wstation_data(buf, size, own, bssid, bssid, rates, 0x888e, eapol, eaplen);
}

/* Any frame of the access point (ADR 0031): beacons, probe/auth/assoc responses and fromDS data,
 * in the same pool-header/descriptor/save layout as the station frames above. fc is the frame
 * control field (type, subtype, flags); a nonzero et puts an LLC/SNAP header for that ethertype
 * before the body. The transmitter address is always own. Returns the body length, 0 on reject. */
static int wframe(volatile uint8_t *buf, size_t size, unsigned fc, const uint8_t a1[6],
                  const uint8_t own[6], const uint8_t a3[6], const uint8_t rates[8], unsigned et,
                  const uint8_t *body, unsigned blen)
{
    unsigned n = (et ? 8 : 0) + blen;
    if (!buf || !a1 || !a3 || !rates || (blen && !body) || !wstation_unicast(own) ||
        n > WSTATION_DATA_MAXBODY || size < 8 + WSTATION_ASSOC_HDR + n)
        return 0;
    for (unsigned i = 0; i < 8 + WSTATION_ASSOC_HDR; i++)
        buf[i] = 0;
    buf[0] = 0x0a, buf[1] = 4, buf[2] = 3, buf[3] = 0x74;
    volatile uint8_t *d = buf + 8;
    d[0] = 0x18, d[1] = 0x18, d[2] = (fc & 0x0c) == 8 ? 0x32 : 0x30, d[3] = 0xc4;
    for (unsigned i = 0; i < 8; i++)
        d[4 + i] = rates[i];
    d[0x14] = 4;
    d[0x18] = (uint8_t)fc, d[0x19] = (uint8_t)(fc >> 8);
    for (unsigned i = 0; i < 6; i++) {
        d[0x1c + i] = a1[i];
        d[0x22 + i] = own[i];
        d[0x28 + i] = a3[i];
    }
    volatile uint8_t *p = d + WSTATION_ASSOC_HDR;
    if (et) {
        p[0] = 0xaa, p[1] = 0xaa, p[2] = 3, p[3] = 0, p[4] = 0, p[5] = 0;
        p[6] = (uint8_t)(et >> 8), p[7] = (uint8_t)et;
        p += 8;
    }
    for (unsigned i = 0; i < blen; i++)
        p[i] = body[i];
    for (unsigned i = 0; i < 0x38; i++)
        buf[0x94 + i] = buf[8 + i];
    return (int)n;
}
#endif
