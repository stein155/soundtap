/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The station's standard firmware features (ADR 0135) with the real wifi.c on the host, built
 * with the shipped image's -DWILD_OWN:
 *
 * - link events: a deauthentication, a disassociation, beacon loss and a roam are queued for the
 *   SCM with the access point, its channel and the reason code;
 * - the regulatory domain: a scan visits only the allowed channels and only listens on the
 *   passive ones; a BSS on a channel outside it is not joined, the access point not started there;
 * - the SCAN's SSID: the directed pass probes for it, also without a profile;
 * - SET_PROFILE's BSSID: a fixed one is the only one joined, a hinted one comes first;
 * - roaming: below the threshold the station scans one channel per visit away from its own and
 *   goes back (0xbc) between visits, holds the SCM's frames while away, and roams only to an
 *   access point at least ROAM_DELTA stronger; the threshold has hysteresis; a fixed BSSID or a
 *   profile without the roam flag never leaves its channel.
 *
 * As in wifi_poll_test.c, the test links without PIE and maps the pages wifi.c reaches by
 * address. Nothing here talks to a speaker. */
#include <assert.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>

#pragma GCC diagnostic ignored "-Wpointer-to-int-cast"
#pragma GCC diagnostic ignored "-Wint-to-pointer-cast"
#define memcpy wifi_memcpy
#define memset wifi_memset
#include "../src/wifi.c"
#undef memcpy
#undef memset
#include "../src/wpa.c"
#include "../src/wsec.c"

const unsigned char wild_own[4], wild_own_end[1];

void netcard_from_port(unsigned ch, const unsigned char *f, unsigned n)
{
    (void)ch, (void)f, (void)n;
}

__attribute__((constructor)) static void map_pages(void)
{
    static const unsigned long at[3] = {(unsigned long)WREG, MB & ~0xffful, 0xb1001000ul};
    static int probe;
    for (int i = 0; i < 3; i++)
        if (mmap((void *)at[i], 0x1000, PROT_READ | PROT_WRITE,
                 MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0) != (void *)at[i]) {
            perror("wifi_roam: map the WiLD registers, mailbox and UART");
            exit(2);
        }
    if ((uintptr_t)&probe >> 32) {
        fputs("wifi_roam: globals above 4 GiB; build with -no-pie\n", stderr);
        exit(2);
    }
}

static unsigned cmdbuf[4096];

static void cmd_ring(void)
{
    memset(cmdbuf, 0, sizeof cmdbuf);
    wr_init(&tx, 0, (uintptr_t)cmdbuf, sizeof cmdbuf / 4, 4, 2);
}

/* The commands written since cmd_ring(), in order: type, channel (scan commands), payload. */
struct cmd {
    unsigned type, chan;
    const unsigned char *pl;
};

static unsigned cmds(struct cmd *c, unsigned max)
{
    unsigned n = 0;
    for (unsigned w = 1; w < sizeof cmdbuf / 4 && cmdbuf[w] && n < max;) {
        unsigned len = cmdbuf[w] >> 16;
        const unsigned char *pl = (const unsigned char *)(cmdbuf + w + 3);
        c[n].type = cmdbuf[w + 1], c[n].pl = pl;
        c[n].chan = c[n].type == 0xc2 ? pl[0x27] : c[n].type == 0xc1 ? pl[0] : c[n].type == 0xbc ? pl[8] : 0;
        n++;
        w += 3 + (len - 8 + 3) / 4;
    }
    return n;
}

static void poll(unsigned t0, unsigned t1)
{
    for (unsigned t = t0; t < t1; t++)
        wifi_poll(t);
}

static void set_bss(unsigned i, const char *ssid, unsigned last, unsigned chan, signed char rssi)
{
    static const unsigned char ap_rates[] = {1, 4, 0x82, 0x84, 0x8b, 0x96};
    struct bss *b = &bsstab[i];
    memset(b, 0, sizeof *b);
    memcpy(b->bssid, "\x02\xaa\xbb\xcc\xdd\x00", 6), b->bssid[5] = (unsigned char)last;
    b->ssidlen = (unsigned char)strlen(ssid), memcpy(b->ssid, ssid, b->ssidlen);
    b->chan = (unsigned char)chan, b->rsn = 1, b->rsnok = 1, b->grp = 4; /* WPA2-PSK, CCMP */
    b->rssi = rssi, b->gen = (unsigned short)scan_gen;
    b->caps = 0x0011, b->seen = 1;
    memcpy(b->ies, ap_rates, sizeof ap_rates), b->ielen = sizeof ap_rates;
}

/* Joined to bsstab[0] ("Lab", channel 6) at the given averaged level, with these profile flags. */
static void joined(unsigned flags, signed char level, unsigned t)
{
    state = WIFI_IDLE; /* nothing to tear down in the core */
    wifi_leave();
    copy(cur.ssid, (const unsigned char *)"Lab", 3);
    cur.ssidlen = 3, have_cur = 1, prof_sec = WIFI_SEC_PSK;
    prof_flags = flags, copy(prof_bssid, bsstab[0].bssid, 6);
    roam_on = (flags & WIFI_ROAM) && !(flags & WIFI_BSSID_FIXED);
    copy(cur.bssid, bsstab[0].bssid, 6), cur.chan = bsstab[0].chan;
    now = t, state = WIFI_UP;
    link_up();
    rssi_avg16 = 16 * level;
    beacons_up = 0, kicked = 0, txnfree = NTXFBUF, txready = 1;
    while (wifi_event_next(&(struct wifi_event){0})) {}
}

int main(void)
{
    struct wifi_event e;
    static struct cmd c[512];
    unsigned n;

    /* ---- link events ---- */
    scan_gen = 3;
    set_bss(0, "Lab", 1, 6, -60);
    nbss = 1;
    cmd_ring();
    joined(0, -60, 1000);
    kicked = 1, kick_sub = 0xc0, kick_reason = 3;
    wifi_poll(1001);
    assert(state == WIFI_FAILED && wifi_event_next(&e) && e.type == WIFI_EV_DEAUTH && e.reason == 3 &&
           !memcmp(e.bssid, bsstab[0].bssid, 6) && e.chan == 6 && !wifi_event_next(&e));
    joined(0, -60, 2000);
    kicked = 1, kick_sub = 0xa0, kick_reason = 8;
    wifi_poll(2001);
    assert(wifi_event_next(&e) && e.type == WIFI_EV_DISASSOC && e.reason == 8);
    joined(0, -60, 3000);
    beacons_up = 1, last_beacon = 3000;
    poll(3001, 3000 + WIFI_BEACON_LOSS_MS);
    assert(state == WIFI_UP && !wifi_event_next(&e));
    wifi_poll(3000 + WIFI_BEACON_LOSS_MS);
    assert(state == WIFI_FAILED && wifi_event_next(&e) && e.type == WIFI_EV_BEACON_LOSS && e.reason == 0);
    /* a full queue keeps the newest */
    for (unsigned i = 0; i < NEVQ + 2; i++)
        post(WIFI_EV_DEAUTH, i);
    for (unsigned i = 2; i < NEVQ + 2; i++)
        assert(wifi_event_next(&e) && e.reason == i);
    assert(!wifi_event_next(&e));

    /* ---- regulatory domain: channels 1..11, 10 and 11 passive; the SCAN's SSID ---- */
    wifi_leave();
    state = WIFI_IDLE, have_cur = 0;
    assert(wifi_set_regdom((const unsigned char *)"US", 0x07ff, 0x0600, 30) == 0);
    assert(wifi_set_regdom((const unsigned char *)"XX", 0, 0, 20) == -1 && reg_chans == 0x07ff);
    assert(wifi_set_regdom((const unsigned char *)"XX", 0x07ff, 0, 31) == -1);
    cmd_ring();
    wifi_scan_ssid((const unsigned char *)"Hid", 3);
    assert(wifi_scan(1) == -1);
    poll(10000, 20000);
    assert(state == WIFI_IDLE);
    n = cmds(c, 512);
    unsigned probes[14] = {0}, listens[14] = {0}, directed = 0;
    for (unsigned i = 0; i < n; i++) {
        if (c[i].type == 0xc2) {
            probes[c[i].chan]++;
            if (c[i].pl[6]) {
                assert(c[i].pl[6] == 3 && !memcmp(c[i].pl + 7, "Hid", 3));
                directed++;
            }
        } else if (c[i].type == 0xc1) {
            listens[c[i].chan]++;
        }
    }
    for (unsigned ch = 1; ch <= 13; ch++) {
        assert(probes[ch] == (ch <= 9 ? SCAN_PASSES : 0));
        assert(listens[ch] == (ch == 10 || ch == 11 ? SCAN_PASSES : 0));
    }
    assert(directed == 9);
    /* nothing to join on a channel outside the domain; no access point on a passive one */
    copy(cur.ssid, (const unsigned char *)"Lab", 3);
    cur.ssidlen = 3, have_cur = 1, prof_sec = WIFI_SEC_PSK;
    set_bss(0, "Lab", 1, 12, -40);
    nbss = 1;
    assert(pick() == -1);
    bsstab[0].chan = 11; /* passive, but heard there: joinable */
    assert(pick() == 0);
    unsigned char body[16] = {0};
    assert(wifi_ap_start(11, 100, body, sizeof body) == -1 && wifi_ap_start(12, 100, body, sizeof body) == -1);
    assert(wifi_set_regdom((const unsigned char *)"00", 0x1fff, 0, 20) == 0);

    /* ---- the profile's BSSID ---- */
    scan_gen = 9;
    set_bss(0, "Lab", 1, 6, -75);
    set_bss(1, "Lab", 2, 1, -50);
    nbss = 2;
    have_cur = 1, roaming = 0;
    prof_flags = 0;
    assert(pick() == 1);                                   /* the strongest */
    prof_flags = WIFI_BSSID_HINT, copy(prof_bssid, bsstab[0].bssid, 6);
    assert(pick() == 0);                                   /* the hinted one first */
    prof_flags = WIFI_BSSID_FIXED;
    assert(pick() == 0);
    copy(prof_bssid, (const unsigned char *)"\x02\x00\x00\x00\x00\x77", 6);
    assert(pick() == -1);                                  /* fixed and not heard: nothing */
    prof_flags = WIFI_BSSID_HINT;
    assert(pick() == 1);                                   /* hinted and not heard: the strongest */

    /* ---- roaming: weak on channel 6, a 20 dB stronger AP of the SSID on channel 1 ---- */
    scan_gen = 20;
    set_bss(0, "Lab", 1, 6, -80);
    set_bss(1, "Lab", 2, 1, -60);
    set_bss(2, "Other", 3, 1, -30);                       /* another network: never a candidate */
    nbss = 3;
    cmd_ring();
    joined(WIFI_ROAM, -80, 100000);
    unsigned t = 100001, off_seen = 0, held = 0, dropped = 0;
    static const unsigned char frame[60] = {0x02, 0, 0, 0, 0, 1};
    copy(own, (const unsigned char *)"\x02\x11\x22\x33\x44\x55", 6);
    unsigned char f[60];
    memcpy(f, frame, sizeof f), memcpy(f + 6, own, 6);
    for (; t < 100000 + 12 * 1000 && state == WIFI_UP; t++) {
        wifi_poll(t);
        if (bg_phase == BG_OFF) {
            off_seen++;
            held += wifi_tx_free() == 0;
            unsigned d = wifi_tx_drop;
            dropped += radio_tx_eth(f, sizeof f) == -1 && wifi_tx_drop == d + 1;
            if (bg_chan == 1) /* the probe on channel 1 is answered */
                bsstab[1].gen = (unsigned short)scan_gen, bsstab[1].rssi = -60;
        }
    }
    assert(off_seen && held == off_seen && dropped == off_seen);
    assert(roaming && state == WIFI_JOINING && !memcmp(roam_bssid, bsstab[1].bssid, 6) &&
           !memcmp(cur.bssid, bsstab[1].bssid, 6) && cur.chan == 1);
    n = cmds(c, 512);
    unsigned visits = 0, homes = 0, last = 0;
    for (unsigned i = 0; i < n; i++) {
        if (c[i].type == 0xc2) {
            assert(c[i].chan != 6 && c[i].chan > last); /* never the own channel; each once, in order */
            assert(c[i].pl[6] == 3 && !memcmp(c[i].pl + 7, "Lab", 3)); /* directed at the SSID */
            last = c[i].chan, visits++;
            /* the next command takes the radio home to the access point */
            assert(i + 1 < n && c[i + 1].type == 0xbc && c[i + 1].chan == 6 &&
                   !memcmp(c[i + 1].pl, bsstab[0].bssid, 6));
            homes++;
        }
    }
    assert(visits == 12 && homes == 12);
    /* the roam completes: the SCM hears of it */
    jstep = J_4WAY, sm.state = WPA_DONE, keyed = 1, keys_due = 0;
    join_step();
    assert(state == WIFI_UP && !roaming && wifi_event_next(&e) && e.type == WIFI_EV_ROAMED &&
           !memcmp(e.bssid, bsstab[1].bssid, 6) && e.chan == 1);

    /* a candidate only 5 dB stronger: no roam, the next scan twice as far off */
    scan_gen = 30;
    set_bss(0, "Lab", 1, 6, -80);
    set_bss(1, "Lab", 2, 1, -75);
    nbss = 2;
    cmd_ring();
    joined(WIFI_ROAM, -80, 200000);
    for (t = 200001; t < 200000 + 12 * 1000; t++) {
        wifi_poll(t);
        if (bg_phase == BG_OFF && bg_chan == 1)
            bsstab[1].gen = (unsigned short)scan_gen;
    }
    assert(state == WIFI_UP && !roaming && bg_phase == BG_IDLE && bg_interval == 2 * BG_SHORT_MS &&
           bg_next == bg_last + 2 * BG_SHORT_MS);

    /* hysteresis: weak below -70, strong again only from -65 */
    weak = 0, rssi_avg16 = 16 * -68;
    bg_step();
    assert(!weak);
    rssi_avg16 = 16 * -71;
    bg_step();
    assert(weak && bg_phase == BG_IDLE); /* the next scan waits out its interval */
    rssi_avg16 = 16 * -68;
    bg_step();
    assert(weak);
    rssi_avg16 = 16 * -65;
    bg_step();
    assert(!weak);
    rssi_avg16 = 16 * -69;
    bg_step();
    assert(!weak);

    /* strong, a fixed BSSID or no roam flag: the radio never leaves its channel */
    static const struct {
        unsigned flags;
        signed char level;
    } stay[] = {{WIFI_ROAM, -60}, {WIFI_ROAM | WIFI_BSSID_FIXED, -85}, {0, -85}};
    for (unsigned k = 0; k < sizeof stay / sizeof *stay; k++) {
        cmd_ring();
        joined(stay[k].flags, stay[k].level, 300000);
        poll(300001, 300000 + 60000);
        assert(state == WIFI_UP && bg_phase == BG_IDLE && cmds(c, 512) == 0);
    }

    printf("ok: link events, regulatory channels, the SCAN's SSID, the profile's BSSID and roaming\n");
    return 0;
}
