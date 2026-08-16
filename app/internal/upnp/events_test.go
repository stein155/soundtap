// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A subscriber gets the whole state once, then only what changed, in SEQ order, and
// nothing after UNSUBSCRIBE.
func TestEventsReachSubscriberOnChange(t *testing.T) {
	type note struct{ sid, seq, body string }
	got := make(chan note, 8)
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- note{r.Header.Get("SID"), r.Header.Get("SEQ"), string(b)}
	}))
	defer cb.Close()
	fb := &fakeBackend{state: "STOPPED", volume: 5}
	r := &Renderer{Backend: fb}
	mux := http.NewServeMux()
	r.Routes(mux)
	sub := func(method, cbURL, sid string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/upnp/event/avtransport", nil)
		req.RemoteAddr = "127.0.0.1:40000"
		if cbURL != "" {
			req.Header.Set("CALLBACK", "<"+cbURL+">")
			req.Header.Set("NT", "upnp:event")
		}
		if sid != "" {
			req.Header.Set("SID", sid)
		}
		req.Header.Set("TIMEOUT", "Second-300")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	w := sub("SUBSCRIBE", cb.URL+"/ev", "")
	sid := w.Header().Get("SID")
	if w.Code != 200 || !strings.HasPrefix(sid, "uuid:") || w.Header().Get("TIMEOUT") != "Second-300" {
		t.Fatalf("SUBSCRIBE = %d %v", w.Code, w.Header())
	}
	ctx := context.Background()
	r.sendEvents(ctx)
	if n := <-got; n.sid != sid || n.seq != "0" || !strings.Contains(n.body, "TransportState val=&#34;STOPPED&#34;") {
		t.Fatalf("initial event = %+v", n)
	}
	r.sendEvents(ctx)
	fb.state = "PLAYING"
	r.sendEvents(ctx)
	if n := <-got; n.seq != "1" || !strings.Contains(n.body, "PLAYING") {
		t.Fatalf("change event = %+v", n)
	}
	if w := sub("SUBSCRIBE", "", sid); w.Code != 200 {
		t.Fatalf("renewal = %d", w.Code)
	}
	if w := sub("UNSUBSCRIBE", "", sid); w.Code != 200 {
		t.Fatalf("UNSUBSCRIBE = %d", w.Code)
	}
	fb.state = "STOPPED"
	r.sendEvents(ctx)
	select {
	case n := <-got:
		t.Fatalf("event after UNSUBSCRIBE: %+v", n)
	default:
	}
	if w := sub("SUBSCRIBE", "", sid); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("renewing a dropped SID = %d", w.Code)
	}
}

// NOTIFYs only ever go back to the host that subscribed.
func TestSubscribeRefusesAForeignCallback(t *testing.T) {
	r := &Renderer{Backend: &fakeBackend{}}
	for _, cb := range []string{"<http://192.0.2.7:9000/ev>", "<https://127.0.0.1/ev>", "http://127.0.0.1/ev", ""} {
		req := httptest.NewRequest("SUBSCRIBE", "/upnp/event/avtransport", nil)
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("CALLBACK", cb)
		req.Header.Set("NT", "upnp:event")
		w := httptest.NewRecorder()
		r.subscribeHandler(avTransURN)(w, req)
		if w.Code != http.StatusPreconditionFailed {
			t.Fatalf("CALLBACK %q = %d", cb, w.Code)
		}
	}
}

// Every eventSubURL in the description takes a SUBSCRIBE: async_upnp_client subscribes to
// all three services and drops every subscription when one fails.
func TestEveryEventSubURLSubscribes(t *testing.T) {
	r := &Renderer{Backend: &fakeBackend{}}
	mux := http.NewServeMux()
	r.Routes(mux)
	urls := regexp.MustCompile(`<eventSubURL>([^<]*)</eventSubURL>`).FindAllStringSubmatch(descTmpl, -1)
	if len(urls) != 3 {
		t.Fatalf("eventSubURLs = %v", urls)
	}
	for _, u := range urls {
		req := httptest.NewRequest("SUBSCRIBE", u[1], nil)
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("CALLBACK", "<http://127.0.0.1:9/ev>")
		req.Header.Set("NT", "upnp:event")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK || w.Header().Get("SID") == "" {
			t.Fatalf("SUBSCRIBE %q = %d", u[1], w.Code)
		}
	}
}

// A subscriber that no longer answers must not hold up the others: NOTIFYs went out one
// by one, and each dead callback cost the 3 s client timeout.
func TestDeadSubscriberDoesNotDelayOthers(t *testing.T) {
	hang := make(chan struct{})
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hang }))
	defer dead.Close()
	defer close(hang)
	got := make(chan struct{}, 8)
	live := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { got <- struct{}{} }))
	defer live.Close()
	r := &Renderer{Backend: &fakeBackend{state: "STOPPED", volume: 5}}
	mux := http.NewServeMux()
	r.Routes(mux)
	for _, cb := range []string{dead.URL, dead.URL, live.URL} {
		req := httptest.NewRequest("SUBSCRIBE", "/upnp/event/avtransport", nil)
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("CALLBACK", "<"+cb+"/ev>")
		req.Header.Set("NT", "upnp:event")
		mux.ServeHTTP(httptest.NewRecorder(), req)
	}
	start := time.Now()
	r.sendEvents(context.Background())
	<-got
	if d := time.Since(start); d > notifyClient.Timeout+time.Second {
		t.Fatalf("sendEvents took %v with two dead subscribers", d)
	}
}

// A subscriber must not be able to bounce the speaker's NOTIFY to another host.
func TestNotifyDoesNotFollowRedirects(t *testing.T) {
	hit := make(chan string, 2)
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { hit <- r.Method }))
	defer other.Close()
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/debug", http.StatusFound)
	}))
	defer cb.Close()
	notify(context.Background(), cb.URL+"/ev", "uuid:x", 0, "")
	select {
	case m := <-hit:
		t.Fatalf("redirect followed: %s reached the other host", m)
	default:
	}
}

// A soundtapd restart keeps the subscriptions: the renewal still finds its SID, and the
// first event after the restart carries the whole state with the SEQ moving on.
func TestSubscriptionsOutliveARestart(t *testing.T) {
	type note struct{ seq, body string }
	got := make(chan note, 8)
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- note{r.Header.Get("SEQ"), string(b)}
	}))
	defer cb.Close()
	file := t.TempDir() + "/subs.json"
	fb := &fakeBackend{state: "STOPPED", volume: 5}
	start := func() (*Renderer, *http.ServeMux) {
		r := &Renderer{Backend: fb, SubsFile: file}
		r.events.file = file
		r.events.load(time.Now())
		mux := http.NewServeMux()
		r.Routes(mux)
		return r, mux
	}
	sub := func(mux *http.ServeMux, cbURL, sid string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("SUBSCRIBE", "/upnp/event/avtransport", nil)
		req.RemoteAddr = "127.0.0.1:40000"
		if cbURL != "" {
			req.Header.Set("CALLBACK", "<"+cbURL+">")
			req.Header.Set("NT", "upnp:event")
		} else {
			req.Header.Set("SID", sid)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	ctx := context.Background()
	r, mux := start()
	sid := sub(mux, cb.URL, "").Header().Get("SID")
	r.sendEvents(ctx)
	if n := <-got; n.seq != "0" {
		t.Fatalf("initial event = %+v", n)
	}

	r, mux = start() // the restart
	if w := sub(mux, "", sid); w.Code != 200 {
		t.Fatalf("renewal after restart = %d", w.Code)
	}
	r.sendEvents(ctx)
	if n := <-got; n.seq != "1" || !strings.Contains(n.body, "STOPPED") {
		t.Fatalf("first event after restart = %+v", n)
	}
}
