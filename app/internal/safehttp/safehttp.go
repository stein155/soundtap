// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package safehttp is the shared SSRF-safe HTTP layer: a transport that refuses
// non-public addresses, a request helper that rides out a speaker's not-yet-synced
// clock, and bounded JSON/file fetches.
package safehttp

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// maxArtifact caps a single downloaded artifact. Generous for a stripped ARMv7
// binary, but bounds the write on the speaker's small flash so a
// hostile/compromised release host can't stream unbounded data to fill it (the
// checksum is only verified after the whole file is written).
const maxArtifact = 64 << 20

// maxJSON caps a JSON document GetJSON decodes.
const maxJSON = 4 << 20

// The transport SafeTransport builds.
const (
	dialTimeout         = 10 * time.Second
	maxIdleConns        = 10
	idleConnTimeout     = 30 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
)

// The clock Do waits on; tests replace them.
//
//nolint:gochecknoglobals // test seams: a test fakes the clock and shortens the waits
var (
	now               = time.Now
	clockPollInterval = 250 * time.Millisecond
	clockWaitLimit    = 30 * time.Second
)

// errTooLarge: a download went past maxArtifact.
var errTooLarge = errors.New("artifact exceeds size limit")

// errNotPublic: the dialer refused an address that is not globally reachable.
var errNotPublic = errors.New("refusing to connect to non-public address")

// Do retries one HTTPS request after an invalid wall clock becomes credible.
// Speakers have no working RTC, so certificate validation can race boot-time NTP.
func Do(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req) //nolint:gosec // G704: fetching the caller's URL is the point; SafeTransport guards it
	if err == nil {
		return resp, nil
	}

	notBefore, behind := certificateNotYetValid(err)
	if req.URL.Scheme != "https" || !behind || !waitForClock(ctx, notBefore) {
		return resp, fmt.Errorf("request: %w", err)
	}

	retry := req.Clone(ctx)
	// http.NoBody carries no GetBody, but there is nothing to replay.
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return resp, fmt.Errorf("request: %w", err)
		}

		retry.Body, err = req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("replay the request body: %w", err)
		}
	}

	resp, err = client.Do(retry) //nolint:gosec // G704: the same request again, through the same client
	if err != nil {
		return resp, fmt.Errorf("request after the clock caught up: %w", err)
	}

	return resp, nil
}

func clockSane(notBefore time.Time) bool {
	t := now()

	return t.Year() >= 2020 && !t.Before(notBefore)
}

func certificateNotYetValid(err error) (time.Time, bool) {
	var invalid x509.CertificateInvalidError
	if !errors.As(err, &invalid) || invalid.Reason != x509.Expired || invalid.Cert == nil {
		return time.Time{}, false
	}

	return invalid.Cert.NotBefore, now().Before(invalid.Cert.NotBefore)
}

func waitForClock(ctx context.Context, notBefore time.Time) bool {
	ticker := time.NewTicker(clockPollInterval)
	defer ticker.Stop()

	timer := time.NewTimer(clockWaitLimit)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
			if clockSane(notBefore) {
				return true
			}
		}
	}
}

// GetJSON fetches url with client and decodes the (bounded) JSON body into out.
// userAgent sets the User-Agent so GitHub doesn't reject the request.
func GetJSON(ctx context.Context, client *http.Client, userAgent, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := Do(ctx, client, req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return HTTPStatusError{URL: url, Status: resp.StatusCode}
	}

	err = json.NewDecoder(io.LimitReader(resp.Body, maxJSON)).Decode(out)
	if err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}

	return nil
}

// Download fetches url into dst with the given file mode, using client. A partial
// download is removed rather than left behind.
func Download(ctx context.Context, client *http.Client, userAgent, url, dst string, mode fs.FileMode) error {
	return DownloadWatched(ctx, client, userAgent, url, dst, mode, nil)
}

// DownloadWatched is Download that tells seen, when not nil, how many bytes are in dst so
// far and how many the server announced (-1 when it did not say): once before the first
// byte and after every write.
func DownloadWatched(ctx context.Context, client *http.Client, userAgent, url, dst string, mode fs.FileMode,
	seen func(done, total int64),
) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := Do(ctx, client, req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return HTTPStatusError{URL: url, Status: resp.StatusCode}
	}

	file, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	var out io.Writer = file

	if seen != nil {
		seen(0, resp.ContentLength)
		out = &watchedWriter{w: file, done: 0, total: resp.ContentLength, seen: seen}
	}

	n, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxArtifact+1))
	if copyErr == nil && n > maxArtifact {
		copyErr = fmt.Errorf("%s: %w", url, errTooLarge)
	}

	closeErr := file.Close()

	if copyErr != nil {
		_ = os.Remove(dst)

		return copyErr
	}

	if closeErr != nil {
		_ = os.Remove(dst)

		return fmt.Errorf("download: %w", closeErr)
	}

	return nil
}

// watchedWriter counts what reaches w for DownloadWatched.
type watchedWriter struct {
	w           io.Writer
	done, total int64
	seen        func(done, total int64)
}

func (c *watchedWriter) Write(b []byte) (int, error) {
	written, err := c.w.Write(b)
	c.done += int64(written)
	c.seen(c.done, c.total)

	if err != nil {
		return written, fmt.Errorf("write: %w", err)
	}

	return written, nil
}

// SafeTransport returns a transport whose dialer refuses any non-public address,
// checked per hop so a 302-to-loopback or a DNS rebind can't slip an internal
// target past a pre-flight hostname check. All release artifacts live on public
// hosts (github.com), so nothing legitimate is affected.
func SafeTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout: dialTimeout,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return fmt.Errorf("dial address: %w", err)
				}

				if ip := net.ParseIP(host); ip == nil || !PublicIP(ip) {
					return fmt.Errorf("%w %s", errNotPublic, address)
				}

				return nil
			},
		}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        maxIdleConns,
		IdleConnTimeout:     idleConnTimeout,
		TLSHandshakeTimeout: tlsHandshakeTimeout,
	}
}

// PublicIP reports whether a is globally reachable: not loopback, RFC1918/ULA,
// link-local, multicast, unspecified, or one of IANA's special-purpose ranges that
// are not globally reachable (shared CGNAT space, documentation, benchmarking...).
// TEST-NET-3 matters here: the ST20 USB TAP lives in 203.0.113.0/30.
func PublicIP(a net.IP) bool {
	addr, ok := netip.AddrFromSlice(a)
	if !ok {
		return false
	}

	addr = addr.Unmap()
	if nat64().Contains(addr) {
		// A NAT64 gateway turns 64:ff9b::/96 into the IPv4 address in its last 32 bits,
		// a private one on the gateway's side too: that address is what is reached.
		v4 := addr.As16()

		return PublicIP(net.IPv4(v4[12], v4[13], v4[14], v4[15]))
	}

	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}

	for _, p := range globalExceptions() {
		if p.Contains(addr) {
			return true
		}
	}

	for _, p := range notGlobal() {
		if p.Contains(addr) {
			return false
		}
	}

	return true
}

// From the IANA IPv4 and IPv6 special-purpose address registries ("Globally
// Reachable: False"), minus what the checks above already cover.
func notGlobal() []netip.Prefix {
	return prefixes(
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "3fff::/20", "5f00::/16",
	)
}

// nat64 is the well-known NAT64 prefix (RFC 6052).
func nat64() netip.Prefix { return netip.MustParsePrefix("64:ff9b::/96") }

// Blocks inside notGlobal that the registries mark globally reachable.
func globalExceptions() []netip.Prefix {
	return prefixes(
		"192.0.0.9/32", "192.0.0.10/32",
		"2001:1::1/128", "2001:1::2/128", "2001:3::/32", "2001:4:112::/48", "2001:20::/28", "2001:30::/28",
	)
}

func prefixes(ss ...string) []netip.Prefix {
	ps := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		ps[i] = netip.MustParsePrefix(s)
	}

	return ps
}

// userinfo is the user:password@ part of a URL in text: the authority up to its last @,
// as a URL parser reads it, so a password with an @ in it goes whole.
var userinfo = regexp.MustCompile(`(://)[^/?#\s]+@`)

// LogURL is text, a URL or a line that holds some, without the user:password@ part of
// any URL in it. A custom stream may carry its credentials there, and some services put a
// token in the user, so both go: credentials never reach the log (AGENTS.md).
func LogURL(text string) string {
	if !strings.Contains(text, "@") {
		return text
	}

	return userinfo.ReplaceAllString(text, "${1}")
}

// HTTPStatusError is returned when a fetch gets a non-200 response.
type HTTPStatusError struct {
	URL    string
	Status int
}

func (e HTTPStatusError) Error() string { return LogURL(e.URL) + " status " + strconv.Itoa(e.Status) }
