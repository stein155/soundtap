// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeDIDL is a minimal DIDL-Lite with one container and one audio item.
const fakeDIDL = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"` +
	` xmlns:dc="http://purl.org/dc/elements/1.1/"` +
	` xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` +
	`<container id="A:MUSIC" parentID="0" restricted="1">` +
	`<dc:title>Music</dc:title>` +
	`<upnp:class>object.container</upnp:class>` +
	`</container>` +
	`<item id="I:1" parentID="0" restricted="1">` +
	`<dc:title>My Track</dc:title>` +
	`<upnp:class>object.item.audioItem.musicTrack</upnp:class>` +
	`<res protocolInfo="http-get:*:audio/mpeg:*">http://192.168.1.50:8200/track/1.mp3</res>` +
	`</item>` +
	`</DIDL-Lite>`

// fakeBrowseResp builds a SOAP Browse response wrapping an escaped DIDL-Lite document.
func fakeBrowseResp(didl string) string {
	escaped := strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;",
	).Replace(didl)

	return `<?xml version="1.0" encoding="utf-8"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">` +
		`<s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">` +
		`<Result>` + escaped + `</Result>` +
		`<NumberReturned>2</NumberReturned><TotalMatches>2</TotalMatches><UpdateID>1</UpdateID>` +
		`</u:BrowseResponse></s:Body></s:Envelope>`
}

func fakeDescXML(controlPath string) string {
	return `<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0">` +
		`<device><friendlyName>TestServer</friendlyName>` +
		`<serviceList><service>` +
		`<serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType>` +
		`<controlURL>` + controlPath + `</controlURL>` +
		`</service></serviceList></device></root>`
}

func TestParseDIDL(t *testing.T) {
	t.Parallel()

	items, err := parseDIDL([]byte(fakeDIDL))
	if err != nil {
		t.Fatalf("parseDIDL: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d: %v", len(items), items)
	}
	// containers first
	if items[0].ID != "A:MUSIC" || items[0].Title != "Music" || items[0].Class != "object.container" {
		t.Errorf("container = %+v", items[0])
	}

	if items[1].ID != "I:1" || items[1].Title != "My Track" || items[1].URL != "http://192.168.1.50:8200/track/1.mp3" {
		t.Errorf("item = %+v", items[1])
	}
}

func TestParseBrowseResponse(t *testing.T) {
	t.Parallel()

	resp := fakeBrowseResp(fakeDIDL)

	items, err := parseBrowseResponse([]byte(resp))
	if err != nil {
		t.Fatalf("parseBrowseResponse: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("want 2, got %d", len(items))
	}
}

func TestBrowseURL(t *testing.T) {
	t.Parallel()

	var gotAction, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}

		gotAction = r.Header.Get("SoapAction")
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])

		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		fmt.Fprint(w, fakeBrowseResp(fakeDIDL))
	}))
	defer srv.Close()

	items, err := browseURL(context.Background(), srv.Client(), srv.URL+"/cd/control", "0")
	if err != nil {
		t.Fatalf("browseURL: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}

	if !strings.Contains(gotAction, "Browse") {
		t.Errorf("SOAPACTION missing Browse: %q", gotAction)
	}

	if !strings.Contains(gotBody, "<ObjectID>0</ObjectID>") {
		t.Errorf("body missing ObjectID: %q", gotBody)
	}

	if !strings.Contains(gotBody, fmt.Sprintf("<RequestedCount>%d</RequestedCount>", browseMax)) {
		t.Errorf("body missing RequestedCount: %q", gotBody)
	}
}

func TestFetchDesc(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/desc.xml":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, fakeDescXML("/cd/control"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	reg := NewRegistry()

	server, err := reg.fetchDesc(context.Background(), srv.URL+"/desc.xml", "uuid:test::"+mediaServerURN, defaultTTL)
	if err != nil {
		t.Fatalf("fetchDesc: %v", err)
	}

	if server.FriendlyName != "TestServer" {
		t.Errorf("FriendlyName = %q", server.FriendlyName)
	}

	wantCtrl := srv.URL + "/cd/control"
	if server.controlURL != wantCtrl {
		t.Errorf("controlURL = %q, want %q", server.controlURL, wantCtrl)
	}
}

// A relative controlURL resolves against URLBase when the description gives one.
func TestFetchDescUsesURLBase(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		desc := fakeDescXML("ContentDirectory/control")
		fmt.Fprint(w, strings.Replace(desc, "<device>", "<URLBase>/services/</URLBase><device>", 1))
	}))
	defer srv.Close()

	server, err := NewRegistry().fetchDesc(
		context.Background(), srv.URL+"/device/desc.xml", "uuid:test::"+mediaServerURN, defaultTTL)
	if err != nil {
		t.Fatalf("fetchDesc: %v", err)
	}

	if want := srv.URL + "/services/ContentDirectory/control"; server.controlURL != want {
		t.Errorf("controlURL = %q, want %q", server.controlURL, want)
	}
}

func TestRegistryBrowse(t *testing.T) {
	t.Parallel()

	// One fake server: serves device desc + SOAP Browse
	fakeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/desc.xml":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, fakeDescXML("/cd/control"))
		case "/cd/control":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, fakeBrowseResp(fakeDIDL))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fakeSrv.Close()

	reg := NewRegistry()
	usn := "uuid:fake-server::urn:schemas-upnp-org:device:MediaServer:1"

	// Inject a server directly (avoids SSDP in unit tests)
	server, err := reg.fetchDesc(context.Background(), fakeSrv.URL+"/desc.xml", usn, defaultTTL)
	if err != nil {
		t.Fatalf("fetchDesc: %v", err)
	}

	reg.mu.Lock()
	reg.servers[usn] = server
	reg.mu.Unlock()

	// Servers() should return it
	servers := reg.Servers()
	if len(servers) != 1 || servers[0].USN != usn {
		t.Fatalf("Servers() = %v", servers)
	}

	// Browse root
	items, err := reg.Browse(context.Background(), usn, "0")
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("Browse: want 2 items, got %d: %v", len(items), items)
	}
}

// A round cut short must not pass for a finished one: whoever waited behind it would
// take the cached list without a search of its own.
func TestCanceledRoundDoesNotCount(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range 50 { // Search picks at random between the free slot and the cancelled context
		reg.Search(ctx, nil)
	}

	if !reg.lastRound.IsZero() {
		t.Fatal("a cancelled round was recorded as finished")
	}
}

func TestRegistryTTLPrune(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	// Insert an already-expired server
	reg.mu.Lock()
	reg.servers["uuid:expired"] = &MediaServer{
		USN:          "uuid:expired",
		FriendlyName: "",
		Location:     "",
		controlURL:   "",
		expiresAt:    time.Now().Add(-time.Second),
	}
	reg.mu.Unlock()

	servers := reg.Servers()
	if len(servers) != 0 {
		t.Fatalf("expected expired server pruned, got %v", servers)
	}
}

// TestParseDIDLPicksAudioResource covers an item advertising several <res>: an
// unplayable internal transport, a thumbnail, and the http-get audio stream. Binding
// <res> to a single string used to keep whichever element came last.
func TestParseDIDLPicksAudioResource(t *testing.T) {
	t.Parallel()

	didl := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"` +
		` xmlns:dc="http://purl.org/dc/elements/1.1/"` +
		` xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` +
		`<item id="I:9" parentID="0" restricted="1">` +
		`<dc:title>Multi</dc:title>` +
		`<upnp:class>object.item.audioItem.musicTrack</upnp:class>` +
		`<res protocolInfo="rtsp-rtp-udp:*:audio/L16:*">rtsp://192.168.1.50/9</res>` +
		`<res protocolInfo="http-get:*:audio/flac:*">http://192.168.1.50:8200/9.flac</res>` +
		`<res protocolInfo="http-get:*:image/jpeg:*">http://192.168.1.50:8200/9.jpg</res>` +
		`</item></DIDL-Lite>`

	items, err := parseDIDL([]byte(didl))
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}

	if want := "http://192.168.1.50:8200/9.flac"; items[0].URL != want {
		t.Errorf("URL = %q, want the http-get audio resource %q", items[0].URL, want)
	}
}

// TestBrowseURLRejectsOversizedResponse proves an over-cap document is reported as
// oversized rather than truncated into a confusing "no <Result>" parse failure.
func TestBrowseURLRejectsOversizedResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		// A valid envelope whose DIDL padding pushes it past soapBodyMax.
		padding := strings.Repeat("x", soapBodyMax+1024)
		_, _ = w.Write([]byte(fakeBrowseResp(padding)))
	}))
	defer srv.Close()

	_, err := browseURL(context.Background(), srv.Client(), srv.URL, "0")
	if err == nil {
		t.Fatal("want an error for an oversized Browse response, got nil")
	}

	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want it to name the size limit", err)
	}
}

// TestBrowseURLAcceptsLargeFolder is the regression guard for the 4 KiB cap: a
// realistic 32-item folder is tens of KiB and must parse.
func TestBrowseURLAcceptsLargeFolder(t *testing.T) {
	t.Parallel()

	var didl strings.Builder
	didl.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"` +
		` xmlns:dc="http://purl.org/dc/elements/1.1/"` +
		` xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">`)

	for index := range browseMax {
		fmt.Fprintf(&didl, `<item id="I:%d" parentID="0" restricted="1">`+
			`<dc:title>Track number %d with a realistically long title</dc:title>`+
			`<upnp:class>object.item.audioItem.musicTrack</upnp:class>`+
			`<res protocolInfo="http-get:*:audio/mpeg:DLNA.ORG_PN=MP3;DLNA.ORG_OP=01;`+
			`DLNA.ORG_FLAGS=01700000000000000000000000000000">`+
			`http://192.168.1.50:8200/MediaItems/%d.mp3</res></item>`, index, index, index)
	}

	didl.WriteString(`</DIDL-Lite>`)

	body := fakeBrowseResp(didl.String())
	if len(body) < 8<<10 {
		t.Fatalf("test fixture is only %d bytes; it must exceed the old 4 KiB cap", len(body))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	items, err := browseURL(context.Background(), srv.Client(), srv.URL, "0")
	if err != nil {
		t.Fatalf("browseURL on a %d-byte response: %v", len(body), err)
	}

	if len(items) != browseMax {
		t.Errorf("got %d items, want %d", len(items), browseMax)
	}
}

// A 33-track folder comes back whole, the second page asked from index 32.
func TestBrowseURLPagesToTotalMatches(t *testing.T) {
	t.Parallel()

	var starts []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		start := regexp.MustCompile(`<StartingIndex>(\d+)<`).FindSubmatch(body)
		starts = append(starts, string(start[1]))
		from, _ := strconv.Atoi(string(start[1]))

		var didl strings.Builder
		didl.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" ` +
			`xmlns:dc="http://purl.org/dc/elements/1.1/">`)

		for i := from; i < min(from+browseMax, 33); i++ {
			fmt.Fprintf(&didl, `<item id="I:%d"><dc:title>T%d</dc:title></item>`, i, i)
		}

		didl.WriteString(`</DIDL-Lite>`)
		resp := strings.Replace(fakeBrowseResp(didl.String()), "<TotalMatches>2<", "<TotalMatches>33<", 1)
		_, _ = w.Write([]byte(resp))
	}))
	defer srv.Close()

	items, err := browseURL(context.Background(), srv.Client(), srv.URL, "0")
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 33 || items[32].ID != "I:32" || strings.Join(starts, ",") != "0,32" {
		t.Errorf("got %d items, requests from %v", len(items), starts)
	}
}

func TestBrowseURLRejectsHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such object", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := browseURL(context.Background(), srv.Client(), srv.URL, "0")
	if err == nil {
		t.Fatal("want an error for a 500 Browse response, got nil")
	}
}

func TestParseAdvert(t *testing.T) {
	t.Parallel()

	const validReply = "HTTP/1.1 200 OK\r\n" +
		"CACHE-CONTROL: max-age = 1800\r\n" +
		"ST: urn:schemas-upnp-org:device:MediaServer:1\r\n" +
		"USN: uuid:abc::urn:schemas-upnp-org:device:MediaServer:1\r\n" +
		"LOCATION: http://192.168.1.50:8200/rootDesc.xml\r\n\r\n"

	reply, valid := parseAdvert(validReply)
	if !valid {
		t.Fatal("want a valid advert")
	}

	if reply.ttl != 1800*time.Second {
		t.Errorf("ttl = %v, want 30m from max-age", reply.ttl)
	}

	if reply.location != "http://192.168.1.50:8200/rootDesc.xml" {
		t.Errorf("location = %q", reply.location)
	}

	for name, msg := range map[string]string{
		"not 200":     strings.Replace(validReply, "200 OK", "404 Not Found", 1),
		"wrong ST":    strings.Replace(validReply, mediaServerURN, "urn:schemas-upnp-org:device:Printer:1", 1),
		"no location": strings.Replace(validReply, "LOCATION: http://192.168.1.50:8200/rootDesc.xml\r\n", "", 1),
		"no usn": strings.Replace(
			validReply, "USN: uuid:abc::urn:schemas-upnp-org:device:MediaServer:1\r\n", "", 1),
	} {
		if _, valid := parseAdvert(msg); valid {
			t.Errorf("%s: want rejected, got accepted", name)
		}
	}

	// No CACHE-CONTROL falls back to defaultTTL.
	bare := strings.Replace(validReply, "CACHE-CONTROL: max-age = 1800\r\n", "", 1)
	if a, _ := parseAdvert(bare); a.ttl != defaultTTL {
		t.Errorf("ttl = %v, want defaultTTL %v", a.ttl, defaultTTL)
	}
}

// TestSearchStopsOnCancelledContext proves Discover returns promptly on shutdown
// instead of waiting out the full search window.
func TestSearchStopsOnCancelledContext(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { defer close(done); r.Discover(ctx, nil) }()

	cancel()

	select {
	case <-done:
	case <-time.After(searchWindow):
		t.Fatal("Discover did not return promptly after cancellation")
	}
}

func TestRejectedServerIsNotRefetchedUntilItsAdvertExpires(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	now := time.Now()
	sonos := advert{usn: "uuid:RINCON", location: "http://192.0.2.1:1400/desc.xml", ttl: time.Minute}

	reg.rejected[sonos.usn] = now.Add(sonos.ttl)
	if got := reg.unknown([]advert{sonos}, now.Add(30*time.Second)); len(got) != 0 {
		t.Fatalf("rejected server refetched within its TTL: %v", got)
	}

	if got := reg.unknown([]advert{sonos}, now.Add(2*time.Minute)); len(got) != 1 {
		t.Fatalf("rejected server not retried after its TTL: %v", got)
	}
}

// One failed fetch must not hide a media server until its advert expires: on a NAS that
// is half an hour. A device that answers and is no media server stays rejected.
func TestOnlyADeviceThatAnsweredIsRejected(t *testing.T) {
	t.Parallel()

	waking := true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sonos.xml":
			fmt.Fprint(w, `<root><device><friendlyName>Sonos</friendlyName></device></root>`)
		case waking:
			http.Error(w, "starting", http.StatusServiceUnavailable)
		default:
			fmt.Fprint(w, fakeDescXML("/cd/control"))
		}
	}))
	defer srv.Close()

	reg := NewRegistry()
	nas := advert{usn: "uuid:nas", location: srv.URL + "/desc.xml", ttl: time.Hour}
	reg.describe(context.Background(), nas, nil)

	if got := reg.unknown([]advert{nas}, time.Now()); len(got) != 1 {
		t.Fatalf("a server that failed to answer is not retried: %v", got)
	}

	waking = false

	reg.describe(context.Background(), nas, nil)

	if len(reg.Servers()) != 1 {
		t.Fatalf("server not found on the retry: %v", reg.Servers())
	}

	sonos := advert{usn: "uuid:sonos", location: srv.URL + "/sonos.xml", ttl: time.Hour}
	reg.describe(context.Background(), sonos, nil)

	if got := reg.unknown([]advert{sonos}, time.Now()); len(got) != 0 {
		t.Fatalf("device without ContentDirectory refetched: %v", got)
	}
}

// A device that is no media server is fetched again each time its advert expires; the log
// says so once, not every half hour, until the answer changes.
func TestARejectedDeviceIsLoggedOnce(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<root><device><friendlyName>Sonos</friendlyName></device></root>`)
	}))
	defer srv.Close()

	var lines []string

	logged := func(msg string, _ ...any) { lines = append(lines, msg) }
	reg := NewRegistry()
	sonos := advert{usn: "uuid:sonos", location: srv.URL + "/sonos.xml", ttl: time.Hour}

	for range 3 {
		reg.describe(context.Background(), sonos, logged)
	}

	if len(lines) != 1 {
		t.Fatalf("logged %d times, want once: %v", len(lines), lines)
	}

	moved := advert{usn: "uuid:sonos", location: srv.URL + "/sonos.xml?moved", ttl: time.Hour}
	reg.describe(context.Background(), moved, logged)

	if len(lines) != 2 {
		t.Fatalf("a new LOCATION is not logged: %v", lines)
	}
}

// Requests that queue behind a round take its result instead of each running their own.
func TestSearchSharesTheRoundItWaitedFor(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.rounds <- struct{}{}

	done := make(chan struct{})
	go func() { defer close(done); reg.Search(context.Background(), nil) }()

	time.Sleep(50 * time.Millisecond)

	reg.lastRound = time.Now()
	<-reg.rounds

	select {
	case <-done:
	case <-time.After(searchWindow / 2):
		t.Fatal("Search ran its own round after waiting for one")
	}
}

// A known server that adverts a new LOCATION is fetched again, not refreshed with its
// old, dead control URL.
func TestMovedServerIsRefetched(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	now := time.Now()
	reg.servers["uuid:nas"] = &MediaServer{
		USN: "", FriendlyName: "", Location: "http://192.168.1.50:8200/desc.xml", controlURL: "", expiresAt: now,
	}

	same := advert{usn: "uuid:nas", location: "http://192.168.1.50:8200/desc.xml", ttl: time.Minute}
	if got := reg.unknown([]advert{same}, now); len(got) != 0 {
		t.Fatalf("unchanged server refetched: %v", got)
	}

	moved := advert{usn: "uuid:nas", location: "http://192.168.1.60:8200/desc.xml", ttl: time.Minute}
	if got := reg.unknown([]advert{moved}, now); len(got) != 1 {
		t.Fatalf("moved server not refetched: %v", got)
	}
}

// SSDP is unauthenticated: a LOCATION may only name the device that sent it, never
// loopback, and the control URL stays on the description's host.
func TestDeviceURLsStayOnTheAdvertisingHost(t *testing.T) {
	t.Parallel()

	src := net.IPv4(192, 168, 1, 50)
	if !sameHost("http://192.168.1.50:8200/desc.xml", src) {
		t.Fatal("a device's own LOCATION was refused")
	}

	for _, loc := range []string{
		"http://127.0.0.1:4071/desc.xml", "http://192.168.1.51/desc.xml",
		"file:///etc/passwd", "http://nas.local/desc.xml",
	} {
		if sameHost(loc, src) {
			t.Errorf("LOCATION %q accepted from %v", loc, src)
		}
	}

	if sameHost("http://127.0.0.1/desc.xml", net.IPv4(127, 0, 0, 1)) {
		t.Error("a loopback LOCATION was accepted")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, fakeDescXML("http://192.0.2.9:4071/load"))
	}))
	defer srv.Close()

	_, err := NewRegistry().fetchDesc(context.Background(), srv.URL+"/desc.xml", "uuid:x", defaultTTL)
	if err == nil {
		t.Fatal("a controlURL on another host was accepted")
	}
}

// Namespace declarations and attributes on Result/TotalMatches are valid SOAP.
func TestReadBrowseResponseAttributes(t *testing.T) {
	t.Parallel()

	resp := `<?xml version="1.0" encoding="ISO-8859-1"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>` +
		`<u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">` +
		`<Result xmlns="urn:schemas-upnp-org:service:ContentDirectory:1">&lt;DIDL-Lite/&gt;</Result>` +
		`<NumberReturned>0</NumberReturned><TotalMatches xmlns:dt="urn:x" dt:dt="ui4">120</TotalMatches>` +
		`</u:BrowseResponse></s:Body></s:Envelope>`

	reg, err := readBrowseResponse([]byte(resp))
	if err != nil {
		t.Fatal(err)
	}

	if reg.result != "<DIDL-Lite/>" || reg.total != 120 {
		t.Fatalf("got %+v", reg)
	}

	_, err = readBrowseResponse([]byte(`<Envelope/>`))
	if err == nil {
		t.Fatal("missing Result accepted")
	}
}

// A search that outlives its budget behind a running round must not hold up the request.
func TestSearchStopsWaitingWhenTheCallerGivesUp(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.rounds <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	done := make(chan struct{})

	go func() { reg.Search(ctx, nil); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("search still waits for the round after its context ended")
	}
}

// TestHostileDevicesCannotGrowTheRegistry replays an announcement flood: full rounds of fresh USNs with
// huge names and year-long TTLs, and nobody calling Servers() in between.
func TestHostileDevicesCannotGrowTheRegistry(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, strings.Replace(fakeDescXML("/control"), "TestServer", strings.Repeat("A", 60000), 1))
	}))
	defer srv.Close()

	reg := NewRegistry()

	for round := range 5 {
		adverts := make([]advert, 0, maxAdverts)
		for i := range maxAdverts {
			adverts = append(adverts, advert{
				usn: fmt.Sprintf("uuid:%d-%d", round, i), location: srv.URL, ttl: 365 * 24 * time.Hour,
			})
		}

		for _, a := range reg.unknown(adverts, time.Now()) {
			reg.describe(context.Background(), a, nil)
		}
	}

	reg.mu.Lock()
	n := len(reg.servers)
	reg.mu.Unlock()

	if n != maxServers {
		t.Fatalf("retained %d servers, want cap %d", n, maxServers)
	}

	for _, s := range reg.Servers() {
		if len(s.FriendlyName) > maxNameLen {
			t.Fatalf("name kept %d bytes", len(s.FriendlyName))
		}
	}

	// An expired entry makes room before a live one is evicted.
	reg.mu.Lock()
	for _, s := range reg.servers {
		s.expiresAt = time.Now().Add(-time.Second)

		break
	}

	reg.store(&MediaServer{
		USN: "uuid:new", FriendlyName: "", Location: "", controlURL: "", expiresAt: time.Now().Add(time.Hour),
	}, time.Now())
	_, newKept := reg.servers["uuid:new"]
	size := len(reg.servers)
	reg.mu.Unlock()

	if !newKept || size != maxServers {
		t.Fatalf("new server kept=%v, size=%d", newKept, size)
	}

	// max-age is clamped, including values that overflow a Duration.
	for _, age := range []string{"31536000", "99999999999999"} {
		a, _ := parseAdvert("HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=" + age + "\r\nST: " + mediaServerURN +
			"\r\nUSN: u\r\nLOCATION: http://192.0.2.1/d\r\n\r\n")
		if a.ttl != maxTTL {
			t.Errorf("max-age=%s: ttl %v, want %v", age, a.ttl, maxTTL)
		}
	}
}

// TestBrowseURLBoundsRetainedBytes guards the retained size: every page is under soapBodyMax and
// the item count is under browseTotalMax, yet the titles together would be 64 MiB.
func TestBrowseURLBoundsRetainedBytes(t *testing.T) {
	t.Parallel()

	didl := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"` +
		` xmlns:dc="http://purl.org/dc/elements/1.1/"><container id="x"><dc:title>` +
		strings.Repeat("x", 512<<10) + `</dc:title></container></DIDL-Lite>`

	body := strings.Replace(
		fakeBrowseResp(didl), "<TotalMatches>2</TotalMatches>", "<TotalMatches>128</TotalMatches>", 1)
	if !strings.Contains(body, "<TotalMatches>128</TotalMatches>") {
		t.Fatal("fixture does not advertise 128 matches")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	items, err := browseURL(context.Background(), srv.Client(), srv.URL, "0")
	if err != nil {
		t.Fatalf("browseURL: %v", err)
	}

	retained := 0
	for _, it := range items {
		retained += len(it.ID) + len(it.Title) + len(it.Class)
		if len(it.Title) > fieldMax {
			t.Fatalf("title kept %d bytes, cap is %d", len(it.Title), fieldMax)
		}
	}

	if retained > browseBytesMax {
		t.Errorf("retained %d bytes, budget is %d", retained, browseBytesMax)
	}
}

func TestParseDIDLKeepsArtistAndCover(t *testing.T) {
	t.Parallel()

	items, err := parseDIDL([]byte(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"
		xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">
		<item id="1"><dc:title>a</dc:title><upnp:artist>Band</upnp:artist><upnp:album>Record</upnp:album>` +
		`<upnp:albumArtURI>http://n/c.jpg</upnp:albumArtURI></item>
		<item id="2"><dc:title>b</dc:title><dc:creator>Solo</dc:creator>` +
		`<upnp:albumArtURI>file:///etc/x</upnp:albumArtURI></item>
	</DIDL-Lite>`))
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}

	if items[0].Artist != "Band" || items[0].Album != "Record" || items[0].Art != "http://n/c.jpg" {
		t.Errorf("first = %+v", items[0])
	}

	if items[1].Artist != "Solo" || items[1].Art != "" {
		t.Errorf("second = %+v: creator stands in, a file URL does not", items[1])
	}
}
