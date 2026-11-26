// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/safehttp"
)

const (
	mediaServerURN = "urn:schemas-upnp-org:device:MediaServer:1"
	cdServiceURN   = "urn:schemas-upnp-org:service:ContentDirectory:1"
	searchInterval = 60 * time.Second
	defaultTTL     = 30 * time.Minute
	browseMax      = 32 // per Browse request; a folder is fetched page by page
	// browseTotalMax bounds one folder listing.
	// a folder past this is cut off; add an offset to /api/upnp/browse if one ever matters.
	browseTotalMax = 1000
	// browseBytesMax bounds the strings one folder listing keeps across all pages; a
	// listing that outgrows it is cut off like one past browseTotalMax. fieldMax and
	// urlMax bound a single stored field.
	browseBytesMax = 4 << 20
	fieldMax       = 1 << 10
	urlMax         = 2 << 10
	// soapBodyMax bounds a Browse response. The DIDL-Lite for browseMax items is
	// escaped inside <Result>, and servers emit a <res> element per transcoding
	// profile with long protocolInfo strings, so a realistic 32-item folder runs to
	// tens of KiB and a verbose server well past 100 KiB. Anything under that
	// truncates mid-document and the parse fails, so this is deliberately generous:
	// it is a memory bound, not a protocol expectation.
	soapBodyMax = 1 << 20 // 1 MiB
	// descBodyMax bounds a device description document.
	descBodyMax = 64 << 10 // 64 KiB
	// searchWindow is how long one M-SEARCH round collects responses. It must exceed
	// the MX value advertised in the request, since devices spread their replies
	// randomly across that interval.
	searchWindow = 4 * time.Second
	// searchMX is the MX (maximum wait) advertised to devices, in seconds.
	searchMX = 3
	// descFetchLimit bounds how many device descriptions are fetched concurrently
	// after a search round.
	descFetchLimit = 4
	// maxAdverts and maxRejected bound one round's work and the rejected table: SSDP
	// replies are unauthenticated, and a flood of unique USNs must not grow either.
	maxAdverts  = 32
	maxRejected = 256
	// maxServers, maxTTL and the field caps bound what a LAN device can make us keep:
	// every successful description stays resident until its advert expires.
	maxServers    = 64
	maxTTL        = 24 * time.Hour
	maxNameLen    = 128
	maxControlURL = 2048
	// descTimeout bounds one device description fetch.
	descTimeout = 8 * time.Second
	// ssdpReplyMax is the largest M-SEARCH reply read; a longer one is cut off.
	ssdpReplyMax = 4096
)

// errNoAnswer marks a description fetch that got no usable reply. It says nothing about
// the device: a NAS that is waking up answers the next round.
var errNoAnswer = errors.New("no answer")

// ErrUnknownServer is Browse asked for a server the registry does not hold.
var ErrUnknownServer = errors.New("server not in registry")

var (
	// errNoContentDirectory is a device description without a ContentDirectory service.
	errNoContentDirectory = errors.New("no ContentDirectory service found")
	// errForeignControlURL is a control URL off the host that served the description.
	errForeignControlURL = errors.New("is not on the device's own host")
	// errControlURLTooLong is a control URL past maxControlURL.
	errControlURLTooLong = errors.New("controlURL too long")
)

// MediaServer is a discovered UPnP MediaServer:1 device.
type MediaServer struct {
	USN          string `json:"usn"`
	FriendlyName string `json:"friendlyName"`
	Location     string `json:"location"`
	controlURL   string // ContentDirectory SOAP control URL (absolute)
	expiresAt    time.Time
}

// Item is a DIDL-Lite object: container or audio item.
type Item struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Class  string `json:"class"`
	Artist string `json:"-"`
	Album  string `json:"-"`
	Art    string `json:"-"`
	URL    string `json:"url,omitempty"` // resource URL; empty for containers
}

// Registry discovers MediaServer:1 devices via SSDP and exposes ContentDirectory Browse.
type Registry struct {
	mu      sync.Mutex
	servers map[string]*MediaServer // keyed by USN
	// rejected holds devices whose description fetch failed (a Sonos player answers
	// the MediaServer search but has no ContentDirectory), until their advert expires.
	// Without it every search round refetched and logged them again. A device that only
	// failed to answer is not held here; see errNoAnswer.
	rejected map[string]time.Time
	// told is what the log last said about a rejected device (its LOCATION and the
	// reason), so a device that is refetched each time its advert expires and fails the
	// same way is logged once, not every half hour all day.
	told   map[string]string
	client *http.Client
	// rounds lets one search run at a time; lastRound is when the last one ended.
	rounds    chan struct{}
	browses   chan struct{} // admits one Browse at a time
	lastRound time.Time
}

// NewRegistry creates a ready Registry.
func NewRegistry() *Registry {
	return &Registry{
		mu:       sync.Mutex{},
		rounds:   make(chan struct{}, 1),
		browses:  make(chan struct{}, 1),
		servers:  map[string]*MediaServer{},
		rejected: map[string]time.Time{},
		told:     map[string]string{},
		// LAN media servers are private addresses, which SafeTransport refuses.
		// No redirects: a device description must not bounce soundtapd to another host.
		client: &http.Client{Timeout: descTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		lastRound: time.Time{},
	}
}

// Discover runs SSDP M-SEARCH for MediaServer:1 on the LAN until ctx is cancelled.
// Call once in a goroutine.
func (r *Registry) Discover(ctx context.Context, log func(string, ...any)) {
	ticker := time.NewTicker(searchInterval)
	defer ticker.Stop()

	r.Search(ctx, log)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Search(ctx, log)
		}
	}
}

// Search runs one round now, for someone who asked. A caller that had to wait for a
// round in progress takes that round's result, so a burst of requests costs one more
// round at most.
func (r *Registry) Search(ctx context.Context, log func(string, ...any)) {
	asked := time.Now()

	select { // not a mutex: a caller that gives up must stop waiting
	case r.rounds <- struct{}{}:
		defer func() { <-r.rounds }()
	case <-ctx.Done():
		return
	}

	if r.lastRound.After(asked) {
		return
	}

	r.searchRound(ctx, log)

	if ctx.Err() == nil { // a round cut short answers nobody who waited for it
		r.lastRound = time.Now()
	}
}

// Servers returns currently-live (non-expired) discovered servers.
// Expired entries are pruned in-place.
func (r *Registry) Servers() []MediaServer {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	out := []MediaServer{} // an empty list is [], not null: the API says array

	for usn, srv := range r.servers {
		if now.After(srv.expiresAt) {
			delete(r.servers, usn)

			continue
		}

		out = append(out, MediaServer{
			USN:          srv.USN,
			FriendlyName: srv.FriendlyName,
			Location:     srv.Location,
			controlURL:   "", // the API lists servers; Browse looks the control URL up itself
			expiresAt:    time.Time{},
		})
	}

	return out
}

// advert is one parsed M-SEARCH reply worth following up on.
type advert struct {
	usn      string
	location string
	ttl      time.Duration
}

// searchRound sends one M-SEARCH round, collects replies for searchWindow, and only then
// fetches the device descriptions of servers it has not seen before.
//
// The two phases must stay separate. A description fetch is an HTTP round trip to a
// possibly-slow device, and doing it inline would burn the read window: the second
// and later servers on the LAN would answer into a socket nobody is reading and be
// dropped, so a multi-server network would only ever discover one of them.
func (r *Registry) searchRound(ctx context.Context, log func(string, ...any)) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		if log != nil {
			log("ssdp ms-search listen", "err", err)
		}

		return
	}

	dst, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		_ = conn.Close()

		if log != nil {
			log("ssdp resolve group", "addr", ssdpAddr, "err", err)
		}

		return
	}

	msg := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: " + ssdpAddr + "\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: " + strconv.Itoa(searchMX) + "\r\n" +
		"ST: " + mediaServerURN + "\r\n\r\n"

	_, err = conn.WriteToUDP([]byte(msg), dst)
	if err != nil {
		_ = conn.Close()

		if log != nil {
			log("ssdp ms-search send", "err", err)
		}

		return
	}

	// Phase 1: collect. Closing the socket on ctx cancellation unblocks the read
	// immediately instead of waiting out the window on shutdown.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(searchWindow))

	adverts := collectAdverts(conn)

	if !stop() {
		return // ctx cancelled: the socket was closed under us
	}

	_ = conn.Close()

	// Phase 2: refresh what we know, fetch descriptions for what we don't.
	r.describeAll(ctx, r.unknown(adverts, time.Now()), log)
}

// collectAdverts reads M-SEARCH replies until the socket's deadline or its close, and
// keeps the first maxAdverts usable ones, one per USN.
func collectAdverts(conn *net.UDPConn) []advert {
	var adverts []advert

	seen := map[string]bool{}

	buf := make([]byte, ssdpReplyMax)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return adverts // window expired, or ctx cancelled and the socket closed
		}

		reply, ok := parseAdvert(string(buf[:n]))
		if !ok || seen[reply.usn] || len(adverts) >= maxAdverts || !sameHost(reply.location, src.IP) {
			continue
		}

		seen[reply.usn] = true
		adverts = append(adverts, reply)
	}
}

// describeAll fetches the descriptions of fresh, descFetchLimit at a time.
func (r *Registry) describeAll(ctx context.Context, fresh []advert, log func(string, ...any)) {
	sem := make(chan struct{}, descFetchLimit)

	var wg sync.WaitGroup
	for _, reply := range fresh {
		wg.Add(1)
		safego.Go("upnp.describe", func() {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			r.describe(ctx, reply, log)
		})
	}

	wg.Wait()
}

// describe fetches one advert's description and files the device as a server or, when
// it answered with something that is no media server, as rejected.
func (r *Registry) describe(ctx context.Context, reply advert, log func(string, ...any)) {
	srv, err := r.fetchDesc(ctx, reply.location, reply.usn, reply.ttl)
	r.mu.Lock()
	defer r.mu.Unlock()

	switch {
	case err == nil:
		r.store(srv, time.Now())
		delete(r.told, reply.usn)

		return
	case !errors.Is(err, errNoAnswer):
		r.reject(reply.usn, time.Now().Add(reply.ttl))

		said := reply.location + " " + err.Error()
		if r.told[reply.usn] == said {
			return
		}

		r.told[reply.usn] = said
	default:
		// No answer says nothing about the device: the next round asks again.
	}

	if log != nil {
		log("upnp desc fetch", "location", reply.location, "err", err)
	}
}

// unknown refreshes the expiry of servers already known and returns the adverts
// that still need a description fetch: new ones, ones that moved to another LOCATION
// (a new DHCP lease, a restarted server: the old control URL is dead), and
// rejected ones whose TTL ran out.
func (r *Registry) unknown(adverts []advert, now time.Time) []advert {
	var fresh []advert

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, reply := range adverts {
		if srv, ok := r.servers[reply.usn]; ok && srv.Location == reply.location {
			srv.expiresAt = now.Add(reply.ttl)

			continue
		}

		if until, ok := r.rejected[reply.usn]; ok {
			if now.Before(until) {
				continue
			}

			delete(r.rejected, reply.usn)
		}

		fresh = append(fresh, reply)
	}

	return fresh
}

// parseAdvert extracts the fields we need from one SSDP HTTP response datagram.
// ok is false for anything that is not a usable MediaServer:1 reply.
func parseAdvert(msg string) (advert, bool) {
	var none advert

	if !strings.HasPrefix(msg, "HTTP/1.1 200") || ssdpHeaderVal(msg, "ST") != mediaServerURN {
		return none, false
	}

	reply := advert{
		usn:      ssdpHeaderVal(msg, "USN"),
		location: ssdpHeaderVal(msg, "LOCATION"),
		ttl:      maxAge(ssdpHeaderVal(msg, "CACHE-CONTROL")),
	}
	if reply.location == "" || reply.usn == "" {
		return none, false
	}

	return reply, true
}

// maxAge reads the max-age of a CACHE-CONTROL value (also "max-age = NNN", with spaces
// per RFC), capped at maxTTL; defaultTTL when there is none.
func maxAge(cacheControl string) time.Duration {
	ttl := defaultTTL

	for part := range strings.SplitSeq(cacheControl, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.TrimSpace(key) != "max-age" {
			continue
		}

		seconds, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || seconds <= 0 {
			continue
		}

		ttl = maxTTL // checked before multiplying: no overflow
		if seconds < int(maxTTL/time.Second) {
			ttl = time.Duration(seconds) * time.Second
		}
	}

	return ttl
}

// ssdpHeaderVal extracts a header value from an SSDP/HTTP-like message.
func ssdpHeaderVal(msg, key string) string {
	prefix := strings.ToLower(key) + ":"
	for line := range strings.SplitSeq(msg, "\r\n") {
		if len(line) > len(prefix) && strings.ToLower(line[:len(prefix)]) == prefix {
			return strings.TrimSpace(line[len(prefix):])
		}
	}

	return ""
}

// fetchDesc retrieves the device description XML and locates the ContentDirectory controlURL.
func (r *Registry) fetchDesc(ctx context.Context, location, usn string, ttl time.Duration) (*MediaServer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("description request: %w", err)
	}

	resp, err := safehttp.Do(ctx, r.client, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNoAnswer, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", errNoAnswer, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, descBodyMax))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNoAnswer, err)
	}

	ctrlURL, name, err := parseDesc(location, body)
	if err != nil {
		return nil, err
	}

	return &MediaServer{
		USN:          usn,
		FriendlyName: name,
		Location:     location,
		controlURL:   ctrlURL,
		expiresAt:    time.Now().Add(ttl),
	}, nil
}

// absoluteURL makes a description's control URL absolute: a relative one resolves against
// the URLBase when there is one, else against the location. The host check is the caller's.
func absoluteURL(location, urlBase, ctrlPath string) (string, error) {
	if strings.HasPrefix(ctrlPath, "http://") || strings.HasPrefix(ctrlPath, "https://") {
		return ctrlPath, nil
	}

	base, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("bad location URL: %w", err)
	}

	baseRef, baseErr := url.Parse(strings.TrimSpace(urlBase))
	if baseErr == nil && urlBase != "" {
		base = base.ResolveReference(baseRef)
	}

	ref, err := url.Parse(ctrlPath)
	if err != nil {
		return "", fmt.Errorf("bad controlURL: %w", err)
	}

	return base.ResolveReference(ref).String(), nil
}

// parseDesc reads a device description fetched from location: the ContentDirectory's
// control URL, made absolute, and the friendly name. The control URL must be on the host
// that served the description.
func parseDesc(location string, body []byte) (string, string, error) {
	var desc deviceDesc

	err := xml.Unmarshal(body, &desc)
	if err != nil {
		return "", "", fmt.Errorf("desc xml: %w", err)
	}

	var ctrlPath string

	for _, svc := range desc.Device.ServiceList.Services {
		if svc.ServiceType == cdServiceURN {
			ctrlPath = svc.ControlURL

			break
		}
	}

	if ctrlPath == "" {
		return "", "", errNoContentDirectory
	}

	ctrlURL, err := absoluteURL(location, desc.URLBase, ctrlPath)
	if err != nil {
		return "", "", err
	}

	// The SOAP Browse goes to the host that served the description and nowhere else:
	// an absolute controlURL could point it at the speaker's own loopback services.
	ctrl, err := url.Parse(ctrlURL)
	if err != nil || ctrl.Hostname() != hostOf(location) || (ctrl.Scheme != "http" && ctrl.Scheme != schemeHTTPS) {
		return "", "", fmt.Errorf("controlURL %q %w", ctrlURL, errForeignControlURL)
	}

	if len(ctrlURL) > maxControlURL {
		return "", "", errControlURLTooLong
	}

	name := desc.Device.FriendlyName
	if len(name) > maxNameLen {
		name = strings.ToValidUTF8(name[:maxNameLen], "")
	}

	return ctrlURL, name, nil
}

// store files a described server. At maxServers it drops expired entries, then the one
// expiring soonest, so a flood cannot grow the table and a refresh never evicts.
// Caller holds r.mu.
func (r *Registry) store(srv *MediaServer, now time.Time) {
	if _, known := r.servers[srv.USN]; !known && len(r.servers) >= maxServers {
		var oldest *MediaServer
		for usn, s := range r.servers {
			if now.After(s.expiresAt) {
				delete(r.servers, usn)
			} else if oldest == nil || s.expiresAt.Before(oldest.expiresAt) ||
				(s.expiresAt.Equal(oldest.expiresAt) && usn < oldest.USN) {
				oldest = s
			}
		}

		if len(r.servers) >= maxServers {
			delete(r.servers, oldest.USN)
		}
	}

	r.servers[srv.USN] = srv
}

// reject remembers a device whose description failed. Caller holds r.mu.
func (r *Registry) reject(usn string, until time.Time) {
	if len(r.rejected) >= maxRejected {
		now := time.Now()
		for k, v := range r.rejected {
			if now.After(v) {
				delete(r.rejected, k)
			}
		}

		if len(r.rejected) >= maxRejected {
			return
		}
	}

	r.rejected[usn] = until
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	return u.Hostname()
}

// sameHost reports whether raw is an http(s) URL on ip itself, given as an address,
// and ip is not loopback: SSDP replies are unauthenticated, so a LOCATION may only
// name the device that sent it.
func sameHost(raw string, sender net.IP) bool {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != schemeHTTPS) || sender == nil || sender.IsLoopback() {
		return false
	}

	host := net.ParseIP(parsed.Hostname())

	return host != nil && host.Equal(sender)
}

// deviceDesc is the part of a UPnP device description parseDesc reads.
type deviceDesc struct {
	URLBase string     `xml:"URLBase"`
	Device  descDevice `xml:"device"`
}

// descDevice is a description's root device.
type descDevice struct {
	FriendlyName string          `xml:"friendlyName"`
	ServiceList  descServiceList `xml:"serviceList"`
}

// descServiceList is a device's serviceList.
type descServiceList struct {
	Services []descService `xml:"service"`
}

// descService is one service of a device.
type descService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}
