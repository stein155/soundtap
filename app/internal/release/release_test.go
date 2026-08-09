// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func testKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub), priv
}

func mustSign(t *testing.T, priv ed25519.PrivateKey, s Statement) string {
	t.Helper()
	sig, err := Sign(priv, s)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// The .sig is one line in a fixed order; this is the text U-Boot will parse as well.
func TestStatementFormat(t *testing.T) {
	file := sha256.Sum256([]byte("file"))
	s := Statement{Kind: KindCore, Profile: ProfileProduction, Version: "0.1.0", Digest: file[:]}
	want := "soundtap-package v1 kind=core profile=production version=0.1.0 sha256=" + hex.EncodeToString(file[:])
	if s.String() != want {
		t.Fatalf("statement = %q, want %q", s, want)
	}
	key, priv := testKey(t)
	sig := mustSign(t, priv, s)
	text, b64, ok := strings.Cut(sig, " sig=")
	if !ok || text != want {
		t.Fatalf(".sig = %q", sig)
	}
	raw, _ := base64.StdEncoding.DecodeString(b64)
	pub, _ := base64.StdEncoding.DecodeString(key)
	if !ed25519.Verify(pub, []byte(want), raw) {
		t.Fatal("sig= is not the signature over the statement text")
	}
	for _, tail := range []string{"", "\n", "\r\n"} {
		got, err := Verify([]string{key}, sig+tail, KindCore)
		if err != nil || got.String() != want || got.Matches(file[:]) != nil {
			t.Errorf("tail %q: Verify = %v, %v", tail, got, err)
		}
	}
}

// Every field is signed: a .sig edited by hand, truncated, in the digest-only form of
// before 0.1.0, or for another kind than the route asks for is refused.
func TestVerifyRefuses(t *testing.T) {
	key, priv := testKey(t)
	otherKey, _ := testKey(t)
	file := sha256.Sum256([]byte("file"))
	hexDigest := hex.EncodeToString(file[:])
	good := mustSign(t, priv, Statement{Kind: KindCore, Profile: ProfileProduction, Version: "0.2.0", Digest: file[:]})
	text, sigB64, _ := strings.Cut(good, " sig=")
	rawSig, _ := base64.StdEncoding.DecodeString(sigB64)
	b64 := base64.StdEncoding.EncodeToString
	fields := strings.Fields(good)
	for name, c := range map[string]struct{ key, sig, kind string }{
		"wrong key":        {otherKey, good, KindCore},
		"swapped kind":     {key, good, KindApp},
		"edited profile":   {key, strings.Replace(good, "profile=production", "profile=alpha", 1), KindCore},
		"edited version":   {key, strings.Replace(good, "version=0.2.0", "version=0.3.0", 1), KindCore},
		"edited digest":    {key, strings.Replace(good, hexDigest, strings.Repeat("0", 64), 1), KindCore},
		"truncated":        {key, strings.Join(fields[:len(fields)-1], " "), KindCore},
		"two fields":       {key, b64(ed25519.Sign(priv, file[:])) + " " + hexDigest, KindCore},
		"digest only":      {key, b64(ed25519.Sign(priv, file[:])), KindCore},
		"old four fields":  {key, b64(ed25519.Sign(priv, file[:])) + " " + hexDigest + " 0.2.0 " + b64(rawSig), KindCore},
		"empty":            {key, "", KindCore},
		"double space":     {key, strings.Replace(good, " kind=", "  kind=", 1), KindCore},
		"reordered":        {key, strings.Replace(good, "kind=core profile=production", "profile=production kind=core", 1), KindCore},
		"extra field":      {key, text + " extra=1 sig=" + sigB64, KindCore},
		"uppercase digest": {key, strings.Replace(good, hexDigest, strings.ToUpper(hexDigest), 1), KindCore},
		"short signature":  {key, text + " sig=" + b64(rawSig[:32]), KindCore},
		"two newlines":     {key, good + "\n\n", KindCore},
		"leading space":    {key, " " + good, KindCore},
		"too long":         {key, good + strings.Repeat(" ", SigMax), KindCore},
		"invalid key":      {"AAAA", good, KindCore},
	} {
		if _, err := Verify([]string{c.key}, c.sig, c.kind); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Verify(PublicKeys(), good, KindCore); err == nil {
		t.Error("a statement signed by a test key passed the release key")
	}
}

// Only valid statements are signed at all: stock is a core version, and a version is
// MAJOR.MINOR.PATCH with nothing after it but -alpha.N.
func TestSignRefusesInvalidStatements(t *testing.T) {
	_, priv := testKey(t)
	file := sha256.Sum256([]byte("file"))
	for name, s := range map[string]Statement{
		"stock app":       {Kind: KindApp, Profile: ProfileProduction, Version: StockVersion, Digest: file[:]},
		"alpha suffix":    {Kind: KindCore, Profile: ProfileAlpha, Version: "0.1.0-alpha", Digest: file[:]},
		"two parts":       {Kind: KindCore, Profile: ProfileProduction, Version: "0.1", Digest: file[:]},
		"unknown kind":    {Kind: "bco", Profile: ProfileProduction, Version: "0.1.0", Digest: file[:]},
		"unknown profile": {Kind: KindCore, Profile: "beta", Version: "0.1.0", Digest: file[:]},
		"short digest":    {Kind: KindCore, Profile: ProfileProduction, Version: "0.1.0", Digest: file[:16]},
		"space":           {Kind: KindCore, Profile: ProfileProduction, Version: "0.1.0 x", Digest: file[:]},
	} {
		if _, err := Sign(priv, s); err == nil {
			t.Errorf("%s: signed", name)
		}
	}
	if _, err := Sign(priv, Statement{Kind: KindCore, Profile: ProfileProduction, Version: StockVersion, Digest: file[:]}); err != nil {
		t.Errorf("stock core: %v", err)
	}
}

func TestMatches(t *testing.T) {
	file := sha256.Sum256([]byte("file"))
	other := sha256.Sum256([]byte("other"))
	s := Statement{Kind: KindApp, Profile: ProfileProduction, Version: "1.0.0", Digest: file[:]}
	if s.Matches(file[:]) != nil || s.Matches(other[:]) == nil || s.Matches(file[:16]) == nil {
		t.Fatal("Matches compares digests wrongly")
	}
}

// A .sig written by tools/release/sign.sh (openssl, a throwaway key whose private half
// was discarded) verifies here: the shell and the speaker read the same format.
func TestSignShOutputVerifies(t *testing.T) {
	const pub = "syQ3h0YimtK+yq+OpfGB57V0VvhVdnEpQBwqALv1OUw="
	const sig = "soundtap-package v1 kind=core profile=production version=0.3.0 " +
		"sha256=a2beb50bfd5a837f9e0970359b42e4da8b8d465e9f33eb7e3a4989f6f2c9afc1 " +
		"sig=ukF0FTJvfHvlULoQ5vQv5Nnt859WzGwhunqfaTcItnGMQhw2InFc652NSe/3BpeNbjaRKgNCcSgQY/96d2mMAQ==\n"
	s, err := Verify([]string{pub}, sig, KindCore)
	if err != nil {
		t.Fatal(err)
	}
	asset := sha256.Sum256([]byte("a package signed by sign.sh\n"))
	if s.Profile != ProfileProduction || s.Version != "0.3.0" || s.Matches(asset[:]) != nil {
		t.Fatalf("sign.sh statement = %+v", s)
	}
}

// Speakers trust two keys, the active and the reserve: a statement signed by either
// verifies, one signed by neither does not (RELEASING.md, "Release keys").
func TestVerifyAcceptsEitherKey(t *testing.T) {
	active, activePriv := testKey(t)
	reserve, reservePriv := testKey(t)
	_, strangerPriv := testKey(t)
	file := sha256.Sum256([]byte("file"))
	s := Statement{Kind: KindCore, Profile: ProfileProduction, Version: "0.2.0", Digest: file[:]}
	keys := []string{active, reserve}
	for name, priv := range map[string]ed25519.PrivateKey{"active": activePriv, "reserve": reservePriv} {
		if _, err := Verify(keys, mustSign(t, priv, s), KindCore); err != nil {
			t.Errorf("signed by the %s key: %v", name, err)
		}
	}
	if _, err := Verify(keys, mustSign(t, strangerPriv, s), KindCore); err == nil {
		t.Error("a statement signed by neither key verified")
	}
	// After a rotation the retired key is no longer in the list.
	if _, err := Verify([]string{reserve}, mustSign(t, activePriv, s), KindCore); err == nil {
		t.Error("a statement signed by a key that is not listed verified")
	}
}

// The reserve is a placeholder until the maintainer fills it in: an empty key is skipped,
// not an error, while a malformed one or no key at all refuses everything.
func TestVerifyKeyList(t *testing.T) {
	key, priv := testKey(t)
	file := sha256.Sum256([]byte("file"))
	sig := mustSign(t, priv, Statement{Kind: KindApp, Profile: ProfileProduction, Version: "1.0.0", Digest: file[:]})
	for name, keys := range map[string][]string{
		"empty reserve":   {key, ""},
		"empty first":     {"", key},
		"blank reserve":   {key, " \n"},
		"trailing spaces": {key + "\n"},
	} {
		if _, err := Verify(keys, sig, KindApp); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, keys := range map[string][]string{
		"no keys":           nil,
		"only empty keys":   {"", ""},
		"malformed reserve": {key, "AAAA"},
		"short key":         {key, base64.StdEncoding.EncodeToString(make([]byte, 31))},
		"unpadded key":      {strings.TrimRight(key, "=")},
	} {
		if _, err := Verify(keys, sig, KindApp); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
}

// The compiled keys: the active key is a real ed25519 key, the reserve is empty (the
// placeholder) or another real key. A malformed constant would refuse every update.
func TestCompiledKeys(t *testing.T) {
	keys := PublicKeys()
	if len(keys) != 2 || keys[0] != PublicKey || keys[1] != ReservePublicKey {
		t.Fatalf("PublicKeys = %q", keys)
	}
	if _, err := parseKeys(keys); err != nil {
		t.Fatalf("the compiled keys: %v", err)
	}
	if pub, err := base64.StdEncoding.Strict().DecodeString(PublicKey); err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("PublicKey is not an ed25519 key: %v", err)
	}
	if ReservePublicKey != "" && ReservePublicKey == PublicKey {
		t.Fatal("the reserve key is the active key: it reserves nothing")
	}
}
