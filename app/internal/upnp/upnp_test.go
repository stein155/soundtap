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
	at         time.Duration
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
	f.volume = v
	return nil
}
func (f *fakeBackend) SetMute(_ context.Context, on bool) error {
	if on {
		f.volume = 0
	} else if f.volume == 0 {
		f.volume = 20
	}
	return nil
}

func rcReq(action, inner string) *http.Request {
	body := `<s:Envelope><s:Body><u:` + action + ` xmlns:u="` + renderURN + `"><InstanceID>0</InstanceID>` +
		inner + `</u:` + action + `></s:Body></s:Envelope>`
	r := httptest.NewRequest("POST", "/upnp/control/renderingcontrol", strings.NewReader(body))
	r.Header.Set("SOAPACTION", `"`+renderURN+`#`+action+`"`)
	return r
}

// RenderingControl drives the speaker's own volume and mute.
func TestRenderingControlVolumeAndMute(t *testing.T) {
	fb := &fakeBackend{volume: 30}
	r := &Renderer{Backend: fb}
	do := func(action, inner string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.rcControl(w, rcReq(action, inner))
		return w
	}
	if w := do("GetVolume", "<Channel>Master</Channel>"); !strings.Contains(w.Body.String(), "<CurrentVolume>30</CurrentVolume>") {
		t.Fatalf("GetVolume = %s", w.Body)
	}
	if w := do("SetVolume", "<Channel>Master</Channel><DesiredVolume>55</DesiredVolume>"); w.Code != 200 || fb.volume != 55 {
		t.Fatalf("SetVolume = %d, volume %d", w.Code, fb.volume)
	}
	for _, bad := range []string{"<DesiredVolume>101</DesiredVolume>", "<DesiredVolume>x</DesiredVolume>", ""} {
		if w := do("SetVolume", "<Channel>Master</Channel>"+bad); w.Code != 500 || fb.volume != 55 {
			t.Fatalf("SetVolume %q = %d, volume %d", bad, w.Code, fb.volume)
		}
	}
	if w := do("SetVolume", "<Channel>LF</Channel><DesiredVolume>10</DesiredVolume>"); w.Code != 500 || fb.volume != 55 {
		t.Fatalf("SetVolume on LF = %d, volume %d", w.Code, fb.volume)
	}
	if w := do("SetMute", "<Channel>Master</Channel><DesiredMute>1</DesiredMute>"); w.Code != 200 || fb.volume != 0 {
		t.Fatalf("SetMute 1 = %d, volume %d", w.Code, fb.volume)
	}
	if w := do("GetMute", "<Channel>Master</Channel>"); !strings.Contains(w.Body.String(), "<CurrentMute>1</CurrentMute>") {
		t.Fatalf("GetMute = %s", w.Body)
	}
	if w := do("SetMute", "<Channel>Master</Channel><DesiredMute>false</DesiredMute>"); w.Code != 200 || fb.volume == 0 {
		t.Fatalf("SetMute false = %d, volume %d", w.Code, fb.volume)
	}
}

// Controllers send the URI with a namespace prefix, attributes or as CDATA.
func TestSetAVTransportURIForms(t *testing.T) {
	for _, uri := range []string{
		`<CurrentURI xmlns:dt="urn:schemas-microsoft-com:datatypes" dt:dt="string">http://192.168.1.9/a.mp3</CurrentURI>`,
		`<u:CurrentURI>http://192.168.1.9/a.mp3</u:CurrentURI>`,
		`<CurrentURI><![CDATA[http://192.168.1.9/a.mp3]]></CurrentURI>`,
	} {
		fb := &fakeBackend{state: "STOPPED"}
		r := &Renderer{Backend: fb}
		w := httptest.NewRecorder()
		r.avControl(w, soapReq("SetAVTransportURI", `<s:Envelope><s:Body><u:SetAVTransportURI>`+uri+
			`<CurrentURIMetaData></CurrentURIMetaData></u:SetAVTransportURI></s:Body></s:Envelope>`))
		if w.Code != 200 || fb.uri != "http://192.168.1.9/a.mp3" {
			t.Errorf("%s: %d uri=%q", uri, w.Code, fb.uri)
		}
	}
}

func soapReq(action, body string) *http.Request {
	r := httptest.NewRequest("POST", "/upnp/control/avtransport", strings.NewReader(body))
	r.Header.Set("SOAPACTION", `"`+avTransURN+`#`+action+`"`)
	return r
}

func TestSoapActionParse(t *testing.T) {
	if got := soapAction(`"urn:schemas-upnp-org:service:AVTransport:1#Play"`); got != "Play" {
		t.Fatalf("soapAction=%q", got)
	}
}

func TestSearchTarget(t *testing.T) {
	msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nST: upnp:rootdevice\r\nMAN: \"ssdp:discover\"\r\n\r\n"
	if got := searchTarget(msg); got != "upnp:rootdevice" {
		t.Fatalf("searchTarget=%q", got)
	}
}

func TestMatchTargets(t *testing.T) {
	r := &Renderer{UDN: "uuid:abc"}
	if len(r.matchTargets("ssdp:all")) != 6 {
		t.Fatalf("ssdp:all should match all advertised targets")
	}
	if got := r.matchTargets(rootDevice); len(got) != 1 || got[0] != rootDevice {
		t.Fatalf("device match = %v", got)
	}
	if r.matchTargets("urn:nope") != nil {
		t.Fatalf("unknown ST should not match")
	}
}

func TestAVTransportSetURIPlayStop(t *testing.T) {
	fb := &fakeBackend{state: "STOPPED"}
	r := &Renderer{UDN: "uuid:abc", Name: "Test", IP: "1.2.3.4", Port: "80", Backend: fb}

	// SetAVTransportURI with a DIDL title and an escaped &.
	body := `<s:Envelope><s:Body><u:SetAVTransportURI>` +
		`<CurrentURI>http://192.168.1.9/song.mp3?a=1&amp;b=2</CurrentURI>` +
		`<CurrentURIMetaData>&lt;DIDL&gt;&lt;dc:title&gt;Rock &amp;amp; Roll&lt;/dc:title&gt;</CurrentURIMetaData>` +
		`</u:SetAVTransportURI></s:Body></s:Envelope>`
	w := httptest.NewRecorder()
	r.avControl(w, soapReq("SetAVTransportURI", body))
	if w.Code != 200 {
		t.Fatalf("SetAVTransportURI status=%d", w.Code)
	}
	if fb.uri != "http://192.168.1.9/song.mp3?a=1&b=2" {
		t.Fatalf("uri = %q (unescape failed?)", fb.uri)
	}
	if fb.title != "Rock & Roll" {
		t.Fatalf("title = %q", fb.title)
	}

	// GetTransportInfo reflects PLAYING.
	w = httptest.NewRecorder()
	r.avControl(w, soapReq("GetTransportInfo", "<s:Envelope/>"))
	if !strings.Contains(w.Body.String(), "<CurrentTransportState>PLAYING</CurrentTransportState>") {
		t.Fatalf("transport info body = %s", w.Body.String())
	}

	// Stop.
	w = httptest.NewRecorder()
	r.avControl(w, soapReq("Stop", "<s:Envelope/>"))
	if !fb.stopped {
		t.Fatalf("Stop did not reach backend")
	}

	// Play after Stop restarts the same URI.
	fb.uri = ""
	w = httptest.NewRecorder()
	r.avControl(w, soapReq("Play", "<s:Envelope/>"))
	if w.Code != 200 || fb.state != "PLAYING" || fb.uri != "http://192.168.1.9/song.mp3?a=1&b=2" {
		t.Fatalf("Play after Stop: %d state=%s uri=%q", w.Code, fb.state, fb.uri)
	}
}

// Play with nothing to play must not claim success.
func TestAVTransportPlayWithoutURIIsAFault(t *testing.T) {
	r := &Renderer{Backend: &fakeBackend{state: "STOPPED"}}
	w := httptest.NewRecorder()
	r.avControl(w, soapReq("Play", "<s:Envelope/>"))
	if w.Code == 200 {
		t.Fatalf("Play without a URI: %d %s", w.Code, w.Body)
	}
}

// A controller must not be told playback stopped when it did not.
func TestAVTransportStopFailureIsAFault(t *testing.T) {
	for _, action := range []string{"Stop", "Pause"} {
		r := &Renderer{Backend: &fakeBackend{state: "PLAYING", stopErr: errors.New("backend unavailable")}}
		w := httptest.NewRecorder()
		r.avControl(w, soapReq(action, "<s:Envelope/>"))
		if w.Code == 200 || !strings.Contains(w.Body.String(), "Action Failed") {
			t.Errorf("%s with a failing backend: %d %s", action, w.Code, w.Body)
		}
	}
}

func TestAVTransportRejectsUnsafeURI(t *testing.T) {
	for _, uri := range []string{"file:///etc/shadow", "pipe:cat /etc/shadow", "http:///missing-host"} {
		fb := &fakeBackend{}
		r := &Renderer{Backend: fb}
		body := `<s:Envelope><s:Body><u:SetAVTransportURI><CurrentURI>` + uri +
			`</CurrentURI></u:SetAVTransportURI></s:Body></s:Envelope>`
		w := httptest.NewRecorder()
		r.avControl(w, soapReq("SetAVTransportURI", body))
		if w.Code == http.StatusOK || fb.uri != "" {
			t.Fatalf("unsafe URI %q accepted: status=%d", uri, w.Code)
		}
	}
}

func TestDescEscapesName(t *testing.T) {
	r := &Renderer{UDN: "uuid:abc", Name: "Bert & Ernie", IP: "1.2.3.4", Port: "80"}
	w := httptest.NewRecorder()
	r.desc(w, httptest.NewRequest("GET", "/upnp/desc.xml", nil))
	body := w.Body.String()
	if !strings.Contains(body, "Bert &amp; Ernie") {
		t.Fatalf("name not XML-escaped in desc: %s", body)
	}
	if strings.Contains(body, "Bert & Ernie") {
		t.Fatalf("raw & leaked into desc XML")
	}
}

func TestSearchResponseHasLocation(t *testing.T) {
	r := &Renderer{UDN: "uuid:abc", IP: "1.2.3.4", Port: "80"}
	resp := r.searchResponse(rootDevice)
	if !strings.Contains(resp, "LOCATION: http://1.2.3.4:80/upnp/desc.xml") {
		t.Fatalf("search response missing location: %q", resp)
	}
	if !strings.Contains(resp, "USN: uuid:abc::"+rootDevice) {
		t.Fatalf("search response USN wrong: %q", resp)
	}
}

// DLNA control is unauthenticated, and the URI it supplies is fetched by ffmpeg on
// the speaker — so it must not be usable to reach the speaker's own services.
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
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupIP = orig })

	for _, uri := range []string{
		"http://127.0.0.1:8080/api/settings",
		"http://localhost/api/settings",
		"http://[::1]/x.mp3",
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
		"http://media.example.test/x.mp3",
	} {
		if !MediaURL(context.Background(), uri) {
			t.Errorf("%s should be allowed", uri)
		}
	}
}

// A join that fails before the network has a route is tried again, not given up.
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
		return nil, errors.New("setsockopt: no such device")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		(&Renderer{}).Serve(ctx, func(string, ...any) {})
		close(done)
	}()
	for i := 0; i < 3; i++ {
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
	fb := &fakeBackend{state: "STOPPED", volume: 30}
	r := &Renderer{UDN: "uuid:abc", Name: "Test", IP: "1.2.3.4", Port: "80", Backend: fb}
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
		mux.ServeHTTP(w, httptest.NewRequest("GET", svc.scpd, nil))
		var doc struct {
			Actions []struct {
				Name string `xml:"name"`
				Args []struct {
					Name string `xml:"name"`
					Dir  string `xml:"direction"`
				} `xml:"argumentList>argument"`
			} `xml:"actionList>action"`
		}
		if err := xml.Unmarshal([]byte(strings.TrimPrefix(w.Body.String(), xmlDecl)), &doc); err != nil || len(doc.Actions) == 0 {
			t.Fatalf("%s: %v, %d actions", svc.scpd, err, len(doc.Actions))
		}
		for _, a := range doc.Actions {
			switch a.Name {
			case "SetAVTransportURI", "Play":
				continue // they start playback; TestAVTransportSetURIPlayStop covers them
			case "SetNextAVTransportURI":
				continue // it needs a URI; TestAVTransportNextURIAdvances covers it
			case "Seek":
				continue // it needs a track with a duration; TestDurationResumeAndSeek covers it
			case "Next", "Previous":
				continue // they need a queued track; TestAVTransportNextSkips covers them
			}
			body := `<s:Envelope><s:Body><u:` + a.Name + ` xmlns:u="` + svc.urn + `"><InstanceID>0</InstanceID>` +
				`<Channel>Master</Channel><DesiredVolume>20</DesiredVolume><DesiredMute>0</DesiredMute>` +
				`<ConnectionID>0</ConnectionID><NewPlayMode>NORMAL</NewPlayMode></u:` + a.Name + `></s:Body></s:Envelope>`
			req := httptest.NewRequest("POST", svc.control, strings.NewReader(body))
			req.Header.Set("SOAPACTION", `"`+svc.urn+`#`+a.Name+`"`)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("%s: status %d: %s", a.Name, w.Code, w.Body)
			}
			for _, arg := range a.Args {
				if arg.Dir == "out" && !strings.Contains(w.Body.String(), "<"+arg.Name+">") {
					t.Fatalf("%s answered without %s: %s", a.Name, arg.Name, w.Body)
				}
			}
		}
	}
}

// Every argument's relatedStateVariable is declared: async_upnp_client refuses the whole
// service otherwise (KeyError), and Music Assistant then loses the player.
func TestSCPDDeclaresEveryArgumentState(t *testing.T) {
	type doc struct {
		Args []string `xml:"actionList>action>argumentList>argument>relatedStateVariable"`
		Vars []string `xml:"serviceStateTable>stateVariable>name"`
	}
	for name, s := range map[string]string{"AVT": avTransportSCPD, "RCS": renderingControlSCPD, "CM": connectionManagerSCPD} {
		var d doc
		if err := xml.Unmarshal([]byte(s), &d); err != nil {
			t.Fatal(name, err)
		}
		declared := map[string]bool{}
		for _, v := range d.Vars {
			declared[v] = true
		}
		for _, a := range d.Args {
			if !declared[a] {
				t.Errorf("%s: argument state %q not declared", name, a)
			}
		}
	}
}

// A controller with its own queue sends the next track with SetNextAVTransportURI; it
// plays once the current one ends, but not after Stop or once another source took over.
func TestAVTransportNextURIAdvances(t *testing.T) {
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
		fb := &fakeBackend{state: "STOPPED"}
		r := &Renderer{Backend: fb}
		w := httptest.NewRecorder()
		r.avControl(w, soapReq("SetAVTransportURI", `<s:Envelope><s:Body><u:SetAVTransportURI>`+
			`<CurrentURI>http://192.168.1.9/one.flac</CurrentURI></u:SetAVTransportURI></s:Body></s:Envelope>`))
		setNext(r, "http://192.168.1.9/two.flac")
		return fb, r
	}

	fb, r := start()
	r.advance(ctx) // still PLAYING
	if fb.uri != "http://192.168.1.9/one.flac" {
		t.Fatalf("advanced while playing: %q", fb.uri)
	}
	fb.state = "STOPPED" // the track ran out
	r.advance(ctx)
	if fb.uri != "http://192.168.1.9/two.flac" || fb.title != "Two" {
		t.Fatalf("did not advance: %q %q", fb.uri, fb.title)
	}
	if uri, _ := r.current(); uri != fb.uri {
		t.Fatalf("current = %q", uri)
	}
	fb.uri, fb.state = "", "STOPPED"
	r.advance(ctx)
	if fb.uri != "" {
		t.Fatalf("advanced twice into %q", fb.uri)
	}

	fb, r = start()
	r.avControl(httptest.NewRecorder(), soapReq("Stop", "<s:Envelope/>"))
	r.advance(ctx)
	if fb.uri != "http://192.168.1.9/one.flac" {
		t.Fatalf("advanced after Stop: %q", fb.uri)
	}

	fb, r = start()
	fb.state = "NO_MEDIA_PRESENT" // another source took over
	r.advance(ctx)
	if fb.uri != "http://192.168.1.9/one.flac" {
		t.Fatalf("advanced over another source: %q", fb.uri)
	}

	_, r = start()
	w := httptest.NewRecorder()
	r.avControl(w, soapReq("SetNextAVTransportURI", `<s:Envelope><s:Body><u:SetNextAVTransportURI>`+
		`<NextURI>file:///etc/passwd</NextURI></u:SetNextAVTransportURI></s:Body></s:Envelope>`))
	if w.Code == 200 {
		t.Fatalf("SetNextAVTransportURI took a file URI")
	}
}

// GetPositionInfo counts the time the URI has played, not paused time, and a new URI
// starts again at 0 (Music Assistant's progress bar stood still at 0:00:00).
func TestPositionCountsPlayingTime(t *testing.T) {
	fb := &fakeBackend{state: "STOPPED"}
	r := &Renderer{Backend: fb}
	set := func(uri string) {
		w := httptest.NewRecorder()
		r.avControl(w, soapReq("SetAVTransportURI", `<s:Envelope><s:Body><u:SetAVTransportURI><CurrentURI>`+uri+
			`</CurrentURI><CurrentURIMetaData></CurrentURIMetaData></u:SetAVTransportURI></s:Body></s:Envelope>`))
		if w.Code != 200 {
			t.Fatalf("SetAVTransportURI = %d", w.Code)
		}
	}
	relTime := func() string {
		w := httptest.NewRecorder()
		r.avControl(w, soapReq("GetPositionInfo", "<s:Envelope/>"))
		return regexp.MustCompile(`<RelTime>([^<]*)</RelTime>`).FindStringSubmatch(w.Body.String())[1]
	}
	ctx := context.Background()
	t0 := time.Now()
	var heard time.Duration
	r.OnPosition = func(position, _ time.Duration) { heard = position }
	set("http://192.168.1.9/a.flac")
	for _, s := range []int{0, 1, 2, 65} {
		r.countPosition(ctx, t0.Add(time.Duration(s)*time.Second))
	}
	fb.state = "PAUSED_PLAYBACK"
	r.countPosition(ctx, t0.Add(100*time.Second))
	fb.state = "PLAYING"
	r.countPosition(ctx, t0.Add(200*time.Second))
	r.countPosition(ctx, t0.Add(201*time.Second))
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
	didl := `<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` +
		`<item id="q1" restricted="true" parentID="p"><dc:title>Caf&#233; &amp; Bar</dc:title>` +
		`<dc:creator>Some Band</dc:creator><upnp:album>LP</upnp:album><upnp:artist>Some Band</upnp:artist>` +
		`<upnp:albumArtURI>http://192.0.2.124:8095/imageproxy?path=x&amp;size=256</upnp:albumArtURI>` +
		`<upnp:class>object.item.audioItem.musicTrack</upnp:class></item></DIDL-Lite>`
	m := didlMeta(didl)
	if m.Title != "Café & Bar" || m.Artist != "Some Band" || m.Art != "http://192.0.2.124:8095/imageproxy?path=x&size=256" {
		t.Fatalf("meta = %+v", m)
	}
	if m := didlMeta(`<dc:title>T</dc:title><dc:creator>C</dc:creator><upnp:albumArtURI>javascript:alert(1)</upnp:albumArtURI>`); m.Artist != "C" || m.Art != "" {
		t.Fatalf("meta = %+v", m)
	}
}

// A controller polling the speaker reads back what it sent about the track, also through
// the event stream, where the DIDL is escaped once more.
func TestControllerReadsTheTrackBack(t *testing.T) {
	r := &Renderer{Backend: &fakeBackend{state: "STOPPED"}}
	didl := `<dc:title>Rock &amp; Roll</dc:title><upnp:artist>A&lt;B</upnp:artist><upnp:album>LP</upnp:album>` +
		`<upnp:albumArtURI>http://h/a.jpg</upnp:albumArtURI>`
	w := httptest.NewRecorder()
	r.avControl(w, soapReq("SetAVTransportURI", `<s:Envelope><s:Body><u:SetAVTransportURI><CurrentURI>http://192.168.1.50/a.mp3`+
		`</CurrentURI><CurrentURIMetaData>`+xmlEscape(didl)+`</CurrentURIMetaData></u:SetAVTransportURI></s:Body></s:Envelope>`))
	want := Meta{Title: "Rock & Roll", Artist: "A<B", Album: "LP", Art: "http://h/a.jpg"}
	for action, tag := range map[string]*regexp.Regexp{"GetPositionInfo": arg("TrackMetaData"), "GetMediaInfo": arg("CurrentURIMetaData")} {
		w := httptest.NewRecorder()
		r.avControl(w, soapReq(action, ""))
		if got := didlMeta(unescapeXML(firstGroup(tag, w.Body.String()))); got != want {
			t.Errorf("%s: %+v, want %+v", action, got, want)
		}
	}
	event := unescapeXML(r.properties(context.Background())[avTransURN])
	val := regexp.MustCompile(`CurrentTrackMetaData val="([^"]*)"`).FindStringSubmatch(event)
	if val == nil {
		t.Fatalf("no metadata in %s", event)
	}
	if got := didlMeta(unescapeXML(val[1])); got != want {
		t.Errorf("event: %+v, want %+v", got, want)
	}
	if (Meta{}).didl() != "" {
		t.Error("an unknown track has metadata")
	}
}

// A controller that stops sending its body does not hold the connection.
func TestStalledSOAPBodyIsCutOff(t *testing.T) {
	old := soapBodyTimeout
	soapBodyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { soapBodyTimeout = old })
	r := &Renderer{Backend: &fakeBackend{state: "STOPPED"}}
	srv := httptest.NewServer(http.HandlerFunc(r.avControl))
	defer srv.Close()
	c, err := new(net.Dialer).DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("POST / HTTP/1.1\r\nHost: x\r\nSOAPAction: \"" + avTransURN +
		"#GetTransportInfo\"\r\nContent-Length: 100\r\nConnection: close\r\n\r\n<"))
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("connection still held after the body deadline: %v", err)
	}
}

// A body that failed to arrive or ran past the 64 KiB limit must not run an action,
// even when the received prefix holds a complete DesiredVolume.
func TestSOAPPartialBodyRunsNoAction(t *testing.T) {
	fb := &fakeBackend{volume: 14}
	r := &Renderer{Backend: fb}
	const el = "<DesiredVolume>99</DesiredVolume>"
	for name, body := range map[string]io.Reader{
		"read error": io.MultiReader(strings.NewReader(el), iotestErr{}),
		"oversize":   strings.NewReader(el + strings.Repeat(" ", 70<<10)),
	} {
		req := rcReq("SetVolume", "")
		req.Body = io.NopCloser(body)
		w := httptest.NewRecorder()
		r.rcControl(w, req)
		if w.Code != 400 || fb.volume != 14 {
			t.Fatalf("%s: code %d, volume %d; want 400, 14", name, w.Code, fb.volume)
		}
	}
}

type iotestErr struct{}

func (iotestErr) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// A file's <res duration> is reported, Pause then Play resumes where it was, and Seek
// moves it; a stream without a duration does neither.
func TestDurationResumeAndSeek(t *testing.T) {
	fb := &fakeBackend{state: "STOPPED"}
	r := &Renderer{Backend: fb}
	call := func(action, args string) string {
		w := httptest.NewRecorder()
		r.avControl(w, soapReq(action, `<s:Envelope><s:Body><u:`+action+`>`+args+`</u:`+action+`></s:Body></s:Envelope>`))
		return fmt.Sprint(w.Code, " ", w.Body.String())
	}
	didl := `&lt;DIDL-Lite&gt;&lt;item&gt;&lt;dc:title&gt;T&lt;/dc:title&gt;` +
		`&lt;res protocolInfo="http-get:*:audio/flac:*" duration="0:03:25.500"&gt;http://192.168.1.9/a.flac&lt;/res&gt;&lt;/item&gt;&lt;/DIDL-Lite&gt;`
	call("SetAVTransportURI", `<CurrentURI>http://192.168.1.9/a.flac</CurrentURI><CurrentURIMetaData>`+didl+`</CurrentURIMetaData>`)
	if got := call("GetPositionInfo", ""); !strings.Contains(got, "<TrackDuration>0:03:25</TrackDuration>") {
		t.Fatalf("GetPositionInfo = %s", got)
	}
	if got := call("GetMediaInfo", ""); !strings.Contains(got, "<MediaDuration>0:03:25</MediaDuration>") {
		t.Fatalf("GetMediaInfo = %s", got)
	}
	if got := call("GetCurrentTransportActions", ""); !strings.Contains(got, "Play,Stop,Pause,Seek") {
		t.Fatalf("actions = %s", got)
	}

	r.elapsed = 40 * time.Second
	call("Pause", "")
	if got := call("Play", ""); !strings.HasPrefix(got, "200") || fb.at != 40*time.Second {
		t.Fatalf("Play after Pause: %s at=%v, want 40s", got, fb.at)
	}
	if got := call("Seek", `<Unit>REL_TIME</Unit><Target>0:01:30</Target>`); !strings.HasPrefix(got, "200") || fb.at != 90*time.Second || r.elapsed != 90*time.Second {
		t.Fatalf("Seek: %s at=%v elapsed=%v", got, fb.at, r.elapsed)
	}
	if got := call("Seek", `<Unit>REL_TIME</Unit><Target>0:09:00</Target>`); !strings.Contains(got, "711") {
		t.Fatalf("Seek past the end = %s", got)
	}
	// Paused, Seek only moves the position; Play starts there.
	call("Pause", "")
	fb.at = 0
	call("Seek", `<Unit>REL_TIME</Unit><Target>0:00:10</Target>`)
	if fb.at != 0 || fb.state != "PAUSED_PLAYBACK" {
		t.Fatalf("Seek while paused started playback: at=%v state=%s", fb.at, fb.state)
	}
	if call("Play", ""); fb.at != 10*time.Second {
		t.Fatalf("Play after a paused Seek at=%v, want 10s", fb.at)
	}
	// Stop then Play starts from the top.
	call("Stop", "")
	if call("Play", ""); fb.at != 0 {
		t.Fatalf("Play after Stop at=%v", fb.at)
	}

	call("SetAVTransportURI", `<CurrentURI>http://192.168.1.9/live</CurrentURI><CurrentURIMetaData></CurrentURIMetaData>`)
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
	fb := &fakeBackend{state: "PLAYING"}
	r := &Renderer{Backend: fb}
	code := func(action, body string) int {
		w := httptest.NewRecorder()
		r.avControl(w, soapReq(action, `<s:Envelope><s:Body>`+body+`</s:Body></s:Envelope>`))
		return w.Code
	}
	if code("Next", "") == 200 {
		t.Fatal("Next with nothing queued must fail")
	}
	code("SetAVTransportURI", `<CurrentURI>http://192.168.1.9/one.flac</CurrentURI>`)
	code("SetNextAVTransportURI", `<NextURI>http://192.168.1.9/two.flac</NextURI>`)
	if got := code("Next", ""); got != 200 || fb.uri != "http://192.168.1.9/two.flac" {
		t.Fatalf("Next = %d, on %q", got, fb.uri)
	}
	if uri, _ := r.current(); uri != fb.uri {
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
	r := &Renderer{}
	if err := r.SeekTo(context.Background(), time.Second); !errors.Is(err, ErrNoSeek) {
		t.Fatalf("empty renderer: %v", err)
	}
	if err := r.seekTo(context.Background(), "http://x/a.mp3", Meta{Duration: time.Minute}, time.Minute); !errors.Is(err, ErrSeekTarget) {
		t.Fatalf("past the end: %v", err)
	}
}

// A media fetch follows a redirect only to another MediaURL, and never dials loopback,
// whatever the URL or the DNS answer said.
func TestMediaClientStaysOffLoopback(t *testing.T) {
	c := MediaClient(time.Second)
	ctx := context.Background()
	for target, ok := range map[string]bool{
		"http://192.168.1.9/radio.pls": true, "http://127.0.0.1/api/info": false,
		"http://localhost/x": false, "http://169.254.169.254/latest": false, "file:///etc/passwd": false,
	} {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err := c.CheckRedirect(req, nil); (err == nil) != ok {
			t.Errorf("redirect to %s: err %v, want allowed=%v", target, err, ok)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the loopback server was reached")
	}))
	defer srv.Close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if resp, err := c.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("dialled loopback")
	}
}
