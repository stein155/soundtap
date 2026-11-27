ENTRY(_start)
SECTIONS {
    . = 0x60008000;
    .text : { *(.text*) *(.rodata*) *(.data*) *(.bss*) *(COMMON) }
    /* Uninitialised SDRAM past the image (wifi.c's frames held for dozing clients): not in the
     * image, never zeroed; the stacks and the page table sit far above it (src/stacks.h). */
    .noinit (NOLOAD) : ALIGN(8) { *(.noinit*) }
    /DISCARD/ : { *(.ARM.exidx*) *(.ARM.extab*) *(.comment*) }
}
