ENTRY(_start)
SECTIONS {
    . = 0x0040d9e8; /* BFL puts image +0x18 at 0x40da00 */
    .text : { *(.text*) *(.rodata*) }
    /DISCARD/ : { *(.ARM.exidx*) *(.ARM.extab*) *(.comment*) }
    ASSERT(SIZEOF(.text) < 0x1ff0, "first stage exceeds handoff window")
}
