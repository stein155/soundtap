/* Core SRAM: 128 KiB at 0. The host reaches the mailbox at a fixed offset (0x1fde4,
 * MB in bco/app/src/wifi.c) and two flag words just below it, so that tail is reserved. */
ENTRY(_start)
MEMORY {
    sram (rwx) : ORIGIN = 0, LENGTH = 0x1fddc
    mbox (rw)  : ORIGIN = 0x1fddc, LENGTH = 0x20000 - 0x1fddc
}
SECTIONS {
    .text : { KEEP(*(.vectors)) *(.text*) *(.rodata*) } > sram
    .data : { *(.data*) } > sram
    .bss (NOLOAD) : { __bss_start = .; *(.bss*) *(COMMON) . = ALIGN(4); __bss_end = .; } > sram
    .stacks (NOLOAD) : {
        . = ALIGN(8);
        . += 0x100; __fiq_stack_top = .;
        . += 0x100; __abt_stack_top = .;
        . += 0x100; __und_stack_top = .;
        . += 0x800; __irq_stack_top = .;
        . += 0x800; __svc_stack_top = .;
    } > sram
    .mbox (NOLOAD) : { __mbox = .; . += 0x20000 - 0x1fddc; } > mbox
    /DISCARD/ : { *(.ARM.exidx*) *(.ARM.extab*) *(.comment*) }
}
