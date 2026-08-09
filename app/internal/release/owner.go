// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// OwnerKeyEnv is the U-Boot environment variable that holds the owner key: an ed25519
// public key the speaker's owner chose, trusted next to the release keys (ADR 0048, "The
// owner's own key"). Its value is the key as PublicKey spells one: padded standard base64
// of the 32 raw bytes, 44 characters. It lives in the environment rather than on /mnt/nv
// so U-Boot's USB-stick recovery reads it without UBI; soundtapd reads it with fw_printenv
// and writes it only through the hold on the speaker.
const OwnerKeyEnv = "soundtap_owner_key"

// ownerKeyLen is the base64 length of a 32-byte key: 43 characters and one '='.
const ownerKeyLen = 44

var errOwnerKey = errors.New("not an ed25519 public key: give the 32 raw bytes in standard base64, 44 characters")

// ParseOwnerKey reads an owner key as the owner types it: the base64 of the 32 raw bytes,
// surrounding space ignored. It returns the key in the one spelling the environment holds.
func ParseOwnerKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if _, err := ownerKey(s); err != nil {
		return "", err
	}
	return s, nil
}

var errWeakKey = errors.New("a point of small order, not a public key: signatures can be forged against it")

// ownerKey decodes the environment's value exactly as U-Boot does (st_owner_key in
// os/bootloader/uboot/soundtap-sig.c): 44 characters of padded standard base64 whose
// unused bits are zero, nothing around them, and not a point of small order.
func ownerKey(s string) (ed25519.PublicKey, error) {
	if len(s) != ownerKeyLen {
		return nil, errOwnerKey
	}
	pub, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errOwnerKey
	}
	if smallOrder(pub) {
		return nil, errWeakKey
	}
	return ed25519.PublicKey(pub), nil
}

// smallOrderPoints are the encodings of the eight points of order 1, 2, 4 and 8 on
// edwards25519, the sign bit left out, with the two non-canonical ones crypto/ed25519 also
// decodes (y = p and p + 1): libsodium's blocklist. Against such a "key" a signature with
// S = 0 verifies for one message in four, without any private key; a release key is not
// one, but an owner key is whatever was typed. owner_test.go derives the list again.
var smallOrderPoints = [][32]byte{
	{},     // y = 0, order 4
	{0x01}, // y = 1, the identity
	{0x26, 0xe8, 0x95, 0x8f, 0xc2, 0xb2, 0x27, 0xb0, 0x45, 0xc3, 0xf4, 0x89, 0xf2, 0xef, 0x98, 0xf0,
		0xd5, 0xdf, 0xac, 0x05, 0xd3, 0xc6, 0x33, 0x39, 0xb1, 0x38, 0x02, 0x88, 0x6d, 0x53, 0xfc, 0x05}, // order 8
	{0xc7, 0x17, 0x6a, 0x70, 0x3d, 0x4d, 0xd8, 0x4f, 0xba, 0x3c, 0x0b, 0x76, 0x0d, 0x10, 0x67, 0x0f,
		0x2a, 0x20, 0x53, 0xfa, 0x2c, 0x39, 0xcc, 0xc6, 0x4e, 0xc7, 0xfd, 0x77, 0x92, 0xac, 0x03, 0x7a}, // order 8
	{0xec, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}, // y = p - 1, order 2
	{0xed, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}, // y = p, read as 0
	{0xee, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}, // y = p + 1, read as 1
}

// smallOrder reports a key that encodes one of smallOrderPoints, whatever its sign bit.
func smallOrder(pub []byte) bool {
	for _, p := range smallOrderPoints {
		if bytes.Equal(pub[:31], p[:31]) && pub[31]&0x7f == p[31] {
			return true
		}
	}
	return false
}

// Fingerprint names a key in the UI: the first 8 bytes of the SHA-256 of its 32 raw bytes,
// in lowercase hex. "" for a value that is not a key.
func Fingerprint(key string) string {
	pub, err := ownerKey(key)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// TrustedKeys are the keys a statement on this speaker may be signed with: keys, the
// release keys, and the owner key from the environment when it is one. A value that is
// not a key (garbled, cut short, spelled otherwise) is left out rather than passed on,
// where Verify would fail every statement over it: the release keys keep working.
func TrustedKeys(keys []string, owner string) []string {
	out := append([]string(nil), keys...)
	if _, err := ownerKey(owner); err == nil {
		out = append(out, owner)
	}
	return out
}
