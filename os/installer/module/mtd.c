/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#include <unistd.h>

#include "mtd.h"
#include "swupdate.h"

/* From linux/mtd/mtd-abi.h. Declared here so the installer builds against any libc, including
 * the cross toolchain's, without depending on kernel headers being installed. */
struct mtd_info_user_compat {
    unsigned char type;
    unsigned int flags;
    unsigned int size;
    unsigned int erasesize;
    unsigned int writesize;
    unsigned int oobsize;
    unsigned long long padding;
};

struct erase_info_user_compat {
    unsigned int start;
    unsigned int length;
};

#define MEMGETINFO _IOR('M', 1, struct mtd_info_user_compat)
#define MEMERASE _IOW('M', 2, struct erase_info_user_compat)
#define MEMGETBADBLOCK _IOW('M', 11, long long)
#define MEMSETBADBLOCK _IOW('M', 12, long long)

/* Board geometry, used for the plain-file backend in the offline gate. */
#define FILE_ERASE_SIZE 0x20000
#define FILE_WRITE_SIZE 0x800

/* Blocks marked bad by this process, over every partition it opens. */
static unsigned run_marked;

unsigned mtd_marked_this_run(void)
{
    return run_marked;
}

/* File backend: the errno a test's fault for this operation and block asks for, or 0. */
static int fault(struct mtd_part *part, char op, unsigned long long offset)
{
    if (part->is_char_device) return 0;
    unsigned long long block = offset / part->erase_size;
    for (int i = 0; i < part->fault_count; i++)
        if (part->faults[i].op == op && part->faults[i].block == block &&
            offset % part->erase_size >= part->faults[i].from)
            return part->faults[i].err;
    return 0;
}

static void load_faults(struct mtd_part *part)
{
    static const struct { const char *name; int value; } errnos[] = {
        {"EIO", EIO}, {"ETIMEDOUT", ETIMEDOUT}, {"EROFS", EROFS}, {"EBADMSG", EBADMSG},
    };
    char path[sizeof part->path + 8];
    snprintf(path, sizeof path, "%s.fail", part->path);
    FILE *file = fopen(path, "r");
    if (!file) return;
    char op[16], name[16], line[64];
    unsigned block, from;
    while (fgets(line, sizeof line, file) && part->fault_count < MTD_MAX_FAULTS) {
        from = 0;
        int fields = sscanf(line, "%15s %u %15s %u", op, &block, name, &from);
        if (fields < 2) continue;
        struct mtd_fault *f = &part->faults[part->fault_count++];
        f->op = op[0];
        f->block = block;
        f->from = from;
        f->err = f->op == 'r' ? EBADMSG : f->op == 'm' ? EROFS : EIO;
        for (unsigned i = 0; fields >= 3 && i < sizeof errnos / sizeof errnos[0]; i++)
            if (!strcmp(name, errnos[i].name)) f->err = errnos[i].value;
    }
    fclose(file);
}

static int is_bad_block(struct mtd_part *part, unsigned long long offset)
{
    if (!part->is_char_device) {
        unsigned long long block = offset / part->erase_size;
        return block < 64 && (part->file_bad >> block) & 1;
    }
    long long where = (long long)offset;
    int bad = ioctl(part->fd, MEMGETBADBLOCK, &where);
    return bad > 0;
}

int mtd_open(struct mtd_part *part, const char *label, int writable)
{
    memset(part, 0, sizeof *part);
    part->fd = -1;

    FILE *table = fopen("/proc/mtd", "r");
    int index = -1;
    if (table) {
        char line[256];
        while (fgets(line, sizeof line, table)) {
            int candidate;
            unsigned size, erase;
            char name[128];
            if (sscanf(line, "mtd%d: %x %x \"%127[^\"]\"", &candidate, &size, &erase, name) == 4 &&
                strcmp(name, label) == 0) {
                index = candidate;
                part->size = size;
                part->erase_size = erase;
                break;
            }
        }
        fclose(table);
    }
    if (index < 0) {
        /* Offline gate: the label names a file in SOUNDTAP_MTD_DIR instead of a partition. */
        const char *dir = getenv("SOUNDTAP_MTD_DIR");
        if (!dir) {
            swu_log("mtd: no partition labelled %s", label);
            return -1;
        }
        if (snprintf(part->path, sizeof part->path, "%s/%s", dir, label)
                >= (int)sizeof part->path) {
            swu_log("mtd: path for %s does not fit", label);
            return -1;
        }
        struct stat st;
        if (stat(part->path, &st) != 0) {
            swu_log("mtd: %s missing", part->path);
            return -1;
        }
        part->size = st.st_size;
        char bad_path[sizeof part->path + 4];
        snprintf(bad_path, sizeof bad_path, "%s.bad", part->path);
        FILE *bad = fopen(bad_path, "r");
        unsigned block;
        while (bad && fscanf(bad, "%u", &block) == 1)
            if (block < 64) part->file_bad |= 1ULL << block;
        if (bad) fclose(bad);
        part->erase_size = FILE_ERASE_SIZE;
        load_faults(part);
        part->write_size = FILE_WRITE_SIZE;
        part->index = -1;
    } else {
        part->index = index;
        snprintf(part->path, sizeof part->path, "/dev/mtd%d", index);
    }
    snprintf(part->label, sizeof part->label, "%s", label);

    part->fd = open(part->path, writable ? O_RDWR : O_RDONLY);
    if (part->fd < 0) {
        swu_log("mtd: %s open failed errno=%d", part->path, errno);
        return -1;
    }
    struct mtd_info_user_compat info;
    if (ioctl(part->fd, MEMGETINFO, &info) == 0) {
        part->is_char_device = 1;
        part->size = info.size;
        part->erase_size = info.erasesize;
        part->write_size = info.writesize;
    } else if (part->index >= 0) {
        swu_log("mtd: %s is not an mtd device (errno=%d)", part->path, errno);
        close(part->fd);
        part->fd = -1;
        return -1;
    }
    part->limit = part->size;
    swu_log("mtd: %s (%s) size=%llu erase=%u write=%u%s", part->label, part->path, part->size,
            part->erase_size, part->write_size, part->is_char_device ? "" : " [file backend]");
    return 0;
}

void mtd_close(struct mtd_part *part)
{
    if (part->fd >= 0) close(part->fd);
    part->fd = -1;
    free(part->block_buf);
    part->block_buf = NULL;
}

/* An install writes these and only these with relocation: the inactive kernel and rootfs. */
static const char *const slot_labels[] = {
    "NAND.Kernel-Ping", "NAND.Kernel-Pong", "NAND.File-System-Ping", "NAND.File-System-Pong",
};
/* mtd10-mtd12. No install writes them, so nothing can fail there; if that ever changes, a
 * failure there still marks nothing. */
static const char *const never_marked[] = {
    "NAND.Persistent-Storage", "NAND.Update-Staging", "NAND.Manufacturing-Data",
};

static int label_in(const char *label, const char *const *list, unsigned n)
{
    for (unsigned i = 0; i < n; i++)
        if (!strcmp(label, list[i])) return 1;
    return 0;
}

#define COUNT(list) (unsigned)(sizeof list / sizeof list[0])

int mtd_allow_mark_bad(struct mtd_part *part)
{
    if (label_in(part->label, never_marked, COUNT(never_marked)) ||
        !label_in(part->label, slot_labels, COUNT(slot_labels))) {
        swu_log("mtd: %s is not a slot partition; no block of it is ever marked bad", part->label);
        return -1;
    }
    part->block_buf = malloc(part->erase_size);
    if (!part->block_buf) {
        swu_log("mtd: %s: no memory to hold a block", part->label);
        return -1;
    }
    part->block_start = ~0ULL;
    part->block_fill = 0;
    part->may_mark_bad = 1;
    return 0;
}

/* Retire one block after the driver failed its erase or program with `err`. Only EIO is a
 * failure of the block itself: the NAND reported FAIL in its status. A timeout, a refusal
 * (EROFS, EINVAL) or an ECC error on a read never comes here, and nothing marks a block of a
 * partition mtd_allow_mark_bad was not asked for, or one past the limits. Every mark is logged
 * with the partition, the block and the error. */
static int mark_bad(struct mtd_part *part, unsigned long long offset, const char *op, int err)
{
    unsigned long long block = offset / part->erase_size;
    if (label_in(part->label, never_marked, COUNT(never_marked)) || !part->may_mark_bad) {
        swu_log("mtd: %s block %llu: %s failed errno=%d; this partition is never marked bad",
                part->label, block, op, err);
        return -1;
    }
    if (err != EIO) {
        swu_log("mtd: %s block %llu: %s failed errno=%d, not a NAND failure; not marked bad",
                part->label, block, op, err);
        return -1;
    }
    if (part->marked >= MTD_MARK_BAD_PER_PARTITION || run_marked >= MTD_MARK_BAD_PER_RUN) {
        swu_log("mtd: %s block %llu: %s failed errno=%d; %u marked bad in this partition and %u in "
                "this install already, the most allowed (%u, %u): not marked, the install stops",
                part->label, block, op, err, part->marked, run_marked, MTD_MARK_BAD_PER_PARTITION,
                MTD_MARK_BAD_PER_RUN);
        return -1;
    }
    int mark_err = 0;
    if (part->is_char_device) {
        long long where = (long long)(block * part->erase_size);
        if (ioctl(part->fd, MEMSETBADBLOCK, &where) != 0) mark_err = errno;
    } else if ((mark_err = fault(part, 'm', offset)) == 0) {
        /* The file backend keeps its marks where it reads them, as NAND keeps them in OOB. */
        char path[sizeof part->path + 4];
        snprintf(path, sizeof path, "%s.bad", part->path);
        FILE *bad = block < 64 ? fopen(path, "a") : NULL;
        if (!bad || fprintf(bad, "%llu\n", block) < 0) mark_err = EIO;
        if (bad && fclose(bad) != 0) mark_err = EIO;
        if (!mark_err) part->file_bad |= 1ULL << block;
    }
    if (mark_err) {
        swu_log("mtd: %s block %llu: %s failed errno=%d, and marking it bad failed errno=%d",
                part->label, block, op, err, mark_err);
        return -1;
    }
    part->marked++;
    run_marked++;
    swu_log("mtd: %s block %llu (offset %llu) marked bad: %s failed errno=%d "
            "(%u of %u in this partition, %u of %u in this install)",
            part->label, block, block * part->erase_size, op, err, part->marked,
            MTD_MARK_BAD_PER_PARTITION, run_marked, MTD_MARK_BAD_PER_RUN);
    return 0;
}

/* Erase one block. 0, or the negative errno the driver reported. */
static int erase_raw(struct mtd_part *part, unsigned long long offset)
{
    if (!part->is_char_device) {
        int err = fault(part, 'e', offset);
        if (err) return -err;  /* a failed erase leaves the block as it was */
        static unsigned char blank[FILE_ERASE_SIZE];
        memset(blank, 0xff, sizeof blank);
        if (lseek(part->fd, offset, SEEK_SET) < 0) return -errno;
        unsigned long long left = part->erase_size;
        while (left) {
            int want = (int)(left > sizeof blank ? sizeof blank : left);
            if (write(part->fd, blank, want) != want) return -ENOSPC;
            left -= want;
        }
        return 0;
    }
    struct erase_info_user_compat erase;
    erase.start = (unsigned)offset;
    erase.length = part->erase_size;
    return ioctl(part->fd, MEMERASE, &erase) == 0 ? 0 : -errno;
}

/* Write `len` bytes at `offset`, inside one block. 0, or the negative errno the driver
 * reported. A short count is no error a driver reports, so it is never EIO. */
static int write_raw(struct mtd_part *part, unsigned long long offset, const unsigned char *data,
                     int len)
{
    int err = fault(part, 'w', offset);
    if (err) return -err;
    if (lseek(part->fd, offset, SEEK_SET) < 0) return -errno;
    int written = write(part->fd, data, len);
    if (written < 0) return -errno;
    return written == len ? 0 : -ENOSPC;
}

int mtd_erase_block(struct mtd_part *part, unsigned long long offset)
{
    int err = erase_raw(part, offset);
    if (err) {
        swu_log("mtd: %s erase at %llu failed errno=%d", part->label, offset, -err);
        return -1;
    }
    return 0;
}

int mtd_erase_all(struct mtd_part *part)
{
    return mtd_erase_from(part, 0);
}

int mtd_erase_from(struct mtd_part *part, unsigned long long from)
{
    int erased = 0, bad = 0, marked = 0;
    for (unsigned long long offset = from; offset < part->size; offset += part->erase_size) {
        if (is_bad_block(part, offset)) { bad++; continue; }
        int err = erase_raw(part, offset);
        if (err == -EIO && part->may_mark_bad) {
            if (mark_bad(part, offset, "erase", EIO) != 0) return -1;
            marked++;
            continue;
        }
        if (err) {
            swu_log("mtd: %s erase at %llu failed errno=%d", part->label, offset, -err);
            return -1;
        }
        erased++;
    }
    swu_log("mtd: %s erased %d blocks, skipped %d bad, marked %d bad", part->label, erased, bad,
            marked);
    part->at = from;
    return erased;
}

int mtd_seek(struct mtd_part *part, unsigned long long offset)
{
    if (offset > part->size) return -1;
    part->at = offset;
    return 0;
}

unsigned long long mtd_skip_bad(struct mtd_part *part, unsigned long long offset)
{
    while (offset < part->size && (offset % part->erase_size) == 0 && is_bad_block(part, offset))
        offset += part->erase_size;
    return offset;
}

/* The program of the block at `block` failed with EIO: mark it bad, then put what this run had
 * written into it into the next good block, erased first, as nandwrite -m and ubiformat do. A
 * block that fails there is marked as well, until the limits stop it. The cursor ends behind the
 * rewritten bytes, where the failed write goes again. */
static int relocate(struct mtd_part *part, unsigned long long block)
{
    unsigned long long failed = block;
    const char *op = "write";
    unsigned fill = part->block_fill;
    if (part->block_start != block || part->high > block + part->erase_size) {
        /* Marked all the same: it failed, and the next install must not pick it again. */
        mark_bad(part, block, op, EIO);
        swu_log("mtd: %s block %llu is not written again elsewhere: it was not written in order "
                "from its start, or data behind it is on flash already", part->label,
                block / part->erase_size);
        return -1;
    }
    for (;;) {
        if (mark_bad(part, block, op, EIO) != 0) return -1;
        unsigned long long next = mtd_skip_bad(part, block + part->erase_size);
        if (next >= part->limit) {
            swu_log("mtd: %s ran out of space before %llu", part->label, part->limit);
            return -1;
        }
        int err = erase_raw(part, next);
        if (err == -EIO) { block = next; op = "erase"; continue; }
        if (err) {
            swu_log("mtd: %s erase at %llu failed errno=%d", part->label, next, -err);
            return -1;
        }
        err = fill ? write_raw(part, next, part->block_buf, (int)fill) : 0;
        if (err == -EIO) { block = next; op = "write"; continue; }
        if (err) {
            swu_log("mtd: %s write of %u at %llu failed errno=%d", part->label, fill, next, -err);
            return -1;
        }
        swu_log("mtd: %s block %llu takes over from block %llu, %u bytes written again",
                part->label, next / part->erase_size, failed / part->erase_size, fill);
        part->block_start = next;
        part->at = next + fill;
        if (part->high < part->at) part->high = part->at;
        return 0;
    }
}

/* Advance the cursor past bad blocks, then write within the current block. */
int mtd_write(struct mtd_part *part, const unsigned char *data, int len)
{
    int done = 0;
    while (done < len) {
        part->at = mtd_skip_bad(part, part->at);
        if (part->at >= part->limit) {
            swu_log("mtd: %s ran out of space before %llu", part->label, part->limit);
            return -1;
        }
        unsigned long long block = part->at - part->at % part->erase_size;
        int room = (int)(block + part->erase_size - part->at);
        int want = len - done < room ? len - done : room;
        /* What goes into this block is kept while it is written in order from its first byte:
         * that is what a failed program has to write again into another block. */
        if (part->at == block && part->block_start != block) {
            part->block_start = block;
            part->block_fill = 0;
        } else if (part->block_start != block || part->at != block + part->block_fill) {
            part->block_start = ~0ULL;
        }
        int err = write_raw(part, part->at, data + done, want);
        if (err == -EIO && part->may_mark_bad) {
            swu_log("mtd: %s write of %d at %llu failed errno=%d", part->label, want, part->at, EIO);
            if (relocate(part, block) != 0) return -1;
            continue;
        }
        if (err) {
            swu_log("mtd: %s write of %d at %llu failed errno=%d", part->label, want, part->at,
                    -err);
            return -1;
        }
        if (part->block_buf && part->block_start == block) {
            memcpy(part->block_buf + part->block_fill, data + done, want);
            part->block_fill += want;
        }
        part->at += want;
        if (part->high < part->at) part->high = part->at;
        done += want;
    }
    return 0;
}

int mtd_read(struct mtd_part *part, unsigned long long *cursor, unsigned char *out, int len)
{
    int done = 0;
    unsigned long long at = *cursor;
    while (done < len) {
        at = mtd_skip_bad(part, at);
        if (at >= part->size) return -1;
        unsigned long long block_end = (at / part->erase_size + 1) * part->erase_size;
        int room = (int)(block_end - at);
        int want = len - done < room ? len - done : room;
        int err = fault(part, 'r', at);
        if (err) {
            swu_log("mtd: %s read of %d at %llu failed errno=%d", part->label, want, at, err);
            return -1;
        }
        if (lseek(part->fd, at, SEEK_SET) < 0) return -1;
        int got = read(part->fd, out + done, want);
        if (got != want) {
            swu_log("mtd: %s read of %d at %llu returned %d errno=%d", part->label, want, at,
                    got, errno);
            return -1;
        }
        at += want;
        done += want;
    }
    *cursor = at;
    return 0;
}

int mtd_verify_crc(struct mtd_part *part, unsigned long long offset, unsigned long long len,
                   unsigned expected, unsigned *computed)
{
    unsigned char buffer[32 * 1024];
    unsigned crc = 0;
    unsigned long long left = len, at = offset;
    while (left) {
        int want = (int)(left > sizeof buffer ? sizeof buffer : left);
        if (mtd_read(part, &at, buffer, want) != 0) return -1;
        crc = swu_crc32(crc, buffer, want);
        left -= want;
    }
    if (computed) *computed = crc;
    if (crc != expected) {
        swu_log("mtd: %s readback CRC %08x != written %08x", part->label, crc, expected);
        return -1;
    }
    swu_log("mtd: %s readback verified, %llu bytes, CRC %08x", part->label, len, crc);
    return 0;
}
