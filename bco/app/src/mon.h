/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* UART monitor: peek/poke and a RAM loader, so hardware experiments do not need a 20 minute
 * flash cycle. Polled, no interrupts, output only through say(), so it can be dropped into any app.
 * RX is the stock driver's FIFO: count at +0xf18, pop at +0xe00 (stock ISR 0x601efbd4).
 * Commands (hex, no 0x): r A [N] words, rb A [N] bytes, rh A [N] halfwords, w A V, wb A V, wh A V,
 * l A N (then N raw bytes, answers the byte sum), g A (call A, prints the return value), ? help. */
#include "board.h"

#define MON_RXCNT (*(volatile unsigned *)0xb1001f18)
#define MON_RXB (*(volatile unsigned char *)0xb1001e00)

static inline int mon_getc(void)
{
    return MON_RXCNT ? MON_RXB : -1;
}

static inline void mon_putc(char c)
{
    char s[2] = {c, 0};
    say(s);
}

static inline void mon_hex(unsigned v, int digits)
{
    for (int i = digits - 1; i >= 0; i--)
        mon_putc("0123456789abcdef"[v >> (4 * i) & 15]);
}

static inline unsigned mon_num(const char **p)
{
    unsigned v = 0;
    while (**p == ' ')
        (*p)++;
    for (;; (*p)++) {
        char c = **p;
        if (c >= '0' && c <= '9')
            v = v << 4 | (c - '0');
        else if (c >= 'a' && c <= 'f')
            v = v << 4 | (c - 'a' + 10);
        else
            return v;
    }
}

static inline void mon_exec(const char *l)
{
    const char *p = l;
    char op[3] = {0, 0, 0};
    unsigned a, n, w = 4;
    for (int i = 0; i < 2 && *p >= 'a' && *p <= 'z'; i++)
        op[i] = *p++;
    if (op[0] == 'r' || op[0] == 'w') {
        w = op[1] == 'b' ? 1 : op[1] == 'h' ? 2 : 4;
        a = mon_num(&p) & ~(w - 1); /* aligned: SCTLR.A may be set and there is no abort vector */
        if (op[0] == 'w') {
            unsigned v = mon_num(&p);
            if (w == 1)
                *(volatile unsigned char *)a = v;
            else if (w == 2)
                *(volatile unsigned short *)a = v;
            else
                *(volatile unsigned *)a = v;
            say("ok\r\n");
            return;
        }
        n = mon_num(&p);
        for (n = n ? n : 1; n; n--, a += w) {
            mon_hex(a, 8);
            mon_putc(' ');
            mon_hex(w == 1 ? *(volatile unsigned char *)a : w == 2 ? *(volatile unsigned short *)a : *(volatile unsigned *)a, 2 * w);
            say("\r\n");
        }
    } else if (op[0] == 'l' && !op[1]) {
        volatile unsigned char *d = (void *)mon_num(&p);
        unsigned len = mon_num(&p), sum = 0;
        for (n = 0; n < len; n++) {
            int c;
            for (unsigned t = 0; (c = mon_getc()) < 0; t++)
                if (t > 20000000) { /* ~1 s of nothing: a byte was lost */
                    say("err\r\n");
                    return;
                }
            d[n] = c;
            sum += c;
        }
        asm volatile("mcr p15, 0, %0, c7, c5, 0" : : "r"(0)); /* invalidate I-cache */
        mon_hex(sum, 8);
        say("\r\n");
    } else if (op[0] == 'g' && !op[1]) {
        unsigned (*f)(unsigned, unsigned, unsigned, unsigned) = (void *)mon_num(&p);
        unsigned a = mon_num(&p), b = mon_num(&p), c = mon_num(&p), d = mon_num(&p);
        mon_hex(f(a, b, c, d), 8);
        say("\r\n");
    } else {
        say("r|rb|rh A [N]  w|wb|wh A V  l A N  g A [a0..a3]\r\n");
    }
}

/* Never returns. */
static inline void mon_run(void)
{
    char line[64];
    unsigned n = 0;
    say("mon ready\r\n> ");
    for (;;) {
        int c = mon_getc();
        if (c < 0)
            continue;
        if (c == '\n')
            continue; /* CRLF: only CR ends a line, so the loader never sees a stray LF */
        if (c == '\r') {
            line[n] = 0;
            say("\r\n");
            if (n)
                mon_exec(line);
            n = 0;
            say("> ");
        } else if (n < sizeof line - 1) {
            line[n++] = c;
            mon_putc(c);
        }
    }
}
