// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package ubootenv reads and rewrites the U-Boot environment this board keeps in
// NAND, so an install can point the bootloader at the slot it just wrote.
//
// On the Wireless Link the environment is the *only* slot selector. That is not
// how the ST10 works, and the difference has bitten this project twice. Bose's
// ARMUpdate picks a slot with the five signature bytes at offset 32 of the uImage
// and never touches the environment; this board's own U-Boot ignores those and
// runs whatever `bootcmd` says:
//
//	bootcmd=run nand_boot;    -> nand_args  -> ubi.mtd=8  -> PING
//	bootcmd=run nand_boot2;   -> nand_args2 -> ubi.mtd=9  -> PONG
//
// Read out of this board's own mtd5, not inferred.
//
// Two formats share the partition. Bose's U-Boot, and ours before the redundant
// environment, keep one copy: a little-endian CRC32 followed by NUL-separated key=value
// entries, zero-padded, CRC over LegacySize-4 bytes at the start of NAND.U-Boot-Env.
// Ours now keeps two copies of Size bytes each (CONFIG_ENV_REDUNDANT, ADR 0010): the
// primary at the start of NAND.U-Boot-Env and the redundant one in the last erase block
// of NAND.U-Boot, at RedundantOffset. Each copy is the CRC32, a flag byte, then the
// entries; the CRC covers only the entries, and the copy whose flag is newer is the one
// U-Boot boots. Getting either window wrong is not destructive but it is silent: U-Boot
// reports a bad CRC, falls back to its built-in default and boots as though nothing had
// been asked. That cost the first install its slot switch.
package ubootenv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"slices"
	"strings"
)

const (
	// Size is CONFIG_ENV_SIZE of our U-Boot: one copy of the redundant environment, 32 KiB
	// at the start of a 128 KiB erase block. It holds four times what the 8 KiB single
	// copy did, which an installed environment had filled to within 120 bytes.
	Size = 0x8000
	// LegacySize is Bose's CONFIG_ENV_SIZE, and ours before the redundant environment: one
	// 8 KiB copy at the start of NAND.U-Boot-Env. The way back to stock writes it, and our
	// U-Boot upgrades from it (CONFIG_ENV_REDUNDANT_UPGRADE).
	LegacySize = 0x2000
	// RedundantOffset is where in NAND.U-Boot the redundant copy lives: its last erase
	// block, flash 0x240000 (CONFIG_ENV_OFFSET_REDUND). The spare U-Boot ends before it.
	RedundantOffset = 0x1c0000
	// crcSize is the little-endian CRC32 every copy starts with.
	crcSize = 4
	// headerSize is the CRC and the flag byte in front of a redundant copy's entries.
	headerSize = 5
)

// ErrBadCRC means the window did not check out. Rewriting an environment that is
// already corrupt would turn a bootloader that still falls back to its built-in
// default into one that runs whatever we invented on top of the damage.
var ErrBadCRC = errors.New("u-boot environment CRC does not match")

// The other ways a window or a text form is refused; each message reads as before, with the
// sentinel in place of its fixed part.
var (
	errNeedAtLeast  = errors.New("need at least")
	errNotKeyValue  = errors.New("is not key=value")
	errEmpty        = errors.New("environment is empty")
	errLargerThan   = errors.New("larger than the")
	errCannotCarry  = errors.New("fw_setenv cannot carry")
	errCannotDelete = errors.New("fw_setenv cannot delete")
)

// Env is a parsed environment. Entry order is preserved, because a rewritten
// environment that reorders entries is needlessly hard to diff against a dump.
type Env struct {
	order []string
	vals  map[string]string
}

// NewEnv returns an empty environment, for building one up entry by entry.
func NewEnv() *Env { return &Env{order: nil, vals: map[string]string{}} }

// ParseLegacy reads the single 8 KiB copy at the start of a NAND.U-Boot-Env dump:
// Bose's environment, and ours before the redundant one.
func ParseLegacy(window []byte) (*Env, error) {
	if len(window) < LegacySize {
		return nil, fmt.Errorf("environment is %d bytes, %w %d", len(window), errNeedAtLeast, LegacySize)
	}

	return parseBody(window[0:crcSize], window[crcSize:LegacySize])
}

// ParseCopy reads one copy of the redundant environment, Size bytes, and returns its flag.
func ParseCopy(window []byte) (*Env, byte, error) {
	if len(window) < Size {
		return nil, 0, fmt.Errorf("environment copy is %d bytes, %w %d", len(window), errNeedAtLeast, Size)
	}

	env, err := parseBody(window[0:crcSize], window[headerSize:Size])
	if err != nil {
		return nil, 0, err
	}

	return env, window[crcSize], nil
}

// Copies of the redundant environment, as Current names them.
const (
	Primary   = 0 // NAND.U-Boot-Env
	Redundant = 1 // NAND.U-Boot at RedundantOffset
)

// Current is the copy U-Boot boots from (env_check_redund): the one whose CRC checks out,
// and of two that do, the one with the newer flag. It returns that copy's environment,
// which copy it is and its flag; an error wrapping ErrBadCRC when neither checks out.
//
//nolint:revive // function-result-limit: flash and install call this exported signature.
func Current(primary, redundant []byte) (*Env, int, byte, error) {
	env0, flag0, err0 := ParseCopy(primary)
	env1, flag1, err1 := ParseCopy(redundant)

	// A copy that does not check out is skipped, not reported, while the other one does.
	valid0, valid1 := err0 == nil, err1 == nil
	switch {
	case !valid0 && !valid1:
		return nil, 0, 0, fmt.Errorf("neither copy: %w; %w", err0, err1)
	case !valid1:
		return env0, Primary, flag0, nil
	case !valid0:
		return env1, Redundant, flag1, nil
	case Newer(flag1, flag0):
		return env1, Redundant, flag1, nil
	}

	return env0, Primary, flag0, nil
}

// Newer reports whether flag was written after flag than. Every writer counts up, and U-Boot
// and libubootenv read 0 after 255 as the wrap. Equal flags are not newer, so U-Boot keeps
// the primary; libubootenv would take the other one, which is why no writer leaves two
// valid copies with equal flags.
func Newer(flag, than byte) bool {
	switch {
	case flag == 0 && than == math.MaxUint8:
		return true
	case flag == math.MaxUint8 && than == 0:
		return false
	}

	return flag > than
}

func parseBody(crc, body []byte) (*Env, error) {
	stored := binary.LittleEndian.Uint32(crc)
	if got := crc32.ChecksumIEEE(body); got != stored {
		return nil, fmt.Errorf("%w: stored %08x, computed %08x", ErrBadCRC, stored, got)
	}

	env := NewEnv()

	for entry := range bytes.SplitSeq(body, []byte{0}) {
		if len(entry) == 0 || entry[0] == 0xff {
			break
		}

		key, value, ok := strings.Cut(string(entry), "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("environment entry %q %w", entry, errNotKeyValue)
		}

		if _, seen := env.vals[key]; !seen {
			env.order = append(env.order, key)
		}

		env.vals[key] = value
	}

	if len(env.order) == 0 {
		return nil, errEmpty
	}

	return env, nil
}

// Get returns a value and whether the key was present.
func (e *Env) Get(key string) (string, bool) {
	v, ok := e.vals[key]

	return v, ok
}

// Set adds or replaces a key, keeping the position of one that already exists.
func (e *Env) Set(key, value string) {
	if _, seen := e.vals[key]; !seen {
		e.order = append(e.order, key)
	}

	e.vals[key] = value
}

// Delete removes a key; deleting one that is not there is a no-op.
func (e *Env) Delete(key string) {
	if _, seen := e.vals[key]; !seen {
		return
	}

	delete(e.vals, key)

	for i, k := range e.order {
		if k == key {
			e.order = append(e.order[:i], e.order[i+1:]...)

			break
		}
	}
}

// Keys returns the entry keys in the order they will be encoded.
func (e *Env) Keys() []string { return append([]string(nil), e.order...) }

// EncodeLegacy renders the single 8 KiB copy Bose's U-Boot reads, with a fresh CRC.
func (e *Env) EncodeLegacy() ([]byte, error) {
	body, err := e.body(LegacySize - crcSize)
	if err != nil {
		return nil, err
	}

	return append(le32(crc32.ChecksumIEEE(body)), body...), nil
}

// EncodeCopy renders one copy of the redundant environment with the given flag.
func (e *Env) EncodeCopy(flag byte) ([]byte, error) {
	body, err := e.body(Size - headerSize)
	if err != nil {
		return nil, err
	}

	return append(append(le32(crc32.ChecksumIEEE(body)), flag), body...), nil
}

// The flags of a redundant environment written whole because no copy checked out before
// (a first install, a conversion from the single copy, a rebuild): the redundant copy goes
// first with 1, the primary after it with 2. Until the primary is rewritten the old one is
// not a valid copy, so the redundant copy boots from the moment it is whole, and once both
// are the primary is the newer.
const (
	FreshRedundantFlag = 1
	FreshPrimaryFlag   = 2
)

func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// The text forms below are what libubootenv's fw_printenv prints and what its
// fw_setenv -s reads, which is how soundtapd reads and writes the environment on a
// speaker. The image codecs above are for what libubootenv does not do: rebuilding a torn
// environment, converting between the two formats, and the tests.

// scriptLine is LINE_LENGTH in libubootenv: fw_setenv -s reads a script line into a
// buffer this size and splits a longer one silently.
const scriptLine = 2048

// ParseText reads fw_printenv output: one key=value per line. fw_printenv prints a value
// that holds a newline over several lines, so a line that does not start with a key
// continues the value before it. Our U-Boot leaves such values: it sets board_name,
// board_rev and board_serial from a TI EEPROM these boards do not have, out of SRAM that
// nothing initialised, and saves them with the boot count (ADR 0010).
func ParseText(b []byte) (*Env, error) {
	env := NewEnv()
	last := ""
	// Only the newline that ends the last entry: one before it belongs to a value.
	for line := range strings.SplitSeq(strings.TrimSuffix(string(b), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || !isKey(key) {
			if last == "" {
				return nil, fmt.Errorf("fw_printenv line %q %w", line, errNotKeyValue)
			}

			env.Set(last, env.vals[last]+"\n"+line)

			continue
		}

		env.Set(key, value)
		last = key
	}

	if len(env.order) == 0 {
		return nil, errEmpty
	}

	return env, nil
}

// isKey reports whether name can be an entry's name as fw_printenv prints it: printable
// ASCII without a space or '='.
func isKey(name string) bool {
	if name == "" {
		return false
	}

	for i := range len(name) {
		if name[i] <= ' ' || name[i] > '~' {
			return false
		}
	}

	return true
}

// Text renders env the way fw_printenv prints it.
func (e *Env) Text() []byte {
	var b bytes.Buffer
	for _, key := range e.order {
		fmt.Fprintf(&b, "%s=%s\n", key, e.vals[key])
	}

	return b.Bytes()
}

// Clone returns an independent copy.
func (e *Env) Clone() *Env {
	c := NewEnv()
	for _, key := range e.order {
		c.Set(key, e.vals[key])
	}

	return c
}

// Equal reports whether both hold the same entries, in any order.
func (e *Env) Equal(other *Env) bool {
	if len(e.vals) != len(other.vals) {
		return false
	}

	for key, value := range e.vals {
		if v, ok := other.vals[key]; !ok || v != value {
			return false
		}
	}

	return true
}

// Script is the fw_setenv -s input that turns from into target: key=value for every entry
// that is new or changed, key= for every entry that is gone. Empty when nothing changed.
// A value fw_setenv cannot carry (empty, multi-line, or longer than its line buffer)
// is refused rather than written wrong.
func Script(from, target *Env) ([]byte, error) {
	var script bytes.Buffer

	for _, key := range target.Keys() {
		value := target.vals[key]
		if old, ok := from.vals[key]; ok && old == value {
			continue
		}
		// A line that starts with # is a comment to fw_setenv -s: such a key would be skipped.
		if !scriptCarries(key, value) {
			return nil, fmt.Errorf("%w %s=%q", errCannotCarry, key, value)
		}

		fmt.Fprintf(&script, "%s=%s\n", key, value)
	}

	for _, key := range from.Keys() {
		if _, ok := target.vals[key]; !ok {
			if strings.HasPrefix(key, "#") || strings.ContainsAny(key, "\n\r") {
				return nil, fmt.Errorf("%w %q", errCannotDelete, key)
			}

			fmt.Fprintf(&script, "%s=\n", key)
		}
	}

	return script.Bytes(), nil
}

// scriptCarries reports whether fw_setenv -s can set key to value: not empty, not a
// comment, one line, and within its line buffer with the '=' and the newline.
func scriptCarries(key, value string) bool {
	switch {
	case value == "", strings.HasPrefix(key, "#"), strings.ContainsAny(key+value, "\n\r"):
		return false
	}

	return len(key)+len(value)+2 < scriptLine
}

// Apply runs a fw_setenv -s script against env, as libubootenv does: key= deletes.
func (e *Env) Apply(script []byte) {
	for line := range strings.SplitSeq(string(script), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if value == "" {
			e.Delete(key)
		} else {
			e.Set(key, value)
		}
	}
}

// body renders the entries the way U-Boot's env_export does: sorted by key,
// NUL-separated, an empty entry at the end, zero padding to size bytes. U-Boot sorts when
// it saves, so an environment written here is byte-identical to one U-Boot wrote.
func (e *Env) body(size int) ([]byte, error) {
	keys := e.Keys()
	slices.Sort(keys)

	var body bytes.Buffer
	for _, key := range keys {
		body.WriteString(key)
		body.WriteByte('=')
		body.WriteString(e.vals[key])
		body.WriteByte(0)
	}

	body.WriteByte(0) // the empty entry that ends the list

	if body.Len() > size {
		return nil, fmt.Errorf("environment is %d bytes, %w %d byte window", body.Len(), errLargerThan, size)
	}
	// Zero padding, not 0xff: U-Boot zeroes its buffer before saving, and a dump of this
	// board's own partition is zero-filled from the terminating empty entry to the end of
	// the window. (The rest of the 128 KiB erase block stays erased 0xff.)
	out := make([]byte, size)
	copy(out, body.Bytes())

	return out, nil
}
