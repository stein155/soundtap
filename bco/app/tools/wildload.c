/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Dev-phase iteration aid: stream a WiLD core image into the BCO app's runtime staging area and
 * boot it (mgmt WILD_WRITE/WILD_BOOT), without re-flashing the app. Runs on the SCM with the
 * soundtap_bco driver bound: the requests go out as raw channel-0 transfers on bcolink0 (the
 * driver passes ETH_P_802_EX1 skbs through). Replies go to the driver, not to us; the result is
 * on the BCO UART ("wild ram boot" / "wild ram bad crc").
 * Build: arm-linux-gnueabihf-gcc -static -O2 -Isrc tools/wildload.c -o wildload
 * Usage: wildload wild.bin [iface]     load and boot
 *        wildload -p OFFSET LEN [iface] dump core SRAM words to the UART ("peek ...") */
#include <stdlib.h>
#include <arpa/inet.h>
#include <net/if.h>
#include <netpacket/packet.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>
#include "bcolink.h"

static int fd;
static struct sockaddr_ll sa;

static unsigned crc32(const unsigned char *p, unsigned n)
{
    unsigned c = ~0u;
    while (n--) {
        c ^= *p++;
        for (int k = 0; k < 8; k++)
            c = c >> 1 ^ (0xedb88320u & -(c & 1));
    }
    return ~c;
}

static int req(unsigned char type, unsigned short seq, const unsigned char *pl, unsigned plen)
{
    static unsigned char q[BCOLINK_HDR + BCOLINK_MAX];
    unsigned n = 6 + plen;
    bcolink_put_hdr(q, BCOLINK_CH_CTRL, n);
    q[2] = 1, q[3] = type, q[4] = seq >> 8, q[5] = seq, q[6] = plen >> 8, q[7] = plen;
    memcpy(q + 8, pl, plen);
    if (sendto(fd, q, BCOLINK_HDR + n, 0, (struct sockaddr *)&sa, sizeof sa) < 0)
        return perror("sendto"), -1;
    usleep(100000); /* the app handles one request per main-loop pass; a busy pass (UART) is slow */
    return 0;
}

static void be32(unsigned char *p, unsigned v)
{
    p[0] = v >> 24, p[1] = v >> 16, p[2] = v >> 8, p[3] = v;
}

int main(int argc, char **argv)
{
    static unsigned char img[0x10000], b[4 + 1024];
    int peek = argc > 3 && !strcmp(argv[1], "-p");
    FILE *f = argc > 1 && !peek ? fopen(argv[1], "rb") : NULL;
    if (!f && !peek)
        return fprintf(stderr, "usage: wildload wild.bin [iface] | wildload -p OFFSET LEN [iface]\n"), 2;
    unsigned len = 0, seq = 0xc000 | (getpid() & 0xfff);
    if (f) {
        len = fread(img, 1, sizeof img, f);
        fclose(f);
    }
    fd = socket(AF_PACKET, SOCK_RAW, htons(0x88b5));
    sa.sll_family = AF_PACKET;
    sa.sll_protocol = htons(0x88b5);
    sa.sll_ifindex = if_nametoindex(argc > (peek ? 4 : 2) ? argv[peek ? 4 : 2] : "bcolink0");
    if (fd < 0 || !sa.sll_ifindex)
        return perror("socket/iface"), 1;
    if (peek) {
        unsigned off = strtoul(argv[2], 0, 0), n = strtoul(argv[3], 0, 0);
        be32(b, off);
        b[4] = n >> 8, b[5] = n;
        return req(11, seq, b, 6) ? 1 : 0;
    }
    for (unsigned off = 0; off < len; off += 1024) {
        unsigned n = len - off < 1024 ? len - off : 1024;
        be32(b, off);
        memcpy(b + 4, img + off, n);
        if (req(9, seq++, b, 4 + n))
            return 1;
    }
    be32(b, len);
    be32(b + 4, crc32(img, len));
    if (req(10, seq++, b, 8))
        return 1;
    printf("sent %u bytes crc %08x\n", len, crc32(img, len));
    return 0;
}
