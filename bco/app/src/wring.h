/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Host<->WiLD command rings, ported from the stock helpers (init 0x6004e590, link 0x6004e5ec,
 * reserve 0x6004e4dc, commit 0x6004e818, avail 0x6004e76c, space 0x6004e7c4).
 * A ring is a word buffer split into channels that chase each other: channel c may use the
 * slots [pos, lim) and hands them on by moving channel c+1's lim. The shared header in WILD
 * SRAM and a host-local copy are linked so that every field lives on exactly one side; the
 * fields the other side owns are poisoned (0xaaaaaaaa local, 0xbbbbbbbb shared). */
#include <stdint.h>
struct wch { unsigned shared, lim, pos; };
struct wring {
    struct wring *peer;   /* shared header (local copy only) */
    unsigned core_base;   /* buffer address as the core sees it */
    uintptr_t base;       /* buffer address for the host (pointer-sized so host tests work) */
    unsigned size, esize; /* in elements; element size in bytes */
    unsigned char nch, pad[7];
    struct wch ch[5];
};

#define POISON_LOCAL 0xaaaaaaaau
#define POISON_SHARED 0xbbbbbbbbu

static void wr_init(volatile struct wring *r, unsigned core_base, uintptr_t base, unsigned size,
                    unsigned esize, unsigned nch)
{
    r->peer = 0, r->core_base = core_base, r->base = base, r->size = size, r->esize = esize;
    r->nch = nch;
    for (unsigned c = 0; c < nch; c++)
        r->ch[c].shared = 0, r->ch[c].lim = 1, r->ch[c].pos = 1;
    r->ch[0].lim = 0;
}

/* mode 1: the host produces on c0 (TX ring); mode 2: the host consumes on c1 (RX ring). */
static void wr_link(volatile struct wring *r, volatile struct wring *p, unsigned c0, unsigned c1,
                    unsigned mode)
{
    unsigned i;
    r->peer = (struct wring *)p;
    r->ch[c0].shared = r->ch[c1].shared = 1;
    if (mode == 2) {
        for (i = c0; i < c1; i++)
            r->ch[i].lim = r->ch[i].pos = POISON_LOCAL;
        r->ch[c1].lim = POISON_LOCAL;
        for (i = 0; i < c0; i++)
            p->ch[i].lim = p->ch[i].pos = POISON_SHARED;
        p->ch[c1].pos = POISON_SHARED;
        for (i = c1 + 1; i < r->nch; i++)
            p->ch[i].lim = p->ch[i].pos = POISON_SHARED;
    } else {
        for (i = 0; i < c0; i++)
            r->ch[i].lim = r->ch[i].pos = POISON_LOCAL;
        r->ch[c0].lim = POISON_LOCAL;
        for (i = c1; i < r->nch; i++)
            r->ch[i].lim = r->ch[i].pos = POISON_LOCAL;
        p->ch[c0].pos = POISON_SHARED;
        for (i = c0 + 1; i < c1; i++)
            p->ch[i].lim = p->ch[i].pos = POISON_SHARED;
    }
}

static volatile struct wch *wr_owner(volatile struct wring *r, unsigned c)
{
    return r->ch[c].shared == 1 ? &r->peer->ch[c] : &r->ch[c];
}

/* Elements ready on channel c (wrapping); 0 while the limit is still poisoned. */
static unsigned wr_avail(volatile struct wring *r, unsigned c)
{
    unsigned lim = wr_owner(r, c)->lim, n;
    if (lim > r->size)
        return 0;
    n = r->size + lim - r->ch[c].pos;
    return n >= r->size ? n - r->size : n;
}

/* Elements ready on channel c without wrapping. */
static unsigned wr_space(volatile struct wring *r, unsigned c)
{
    unsigned lim = wr_owner(r, c)->lim, pos = r->ch[c].pos;
    if (lim > r->size)
        return 0;
    return pos > lim ? r->size - pos : lim - pos;
}

static volatile unsigned *wr_reserve(volatile struct wring *r, unsigned c, unsigned n)
{
    unsigned pos = r->ch[c].pos, next = pos + n;
    r->ch[c].pos = next >= r->size ? next - r->size : next;
    return (volatile unsigned *)(r->base + pos * r->esize);
}

static void wr_commit(volatile struct wring *r, unsigned c, unsigned n)
{
    unsigned next = c + 1 == r->nch ? 0 : c + 1, lim;
    volatile struct wch *e = wr_owner(r, next);
    lim = e->lim + n;
    e->lim = lim >= r->size ? lim - r->size : lim;
}

/* Stock FUN_6005833c: stage 4 carries 24-byte TX reports; low16(word3) is status.
 * Stage 3 already returned the allocation, so never dereference report buffer pointers here.
 * count status only; MLME routing/events belong to the future station handler. */
static void wr_tx_status(volatile struct wring *r, unsigned *ok, unsigned *failed)
{
    unsigned n = wr_avail(r, 4);
    while (n--) {
        volatile unsigned *e = wr_reserve(r, 4, 1);
        if ((e[3] & 0xffff) == 0)
            (*ok)++;
        else
            (*failed)++;
        wr_commit(r, 4, 1);
    }
}

/* Take one word off channel c (the caller checked wr_avail). */
static unsigned wr_get(volatile struct wring *r, unsigned c)
{
    unsigned w = *wr_reserve(r, c, 1);
    wr_commit(r, c, 1);
    return w;
}

/* Copy n words into channel c, splitting at the wrap (stock 0x6004e408), then commit. */
static void wr_write(volatile struct wring *r, unsigned c, const unsigned *src, unsigned n)
{
    unsigned first = wr_space(r, c), i;
    volatile unsigned *d;
    if (first > n)
        first = n;
    d = wr_reserve(r, c, first);
    for (i = 0; i < first; i++)
        d[i] = src[i];
    if (n > first) {
        d = wr_reserve(r, c, n - first);
        for (i = first; i < n; i++)
            d[i - first] = src[i];
    }
    wr_commit(r, c, n);
}

/* Kind-1 command record (stock 0x60068494): {1, 0, len+8} {type, 0} {sub, flag} payload. */
static void wr_cmd(volatile struct wring *r, unsigned type, unsigned sub, unsigned flag,
                    const void *payload, unsigned len)
{
    /* Assemble the whole record, then emit it with one wr_write: stock 0x60068600 commits the
     * record before it signals the core, so the core never sees a header whose declared length
     * outruns the payload words already in the ring. */
    unsigned rec[3 + 64];           /* 3 header words + <=256-byte inline payload (node +0x0c limit) */
    const unsigned char *s = payload;
    unsigned i, j, nw;
    if (len > 256)
        return;
    rec[0] = 1u | (len + 8) << 16;              /* {1, 0, low8(len+8), high8(len+8)} */
    rec[1] = type & 0xffff;                      /* {low8(type), high8(type), 0, 0} */
    rec[2] = (sub & 0xffff) | flag << 16;        /* destination | source<<16 */
    /* Pack bytewise into zero-padded little-endian words: a word load would read up to 3 bytes
     * past `len` (foreign stack bytes into the core's ring) and ARM926 rotates unaligned loads. */
    nw = (len + 3) / 4;
    for (i = 0; i < nw; i++) {
        unsigned w = 0;
        for (j = 0; j < 4 && i * 4 + j < len; j++)
            w |= (unsigned)s[i * 4 + j] << 8 * j;
        rec[3 + i] = w;
    }
    wr_write(r, 0, rec, 3 + nw);
}

static int wr_cmd_checked(volatile struct wring *r, unsigned type, unsigned sub, unsigned flag,
                          const void *payload, unsigned len)
{
    if (wr_avail(r, 0) < 3 + (len + 3) / 4)
        return 0;
    wr_cmd(r, type, sub, flag, payload, len);
    return 1;
}
