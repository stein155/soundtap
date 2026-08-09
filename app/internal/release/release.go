// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package release holds what SoundTap needs to trust a release asset: a file
// checksum and the signed statement an <asset>.sig carries (ADR 0048).
package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// SHA256File returns the lowercase hex SHA-256 of the file at path.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PublicKey is the active ed25519 key SoundTap release assets are signed with (ADR 0048).
// The private half stays offline with the maintainer; tools/release/sign-release.sh signs with it
// (ADR 0035), and tools/release/sign.sh refuses any other key.
const PublicKey = "88omIN8FRazo9PIEnIHv3JUqYTQBzN+wnqpexHvX+MY="

// ReservePublicKey is the reserve release key: a second ed25519 key, kept offline apart from
// the active one, that no release is signed with until the active key is retired. Every
// speaker trusts it from the start, so a release signed with it reaches speakers in the
// field when the active key is lost or leaked (RELEASING.md, "Release keys").
//
// PLACEHOLDER: empty until the maintainer generates the reserve key and fills in its public
// half here (base64 of the raw 32 bytes, as PublicKey). Verification skips an empty key.
const ReservePublicKey = ""

// PublicKeys are the keys a release asset may be signed with: the active and the reserve.
func PublicKeys() []string { return []string{PublicKey, ReservePublicKey} }

// What a package is: a SoundTap Core STU or a SoundTap app binary. A route takes only
// its own kind, so a signed app is never a firmware image and the other way round.
const (
	KindCore = "core"
	KindApp  = "app"
)

// Who a package is for: production is a release; alpha is a bench build, which only an
// alpha image installs.
const (
	ProfileProduction = "production"
	ProfileAlpha      = "alpha"
)

// StockVersion is the version of a Bose package signed for the way back to stock. Only
// a core package carries it.
const StockVersion = "stock"

// statementPrefix opens every statement; v1 is this format.
const statementPrefix = "soundtap-package v1 "

// SigMax bounds a .sig. A statement with its signature is about 210 bytes.
const SigMax = 1024

// Statement is what an <asset>.sig vouches for, in one line that soundtapd, a shell
// script and U-Boot can all read:
//
//	soundtap-package v1 kind=core profile=production version=0.1.0 sha256=<64 hex> sig=<base64>
//
// The fields come in that order, separated by single spaces; the digest is lowercase
// hex. sig is the ed25519 signature, in padded standard base64, over everything before
// " sig=": the statement text, without a newline. One trailing newline (LF or CRLF)
// after the line is ignored; anything else makes the file invalid.
type Statement struct {
	Kind    string // KindCore or KindApp
	Profile string // ProfileProduction or ProfileAlpha
	Version string // a Version (MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-alpha.N), or StockVersion for a core package
	Digest  []byte // the asset's SHA-256
}

// String is the statement text the signature covers.
func (s Statement) String() string {
	return statementPrefix + "kind=" + s.Kind + " profile=" + s.Profile + " version=" + s.Version +
		" sha256=" + hex.EncodeToString(s.Digest)
}

func (s Statement) valid() error {
	switch {
	case s.Kind != KindCore && s.Kind != KindApp:
		return fmt.Errorf("unknown package kind %q", s.Kind)
	case s.Profile != ProfileProduction && s.Profile != ProfileAlpha:
		return fmt.Errorf("unknown package profile %q", s.Profile)
	case len(s.Digest) != sha256.Size:
		return errors.New("the statement names no SHA-256 digest")
	case s.Version == StockVersion && s.Kind == KindCore:
		return nil
	case !ValidVersion(s.Version):
		return fmt.Errorf("invalid package version %q", s.Version)
	}
	return nil
}

// Sign writes the .sig line for s with priv. tools/release/sign.sh writes the same line
// with openssl; tests sign with throwaway keys through this.
func Sign(priv ed25519.PrivateKey, s Statement) (string, error) {
	if err := s.valid(); err != nil {
		return "", err
	}
	text := s.String()
	return text + " sig=" + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(text))), nil
}

// errNotStatement refuses a .sig that is not a signed statement, such as the digest-only
// form SoundTap used before 0.1.0: nothing falls back to it.
var errNotStatement = errors.New("the signature file is not a SoundTap package statement: sign the package again with tools/release/sign.sh")

// parse reads sigText strictly: the fields in order, single spaces, canonical encodings.
func parse(sigText string) (Statement, []byte, error) {
	var s Statement
	if len(sigText) > SigMax {
		return s, nil, errNotStatement
	}
	line := strings.TrimSuffix(strings.TrimSuffix(sigText, "\n"), "\r")
	rest, ok := strings.CutPrefix(line, statementPrefix)
	// Go's base64 skips newlines; the format has none inside the line.
	if !ok || strings.ContainsAny(rest, "\r\n\t") {
		return s, nil, errNotStatement
	}
	fields := strings.Split(rest, " ")
	keys := []string{"kind=", "profile=", "version=", "sha256=", "sig="}
	if len(fields) != len(keys) {
		return s, nil, errNotStatement
	}
	values := make([]string, len(keys))
	for i, key := range keys {
		if values[i], ok = strings.CutPrefix(fields[i], key); !ok {
			return s, nil, errNotStatement
		}
	}
	s.Kind, s.Profile, s.Version = values[0], values[1], values[2]
	digest, err := hex.DecodeString(values[3])
	if err != nil || values[3] != strings.ToLower(values[3]) {
		return s, nil, errNotStatement
	}
	s.Digest = digest
	if err := s.valid(); err != nil {
		return s, nil, err
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(values[4])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return s, nil, errors.New("invalid signature encoding")
	}
	return s, sig, nil
}

// Verify checks sigText, an <asset>.sig, against the release keys (base64 ed25519 public
// keys; a signature by any one of them verifies) and returns the statement it signs. An
// empty key, the reserve before it is filled in, is skipped; a malformed one fails every
// check, so a bad constant cannot pass unnoticed. A statement for another kind than the
// caller installs is refused here, so no route can forget to ask. The profile and the
// version are the caller's policy (ADR 0048); the digest is checked with Matches once the
// file is hashed.
func Verify(keys []string, sigText, kind string) (Statement, error) {
	pubs, err := parseKeys(keys)
	if err != nil {
		return Statement{}, err
	}
	s, sig, err := parse(sigText)
	if err != nil {
		return Statement{}, err
	}
	signed := false
	for _, pub := range pubs {
		if ed25519.Verify(pub, []byte(s.String()), sig) {
			signed = true
			break
		}
	}
	if !signed {
		return Statement{}, errors.New("the signature does not verify against the release key")
	}
	if s.Kind != kind {
		return Statement{}, fmt.Errorf("this package is signed as a SoundTap %s package, not %s", s.Kind, kind)
	}
	return s, nil
}

// parseKeys decodes the non-empty keys; it fails on a malformed one, or when none is left.
func parseKeys(keys []string) ([]ed25519.PublicKey, error) {
	var pubs []ed25519.PublicKey
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		pub, err := base64.StdEncoding.Strict().DecodeString(k)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return nil, errors.New("invalid release public key")
		}
		pubs = append(pubs, ed25519.PublicKey(pub))
	}
	if len(pubs) == 0 {
		return nil, errors.New("no release public key")
	}
	return pubs, nil
}

// Matches reports whether the file that hashed to digest is the one s names.
func (s Statement) Matches(digest []byte) error {
	if len(digest) != sha256.Size || !bytes.Equal(s.Digest, digest) {
		return errors.New("the signature does not match this file")
	}
	return nil
}
