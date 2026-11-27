/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* The station's main-loop side (src/wifi.c) with the real wifi.c on the host, built with the
 * shipped image's -DWILD_OWN (build-app.sh link-own):
 *
 * - the shipped build writes nothing to the UART while the link is up or a scan runs: say()
 *   busy-waits per character, and USB and the RX rings go unserviced meanwhile. Those lines
 *   belong to the trace build (-DBCO_DEBUG_TRACE, link-own-trace);
 * - a scan's last pass probes for the profile's SSID, so a hidden network answers; the first
 *   pass stays a wildcard;
 * - the join takes the strongest matching BSS, on a tie the one heard last, and carries its
 *   capability over for the association request.
 *
 * As in wifi_join_test.c, the test links without PIE and maps the pages wifi.c reaches by
 * address: the WiLD registers, the mailbox and the UART. Nothing here talks to a speaker. */
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

/* wild_own.S's core image; the test never boots it */
const unsigned char wild_own[4], wild_own_end[1];

void netcard_from_port(unsigned ch, const unsigned char *f, unsigned n)
{
    (void)ch, (void)f, (void)n;
}

#define UART_PAGE 0xb1001000ul
#define THR (*(volatile unsigned char *)0xb1001e80) /* board.h say(): the byte register */

__attribute__((constructor)) static void map_pages(void)
{
    static const unsigned long at[3] = {(unsigned long)WREG, MB & ~0xffful, UART_PAGE};
    static int probe;
    for (int i = 0; i < 3; i++)
        if (mmap((void *)at[i], 0x1000, PROT_READ | PROT_WRITE,
                 MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0) != (void *)at[i]) {
            perror("wifi_poll: map the WiLD registers, mailbox and UART");
            exit(2);
        }
    if ((uintptr_t)&probe >> 32) {
        fputs("wifi_poll: globals above 4 GiB; build with -no-pie\n", stderr);
        exit(2);
    }
}

/* The host side of the command ring, large enough that a whole scan fits without wrapping. */
static unsigned cmdbuf[2048];

static void cmd_ring(void)
{
    memset(cmdbuf, 0, sizeof cmdbuf);
    wr_init(&tx, 0, (uintptr_t)cmdbuf, sizeof cmdbuf / 4, 4, 2);
}

/* Poll from t0 to t1 (1 ms steps); fails on the first character sent to the UART. */
static void poll_quiet(unsigned t0, unsigned t1, const char *what)
{
    for (unsigned t = t0; t < t1; t++) {
        wifi_poll(t);
        if (THR) {
            fprintf(stderr, "wifi_poll: UART output at %u ms while %s\n", t, what);
            exit(1);
        }
    }
}

static void set_bss(unsigned i, const char *ssid, unsigned last, signed char rssi, unsigned age,
                    unsigned short caps)
{
    static const unsigned char ap_rates[] = {1, 4, 0x82, 0x84, 0x8b, 0x96};
    struct bss *b = &bsstab[i];
    memset(b, 0, sizeof *b);
    memcpy(b->bssid, "\x02\xaa\xbb\xcc\xdd\x00", 6), b->bssid[5] = (unsigned char)last;
    b->ssidlen = (unsigned char)strlen(ssid), memcpy(b->ssid, ssid, b->ssidlen);
    b->chan = 6, b->rsn = 1, b->rsnok = 1, b->grp = 4; /* WPA2-PSK, CCMP: joinable */
    b->rssi = rssi, b->gen = (unsigned short)(scan_gen - age);
    b->seen = (unsigned short)(100 - i), b->caps = caps; /* most beacons on the first entries */
    memcpy(b->ies, ap_rates, sizeof ap_rates), b->ielen = sizeof ap_rates;
}

int main(void)
{
    /* ---- quiet while up: no heartbeat, no per-state trace ---- */
    copy(cur.ssid, (const unsigned char *)"Lab", 3);
    cur.ssidlen = 3, have_cur = 1;
    state = WIFI_UP, txnfree = NTXFBUF; /* buffers free: the TX-wedge self-heal stays out */
    THR = 0;
    poll_quiet(1, 10000, "up");
    assert(state == WIFI_UP);

    /* ---- a whole scan, quiet, with the last pass directed at "Lab" ---- */
    cmd_ring();
    state = WIFI_IDLE;
    assert(wifi_scan(1) == -1 && state == WIFI_SCANNING && !THR);
    poll_quiet(10000, 20000, "scanning");
    assert(state == WIFI_IDLE);
    unsigned probes = 0, directed = 0;
    for (unsigned w = 1; w < sizeof cmdbuf / 4 && cmdbuf[w];) {
        unsigned len = cmdbuf[w] >> 16, type = cmdbuf[w + 1];
        const unsigned char *pl = (const unsigned char *)(cmdbuf + w + 3);
        if (type == 0xc2) {
            probes++;
            assert(!memcmp(pl, "\xff\xff\xff\xff\xff\xff", 6)); /* any BSSID */
            if (pl[6]) {
                assert(pl[6] == 3 && !memcmp(pl + 7, "Lab", 3) && probes > NCHAN);
                directed++;
            } else {
                assert(probes <= NCHAN); /* the first pass: wildcard */
            }
        }
        w += 3 + (len - 8 + 3) / 4;
    }
    assert(probes == SCAN_PASSES * NCHAN && directed == NCHAN);

    /* no profile: every probe is a wildcard */
    cmd_ring();
    have_cur = 0;
    assert(wifi_scan(1) == -1);
    poll_quiet(20000, 30000, "scanning");
    for (unsigned w = 1; w < sizeof cmdbuf / 4 && cmdbuf[w]; w += 3 + ((cmdbuf[w] >> 16) - 8 + 3) / 4)
        assert(cmdbuf[w + 1] != 0xc2 || !((const unsigned char *)(cmdbuf + w + 3))[6]);

    /* ---- the join takes the strongest "Lab", not the most heard ---- */
    copy(cur.ssid, (const unsigned char *)"Lab", 3);
    cur.ssidlen = 3, have_cur = 1;
    scan_gen = 7;
    set_bss(0, "Lab", 1, -78, 0, 0x0011);
    set_bss(1, "Lab", 2, -55, 2, 0x0411);
    set_bss(2, "Lab", 3, -55, 0, 0x0431); /* as strong, heard in the latest scan */
    set_bss(3, "Lab", 4, 0, 0, 0x0011);   /* no level yet: the weakest */
    set_bss(4, "Lab2", 5, -30, 0, 0x0011);
    set_bss(5, "Lab", 6, -20, 0, 0x0011);
    bsstab[5].rsn = bsstab[5].rsnok = 0;  /* open: not ours */
    set_bss(6, "Lab", 7, -25, 0, 0x0011);
    bsstab[6].chan = 14;                  /* off our channels */
    nbss = 7;
    assert(pick() == 2);
    bsstab[1].gen = scan_gen;             /* both in the latest scan: the first stays */
    assert(pick() == 1);
    bsstab[1].gen = (unsigned short)(scan_gen - 2);
    nbss = 1;
    assert(pick() == 0);
    bsstab[0].rssi = 0;
    assert(pick() == 0);                  /* no level is still a match */
    nbss = 7, bsstab[0].rssi = -78;

    cmd_ring();
    state = WIFI_JOINING;
    join_attempt();
    assert(state == WIFI_JOINING && jstep == J_SETUP);
    assert(!memcmp(cur.bssid, bsstab[2].bssid, 6) && cur.caps == 0x0431);

    printf("ok: the shipped build is quiet on the UART, probes the profile's SSID and joins the strongest BSS\n");
    return 0;
}
