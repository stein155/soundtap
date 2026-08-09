// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package release

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"maps"
	"math/big"
	"slices"
	"strings"
	"testing"
)

// The owner types the key with whatever space the copy brought; the environment holds the
// one spelling U-Boot reads, and anything that is not 32 bytes of strict base64 is refused.
func TestParseOwnerKey(t *testing.T) {
	key, _ := testKey(t)
	for _, in := range []string{key, " " + key + "\n", "\t" + key + "\r\n"} {
		if got, err := ParseOwnerKey(in); err != nil || got != key {
			t.Errorf("ParseOwnerKey(%q) = %q, %v; want %q", in, got, err, key)
		}
	}
	raw, _ := base64.StdEncoding.DecodeString(key)
	last := strings.IndexByte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/", key[42])
	for name, in := range map[string]string{
		"empty":                "",
		"unpadded":             strings.TrimRight(key, "="),
		"raw URL alphabet":     base64.RawURLEncoding.EncodeToString(raw),
		"31 bytes":             base64.StdEncoding.EncodeToString(raw[:31]),
		"33 bytes":             base64.StdEncoding.EncodeToString(append(raw, 0)),
		"hex":                  hex.EncodeToString(raw),
		"space inside":         key[:20] + " " + key[21:],
		"unused bits not zero": key[:42] + string("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"[last|1]) + "=",
		"a whole .pub line":    "ssh-ed25519 " + key,
	} {
		if in == key {
			continue // the flipped bit was already set
		}
		if _, err := ParseOwnerKey(in); err == nil {
			t.Errorf("%s: %q accepted", name, in)
		}
	}
}

// torsionKeys derives, without trusting smallOrderPoints, every key that encodes a point
// of small order: the eight points P with [8]P the identity, found as [L]Q for points Q on
// edwards25519, each with both sign bits, and the non-canonical y = p and p + 1 that read
// as 0 and 1. In base64, as an owner key is given.
func torsionKeys(t *testing.T) []string {
	t.Helper()
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	mod := func(x *big.Int) *big.Int { return x.Mod(x, p) }
	inv := func(x *big.Int) *big.Int { return new(big.Int).ModInverse(x, p) }
	d := mod(new(big.Int).Mul(big.NewInt(-121665), inv(big.NewInt(121666))))
	l, _ := new(big.Int).SetString("7237005577332262213973186563042994240857116359379907606001950938285454250989", 10)
	type point struct{ x, y *big.Int }
	add := func(a, b point) point {
		dxy := mod(new(big.Int).Mul(d, new(big.Int).Mul(new(big.Int).Mul(a.x, b.x), new(big.Int).Mul(a.y, b.y))))
		x := new(big.Int).Add(new(big.Int).Mul(a.x, b.y), new(big.Int).Mul(b.x, a.y))
		y := new(big.Int).Add(new(big.Int).Mul(a.y, b.y), new(big.Int).Mul(a.x, b.x))
		return point{mod(x.Mul(x, inv(new(big.Int).Add(big.NewInt(1), dxy)))),
			mod(y.Mul(y, inv(new(big.Int).Sub(big.NewInt(1), dxy))))}
	}
	mul := func(k *big.Int, a point) point {
		r := point{big.NewInt(0), big.NewInt(1)}
		for i := k.BitLen() - 1; i >= 0; i-- {
			r = add(r, r)
			if k.Bit(i) == 1 {
				r = add(r, a)
			}
		}
		return r
	}
	enc := func(y *big.Int, sign uint) string {
		b := make([]byte, 32)
		y.FillBytes(b)
		slices.Reverse(b)
		b[31] |= byte(sign << 7)
		return base64.StdEncoding.EncodeToString(b)
	}
	sqrtM1 := new(big.Int).Exp(big.NewInt(2), new(big.Int).Rsh(new(big.Int).Sub(p, big.NewInt(1)), 2), p)
	seen := map[string]bool{}
	for y := int64(2); len(seen) < 8; y++ {
		yy := big.NewInt(y * y)
		xx := mod(new(big.Int).Mul(new(big.Int).Sub(yy, big.NewInt(1)), inv(mod(new(big.Int).Add(new(big.Int).Mul(d, yy), big.NewInt(1))))))
		x := new(big.Int).Exp(xx, new(big.Int).Rsh(new(big.Int).Add(p, big.NewInt(3)), 3), p)
		if mod(new(big.Int).Sub(new(big.Int).Mul(x, x), xx)).Sign() != 0 {
			x = mod(x.Mul(x, sqrtM1))
		}
		if mod(new(big.Int).Sub(new(big.Int).Mul(x, x), xx)).Sign() != 0 {
			continue // no point with this y
		}
		torsion := mul(l, point{x, big.NewInt(y)})
		pt := point{big.NewInt(0), big.NewInt(1)}
		for range 8 {
			seen[pt.x.String()+","+pt.y.String()] = true
			pt = add(pt, torsion)
		}
	}
	ys := []*big.Int{p, new(big.Int).Add(p, big.NewInt(1))}
	for xy := range seen {
		_, s, _ := strings.Cut(xy, ",")
		y, _ := new(big.Int).SetString(s, 10)
		ys = append(ys, y)
	}
	set := map[string]bool{}
	for _, y := range ys {
		set[enc(y, 0)], set[enc(y, 1)] = true, true
	}
	return slices.Sorted(maps.Keys(set))
}

// A point of small order is no key: signatures with S = 0 verify against it for one
// message in four. Every one of them is refused, however it is spelled.
func TestOwnerKeyRefusesSmallOrderPoints(t *testing.T) {
	keys := torsionKeys(t)
	if len(keys) != 14 {
		t.Fatalf("derived %d encodings, want five y with both signs, and two non-canonical y", len(keys))
	}
	for _, k := range keys {
		if _, err := ParseOwnerKey(k); err == nil {
			t.Errorf("%s accepted as an owner key", k)
		}
		if got := TrustedKeys([]string{"r"}, k); len(got) != 1 {
			t.Errorf("%s trusted", k)
		}
	}
	// And the list is no wider than that: a real key is not refused.
	key, _ := testKey(t)
	if _, err := ParseOwnerKey(key); err != nil {
		t.Fatal(err)
	}
}

// The fingerprint the UI shows: the first 8 bytes of the key's SHA-256.
func TestFingerprint(t *testing.T) {
	key, _ := testKey(t)
	raw, _ := base64.StdEncoding.DecodeString(key)
	sum := sha256.Sum256(raw)
	if got := Fingerprint(key); got != hex.EncodeToString(sum[:8]) || len(got) != 16 {
		t.Fatalf("Fingerprint = %q", got)
	}
	if Fingerprint("garbage") != "" || Fingerprint("") != "" {
		t.Fatal("a value that is not a key has a fingerprint")
	}
}

// A statement signed with the owner key verifies only when that key is trusted; a garbled
// owner key leaves the release keys working rather than failing every statement.
func TestTrustedKeysAddTheOwnerKey(t *testing.T) {
	release, releasePriv := testKey(t)
	owner, ownerPriv := testKey(t)
	file := sha256.Sum256([]byte("Update.stu"))
	st := Statement{Kind: KindCore, Profile: ProfileProduction, Version: "0.1.0", Digest: file[:]}
	byOwner, byRelease := mustSign(t, ownerPriv, st), mustSign(t, releasePriv, st)
	base := []string{release, ""}

	if _, err := Verify(TrustedKeys(base, ""), byOwner, KindCore); err == nil {
		t.Fatal("an owner-signed statement verified without an owner key")
	}
	keys := TrustedKeys(base, owner)
	for name, text := range map[string]string{"owner": byOwner, "release": byRelease} {
		if got, err := Verify(keys, text, KindCore); err != nil || got.Matches(file[:]) != nil {
			t.Errorf("%s-signed with the owner key set: %v", name, err)
		}
	}
	if len(base) != 2 {
		t.Fatal("TrustedKeys changed the release keys it was given")
	}
	for _, garbled := range []string{owner[:43], owner + " ", " " + owner, strings.ToLower(owner), "AAAA", "-"} {
		keys := TrustedKeys(base, garbled)
		if _, err := Verify(keys, byRelease, KindCore); err != nil {
			t.Errorf("owner key %q broke the release key: %v", garbled, err)
		}
		if _, err := Verify(keys, byOwner, KindCore); err == nil && garbled != owner {
			t.Errorf("owner key %q verified the owner's statement", garbled)
		}
	}
}
