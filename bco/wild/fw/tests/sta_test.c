/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Station module (src/sta.c) through the command ring as bco/app/src/wifi.c drives it: the peer
 * table, CCMP key install and delete with their replies, the transmit packet number and the
 * receive-key lookup. Command layouts are wifi.c's set_key() and the PEER_ADD/JOIN_DONE bodies. */
#define SIM_STA_COPY sta_copy
void sta_copy(void *dst, const void *src, unsigned n);
#include "core_sim.h"

/* src/sta.c's copies: after each one, where interrupts are on, the IRQ comes and enciphers a frame
 * for slot 0 as tx_stage_done does (sta_txkey), the key it gets and its PN noted. */
static int irq_watch, irq_seen;
static unsigned char irq_tk[16];
static unsigned long long irq_pn;
void sta_copy(void *dst, const void *src, unsigned n)
{
    copy_bytes(dst, src, n);
    if (irq_watch && !irq_depth) {
        const unsigned char *k = sta_txkey(0, &irq_pn);
        irq_seen = k != 0;
        if (k)
            memcpy(irq_tk, k, 16);
    }
}

static int soft; /* sta_rxkey: the app deciphers under this key (ADR 0136) */

static const unsigned char TK[16] = {0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
                                     0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f};
static const unsigned char GTK[16] = {0xe0, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7,
                                      0xe8, 0xe9, 0xea, 0xeb, 0xec, 0xed, 0xee, 0xef};

static void peer_add(const unsigned char *mac, unsigned slot)
{
    unsigned char pr[10] = {0};
    memcpy(pr, mac, 6), pr[6] = slot;
    host_cmd(0xb5, 0x12, 0x11, pr, sizeof pr);
}

/* wifi.c set_key(): pairwise type 13 (52 bytes, MAC at 12, key at 20) / delete 14 (MAC at 0);
 * group type 11 (40 bytes, index at 4, key at 8, Key RSC at 24) / delete 12 (index at 0). */
static void set_key(int pairwise, unsigned idx, const unsigned char *mac, const unsigned char *key)
{
    unsigned char p[52] = {0};
    if (pairwise) {
        if (key) {
            p[8] = 4, p[19] = 16;
            memcpy(p + 20, key, 16);
        }
        memcpy(p + (key ? 12 : 0), mac, 6);
        host_cmd(key ? 13 : 14, 0x12, 1, p, key ? 52 : 6);
    } else {
        if (key) {
            p[0] = 4, p[4] = idx, p[7] = 16;
            memcpy(p + 8, key, 16);
        } else {
            p[0] = idx;
        }
        host_cmd(key ? 11 : 12, 0x12, 1, p, key ? 40 : 2);
    }
}

static void run(void)
{
    nev = 0;
    pump(4);
    host_events();
}

/* A key of another cipher than CCMP is the app's (ADR 0136): it marks the slot soft, never gives
 * the core a key to encipher with, and a CCMP install over it makes the slot the engine's again. */
static void soft_keys(void)
{
    unsigned char pr[10] = {0}, p[52] = {0};
    unsigned long long pn = 0, *rxpn;
    sim_init();
    memcpy(pr, BSSID, 6);
    host_cmd(0xb5, 0x12, 0x11, pr, sizeof pr);
    p[8] = 2, p[19] = 16;                       /* TKIP pairwise, no key bytes used */
    memcpy(p + 12, BSSID, 6);
    host_cmd(13, 0x12, 1, p, 52);
    memset(p, 0, sizeof p);
    p[0] = 1, p[4] = 0, p[7] = 16;              /* WEP-40 default key 0 */
    host_cmd(11, 0x12, 1, p, 40);
    pump(4);
    soft = 0;
    CHECK(sta_rxkey(0, 0, 0, &rxpn, &soft) && soft, "TKIP pairwise: the app deciphers");
    CHECK(!sta_txkey(0, &pn), "and the core never enciphers with it");
    soft = 0;
    CHECK(sta_rxkey(6, 1, 0, &rxpn, &soft) && soft, "WEP default key 0: the app's");
    set_key(1, 0, BSSID, TK);                   /* a CCMP rekey: the engine's again */
    pump(4);
    soft = 1;
    CHECK(sta_rxkey(0, 0, 0, &rxpn, &soft) && !soft && sta_txkey(0, &pn), "CCMP over it: hardware");
}

/* A rekey seen from the IRQ: the old key with its PN, or the new key from PN 1, never the new key
 * with the old key's PN (after which the access point drops every frame numbered from 1 again as a
 * replay). */
static void rekey_irq(void)
{
    static const unsigned char TK2[16] = {0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27,
                                          0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f};
    unsigned long long pn = 0;
    const unsigned char *k;
    sim_init();
    peer_add(BSSID, 0);
    set_key(1, 0, BSSID, TK);
    run();
    for (unsigned i = 0; i < 100; i++)
        sta_txkey(0, &pn);
    irq_watch = 1, irq_seen = 0;
    set_key(1, 0, BSSID, TK2);
    run();
    irq_watch = 0;
    CHECK(!irq_seen || (!memcmp(irq_tk, TK, 16) && irq_pn == 101) || (!memcmp(irq_tk, TK2, 16) && irq_pn == 1),
          "the IRQ saw a half-done rekey: PN %llu under the %s key", irq_pn,
          memcmp(irq_tk, TK2, 16) ? "old" : "new");
    k = sta_txkey(0, &pn);
    CHECK(k && !memcmp(k, TK2, 16) && pn == (irq_seen && !memcmp(irq_tk, TK2, 16) ? 2 : 1),
          "the new key's PNs go on from its first frame (%llu)", pn);
}

int main(void)
{
    rekey_irq();
    soft_keys();
    unsigned long long pn = 0, *rxpn = 0;
    const unsigned char *k;
    const struct event *e;
    sim_init();

    /* peer table */
    peer_add(BSSID, 0);
    run();
    CHECK(peer_slot(BSSID) == 0, "PEER_ADD puts the AP in slot 0");
    CHECK(peer_slot(OWN) == 6, "an unknown MAC has no slot");
    peer_add(OWN, 6);
    peer_add(OWN, 200);
    run();
    CHECK(peer_slot(OWN) == 6, "a slot past the table is ignored");
    CHECK(nev == 0, "PEER_ADD has no reply");

    /* pairwise key: slot 0, PN counts from 1, replies to the station manager and the sender */
    set_key(1, 0, BSSID, TK);
    run();
    k = sta_txkey(0, &pn);
    CHECK(k && !memcmp(k, TK, 16) && pn == 1, "pairwise key installed, first PN 1");
    k = sta_txkey(0, &pn);
    CHECK(k && pn == 2, "PN increments per frame");
    e = find_event(0xba);
    CHECK(e && e->module == 0x11 && e->src == 0x12 && e->len == 2 && e->payload[0] == 0,
          "key notification names slot 0");
    e = find_event(0x1b);
    CHECK(e && e->module == 1 && e->len == 2 && e->payload[0] == 0 && e->payload[1] == 0,
          "install acknowledged to the sender");
    k = sta_rxkey(0, 0, 0, &rxpn, &soft);
    CHECK(k && !memcmp(k, TK, 16) && rxpn && *rxpn == 0, "receive key with PN 0");
    CHECK(!sta_rxkey(1, 0, 0, &rxpn, &soft) && !sta_rxkey(6, 0, 0, &rxpn, &soft) && !sta_rxkey(99, 0, 0, &rxpn, &soft),
          "no receive key for other slots");
    CHECK(!sta_txkey(6, &pn) && !sta_txkey(1000, &pn), "no transmit key past the table");

    set_key(1, 0, BSSID, TK); /* a reinstall (rekey) restarts both packet numbers */
    run();
    *rxpn = 77;
    set_key(1, 0, BSSID, TK);
    run();
    sta_txkey(0, &pn);
    CHECK(pn == 1 && *rxpn == 0, "reinstall restarts the PNs");

    /* a key for a MAC that is not in the table installs nothing (slot 6 in the notification) */
    set_key(1, 0, OWN, GTK);
    run();
    e = find_event(0xba);
    CHECK(e && e->payload[0] == 6, "notification says slot 6 for an unknown peer");
    e = find_event(0x1b);
    CHECK(e && e->module == 1 && e->len == 2 && e->payload[0] == 1 && e->payload[1] == 0,
          "the install is answered as failed (status %u)", e ? e->payload[0] : 0u);
    k = sta_txkey(0, &pn);
    CHECK(k && !memcmp(k, TK, 16), "the AP's key is untouched");

    /* pairwise delete */
    set_key(1, 0, BSSID, 0);
    run();
    CHECK(!sta_txkey(0, &pn) && !sta_rxkey(0, 0, 0, &rxpn, &soft), "pairwise key deleted");
    e = find_event(0xbb);
    CHECK(e && e->module == 0x11 && e->payload[0] == 0, "delete notification");
    CHECK(find_event(0x1c) && find_event(0x1c)->module == 1, "delete acknowledged");

    /* group keys: index 0..3, key id taken modulo 4 on receive */
    set_key(0, 2, 0, GTK);
    run();
    k = sta_rxkey(6, 1, 2, &rxpn, &soft);
    CHECK(k && !memcmp(k, GTK, 16) && *rxpn == 0, "group key 2 installed");
    CHECK(sta_rxkey(0, 1, 6, &rxpn, &soft) == k, "key id 6 is index 2");
    CHECK(!sta_rxkey(0, 1, 1, &rxpn, &soft), "no group key 1");
    CHECK(find_event(0x1b) && find_event(0x1b)->payload[0] == 0, "group install acknowledged");
    /* the Key RSC at bytes 24-29 is the group key's last packet number: frames up to it are
     * replays (a 24-byte install, before the RSC was sent, starts at 0 as above) */
    {
        unsigned char p[40] = {4, 0, 0, 0, 3, 0, 0, 16};
        memcpy(p + 8, GTK, 16);
        p[24] = 0x34, p[25] = 0x12, p[29] = 0x01;
        host_cmd(11, 0x12, 1, p, sizeof p);
        run();
        CHECK(sta_rxkey(6, 1, 3, &rxpn, &soft) && *rxpn == 0x010000001234ull, "group key 3 from its RSC");
        host_cmd(11, 0x12, 1, p, 24);
        run();
        CHECK(sta_rxkey(6, 1, 3, &rxpn, &soft) && *rxpn == 0, "an install without the RSC starts at 0");
        set_key(0, 3, 0, 0);
        run();
    }
    set_key(0, 4, 0, TK);
    run();
    e = find_event(0x1b);
    CHECK(e && e->payload[0] == 1, "a group index past 3 is answered as failed");
    set_key(0, 255, 0, TK);
    run();
    for (unsigned i = 0; i < 4; i++)
        CHECK(i == 2 ? sta_rxkey(0, 1, i, &rxpn, &soft) == k : !sta_rxkey(0, 1, i, &rxpn, &soft),
              "index %u after out-of-range installs", i);
    set_key(0, 7, 0, 0);
    run();
    CHECK(sta_rxkey(0, 1, 2, &rxpn, &soft) != 0, "delete of index 7 touches nothing");
    set_key(0, 2, 0, 0);
    run();
    CHECK(!sta_rxkey(0, 1, 2, &rxpn, &soft), "group key 2 deleted");

    /* PEER_DEL, drop all, JOIN_DONE */
    peer_add(BSSID, 0);
    peer_add(OWN, 3);
    run();
    CHECK(peer_slot(OWN) == 3, "second peer in slot 3");
    {
        unsigned char d[8] = {0};
        d[6] = 3;
        host_cmd(0xb6, 0x12, 0x11, d, sizeof d);
        run();
    }
    CHECK(peer_slot(OWN) == 6 && peer_slot(BSSID) == 0, "PEER_DEL removes only slot 3");
    /* a pairwise key goes with its peer: PEER_DEL, a new MAC in the slot, drop-all, JOIN_DONE */
    set_key(1, 0, BSSID, TK);
    peer_add(OWN, 3);
    set_key(1, 0, OWN, TK);
    run();
    CHECK(sta_txkey(0, &pn) && sta_txkey(3, &pn), "keys for slots 0 and 3");
    peer_add(OWN, 3);
    run();
    CHECK(sta_txkey(3, &pn), "PEER_ADD of the same MAC keeps its key");
    peer_add(GTK, 3); /* another MAC in slot 3 */
    run();
    CHECK(!sta_txkey(3, &pn) && !sta_rxkey(3, 0, 0, &rxpn, &soft), "a new peer in the slot has no key");
    peer_add(OWN, 3);
    set_key(1, 0, OWN, TK);
    {
        unsigned char d[8] = {0};
        d[6] = 3;
        host_cmd(0xb6, 0x12, 0x11, d, sizeof d);
    }
    run();
    CHECK(!sta_txkey(3, &pn) && sta_txkey(0, &pn), "PEER_DEL drops slot 3's key only");
    host_cmd(0xb7, 0x12, 0x11, 0, 0);
    run();
    CHECK(peer_slot(BSSID) == 6, "drop-all empties the table");
    CHECK(!sta_txkey(0, &pn) && !sta_rxkey(0, 0, 0, &rxpn, &soft), "and drops the keys");
    peer_add(BSSID, 0);
    set_key(1, 0, BSSID, TK);
    {
        unsigned char jd[6] = {1, 1, 0, 0, 0, 0};
        host_cmd(0xb8, 0x12, 11, jd, sizeof jd);
    }
    run();
    CHECK(peer_slot(BSSID) == 6 && privacy == 1, "JOIN_DONE resets the table, keeps privacy");
    CHECK(!sta_txkey(0, &pn), "JOIN_DONE drops the pairwise keys");
    CHECK(sta_cmd(0x99, (const unsigned char *)"\0\0\0\0\0\0\0\0", 8, 0x12, 1) == 1,
          "an unknown station command is refused");

    /* A command shorter than its layout is refused before it reads past its payload (each one
     * here sits in a heap block of exactly its length, so ASan sees a read beyond it): no
     * effect, counted, and a key install answers failed. */
    {
        static const struct { unsigned type, len; } shortc[] = {
            {0x0b, 23}, {0x0b, 0}, {0x0c, 0}, {0x0d, 0x23}, {0x0d, 12}, {0x0e, 5},
            {0xb5, 6}, {0xb6, 6}, {0xb8, 0},
        };
        sim_init();
        peer_add(BSSID, 0);
        set_key(1, 0, BSSID, TK);
        set_key(0, 1, 0, GTK);
        run();
        privacy = 7;
        for (unsigned i = 0; i < sizeof shortc / sizeof shortc[0]; i++) {
            unsigned char *p = malloc(shortc[i].len ? shortc[i].len : 1);
            memset(p, 0, shortc[i].len ? shortc[i].len : 1);
            if (shortc[i].len > 0x11)
                memcpy(p + 0xc, BSSID, 6);      /* a key install for the AP, key cut short */
            if (shortc[i].type == 0xb5 || shortc[i].type == 0xb6)
                memcpy(p, OWN, 6);
            nev = 0;
            CHECK(sta_cmd(shortc[i].type, p, shortc[i].len, 0x12, 1) == 1,
                  "type %#x with %u bytes refused", shortc[i].type, shortc[i].len);
            free(p);
            pump(4);
            host_events();
            e = find_event(0x1b);
            if (shortc[i].type == 0x0b || shortc[i].type == 0x0d)
                CHECK(e && e->payload[0] == 1, "type %#x answered as failed", shortc[i].type);
            else
                CHECK(nev == 0, "type %#x has no reply", shortc[i].type);
        }
        CHECK(rec[RC_CMDBAD] == sizeof shortc / sizeof shortc[0], "each one counted (%u)",
              rec[RC_CMDBAD]);
        k = sta_txkey(0, &pn);
        CHECK(k && !memcmp(k, TK, 16) && pn == 1, "the AP's pairwise key untouched");
        CHECK(sta_rxkey(0, 1, 1, &rxpn, &soft) && !sta_rxkey(0, 1, 0, &rxpn, &soft), "group keys untouched");
        CHECK(peer_slot(BSSID) == 0 && peer_slot(OWN) == 6 && privacy == 7, "peer table untouched");
    }
    CHECK(irq_depth == 0, "critical sections balanced");
    return done("sta");
}
