// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/stein155/soundtap/app/internal/release"
	"github.com/stein155/soundtap/app/internal/stu"
)

// signatureHeader carries <asset>.sig, the signed package statement (ADR 0048).
const signatureHeader = "X-SoundTap-Signature"

// errUnsigned refuses a package without a signature on a route that fetches it itself:
// only an upload, behind the press on the speaker, installs one (ADR 0048).
var errUnsigned = errors.New("a package the speaker fetches itself must be signed")

// verifyStatement checks the upload's .sig as the signed statement about a package of
// kind, signed by a release key. A statement that verifies is a signature on every image,
// alpha or production profile, alpha or release version alike (ADR 0048). There is no
// digest-only fallback.
func (s *Handler) verifyStatement(r *http.Request, kind string) (release.Statement, error) {
	keys := s.releaseKeys
	if keys == nil {
		keys = release.PublicKeys()
	}

	// release words its refusals for the owner, and the API answers with them as they are.
	return release.Verify(keys, r.Header.Get(signatureHeader), kind) //nolint:wrapcheck // the text is the API's 403
}

// signatureBeforeWrite fails a signed firmware upload before its first byte reaches NAND
// unless its statement verifies. The rootfs streams into the slot the speaker would fall
// back to, so a signature checked only at the end of the stream came too late to keep
// that slot. checkSignature then holds the real digest to the signed one. An upload
// without a signature passes: the upload routes that reach here, POST /api/firmware?install=1
// and POST /api/soundtapd, waited for the press on the speaker (needsPress in internal/web),
// signed or not, and that press is what an unsigned package installs on (ADR 0048). The
// press also keeps a replayed .sig with another body from wiping the fallback slot.
func (s *Handler) signatureBeforeWrite(r *http.Request) error {
	if r.Header.Get(signatureHeader) == "" {
		return nil
	}

	_, err := s.verifyStatement(r, release.KindCore)

	return err
}

// checkSignature verifies the upload of kind whose SHA-256 is digest. It runs before
// anything the upload brings can boot or execute. An unsigned upload passes, as in
// signatureBeforeWrite, and says so in the log.
func (s *Handler) checkSignature(r *http.Request, kind string, digest []byte) error {
	if r.Header.Get(signatureHeader) == "" {
		s.logger().Warn("installing an unsigned package, confirmed on the speaker", "kind", kind,
			"sha256", hex.EncodeToString(digest))

		return nil
	}

	statement, err := s.verifyStatement(r, kind)
	if err != nil {
		return err
	}

	// As in verifyStatement: the refusal is the answer to POST /api/soundtapd as worded.
	return statement.Matches(digest) //nolint:wrapcheck // the text is the API's 403 for an app upload
}

// errNeedsStock refuses Bose's firmware sent without a signature under the plain
// confirmation: the way back to stock is never a side effect of "install".
var errNeedsStock = versionRefusal(stockVersion, "", confirmStock)

// errNotStock refuses an upload confirmed as Bose's firmware that is not Bose's firmware.
var errNotStock = errors.New("this package is not Bose's firmware: install it with X-SoundTap-Confirm: install")

// unsignedStock reports an upload without a signature that its sender confirms as
// Bose's firmware: the way back to stock, after the press on the speaker (ADR 0048). Only
// Bose's modules may reach NAND then.
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
