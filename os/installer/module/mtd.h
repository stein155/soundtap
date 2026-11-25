/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Minimal MTD access for the WLA installer: find a partition by its /proc/mtd label, erase it,
 * write a stream into it skipping bad blocks, and read it back to verify.
 *
 * mtd-utils is not present on the stock rootfs we run under, so this talks to the ioctls
 * directly. When the target is a plain file rather than an mtd character device (the offline
 * gate), the same code path runs against the file with the board's geometry, which is what makes
 * the transaction testable without hardware.
 */
#ifndef SOUNDTAP_MTD_H
#define SOUNDTAP_MTD_H

/* How many blocks one install may mark bad: per partition, and over the whole run. A healthy
 * chip retires a block in an install about never; three in one run is something else -- a
 * driver, timing or power fault, a write-protected chip -- and every mark is for good. So the
 * install stops there and leaves the running slot selected. ubiformat stops at four bad blocks
 * in a row; this stops sooner, counting all of them. */
#define MTD_MARK_BAD_PER_PARTITION 2
#define MTD_MARK_BAD_PER_RUN 3

/* File backend only: a block that fails when an operation reaches it, as a test asks in
 * <path>.fail, one per line: "erase 3", "write 5 ETIMEDOUT", "write 6 EIO 65536" (from that
 * byte of the block on), "read 7", "markbad 4". */
struct mtd_fault {
    char op;          /* 'e'rase, 'w'rite, 'r'ead, 'm'arkbad */
    unsigned block;
    int err;          /* the errno the driver would report */
    unsigned from;    /* the first byte of the block that fails */
};
#define MTD_MAX_FAULTS 16

struct mtd_part {
    int fd;
    int index;
    char label[64];
    /* Long enough for a workspace path, not just /dev/mtdN: the file backend is handed whatever
     * directory the caller runs in, and a truncated path silently becomes a missing file. */
    char path[512];
    unsigned long long size;
    unsigned erase_size;
    unsigned write_size;
    int is_char_device;     /* 0 = plain file backend, used by the offline gate */
    unsigned long long at;  /* physical write cursor, skips bad blocks */
    /* Writes never reach this offset: the partition's size unless a caller replaces only a
     * range of it (the two U-Boot copies share NAND.U-Boot), where skipping a bad block must
     * not carry the image into the blocks behind the range. */
    unsigned long long limit;
    /* File backend only: blocks a test marks bad in <path>.bad (block numbers). the
     * first 64 blocks, which covers every partition file the offline gate uses. */
    unsigned long long file_bad;
    struct mtd_fault faults[MTD_MAX_FAULTS];
    int fault_count;
    /* Set by mtd_allow_mark_bad for a slot partition: a block whose erase or program the driver
     * fails with EIO is marked bad and its data goes to the next good block. */
    int may_mark_bad;
    unsigned marked;                /* blocks this run marked bad here */
    /* What this run wrote into the block at block_start, from its first byte: what a program
     * failure there has to write again into the next good block. */
    unsigned char *block_buf;
    unsigned long long block_start; /* ~0 when the block was not written from its start */
    unsigned block_fill;
    unsigned long long high;        /* the end of the furthest write this run */
};

/* Look a partition up by label ("NAND.Kernel-Pong"). Returns 0 on success. */
int mtd_open(struct mtd_part *part, const char *label, int writable);
void mtd_close(struct mtd_part *part);

/* Let erases and writes of a slot partition (NAND.Kernel-*, NAND.File-System-*) mark a block
 * bad when the driver reports that its erase or program failed (EIO), within the limits above.
 * Any other partition is refused: there a failure fails the operation, nothing is marked. */
int mtd_allow_mark_bad(struct mtd_part *part);

/* Blocks marked bad by this process so far. */
unsigned mtd_marked_this_run(void);

/* Erase the whole partition, skipping blocks already marked bad. Returns erased block count.
 * Under mtd_allow_mark_bad a block whose erase fails is marked bad and skipped. */
int mtd_erase_all(struct mtd_part *part);
/* The same from offset on (a multiple of the erase size); blocks before it are left alone. */
int mtd_erase_from(struct mtd_part *part, unsigned long long from);

/* Erase one block by offset. */
int mtd_erase_block(struct mtd_part *part, unsigned long long offset);

/* Append `len` bytes at the partition's cursor, skipping bad blocks, never at or past `limit`.
 * Returns 0 on success. Under mtd_allow_mark_bad a block whose program fails is marked bad and
 * what this run wrote into it goes again into the next good block -- only while nothing was
 * written past it yet; a block written later than the data behind it (the kernel's deferred
 * first block) is marked and the write fails. */
int mtd_write(struct mtd_part *part, const unsigned char *data, int len);

/* Position the cursor; used to defer a block and come back to it. */
int mtd_seek(struct mtd_part *part, unsigned long long offset);

/* The first good block at or after `offset` when it is block-aligned; `offset` otherwise. */
unsigned long long mtd_skip_bad(struct mtd_part *part, unsigned long long offset);

/* Read `len` bytes from the physical cursor `*cursor`, skipping bad blocks the same way writes do,
 * and advance it: callers reading in pieces keep one cursor, never a logical offset. */
int mtd_read(struct mtd_part *part, unsigned long long *cursor, unsigned char *out, int len);

/* CRC32 of `len` bytes from physical `offset` on, read back through the same bad-block map. */
int mtd_verify_crc(struct mtd_part *part, unsigned long long offset, unsigned long long len,
                   unsigned expected, unsigned *computed);

#endif
