// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package envfixture builds the pre-install Wireless Link environment the tests start
// from. The entries are written here, not copied from any vendor dump; the package does
// not import ubootenv so that ubootenv's own tests can use it.
package envfixture

import (
	"encoding/binary"
	"hash/crc32"
	"sort"
	"strings"
)

const size = 0x2000 // ubootenv.Size

// usedBytes is how full a real environment on this board is: 6321 of the 8188 usable
// bytes. The filler entry reproduces the occupancy and nothing else, because the question
// the install tests ask is whether the recipes still fit once they are added.
const usedBytes = 6321

// entries keep the shape of a vendor U-Boot's (nboot.e recipes, one args entry per slot),
// which is what an install has to replace. PING is selected.
var entries = []string{
	"baudrate=115200",
	"bootdelay=1",
	"bootfile=uImage",
	"console=ttyO3,115200n8",
	"loadaddr=0x82000000",
	"kloadaddr=0x80007fc0",
	"kernel0_nand_address=0x280000",
	"kernel1_nand_address=0x780000",
	"bootcmd=run nand_boot;",
	"nand_args=setenv bootargs console=${console} root=ubi0:rootfs rootfstype=ubifs ro ubi.mtd=8,2048 variant=${variant} module_type=${module_type}",
	"nand_args2=setenv bootargs console=${console} root=ubi0:rootfs rootfstype=ubifs ro ubi.mtd=9,2048 variant=${variant} module_type=${module_type}",
	// The same two recipes as flash.stockRecipes (a test there holds them equal): the way
	// back to stock must restore this environment byte for byte.
	"nand_boot=run nand_args; mtdparts default; ubi part NAND.File-System-Ping 2048; ubifsmount ubi0; " +
		"ubifsload ${loadaddr} /boot/${variant}_${module_type}.dtb; fdt addr ${loadaddr}; " +
		"nboot.e ${kloadaddr} 0 ${kernel0_nand_address}; fixsig; bootm ${kloadaddr} - ${loadaddr}",
	"nand_boot2=run nand_args2; mtdparts default; ubi part NAND.File-System-Pong 2048; ubifsmount ubi0; " +
		"ubifsload ${loadaddr} /boot/${variant}_${module_type}.dtb; fdt addr ${loadaddr}; " +
		"nboot.e ${kloadaddr} 0 ${kernel1_nand_address}; fixsig; bootm ${kloadaddr} - ${loadaddr}",
}

// Image is the 8 KiB window mtd5 holds: CRC32, sorted NUL-separated entries, zero padding.
func Image() []byte {
	all := append([]string(nil), entries...)
	used := 1 // the empty entry that ends the list
	for _, e := range all {
		used += len(e) + 1
	}
	const filler = "filler="
	all = append(all, filler+strings.Repeat("x", usedBytes-used-len(filler)-1))
	// U-Boot sorts by key, so "nand_args" precedes "nand_args2" although '2' < '='.
	key := func(e string) string { k, _, _ := strings.Cut(e, "="); return k }
	sort.Slice(all, func(i, j int) bool { return key(all[i]) < key(all[j]) })
	image := make([]byte, size)
	copy(image[4:], strings.Join(all, "\x00")+"\x00\x00")
	binary.LittleEndian.PutUint32(image, crc32.ChecksumIEEE(image[4:]))
	return image
}
