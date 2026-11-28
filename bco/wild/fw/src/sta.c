/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Station module (0x12): commands the host sends to the station side of the MAC, and the peer
 * table those commands maintain (stock dispatcher 0x318c). */
#include "hw.h"

static unsigned char *keys;

#define NPEER 6

/* CCMP keys the host installs (stock dispatcher types 0xb/0xd, builders 0x60113268..0x601136e8).
 * Pairwise keys are bound to a peer slot, group keys to an index 0..3. The 16-byte temporal key and
 * the per-key packet numbers are all CCMP needs: sta_txkey for a protected TX, sta_rxkey for a
 * protected RX (both on the cipher engine, ADRs 0031 and 0031). Group-key TX and the hardware
 * peer/key table (stock 0x894 via 0x29d8 / 0x522c) are not used; the engine takes the key from its
 * control block. A pairwise key's receive PN starts at 0 with each install; a group key's at the
 * Key RSC the app passes with it (bytes 24-29 of the install), so frames the access point sent
 * before the install count as replays. */
static struct { unsigned char tk[16], used, soft; unsigned long long pn, rxpn; } pkey[NPEER];
static struct { unsigned char tk[16], used, soft; unsigned long long rxpn; } gkey[4];
/* A key of another cipher than CCMP (WEP, TKIP) is the app's (ADR 0136): the install names the
 * cipher suite type and carries no key bytes we use. Such a slot only tells rx_deliver to pass a
 * frame for it up undeciphered (sta_rxkey's *soft); the app deciphers and checks it. It never
 * enciphers here: sta_txkey has nothing for it, so a frame marked Protected but not enciphered by
 * the app is dropped, never sent in clear. */
#define CIPHER_CCMP 4

/* Peer table: slot -> MAC of the far end (stock keeps this behind 0x894 for the MAC's crypto and
 * addressing; here it is the software map that resolves a received frame's transmitter to its
 * slot). Slot 0 is the AP for a station (PEER_AP); 0..5 are an AP's clients. Slot 6 = none.
 * the hardware peer/key table (stock 0x3938 into 0x894) binds CCMP keys to a slot; that
 * is the crypto milestone, so this table stays software-only until keys land. */
static struct { unsigned char mac[6], used; } peer[NPEER];
static unsigned privacy;

void sta_init(unsigned char *key_block)
{
    keys = key_block;
}

/* Pairwise temporal key for a peer slot and the next transmit PN (pre-incremented), or 0 if no
 * key is installed there. xport.c enciphers with it (ADR 0031) and writes the PN into the on-air
 * CCMP header. */
const unsigned char *sta_txkey(unsigned slot, unsigned long long *pn)
{
    if (slot >= NPEER || !pkey[slot].used || pkey[slot].soft)
        return 0;
    *pn = ++pkey[slot].pn;
    return pkey[slot].tk;
}

/* Receive key for a protected frame: the group key with this key id when group, else the
 * pairwise key of the transmitter's peer slot; *rxpn is its last accepted PN, *soft set when the app
 * deciphers frames under it. 0 if none. */
const unsigned char *sta_rxkey(unsigned slot, int group, unsigned keyid, unsigned long long **rxpn,
                               int *soft)
{
    if (group) {
        if (!gkey[keyid & 3].used)
            return 0;
        *rxpn = &gkey[keyid & 3].rxpn;
        *soft = gkey[keyid & 3].soft;
        return gkey[keyid & 3].tk;
    }
    if (slot >= NPEER || !pkey[slot].used)
        return 0;
    *rxpn = &pkey[slot].rxpn;
    *soft = pkey[slot].soft;
    return pkey[slot].tk;
}

/* Slot of the peer with this MAC, or 6 (none). */
unsigned peer_slot(const unsigned char *mac)
{
    for (unsigned s = 0; s < NPEER; s++) {
        if (!peer[s].used)
            continue;
        unsigned diff = 0;
        for (unsigned i = 0; i < 6; i++)
            diff |= peer[s].mac[i] ^ mac[i];
        if (!diff)
            return s;
    }
    return 6;
}

/* A pairwise key belongs to its peer: when the slot is emptied or given to another MAC, the key
 * goes with it. A data frame still queued for the slot then finds no key and is dropped
 * (xport.c tx_stage_done) instead of going out under the old peer's key. */
static void peer_forget(unsigned s)
{
    peer[s].used = 0;
    pkey[s].used = 0;
}

static void peer_clear_all(void)
{
    for (unsigned s = 0; s < NPEER; s++)
        peer_forget(s);
}

/* The payload bytes each command reads (the layouts below). A shorter command is refused before
 * it reads anything: no effect, a failed reply where the command has a status, and counted in
 * rec[RC_CMDBAD]. */
static unsigned need(unsigned type)
{
    switch (type) {
    case 0x0b: return 8 + 16;
    case 0x0d: return 0x14 + 16;
    case 0x0e: return 6;
    case 0xb5:
    case 0xb6: return 7;
    case 0x0c:
    case 0xb8: return 1;
    }
    return 0;
}

static unsigned station(unsigned type, const unsigned char *payload, unsigned len, unsigned module,
                        unsigned src)
{
    unsigned s;
    unsigned slot = 0, status = 0;   /* 4-byte so the event's payload word has no stack bytes */
    (void)module, (void)keys;
    if (len < need(type)) {
        rec[RC_CMDBAD]++;
        if (type == 0x0b || type == 0x0d) {
            status = 1;
            route(0x1b, src, MOD_STA, &status, 2);
        }
        return 1;
    }
    switch (type) {
    case 0x0b: /* group key install: cipher suite type payload[0], index payload[4], 16-byte key
                * payload[8..], the Key RSC little-endian payload[24..29] when sent (stock 0x33a0) */
        if (payload[4] < 4) {
            unsigned long long rsc = 0;
            for (unsigned i = 0; len >= 30 && i < 6; i++)
                rsc |= (unsigned long long)payload[24 + i] << (8 * i);
            copy_bytes(gkey[payload[4]].tk, payload + 8, 16);
            gkey[payload[4]].rxpn = rsc;
            gkey[payload[4]].soft = payload[0] != CIPHER_CCMP;
            gkey[payload[4]].used = 1;
        } else {
            status = 1;                     /* nothing installed: the reply says so */
        }
        route(0x1b, src, MOD_STA, &status, 2);
        return 0;
    case 0x0c: /* group key delete: index payload[0] (stock 0x31e0) */
        if (payload[0] < 4)
            gkey[payload[0]].used = 0;
        route(0x1c, src, MOD_STA, 0, 0);
        return 0;
    case 0x0d: /* pairwise key install: cipher suite type payload[8], peer MAC payload[0xc..0x11],
                * 16-byte key payload[0x14..] (stock 0x345c) */
        s = peer_slot(payload + 0xc);
        if (s < NPEER) {
            copy_bytes(pkey[s].tk, payload + 0x14, 16);
            pkey[s].pn = pkey[s].rxpn = 0;   /* first protected frame carries PN 1 */
            pkey[s].soft = payload[8] != CIPHER_CCMP;
            pkey[s].used = 1;
        } else {
            status = 1;   /* no peer with this MAC: nothing installed, never an install "ok" */
        }
        slot = s;
        route(0xba, 0x11, MOD_STA, &slot, 2);   /* key notification to the station manager */
        route(0x1b, src, MOD_STA, &status, 2);
        return 0;
    case 0x0e: /* pairwise key delete: peer MAC payload[0..5] (stock 0x35f0) */
        s = peer_slot(payload);
        if (s < NPEER)
            pkey[s].used = 0;
        slot = s;
        route(0xbb, 0x11, MOD_STA, &slot, 2);
        route(0x1c, src, MOD_STA, 0, 0);
        return 0;
    case 0xb5: /* PEER_ADD: MAC payload[0..5], slot payload[6..7] (stock 0x32e8) */
        s = payload[6];
        if (s < NPEER) {
            if (peer_slot(payload) != s)
                peer_forget(s);         /* a new peer in the slot starts without a key */
            copy_bytes(peer[s].mac, payload, 6);
            peer[s].used = 1;
        }
        return 0;
    case 0xb6: /* PEER_DEL: slot payload[6..7] (stock 0x3324) */
        s = payload[6];
        if (s < NPEER)
            peer_forget(s);
        return 0;
    case 0xb7: /* drop every peer and its pairwise key (stock 0x3368); the app sends it when it
                * stops its access point, which also ends ap_mode and the BSS */
        peer_clear_all();
        if (ap_mode)
            ap_mode = joined = 0;
        return 0;
    case 0xb8: /* JOIN_DONE: privacy payload[0], open the AC queues payload[1]; resets the peer
                * table and the pairwise keys (stock 0x3298) */
        privacy = payload[0];
        peer_clear_all();
        return 0;
    }
    /* the remaining station commands (keys, ...) come with the crypto milestone */
    return 1;
}

/* A command runs with IRQs off: the IRQ's DMA completions encipher (tx_stage_done, sta_txkey) and
 * decipher (rx_deliver, sta_rxkey) with these keys and look up these peers. A key install it
 * interrupted between the new key and its PN reset sent one frame under the new key with the old
 * key's next PN; the access point then took every frame after it, numbered from 1 again, as a
 * replay until the PN caught up, for the life of the key. */
unsigned sta_cmd(unsigned type, const unsigned char *payload, unsigned len, unsigned module,
                 unsigned src)
{
    unsigned r;
    irq_off();
    r = station(type, payload, len, module, src);
    irq_on();
    return r;
}
