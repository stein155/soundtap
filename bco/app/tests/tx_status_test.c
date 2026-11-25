/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Offline stage-3 -> stage-4 -> stage-0 ownership check. No radio or frame bytes. */
#include <assert.h>
#include "../src/wring.h"

int main(void)
{
    unsigned records[4][6] = {{0}};
    struct wring local, core;
    unsigned ok = 0, failed = 0;
    wr_init(&core, 0, (uintptr_t)records, 4, 24, 5);
    wr_init(&local, 0, (uintptr_t)records, 4, 24, 5);
    wr_link(&local, &core, 1, 3, 2);
    /* Two reports cross ring wrap; stage 3 makes them available to stage 4. */
    local.ch[3].pos = local.ch[4].pos = local.ch[4].lim = 3;
    records[3][3] = 0x12340000; /* high bits are not status */
    records[0][3] = 7; /* cppcheck-suppress unreadVariable ; wr_tx_status reads it through the ring */
    wr_commit(&local, 3, 2);
    wr_tx_status(&local, &ok, &failed);
    assert(ok == 1 && failed == 1);
    assert(local.ch[4].pos == 1 && local.ch[0].lim == 2);
    wr_tx_status(&local, &ok, &failed);
    assert(ok == 1 && failed == 1); /* reports consumed exactly once */
    return 0;
}
