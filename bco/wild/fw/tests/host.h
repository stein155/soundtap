/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Host harness of the WiLD core's unit tests (tests/run-all.sh). The core reaches its register
 * block (0x05000000) and the mailbox tail of its SRAM (0x1fddc..0x1ffff) by absolute address and
 * keeps addresses in 32-bit words, so the tests link without PIE (every global below 4 GiB) and
 * map both regions at those addresses before main: the sources compile unchanged. A register is
 * plain memory here: a test sets what the hardware would show and reads what the core wrote.
 * Nothing here talks to a speaker. */
#ifndef WILD_HOST_H
#define WILD_HOST_H
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>

#include "hw.h"

#define REG_BASE 0x05000000ul
#define MBOX_PAGE 0x1f000ul

__attribute__((constructor)) static void host_map(void)
{
    static const unsigned long at[2] = {REG_BASE, MBOX_PAGE};
    static int probe;
    for (int i = 0; i < 2; i++)
        if (mmap((void *)at[i], 0x1000, PROT_READ | PROT_WRITE,
                 MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0) != (void *)at[i]) {
            perror("wild host: map the register block / mailbox");
            exit(2);
        }
    if ((uintptr_t)&probe >> 32) {
        fputs("wild host: globals above 4 GiB; build with -no-pie\n", stderr);
        exit(2);
    }
}

static int fails;
#define CHECK(cond, ...)                                                                       \
    do {                                                                                       \
        if (!(cond)) {                                                                         \
            fails++;                                                                           \
            printf("FAIL %s:%d: ", __FILE__, __LINE__);                                        \
            printf(__VA_ARGS__);                                                               \
            putchar('\n');                                                                     \
        }                                                                                      \
    } while (0)

static int done(const char *name)
{
    printf("%s: %s\n", name, fails ? "FAILED" : "ok");
    return fails != 0;
}

/* start.S's nested critical sections: the tests only check that every off has its on. */
static int irq_depth;
void irq_off(void)
{
    irq_depth++;
}
void irq_on(void)
{
    if (--irq_depth < 0) {
        printf("FAIL irq_on without irq_off\n");
        exit(1);
    }
}
void fiq_on(void)
{
}

/* main.c's operation parameters (block id6): our MAC at +3, CWmin at +0x38. */
static unsigned char op_block[0xa0] __attribute__((aligned(4)));
const unsigned char *const opparams = op_block;
static const unsigned char OWN[6] = {0x02, 0x00, 0x00, 0x5d, 0x00, 0x01};

static void op_init(void)
{
    memcpy(op_block + 3, OWN, 6);
    op_block[0x38] = 0x1f, op_block[0x39] = 0;
}

/* Deterministic generator for the randomised tests (xorshift32). */
static uint32_t rnd_state = 0x5eed1234u;
static uint32_t rnd(void)
{
    uint32_t x = rnd_state;
    x ^= x << 13, x ^= x >> 17, x ^= x << 5;
    return rnd_state = x;
}

static void put_le16(unsigned char *p, unsigned v)
{
    p[0] = v, p[1] = v >> 8;
}

#endif
