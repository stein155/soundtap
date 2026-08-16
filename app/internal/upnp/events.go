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
)

// A redirect would send the NOTIFY (or a GET) to a host other than the subscriber's,
// which callbackURL has checked, so the callback answer is taken as it is.
var notifyClient = &http.Client{
	Timeout:       3 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
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

// savedSub is a subscription as kept in eventer.file.
type savedSub struct {
	Service, Callback string
	Expires           time.Time
	Seq               uint32
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
	for sid, s := range e.subs {
		out[sid] = savedSub{s.service, s.callback, s.expires, s.seq}
	}
	b, err := json.Marshal(out) // strings, a time and a counter: nothing that fails to marshal
	if err == nil {
		err = atomicjson.WriteFile(e.file, b, 0o600)
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
	b, err := os.ReadFile(e.file)
	if err != nil {
		return
	}
	var in map[string]savedSub
	if json.Unmarshal(b, &in) != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for sid, s := range in {
		if now.Before(s.Expires) && len(e.subs) < maxSubscriptions {
			if e.subs == nil {
				e.subs = map[string]*subscription{}
			}
			e.subs[sid] = &subscription{service: s.Service, callback: s.Callback, expires: s.Expires, seq: s.Seq}
		}
	}
}

// subscribeHandler answers SUBSCRIBE (new or renewal) and UNSUBSCRIBE for one service.
func (r *Renderer) subscribeHandler(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		e := &r.events
		sid := strings.TrimSpace(req.Header.Get("SID"))
		e.mu.Lock()
		defer e.mu.Unlock()
		switch req.Method {
		case "UNSUBSCRIBE":
			if s := e.subs[sid]; s == nil || s.service != service {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			delete(e.subs, sid)
			e.save()
		case "SUBSCRIBE":
			timeout := subTimeout(req.Header.Get("TIMEOUT"))
			if sid != "" { // renewal
				s := e.subs[sid]
				if s == nil || s.service != service {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				s.expires = time.Now().Add(timeout)
			} else {
				cb, ok := callbackURL(req)
				if !ok || req.Header.Get("NT") != "upnp:event" {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
				e.prune(time.Now())
				if len(e.subs) >= maxSubscriptions {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				if e.subs == nil {
					e.subs = map[string]*subscription{}
				}
				sid = newSID()
				e.subs[sid] = &subscription{service: service, callback: cb, expires: time.Now().Add(timeout)}
			}
			e.save()
			w.Header().Set("SID", sid)
			w.Header().Set("TIMEOUT", "Second-"+strconv.Itoa(int(timeout/time.Second)))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// callbackURL takes the first URL of the CALLBACK header. It must point back at the host
// that subscribes: the renderer is unauthenticated, and any other target would let a
// client aim its NOTIFYs at a third machine.
func callbackURL(req *http.Request) (string, bool) {
	h := strings.TrimSpace(req.Header.Get("CALLBACK"))
	if !strings.HasPrefix(h, "<") || !strings.Contains(h, ">") {
		return "", false
	}
	u, err := url.Parse(h[1:strings.Index(h, ">")])
	if err != nil || u.Scheme != "http" {
		return "", false
	}
	peer, _, _ := net.SplitHostPort(req.RemoteAddr)
	if ip := net.ParseIP(u.Hostname()); ip == nil || !ip.Equal(net.ParseIP(peer)) {
		return "", false
	}
	return u.String(), true
}

func subTimeout(h string) time.Duration {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(h), "Second-"))
	if err != nil || time.Duration(n)*time.Second > maxSubTimeout {
		return maxSubTimeout
	}
	return max(time.Duration(n)*time.Second, minSubTimeout)
}

func newSID() string {
	b := make([]byte, 16)
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
	t := time.NewTicker(eventEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			r.countPosition(ctx, now)
		}
		r.advance(ctx)
		r.sendEvents(ctx)
	}
}

// sendEvents sends each subscriber its service's properties when they differ from what
// that subscriber last got. The first event after SUBSCRIBE carries the whole state.
func (r *Renderer) sendEvents(ctx context.Context) {
	e := &r.events
	e.mu.Lock()
	e.prune(time.Now())
	n := len(e.subs)
	e.mu.Unlock()
	if n == 0 {
		return
	}
	state := r.properties(ctx)
	type delivery struct {
		sid, callback, body string
		seq                 uint32
	}
	var todo []delivery
	e.mu.Lock()
	for sid, s := range e.subs {
		if body := state[s.service]; body != s.sent {
			todo = append(todo, delivery{sid, s.callback, body, s.seq})
			s.sent = body
			s.seq++
		}
	}
	if len(todo) > 0 {
		e.save() // the SEQ moved on, and a controller expects it never to go back
	}
	e.mu.Unlock()
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
			`<CurrentTrackDuration val="` + hms(meta.Duration) + `"/><CurrentMediaDuration val="` + hms(meta.Duration) + `"/>` +
			`<NextAVTransportURI val="` + xmlEscape(next) + `"/></InstanceID></Event>`),
		connMgrURN: `<e:property><SourceProtocolInfo></SourceProtocolInfo></e:property>` +
			`<e:property><SinkProtocolInfo>` + sinkProtocols + `</SinkProtocolInfo></e:property>` +
			`<e:property><CurrentConnectionIDs>0</CurrentConnectionIDs></e:property>`,
	}
	if v, err := r.Backend.Volume(ctx); err == nil {
		mute := "0"
		if v == 0 {
			mute = "1"
		}
		state[renderURN] = lastChange(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0">` +
			`<Volume channel="Master" val="` + strconv.Itoa(v) + `"/>` +
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
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	req.Header.Set("SID", sid)
	req.Header.Set("SEQ", strconv.FormatUint(uint64(seq), 10))
	if resp, err := notifyClient.Do(req); err == nil {
		resp.Body.Close()
	}
}
