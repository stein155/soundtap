/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Read-only lookup in the stock PersParam log (NOR sector 0x40010000, header "PersParam").
 * Records follow the 0x20-byte sector header back to back until an erased (0xffffffff) length:
 *   u32 rec_len, u32 val_len, u32 0, u32 key_len (4), key (FourCC, see PP_KEY),
 *   value[val_len], u32 trailer
 * The log appends; the last record for a key holds its current value. Stock keeps the Wi-Fi
 * MAC under 'MAC0' (8-byte value: MAC + 2 pad), which matches the stock on-air station address
 * and the MIB MacAddr (hardware 2026-10-06). Pure: the caller passes the mapped sector. */
#ifndef PERSPARAM_H
#define PERSPARAM_H
#include <stdint.h>

#define PERSPARAM_NOR ((const volatile uint8_t *)0x40010000)
#define PERSPARAM_SIZE 0x10000u
/* FourCC as stock writes it: the u32 'MAC0' in a LE word, so the bytes on NOR read "0CAM". */
#define PP_KEY(a, b, c, d) ((uint32_t)(d) | (uint32_t)(c) << 8 | (uint32_t)(b) << 16 | (uint32_t)(a) << 24)

static uint32_t pp_le32(const volatile uint8_t *p)
{
    return p[0] | p[1] << 8 | p[2] << 16 | (uint32_t)p[3] << 24;
}

/* Copy the newest value of key (up to len bytes) into out. Returns the stored value length,
 * 0 if the key is absent or the log is malformed. */
static unsigned pp_get(const volatile uint8_t *sec, unsigned size, uint32_t key, uint8_t *out,
                       unsigned len)
{
    static const char magic[9] = "PersParam";
    const volatile uint8_t *hit = 0;
    unsigned hitlen = 0;
    if (size < 0x20)
        return 0;
    for (unsigned i = 0; i < sizeof magic; i++)
        if (sec[0x10 + i] != (uint8_t)magic[i])
            return 0;
    for (unsigned o = 0x20; o + 16 <= size;) {
        uint32_t rl = pp_le32(sec + o), vl = pp_le32(sec + o + 4), kl = pp_le32(sec + o + 12);
        if (rl == 0xffffffffu)
            break;
        if (rl < 16 || kl != 4 || vl > rl || 16 + kl + vl + 4 > rl || rl > size - o)
            return 0; /* not the layout we know: refuse rather than guess */
        if (pp_le32(sec + o + 16) == key)
            hit = sec + o + 20, hitlen = vl;
        o += (rl + 3) & ~3u;
    }
    for (unsigned i = 0; hit && i < hitlen && i < len; i++)
        out[i] = hit[i];
    return hit ? hitlen : 0;
}

/* The device's Wi-Fi MAC from 'MAC0'. Returns 1 on a usable unicast address. */
static int pp_wifi_mac(const volatile uint8_t *sec, unsigned size, uint8_t mac[6])
{
    uint8_t v[8];
    unsigned any = 0;
    if (pp_get(sec, size, PP_KEY('M', 'A', 'C', '0'), v, sizeof v) < 6)
        return 0;
    for (unsigned i = 0; i < 6; i++)
        mac[i] = v[i], any |= v[i];
    return any && !(mac[0] & 1);
}
#endif
