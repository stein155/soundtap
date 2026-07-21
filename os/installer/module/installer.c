/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* SoundTap WLA update installer.
 *
 * This is the binary stock Bose firmware extracts from our package and runs: it is module 1 of
 * the STU, it is executed as root with argc == 0, and everything after that point is ours.
 * Measured behaviour it implements (os/tests/fixtures/wla-stock-17008-handshake-20260913.txt):
 *
 *   - bind :40015; stock connects from 127.0.0.1 and then stays silent
 *   - the installer speaks first with SoftwareUpdateStatus, or stock kills it after ~10 s
 *   - stock answers SoftwareUpdateStart{url="bonjour//", update_crc=0, update_size}
 *   - the installer pulls update_size bytes in 16 KiB records, first sequence 2
 *   - update_size is the package *after* this module, so the stream starts at module 2's header
 *   - stock declares no CRC: validating the package is our job
 *   - a status event sent before the first data request makes stock drop the link
 *
 * It is C, not Go, because the stock 3.14.43+ kernel does not run our Go builds; the same probe
 * in Go never reported from this slot while the C build reported in 1.3 s.
 *
 * The NAND transaction is compiled in only with -DSOUNDTAP_INSTALL_NAND. Without that flag this
 * binary contains no path that opens a partition for writing.
 */
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <signal.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#include "swupdate.h"
#ifdef SOUNDTAP_INSTALL_NAND
#include <sys/reboot.h>
#include "mtd.h"
#endif

/* Stock connects to :40015, so that is what the installed binary must listen on. The
 * offline gates are both sides of the conversation and run several at a time on a shared
 * runner, where one fixed port is a race, so they compile with their own -DLISTEN_PORT. */
#ifndef LISTEN_PORT
#define LISTEN_PORT 40015
#endif
#define CHUNK 16384

/* STU container geometry, mirroring os/installer/build-stu.py. */
#define STU_HDR 0x138
#define STU_OVERLAP 0x14
#define STU_SIZE_OFF 0x98
#define STU_NAME_OFF 0x18
#define STU_NAME_MAX 0x28
#define STU_IMGCRC_OFF 0x12c
#define STU_HDRCRC_OFF 0x134

/* SoftwareUpdateStatus.state, as stock reports it back on :8090/swUpdateQuery. */
#define STATE_IDLE 0
#define STATE_DOWNLOADING 1
#define STATE_AUTHENTICATING 2
#define STATE_INSTALLING 3
#define STATE_COMPLETED 4

struct module_header {
    char name[STU_NAME_MAX + 1];
    unsigned size;          /* payload bytes, including the 20-byte overlap into the next header */
    unsigned image_crc;
    unsigned header_crc;
};

static unsigned be32(const unsigned char *b)
{
    return ((unsigned)b[0] << 24) | ((unsigned)b[1] << 16) | ((unsigned)b[2] << 8) | b[3];
}

static void parse_header(const unsigned char *raw, struct module_header *out)
{
    memcpy(out->name, raw + STU_NAME_OFF, STU_NAME_MAX);
    out->name[STU_NAME_MAX] = 0;
    out->size = be32(raw + STU_SIZE_OFF);
    out->image_crc = be32(raw + STU_IMGCRC_OFF);
    out->header_crc = be32(raw + STU_HDRCRC_OFF);
}

/* The stored header CRC covers the header from 0x14 up to the CRC field itself: the first 20
 * bytes are the previous module's payload tail, which is why they are excluded. */
static int header_crc_ok(const unsigned char *raw, const struct module_header *header)
{
    return swu_crc32(0, raw + STU_OVERLAP, STU_HDRCRC_OFF - STU_OVERLAP) == header->header_crc;
}

/* ---- the pull side: one bounded reader over the coordinator link ---- */

struct puller {
    struct swu_link *link;
    unsigned sequence;
    unsigned long long remaining;
    unsigned char buffer[CHUNK * 2];
    int have;
    int at;
    int end_of_stream;
};

static void puller_init(struct puller *p, struct swu_link *link, unsigned long long size)
{
    memset(p, 0, sizeof *p);
    p->link = link;
    p->sequence = 1;
    p->remaining = size;
}

static int puller_fill(struct puller *p)
{
    if (p->end_of_stream) return 0;
    if (p->have - p->at > 0) {
        memmove(p->buffer, p->buffer + p->at, p->have - p->at);
        p->have -= p->at;
    } else {
        p->have = 0;
    }
    p->at = 0;
    if (p->have + CHUNK > (int)sizeof p->buffer) return 0;

    p->sequence++;
    unsigned char request[16];
    int n = swu_put_field_varint(request, 1, CHUNK);
    if (swu_send(p->link, SWU_EVENT_REQUEST, SWU_MSG_DATA, 1, p->sequence, request, n,
                 "SoftwareUpdateDataRequest") < 0)
        return -1;

    struct swu_message message;
    if (swu_receive(p->link, &message) < 0) return -1;
    if (message.message_id == SWU_MSG_ABORT) {
        swu_log("stock aborted the stream at sequence %u", p->sequence);
        return -1;
    }
    if (message.message_id != SWU_MSG_DATA) {
        swu_log("unexpected message %u (%s) during stream", message.message_id, message.type_name);
        return -1;
    }
    if (message.sequence != p->sequence) {
        swu_log("data response sequence %u does not match request %u", message.sequence, p->sequence);
        return -1;
    }

    unsigned long long count = 0, eos = 0;
    const unsigned char *data = NULL;
    int data_len = 0;
    swu_parse_data_response(message.contents, message.contents_len, &count, &data, &data_len, &eos);
    if (data_len > 0) {
        if (p->have + data_len > (int)sizeof p->buffer) {
            swu_log("data record %d larger than the pull buffer", data_len);
            return -1;
        }
        memcpy(p->buffer + p->have, data, data_len);
        p->have += data_len;
        if ((unsigned long long)data_len <= p->remaining) p->remaining -= data_len;
        else p->remaining = 0;
    }
    if (eos) p->end_of_stream = 1;
    return data_len;
}

static int puller_read(struct puller *p, unsigned char *out, int want)
{
    int done = 0;
    while (done < want) {
        if (p->at >= p->have) {
            int got = puller_fill(p);
            if (got < 0) return -1;
            if (got == 0 && p->at >= p->have) return done;
            continue;
        }
        int take = p->have - p->at;
        if (take > want - done) take = want - done;
        if (out) memcpy(out + done, p->buffer + p->at, take);
        p->at += take;
        done += take;
    }
    return done;
}

/* ---- where a module's bytes go ----
 *
 * A module is consumed once, streaming: 85 MiB of rootfs never fits in RAM, so the sink writes
 * it out while the CRC is still being computed. Nothing is trusted until the module's own CRC
 * matches, and the candidate slot only becomes bootable after every readback has succeeded.
 */
struct sink {
    const char *what;
    int (*write)(struct sink *sink, const unsigned char *data, int len);
    void *ctx;
    unsigned long long written;
    unsigned crc;           /* CRC of the image bytes, i.e. excluding the shared header tail */
};

static int sink_discard(struct sink *sink, const unsigned char *data, int len)
{
    (void)sink; (void)data; (void)len;
    return 0;
}

#ifdef SOUNDTAP_INSTALL_NAND
/* Kernel writing defers the first erase block: until that block is written the candidate slot
 * cannot boot, which is what keeps a power cut during the rootfs write survivable. */
struct nand_sink {
    struct mtd_part part;
    unsigned char first_block[0x20000];
    unsigned first_block_len;
    int defer_first_block;
    int first_block_full;
};

#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
/* MLO and u-boot.img are collected in RAM: no bootloader byte may reach flash before the kernel
 * and rootfs are verified, and the copy the mask ROM reads must be the very last write. */
struct ram_sink {
    unsigned char *buffer;
    unsigned capacity;
    unsigned *length;
};

static int sink_ram(struct sink *sink, const unsigned char *data, int len)
{
    struct ram_sink *ram = sink->ctx;
    if (*ram->length + (unsigned)len > ram->capacity) {
        swu_log("%s does not fit in %u bytes", sink->what, ram->capacity);
        return -1;
    }
    memcpy(ram->buffer + *ram->length, data, len);
    *ram->length += (unsigned)len;
    return 0;
}
#endif

static int sink_nand(struct sink *sink, const unsigned char *data, int len)
{
    struct nand_sink *nand = sink->ctx;
    if (nand->defer_first_block && !nand->first_block_full) {
        unsigned room = (unsigned)sizeof nand->first_block - nand->first_block_len;
        unsigned take = (unsigned)len < room ? (unsigned)len : room;
        memcpy(nand->first_block + nand->first_block_len, data, take);
        nand->first_block_len += take;
        data += take;
        len -= take;
        if (nand->first_block_len == sizeof nand->first_block) {
            /* The deferred block is complete: everything after it goes straight to flash. A
             * record that ends exactly on the block boundary must move the cursor here, not on
             * the next call, or the remainder overwrites offset zero. The deferred block's home
             * is the first good block, which a bad block 0 moves. */
            nand->first_block_full = 1;
            if (mtd_seek(&nand->part, mtd_skip_bad(&nand->part, 0) + sizeof nand->first_block) != 0)
                return -1;
        }
        if (len == 0) return 0;
    }
    return mtd_write(&nand->part, data, len);
}
#endif

/* Consume exactly one module. `overlap_out` receives the shared 20-byte tail, which is the start
 * of the next module's header and never part of the image. */
static int consume_module(struct puller *p, const struct module_header *header, struct sink *sink,
                          unsigned char *overlap_out, int last_module)
{
    unsigned crc = 0;
    unsigned overlap = last_module ? 0 : STU_OVERLAP;
    unsigned long long left = header->size;
    static unsigned char scratch[CHUNK];
    while (left > overlap) {
        int want = (int)((left - overlap) > sizeof scratch ? sizeof scratch : (left - overlap));
        int got = puller_read(p, scratch, want);
        if (got != want) {
            swu_log("%s: stream ended %d bytes early", header->name, want - got);
            return -1;
        }
        crc = swu_crc32(crc, scratch, got);
        if (sink->write(sink, scratch, got) != 0) {
            swu_log("%s: writing to %s failed", header->name, sink->what);
            return -1;
        }
        sink->crc = swu_crc32(sink->crc, scratch, got);
        sink->written += got;
        left -= got;
    }
    if (overlap) {
        if (puller_read(p, overlap_out, overlap) != (int)overlap) {
            swu_log("%s: stream ended inside the shared tail", header->name);
            return -1;
        }
        crc = swu_crc32(crc, overlap_out, overlap);
    }
    if (crc != header->image_crc) {
        swu_log("%s: image CRC %08x does not match header %08x", header->name, crc,
                header->image_crc);
        return -1;
    }
    swu_log("%s: %u bytes verified, image CRC %08x -> %s", header->name, header->size, crc,
            sink->what);
    return 0;
}

/* ---- the transaction ---- */

struct install {
    int modules;
#ifdef SOUNDTAP_INSTALL_NAND
    struct nand_sink kernel;
    struct nand_sink rootfs;
    unsigned long long kernel_len, rootfs_len;
    unsigned kernel_crc, rootfs_crc;
    /* Bootloaders are small enough to hold in RAM, which is what lets them be written in a
     * chosen order at the end instead of streamed into flash as they arrive. */
    unsigned char spl[0x20000];
    unsigned spl_len;
    unsigned char uboot[0x1e0000];
    unsigned uboot_len;
#endif
};

#ifdef SOUNDTAP_INSTALL_NAND
static const char *running_variant(void);
static const char *soundtap_variant(void);

/* The running slot comes from the kernel command line: ubi.mtd=8 is Ping, 9 is Pong. The
 * candidate is always the other one, so the system we are running on is never written. */
static int candidate_slot(int *rootfs_mtd)
{
    int fd = open("/proc/cmdline", O_RDONLY);
    char line[1024];
    int n = fd >= 0 ? (int)read(fd, line, sizeof line - 1) : -1;
    if (fd >= 0) close(fd);
    if (n <= 0) {
        swu_log("cannot read /proc/cmdline");
        return -1;
    }
    line[n] = 0;
    const char *at = strstr(line, "ubi.mtd=");
    if (!at) {
        swu_log("no ubi.mtd on the command line");
        return -1;
    }
    int running = atoi(at + 8);
    if (running != 8 && running != 9) {
        swu_log("unexpected running rootfs partition mtd%d", running);
        return -1;
    }
    *rootfs_mtd = running == 8 ? 9 : 8;
    swu_log("running from mtd%d, candidate slot is mtd%d", running, *rootfs_mtd);
    return 0;
}

static int open_targets(struct install *install)
{
    int rootfs_mtd = 9;
    const char *forced = getenv("SOUNDTAP_CANDIDATE_ROOTFS");
    if (forced) rootfs_mtd = atoi(forced);
    else if (candidate_slot(&rootfs_mtd) != 0) return -1;
    const char *kernel_label = rootfs_mtd == 9 ? "NAND.Kernel-Pong" : "NAND.Kernel-Ping";
    const char *rootfs_label = rootfs_mtd == 9 ? "NAND.File-System-Pong" : "NAND.File-System-Ping";

    if (mtd_open(&install->kernel.part, kernel_label, 1) != 0) return -1;
    install->kernel.defer_first_block = 1;
    if (mtd_open(&install->rootfs.part, rootfs_label, 1) != 0) return -1;
    /* A block of the candidate slot whose erase or program fails is marked bad and written
     * around, within mtd.h's limits; past them the install stops with the running slot
     * selected. No other partition this installer writes ever has a block marked. */
    if (mtd_allow_mark_bad(&install->kernel.part) != 0 ||
        mtd_allow_mark_bad(&install->rootfs.part) != 0)
        return -1;

    /* Erasing the candidate kernel first is what makes the slot unbootable for the whole
     * transaction: a power cut from here on leaves the running slot untouched and selected. */
    if (mtd_erase_all(&install->kernel.part) < 0) return -1;
    if (mtd_erase_all(&install->rootfs.part) < 0) return -1;
    return 0;
}

/* Point the bootloader at the candidate slot. Stock U-Boot's environment is the only selector:
 * bootcmd is literally "run nand_boot;" (Ping) or "run nand_boot2;" (Pong). There is no
 * name-byte choice at boot -- this board's mtd5 dump shows the whole selection logic.
 *
 * Bose's environment is CONFIG_ENV_SIZE = 8 KiB at the start of the 128 KiB partition, not the
 * whole partition: the stored CRC32 of this board's own environment only matches over that
 * window. Getting this wrong is not destructive but it is silent -- U-Boot reports a bad CRC,
 * falls back to its built-in default, and boots the Ping slot as if nothing had been asked.
 *
 * Our U-Boot keeps a redundant environment instead (ADR 0010): two copies of 32 KiB, the primary
 * at the start of NAND.U-Boot-Env and the other in the last erase block of NAND.U-Boot, each a
 * CRC32 over its entries, a flag byte, then the entries sorted by key. Of two that check out it
 * boots the newer flag; while neither does it reads the 8 KiB copy above. So the environment
 * stays in Bose's format while Bose's U-Boot may still be the one that boots, and only once our
 * boot chain is written and verified is it rewritten as both copies, the redundant one first:
 * until the primary is rewritten that copy is the only one that checks out.
 * internal/ubootenv writes the same bytes for the same entries (writers_test.go). */
#define UBOOT_ENV_SIZE 0x2000
#define REDUND_ENV_SIZE 0x8000
#define REDUND_ENV_OFFSET 0x1c0000ULL  /* in NAND.U-Boot: its last erase block, flash 0x240000 */
#define REDUND_ENV_FRESH_REDUNDANT 1
#define REDUND_ENV_FRESH_PRIMARY 2

/* One environment entry, for sorting by key the way U-Boot's env_export does. */
struct env_entry {
    const char *text;
    unsigned len, keylen;
};

static int env_entry_cmp(const void *a, const void *b)
{
    const struct env_entry *x = a, *y = b;
    unsigned n = x->keylen < y->keylen ? x->keylen : y->keylen;
    int c = memcmp(x->text, y->text, n);
    if (c) return c;
    return x->keylen < y->keylen ? -1 : x->keylen > y->keylen;
}

/* Render NUL-separated entries (ending in an empty one) as one copy of the redundant
 * environment: CRC32 over the data, the flag, the entries sorted by key, zero padding. */
static int encode_redundant_env(const unsigned char *entries, unsigned len, unsigned char flag,
                                unsigned char *image)
{
    struct env_entry list[512] = {0};
    unsigned count = 0;
    for (unsigned at = 0; at < len && entries[at];) {
        const char *text = (const char *)entries + at;
        unsigned n = (unsigned)strlen(text);
        const char *eq = memchr(text, '=', n);
        if (!eq || count == sizeof list / sizeof *list) return -1;
        list[count].text = text;
        list[count].len = n;
        list[count].keylen = (unsigned)(eq - text);
        count++;
        at += n + 1;
    }
    qsort(list, count, sizeof *list, env_entry_cmp);
    memset(image, 0, REDUND_ENV_SIZE);
    unsigned out = 5;
    for (unsigned i = 0; i < count; i++) {
        if (out + list[i].len + 2 > REDUND_ENV_SIZE) return -1;
        memcpy(image + out, list[i].text, list[i].len);
        out += list[i].len + 1;
    }
    unsigned crc = swu_crc32(0, image + 5, REDUND_ENV_SIZE - 5);
    image[0] = crc & 0xff;
    image[1] = (crc >> 8) & 0xff;
    image[2] = (crc >> 16) & 0xff;
    image[3] = (crc >> 24) & 0xff;
    image[4] = flag;
    return 0;
}

/* Erase the block at offset of the partition, write image there and read its CRC back. */
static int write_env_copy(const char *label, unsigned long long offset, const unsigned char *image,
                          unsigned len)
{
    struct mtd_part part;
    if (mtd_open(&part, label, 1) != 0) return -1;
    unsigned computed;
    int ok = offset % part.erase_size == 0 && offset + part.erase_size <= part.size &&
             mtd_erase_block(&part, offset) == 0 && mtd_seek(&part, offset) == 0 &&
             mtd_write(&part, image, (int)len) == 0 &&
             mtd_verify_crc(&part, offset, len, swu_crc32(0, image, (int)len), &computed) == 0;
    swu_log("%s: environment copy at %llu %s", label, offset, ok ? "written and read back" : "FAILED");
    mtd_close(&part);
    return ok ? 0 : -1;
}

/* Both copies of a whole redundant environment: the one in NAND.U-Boot first, then the primary,
 * which until then still holds the 8 KiB copy and so is not a valid copy of its own. */
static int write_redundant_env(const unsigned char *entries, unsigned len)
{
    unsigned char *image = malloc(REDUND_ENV_SIZE);
    int ok = image &&
             encode_redundant_env(entries, len, REDUND_ENV_FRESH_REDUNDANT, image) == 0 &&
             write_env_copy("NAND.U-Boot", REDUND_ENV_OFFSET, image, REDUND_ENV_SIZE) == 0 &&
             encode_redundant_env(entries, len, REDUND_ENV_FRESH_PRIMARY, image) == 0 &&
             write_env_copy("NAND.U-Boot-Env", 0, image, REDUND_ENV_SIZE) == 0;
    free(image);
    return ok ? 0 : -1;
}

#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
/* Erase the redundant copy. Done before the boot chain is written, while Bose's U-Boot still
 * boots and never reads that block: a copy left from an earlier SoundTap would outrank the
 * 8 KiB copy this install writes the moment our U-Boot runs. */
static int drop_redundant_env(void)
{
    struct mtd_part part;
    if (mtd_open(&part, "NAND.U-Boot", 1) != 0) return -1;
    int ok = REDUND_ENV_OFFSET + part.erase_size <= part.size &&
             mtd_erase_block(&part, REDUND_ENV_OFFSET) == 0;
    swu_log("NAND.U-Boot: redundant environment block %s", ok ? "erased" : "NOT erased");
    mtd_close(&part);
    return ok ? 0 : -1;
}
#endif

/* The two markers appended to nand_args2 are the command line a bootloader that passes bootargs
 * will hand over: the kernel is built without CONFIG_CMDLINE_FORCE, which would pin the root
 * filesystem to its built-in line whatever slot U-Boot chose. os/board/image/linux.fragment
 * records that, and buildroot_wla_nand_check.py holds the device tree and the fragment to each
 * other.
 *
 * A kernel read raw out of a kernel partition still overrides all of this, because it carries an
 * appended device tree whose own chosen/bootargs wins.
 *
 * They are written anyway because they are the correct line for a bootloader that does pass
 * bootargs, and because an environment that says something different from the kernel it boots is
 * a trap for the next person reading mtd5. ubi.mtd= names the partition rather than numbering it:
 * the name survives a renumbered table and is the same string on the board and in the offline
 * gate, which has no /proc/mtd to look a number up in. */
static const char *persistent_mtd_name(void)
{
    /* Stock misspells "Persistent" on some images; the board decides which one it is. */
    static const char *const names[] = { "NAND.Persistent-Storage", "NAND.Persistant-Storage" };
    FILE *table = fopen("/proc/mtd", "r");
    if (table) {
        char line[256];
        while (fgets(line, sizeof line, table)) {
            for (unsigned i = 0; i < sizeof names / sizeof *names; i++) {
                char quoted[160];
                snprintf(quoted, sizeof quoted, "\"%s\"", names[i]);
                if (strstr(line, quoted)) {
                    fclose(table);
                    return names[i];
                }
            }
        }
        fclose(table);
    }
    return names[0];
}

#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
/* When this install also replaces the boot chain, the environment's two slot recipes have to be
 * replaced with it. Bose's nand_boot and nand_boot2 are written for Bose's 2014.07 fork: they use
 * `nboot.e` and their own `fixsig`, and neither command exists in mainline U-Boot. Leaving them in
 * place while installing our U-Boot means bootcmd resolves to a recipe our bootloader cannot run,
 * and the slot selection this installer just made decides nothing.
 *
 * So these entries travel with the bootloader, and only with it. A package that does not carry
 * MLO and u-boot.img must leave Bose's recipes exactly as they are -- that board keeps booting on
 * Bose's U-Boot, which needs them.
 *
 * mtdids and mtdparts are here for the same reason. U-Boot builds them into its *default*
 * environment; a stored environment replaces that default whole, and Bose's has neither, so
 * `ubi part NAND.File-System-Pong` would have no partition table to resolve the name against.
 *
 * These strings are the ones os/support/uboot_recipes.py writes, byte for byte. That file is
 * where they are explained and it is the copy to change first; os/tests/installer/test_wla_env_recipes.py
 * fails the build if the two ever drift apart.
 *
 * Size: measured against this board's own factory environment, Bose's 78 entries use 6321 of the
 * 8188 usable bytes. Replacing their two recipes frees 439 and these seven entries cost 1637, so
 * a factory environment lands near 7400.
 *
 * The worst case is an environment that is just as full but has no recipes of ours to replace, so
 * nothing is freed and everything is appended. os/support/wla_stock_route_dryrun.py builds exactly
 * that and measures it: 8014 of 8188 bytes used, 174 to spare. It fits, and not by much -- which
 * is why the rebuild loop below fails closed rather than truncating.
 */
static const char *const mainline_env_templates[] = {
    "nand_boot=setenv slot Ping; setenv rmtd 8; setenv kaddr 0x280000; run slot_boot",
    "nand_boot2=setenv slot Pong; setenv rmtd 9; setenv kaddr 0x780000; run slot_boot",
    "slot_boot=if ubi part NAND.File-System-${slot} 2048; then if ubifsmount ubi0:rootfs; "
        "then if ubifsload 0x83000000 /boot/%s; then mw.l 0x82000000 0 1; ubifsload "
        "0x82000000 /boot/%s; if itest.l *0x82000000 == 0x56190527; then run "
        "soundtap_args; bootm 0x82000000 - 0x83000000; fi; run stock_args; if nand read 0x80007fc0 "
        "${kaddr} 0x500000; then if fixsig 0x80007fc0; then bootm 0x80007fc0 - 0x83000000; fi; "
        "fi; fi; fi; fi; reset",
    "soundtap_args=setenv bootargs console=ttyGS0,115200n8 root=ubi0:rootfs ro "
        "rootfstype=ubifs ubi.mtd=${rmtd},2048 rootwait init=/init mem=128M cma=8M panic=10 "
        "omap_wdt.early_enable=1 variant=%s module_type=%s country=GB region=GB "
        "soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048",
    "stock_args=setenv bootargs console=ttyO3,115200n8 quiet root=ubi0:rootfs ro "
        "ubi.mtd=${rmtd},2048 noinitrd rootfstype=ubifs rootwait=1 ip=off variant=%s "
        "variant_raw=${variant_raw} variant_mode=normal oled_type= country=GB region=GB",
    "mtdids=nand0=nand0",
    "mtdparts=mtdparts=nand0:0x20000@0x0(NAND.SPL),0x20000@0x20000(NAND.SPL.backup1),0x20000@0x40000(NAND.SPL.backup2),0x20000@0x60000(NAND.SPL.backup3),0x1E0000@0x80000(NAND.U-Boot),0x20000@0x260000(NAND.U-Boot-Env),0x500000@0x280000(NAND.Kernel-Ping),0x500000@0x780000(NAND.Kernel-Pong),0x6000000@0xC80000(NAND.File-System-Ping),0x6000000@0x6C80000(NAND.File-System-Pong),0x2600000@0xCC80000(NAND.Persistent-Storage),0xB80000@0xF280000(NAND.Update-Staging),0x40000@0xFE00000(NAND.Manufacturing-Data)",
};
#define MAINLINE_ENV_COUNT (sizeof mainline_env_templates / sizeof *mainline_env_templates)

/* What differs between the three speakers is four strings, and nothing else. The NAND wiring,
 * the GPMC timings and all thirteen partitions are byte-identical across binky, rhino and
 * spotty -- checked against os/board/common/dts/ti/omap/am335x-soundtap-st10.dts and
 * am335x-soundtap-nand.dtsi -- so one set of recipes serves every model once the kernel name,
 * the device-tree name and the two variant fields come from here. This is what one package
 * with mainline U-Boot on all three models needs from the environment; the bootloader images
 * themselves still have to be proven per model. */
struct model_boot {
    const char *variant, *dtb, *kernel, *module_type;
};
static const struct model_boot model_boots[] = {
    { "binky",  "binky_sm2.dtb",  "uImage-binky",  "sm2" },
    { "rhino",  "rhino_sm2.dtb",  "uImage-rhino",  "sm2" },
    { "spotty", "spotty_scm.dtb", "uImage-spotty", "scm" },
    /* SoundTouch 30 Series I: the ST20's board (see board_variant). */
    { "mojo", "spotty_scm.dtb", "uImage-spotty", "scm" },
    /* ST20 and ST30 Series III: the ST10's module and kernel, their own tree (no lamps). */
    { "spotty3", "spotty3_sm2.dtb", "uImage-rhino", "sm2" },
    { "mojo3", "spotty3_sm2.dtb", "uImage-rhino", "sm2" },
    /* SoundTouch 300, proof of concept. */
    { "ginger", "ginger_sm2.dtb", "uImage-ginger", "sm2" },
};

/* The recipes for the board this is running on, filled once. NULL means a rendered entry did
 * not fit its buffer, and every caller fails the install rather than writing a truncated boot
 * command -- a half-written recipe is a board that does not boot.
 *
 * Unknown hardware is rejected before NAND is opened, so this lookup always has a match. */
static const char *const *mainline_env(void)
{
    static char buf[MAINLINE_ENV_COUNT][1024];
    static const char *table[MAINLINE_ENV_COUNT];
    static int built;
    if (built) return table[0] ? table : NULL;
    built = 1;
    const struct model_boot *m = NULL;
    const char *variant = soundtap_variant();
    for (unsigned i = 0; i < sizeof model_boots / sizeof *model_boots; i++)
        if (!strcmp(variant, model_boots[i].variant)) m = &model_boots[i];
    if (!m) return NULL;
    for (unsigned i = 0; i < MAINLINE_ENV_COUNT; i++) {
        const char *t = mainline_env_templates[i];
        int n;
        /* Index-keyed on purpose: os/support/uboot_recipes.py fills the same indices with
         * the same arguments, and os/tests/installer/test_wla_env_recipes.py holds the two together. */
        if (i == 2) n = snprintf(buf[i], sizeof buf[i], t, m->dtb, m->kernel);
        else if (i == 3) n = snprintf(buf[i], sizeof buf[i], t, m->variant, m->module_type);
        else if (i == 4) n = snprintf(buf[i], sizeof buf[i], t, m->variant);
        else n = snprintf(buf[i], sizeof buf[i], "%s", t);
        if (n < 0 || (unsigned)n >= sizeof buf[i]) {
            swu_log("environment recipe %u does not fit its buffer", i);
            table[0] = NULL;
            return NULL;
        }
        table[i] = buf[i];
    }
    return table;
}

/* If this entry's key is one this build replaces, return the replacement and report which slot of
 * the table it came from; otherwise NULL. The key comparison includes the '=' so that a longer
 * name starting with a shorter one -- nand_boot2 against nand_boot -- cannot match the wrong row. */
static const char *mainline_entry(const char *entry, unsigned *index)
{
    const char *const *env = mainline_env();
    if (!env) return NULL;
    for (unsigned i = 0; i < MAINLINE_ENV_COUNT; i++) {
        const char *equals = strchr(env[i], '=');
        unsigned keylen = (unsigned)(equals - env[i]) + 1;
        if (!strncmp(entry, env[i], keylen)) {
            *index = i;
            return env[i];
        }
    }
    return NULL;
}
#endif

/* with_bootloaders says whether this install actually replaced the boot chain. Entries the vendor
 * environment already names are replaced either way, and that is deliberate: the recipes run on
 * Bose's U-Boot too, and they are how an ST10 on Bose's boot chain boots SoundTap at all --
 * /proc/cmdline on the ST10 carries soundtap_args. What with_bootloaders gates is
 * appending the entries the vendor environment lacks, which only mainline U-Boot asks for. The
 * compile flag alone cannot answer that -- the same binary installs both kinds of package. */
static int switch_bootcmd(int to_pong, int with_bootloaders, int redundant)
{
#ifndef SOUNDTAP_INSTALL_BOOTLOADERS
    (void)with_bootloaders; /* a build without the phase never writes those entries at all */
#endif
    struct mtd_part env;
    if (mtd_open(&env, "NAND.U-Boot-Env", 1) != 0) return -1;
    if (env.size < UBOOT_ENV_SIZE || env.erase_size < UBOOT_ENV_SIZE) {
        swu_log("environment partition is too small for a %d byte environment", UBOOT_ENV_SIZE);
        mtd_close(&env);
        return -1;
    }
    unsigned char *image = malloc(UBOOT_ENV_SIZE);
    unsigned char *rebuilt = malloc(REDUND_ENV_SIZE);
    if (!image || !rebuilt) {
        free(image); free(rebuilt); mtd_close(&env);
        return -1;
    }
    unsigned long long env_at = 0;
    int ok = mtd_read(&env, &env_at, image, UBOOT_ENV_SIZE) == 0;

    /* The environment is a CRC32 followed by NUL-separated key=value entries, 0xff padded. */
    const char *want = to_pong ? "bootcmd=run nand_boot2;" : "bootcmd=run nand_boot;";
    /* An ST300 left on Bose's U-Boot (a package without a boot chain) has no boot count, so
     * a SoundTap that hangs before soundtapd runs would boot again and again. That U-Boot
     * then points bootcmd back at stock and saves it before it loads our kernel: every
     * SoundTap boot there is a one-shot. saveenv is in that U-Boot's command table and writes
     * the same 0x2000 environment (fw_env.config, CRC over 0x2000). Ours counts boots. */
    if (!strcmp(running_variant(), "ginger") && !with_bootloaders)
        want = to_pong ? "bootcmd=setenv bootcmd run nand_boot; saveenv; run nand_boot2;"
                       : "bootcmd=setenv bootcmd run nand_boot2; saveenv; run nand_boot;";
    char persistent[96];
    snprintf(persistent, sizeof persistent, " ubi.mtd=%s,2048", persistent_mtd_name());
    /* Appended to nand_args2, each one only when it is not already there, so reinstalling does
     * not grow the entry. Every marker starts with the separating space. */
    const char *markers[] = { " soundtap.nandroot=1", persistent };
    const unsigned marker_count = sizeof markers / sizeof *markers;
    unsigned data_size = UBOOT_ENV_SIZE - 4;
    /* What the rewritten environment may fill: the single 8 KiB copy, or one redundant copy. */
    unsigned room = redundant ? REDUND_ENV_SIZE - 5 : data_size;
    unsigned out = 0;
    int replaced = 0;
    int nand_args = 0;
#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
    /* Our U-Boot's console is UART0, which on the ST300 is the soundbar processor's 921600 link.
     * Any byte it sends during the factory bootdelay=1 window would stop autoboot at a prompt
     * nobody can reach. -2 boots without looking at the console at all. */
    int lpm_on_console = !strcmp(running_variant(), "ginger");
    int carried[MAINLINE_ENV_COUNT];
    memset(carried, 0, sizeof carried);
#endif
    /* Rewrite only an environment U-Boot itself would accept: with a bad CRC it boots its
     * built-in default, and rebuilding from those bytes would make garbage the live one. */
    if (ok && swu_crc32(0, image + 4, (int)data_size) !=
              ((unsigned)image[0] | (unsigned)image[1] << 8 | (unsigned)image[2] << 16 |
               (unsigned)image[3] << 24)) {
        swu_log("environment CRC does not match; not rewriting it");
        ok = 0;
    }
    if (ok) {
        memset(rebuilt, 0, room);
        for (unsigned at = 0; at < data_size && image[4 + at] && image[4 + at] != 0xff;) {
            const char *entry = (const char *)image + 4 + at;
            const unsigned char *nul = memchr(image + 4 + at, 0, data_size - at);
            if (!nul) {
                swu_log("environment entry at %u has no terminator", at);
                ok = 0;
                break;
            }
            unsigned len = (unsigned)(nul - (image + 4 + at));
            int is_bootcmd = !strncmp(entry, "bootcmd=", 8);
            int is_nand_args = !strncmp(entry, "nand_args2=", 11);
#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
            /* Rewritten below for the slot being selected, like internal/flash armRollback. */
            if (with_bootloaders &&
                (!strncmp(entry, "altbootcmd=", 11) || !strncmp(entry, "bootlimit=", 10) ||
                 !strncmp(entry, "upgrade_available=", 18) || !strncmp(entry, "bootcount=", 10) ||
                 (lpm_on_console && !strncmp(entry, "bootdelay=", 10)))) {
                at += len + 1;
                continue;
            }
#endif
            const char *copy = is_bootcmd ? want : entry;
#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
            if (!is_bootcmd) {
                unsigned slot;
                const char *mainline = mainline_entry(entry, &slot);
                if (mainline) {
                    copy = mainline;
                    carried[slot] = 1;
                }
            }
#endif
            unsigned copy_len = (unsigned)strlen(copy);
            unsigned marker_len = 0;
            if (is_nand_args) {
                for (unsigned m = 0; m < marker_count; m++)
                    if (!strstr(entry, markers[m] + 1))
                        marker_len += (unsigned)strlen(markers[m]);
            }
            if (out + copy_len + marker_len + 2 >= room) { ok = 0; break; }
            memcpy(rebuilt + out, copy, copy_len);
            out += copy_len;
            if (is_nand_args) {
                for (unsigned m = 0; m < marker_count; m++) {
                    if (strstr(entry, markers[m] + 1)) continue;
                    unsigned len_m = (unsigned)strlen(markers[m]);
                    memcpy(rebuilt + out, markers[m], len_m);
                    out += len_m;
                }
            }
            rebuilt[out++] = 0;
            replaced |= is_bootcmd;
            nand_args += is_nand_args;
            at += len + 1;
        }
    }
    if (nand_args != 1) ok = 0;
    if (ok && !replaced) {
        unsigned copy_len = (unsigned)strlen(want);
        if (out + copy_len + 2 < room) {
            memcpy(rebuilt + out, want, copy_len);
            out += copy_len + 1;
        } else {
            ok = 0;
        }
    }
#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
    /* Whatever the vendor environment did not already name, append. A factory environment has
     * nand_boot and nand_boot2 and neither mtdids nor mtdparts, so most of this table lands here. */
    const char *const *recipes = mainline_env();
    if (with_bootloaders && !recipes) ok = 0;
    for (unsigned i = 0; ok && with_bootloaders && i < MAINLINE_ENV_COUNT; i++) {
        if (carried[i]) continue;
        unsigned len = (unsigned)strlen(recipes[i]);
        if (out + len + 2 >= room) {
            swu_log("environment has no room for %s", recipes[i]);
            ok = 0;
            break;
        }
        memcpy(rebuilt + out, recipes[i], len);
        out += len;
        rebuilt[out++] = 0;
    }
    /* Our U-Boot counts warm boots that soundtapd did not confirm, and past bootlimit runs
     * altbootcmd: the slot this install leaves, which still holds what ran before it. Without
     * these a first SoundTap boot that hangs (on a model never booted before, the ST300) would
     * be retried forever. Same keys and limit as internal/flash armRollback (BootLimit 4). */
    /* upgrade_available goes with the selection soundtapd will confirm, the one written as
     * the redundant environment once our boot chain is in: while it is set our U-Boot keeps
     * the slot's boot count in the environment too, where a power cut does not clear it,
     * and soundtapd's boot confirm clears it (ADR 0010). */
    const char *rollback[] = { to_pong ? "altbootcmd=run nand_boot;" : "altbootcmd=run nand_boot2;",
                               "bootlimit=4", redundant ? "upgrade_available=1" : NULL, "bootdelay=-2" };
    unsigned rollback_count = lpm_on_console ? 4 : 3;
    for (unsigned i = 0; ok && with_bootloaders && i < rollback_count; i++) {
        if (!rollback[i]) continue;
        unsigned len = (unsigned)strlen(rollback[i]);
        if (out + len + 2 >= room) {
            swu_log("environment has no room for %s", rollback[i]);
            ok = 0;
            break;
        }
        memcpy(rebuilt + out, rollback[i], len);
        out += len;
        rebuilt[out++] = 0;
    }
#endif
    if (ok && redundant) {
        rebuilt[out++] = 0;
        ok = write_redundant_env(rebuilt, out) == 0;
    } else if (ok) {
        rebuilt[out++] = 0;
        /* Zero padding, not 0xff: U-Boot zeroes its buffer before saving, and this board's own
         * factory mtd5 is zero-filled from the terminating empty entry to the end of the 8 KiB
         * window. internal/ubootenv encodes the same way, so an environment written here and one
         * written by the web install path are byte-identical and can be diffed against a dump.
         * (The rest of the 128 KiB partition, outside the window, stays erased 0xff.) */
        memset(image + 4, 0, data_size);
        memcpy(image + 4, rebuilt, out);
        unsigned crc = swu_crc32(0, image + 4, (int)data_size);
        image[0] = crc & 0xff;
        image[1] = (crc >> 8) & 0xff;
        image[2] = (crc >> 16) & 0xff;
        image[3] = (crc >> 24) & 0xff;
        unsigned computed;
        ok = mtd_erase_all(&env) >= 0 &&
             mtd_write(&env, image, UBOOT_ENV_SIZE) == 0 &&
             mtd_verify_crc(&env, 0, UBOOT_ENV_SIZE,
                            swu_crc32(0, image, UBOOT_ENV_SIZE), &computed) == 0;
    }
    swu_log("bootcmd -> \"%s\": %s%s", want, ok ? "written" : "FAILED",
            redundant ? " (redundant environment)" : "");
    free(rebuilt);
    free(image);
    mtd_close(&env);
    return ok ? 0 : -1;
}
#endif  /* SOUNDTAP_INSTALL_NAND */

/* The product this installer is running on, as the kernel command line names it: binky on
 * the Wireless Link, rhino on the SoundTouch 10, spotty on the 20. Read once; "" when the
 * command line carries no variant, which is how a build for a board we do not know behaves.
 *
 * This is what lets one package serve every model. Stock never validated the module chain --
 * it runs module 0 and streams the rest in -- so the shape of the file was only ever our own
 * lock on our own door, and a package can carry a module per model as long as the installer
 * takes the right one. See docs/decisions/0008-one-universal-stu.md. */
static const char *running_variant(void)
{
    static char variant[32];
    static int read_once;
    if (read_once) return variant;
    read_once = 1;
    /* Same escape hatch as SOUNDTAP_CANDIDATE_ROOTFS: the offline tests have no
     * /proc/cmdline of the board's shape to read. */
    const char *forced = getenv("SOUNDTAP_VARIANT");
    if (forced) {
        snprintf(variant, sizeof variant, "%s", forced);
        return variant;
    }
    int fd = open("/proc/cmdline", O_RDONLY);
    char line[1024];
    int n = fd >= 0 ? (int)read(fd, line, sizeof line - 1) : -1;
    if (fd >= 0) close(fd);
    if (n <= 0) return variant;
    line[n] = 0;
    /* "variant=" also matches "variant_raw=" and "variant_mode="; require the exact key. */
    for (const char *at = strstr(line, "variant="); at; at = strstr(at + 1, "variant=")) {
        if (at != line && at[-1] != ' ') continue;
        at += 8;
        int i = 0;
        while (at[i] && at[i] != ' ' && i < (int)sizeof variant - 1) {
            variant[i] = at[i];
            i++;
        }
        variant[i] = 0;
        break;
    }
    return variant;
}

/* variant= does not name the module: an ST20 Series III says variant=spotty like the Series
 * I, but carries the Shelby 2 module of the ST10 instead of the Series I's Common Module, and
 * the Series I's boot chain would not start it. The device tree's first compatible says which
 * one this is -- "bose,scm" or "bose,sm2" under stock, "bose,spotty-scm" and the like under
 * SoundTap. Returns "scm", "sm2", or "" when there is no Bose device tree to read (offline
 * tests, a host). */
static const char *running_module(void)
{
    const char *path = getenv("SOUNDTAP_DT_COMPATIBLE");
    int fd = open(path ? path : "/proc/device-tree/compatible", O_RDONLY);
    char first[64];
    int n = fd >= 0 ? (int)read(fd, first, sizeof first - 1) : -1;
    if (fd >= 0) close(fd);
    if (n <= 0) return "";
    first[n] = 0; /* the first string ends at its own NUL */
    size_t len = strlen(first);
    if (len >= 3 && !strcmp(first + len - 3, "scm")) return "scm";
    if (len >= 3 && !strcmp(first + len - 3, "sm2")) return "sm2";
    return "";
}

/* The board this is, for every decision but the kernel command line.
 *   - mojo, the SoundTouch 30 Series I, is the ST20's Shelby Common Module in a bigger box:
 *     stock's mojo_scm and spotty_scm trees differ only in the codec script's name and the
 *     sync-out timer, so it takes the ST20's kernel, boot chain and payloads.
 *   - The ST20 and ST30 Series III say spotty and mojo too, on the ST10's Shelby 2 module,
 *     and stock's spotty_sm2 and mojo_sm2 trees wire the amplifier, codec, keypad and IR as
 *     rhino_sm2 does, so they take the ST10's. Under SoundTap they are spotty3 and mojo3.
 * soundtap_variant() is the name each keeps on the command line, for its own codec script
 * and its name. */
static const char *board_variant(void)
{
    const char *v = running_variant();
    int sm2 = !strcmp(running_module(), "sm2");
    if (!strcmp(v, "spotty3") || !strcmp(v, "mojo3")) return "rhino";
    if ((!strcmp(v, "spotty") || !strcmp(v, "mojo")) && sm2) return "rhino";
    if (!strcmp(v, "mojo")) return "spotty";
    return v;
}

static const char *soundtap_variant(void)
{
    const char *v = running_variant();
    if (!strcmp(running_module(), "sm2")) {
        if (!strcmp(v, "spotty")) return "spotty3";
        if (!strcmp(v, "mojo")) return "mojo3";
    }
    return v;
}

static int known_variant(void)
{
    const char *variant = board_variant();
    int known = !strcmp(variant, "binky") || !strcmp(variant, "rhino") ||
                !strcmp(variant, "spotty") || !strcmp(variant, "ginger");
    const char *want = !strcmp(variant, "spotty") ? "scm" : "sm2";
    const char *module = running_module();
    if (known && module[0] && strcmp(module, want)) {
        swu_log("variant %s on a %s module: not a board this package was built for",
                running_variant(), module);
        return 0;
    }
    return known;
}

/* A module name may carry a model suffix: "ubi.img.binky" is for the Wireless Link only.
 * Returns the bare name for a module this board should take, and NULL for one meant for a
 * different model. An unsuffixed name is for every model, which is what today's packages use
 * and why this changes nothing for them. */
static const char *module_for_this_model(const char *name, char *bare, int bare_size)
{
    const char *dot = strrchr(name, '.');
    static const char *models[] = {"binky", "rhino", "spotty"};
    int suffixed = 0;
    for (unsigned i = 0; dot && i < sizeof models / sizeof models[0]; i++)
        if (!strcmp(dot + 1, models[i])) suffixed = 1;
    if (!suffixed) return name;
    int n = (int)(dot - name);
    /* The boot chain is named for binky. The ST10 is the Wireless Link's sm2 module and boots
     * the same SPL and U-Boot; the ST20 boots the same U-Boot behind an SPL of its own, built
     * to ask USB first because its ROM boots NAND before USB. */
    int is_spl = n == sizeof "soundtap-spl" - 1 && !strncmp(name, "soundtap-spl", n);
    int is_uboot = n == sizeof "soundtap-uboot" - 1 && !strncmp(name, "soundtap-uboot", n);
    int sm2_chain = !strcmp(dot + 1, "binky") &&
                    ((!strcmp(board_variant(), "rhino") && (is_spl || is_uboot)) ||
                     (!strcmp(board_variant(), "spotty") && is_uboot));
    /* The SoundTouch 300 is the Wireless Link's module in a soundbar and runs the same
     * kernel, so it takes everything the package names for binky, boot chain included. */
    int ginger = !strcmp(dot + 1, "binky") && !strcmp(board_variant(), "ginger");
    if (strcmp(dot + 1, board_variant()) && !sm2_chain && !ginger) return NULL;
    if (n >= bare_size) return NULL;
    memcpy(bare, name, n);
    bare[n] = 0;
    return bare;
}

static struct sink sink_for(struct install *install, const char *full_name)
{
    struct sink sink;
    memset(&sink, 0, sizeof sink);
    sink.write = sink_discard;
    sink.what = "discard";
    char bare[64];
    const char *name = module_for_this_model(full_name, bare, sizeof bare);
    if (!name) {
        sink.what = "discard (another model)";
        return sink;
    }
#ifdef SOUNDTAP_INSTALL_NAND
    /* Two vocabularies name the same four payloads. The stock-shaped package borrows Bose's
     * names because that is the shape their updater's own page produces; a package of ours
     * calls them what they are. Stock does not read module names at all -- it takes module 0
     * by position, see docs/decisions/0008-one-universal-stu.md
     * -- so one package can use our vocabulary on both routes, and this is what lets the
     * :17008 route and SoundTap's own web API read the same file. */
    if (!strcmp(name, "linuxPatched") || !strcmp(name, "soundtap-kernel")) {
        sink.write = sink_nand;
        sink.ctx = &install->kernel;
        sink.what = install->kernel.part.label;
    } else if (!strcmp(name, "ubi.img") || !strcmp(name, "soundtap-rootfs")) {
        sink.write = sink_nand;
        sink.ctx = &install->rootfs;
        sink.what = install->rootfs.part.label;
    }
#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
    else if (!strcmp(name, "MLO") || !strcmp(name, "soundtap-spl")) {
        static struct ram_sink spl;
        spl.buffer = install->spl;
        spl.capacity = sizeof install->spl;
        spl.length = &install->spl_len;
        sink.write = sink_ram;
        sink.ctx = &spl;
        sink.what = "MLO (held for the bootloader phase)";
    } else if (!strcmp(name, "u-boot.img") || !strcmp(name, "soundtap-uboot")) {
        static struct ram_sink uboot;
        uboot.buffer = install->uboot;
        uboot.capacity = sizeof install->uboot;
        uboot.length = &install->uboot_len;
        sink.write = sink_ram;
        sink.ctx = &uboot;
        sink.what = "u-boot.img (held for the bootloader phase)";
    }
#endif
#else
    (void)install; (void)name;
#endif
    return sink;
}

static int consume_chain(struct puller *p, struct install *install, unsigned long long declared)
{
    unsigned char header_raw[STU_HDR] = {0};
    int first = 1;
    unsigned long long consumed = 0;
    for (;;) {
        int want = first ? STU_HDR : STU_HDR - STU_OVERLAP;
        unsigned char *target = first ? header_raw : header_raw + STU_OVERLAP;
        int got = puller_read(p, target, want);
        if (got == 0 && !first) break;
        if (got != want) {
            if (got == 0) break;
            swu_log("truncated module header: %d of %d bytes", got, want);
            return -1;
        }
        first = 0;

        struct module_header header;
        parse_header(header_raw, &header);
        if (!header_crc_ok(header_raw, &header)) {
            swu_log("%s: header CRC mismatch", header.name);
            return -1;
        }
        if (header.size <= STU_OVERLAP) {
            swu_log("%s: module size %u does not exceed the header overlap", header.name,
                    header.size);
            return -1;
        }
        consumed += STU_HDR + header.size - (install->modules ? STU_OVERLAP : 0);
        int last = (consumed >= declared);
        struct sink sink = sink_for(install, header.name);
        if (consume_module(p, &header, &sink, header_raw, last) < 0) return -1;
#ifdef SOUNDTAP_INSTALL_NAND
        if (sink.ctx == &install->kernel) {
            install->kernel_len = sink.written;
            install->kernel_crc = sink.crc;
        } else if (sink.ctx == &install->rootfs) {
            install->rootfs_len = sink.written;
            install->rootfs_crc = sink.crc;
        }
#endif
        install->modules++;
        if (last) break;
    }
    /* End of stream at a module boundary is not the end of the package: a cut stream would
     * otherwise "install" without the modules after the cut, its bootloaders among them. */
    if (consumed != declared) {
        swu_log("module chain ended at %llu of %llu declared bytes", consumed, declared);
        return -1;
    }
    swu_log("module chain verified: %d modules, %llu of %llu declared bytes", install->modules,
            consumed, declared);
    return install->modules;
}

#ifdef SOUNDTAP_INSTALL_NAND
/* The module suffix picks the slot, but the image inside is only checked by CRC, so a kernel
 * built for another model under the right suffix would be made bootable. A SoundTap kernel names
 * its model in the uImage header ("Linub-6.12 SoundTap st10"); hold it to the board. One image
 * serves rhino and binky, so the Wireless Link takes the st10 name as well as its own. A kernel
 * without "SoundTap " in its name is left to the CRC, as before. */
static int kernel_fits_model(const unsigned char *block, unsigned len)
{
    static const unsigned char magic[4] = {0x27, 0x05, 0x19, 0x56};
    if (len < 64 || memcmp(block, magic, sizeof magic)) return 1;
    char name[33];
    memcpy(name, block + 32, 32);
    name[32] = 0;
    const char *at = strstr(name, "SoundTap ");
    if (!at) return 1;
    at += sizeof "SoundTap " - 1;
    const char *variant = board_variant();
    if (!strcmp(variant, "rhino")) return !strcmp(at, "st10");
    if (!strcmp(variant, "binky") || !strcmp(variant, "ginger"))
        return !strcmp(at, "st10") || !strcmp(at, "binky");
    if (!strcmp(variant, "spotty")) return !strcmp(at, "st20");
    return 0;
}
#endif

#ifdef SOUNDTAP_INSTALL_NAND
/* Everything is on flash except the kernel's first block. Verify what is there, then write that
 * block and verify the kernel. Slot selection waits until the optional bootloader phase too. */
static int finish_transaction(struct install *install)
{
    if (!install->rootfs_len || !install->kernel_len) {
        swu_log("package carried no %s image", install->rootfs_len ? "kernel" : "rootfs");
        return -1;
    }
    unsigned computed;
    if (mtd_verify_crc(&install->rootfs.part, 0, install->rootfs_len, install->rootfs_crc,
                       &computed) != 0)
        return -1;

    struct nand_sink *kernel = &install->kernel;
    if (!kernel_fits_model(kernel->first_block, kernel->first_block_len)) {
        swu_log("kernel image is built for another model than %s; candidate slot stays unbootable",
                board_variant());
        return -1;
    }
    if (mtd_seek(&kernel->part, 0) != 0) return -1;
    if (mtd_write(&kernel->part, kernel->first_block, (int)kernel->first_block_len) != 0) {
        swu_log("writing the kernel's first block failed; candidate slot stays unbootable");
        return -1;
    }
    if (mtd_verify_crc(&kernel->part, 0, install->kernel_len, install->kernel_crc, &computed) != 0)
        return -1;

    swu_log("slot images verified: %llu byte kernel and %llu byte rootfs",
            install->kernel_len, install->rootfs_len);
    return 0;
}
#endif

#if defined(SOUNDTAP_INSTALL_NAND) && defined(SOUNDTAP_INSTALL_BOOTLOADERS)
/* Replace the blocks [offset, end) of a partition with a RAM image and read it back; end 0 is
 * the end of the partition. No block outside the range is erased or written: in NAND.U-Boot the
 * two U-Boot copies and the redundant environment share one partition. */
static int replace_range(const char *label, unsigned long long offset, unsigned long long end,
                         const unsigned char *image, unsigned len)
{
    struct mtd_part part;
    if (mtd_open(&part, label, 1) != 0) return -1;
    if (!end) end = part.size;
    if (offset % part.erase_size || end % part.erase_size || end > part.size ||
        offset + len > end) {
        swu_log("%s: %u bytes at %llu do not fit before %llu", label, len, offset, end);
        mtd_close(&part);
        return -1;
    }
    unsigned crc = swu_crc32(0, image, (int)len), computed;
    unsigned write_len = len;
    const unsigned char *write_image = image;
    unsigned char *padded = NULL;
    if (part.is_char_device && part.write_size && len % part.write_size) {
        write_len = ((len + part.write_size - 1) / part.write_size) * part.write_size;
        padded = malloc(write_len);
        if (!padded) {
            swu_log("%s: cannot allocate %u-byte page-aligned image", label, write_len);
            mtd_close(&part);
            return -1;
        }
        memcpy(padded, image, len);
        memset(padded + len, 0xff, write_len - len);
        write_image = padded;
        swu_log("%s: padded %u bytes to %u-byte NAND pages", label, len, write_len);
    }
    int ok = 1;
    for (unsigned long long block = offset; ok && block < end; block += part.erase_size)
        if (mtd_skip_bad(&part, block) == block) ok = mtd_erase_block(&part, block) == 0;
    /* Bad blocks inside the range push the image back; it must run out of room at `end`, not
     * carry on into the next range, which holds the other U-Boot copy and is not erased. */
    part.limit = end;
    ok = ok && mtd_seek(&part, offset) == 0 && mtd_write(&part, write_image, (int)write_len) == 0;
    if (ok) ok = mtd_verify_crc(&part, offset, len, crc, &computed) == 0;
    free(padded);
    mtd_close(&part);
    swu_log("%s: %u bytes at %llu %s", label, len, offset, ok ? "written and verified" : "FAILED");
    return ok ? 0 : -1;
}

static int replace_partition(const char *label, const unsigned char *image, unsigned len)
{
    return replace_range(label, 0, 0, image, len);
}

/* NAND.U-Boot: the first copy in blocks 0-7, the spare (SecondUBootOffset in internal/install)
 * in blocks 8-13, the redundant environment in block 14. */
#define UBOOT_SPARE_OFFSET 0x100000ULL

/* Where the U-Boot on NAND ends: the first copy's uImage header gives its size, and the
 * blocks it takes are counted past bad ones, as it was written. 0 when the first block holds
 * no uImage, which is not a U-Boot this installer can reason about. */
static unsigned long long uboot_end_on_nand(void)
{
    struct mtd_part part;
    unsigned char header[64];
    unsigned long long at = 0, end = 0;
    if (mtd_open(&part, "NAND.U-Boot", 0) != 0) return 0;
    if (mtd_read(&part, &at, header, sizeof header) == 0 && be32(header) == 0x27051956) {
        unsigned long long left = sizeof header + (unsigned long long)be32(header + 12);
        for (end = 0; left && end < part.size; end += part.erase_size) {
            end = mtd_skip_bad(&part, end);
            if (end >= part.size) break;
            left -= left < part.erase_size ? left : part.erase_size;
        }
        if (left) end = part.size;
    }
    mtd_close(&part);
    return end;
}

/* The order soundtapd writes a boot chain in (bootSlots in internal/install, ADR 0009), and for
 * the same reason: at every moment a whole chain is on NAND.
 *
 *   1. The spare U-Boot, blocks 8-13 of NAND.U-Boot. Bose's SPL reads only the first copy, so it
 *      goes on booting Bose's U-Boot, which must end before the spare: that is checked first.
 *   2. SPL backups 1-3. The ROM reads NAND.SPL first and only looks at a backup when that one
 *      does not read cleanly, so Bose's SPL still runs.
 *   3. NAND.SPL. From here our SPL runs. The first U-Boot copy is still Bose's, a pairing nobody
 *      has proven, so our SPL boots the spare when the first copy is not ours
 *      (uboot-2026.07-spl-second-uboot.patch); a cut during this write sends the ROM to a backup,
 *      which is ours too.
 *   4. The first U-Boot copy, last. A cut during it leaves a copy that fails its CRC, and our
 *      SPL falls back to the spare.
 *
 * Every write is read back before the next one starts. The redundant environment's block, the
 * last of NAND.U-Boot, is erased before the first write: Bose's U-Boot never reads it, and a copy
 * left from an earlier SoundTap would outrank the environment this install writes. */
static int install_bootloaders(struct install *install)
{
    if (!install->spl_len || !install->uboot_len) {
        swu_log("package carries no bootloaders; the boot chain is left as it is");
        return 0;
    }
    if (strcmp(board_variant(), "binky") && strcmp(board_variant(), "rhino") &&
        strcmp(board_variant(), "spotty") && strcmp(board_variant(), "ginger")) {
        swu_log("bootloader payloads ignored on %s; only binky, rhino, spotty and ginger take a "
                "boot chain",
                board_variant()[0] ? board_variant() : "unknown model");
        install->spl_len = 0;
        install->uboot_len = 0;
        return 0;
    }
    unsigned long long running_end = uboot_end_on_nand();
    if (!running_end || running_end > UBOOT_SPARE_OFFSET) {
        swu_log("NAND.U-Boot: the U-Boot that boots now %s; the boot chain is left as it is",
                running_end ? "runs into the spare's blocks" : "is not a uImage");
        return -1;
    }
    if (drop_redundant_env() != 0) return -1;
    if (replace_range("NAND.U-Boot", UBOOT_SPARE_OFFSET, REDUND_ENV_OFFSET, install->uboot,
                      install->uboot_len) != 0)
        return -1;
    static const char *backups[] = {"NAND.SPL.backup1", "NAND.SPL.backup2", "NAND.SPL.backup3"};
    for (unsigned i = 0; i < sizeof backups / sizeof backups[0]; i++)
        if (replace_partition(backups[i], install->spl, install->spl_len) != 0) return -1;
    if (replace_partition("NAND.SPL", install->spl, install->spl_len) != 0) return -1;
    if (replace_range("NAND.U-Boot", 0, UBOOT_SPARE_OFFSET, install->uboot, install->uboot_len) != 0)
        return -1;
    swu_log("boot chain replaced: %u byte SPL in four copies, %u byte U-Boot and its spare",
            install->spl_len, install->uboot_len);
    return 0;
}
#endif

#ifdef SOUNDTAP_TRACE_UDP
static void survey_flash(void)
{
    int fd = open("/proc/mtd", O_RDONLY);
    if (fd < 0) {
        swu_log("survey: /proc/mtd unavailable errno=%d", errno);
        return;
    }
    char buf[2048];
    int n = (int)read(fd, buf, sizeof buf - 1);
    close(fd);
    if (n <= 0) return;
    buf[n] = 0;
    for (char *line = strtok(buf, "\n"); line; line = strtok(NULL, "\n"))
        swu_log("survey: /proc/mtd %s", line);
}
#endif


#ifdef SOUNDTAP_INSTALL_NAND
/* Once the candidate slot is selected the only correct ending is a reboot, and two things can
 * stop it. The link to stock is a plain socket written with write(), so the final
 * SoftwareUpdateStatus raises SIGPIPE the moment the coordinator has hung up -- default action,
 * process gone, bootcmd already rewritten and nobody left to act on it. The second is stock
 * reaping us once its own transfer is finished.
 *
 * So: SIGPIPE is ignored for the whole run (a dead link must never kill a write to NAND either),
 * and from the moment the slot is selected a termination signal reboots instead of exiting.
 */
static volatile sig_atomic_t reboot_armed;

/* The reset that does not depend on us surviving. On stock, with all writes done, either
 * reboot() hangs in a driver's shutdown hook or stock reaps the module with SIGKILL once it
 * reports completion -- which no handler sees, and which takes down a fallback forked only
 * after the final status.
 *
 * So the fallback is armed before the final status, in its own session so a kill aimed at our
 * process group misses it. After the delay it forces the reset without shutdown hooks: sysrq
 * 'b' first, then PRM_RSTCTRL.RST_GLOBAL_WARM_SW through /dev/mem. When the normal reboot
 * works, the child goes down with the board. */
#define RESET_FALLBACK_SECONDS 30

static void warm_reset_after(unsigned seconds)
{
    pid_t pid = fork();
    if (pid != 0) {
        if (pid < 0) swu_log("reset fallback not armed: fork errno=%d", errno);
        return;
    }
    setsid();
    signal(SIGTERM, SIG_IGN);
    signal(SIGHUP, SIG_IGN);
    signal(SIGINT, SIG_IGN);
    sleep(seconds);
    sync();
    int fd = open("/proc/sysrq-trigger", O_WRONLY);
    if (fd >= 0) {
        (void)write(fd, "b", 1);
        close(fd);
    }
    fd = open("/dev/mem", O_RDWR | O_SYNC);
    if (fd >= 0) {
        volatile unsigned *prm = mmap(NULL, 0x1000, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0x44e00000);
        if (prm != MAP_FAILED) prm[0xf00 / 4] = 1; /* PRM_RSTCTRL: RST_GLOBAL_WARM_SW */
    }
    _exit(1);
}

static void reboot_now(void)
{
    sync();
    reboot(RB_AUTOBOOT);
    /* Only reached if the call failed. Say so -- a silent non-reboot is the bug being fixed. */
    swu_log("reboot() failed errno=%d; falling back to /sbin/reboot", errno);
    execl("/sbin/reboot", "reboot", (char *)NULL);
    swu_log("/sbin/reboot unavailable errno=%d; power-cycle to run the new slot", errno);
}

static void reboot_on_signal(int signo)
{
    if (!reboot_armed) _exit(1);
    swu_log("signal %d after the slot was selected; rebooting", signo);
    reboot_now();
    _exit(1);
}
#endif

/* Stock saves its networks in NetworkProfiles.xml on /mnt/nv with each passphrase
 * AES-256-CBC encrypted. The key and IV are not on /mnt/nv: they are two ASCII digit strings,
 * 32 and 16 long, inside stock's own NetManager. The installer runs under stock, so on a first
 * install it takes them from the NetManager that is running and leaves them for SoundTap's
 * first boot, which decrypts the profile into wifi.conf (ADR 0008). This source holds no
 * Bose byte. Best effort: when anything is missing, the speaker opens the setup network as
 * before. */
#define STOCK_NETMANAGER "/opt/Bose/NetManager"
#define STOCK_WIFI_PROFILES "/mnt/nv/BoseApp-Persistence/1/NetworkProfiles.xml"
#define STOCK_WIFI_DST_DIR "/mnt/nv/soundtap"
#define STOCK_WIFI_DST STOCK_WIFI_DST_DIR "/stock-wifi-key"
#define STOCK_WIFI_KEY_LEN 32
#define STOCK_WIFI_IV_LEN 16
#define STOCK_WIFI_MAX_BINARY (16u << 20)

/* stock_wifi_key finds the key and IV in a NetManager image: of all its runs of 12 or more
 * ASCII digits there must be exactly two, one 32 long (the key) and one 16 long (the IV).
 * Any other count means a NetManager this rule was not checked against, and gives nothing. */
static int stock_wifi_key(const unsigned char *buf, size_t len,
                          unsigned char out[STOCK_WIFI_KEY_LEN + STOCK_WIFI_IV_LEN])
{
    int keys = 0, ivs = 0, others = 0;
    size_t i = 0;
    while (i < len) {
        if (buf[i] < '0' || buf[i] > '9') {
            i++;
            continue;
        }
        size_t start = i;
        while (i < len && buf[i] >= '0' && buf[i] <= '9') i++;
        size_t run = i - start;
        if (run < 12) continue;
        if (run == STOCK_WIFI_KEY_LEN) {
            keys++;
            memcpy(out, buf + start, STOCK_WIFI_KEY_LEN);
        } else if (run == STOCK_WIFI_IV_LEN) {
            ivs++;
            memcpy(out + STOCK_WIFI_KEY_LEN, buf + start, STOCK_WIFI_IV_LEN);
        } else {
            others++;
        }
    }
    return keys == 1 && ivs == 1 && others == 0 ? 0 : -1;
}

static int stock_wifi_key_from(const char *path,
                               unsigned char out[STOCK_WIFI_KEY_LEN + STOCK_WIFI_IV_LEN])
{
    int fd = open(path, O_RDONLY);
    if (fd < 0) return -1;
    struct stat st;
    if (fstat(fd, &st) != 0 || st.st_size <= 0 || (size_t)st.st_size > STOCK_WIFI_MAX_BINARY) {
        close(fd);
        return -1;
    }
    size_t len = (size_t)st.st_size;
    void *map = mmap(NULL, len, PROT_READ, MAP_PRIVATE, fd, 0);
    close(fd);
    if (map == MAP_FAILED) return -1;
    int rc = stock_wifi_key(map, len, out);
    munmap(map, len);
    return rc;
}

static void carry_stock_wifi(void)
{
    struct stat st;
    if (stat("/mnt/nv/wifi.conf", &st) == 0) return;   /* SoundTap already has a network */
    if (stat(STOCK_WIFI_PROFILES, &st) != 0) return;   /* stock saved no network */
    unsigned char key[STOCK_WIFI_KEY_LEN + STOCK_WIFI_IV_LEN];
    if (stock_wifi_key_from(STOCK_NETMANAGER, key) != 0) {
        swu_log("stock's Wi-Fi key not found; SoundTap will open its setup network");
        return;
    }
    mkdir(STOCK_WIFI_DST_DIR, 0755);
    char tmp[] = STOCK_WIFI_DST ".tmp";
    int out = open(tmp, O_WRONLY | O_CREAT | O_TRUNC, 0600);
    if (out < 0) return;
    int ok = write(out, key, sizeof key) == (ssize_t)sizeof key && fsync(out) == 0;
    close(out);
    if (!ok || rename(tmp, STOCK_WIFI_DST) != 0) {
        unlink(tmp);
        return;
    }
    swu_log("kept stock's Wi-Fi key for SoundTap's first boot");
}

int main(int argc, char **argv)
{
    /* `installer --which-module NAME` prints what this board would do with a module of that
     * name and exits. Stock never runs the installer with arguments -- it execs it with
     * argc=0 -- so this cannot be reached during an install, and it is the only way to check
     * the model-suffix rule without a speaker and a package. */
    if (argc == 3 && !strcmp(argv[1], "--which-module")) {
        char bare[64];
        const char *name = module_for_this_model(argv[2], bare, sizeof bare);
        printf("%s\n", name ? name : "(another model)");
        return 0;
    }
    /* `installer --board` prints the board this installer takes the running one for and
     * whether it installs on it ("rhino known"), and exits. Unreachable during an install for
     * the same reason; os/tests/build/test_models_table.py holds it to os/models.json. */
    if (argc == 2 && !strcmp(argv[1], "--board")) {
        const char *board = board_variant();
        printf("%s %s\n", board[0] ? board : "-", known_variant() ? "known" : "unknown");
        return 0;
    }
#ifdef SOUNDTAP_INSTALL_NAND
    /* `installer --kernel-fits MODEL` prints whether this board takes a kernel whose uImage
     * header names "Linux-6.12 SoundTap MODEL", and exits. Unreachable during an install. */
    if (argc == 3 && !strcmp(argv[1], "--kernel-fits")) {
        unsigned char head[64] = {0x27, 0x05, 0x19, 0x56};
        snprintf((char *)head + 32, 32, "Linux-6.12 SoundTap %s", argv[2]);
        printf("%s\n", kernel_fits_model(head, sizeof head) ? "yes" : "no");
        return 0;
    }
#endif
    /* `installer --stock-wifi-key FILE` prints the key and IV the installer would take from a
     * NetManager image, or "(none)". Unreachable during an install for the same reason. */
    if (argc == 3 && !strcmp(argv[1], "--stock-wifi-key")) {
        unsigned char key[STOCK_WIFI_KEY_LEN + STOCK_WIFI_IV_LEN];
        if (stock_wifi_key_from(argv[2], key) != 0) {
            printf("(none)\n");
            return 0;
        }
        printf("%.*s %.*s\n", STOCK_WIFI_KEY_LEN, (const char *)key,
               STOCK_WIFI_IV_LEN, (const char *)key + STOCK_WIFI_KEY_LEN);
        return 0;
    }
#if defined(SOUNDTAP_INSTALL_NAND) && defined(SOUNDTAP_INSTALL_BOOTLOADERS)
    /* `installer --print-env` prints the U-Boot recipes this board would write, one per line,
     * and exits. Same reasoning as --which-module: unreachable during an install, and the only
     * way to check the rendering against os/support/uboot_recipes.py without a speaker. */
    /* `installer --env-image FLAG` reads key=value lines on stdin and writes the copy of the
     * redundant environment an install writes for them, so app/internal/ubootenv can hold the
     * three writers to the same bytes (writers_test.go). */
    if (argc == 3 && !strcmp(argv[1], "--env-image")) {
        static unsigned char entries[REDUND_ENV_SIZE], image[REDUND_ENV_SIZE];
        static char line[REDUND_ENV_SIZE];
        unsigned len = 0;
        while (fgets(line, sizeof line, stdin)) {
            unsigned n = (unsigned)strcspn(line, "\n");
            if (!n) continue;
            if (len + n + 2 > sizeof entries) return 1;
            memcpy(entries + len, line, n);
            len += n;
            entries[len++] = 0;
        }
        entries[len++] = 0;
        if (encode_redundant_env(entries, len, (unsigned char)atoi(argv[2]), image) != 0) return 1;
        return fwrite(image, 1, sizeof image, stdout) == sizeof image ? 0 : 1;
    }
    if (argc == 2 && !strcmp(argv[1], "--print-env")) {
        const char *const *env = mainline_env();
        if (!env) return 1;
        for (unsigned i = 0; i < MAINLINE_ENV_COUNT; i++) printf("%s\n", env[i]);
        return 0;
    }
#endif
    (void)argc; (void)argv;
    signal(SIGPIPE, SIG_IGN);
    swu_log_open("wla-installer");
    swu_log("start pid=%d uid=%d", (int)getpid(), (int)getuid());

    struct swu_link link;
    if (swu_listen_and_accept(&link, LISTEN_PORT) < 0) {
        swu_log("no coordinator link on :%d", LISTEN_PORT);
        return 1;
    }

    /* Stock waits for us: this first status is what unblocks SoftwareUpdateStart. */
    if (swu_status(&link, STATE_IDLE, 0) < 0) return 1;

    struct swu_message message;
    if (swu_receive(&link, &message) < 0) {
        swu_log("coordinator closed before sending a start");
        return 1;
    }
    if (message.message_id != SWU_MSG_START) {
        swu_log("expected SoftwareUpdateStart, got %u (%s)", message.message_id, message.type_name);
        return 1;
    }
    struct swu_start start;
    swu_parse_start(message.contents, message.contents_len, &start);
    swu_log("start url=%s size=%llu crc=%08x", start.url, start.update_size, start.update_crc);

#ifdef SOUNDTAP_TRACE_UDP
    survey_flash();
#endif

    struct install install;
    memset(&install, 0, sizeof install);
#ifdef SOUNDTAP_INSTALL_NAND
    if (!known_variant()) {
        swu_log("unknown hardware variant; refusing before NAND is opened");
        swu_failure(&link);
        return 1;
    }
    if (open_targets(&install) != 0) {
        swu_log("no candidate slot to install into");
        swu_failure(&link);
        return 1;
    }
#endif

    /* No status before the first data request: stock drops the link if one arrives here. */
    struct puller puller;
    puller_init(&puller, &link, start.update_size);
    if (consume_chain(&puller, &install, start.update_size) < 0) {
        swu_log("package rejected; candidate slot left unbootable");
        swu_failure(&link);
        return 1;
    }

#ifdef SOUNDTAP_INSTALL_NAND
    swu_status(&link, STATE_INSTALLING, 90);
    if (finish_transaction(&install) != 0) {
        swu_log("install failed; the running slot is untouched");
        swu_failure(&link);
        return 1;
    }
    mtd_close(&install.kernel.part);
    mtd_close(&install.rootfs.part);
#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
    /* Our U-Boot cannot run Bose's nand_boot (nboot.e). Put our recipes in first, with bootcmd
     * still on the running slot, so every moment of the boot chain phase below still boots: the
     * old chain runs our recipes as well as its own. The slot switch comes last. */
    if (install.spl_len && install.uboot_len &&
        switch_bootcmd(install.kernel.part.index == 6, 1, 0) != 0) {
        swu_log("boot recipes not written; boot chain left alone, running slot remains selected");
        swu_failure(&link);
        return 1;
    }
    if (install_bootloaders(&install) != 0) {
        /* Keep the known-good running slot selected. An interrupted bootloader update must not
         * select an image whose matching boot chain was not fully verified. */
        swu_log("boot chain phase failed; running slot remains selected");
        swu_failure(&link);
        return 1;
    }
#endif
#ifdef SOUNDTAP_INSTALL_BOOTLOADERS
    const int wrote_bootloaders = install.spl_len && install.uboot_len;
#else
    const int wrote_bootloaders = 0;
#endif
    /* Our boot chain is on NAND and verified: it reads the redundant environment. A package
     * without one leaves the board on Bose's U-Boot and Bose's 8 KiB environment. */
    if (switch_bootcmd(install.kernel.part.index != 6, wrote_bootloaders, wrote_bootloaders) != 0) {
        swu_failure(&link);
        return 1;
    }
    carry_stock_wifi();
    /* Armed before the last status is sent, not after: everything from here on must end in a
     * reboot, including a kill arriving while we are still talking to stock. */
    if (!getenv("SOUNDTAP_NO_REBOOT")) {
        reboot_armed = 1;
        warm_reset_after(RESET_FALLBACK_SECONDS);
        signal(SIGTERM, reboot_on_signal);
        signal(SIGHUP, reboot_on_signal);
        signal(SIGINT, reboot_on_signal);
    }
    /* Return value deliberately unused: stock may already have hung up, and a coordinator that
     * is no longer listening is not a reason to leave the speaker on its old slot. */
    swu_status(&link, STATE_COMPLETED, 100);
    if (getenv("SOUNDTAP_NO_REBOOT")) {
        swu_log("install complete; reboot suppressed by SOUNDTAP_NO_REBOOT");
        return 0;
    }
    swu_log("rebooting into the candidate slot");
    sleep(2);
    reboot_now();
    return 1;
#else
    swu_log("verify-only build: %d modules accepted, no partition was written", install.modules);
    swu_status(&link, STATE_COMPLETED, 100);
    swu_report_terminal(&link);
    return 0;
#endif
}
