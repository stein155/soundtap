// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/stein155/soundtap/app/internal/release"
	"github.com/stein155/soundtap/app/internal/stu"
)

// signatureHeader carries <asset>.sig, the signed package statement (ADR 0048).
const signatureHeader = "X-SoundTap-Signature"

// releaseKeys are release.PublicKeys; a variable so tests can sign with their own pair.
var releaseKeys = release.PublicKeys()

var errUnsigned = errors.New("the upload is not signed: pick its .sig file together with it")

// profileFile names the image's build profile. post-build.sh writes it, and
// rootfs_check.py --profile production holds a production image to "production" there.
var profileFile = "/etc/soundtap/profile"

// allowUnsigned reports whether this speaker takes unsigned development builds: only on
// an alpha image, and only while <home>/allow-unsigned exists, which only an SSH session
// can create (ADR 0048). An image that does not say it is alpha never does.
var allowUnsigned = func(home string) bool {
	if home == "" {
		return false
	}
	if !alphaImage() {
		return false
	}
	_, err := os.Stat(filepath.Join(home, "allow-unsigned"))
	return err == nil
}

// alphaImage reports whether this image says it is an alpha (development) build.
func alphaImage() bool {
	b, err := os.ReadFile(profileFile)
	return err == nil && strings.TrimSpace(string(b)) == "alpha"
}

// signaturePresent fails an upload early, before minutes of streaming, when it carries
// no signature and the speaker does not take unsigned builds.
func (s *Handler) signaturePresent(r *http.Request) error {
	if r.Header.Get(signatureHeader) == "" && !allowUnsigned(s.homeDir) {
		return errUnsigned
	}
	return nil
}

// verifyStatement checks the upload's .sig as the signed statement about a package of
// kind, signed by a release key or the owner key (ownerkey.go), and that this image takes
// its profile: only an alpha image installs a package signed for the bench. The
// owner key is held to the same rules as a release key. There is no digest-only fallback
// (ADR 0048).
func (s *Handler) verifyStatement(r *http.Request, kind string) (release.Statement, error) {
	st, err := release.Verify(s.trustedKeys(), r.Header.Get(signatureHeader), kind)
	if err != nil {
		return release.Statement{}, err
	}
	if st.Profile != release.ProfileProduction && !alphaImage() {
		return release.Statement{}, errBenchOnly
	}
	return st, nil
}

// signatureBeforeWrite fails a firmware upload before its first byte reaches NAND unless
// its statement verifies. The rootfs streams into the slot the speaker would fall back
// to, so a signature checked only at the end of the stream came too late to keep that
// slot. checkSignature then holds the real digest to the signed one.
func (s *Handler) signatureBeforeWrite(r *http.Request) error {
	if r.Header.Get(signatureHeader) == "" {
		return s.signaturePresent(r)
	}
	_, err := s.verifyStatement(r, release.KindCore)
	return err
}

// checkSignature verifies the upload of kind whose SHA-256 is digest. It runs before
// anything the upload brings can boot or execute.
func (s *Handler) checkSignature(r *http.Request, kind string, digest []byte) error {
	if r.Header.Get(signatureHeader) == "" {
		if !allowUnsigned(s.homeDir) {
			return errUnsigned
		}
		s.log.Warn("accepting an unsigned upload: allow-unsigned is set")
		return nil
	}
	st, err := s.verifyStatement(r, kind)
	if err != nil {
		return err
	}
	return st.Matches(digest)
}

// errNotStock refuses an unsigned upload confirmed as Bose's firmware that is not
// Bose's firmware: only Bose's own packages come back without our signature.
var errNotStock = errors.New("only Bose's own firmware installs without a SoundTap signature; this package is not Bose's")

// unsignedStock reports an upload without a signature that its sender confirms as
// Bose's firmware. A production speaker takes one, after the press on the speaker,
// as the way back to stock (ADR 0048); every other upload needs our
// signature.
func unsignedStock(r *http.Request) bool {
	return r.Header.Get(signatureHeader) == "" && r.Header.Get("X-SoundTap-Confirm") == confirmStock
}

// boseModule reports a module of Bose's own chains. The scan accepts only known chains,
// and none of Bose's has a soundtap- module, so a SoundTap or native module under an
// unsigned stock upload is refused before its payload is written.
func boseModule(m stu.Module) bool {
	return !m.Native && !strings.HasPrefix(stu.BaseModuleName(m.Name), "soundtap-")
}

// boseFirmware reports a scanned package that is Bose's firmware: a stock kernel and
// only Bose's modules.
func boseFirmware(pkg *stu.Package) bool {
	if pkg == nil || pkg.Mainline || pkg.Native {
		return false
	}
	for _, m := range pkg.Modules {
		if !boseModule(m) {
			return false
		}
	}
	return true
}
