ENTRY(_start)
SECTIONS {
    . = 0x60008000;
    .text : { *(.text*) *(.rodata*) *(.data*) *(.bss*) *(COMMON) }
    /DISCARD/ : { *(.ARM.exidx*) *(.ARM.extab*) *(.comment*) }
}
