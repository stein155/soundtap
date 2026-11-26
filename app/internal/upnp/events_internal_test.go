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
	t.Parallel()

	type note struct{ sid, seq, body string }

	got := make(chan note, 8)

	notes := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- note{r.Header.Get("Sid"), r.Header.Get("Seq"), string(b)}
	}))
	defer notes.Close()

	backend := newFakeBackend("STOPPED", 5)
	renderer := newTestRenderer(backend)
	mux := http.NewServeMux()
	renderer.Routes(mux)

	sub := func(method, cbURL, sid string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, "/upnp/event/avtransport", http.NoBody)

		req.RemoteAddr = "127.0.0.1:40000"
		if cbURL != "" {
			req.Header.Set("Callback", "<"+cbURL+">")
			req.Header.Set("Nt", "upnp:event")
		}

		if sid != "" {
			req.Header.Set("Sid", sid)
		}

		req.Header.Set("Timeout", "Second-300")

		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		return w
	}
	w := sub("SUBSCRIBE", notes.URL+"/ev", "")

	sid := w.Header().Get("Sid")
	if w.Code != 200 || !strings.HasPrefix(sid, "uuid:") || w.Header().Get("Timeout") != "Second-300" {
		t.Fatalf("SUBSCRIBE = %d %v", w.Code, w.Header())
	}

	ctx := context.Background()
	renderer.sendEvents(ctx)

	if n := <-got; n.sid != sid || n.seq != "0" || !strings.Contains(n.body, "TransportState val=&#34;STOPPED&#34;") {
		t.Fatalf("initial event = %+v", n)
	}

	renderer.sendEvents(ctx)

	backend.state = "PLAYING"

	renderer.sendEvents(ctx)

	if n := <-got; n.seq != "1" || !strings.Contains(n.body, "PLAYING") {
		t.Fatalf("change event = %+v", n)
	}

	if w := sub("SUBSCRIBE", "", sid); w.Code != 200 {
		t.Fatalf("renewal = %d", w.Code)
	}

	if w := sub("UNSUBSCRIBE", "", sid); w.Code != 200 {
		t.Fatalf("UNSUBSCRIBE = %d", w.Code)
	}

	backend.state = "STOPPED"

	renderer.sendEvents(ctx)

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
	t.Parallel()

	renderer := newTestRenderer(newFakeBackend("", 0))

	for _, callback := range []string{
		"<http://192.0.2.7:9000/ev>", "<https://127.0.0.1/ev>", "http://127.0.0.1/ev", "",
	} {
		req := httptest.NewRequestWithContext(t.Context(), "SUBSCRIBE", "/upnp/event/avtransport", http.NoBody)
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("Callback", callback)
		req.Header.Set("Nt", "upnp:event")

		w := httptest.NewRecorder()
		renderer.subscribeHandler(avTransURN)(w, req)

		if w.Code != http.StatusPreconditionFailed {
			t.Fatalf("CALLBACK %q = %d", callback, w.Code)
		}
	}
}

// A controller on IPv6 is matched against its own address, a zone or an IPv4-mapped
// spelling notwithstanding; another address is still refused.
func TestCallbackURLOverIPv6(t *testing.T) {
	t.Parallel()

	for _, subscribe := range []struct {
		remote, callback string
		ok               bool
	}{
		{"[fe80::7%wlan0]:40000", "<http://[fe80::7%25wlan0]:9000/ev>", true},
		{"[2001:db8::7]:40000", "<http://[2001:db8::7]:9000/ev>", true},
		{"[::ffff:192.0.2.7]:40000", "<http://192.0.2.7:9000/ev>", true},
		{"[2001:db8::7]:40000", "<http://[2001:db8::8]:9000/ev>", false},
	} {
		req := httptest.NewRequestWithContext(t.Context(), "SUBSCRIBE", "/upnp/event/avtransport", http.NoBody)
		req.RemoteAddr = subscribe.remote
		req.Header.Set("Callback", subscribe.callback)

		if _, ok := callbackURL(req); ok != subscribe.ok {
			t.Errorf("callbackURL(%s from %s) ok = %v, want %v", subscribe.callback, subscribe.remote, ok, subscribe.ok)
		}
	}
}

// Every eventSubURL in the description takes a SUBSCRIBE: async_upnp_client subscribes to
// all three services and drops every subscription when one fails.
func TestEveryEventSubURLSubscribes(t *testing.T) {
	t.Parallel()

	r := newTestRenderer(newFakeBackend("", 0))
	mux := http.NewServeMux()
	r.Routes(mux)

	urls := regexp.MustCompile(`<eventSubURL>([^<]*)</eventSubURL>`).FindAllStringSubmatch(descTmpl, -1)
	if len(urls) != 3 {
		t.Fatalf("eventSubURLs = %v", urls)
	}

	for _, match := range urls {
		req := httptest.NewRequestWithContext(t.Context(), "SUBSCRIBE", match[1], http.NoBody)
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("Callback", "<http://127.0.0.1:9/ev>")
		req.Header.Set("Nt", "upnp:event")

		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK || w.Header().Get("Sid") == "" {
			t.Fatalf("SUBSCRIBE %q = %d", match[1], w.Code)
		}
	}
}

// A subscriber that no longer answers must not hold up the others: NOTIFYs went out one
// by one, and each dead callback cost the 3 s client timeout.
func TestDeadSubscriberDoesNotDelayOthers(t *testing.T) {
	t.Parallel()

	hang := make(chan struct{})

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hang }))
	defer dead.Close()
	defer close(hang)

	got := make(chan struct{}, 8)

	live := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { got <- struct{}{} }))
	defer live.Close()

	renderer := newTestRenderer(newFakeBackend("STOPPED", 5))
	mux := http.NewServeMux()
	renderer.Routes(mux)

	for _, cb := range []string{dead.URL, dead.URL, live.URL} {
		req := httptest.NewRequestWithContext(t.Context(), "SUBSCRIBE", "/upnp/event/avtransport", http.NoBody)
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("Callback", "<"+cb+"/ev>")
		req.Header.Set("Nt", "upnp:event")
		mux.ServeHTTP(httptest.NewRecorder(), req)
	}

	start := time.Now()

	renderer.sendEvents(context.Background())
	<-got

	if d := time.Since(start); d > notifyTimeout+time.Second {
		t.Fatalf("sendEvents took %v with two dead subscribers", d)
	}
}

// A subscriber must not be able to bounce the speaker's NOTIFY to another host.
func TestNotifyDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	type note struct{ seq, body string }

	got := make(chan note, 8)

	notes := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- note{r.Header.Get("Seq"), string(b)}
	}))
	defer notes.Close()

	file := t.TempDir() + "/subs.json"
	fb := newFakeBackend("STOPPED", 5)
	start := func() (*Renderer, *http.ServeMux) {
		renderer := newTestRenderer(fb)
		renderer.SubsFile = file
		renderer.events.file = file
		renderer.events.load(time.Now())

		mux := http.NewServeMux()
		renderer.Routes(mux)

		return renderer, mux
	}
	sub := func(mux *http.ServeMux, cbURL, sid string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), "SUBSCRIBE", "/upnp/event/avtransport", http.NoBody)

		req.RemoteAddr = "127.0.0.1:40000"
		if cbURL != "" {
			req.Header.Set("Callback", "<"+cbURL+">")
			req.Header.Set("Nt", "upnp:event")
		} else {
			req.Header.Set("Sid", sid)
		}

		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		return w
	}
	ctx := context.Background()
	renderer, mux := start()
	sid := sub(mux, notes.URL, "").Header().Get("Sid")

	renderer.sendEvents(ctx)

	if n := <-got; n.seq != "0" {
		t.Fatalf("initial event = %+v", n)
	}

	renderer, mux = start() // the restart
	if w := sub(mux, "", sid); w.Code != 200 {
		t.Fatalf("renewal after restart = %d", w.Code)
	}

	renderer.sendEvents(ctx)

	if n := <-got; n.seq != "1" || !strings.Contains(n.body, "STOPPED") {
		t.Fatalf("first event after restart = %+v", n)
	}
}
