// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// U-Boot reads soundtap_owner_key as Go does (ADR 0048): the same name, and the same
// values taken as a key, down to every value one edit away from a good one. Anything else
// is no key at all, so the release keys keep working.
func TestUbootOwnerKeyMatchesGo(t *testing.T) {
	header, err := os.ReadFile(filepath.Join(ubootDir, "soundtap-sig.h"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(header, []byte("#define ST_OWNER_KEY_ENV\t\""+OwnerKeyEnv+"\"")) {
		t.Fatalf("soundtap-sig.h does not name %s as the owner key's variable", OwnerKeyEnv)
	}
	bin := buildUbootVerifier(t)
	key, _ := testKey(t)
	values := []string{key, "", " " + key, key + " ", key + "\n", strings.TrimRight(key, "="), key + "=",
		strings.ReplaceAll(strings.ReplaceAll(key, "+", "-"), "/", "_"), "AAAA", strings.Repeat("A", 43) + "=",
		strings.Repeat("A", 42) + "B=", strings.Repeat("/", 43) + "="}
	values = append(values, torsionKeys(t)...)
	for i := range len(key) {
		values = append(values, key[:i]+key[i+1:], key[:i+1]+key[i:])
		for _, c := range []byte{'=', '-', '_', ' ', 'A', '/', key[i] ^ 0x20} {
			values = append(values, key[:i]+string([]byte{c})+key[i+1:])
		}
	}
	cases, want := []string{"ownerkey -"}, []string{"err"} // not set at all
	for _, v := range values {
		cases = append(cases, "ownerkey "+envArg(v))
		if pub, err := ownerKey(v); err == nil {
			want = append(want, "ok "+hex.EncodeToString(pub))
		} else {
			want = append(want, "err")
		}
	}
	accepted := 0
	for i, got := range runUboot(t, bin, cases) {
		if got != want[i] {
			t.Errorf("%s: U-Boot %s, Go %s", cases[i], got, want[i])
		}
		if got != "err" {
			accepted++
		}
	}
	// The good key and the one of all '/' bits, at least.
	if accepted < 2 {
		t.Fatalf("only %d values were keys", accepted)
	}
}

// envArg spells an environment value for the harness: "e" when set empty, else hex.
func envArg(v string) string {
	if v == "" {
		return "e"
	}
	return hex.EncodeToString([]byte(v))
}

// The stick with the owner's own key in the environment (ADR 0048): a package signed with
// it goes in under the same rules as one signed with a release key; without the key, or
// with a value that is not one, it does not, and a garbled value leaves the release keys
// working.
func TestUbootStickOwnerKey(t *testing.T) {
	bin := buildUbootVerifier(t)
	active, activePriv := testKey(t)
	owner, ownerPriv := testKey(t)
	other, _ := testKey(t)
	keys := hexKeys(t, active)
	pkg := sha256.Sum256([]byte("Update.stu"))
	wrong := sha256.Sum256([]byte("another Update.stu"))
	sign := func(priv ed25519.PrivateKey, profile, version string) string {
		return mustSign(t, priv, Statement{Kind: KindCore, Profile: profile, Version: version, Digest: pkg[:]})
	}
	byOwner := sign(ownerPriv, ProfileProduction, "0.2.0")
	byRelease := sign(activePriv, ProfileProduction, "0.2.0")
	const unset = "\x00unset"
	var names, want, cases []string
	for _, c := range []struct {
		name, image, env, text string
		digest                 [32]byte
		want                   string
	}{
		{"owner-signed, owner key set", ProfileProduction, owner, byOwner, pkg, "ok"},
		{"owner-signed older: the stick takes any version", ProfileProduction, owner, sign(ownerPriv, ProfileProduction, "0.0.1"), pkg, "ok"},
		{"owner-signed alpha on an alpha image", ProfileAlpha, owner, sign(ownerPriv, ProfileAlpha, "0.2.0"), pkg, "ok"},
		{"owner-signed alpha on a production image", ProfileProduction, owner, sign(ownerPriv, ProfileAlpha, "0.2.0"), pkg, stWrongProfile},
		{"owner-signed stock", ProfileProduction, owner, sign(ownerPriv, ProfileProduction, StockVersion), pkg, stStock},
		{"owner-signed, another file", ProfileProduction, owner, byOwner, wrong, stWrongFile},
		{"owner-signed, no owner key", ProfileProduction, unset, byOwner, pkg, stBadSignature},
		{"owner-signed, owner key empty", ProfileProduction, "", byOwner, pkg, stBadSignature},
		{"owner-signed, another owner key", ProfileProduction, other, byOwner, pkg, stBadSignature},
		{"owner-signed, owner key cut short", ProfileProduction, owner[:43], byOwner, pkg, stBadSignature},
		{"owner-signed, owner key with a space", ProfileProduction, owner + " ", byOwner, pkg, stBadSignature},
		{"release-signed, no owner key", ProfileProduction, unset, byRelease, pkg, "ok"},
		{"release-signed, owner key set", ProfileProduction, owner, byRelease, pkg, "ok"},
		{"release-signed, garbled owner key", ProfileProduction, "not a key at all", byRelease, pkg, "ok"},
		{"release-signed, owner key cut short", ProfileProduction, owner[:43], byRelease, pkg, "ok"},
	} {
		env := envArg(c.env)
		if c.env == unset {
			env = "-"
		}
		names = append(names, c.name)
		want = append(want, c.want)
		cases = append(cases, fmt.Sprintf("stick %s %s %s %s %s", c.image, keys, hexText(c.text), hex.EncodeToString(c.digest[:]), env))
	}
	for i, got := range runUboot(t, bin, cases) {
		if got != want[i] {
			t.Errorf("%s: %s, want %s", names[i], got, want[i])
		}
	}
}
