/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Rings: a buffer of size elements split into channels that chase each other (the layout the
 * host's bco/app/src/wring.h shares with us). Channel c owns [pos, lim) and hands slots on by
 * moving channel c+1's lim. The core works on the host-written shared headers directly. */
#include "hw.h"

/* volatile keeps gcc from turning this into a memcpy call we do not link */
void copy_bytes(void *dst, const void *src, unsigned n)
{
    volatile unsigned char *d = dst;
    const unsigned char *s = src;
    while (n--)
        *d++ = *s++;
}

void ring_init(struct ring *r, unsigned base, unsigned size, unsigned esize, unsigned nch)
{
    r->peer = 0, r->core_base = 0, r->base = base, r->size = size, r->esize = esize;
    r->nch = nch;
    for (unsigned c = 0; c < nch; c++)
        r->ch[c].shared = 0, r->ch[c].lim = 1, r->ch[c].pos = 1;
    r->ch[0].lim = 0;
}

/* Slots ready on channel c, counting across the wrap. */
unsigned ring_avail(struct ring *r, unsigned c)
{
    unsigned n = r->size + r->ch[c].lim - r->ch[c].pos;
    return n >= r->size ? n - r->size : n;
}

/* Slots ready on channel c without wrapping. */
unsigned ring_space(struct ring *r, unsigned c)
{
    unsigned lim = r->ch[c].lim, pos = r->ch[c].pos;
    return lim < pos ? r->size - pos : lim - pos;
}

static unsigned advance(const struct ring *r, unsigned at, unsigned n)
{
    at += n;
    return at >= r->size ? at - r->size : at;
}

/* Claim n contiguous slots on channel c; 0 and *slot untouched when they are not there. */
int ring_take(struct ring *r, unsigned c, unsigned n, unsigned *slot)
{
    int ok = 0;
    irq_off();
    if (ring_space(r, c) >= n) {
        *slot = r->base + r->esize * r->ch[c].pos;
        r->ch[c].pos = advance(r, r->ch[c].pos, n);
        ok = 1;
    }
    irq_on();
    return ok;
}

/* Hand n slots from channel c to the next one. */
void ring_commit(struct ring *r, unsigned c, unsigned n)
{
    irq_off();
    if (++c == r->nch)
        c = 0;
    r->ch[c].lim = advance(r, r->ch[c].lim, n);
    irq_on();
}

static unsigned step(struct ring *r, unsigned c, unsigned n)
{
    unsigned slot = r->base + r->esize * r->ch[c].pos;
    r->ch[c].pos = advance(r, r->ch[c].pos, n);
    return slot;
}

/* Copy n elements out of (dir 0) or into (dir 1) channel c across the wrap, then commit them. */
void ring_copy(struct ring *r, unsigned c, unsigned n, void *buf, int dir)
{
    unsigned char *b = buf;
    unsigned first = ring_space(r, c), slot;
    if (first > n)
        first = n;
    irq_off();
    slot = step(r, c, first);
    irq_on();
    if (dir) copy_bytes((void *)slot, b, first * r->esize);
    else copy_bytes(b, (void *)slot, first * r->esize);
    if (n > first) {
        irq_off();
        slot = step(r, c, n - first);
        irq_on();
        b += first * r->esize;
        if (dir) copy_bytes((void *)slot, b, (n - first) * r->esize);
        else copy_bytes(b, (void *)slot, (n - first) * r->esize);
    }
    ring_commit(r, c, n);
}
