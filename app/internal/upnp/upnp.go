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
	// Volume is the level the speaker keeps, also while it is muted.
	Volume(ctx context.Context) (int, error)
	SetVolume(ctx context.Context, level int) error
	// Muted and SetMute are the mute beside it: the volume stays the level an unmute
	// comes back to.
	Muted() bool
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

// AVTransport's actions that more than one place names.
const (
	actSetAVTransportURI     = "SetAVTransportURI"
	actSetNextAVTransportURI = "SetNextAVTransportURI"
	actPlay                  = "Play"
	actPause                 = "Pause"
	actStop                  = "Stop"
)

// Transport states a Backend reports.
const (
	statePlaying       = "PLAYING"
	stateStopped       = "STOPPED"
	statePaused        = "PAUSED_PLAYBACK"
	stateTransitioning = "TRANSITIONING"
)

// ErrNoSeek is a seek with no track of known length on; ErrSeekTarget one past its end.
var (
	ErrNoSeek     = errors.New("upnp: nothing to seek in")
	ErrSeekTarget = errors.New("upnp: seek target past the end")
)

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
		"/upnp/avtransport.xml":       avTransportSCPD(),
		"/upnp/renderingcontrol.xml":  renderingControlSCPD(),
		"/upnp/connectionmanager.xml": connectionManagerSCPD(),
	} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) { writeXML(w, doc) })
	}
}

// SeekTo moves the current track to position, as a controller's Seek does: the speaker's own
// time bar uses it.
func (r *Renderer) SeekTo(ctx context.Context, position time.Duration) error {
	uri, meta := r.current()
	if uri == "" || meta.Duration == 0 {
		return ErrNoSeek
	}

	return r.moveTo(ctx, uri, meta, position)
}

// host is the LAN IP for the SSDP LOCATION. If IP wasn't set (network not up at start),
// resolve it live so late-configured Wi-Fi still advertises a reachable address.
func (r *Renderer) host(ctx context.Context) string {
	if r.IP != "" {
		return r.IP
	}

	return outboundIP(ctx)
}

// location is the device description's URL. UPnP fetches it over plain HTTP.
func (r *Renderer) location(ctx context.Context) string {
	return "http://" + r.host(ctx) + ":" + r.Port + "/upnp/desc.xml"
}

// outboundIP returns the primary non-loopback IPv4 by inspecting the route to a public
// address (no packets are sent for a UDP "connect"). Empty on failure.
func outboundIP(ctx context.Context) string {
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}

	return ""
}

func (r *Renderer) desc(w http.ResponseWriter, _ *http.Request) {
	// friendlyName is user-settable; escape it or a name with & breaks the XML and
	// controllers drop the device.
	writeXML(w, fmt.Sprintf(descTmpl, xmlEscape(r.Name), r.UDN))
}

// argPattern matches one SOAP argument element: controllers send it with or without a
// namespace prefix and with attributes (`<CurrentURI xmlns:dt="..." dt:dt="string">`), not
// only bare.
func argPattern(name string) string {
	return `(?s)<(?:[\w-]+:)?` + name + `(?:\s[^>]*)?>(.*?)</(?:[\w-]+:)?` + name + `>`
}

var (
	reURI      = regexp.MustCompile(argPattern("CurrentURI"))
	reMeta     = regexp.MustCompile(argPattern("CurrentURIMetaData"))
	reNextURI  = regexp.MustCompile(argPattern("NextURI"))
	reNextMeta = regexp.MustCompile(argPattern("NextURIMetaData"))
	reTitle    = regexp.MustCompile(argPattern("dc:title"))
	reArtist   = regexp.MustCompile(argPattern("upnp:artist"))
	reAlbum    = regexp.MustCompile(argPattern("upnp:album"))
	rePlayMode = regexp.MustCompile(argPattern("NewPlayMode"))
	reCreator  = regexp.MustCompile(argPattern("dc:creator"))
	reArt      = regexp.MustCompile(argPattern("upnp:albumArtURI"))
	reDuration = regexp.MustCompile(`<(?:[\w-]+:)?res\s[^>]*\bduration="([^"]*)"`)
	reTarget   = regexp.MustCompile(argPattern("Target"))
	reUnit     = regexp.MustCompile(argPattern("Unit"))
	reVolume   = regexp.MustCompile(argPattern("DesiredVolume"))
	reMute     = regexp.MustCompile(argPattern("DesiredMute"))
	reChannel  = regexp.MustCompile(argPattern("Channel"))
)

// avControl handles the AVTransport SOAP actions we support.
func (r *Renderer) avControl(w http.ResponseWriter, req *http.Request) {
	action := soapAction(req.Header.Get("SoapAction"))

	body, ok := readBody(w, req)
	if !ok {
		return
	}

	ctx := req.Context()

	switch action {
	case actSetAVTransportURI:
		r.setURI(ctx, w, body)
	case actSetNextAVTransportURI:
		r.setNextURI(ctx, w, body)
	case actPlay:
		r.play(ctx, w)
	case actStop:
		r.stop(ctx, w)
	case actPause:
		r.pause(ctx, w)
	case "Seek":
		r.seek(w, req, body)
	case "GetTransportInfo":
		soapOK(w, "GetTransportInfo", avTransURN,
			"<CurrentTransportState>"+r.Backend.TransportState(ctx)+"</CurrentTransportState>"+
				"<CurrentTransportStatus>OK</CurrentTransportStatus>"+
				"<CurrentSpeed>1</CurrentSpeed>")
	case "GetPositionInfo":
		r.positionInfo(w)
	case "GetMediaInfo":
		r.mediaInfo(w)
	case "GetCurrentTransportActions":
		r.transportActions(w)
	case "Next":
		r.skip(w, req)
	case "Previous":
		// The controller holds its queue; this renderer only ever knows the next track.
		soapFail(w, faultIllegalSeekTarget)
	default:
		settingsControl(w, action, body)
	}
}

// settingsControl answers AVTransport's play mode and capability actions, which have
// one fixed answer each, and refuses any other action.
func settingsControl(w http.ResponseWriter, action, body string) {
	switch action {
	case "SetPlayMode":
		if strings.TrimSpace(firstGroup(rePlayMode, body)) != "NORMAL" {
			soapFail(w, faultPlayModeNotSupported)

			return
		}

		soapOK(w, "SetPlayMode", avTransURN, "")
	case "GetTransportSettings":
		soapOK(w, "GetTransportSettings", avTransURN,
			"<PlayMode>NORMAL</PlayMode><RecQualityMode>NOT_IMPLEMENTED</RecQualityMode>")
	case "GetDeviceCapabilities":
		soapOK(w, "GetDeviceCapabilities", avTransURN,
			"<PlayMedia>NETWORK</PlayMedia><RecMedia>NOT_IMPLEMENTED</RecMedia>"+
				"<RecQualityModes>NOT_IMPLEMENTED</RecQualityModes>")
	default:
		soapFail(w, faultInvalidAction)
	}
}

// acceptedURI reads a URI and its DIDL-Lite out of a Set(Next)AVTransportURI body. DLNA
// control is unauthenticated by design, so the URI is hostile until MediaURL passed it: it
// is handed straight to ffmpeg. A refused URI is answered with Invalid Args here.
func acceptedURI(
	ctx context.Context, w http.ResponseWriter, body string, uriArg, metaArg *regexp.Regexp,
) (string, Meta, bool) {
	uri := unescapeXML(firstGroup(uriArg, body))
	meta := didlMeta(unescapeXML(firstGroup(metaArg, body)))

	if !MediaURL(ctx, uri) {
		soapFail(w, faultInvalidArgs)

		return "", meta, false
	}

	return uri, meta, true
}

// setURI starts the URI a SetAVTransportURI names.
func (r *Renderer) setURI(ctx context.Context, w http.ResponseWriter, body string) {
	uri, meta, ok := acceptedURI(ctx, w, body, reURI, reMeta)
	if !ok {
		return
	}

	err := r.Backend.PlayURL(ctx, uri, meta, 0)
	if err != nil {
		soapFail(w, faultActionFailed)

		return
	}

	r.mu.Lock()
	r.startLocked(uri, meta)
	r.armed = true
	r.mu.Unlock()
	soapOK(w, actSetAVTransportURI, avTransURN, "")
}

// setNextURI queues the URI a SetNextAVTransportURI names. It plays when the current
// track ends (advance), so a controller with its own queue, Music Assistant without flow
// mode among them, moves on.
func (r *Renderer) setNextURI(ctx context.Context, w http.ResponseWriter, body string) {
	uri, meta, ok := acceptedURI(ctx, w, body, reNextURI, reNextMeta)
	if !ok {
		return
	}

	r.mu.Lock()
	r.next, r.nextMeta = uri, meta
	r.mu.Unlock()
	soapOK(w, actSetNextAVTransportURI, avTransURN, "")
}

// play answers Play. SetAVTransportURI already started playback; Play after Stop restarts
// the track from the top, and so does Play after Pause on a live stream, which cannot
// seek. A file with a duration resumes where it was paused. A stream still buffering is
// left alone.
func (r *Renderer) play(ctx context.Context, w http.ResponseWriter) {
	if state := r.Backend.TransportState(ctx); state != statePlaying && state != stateTransitioning {
		uri, meta := r.current()
		if uri == "" {
			soapFail(w, faultTransitionNotAvailable)

			return
		}

		r.mu.Lock()
		offset := r.elapsed
		r.mu.Unlock()

		if state != statePaused || meta.Duration == 0 || offset >= meta.Duration {
			offset = 0
		}

		err := r.Backend.PlayURL(ctx, uri, meta, offset)
		if err != nil {
			soapFail(w, faultActionFailed)

			return
		}

		r.mu.Lock()
		r.elapsed = offset
		r.mu.Unlock()
	}

	r.mu.Lock()
	r.armed = true
	r.mu.Unlock()
	soapOK(w, actPlay, avTransURN, "")
}

// stop answers Stop. It disarms, so a stopped track does not run into the queued next one.
func (r *Renderer) stop(ctx context.Context, w http.ResponseWriter) {
	err := r.Backend.Stop(ctx)
	if err != nil {
		soapFail(w, faultActionFailed)

		return
	}

	r.mu.Lock()
	r.armed = false
	r.mu.Unlock()
	soapOK(w, actStop, avTransURN, "")
}

// pause answers Pause: the source is kept, so Play picks the track up again.
func (r *Renderer) pause(ctx context.Context, w http.ResponseWriter) {
	err := r.Backend.Pause(ctx)
	if err != nil {
		soapFail(w, faultActionFailed)

		return
	}

	soapOK(w, actPause, avTransURN, "")
}

// positionInfo answers GetPositionInfo. The duration is the controller's own, from the
// DIDL; a stream has none.
func (r *Renderer) positionInfo(w http.ResponseWriter) {
	uri, meta := r.current()
	r.mu.Lock()
	pos := hms(r.elapsed)
	r.mu.Unlock()
	soapOK(w, "GetPositionInfo", avTransURN,
		"<Track>1</Track><TrackDuration>"+hms(meta.Duration)+"</TrackDuration>"+
			"<TrackMetaData>"+xmlEscape(meta.didl())+"</TrackMetaData>"+
			"<TrackURI>"+xmlEscape(uri)+"</TrackURI><RelTime>"+pos+"</RelTime><AbsTime>"+pos+"</AbsTime>"+
			"<RelCount>0</RelCount><AbsCount>0</AbsCount>")
}

// mediaInfo answers GetMediaInfo: the current URI and the one queued behind it.
func (r *Renderer) mediaInfo(w http.ResponseWriter) {
	uri, meta := r.current()
	r.mu.Lock()
	next, nextMeta := r.next, r.nextMeta
	r.mu.Unlock()

	tracks := "0"
	if uri != "" {
		tracks = "1"
	}

	soapOK(w, "GetMediaInfo", avTransURN,
		"<NrTracks>"+tracks+"</NrTracks><MediaDuration>"+hms(meta.Duration)+"</MediaDuration>"+
			"<CurrentURI>"+xmlEscape(uri)+"</CurrentURI>"+
			"<CurrentURIMetaData>"+xmlEscape(meta.didl())+"</CurrentURIMetaData>"+
			"<NextURI>"+xmlEscape(next)+"</NextURI>"+
			"<NextURIMetaData>"+xmlEscape(nextMeta.didl())+"</NextURIMetaData>"+
			"<PlayMedium>NETWORK</PlayMedium><RecordMedium>NOT_IMPLEMENTED</RecordMedium>"+
			"<WriteStatus>NOT_IMPLEMENTED</WriteStatus>")
}

// transportActions answers GetCurrentTransportActions: Seek with a duration to seek in,
// Next with a track queued.
func (r *Renderer) transportActions(w http.ResponseWriter) {
	actions := "Play,Stop,Pause"
	if _, meta := r.current(); meta.Duration > 0 {
		actions += ",Seek"
	}

	r.mu.Lock()
	if r.next != "" {
		actions += ",Next"
	}
	r.mu.Unlock()
	soapOK(w, "GetCurrentTransportActions", avTransURN, "<Actions>"+actions+"</Actions>")
}

// startLocked makes uri the current track, from its start and with nothing queued behind
// it. The caller holds mu.
func (r *Renderer) startLocked(uri string, meta Meta) {
	var none Meta

	r.uri, r.meta, r.next, r.nextMeta, r.elapsed = uri, meta, "", none, 0
}

// skip starts the queued next URI now, the way advance does when a track runs out.
func (r *Renderer) skip(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	next, meta := r.next, r.nextMeta
	r.mu.Unlock()

	if next == "" {
		soapFail(w, faultIllegalSeekTarget)

		return
	}

	err := r.Backend.PlayURL(req.Context(), next, meta, 0)
	if err != nil {
		soapFail(w, faultActionFailed)

		return
	}

	r.mu.Lock()
	if r.next == next {
		r.startLocked(next, meta)
		r.armed = true
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
		soapFail(w, faultSeekModeNotSupported)

		return
	}

	if uri == "" || meta.Duration == 0 {
		soapFail(w, faultSeekModeNotSupported)

		return
	}

	target, ok := parseHMS(firstGroup(reTarget, body))
	if !ok {
		soapFail(w, faultIllegalSeekTarget)

		return
	}

	switch err := r.moveTo(ctx, uri, meta, target); {
	case errors.Is(err, ErrSeekTarget):
		soapFail(w, faultIllegalSeekTarget)
	case err != nil:
		soapFail(w, faultActionFailed)
	default:
		soapOK(w, "Seek", avTransURN, "")
	}
}

// moveTo is Seek and SeekTo for the track uri: it plays from target, or only takes the
// position while paused.
func (r *Renderer) moveTo(ctx context.Context, uri string, meta Meta, target time.Duration) error {
	if target < 0 || target >= meta.Duration {
		return ErrSeekTarget
	}

	if r.Backend.TransportState(ctx) != statePaused {
		err := r.Backend.PlayURL(ctx, uri, meta, target)
		if err != nil {
			return fmt.Errorf("upnp: seek: %w", err)
		}
	}

	r.mu.Lock()
	r.elapsed, r.armed = target, true
	r.mu.Unlock()

	return nil
}

// hmsFields is how many colon-separated fields a UPnP time has: hours, minutes, seconds.
const hmsFields = 3

// parseHMS reads a UPnP H+:MM:SS[.F+] time, as a DIDL <res duration> or a Seek target.
func parseHMS(v string) (time.Duration, bool) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != hmsFields {
		return 0, false
	}

	hours, err1 := strconv.Atoi(parts[0])
	minutes, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	// Hours past what a Duration holds overflowed into a negative Seek target or duration.
	if err1 != nil || err2 != nil || err3 != nil || hours < 0 || hours >= int(math.MaxInt64/int64(time.Hour)) ||
		minutes < 0 || minutes > 59 || !(sec >= 0 && sec < 60) { // !(…) also refuses NaN
		return 0, false
	}

	return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute +
		time.Duration(sec*float64(time.Second)), true
}

// advance starts the queued next URI once the current track has ended: STOPPED, with
// the renderer armed. Stop disarms it, and another source taking over reads as
// NO_MEDIA_PRESENT, so neither moves the queue on.
func (r *Renderer) advance(ctx context.Context) {
	r.mu.Lock()
	next, meta, armed := r.next, r.nextMeta, r.armed
	r.mu.Unlock()

	if !armed || next == "" || r.Backend.TransportState(ctx) != stateStopped {
		return
	}

	err := r.Backend.PlayURL(ctx, next, meta, 0)
	r.mu.Lock()
	if err != nil {
		r.armed = false // once, not every second; the controller's own Play retries
	} else if r.next == next {
		r.startLocked(next, meta)
	}
	r.mu.Unlock()
}

// countPosition adds the time since the last tick to elapsed while the URI plays.
func (r *Renderer) countPosition(ctx context.Context, now time.Time) {
	uri, _ := r.current()
	playing := uri != "" && r.Backend.TransportState(ctx) == statePlaying
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

// Seconds per hour and per minute, and minutes per hour, for the H:MM:SS of a UPnP time.
const (
	secondsPerHour   = int(time.Hour / time.Second)
	secondsPerMinute = int(time.Minute / time.Second)
	minutesPerHour   = int(time.Hour / time.Minute)
)

// hms formats d as the H:MM:SS of a UPnP time position.
func hms(d time.Duration) string {
	s := int(d / time.Second)

	return fmt.Sprintf("%d:%02d:%02d", s/secondsPerHour, s/secondsPerMinute%minutesPerHour, s%secondsPerMinute)
}

// current is the last accepted SetAVTransportURI.
func (r *Renderer) current() (string, Meta) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.uri, r.meta
}
