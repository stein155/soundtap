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
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	uri, title string
	meta       Meta
	stopped    bool
	state      string
	stopErr    error
	volume     int
	muted      bool
	at         time.Duration
}

// Errors the fakes answer with.
var (
	errBackendUnavailable = errors.New("backend unavailable")
	errNoSuchHost         = errors.New("no such host")
	errNoDevice           = errors.New("setsockopt: no such device")
)

// newFakeBackend is a backend in state at volume that has played nothing yet.
func newFakeBackend(state string, volume int) *fakeBackend {
	var none Meta

	return &fakeBackend{
		uri: "", title: "", meta: none, stopped: false, state: state, stopErr: nil, volume: volume, muted: false, at: 0,
	}
}

// newTestRenderer is a renderer on backend with no identity and nothing played.
func newTestRenderer(backend Backend) *Renderer {
	var none Meta

	return &Renderer{
		UDN: "", Name: "", IP: "", Port: "", Backend: backend, SubsFile: "", OnPosition: nil,
		mu: sync.Mutex{}, uri: "", meta: none, next: "", nextMeta: none, armed: false,
		elapsed: 0, lastTick: time.Time{},
		events: eventer{mu: sync.Mutex{}, subs: nil, file: ""},
	}
}

// newNamedRenderer is newTestRenderer with the identity a description and an SSDP
// answer carry.
func newNamedRenderer(backend Backend, name string) *Renderer {
	r := newTestRenderer(backend)
	r.UDN, r.Name, r.IP, r.Port = "uuid:abc", name, "1.2.3.4", "80"

	return r
}

func (f *fakeBackend) PlayURL(_ context.Context, uri string, m Meta, at time.Duration) error {
	f.uri, f.title, f.meta, f.at, f.state = uri, m.Title, m, at, "PLAYING"

	return nil
}

func (f *fakeBackend) Stop(context.Context) error {
	if f.stopErr != nil {
		return f.stopErr
	}

	f.stopped, f.state = true, "STOPPED"

	return nil
}

func (f *fakeBackend) Pause(context.Context) error {
	if f.stopErr != nil {
		return f.stopErr
	}

	f.state = "PAUSED_PLAYBACK"

	return nil
}
func (f *fakeBackend) TransportState(context.Context) string { return f.state }
func (f *fakeBackend) Volume(context.Context) (int, error)   { return f.volume, nil }
func (f *fakeBackend) SetVolume(_ context.Context, v int) error {
	f.volume, f.muted = v, false // setting a level is asking to hear it, as the speaker does

	return nil
}

func (f *fakeBackend) Muted() bool { return f.muted }

// SetMute keeps the level, as the speaker's mute key does.
func (f *fakeBackend) SetMute(_ context.Context, on bool) error {
	f.muted = on

	return nil
}

func rcReq(action, inner string) *http.Request {
	body := `<s:Envelope><s:Body><u:` + action + ` xmlns:u="` + renderURN + `"><InstanceID>0</InstanceID>` +
		inner + `</u:` + action + `></s:Body></s:Envelope>`
	r := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "/upnp/control/renderingcontrol", strings.NewReader(body))
	r.Header.Set("SoapAction", `"`+renderURN+`#`+action+`"`)

	return r
}

// RenderingControl drives the speaker's own volume and mute.
func TestRenderingControlVolumeAndMute(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend("", 30)
	r := newTestRenderer(backend)

	send := func(action, inner string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.rcControl(w, rcReq(action, inner))

		return w
	}

	rec := send("GetVolume", "<Channel>Master</Channel>")
	if !strings.Contains(rec.Body.String(), "<CurrentVolume>30</CurrentVolume>") {
		t.Fatalf("GetVolume = %s", rec.Body)
	}

	rec = send("SetVolume", "<Channel>Master</Channel><DesiredVolume>55</DesiredVolume>")
	if rec.Code != 200 || backend.volume != 55 {
		t.Fatalf("SetVolume = %d, volume %d", rec.Code, backend.volume)
	}

	for _, bad := range []string{"<DesiredVolume>101</DesiredVolume>", "<DesiredVolume>x</DesiredVolume>", ""} {
		rec = send("SetVolume", "<Channel>Master</Channel>"+bad)
		if rec.Code != 500 || backend.volume != 55 {
			t.Fatalf("SetVolume %q = %d, volume %d", bad, rec.Code, backend.volume)
		}
	}

	rec = send("SetVolume", "<Channel>LF</Channel><DesiredVolume>10</DesiredVolume>")
	if rec.Code != 500 || backend.volume != 55 {
		t.Fatalf("SetVolume on LF = %d, volume %d", rec.Code, backend.volume)
	}

	rec = send("SetMute", "<Channel>Master</Channel><DesiredMute>1</DesiredMute>")
	if rec.Code != 200 || !backend.muted {
		t.Fatalf("SetMute 1 = %d, muted %v", rec.Code, backend.muted)
	}

	// Muted, the volume is still the level an unmute returns to: a controller's slider
	// must not drop to 0, and GetMute says the mute on its own.
	rec = send("GetVolume", "<Channel>Master</Channel>")
	if !strings.Contains(rec.Body.String(), "<CurrentVolume>55</CurrentVolume>") {
		t.Fatalf("muted GetVolume = %s, want 55", rec.Body)
	}

	rec = send("GetMute", "<Channel>Master</Channel>")
	if !strings.Contains(rec.Body.String(), "<CurrentMute>1</CurrentMute>") {
		t.Fatalf("GetMute = %s", rec.Body)
	}

	rec = send("SetMute", "<Channel>Master</Channel><DesiredMute>false</DesiredMute>")
	if rec.Code != 200 || backend.muted {
		t.Fatalf("SetMute false = %d, muted %v", rec.Code, backend.muted)
	}

	rec = send("GetMute", "<Channel>Master</Channel>")
	if !strings.Contains(rec.Body.String(), "<CurrentMute>0</CurrentMute>") {
		t.Fatalf("unmuted GetMute = %s", rec.Body)
	}
}

// Controllers send the URI with a namespace prefix, attributes or as CDATA.
func TestSetAVTransportURIForms(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{
		`<CurrentURI xmlns:dt="urn:schemas-microsoft-com:datatypes" dt:dt="string">` +
			`http://192.168.1.9/a.mp3</CurrentURI>`,
		`<u:CurrentURI>http://192.168.1.9/a.mp3</u:CurrentURI>`,
		`<CurrentURI><![CDATA[http://192.168.1.9/a.mp3]]></CurrentURI>`,
	} {
		backend := newFakeBackend("STOPPED", 0)
		r := newTestRenderer(backend)
		w := httptest.NewRecorder()
		r.avControl(w, soapReq("SetAVTransportURI", `<s:Envelope><s:Body><u:SetAVTransportURI>`+uri+
			`<CurrentURIMetaData></CurrentURIMetaData></u:SetAVTransportURI></s:Body></s:Envelope>`))

		if w.Code != 200 || backend.uri != "http://192.168.1.9/a.mp3" {
			t.Errorf("%s: %d uri=%q", uri, w.Code, backend.uri)
		}
	}
}

func soapReq(action, body string) *http.Request {
	r := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "/upnp/control/avtransport", strings.NewReader(body))
	r.Header.Set("SoapAction", `"`+avTransURN+`#`+action+`"`)

	return r
}

func TestSoapActionParse(t *testing.T) {
	t.Parallel()

	if got := soapAction(`"urn:schemas-upnp-org:service:AVTransport:1#Play"`); got != "Play" {
		t.Fatalf("soapAction=%q", got)
	}
}

func TestSearchTarget(t *testing.T) {
	t.Parallel()

	msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nST: upnp:rootdevice\r\nMAN: \"ssdp:discover\"\r\n\r\n"
	if got := searchTarget(msg); got != "upnp:rootdevice" {
		t.Fatalf("searchTarget=%q", got)
	}
}

func TestMatchTargets(t *testing.T) {
	t.Parallel()

	renderer := newTestRenderer(nil)
	renderer.UDN = "uuid:abc"

	if len(renderer.matchTargets("ssdp:all")) != 6 {
		t.Fatal("ssdp:all should match all advertised targets")
	}

	if got := renderer.matchTargets(rootDevice); len(got) != 1 || got[0] != rootDevice {
		t.Fatalf("device match = %v", got)
	}

	if renderer.matchTargets("urn:nope") != nil {
		t.Fatal("unknown ST should not match")
	}
}

func TestAVTransportSetURIPlayStop(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend("STOPPED", 0)
	renderer := newNamedRenderer(backend, "Test")

	// SetAVTransportURI with a DIDL title and an escaped &.
	body := `<s:Envelope><s:Body><u:SetAVTransportURI>` +
		`<CurrentURI>http://192.168.1.9/song.mp3?a=1&amp;b=2</CurrentURI>` +
		`<CurrentURIMetaData>&lt;DIDL&gt;&lt;dc:title&gt;Rock &amp;amp; Roll&lt;/dc:title&gt;</CurrentURIMetaData>` +
		`</u:SetAVTransportURI></s:Body></s:Envelope>`
	rec := httptest.NewRecorder()
	renderer.avControl(rec, soapReq("SetAVTransportURI", body))

	if rec.Code != 200 {
		t.Fatalf("SetAVTransportURI status=%d", rec.Code)
	}

	if backend.uri != "http://192.168.1.9/song.mp3?a=1&b=2" {
		t.Fatalf("uri = %q (unescape failed?)", backend.uri)
	}

	if backend.title != "Rock & Roll" {
		t.Fatalf("title = %q", backend.title)
	}

	// GetTransportInfo reflects PLAYING.
	rec = httptest.NewRecorder()
	renderer.avControl(rec, soapReq("GetTransportInfo", "<s:Envelope/>"))

	if !strings.Contains(rec.Body.String(), "<CurrentTransportState>PLAYING</CurrentTransportState>") {
		t.Fatalf("transport info body = %s", rec.Body.String())
	}

	// Stop.
	rec = httptest.NewRecorder()
	renderer.avControl(rec, soapReq("Stop", "<s:Envelope/>"))

	if !backend.stopped {
		t.Fatal("Stop did not reach backend")
	}

	// Play after Stop restarts the same URI.
	backend.uri = ""
	rec = httptest.NewRecorder()
	renderer.avControl(rec, soapReq("Play", "<s:Envelope/>"))

	if rec.Code != 200 || backend.state != "PLAYING" || backend.uri != "http://192.168.1.9/song.mp3?a=1&b=2" {
		t.Fatalf("Play after Stop: %d state=%s uri=%q", rec.Code, backend.state, backend.uri)
	}
}

// Play with nothing to play must not claim success.
func TestAVTransportPlayWithoutURIIsAFault(t *testing.T) {
	t.Parallel()

	r := newTestRenderer(newFakeBackend("STOPPED", 0))
	w := httptest.NewRecorder()
	r.avControl(w, soapReq("Play", "<s:Envelope/>"))

	if w.Code == 200 {
		t.Fatalf("Play without a URI: %d %s", w.Code, w.Body)
	}
}

// A controller must not be told playback stopped when it did not.
func TestAVTransportStopFailureIsAFault(t *testing.T) {
	t.Parallel()

	for _, action := range []string{"Stop", "Pause"} {
		fb := newFakeBackend("PLAYING", 0)
		fb.stopErr = errBackendUnavailable
		r := newTestRenderer(fb)
		w := httptest.NewRecorder()
		r.avControl(w, soapReq(action, "<s:Envelope/>"))

		if w.Code == 200 || !strings.Contains(w.Body.String(), "Action Failed") {
			t.Errorf("%s with a failing backend: %d %s", action, w.Code, w.Body)
		}
	}
}

func TestAVTransportRejectsUnsafeURI(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{"file:///etc/shadow", "pipe:cat /etc/shadow", "http:///missing-host"} {
		backend := newFakeBackend("", 0)
		r := newTestRenderer(backend)
		body := `<s:Envelope><s:Body><u:SetAVTransportURI><CurrentURI>` + uri +
			`</CurrentURI></u:SetAVTransportURI></s:Body></s:Envelope>`
		w := httptest.NewRecorder()
		r.avControl(w, soapReq("SetAVTransportURI", body))

		if w.Code == http.StatusOK || backend.uri != "" {
			t.Fatalf("unsafe URI %q accepted: status=%d", uri, w.Code)
		}
	}
}

func TestDescEscapesName(t *testing.T) {
	t.Parallel()

	r := newNamedRenderer(nil, "Bert & Ernie")
	w := httptest.NewRecorder()
	r.desc(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/upnp/desc.xml", http.NoBody))

	body := w.Body.String()
	if !strings.Contains(body, "Bert &amp; Ernie") {
		t.Fatalf("name not XML-escaped in desc: %s", body)
	}

	if strings.Contains(body, "Bert & Ernie") {
		t.Fatal("raw & leaked into desc XML")
	}
}

func TestSearchResponseHasLocation(t *testing.T) {
	t.Parallel()

	r := newNamedRenderer(nil, "")

	resp := r.searchResponse(t.Context(), rootDevice)
	if !strings.Contains(resp, "LOCATION: http://1.2.3.4:80/upnp/desc.xml") {
		t.Fatalf("search response missing location: %q", resp)
	}

	if !strings.Contains(resp, "USN: uuid:abc::"+rootDevice) {
		t.Fatalf("search response USN wrong: %q", resp)
	}
}

// DLNA control is unauthenticated, and the URI it supplies is fetched by ffmpeg on
// the speaker — so it must not be usable to reach the speaker's own services.
//
//nolint:paralleltest // it swaps the package resolver lookupIP
func TestSetAVTransportURIRejectsInternalTargets(t *testing.T) {
	// Answer DNS here: the check must follow a name to its address, and CI has no
	// network.
	names := map[string][]net.IPAddr{
		"lo.example.test":    {{IP: net.IPv4(127, 0, 0, 1)}},
		"meta.example.test":  {{IP: net.IPv4(169, 254, 169, 254)}},
		"stream.example.com": {{IP: net.IPv4(93, 184, 216, 34)}},
		"media.example.test": {{IP: net.IPv4(192, 168, 1, 50)}},
		"split.example.test": {{IP: net.IPv4(93, 184, 216, 34)}, {IP: net.IPv4(127, 0, 0, 1)}},
	}
	orig := lookupIP
	lookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if a, ok := names[host]; ok {
			return a, nil
		}

		return nil, errNoSuchHost
	}

	t.Cleanup(func() { lookupIP = orig })

	for _, uri := range []string{
		"http://127.0.0.1:8080/api/settings",
		"http://localhost/api/settings",
		"http://[::1]/x.mp3",
		"http://[::1%25lo]/x.mp3", // loopback with a zone
		"http://[::ffff:127.0.0.1]/x.mp3",
		"http://[fe80::1%25wlan0]:8200/x.mp3", // link-local, like 169.254/16
		"http://169.254.169.254/latest/meta-data",
		"http://0.0.0.0/x.mp3",
		"http://lo.example.test/x.mp3",    // name resolving to 127.0.0.1
		"http://meta.example.test/x.mp3",  // name resolving to 169.254.169.254
		"http://nx.example.test/x.mp3",    // name that does not resolve
		"http://split.example.test/x.mp3", // one good address, one loopback
		"file:///etc/shadow",
		"ftp://example.com/x.mp3",
	} {
		if MediaURL(context.Background(), uri) {
			t.Errorf("%s should be refused", uri)
		}
	}

	for _, uri := range []string{
		"http://192.168.1.50:8200/media/1.mp3",
		"https://stream.example.com/live.aac",
		"http://10.0.0.5/x.mp3",
		"http://[fd00::50]:8200/media/1.mp3", // a ULA, like a private IPv4 address
		"http://[2001:db8::50]/x.mp3",
		"http://media.example.test/x.mp3",
	} {
		if !MediaURL(context.Background(), uri) {
			t.Errorf("%s should be allowed", uri)
		}
	}
}

// A join that fails before the network has a route is tried again, not given up.
//
//nolint:paralleltest // it swaps the package hooks listenSSDP and ssdpListenRetry
func TestServeRetriesTheGroupJoin(t *testing.T) {
	oldListen, oldRetry := listenSSDP, ssdpListenRetry
	defer func() { listenSSDP, ssdpListenRetry = oldListen, oldRetry }()

	ssdpListenRetry = time.Millisecond
	tries := make(chan struct{}, 16)
	listenSSDP = func(string, *net.Interface, *net.UDPAddr) (*net.UDPConn, error) {
		select {
		case tries <- struct{}{}:
		default:
		}

		return nil, errNoDevice
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		newTestRenderer(nil).Serve(ctx, func(string, ...any) {})
		close(done)
	}()

	for i := range 3 {
		select {
		case <-tries:
		case <-time.After(2 * time.Second):
			t.Fatalf("join tried %d times, want a retry", i)
		}
	}

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

// Every action an SCPD lists answers, and with each out argument it declares:
// async_upnp_client (Music Assistant) calls only what the SCPD lists and reads the
// response by those names.
func TestSCPDActionsAnswerWithTheirOutArgs(t *testing.T) {
	t.Parallel()

	fb := newFakeBackend("STOPPED", 30)
	r := newNamedRenderer(fb, "Test")
	r.uri = "http://192.168.1.9/a.mp3"
	mux := http.NewServeMux()
	r.Routes(mux)

	for _, svc := range []struct {
		scpd, control, urn string
	}{
		{"/upnp/avtransport.xml", "/upnp/control/avtransport", avTransURN},
		{"/upnp/renderingcontrol.xml", "/upnp/control/renderingcontrol", renderURN},
		{"/upnp/connectionmanager.xml", "/upnp/control/connectionmanager", connMgrURN},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, svc.scpd, http.NoBody))

		var doc scpdDoc

		err := xml.Unmarshal([]byte(strings.TrimPrefix(w.Body.String(), xmlDecl)), &doc)
		if err != nil || len(doc.Actions) == 0 {
			t.Fatalf("%s: %v, %d actions", svc.scpd, err, len(doc.Actions))
		}

		for _, action := range doc.Actions {
			// SetAVTransportURI and Play start playback (TestAVTransportSetURIPlayStop),
			// SetNextAVTransportURI needs a URI (TestAVTransportNextURIAdvances), Seek a
			// track with a duration (TestDurationResumeAndSeek), and Next and Previous a
			// queued track (TestAVTransportNextSkips).
			switch action.Name {
			case "SetAVTransportURI", "Play", "SetNextAVTransportURI", "Seek", "Next", "Previous":
				continue
			default:
			}

			body := `<s:Envelope><s:Body><u:` + action.Name + ` xmlns:u="` + svc.urn + `"><InstanceID>0</InstanceID>` +
				`<Channel>Master</Channel><DesiredVolume>20</DesiredVolume><DesiredMute>0</DesiredMute>` +
				`<ConnectionID>0</ConnectionID><NewPlayMode>NORMAL</NewPlayMode>` +
				`</u:` + action.Name + `></s:Body></s:Envelope>`
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, svc.control, strings.NewReader(body))
			req.Header.Set("SoapAction", `"`+svc.urn+`#`+action.Name+`"`)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != 200 {
				t.Fatalf("%s: status %d: %s", action.Name, rec.Code, rec.Body)
			}

			for _, arg := range action.Args {
				if arg.Dir == "out" && !strings.Contains(rec.Body.String(), "<"+arg.Name+">") {
					t.Fatalf("%s answered without %s: %s", action.Name, arg.Name, rec.Body)
				}
			}
		}
	}
}

// scpdDoc is the action list of a service description, as a controller reads it.
type scpdDoc struct {
	Actions []scpdDocAction `xml:"actionList>action"`
}

// scpdDocAction is one action of an scpdDoc.
type scpdDocAction struct {
	Name string            `xml:"name"`
	Args []scpdDocArgument `xml:"argumentList>argument"`
}

// scpdDocArgument is one argument of an scpdDocAction.
type scpdDocArgument struct {
	Name string `xml:"name"`
	Dir  string `xml:"direction"`
}

// Every argument's relatedStateVariable is declared: async_upnp_client refuses the whole
// service otherwise (KeyError), and Music Assistant then loses the player.
func TestSCPDDeclaresEveryArgumentState(t *testing.T) {
	t.Parallel()

	type stateDoc struct {
		Args []string `xml:"actionList>action>argumentList>argument>relatedStateVariable"`
		Vars []string `xml:"serviceStateTable>stateVariable>name"`
	}

	for name, s := range map[string]string{
		"AVT": avTransportSCPD(), "RCS": renderingControlSCPD(), "CM": connectionManagerSCPD(),
	} {
		var doc stateDoc

		err := xml.Unmarshal([]byte(s), &doc)
		if err != nil {
			t.Fatal(name, err)
		}

		declared := map[string]bool{}
		for _, v := range doc.Vars {
			declared[v] = true
		}

		for _, a := range doc.Args {
			if !declared[a] {
				t.Errorf("%s: argument state %q not declared", name, a)
			}
		}
	}
}

// A controller with its own queue sends the next track with SetNextAVTransportURI; it
// plays once the current one ends, but not after Stop or once another source took over.
func TestAVTransportNextURIAdvances(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	setNext := func(r *Renderer, uri string) {
		body := `<s:Envelope><s:Body><u:SetNextAVTransportURI><NextURI>` + uri + `</NextURI>` +
			`<NextURIMetaData>&lt;DIDL&gt;&lt;dc:title&gt;Two&lt;/dc:title&gt;</NextURIMetaData>` +
			`</u:SetNextAVTransportURI></s:Body></s:Envelope>`
		w := httptest.NewRecorder()
		r.avControl(w, soapReq("SetNextAVTransportURI", body))

		if w.Code != 200 {
			t.Fatalf("SetNextAVTransportURI status=%d", w.Code)
		}
	}
	start := func() (*fakeBackend, *Renderer) {
		backend := newFakeBackend("STOPPED", 0)
		renderer := newTestRenderer(backend)
		w := httptest.NewRecorder()
		renderer.avControl(w, soapReq("SetAVTransportURI", `<s:Envelope><s:Body><u:SetAVTransportURI>`+
			`<CurrentURI>http://192.168.1.9/one.flac</CurrentURI></u:SetAVTransportURI></s:Body></s:Envelope>`))
		setNext(renderer, "http://192.168.1.9/two.flac")

		return backend, renderer
	}

	backend, renderer := start()
	renderer.advance(ctx) // still PLAYING

	if backend.uri != "http://192.168.1.9/one.flac" {
		t.Fatalf("advanced while playing: %q", backend.uri)
	}

	backend.state = "STOPPED" // the track ran out

	renderer.advance(ctx)

	if backend.uri != "http://192.168.1.9/two.flac" || backend.title != "Two" {
		t.Fatalf("did not advance: %q %q", backend.uri, backend.title)
	}

	if uri, _ := renderer.current(); uri != backend.uri {
		t.Fatalf("current = %q", uri)
	}

	backend.uri, backend.state = "", "STOPPED"

	renderer.advance(ctx)

	if backend.uri != "" {
		t.Fatalf("advanced twice into %q", backend.uri)
	}

	backend, renderer = start()
	renderer.avControl(httptest.NewRecorder(), soapReq("Stop", "<s:Envelope/>"))
	renderer.advance(ctx)

	if backend.uri != "http://192.168.1.9/one.flac" {
		t.Fatalf("advanced after Stop: %q", backend.uri)
	}

	backend, renderer = start()
	backend.state = "NO_MEDIA_PRESENT" // another source took over

	renderer.advance(ctx)

	if backend.uri != "http://192.168.1.9/one.flac" {
		t.Fatalf("advanced over another source: %q", backend.uri)
	}

	_, renderer = start()
	w := httptest.NewRecorder()
	renderer.avControl(w, soapReq("SetNextAVTransportURI", `<s:Envelope><s:Body><u:SetNextAVTransportURI>`+
		`<NextURI>file:///etc/passwd</NextURI></u:SetNextAVTransportURI></s:Body></s:Envelope>`))

	if w.Code == 200 {
		t.Fatal("SetNextAVTransportURI took a file URI")
	}
}

// GetPositionInfo counts the time the URI has played, not paused time, and a new URI
// starts again at 0 (Music Assistant's progress bar stood still at 0:00:00).
func TestPositionCountsPlayingTime(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend("STOPPED", 0)
	renderer := newTestRenderer(backend)
	set := func(uri string) {
		w := httptest.NewRecorder()
		renderer.avControl(w, soapReq("SetAVTransportURI", `<s:Envelope><s:Body><u:SetAVTransportURI><CurrentURI>`+uri+
			`</CurrentURI><CurrentURIMetaData></CurrentURIMetaData></u:SetAVTransportURI></s:Body></s:Envelope>`))

		if w.Code != 200 {
			t.Fatalf("SetAVTransportURI = %d", w.Code)
		}
	}
	relTime := func() string {
		w := httptest.NewRecorder()
		renderer.avControl(w, soapReq("GetPositionInfo", "<s:Envelope/>"))

		return regexp.MustCompile(`<RelTime>([^<]*)</RelTime>`).FindStringSubmatch(w.Body.String())[1]
	}
	ctx := context.Background()
	start := time.Now()

	var heard time.Duration

	renderer.OnPosition = func(position, _ time.Duration) { heard = position }

	set("http://192.168.1.9/a.flac")

	for _, s := range []int{0, 1, 2, 65} {
		renderer.countPosition(ctx, start.Add(time.Duration(s)*time.Second))
	}

	backend.state = "PAUSED_PLAYBACK"

	renderer.countPosition(ctx, start.Add(100*time.Second))

	backend.state = "PLAYING"

	renderer.countPosition(ctx, start.Add(200*time.Second))
	renderer.countPosition(ctx, start.Add(201*time.Second))

	if got := relTime(); got != "0:01:06" {
		t.Fatalf("RelTime = %s, want 0:01:06 (65 s, the pause left out, then 1 s)", got)
	}

	if heard != 66*time.Second {
		t.Fatalf("OnPosition heard %v", heard)
	}

	set("http://192.168.1.9/b.flac")

	if got := relTime(); got != "0:00:00" {
		t.Fatalf("RelTime after a new URI = %s", got)
	}
}

// Music Assistant's DIDL for a queued track (helpers/upnp.py create_didl_metadata): the
// artist and the cover reach the speaker; a cover that is not http(s) does not.
func TestDIDLMetaFromMusicAssistant(t *testing.T) {
	t.Parallel()

	didl := `<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
		`xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` +
		`<item id="q1" restricted="true" parentID="p"><dc:title>Caf&#233; &amp; Bar</dc:title>` +
		`<dc:creator>Some Band</dc:creator><upnp:album>LP</upnp:album><upnp:artist>Some Band</upnp:artist>` +
		`<upnp:albumArtURI>http://192.0.2.124:8095/imageproxy?path=x&amp;size=256</upnp:albumArtURI>` +
		`<upnp:class>object.item.audioItem.musicTrack</upnp:class></item></DIDL-Lite>`

	meta := didlMeta(didl)
	if meta.Title != "Café & Bar" || meta.Artist != "Some Band" ||
		meta.Art != "http://192.0.2.124:8095/imageproxy?path=x&size=256" {
		t.Fatalf("meta = %+v", meta)
	}

	meta = didlMeta(`<dc:title>T</dc:title><dc:creator>C</dc:creator>` +
		`<upnp:albumArtURI>javascript:alert(1)</upnp:albumArtURI>`)
	if meta.Artist != "C" || meta.Art != "" {
		t.Fatalf("meta = %+v", meta)
	}
}

// A controller polling the speaker reads back what it sent about the track, also through
// the event stream, where the DIDL is escaped once more.
func TestControllerReadsTheTrackBack(t *testing.T) {
	t.Parallel()

	renderer := newTestRenderer(newFakeBackend("STOPPED", 0))
	didl := `<dc:title>Rock &amp; Roll</dc:title><upnp:artist>A&lt;B</upnp:artist><upnp:album>LP</upnp:album>` +
		`<upnp:albumArtURI>http://h/a.jpg</upnp:albumArtURI>`
	w := httptest.NewRecorder()
	renderer.avControl(w, soapReq("SetAVTransportURI", `<s:Envelope><s:Body><u:SetAVTransportURI>`+
		`<CurrentURI>http://192.168.1.50/a.mp3</CurrentURI><CurrentURIMetaData>`+xmlEscape(didl)+
		`</CurrentURIMetaData></u:SetAVTransportURI></s:Body></s:Envelope>`))

	want := Meta{Title: "Rock & Roll", Artist: "A<B", Album: "LP", Art: "http://h/a.jpg", Duration: 0}

	for action, tag := range map[string]*regexp.Regexp{
		"GetPositionInfo": regexp.MustCompile(argPattern("TrackMetaData")),
		"GetMediaInfo":    regexp.MustCompile(argPattern("CurrentURIMetaData")),
	} {
		w := httptest.NewRecorder()
		renderer.avControl(w, soapReq(action, ""))

		if got := didlMeta(unescapeXML(firstGroup(tag, w.Body.String()))); got != want {
			t.Errorf("%s: %+v, want %+v", action, got, want)
		}
	}

	event := unescapeXML(renderer.properties(context.Background())[avTransURN])

	val := regexp.MustCompile(`CurrentTrackMetaData val="([^"]*)"`).FindStringSubmatch(event)
	if val == nil {
		t.Fatalf("no metadata in %s", event)
	}

	if got := didlMeta(unescapeXML(val[1])); got != want {
		t.Errorf("event: %+v, want %+v", got, want)
	}

	var unknown Meta
	if unknown.didl() != "" {
		t.Error("an unknown track has metadata")
	}
}

// A controller that stops sending its body does not hold the connection.
//
//nolint:paralleltest // it shortens the package-wide soapBodyTimeout
func TestStalledSOAPBodyIsCutOff(t *testing.T) {
	old := soapBodyTimeout
	soapBodyTimeout = 200 * time.Millisecond

	t.Cleanup(func() { soapBodyTimeout = old })

	r := newTestRenderer(newFakeBackend("STOPPED", 0))

	srv := httptest.NewServer(http.HandlerFunc(r.avControl))
	defer srv.Close()

	conn, err := new(net.Dialer).DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, _ = conn.Write([]byte("POST / HTTP/1.1\r\nHost: x\r\nSOAPAction: \"" + avTransURN +
		"#GetTransportInfo\"\r\nContent-Length: 100\r\nConnection: close\r\n\r\n<"))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	_, err = io.ReadAll(conn)
	if err != nil {
		t.Fatalf("connection still held after the body deadline: %v", err)
	}
}

// A body that failed to arrive or ran past the 64 KiB limit must not run an action,
// even when the received prefix holds a complete DesiredVolume.
func TestSOAPPartialBodyRunsNoAction(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend("", 14)
	renderer := newTestRenderer(backend)

	const el = "<DesiredVolume>99</DesiredVolume>"
	for name, body := range map[string]io.Reader{
		"read error": io.MultiReader(strings.NewReader(el), iotestErr{}),
		"oversize":   strings.NewReader(el + strings.Repeat(" ", 70<<10)),
	} {
		req := rcReq("SetVolume", "")
		req.Body = io.NopCloser(body)
		w := httptest.NewRecorder()
		renderer.rcControl(w, req)

		if w.Code != 400 || backend.volume != 14 {
			t.Fatalf("%s: code %d, volume %d; want 400, 14", name, w.Code, backend.volume)
		}
	}
}

type iotestErr struct{}

func (iotestErr) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// A file's <res duration> is reported, Pause then Play resumes where it was, and Seek
// moves it; a stream without a duration does neither.
func TestDurationResumeAndSeek(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend("STOPPED", 0)
	renderer := newTestRenderer(backend)
	call := func(action, args string) string {
		w := httptest.NewRecorder()
		renderer.avControl(w, soapReq(action,
			`<s:Envelope><s:Body><u:`+action+`>`+args+`</u:`+action+`></s:Body></s:Envelope>`))

		return fmt.Sprint(w.Code, " ", w.Body.String())
	}
	didl := `&lt;DIDL-Lite&gt;&lt;item&gt;&lt;dc:title&gt;T&lt;/dc:title&gt;` +
		`&lt;res protocolInfo="http-get:*:audio/flac:*" duration="0:03:25.500"&gt;` +
		`http://192.168.1.9/a.flac&lt;/res&gt;` +
		`&lt;/item&gt;&lt;/DIDL-Lite&gt;`
	call("SetAVTransportURI",
		`<CurrentURI>http://192.168.1.9/a.flac</CurrentURI><CurrentURIMetaData>`+didl+`</CurrentURIMetaData>`)

	if got := call("GetPositionInfo", ""); !strings.Contains(got, "<TrackDuration>0:03:25</TrackDuration>") {
		t.Fatalf("GetPositionInfo = %s", got)
	}

	if got := call("GetMediaInfo", ""); !strings.Contains(got, "<MediaDuration>0:03:25</MediaDuration>") {
		t.Fatalf("GetMediaInfo = %s", got)
	}

	if got := call("GetCurrentTransportActions", ""); !strings.Contains(got, "Play,Stop,Pause,Seek") {
		t.Fatalf("actions = %s", got)
	}

	renderer.elapsed = 40 * time.Second

	call("Pause", "")

	if got := call("Play", ""); !strings.HasPrefix(got, "200") || backend.at != 40*time.Second {
		t.Fatalf("Play after Pause: %s at=%v, want 40s", got, backend.at)
	}

	seeked := call("Seek", `<Unit>REL_TIME</Unit><Target>0:01:30</Target>`)
	if !strings.HasPrefix(seeked, "200") || backend.at != 90*time.Second || renderer.elapsed != 90*time.Second {
		t.Fatalf("Seek: %s at=%v elapsed=%v", seeked, backend.at, renderer.elapsed)
	}

	if got := call("Seek", `<Unit>REL_TIME</Unit><Target>0:09:00</Target>`); !strings.Contains(got, "711") {
		t.Fatalf("Seek past the end = %s", got)
	}
	// Paused, Seek only moves the position; Play starts there.
	call("Pause", "")

	backend.at = 0

	call("Seek", `<Unit>REL_TIME</Unit><Target>0:00:10</Target>`)

	if backend.at != 0 || backend.state != "PAUSED_PLAYBACK" {
		t.Fatalf("Seek while paused started playback: at=%v state=%s", backend.at, backend.state)
	}

	call("Play", "")

	if backend.at != 10*time.Second {
		t.Fatalf("Play after a paused Seek at=%v, want 10s", backend.at)
	}
	// Stop then Play starts from the top.
	call("Stop", "")

	call("Play", "")

	if backend.at != 0 {
		t.Fatalf("Play after Stop at=%v", backend.at)
	}

	call("SetAVTransportURI",
		`<CurrentURI>http://192.168.1.9/live</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`)

	if got := call("Seek", `<Unit>REL_TIME</Unit><Target>0:00:10</Target>`); !strings.Contains(got, "710") {
		t.Fatalf("Seek on a stream = %s", got)
	}

	if got := call("GetCurrentTransportActions", ""); strings.Contains(got, "Seek") {
		t.Fatalf("stream actions = %s", got)
	}
}

// Next starts the queued track at once and Previous has nothing to go back to: the
// controller holds the queue. A play mode other than NORMAL is refused.
func TestAVTransportNextSkips(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend("PLAYING", 0)
	renderer := newTestRenderer(backend)

	code := func(action, body string) int {
		w := httptest.NewRecorder()
		renderer.avControl(w, soapReq(action, `<s:Envelope><s:Body>`+body+`</s:Body></s:Envelope>`))

		return w.Code
	}
	if code("Next", "") == 200 {
		t.Fatal("Next with nothing queued must fail")
	}

	code("SetAVTransportURI", `<CurrentURI>http://192.168.1.9/one.flac</CurrentURI>`)
	code("SetNextAVTransportURI", `<NextURI>http://192.168.1.9/two.flac</NextURI>`)

	if got := code("Next", ""); got != 200 || backend.uri != "http://192.168.1.9/two.flac" {
		t.Fatalf("Next = %d, on %q", got, backend.uri)
	}

	if uri, _ := renderer.current(); uri != backend.uri {
		t.Fatalf("current = %q", uri)
	}

	if code("Previous", "") == 200 {
		t.Fatal("Previous must fail")
	}

	if code("SetPlayMode", `<NewPlayMode>SHUFFLE</NewPlayMode>`) == 200 {
		t.Fatal("SHUFFLE must be refused")
	}
}

// The speaker's own time bar seeks through SeekTo: nothing on, or past the end, is refused.
func TestSeekToRefuses(t *testing.T) {
	t.Parallel()

	renderer := newTestRenderer(nil)

	err := renderer.SeekTo(context.Background(), time.Second)
	if !errors.Is(err, ErrNoSeek) {
		t.Fatalf("empty renderer: %v", err)
	}

	track := Meta{Title: "", Artist: "", Album: "", Art: "", Duration: time.Minute}

	err = renderer.moveTo(context.Background(), "http://x/a.mp3", track, time.Minute)
	if !errors.Is(err, ErrSeekTarget) {
		t.Fatalf("past the end: %v", err)
	}
}
