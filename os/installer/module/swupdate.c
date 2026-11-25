/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#include "swupdate.h"

/* ---- logging -------------------------------------------------------------
 *
 * The board has no shell route during an update, so a trace build also sends every line as a
 * UDP datagram to the USB host. Release builds log to /tmp only.
 */
static const char *log_who = "wla-installer";
static int log_file = -1;
#ifdef SOUNDTAP_TRACE_UDP
static int trace_fd = -1;
static struct sockaddr_in trace_hosts[2];
#endif

void swu_log_open(const char *who)
{
    log_who = who;
    char path[128];
    snprintf(path, sizeof path, "/tmp/soundtap-%s.log", who);
    log_file = open(path, O_CREAT | O_WRONLY | O_APPEND, 0644);
#ifdef SOUNDTAP_TRACE_UDP
    trace_fd = socket(AF_INET, SOCK_DGRAM, 0);
    const char *addrs[2] = {"203.0.113.2", "192.168.7.1"};
    for (int i = 0; i < 2; i++) {
        memset(&trace_hosts[i], 0, sizeof trace_hosts[i]);
        trace_hosts[i].sin_family = AF_INET;
        trace_hosts[i].sin_port = htons(40099);
        trace_hosts[i].sin_addr.s_addr = inet_addr(addrs[i]);
    }
#endif
}

void swu_log(const char *fmt, ...)
{
    char line[1400];
    int n = snprintf(line, sizeof line, "%s: ", log_who);
    va_list ap;
    va_start(ap, fmt);
    int m = vsnprintf(line + n, sizeof line - n - 1, fmt, ap);
    va_end(ap);
    if (m < 0) return;
    n += m;
    if (n > (int)sizeof line - 1) n = sizeof line - 1;
    line[n] = '\n';
    if (log_file >= 0) (void)!write(log_file, line, n + 1);
    (void)!write(2, line, n + 1);
#ifdef SOUNDTAP_TRACE_UDP
    for (int i = 0; i < 2 && trace_fd >= 0; i++)
        sendto(trace_fd, line, n + 1, 0, (struct sockaddr *)&trace_hosts[i], sizeof trace_hosts[i]);
#endif
}

/* ---- CRC32, zlib polynomial: the STU's own checksum ---- */

static unsigned crc_table[256];
static int crc_ready;

unsigned swu_crc32(unsigned crc, const unsigned char *data, int len)
{
    if (!crc_ready) {
        for (unsigned i = 0; i < 256; i++) {
            unsigned c = i;
            for (int k = 0; k < 8; k++) c = (c & 1) ? 0xedb88320u ^ (c >> 1) : c >> 1;
            crc_table[i] = c;
        }
        crc_ready = 1;
    }
    crc ^= 0xffffffffu;
    for (int i = 0; i < len; i++) crc = crc_table[(crc ^ data[i]) & 0xff] ^ (crc >> 8);
    return crc ^ 0xffffffffu;
}

/* ---- protobuf ---- */

int swu_put_varint(unsigned char *out, unsigned long long value)
{
    int n = 0;
    while (value >= 0x80) { out[n++] = (value & 0x7f) | 0x80; value >>= 7; }
    out[n++] = (unsigned char)value;
    return n;
}

int swu_put_field_varint(unsigned char *out, int field, unsigned long long value)
{
    int n = swu_put_varint(out, (unsigned long long)field << 3);
    return n + swu_put_varint(out + n, value);
}

static int put_field_bytes(unsigned char *out, int field, const unsigned char *value, int len)
{
    int n = swu_put_varint(out, ((unsigned long long)field << 3) | 2);
    n += swu_put_varint(out + n, len);
    memcpy(out + n, value, len);
    return n + len;
}

static int get_varint(const unsigned char *b, int len, int *at, unsigned long long *value)
{
    *value = 0;
    for (int shift = 0; shift < 64 && *at < len; shift += 7) {
        unsigned char c = b[(*at)++];
        *value |= (unsigned long long)(c & 0x7f) << shift;
        if (!(c & 0x80)) return 1;
    }
    return 0;
}

/* Whether a length-delimited field of `size` bytes fits in what is left after `at`. Compared
 * unsigned and before any cast: (int)size turned 0xffffffff into -1, which passed the old
 * `at + (int)size > len` test and reached memcpy as a huge length. */
static int fits(unsigned long long size, int at, int len)
{
    return at >= 0 && at <= len && size <= (unsigned long long)(len - at);
}

/* ---- link ---- */

static int write_all(int fd, const unsigned char *b, int n)
{
    int done = 0;
    while (done < n) {
        int w = write(fd, b + done, n - done);
        if (w <= 0) return -1;
        done += w;
    }
    return 0;
}

static int read_all(int fd, unsigned char *b, int n)
{
    int done = 0;
    while (done < n) {
        int r = read(fd, b + done, n - done);
        if (r <= 0) return -1;
        done += r;
    }
    return 0;
}

int swu_listen_and_accept(struct swu_link *link, int port)
{
    memset(link, 0, sizeof *link);
    link->listener = link->fd = -1;
    int srv = socket(AF_INET, SOCK_STREAM, 0);
    if (srv < 0) return -1;
    int one = 1;
    setsockopt(srv, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
    struct sockaddr_in addr;
    memset(&addr, 0, sizeof addr);
    addr.sin_family = AF_INET;
    addr.sin_port = htons(port);
    addr.sin_addr.s_addr = htonl(INADDR_ANY);
    if (bind(srv, (struct sockaddr *)&addr, sizeof addr) || listen(srv, 5)) {
        swu_log("bind :%d failed errno=%d", port, errno);
        close(srv);
        return -1;
    }
    link->listener = srv;
    swu_log("listening on 0.0.0.0:%d", port);

    struct timeval timeout = {120, 0};
    setsockopt(srv, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof timeout);
    struct sockaddr_in peer;
    socklen_t peerlen = sizeof peer;
    int fd = accept(srv, (struct sockaddr *)&peer, &peerlen);
    if (fd < 0) {
        swu_log("accept failed errno=%d", errno);
        return -1;
    }
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, sizeof timeout);
    setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, sizeof timeout);
    link->fd = fd;
    swu_log("coordinator connected from %s:%d", inet_ntoa(peer.sin_addr), ntohs(peer.sin_port));
    return 0;
}

int swu_send(const struct swu_link *link, unsigned type, unsigned message_id, int has_sequence,
             unsigned sequence, const unsigned char *contents, int contents_len,
             const char *type_name)
{
    unsigned char body[1024], frame[4];
    int n = 0;
    n += swu_put_field_varint(body + n, 1, type);
    n += swu_put_field_varint(body + n, 2, 0);
    n += swu_put_field_varint(body + n, 3, message_id);
    n += put_field_bytes(body + n, 6, contents, contents_len);
    if (has_sequence) n += swu_put_field_varint(body + n, 7, sequence);
    n += put_field_bytes(body + n, 8, (const unsigned char *)type_name, (int)strlen(type_name));
    frame[0] = (n >> 24) & 0xff; frame[1] = (n >> 16) & 0xff;
    frame[2] = (n >> 8) & 0xff; frame[3] = n & 0xff;
    if (write_all(link->fd, frame, 4) || write_all(link->fd, body, n)) {
        swu_log("sending %s failed errno=%d", type_name, errno);
        return -1;
    }
    return 0;
}

static unsigned char receive_buffer[SWU_MAX_ENVELOPE];

int swu_receive(const struct swu_link *link, struct swu_message *message)
{
    unsigned char head[4];
    if (read_all(link->fd, head, 4) < 0) return -1;
    unsigned len = ((unsigned)head[0] << 24) | ((unsigned)head[1] << 16) |
                   ((unsigned)head[2] << 8) | head[3];
    if (len > SWU_MAX_ENVELOPE) {
        swu_log("envelope length %u refused", len);
        return -1;
    }
    if (read_all(link->fd, receive_buffer, len) < 0) return -1;

    memset(message, 0, sizeof *message);
    int at = 0;
    while (at < (int)len) {
        unsigned long long key;
        if (!get_varint(receive_buffer, len, &at, &key)) break;
        int field = key >> 3, wire = key & 7;
        if (wire == 0) {
            unsigned long long value;
            if (!get_varint(receive_buffer, len, &at, &value)) break;
            if (field == 1) message->type = value;
            else if (field == 3) message->message_id = value;
            else if (field == 7) message->sequence = value;
        } else if (wire == 2) {
            unsigned long long size;
            if (!get_varint(receive_buffer, len, &at, &size) || !fits(size, at, (int)len)) break;
            if (field == 6) {
                message->contents = receive_buffer + at;
                message->contents_len = (int)size;
            } else if (field == 8) {
                int copy = (int)size < (int)sizeof message->type_name - 1
                           ? (int)size : (int)sizeof message->type_name - 1;
                memcpy(message->type_name, receive_buffer + at, copy);
                message->type_name[copy] = 0;
            }
            at += (int)size;
        } else break;
    }
    return (int)len;
}

void swu_parse_start(const unsigned char *contents, int len, struct swu_start *out)
{
    memset(out, 0, sizeof *out);
    int at = 0;
    while (at < len) {
        unsigned long long key;
        if (!get_varint(contents, len, &at, &key)) break;
        int field = key >> 3, wire = key & 7;
        if (wire == 0) {
            unsigned long long value;
            if (!get_varint(contents, len, &at, &value)) break;
            if (field == 2) out->update_crc = (unsigned)value;
            else if (field == 3) out->update_size = value;
        } else if (wire == 2) {
            unsigned long long size;
            if (!get_varint(contents, len, &at, &size) || !fits(size, at, len)) break;
            if (field == 1) {
                int copy = (int)size < (int)sizeof out->url - 1 ? (int)size : (int)sizeof out->url - 1;
                memcpy(out->url, contents + at, copy);
                out->url[copy] = 0;
            }
            at += (int)size;
        } else break;
    }
}

void swu_parse_data_response(const unsigned char *contents, int len, unsigned long long *count,
                             const unsigned char **data, int *data_len, unsigned long long *eos)
{
    *count = *eos = 0;
    *data = NULL;
    *data_len = 0;
    int at = 0;
    while (at < len) {
        unsigned long long key;
        if (!get_varint(contents, len, &at, &key)) break;
        int field = key >> 3, wire = key & 7;
        if (wire == 0) {
            unsigned long long value;
            if (!get_varint(contents, len, &at, &value)) break;
            if (field == 1) *count = value;
            else if (field == 3) *eos = value;
        } else if (wire == 2) {
            unsigned long long size;
            if (!get_varint(contents, len, &at, &size) || !fits(size, at, len)) break;
            if (field == 2) { *data = contents + at; *data_len = (int)size; }
            at += (int)size;
        } else break;
    }
}

int swu_status(const struct swu_link *link, unsigned state, unsigned progress)
{
    unsigned char contents[16];
    int n = swu_put_field_varint(contents, 1, state);
    n += swu_put_field_varint(contents + n, 2, progress);
    n += swu_put_field_varint(contents + n, 3, 1);      /* abort_available */
    return swu_send(link, SWU_EVENT, SWU_MSG_STATUS, 0, 0, contents, n, "SoftwareUpdateStatus");
}

int swu_failure(const struct swu_link *link)
{
    unsigned char contents[8];
    int n = swu_put_field_varint(contents, 1, 8);       /* software_failure */
    return swu_send(link, SWU_EVENT, SWU_MSG_FAILURE, 0, 0, contents, n, "SoftwareUpdateFailure");
}

/* How the installer declares success is still unknown: status state=completed leaves stock in
 * INSTALLING. The vendor installer's string table has SoftwareUpdateSuccess and
 * SoftwareUpdateExit, so a trace build walks those candidates and records what stock does.
 * Release builds must not guess: they only report status and let the caller decide.
 */
void swu_report_terminal(const struct swu_link *link)
{
#ifdef SOUNDTAP_TRACE_UDP
    struct { unsigned id; const char *name; } candidates[] = {
        {5, "SoftwareUpdateSuccess"},
        {6, "SoftwareUpdateExit"},
        {7, "SoftwareUpdateSuccess"},
    };
    for (unsigned i = 0; i < sizeof candidates / sizeof candidates[0]; i++) {
        unsigned char contents[8];
        int n = swu_put_field_varint(contents, 1, 1);
        if (swu_send(link, SWU_EVENT, candidates[i].id, 0, 0, contents, n, candidates[i].name) < 0) {
            swu_log("terminal candidate %u (%s) could not be sent", candidates[i].id, candidates[i].name);
            return;
        }
        swu_log("sent terminal candidate id=%u name=%s", candidates[i].id, candidates[i].name);
        sleep(10);
    }
    swu_log("terminal candidates exhausted; listening for the coordinator");
    struct swu_message message;
    while (swu_receive(link, &message) >= 0)
        swu_log("coordinator said type=%u msg=%u seq=%u name=%s len=%d", message.type,
                message.message_id, message.sequence, message.type_name, message.contents_len);
    swu_log("coordinator link closed");
#else
    (void)link;
#endif
}
