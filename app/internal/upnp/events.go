// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// GENA eventing (UPnP Device Architecture 1.0, section 4) for all three services. Without
// it a controller only polls: Music Assistant showed a stop or a pause made on the
// speaker about 25 s late. The renderer looks at its own state once a second
// while anyone subscribes and sends what moved. ConnectionManager never changes, but it
// is evented too: async_upnp_client subscribes to it and drops every subscription when
// one of them fails.

const (
	maxSubscriptions = 16
	maxSubTimeout    = 30 * time.Minute
	minSubTimeout    = time.Minute
	eventEvery       = time.Second
	// notifyTimeout bounds one NOTIFY to a subscriber.
	notifyTimeout = 3 * time.Second
	// subsFileMode keeps the subscription file to soundtapd: it names the controllers'
	// callback URLs.
	subsFileMode = 0o600
	// sidBytes is the randomness in a SID: a UUID's 128 bits.
	sidBytes = 16
)

// notifyClient sends NOTIFYs. A redirect would send the NOTIFY (or a GET) to a host other
// than the subscriber's, which callbackURL has checked, so the callback answer is taken as
// it is.
func notifyClient() *http.Client {
	return &http.Client{
		Timeout:       notifyTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type subscription struct {
	service  string // avTransURN, renderURN or connMgrURN
	callback string
	expires  time.Time
	seq      uint32
	sent     string // the last properties sent; "" until the initial event
}

type eventer struct {
	mu   sync.Mutex
	subs map[string]*subscription // by SID
	file string                   // where subs outlive a restart; "" keeps them in memory
}

// savedSub is a subscription as kept in eventer.file. The keys are the field names, as
// releases before these tags wrote them.
//
//nolint:tagliatelle // the file's keys stay as earlier releases wrote them
type savedSub struct {
	Service  string    `json:"Service"`
	Callback string    `json:"Callback"`
	Expires  time.Time `json:"Expires"`
	Seq      uint32    `json:"Seq"`
}

// save writes the subscriptions to e.file; the caller holds mu. Without it a soundtapd
// restart (an app update, a crash) dropped them: the controller's renewal then got 412
// and Music Assistant heard nothing from the speaker for up to 8 minutes, until its next
// renewal subscribed afresh. The file lives in /run, so a reboot still starts
// clean; a controller notices a reboot because the speaker went away.
func (e *eventer) save() {
	if e.file == "" {
		return
	}

	out := make(map[string]savedSub, len(e.subs))
	for sid, sub := range e.subs {
		out[sid] = savedSub{sub.service, sub.callback, sub.expires, sub.seq}
	}

	data, err := json.Marshal(out) // strings, a time and a counter: nothing that fails to marshal
	if err == nil {
		err = atomicjson.WriteFile(e.file, data, subsFileMode)
	}

	if err != nil {
		// The subscriptions stay in memory; only a restart in between would lose them.
		slog.Warn("upnp: subscriptions not saved", "path", e.file, "err", err)
	}
}

// load reads the subscriptions save left. Each one gets the whole state as its next
// event, so the controller catches up on whatever changed while soundtapd was down.
func (e *eventer) load(now time.Time) {
	if e.file == "" {
		return
	}

	data, err := os.ReadFile(e.file)
	if err != nil {
		return
	}

	var saved map[string]savedSub
	if json.Unmarshal(data, &saved) != nil {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	for sid, sub := range saved {
		if now.Before(sub.Expires) && len(e.subs) < maxSubscriptions {
			if e.subs == nil {
				e.subs = map[string]*subscription{}
			}

			e.subs[sid] = &subscription{
				service: sub.Service, callback: sub.Callback, expires: sub.Expires, seq: sub.Seq, sent: "",
			}
		}
	}
}

// subscribeHandler answers SUBSCRIBE (new or renewal) and UNSUBSCRIBE for one service.
func (r *Renderer) subscribeHandler(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		events := &r.events
		sid := strings.TrimSpace(req.Header.Get("Sid"))

		events.mu.Lock()
		defer events.mu.Unlock()

		var status int

		switch req.Method {
		case "UNSUBSCRIBE":
			status = events.unsubscribe(service, sid)
		case "SUBSCRIBE":
			status = events.subscribe(w, req, service, sid)
		default:
			status = http.StatusMethodNotAllowed
		}

		w.WriteHeader(status)
	}
}

// unsubscribe drops the subscription sid to service and answers the status to send; the
// caller holds mu.
func (e *eventer) unsubscribe(service, sid string) int {
	if sub := e.subs[sid]; sub == nil || sub.service != service {
		return http.StatusPreconditionFailed
	}

	delete(e.subs, sid)
	e.save()

	return http.StatusOK
}

// subscribe takes a new subscription to service, or renews sid when it is set, sets the
// SID and TIMEOUT headers of the answer and returns its status; the caller holds mu.
func (e *eventer) subscribe(w http.ResponseWriter, req *http.Request, service, sid string) int {
	timeout := subTimeout(req.Header.Get("Timeout"))

	var status int
	if sid != "" {
		status = e.renew(service, sid, timeout)
	} else {
		sid, status = e.add(req, service, timeout)
	}

	if status != http.StatusOK {
		return status
	}

	e.save()
	w.Header().Set("Sid", sid)
	w.Header().Set("Timeout", "Second-"+strconv.Itoa(int(timeout/time.Second)))

	return http.StatusOK
}

// renew extends the subscription sid to service by timeout and answers the status to
// send; the caller holds mu.
func (e *eventer) renew(service, sid string, timeout time.Duration) int {
	sub := e.subs[sid]
	if sub == nil || sub.service != service {
		return http.StatusPreconditionFailed
	}

	sub.expires = time.Now().Add(timeout)

	return http.StatusOK
}

// add takes a new subscription to service for timeout and returns its SID and the status
// to send; the caller holds mu.
func (e *eventer) add(req *http.Request, service string, timeout time.Duration) (string, int) {
	callback, ok := callbackURL(req)
	if !ok || req.Header.Get("Nt") != "upnp:event" {
		return "", http.StatusPreconditionFailed
	}

	e.prune(time.Now())

	if len(e.subs) >= maxSubscriptions {
		return "", http.StatusServiceUnavailable
	}

	if e.subs == nil {
		e.subs = map[string]*subscription{}
	}

	sid := newSID()
	e.subs[sid] = &subscription{
		service: service, callback: callback, expires: time.Now().Add(timeout), seq: 0, sent: "",
	}

	return sid, http.StatusOK
}

// callbackURL takes the first URL of the CALLBACK header. It must point back at the host
// that subscribes: the renderer is unauthenticated, and any other target would let a
// client aim its NOTIFYs at a third machine.
func callbackURL(req *http.Request) (string, bool) {
	h := strings.TrimSpace(req.Header.Get("Callback"))
	if !strings.HasPrefix(h, "<") || !strings.Contains(h, ">") {
		return "", false
	}

	callback, err := url.Parse(h[1:strings.Index(h, ">")])
	if err != nil || callback.Scheme != "http" {
		return "", false
	}

	peer, _, _ := net.SplitHostPort(req.RemoteAddr)
	if ip := net.ParseIP(callback.Hostname()); ip == nil || !ip.Equal(net.ParseIP(peer)) {
		return "", false
	}

	return callback.String(), true
}

func subTimeout(h string) time.Duration {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(h), "Second-"))
	if err != nil || time.Duration(n)*time.Second > maxSubTimeout {
		return maxSubTimeout
	}

	return max(time.Duration(n)*time.Second, minSubTimeout)
}

func newSID() string {
	b := make([]byte, sidBytes)
	_, _ = rand.Read(b)

	return fmt.Sprintf("uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// prune drops expired subscriptions; the caller holds mu.
func (e *eventer) prune(now time.Time) {
	for sid, s := range e.subs {
		if now.After(s.expires) {
			delete(e.subs, sid)
		}
	}
}

// watchEvents moves on to a queued next track and sends what changed to the subscribers,
// once a second, until ctx ends.
func (r *Renderer) watchEvents(ctx context.Context) {
	r.events.file = r.SubsFile
	r.events.load(time.Now())

	ticker := time.NewTicker(eventEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.countPosition(ctx, now)
		}

		r.advance(ctx)
		r.sendEvents(ctx)
	}
}

// sendEvents sends each subscriber its service's properties when they differ from what
// that subscriber last got. The first event after SUBSCRIBE carries the whole state.
func (r *Renderer) sendEvents(ctx context.Context) {
	events := &r.events
	events.mu.Lock()
	events.prune(time.Now())
	n := len(events.subs)
	events.mu.Unlock()

	if n == 0 {
		return
	}

	state := r.properties(ctx)

	type delivery struct {
		sid, callback, body string
		seq                 uint32
	}

	var todo []delivery

	events.mu.Lock()
	for sid, sub := range events.subs {
		if body := state[sub.service]; body != sub.sent {
			todo = append(todo, delivery{sid, sub.callback, body, sub.seq})
			sub.sent = body
			sub.seq++
		}
	}

	if len(todo) > 0 {
		events.save() // the SEQ moved on, and a controller expects it never to go back
	}
	events.mu.Unlock()
	// All at once: a controller that went away without UNSUBSCRIBE (Music Assistant
	// restarted) holds three subscriptions, and one by one each cost the 3 s timeout,
	// so a queued next track waited ~9 s behind them on every change.
	var wg sync.WaitGroup
	for _, d := range todo {
		wg.Go(func() { notify(ctx, d.callback, d.sid, d.seq, d.body) })
	}

	wg.Wait()
}

// properties is the event body of each service for the current state: LastChange for
// AVTransport and RenderingControl, the connection's fixed variables for ConnectionManager.
func (r *Renderer) properties(ctx context.Context) map[string]string {
	uri, meta := r.current()
	r.mu.Lock()
	next := r.next
	r.mu.Unlock()

	state := map[string]string{
		avTransURN: lastChange(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/AVT/"><InstanceID val="0">` +
			`<TransportState val="` + r.Backend.TransportState(ctx) + `"/>` +
			`<TransportStatus val="OK"/><CurrentTransportActions val="Play,Stop,Pause"/>` +
			`<AVTransportURI val="` + xmlEscape(uri) + `"/><CurrentTrackURI val="` + xmlEscape(uri) + `"/>` +
			`<CurrentTrackMetaData val="` + xmlEscape(meta.didl()) + `"/>` +
			`<CurrentTrackDuration val="` + hms(meta.Duration) + `"/>` +
			`<CurrentMediaDuration val="` + hms(meta.Duration) + `"/>` +
			`<NextAVTransportURI val="` + xmlEscape(next) + `"/></InstanceID></Event>`),
		connMgrURN: `<e:property><SourceProtocolInfo></SourceProtocolInfo></e:property>` +
			`<e:property><SinkProtocolInfo>` + sinkProtocols + `</SinkProtocolInfo></e:property>` +
			`<e:property><CurrentConnectionIDs>0</CurrentConnectionIDs></e:property>`,
	}

	volume, err := r.Backend.Volume(ctx)
	if err == nil {
		mute := "0"
		if volume == 0 {
			mute = "1"
		}

		state[renderURN] = lastChange(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0">` +
			`<Volume channel="Master" val="` + strconv.Itoa(volume) + `"/>` +
			`<Mute channel="Master" val="` + mute + `"/></InstanceID></Event>`)
	}

	return state
}

func lastChange(event string) string {
	return `<e:property><LastChange>` + xmlEscape(event) + `</LastChange></e:property>`
}

func notify(ctx context.Context, callback, sid string, seq uint32, properties string) {
	body := xmlDecl + `<e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0">` + properties + `</e:propertyset>`

	req, err := http.NewRequestWithContext(ctx, "NOTIFY", callback, strings.NewReader(body))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("Nt", "upnp:event")
	req.Header.Set("Nts", "upnp:propchange")
	req.Header.Set("Sid", sid)
	req.Header.Set("Seq", strconv.FormatUint(uint64(seq), 10))

	resp, err := notifyClient().Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}
