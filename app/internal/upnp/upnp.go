// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package upnp is a minimal UPnP AV MediaRenderer: it lets DLNA controllers
// (BubbleUPnP, VLC, Windows "Cast to device", …) push a media URL to the speaker,
// which soundtapd then plays through the audio arbiter like any other source.
//
// It implements just enough to be discovered and driven: SSDP advertise/replies, a
// device description, and the AVTransport actions SetAVTransportURI / Play / Stop /
// GetTransportInfo / GetPositionInfo / GetMediaInfo, RenderingControl's volume and mute,
// and ConnectionManager's protocol info. Playback itself is delegated to a
// Backend — on-device that sets the UPNP source so the player streams the URL.
package upnp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Backend performs the actual playback the renderer controls.
type Backend interface {
	// PlayURL starts uri, at the given offset into it: Seek, and Play after Pause, start
	// a track with a known duration where it should be rather than from the top.
	PlayURL(ctx context.Context, uri string, m Meta, at time.Duration) error
	Stop(ctx context.Context) error
	// Pause holds playback with the source kept, so Play picks it up again.
	Pause(ctx context.Context) error
	// TransportState reports "PLAYING", "STOPPED", etc. for GetTransportInfo.
	TransportState(ctx context.Context) string
	// Volume and SetVolume are the speaker's own 0..100 volume, for RenderingControl.
	Volume(ctx context.Context) (int, error)
	SetVolume(ctx context.Context, level int) error
	// SetMute mutes (volume 0, the level kept to come back to) or unmutes.
	SetMute(ctx context.Context, on bool) error
}

// Meta is what a controller's DIDL-Lite says about a URI. Music Assistant sends the
// artist and the cover with every track, its own logo for a flow stream.
// Duration is the <res duration> of a file; 0 is a stream, which cannot seek.
type Meta struct {
	Title, Artist, Album, Art string
	Duration                  time.Duration
}

// Renderer holds the identity + wiring for one MediaRenderer instance.
type Renderer struct {
	UDN     string // "uuid:...."
	Name    string
	IP      string // the speaker's LAN IP (SSDP LOCATION host)
	Port    string // soundtapd's HTTP port
	Backend Backend
	// SubsFile keeps the GENA subscriptions across a restart; "" keeps them in
	// memory only.
	SubsFile string

	// OnPosition, if set, hears where the playing track is on each event tick.
	OnPosition func(position, duration time.Duration)

	mu   sync.Mutex
	uri  string // the last accepted SetAVTransportURI, so Play can restart it
	meta Meta
	// next is the SetNextAVTransportURI queued behind it. armed says a track this renderer
	// started may run out into it: Stop disarms, so a stopped track does not move on.
	next     string
	nextMeta Meta
	armed    bool
	// elapsed is how long the current URI has played, counted on the event tick while
	// PLAYING. GetPositionInfo reports it: at 0:00:00 Music Assistant's progress bar
	// stood still. A restart from the top resets it.
	elapsed  time.Duration
	lastTick time.Time

	events eventer
}

const (
	ssdpAddr   = "239.255.255.250:1900"
	rootDevice = "urn:schemas-upnp-org:device:MediaRenderer:1"
	avTransURN = "urn:schemas-upnp-org:service:AVTransport:1"
	renderURN  = "urn:schemas-upnp-org:service:RenderingControl:1"
	connMgrURN = "urn:schemas-upnp-org:service:ConnectionManager:1"
)

// host is the LAN IP for the SSDP LOCATION. If IP wasn't set (network not up at start),
// resolve it live so late-configured Wi-Fi still advertises a reachable address.
func (r *Renderer) host() string {
	if r.IP != "" {
		return r.IP
	}
	return outboundIP()
}

func (r *Renderer) location() string {
	return fmt.Sprintf("http://%s:%s/upnp/desc.xml", r.host(), r.Port)
}

// outboundIP returns the primary non-loopback IPv4 by inspecting the route to a public
// address (no packets are sent for a UDP "connect"). Empty on failure.
func outboundIP() string {
	c, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer func() { _ = c.Close() }()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// Routes registers the renderer's HTTP endpoints on mux.
func (r *Renderer) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /upnp/desc.xml", r.desc)
	mux.HandleFunc("POST /upnp/control/avtransport", r.avControl)
	mux.HandleFunc("POST /upnp/control/renderingcontrol", r.rcControl)
	mux.HandleFunc("POST /upnp/control/connectionmanager", cmControl)
	mux.HandleFunc("/upnp/event/avtransport", r.subscribeHandler(avTransURN))
	mux.HandleFunc("/upnp/event/renderingcontrol", r.subscribeHandler(renderURN))
	mux.HandleFunc("/upnp/event/connectionmanager", r.subscribeHandler(connMgrURN))
	for path, doc := range map[string]string{
		"/upnp/avtransport.xml":       avTransportSCPD,
		"/upnp/renderingcontrol.xml":  renderingControlSCPD,
		"/upnp/connectionmanager.xml": connectionManagerSCPD,
	} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) { writeXML(w, doc) })
	}
}

func (r *Renderer) desc(w http.ResponseWriter, _ *http.Request) {
	// friendlyName is user-settable; escape it or a name with & breaks the XML and
	// controllers drop the device.
	writeXML(w, fmt.Sprintf(descTmpl, xmlEscape(r.Name), r.UDN))
}

// arg matches one SOAP argument element: controllers send it with or without a namespace
// prefix and with attributes (`<CurrentURI xmlns:dt="..." dt:dt="string">`), not only bare.
func arg(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<(?:[\w-]+:)?` + name + `(?:\s[^>]*)?>(.*?)</(?:[\w-]+:)?` + name + `>`)
}

var (
	reURI      = arg("CurrentURI")
	reMeta     = arg("CurrentURIMetaData")
	reNextURI  = arg("NextURI")
	reNextMeta = arg("NextURIMetaData")
	reTitle    = arg("dc:title")
	reArtist   = arg("upnp:artist")
	reAlbum    = arg("upnp:album")
	rePlayMode = arg("NewPlayMode")
	reCreator  = arg("dc:creator")
	reArt      = arg("upnp:albumArtURI")
	reDuration = regexp.MustCompile(`<(?:[\w-]+:)?res\s[^>]*\bduration="([^"]*)"`)
	reTarget   = arg("Target")
	reUnit     = arg("Unit")
	reVolume   = arg("DesiredVolume")
	reMute     = arg("DesiredMute")
	reChannel  = arg("Channel")
)

// avControl handles the AVTransport SOAP actions we support.
func (r *Renderer) avControl(w http.ResponseWriter, req *http.Request) {
	action := soapAction(req.Header.Get("SOAPACTION"))
	body, ok := readBody(w, req)
	if !ok {
		return
	}
	ctx := req.Context()
	switch action {
	case "SetAVTransportURI", "SetNextAVTransportURI":
		reU, reM := reURI, reMeta
		if action == "SetNextAVTransportURI" {
			reU, reM = reNextURI, reNextMeta
		}
		uri := unescapeXML(firstGroup(reU, body))
		meta := didlMeta(unescapeXML(firstGroup(reM, body)))
		// DLNA control is unauthenticated by design, so treat the URI as hostile: it
		// is handed straight to ffmpeg.
		if !MediaURL(ctx, uri) {
			soapFail(w, 402, "Invalid Args")
			return
		}
		if action == "SetNextAVTransportURI" {
			// Played when the current track ends (advance), so a controller with its own
			// queue, Music Assistant without flow mode among them, moves on.
			r.mu.Lock()
			r.next, r.nextMeta = uri, meta
			r.mu.Unlock()
			soapOK(w, action, avTransURN, "")
			return
		}
		if err := r.Backend.PlayURL(ctx, uri, meta, 0); err != nil {
			soapFail(w, 501, "Action Failed")
			return
		}
		r.mu.Lock()
		r.uri, r.meta, r.next, r.nextMeta, r.armed, r.elapsed = uri, meta, "", Meta{}, true, 0
		r.mu.Unlock()
		soapOK(w, "SetAVTransportURI", avTransURN, "")
	case "Play":
		// SetAVTransportURI already started playback; Play after Stop restarts the track
		// from the top, and so does Play after Pause on a live stream, which cannot seek. A
		// file with a duration resumes where it was paused. A stream still buffering
		// is left alone.
		if st := r.Backend.TransportState(ctx); st != "PLAYING" && st != "TRANSITIONING" {
			uri, meta := r.current()
			if uri == "" {
				soapFail(w, 701, "Transition not available")
				return
			}
			r.mu.Lock()
			at := r.elapsed
			r.mu.Unlock()
			if st != "PAUSED_PLAYBACK" || meta.Duration == 0 || at >= meta.Duration {
				at = 0
			}
			if err := r.Backend.PlayURL(ctx, uri, meta, at); err != nil {
				soapFail(w, 501, "Action Failed")
				return
			}
			r.mu.Lock()
			r.elapsed = at
			r.mu.Unlock()
		}
		r.mu.Lock()
		r.armed = true
		r.mu.Unlock()
		soapOK(w, "Play", avTransURN, "")
	case "Stop", "Pause":
		do := r.Backend.Stop
		if action == "Pause" {
			do = r.Backend.Pause
		}
		if err := do(ctx); err != nil {
			soapFail(w, 501, "Action Failed")
			return
		}
		if action == "Stop" {
			r.mu.Lock()
			r.armed = false
			r.mu.Unlock()
		}
		soapOK(w, action, avTransURN, "")
	case "Seek":
		r.seek(w, req, body)
	case "GetTransportInfo":
		st := r.Backend.TransportState(ctx)
		soapOK(w, "GetTransportInfo", avTransURN,
			"<CurrentTransportState>"+st+"</CurrentTransportState>"+
				"<CurrentTransportStatus>OK</CurrentTransportStatus>"+
				"<CurrentSpeed>1</CurrentSpeed>")
	case "GetPositionInfo":
		// The duration is the controller's own, from the DIDL; a stream has none.
		uri, meta := r.current()
		r.mu.Lock()
		pos := hms(r.elapsed)
		r.mu.Unlock()
		soapOK(w, "GetPositionInfo", avTransURN,
			"<Track>1</Track><TrackDuration>"+hms(meta.Duration)+"</TrackDuration><TrackMetaData>"+xmlEscape(meta.didl())+"</TrackMetaData>"+
				"<TrackURI>"+xmlEscape(uri)+"</TrackURI><RelTime>"+pos+"</RelTime><AbsTime>"+pos+"</AbsTime>"+
				"<RelCount>0</RelCount><AbsCount>0</AbsCount>")
	case "GetMediaInfo":
		uri, meta := r.current()
		r.mu.Lock()
		next, nextMeta := r.next, r.nextMeta
		r.mu.Unlock()
		n := "0"
		if uri != "" {
			n = "1"
		}
		soapOK(w, "GetMediaInfo", avTransURN,
			"<NrTracks>"+n+"</NrTracks><MediaDuration>"+hms(meta.Duration)+"</MediaDuration><CurrentURI>"+xmlEscape(uri)+"</CurrentURI>"+
				"<CurrentURIMetaData>"+xmlEscape(meta.didl())+"</CurrentURIMetaData><NextURI>"+xmlEscape(next)+"</NextURI>"+
				"<NextURIMetaData>"+xmlEscape(nextMeta.didl())+"</NextURIMetaData>"+
				"<PlayMedium>NETWORK</PlayMedium><RecordMedium>NOT_IMPLEMENTED</RecordMedium><WriteStatus>NOT_IMPLEMENTED</WriteStatus>")
	case "GetCurrentTransportActions":
		actions := "Play,Stop,Pause"
		if _, meta := r.current(); meta.Duration > 0 {
			actions += ",Seek"
		}
		r.mu.Lock()
		if r.next != "" {
			actions += ",Next"
		}
		r.mu.Unlock()
		soapOK(w, action, avTransURN, "<Actions>"+actions+"</Actions>")
	case "Next":
		r.skip(w, req)
	case "Previous":
		// The controller holds its queue; this renderer only ever knows the next track.
		soapFail(w, 711, "Illegal seek target")
	case "SetPlayMode":
		if strings.TrimSpace(firstGroup(rePlayMode, body)) != "NORMAL" {
			soapFail(w, 712, "Play mode not supported")
			return
		}
		soapOK(w, action, avTransURN, "")
	case "GetTransportSettings":
		soapOK(w, action, avTransURN, "<PlayMode>NORMAL</PlayMode><RecQualityMode>NOT_IMPLEMENTED</RecQualityMode>")
	case "GetDeviceCapabilities":
		soapOK(w, action, avTransURN, "<PlayMedia>NETWORK</PlayMedia><RecMedia>NOT_IMPLEMENTED</RecMedia>"+
			"<RecQualityModes>NOT_IMPLEMENTED</RecQualityModes>")
	default:
		soapFail(w, 401, "Invalid Action")
	}
}

// skip starts the queued next URI now, the way advance does when a track runs out.
func (r *Renderer) skip(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	next, meta := r.next, r.nextMeta
	r.mu.Unlock()
	if next == "" {
		soapFail(w, 711, "Illegal seek target")
		return
	}
	if err := r.Backend.PlayURL(req.Context(), next, meta, 0); err != nil {
		soapFail(w, 501, "Action Failed")
		return
	}
	r.mu.Lock()
	if r.next == next {
		r.uri, r.meta, r.next, r.nextMeta, r.armed, r.elapsed = next, meta, "", Meta{}, true, 0
	}
	r.mu.Unlock()
	soapOK(w, "Next", avTransURN, "")
}

// seek moves a track with a known duration to a REL_TIME/ABS_TIME target. A
// paused track only takes the new position, which its Play starts from; otherwise the
// track starts again at the target. A live stream has no position to move to.
func (r *Renderer) seek(w http.ResponseWriter, req *http.Request, body string) {
	ctx := req.Context()
	uri, meta := r.current()
	if unit := strings.TrimSpace(firstGroup(reUnit, body)); unit != "REL_TIME" && unit != "ABS_TIME" {
		soapFail(w, 710, "Seek mode not supported")
		return
	}
	if uri == "" || meta.Duration == 0 {
		soapFail(w, 710, "Seek mode not supported")
		return
	}
	at, ok := parseHMS(firstGroup(reTarget, body))
	if !ok {
		soapFail(w, 711, "Illegal seek target")
		return
	}
	switch err := r.seekTo(ctx, uri, meta, at); {
	case errors.Is(err, ErrSeekTarget):
		soapFail(w, 711, "Illegal seek target")
	case err != nil:
		soapFail(w, 501, "Action Failed")
	default:
		soapOK(w, "Seek", avTransURN, "")
	}
}

// ErrNoSeek is a seek with no track of known length on; ErrSeekTarget one past its end.
var (
	ErrNoSeek     = errors.New("upnp: nothing to seek in")
	ErrSeekTarget = errors.New("upnp: seek target past the end")
)

// SeekTo moves the current track to at, as a controller's Seek does: the speaker's own
// time bar uses it.
func (r *Renderer) SeekTo(ctx context.Context, at time.Duration) error {
	uri, meta := r.current()
	if uri == "" || meta.Duration == 0 {
		return ErrNoSeek
	}
	return r.seekTo(ctx, uri, meta, at)
}

func (r *Renderer) seekTo(ctx context.Context, uri string, meta Meta, at time.Duration) error {
	if at < 0 || at >= meta.Duration {
		return ErrSeekTarget
	}
	if r.Backend.TransportState(ctx) != "PAUSED_PLAYBACK" {
		if err := r.Backend.PlayURL(ctx, uri, meta, at); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.elapsed, r.armed = at, true
	r.mu.Unlock()
	return nil
}

// parseHMS reads a UPnP H+:MM:SS[.F+] time, as a DIDL <res duration> or a Seek target.
func parseHMS(v string) (time.Duration, bool) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	// Hours past what a Duration holds overflowed into a negative Seek target or duration.
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || h >= int(math.MaxInt64/int64(time.Hour)) ||
		m < 0 || m > 59 || !(sec >= 0 && sec < 60) { // !(…) also refuses NaN
		return 0, false
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second)), true
}

// advance starts the queued next URI once the current track has ended: STOPPED, with
// the renderer armed. Stop disarms it, and another source taking over reads as
// NO_MEDIA_PRESENT, so neither moves the queue on.
func (r *Renderer) advance(ctx context.Context) {
	r.mu.Lock()
	next, meta, armed := r.next, r.nextMeta, r.armed
	r.mu.Unlock()
	if !armed || next == "" || r.Backend.TransportState(ctx) != "STOPPED" {
		return
	}
	err := r.Backend.PlayURL(ctx, next, meta, 0)
	r.mu.Lock()
	if err != nil {
		r.armed = false // once, not every second; the controller's own Play retries
	} else if r.next == next {
		r.uri, r.meta, r.next, r.nextMeta, r.elapsed = next, meta, "", Meta{}, 0
	}
	r.mu.Unlock()
}

// countPosition adds the time since the last tick to elapsed while the URI plays.
func (r *Renderer) countPosition(ctx context.Context, now time.Time) {
	uri, _ := r.current()
	playing := uri != "" && r.Backend.TransportState(ctx) == "PLAYING"
	r.mu.Lock()
	if playing && !r.lastTick.IsZero() {
		r.elapsed += now.Sub(r.lastTick)
	}
	r.lastTick = time.Time{}
	if playing {
		r.lastTick = now
	}
	elapsed, duration := r.elapsed, r.meta.Duration
	r.mu.Unlock()
	if playing && r.OnPosition != nil {
		r.OnPosition(elapsed, duration)
	}
}

// hms formats d as the H:MM:SS of a UPnP time position.
func hms(d time.Duration) string {
	s := int(d / time.Second)
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

// current is the last accepted SetAVTransportURI.
func (r *Renderer) current() (string, Meta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.uri, r.meta
}
