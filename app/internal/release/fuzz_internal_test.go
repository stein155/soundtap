// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// FuzzVerify feeds Verify the .sig an upload carries. The statement format is strict, so
// exactly one text verifies for each signed statement: anything the fuzzer makes of it that
// still verifies must be that statement, byte for byte. `go test` runs the seeds;
// `go test -fuzz=FuzzVerify ./internal/release` searches further.
func FuzzVerify(f *testing.F) {
	seed := bytes.Repeat([]byte{7}, ed25519.SeedSize) // a fixed throwaway key
	priv := ed25519.NewKeyFromSeed(seed)
	pub := base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	digest := sha256.Sum256([]byte("package"))
	signed := Statement{Kind: KindCore, Profile: ProfileProduction, Version: "0.1.0", Digest: digest[:]}

	good, err := Sign(priv, signed)
	if err != nil {
		f.Fatal(err)
	}

	f.Add(good)
	f.Add(good + "\n")
	f.Add(good + "\r\n")
	f.Add(good[:len(good)-4])
	f.Add(statementPrefix + "kind=app profile=alpha version=1.2.3 sha256=00 sig=AA==")
	f.Add("sha256 only")
	f.Add("")
	f.Fuzz(func(t *testing.T, sigText string) {
		s, sig, err := parse(sigText)
		if err == nil {
			// Canonical: the accepted line is exactly what Sign would write for it.
			line := s.String() + " sig=" + base64.StdEncoding.EncodeToString(sig)
			if trimmed := trimEOL(sigText); trimmed != line {
				t.Fatalf("accepted %q, which reads back as %q", sigText, line)
			}
		}

		got, err := Verify([]string{pub}, sigText, KindCore)
		if err != nil {
			return
		}

		if got.String() != signed.String() || trimEOL(sigText) != good {
			t.Fatalf("%q verified as %+v", sigText, got)
		}
	})
}

func trimEOL(text string) string {
	if n := len(text); n > 0 && text[n-1] == '\n' {
		text = text[:n-1]
	}

	if n := len(text); n > 0 && text[n-1] == '\r' {
		text = text[:n-1]
	}

	return text
}
