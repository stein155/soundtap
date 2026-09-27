/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

.syntax unified
#include "stacks.h"
.arm
.global _start
_start:
    /* Mirror what the stock stage 2 does at entry (0x60008000): IRQ/FIQ off, SVC, MMU off, sp. */
    msr cpsr_c, #0xd3
    mrc p15, 0, r0, c1, c0, 0
    bic r0, r0, #1
    mcr p15, 0, r0, c1, c0, 0
    /* I-cache on (works with the MMU off; nothing here runs code it wrote): without it every
     * instruction is an SDRAM fetch and software CCMP alone costs tens of ms per frame. */
    mov r1, #0
    mcr p15, 0, r1, c7, c5, 0
    orr r0, r0, #0x1000
    mcr p15, 0, r0, c1, c0, 0
#ifdef VECTEST
    msr cpsr_c, #0xdb       /* undef mode stack */
    ldr sp, =STACK_UND
    msr cpsr_c, #0xd3
#endif
#if defined(TICK) || defined(USB_IRQ)
    msr cpsr_c, #0xd2       /* IRQ mode stack, well below the SVC stack */
    ldr sp, =STACK_IRQ
#endif
#ifdef TICK
    msr cpsr_c, #0xd1       /* FIQ mode stack */
    ldr sp, =STACK_FIQ
    msr cpsr_c, #0xd3
#endif
    ldr sp, =STACK_SVC
    bl app_main
1:  b 1b

#ifdef NETCARD_HW
/* Exceptions (low_vectors, board.h): report and park instead of running BFL's handlers,
 * whose SRAM the link app reuses for WiLD buffers. */
.global fault_undef, fault_swi, fault_pabort, fault_dabort
fault_undef:  mov r0, #1
              b 9f
fault_swi:    mov r0, #2
              b 9f
fault_pabort: mov r0, #3
              b 9f
fault_dabort: mov r0, #4
9:            ldr sp, =STACK_ABT
              mov r1, lr
              mrs r2, spsr
              bl app_fault
8:            b 8b
#endif

#if defined(TICK) || defined(USB_IRQ)
.global irq_entry
irq_entry:
    sub lr, lr, #4
    push {r0-r3, r12, lr}
    bl irq_dispatch
    ldmfd sp!, {r0-r3, r12, pc}^
#endif

#ifdef TICK
.global fiq_entry
fiq_entry:
    sub lr, lr, #4
    push {r0-r3, r12, lr}
    bl fiq_dispatch
    cmp r0, #0
    mrsne r1, spsr          /* storm guard: return with FIQ masked */
    orrne r1, r1, #0x40
    msrne spsr_c, r1
    ldmfd sp!, {r0-r3, r12, pc}^
#endif

#ifdef VECTEST
.global vt_undef, vt_swi, vt_pabort, vt_dabort
vt_undef:  push {r0-r3, r12, lr}
           mov r0, #1
           b 5f
vt_swi:    push {r0-r3, r12, lr}
           mov r0, #2
5:         ldr r1, [sp, #20]
           bl vt_hit
           ldmfd sp!, {r0-r3, r12, pc}^
vt_pabort: mov r0, #3
           b 6f
vt_dabort: mov r0, #4
6:         mov r1, lr
           bl vt_hit
7:         b 7b
#endif
