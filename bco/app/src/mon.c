/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include "mon.h"

void app_main(void)
{
    unsigned ahb = ahb_hz();
    matrix();
    UART[2] = 2 * ahb / BAUD - 2;
    mon_run();
}
