/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

/* Reset and exception entry of our WiLD core image. The core runs from SRAM address 0. */
.syntax unified
.arm
.section .vectors, "ax"
.global _start
_start:
    b reset
    b undef_entry
    b swi_entry
    b pabt_entry
    b dabt_entry
    b .
    b irq_entry
    b fiq_entry

.text
reset:
    /* one stack per mode, interrupts masked; ends in SVC */
    msr cpsr_c, #0xd2       /* IRQ */
    ldr sp, =__irq_stack_top
    msr cpsr_c, #0xd1       /* FIQ */
    ldr sp, =__fiq_stack_top
    msr cpsr_c, #0xd7       /* abort */
    ldr sp, =__abt_stack_top
    msr cpsr_c, #0xdb       /* undefined */
    ldr sp, =__und_stack_top
    msr cpsr_c, #0xd3       /* SVC */
    ldr sp, =__svc_stack_top
    ldr r0, =__bss_start
    ldr r1, =__bss_end
    mov r2, #0
1:  cmp r0, r1
    strlo r2, [r0], #4
    blo 1b
    bl main
2:  b 2b

/* A fault parks the core (the host sees no reply and times out) and leaves its trace in rec. */
undef_entry:
    mov r0, #1
    b fault
swi_entry:
    mov r0, #2
    b fault
pabt_entry:
    mov r0, #3
    b fault
dabt_entry:
    mov r0, #4
fault:                      /* diag: rec[RC_FAULT] = vector, rec[RC_FAULTLR] = lr (hw.h) */
    ldr r1, =rec
    str r0, [r1, #25*4]
    str lr, [r1, #24*4]
    b .

/* FIQ = the MAC receive line (mac.c mac_fiq). */
fiq_entry:
    sub lr, lr, #4
    stmfd sp!, {r0-r3, r12, lr}
    bl mac_fiq
    ldmfd sp!, {r0-r3, r12, pc}^

irq_entry:
    sub lr, lr, #4
    stmfd sp!, {r0-r3, r12, lr}
    bl xport_irq
    ldmfd sp!, {r0-r3, r12, pc}^

/* Nested critical sections. Interrupts come on when the outermost one ends, except in IRQ mode;
 * this is also what first enables them after reset. */
.global irq_off, irq_on, fiq_on
fiq_on:
    mrs r0, cpsr
    bic r0, r0, #0x40
    msr cpsr_c, r0
    bx lr

irq_off:
    mrs r0, cpsr
    orr r0, r0, #0x80
    msr cpsr_c, r0
    ldr r2, =irq_depth
    ldrb r1, [r2]
    add r1, r1, #1
    strb r1, [r2]
    bx lr
irq_on:
    ldr r2, =irq_depth
    ldrb r1, [r2]
    subs r1, r1, #1
    strb r1, [r2]
    bxne lr
    mrs r0, cpsr
    and r1, r0, #0x1f
    cmp r1, #0x12
    bxeq lr
    bic r0, r0, #0x80
    msr cpsr_c, r0
    bx lr

.bss
irq_depth:
    .byte 0
