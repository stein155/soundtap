// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// MediaURL reports whether raw may be handed to ffmpeg on behalf of a caller nobody
// authenticated: every media entry point (DLNA, /api/upnp/play, /api/speaker/notify, the
// player itself) asks this one function (ADR 0041).
//
// Only http(s): ffmpeg opens file:, concat:, pipe: and a dozen other protocols as readily,
// which would turn "play this track" into "read this path off the speaker". And the host
// must pass fetchableHost, so the speaker's own admin API on loopback and link-local
// metadata services stay out of reach. Private LAN addresses are allowed: that is where a
// DLNA server lives.
func MediaURL(ctx context.Context, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != schemeHTTPS) {
		return false
	}

	return fetchableHost(ctx, u.Hostname())
}

// lookupIP is the resolver used to vet a controller-supplied hostname. It is a
// variable so the test can answer without a network.
var lookupIP = net.DefaultResolver.LookupIPAddr //nolint:gochecknoglobals // a variable so the test can answer DNS

const (
	// lookupTimeout bounds the DNS lookup that vets a media host.
	lookupTimeout = 3 * time.Second
	// maxRedirects is how many redirects a media fetch follows.
	maxRedirects = 10
)

var (
	// errRefusedAddress is a media fetch that would dial an address fetchableIP refuses.
	errRefusedAddress = errors.New("refusing to fetch media from")
	// errTooManyRedirects is a media fetch past maxRedirects.
	errTooManyRedirects = errors.New("stopped after 10 redirects")
	// errNonMediaRedirect is a redirect to a URL MediaURL refuses.
	errNonMediaRedirect = errors.New("redirect to a non-media URL")
)

// fetchableHost reports whether host is a sensible media origin. A DLNA controller
// on the LAN is anonymous, and the URI it hands us is fetched by ffmpeg running as
// root on the speaker — so loopback (the speaker's own admin API) and link-local
// (169.254.169.254-style metadata services) are refused. Ordinary private LAN
// addresses stay allowed: that is where a DLNA media server lives.
//
// A hostname is resolved and every address it yields must pass, otherwise a name
// pointing at 127.0.0.1 walks straight past an IP-literal check.
//
// ffmpeg resolves the name again itself, so a DNS record that changes
// between the two lookups still slips through. Closing that needs the fetch pinned
// to the address vetted here — a custom dialer, which ffmpeg does not give us.
func fetchableHost(ctx context.Context, host string) bool {
	if host == "" {
		return false
	}

	// An IPv6 literal may carry a zone (fe80::1%wlan0), which net.ParseIP does not take.
	literal, err := netip.ParseAddr(host)
	if err == nil {
		return fetchableIP(net.IP(literal.WithZone("").Unmap().AsSlice()))
	}

	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return false
	}

	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	addrs, err := lookupIP(ctx, host)
	if err != nil || len(addrs) == 0 {
		return false
	}

	for _, a := range addrs {
		if !fetchableIP(a.IP) {
			return false
		}
	}

	return true
}

// fetchableIP holds IPv6 to the IPv4 rule: loopback (::1) and link-local (fe80::/10) are
// refused like 127/8 and 169.254/16, a ULA (fc00::/7) is allowed like a private IPv4
// address, and a global one like a public one (ADR 0041, ADR 0134).
func fetchableIP(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() && !ip.IsInterfaceLocalMulticast()
}

// MediaClient fetches what a MediaURL names, such as a playlist, and holds every hop to
// the same rule: a redirect must be a MediaURL too, and the address actually dialled must
// pass fetchableIP, so neither a redirect nor a changed DNS answer reaches loopback.
func MediaClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if ip := net.ParseIP(host); err != nil || ip == nil || !fetchableIP(ip) {
			return fmt.Errorf("%w %s", errRefusedAddress, address)
		}

		return nil
	}}

	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: timeout},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errTooManyRedirects
			}

			if !MediaURL(req.Context(), req.URL.String()) {
				return fmt.Errorf("%w: %s", errNonMediaRedirect, req.URL.Redacted())
			}

			return nil
		},
	}
}
