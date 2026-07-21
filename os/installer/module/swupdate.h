/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Stock updater IPC, the part of it that the WLA install path actually uses.
 *
 * Framing is 4-byte big-endian length followed by an IPCMessageEnvelopeBase: type=1,
 * module_id=2, message_id=3, contents=6, sequence=7, type_name=8. Recovered in a 2026-09-05
 * audit of the stock updater and confirmed on the board 2026-09-13.
 */
#ifndef SOUNDTAP_SWUPDATE_H
#define SOUNDTAP_SWUPDATE_H

#define SWU_EVENT 1
#define SWU_EVENT_REQUEST 2
#define SWU_EVENT_RESPONSE 3

#define SWU_MSG_START 0
#define SWU_MSG_STATUS 1
#define SWU_MSG_FAILURE 2
#define SWU_MSG_ABORT 3
#define SWU_MSG_VALIDATE 4
#define SWU_MSG_DATA 100

#define SWU_MAX_ENVELOPE 1000000

struct swu_link {
    int listener;
    int fd;
};

struct swu_message {
    unsigned type;
    unsigned message_id;
    unsigned sequence;
    const unsigned char *contents;
    int contents_len;
    char type_name[64];
};

struct swu_start {
    char url[256];
    unsigned update_crc;
    unsigned long long update_size;
};

void swu_log_open(const char *who);
void swu_log(const char *fmt, ...);

unsigned swu_crc32(unsigned crc, const unsigned char *data, int len);

int swu_put_varint(unsigned char *out, unsigned long long value);
int swu_put_field_varint(unsigned char *out, int field, unsigned long long value);

int swu_listen_and_accept(struct swu_link *link, int port);
int swu_send(struct swu_link *link, unsigned type, unsigned message_id, int has_sequence,
             unsigned sequence, const unsigned char *contents, int contents_len,
             const char *type_name);
int swu_receive(struct swu_link *link, struct swu_message *message);

void swu_parse_start(const unsigned char *contents, int len, struct swu_start *out);
void swu_parse_data_response(const unsigned char *contents, int len, unsigned long long *count,
                             const unsigned char **data, int *data_len, unsigned long long *eos);

int swu_status(struct swu_link *link, unsigned state, unsigned progress);
int swu_failure(struct swu_link *link);
void swu_report_terminal(struct swu_link *link);

#endif
