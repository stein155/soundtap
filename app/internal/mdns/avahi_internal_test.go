// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mdns

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/dbustest"
)

// fakeAvahi is avahi-daemon on a test bus: it records what is published, as one line per
// call, and answers resolves from its tables.
type fakeAvahi struct {
	t    *testing.T
	bus  *dbustest.Bus
	conn *dbus.Conn

	mu        sync.Mutex
	host      string
	calls     []string                     // "SetHostName x", "AddService /g name type port txt", ...
	groups    map[dbus.ObjectPath][]string // live groups and what is in them
	committed map[dbus.ObjectPath]bool
	browsers  []dbus.ObjectPath
	holders   map[string]string   // ResolveHostName answers
	services  map[string]resolved // ResolveService answers, by instance
	next      int
}

var errTimeout = dbus.NewError("org.freedesktop.Avahi.TimeoutError", []any{"Timeout reached"})

func newAvahi(t *testing.T, bus *dbustest.Bus) *fakeAvahi {
	t.Helper()
	fake := &fakeAvahi{
		t: t, bus: bus, conn: bus.Connect(t), mu: sync.Mutex{}, host: "soundtap.local", calls: nil,
		groups: map[dbus.ObjectPath][]string{}, committed: map[dbus.ObjectPath]bool{}, browsers: nil,
		holders: map[string]string{}, services: map[string]resolved{}, next: 0,
	}
	path := func(msg dbus.Message) dbus.ObjectPath {
		objPath, _ := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)

		return objPath
	}
	server := map[string]any{
		"GetState": func() (int32, *dbus.Error) { return serverRunning, nil },
		"SetHostName": func(name string) *dbus.Error {
			fake.mu.Lock()
			defer fake.mu.Unlock()

			fake.calls = append(fake.calls, "SetHostName "+name)
			if fake.host == name+".local" {
				return dbus.NewError("org.freedesktop.Avahi.NoChangeError",
					[]any{"The requested operation is invalid because redundant"})
			}

			fake.host = name + ".local"

			return nil
		},
		"GetHostNameFqdn": func() (string, *dbus.Error) {
			fake.mu.Lock()
			defer fake.mu.Unlock()

			return fake.host, nil
		},
		"EntryGroupNew": func() (dbus.ObjectPath, *dbus.Error) {
			fake.mu.Lock()
			defer fake.mu.Unlock()

			fake.next++
			p := dbus.ObjectPath(fmt.Sprintf("/Client1/EntryGroup%d", fake.next))
			fake.groups[p] = nil

			return p, nil
		},
		"GetAlternativeServiceName": func(name string) (string, *dbus.Error) { return name + " #2", nil },
		"ResolveHostName": func(_, _ int32, name string, _ int32, _ uint32) (
			int32, int32, string, int32, string, uint32, *dbus.Error,
		) {
			fake.mu.Lock()
			defer fake.mu.Unlock()

			holder, ok := fake.holders[name]
			if !ok {
				return 0, 0, "", 0, "", 0, errTimeout
			}

			return 2, 0, name, 0, holder, 0, nil
		},
		"ResolveService": func(iface, _ int32, name, typ, _ string, _ int32, _ uint32) (
			int32, int32, string, string, string, string, int32, string, uint16, [][]byte, uint32, *dbus.Error,
		) {
			fake.mu.Lock()
			svc, ok := fake.services[name+"."+typ]
			fake.mu.Unlock()

			if !ok {
				return 0, 0, "", "", "", "", 0, "", 0, nil, 0, errTimeout
			}

			txt := make([][]byte, 0, len(svc.txt))
			for k, v := range svc.txt {
				txt = append(txt, []byte(k+"="+v))
			}

			return iface, 0, name, typ, "local", svc.host, 0, svc.ip.String(), svc.port, txt, 0, nil
		},
		"ServiceBrowserNew": func(_, _ int32, typ, _ string, _ uint32) (dbus.ObjectPath, *dbus.Error) {
			fake.mu.Lock()
			defer fake.mu.Unlock()

			fake.next++
			p := dbus.ObjectPath(fmt.Sprintf("/Client1/ServiceBrowser%d", fake.next))
			fake.browsers = append(fake.browsers, p)
			fake.calls = append(fake.calls, "ServiceBrowserNew "+typ)

			return p, nil
		},
	}
	group := map[string]any{
		"AddService": func(msg dbus.Message, _, _ int32, flags uint32, name, typ, domain, host string, port uint16,
			txt [][]byte,
		) *dbus.Error {
			items := make([]string, 0, len(txt))
			for _, b := range txt {
				items = append(items, string(b))
			}

			fake.add(path(msg), fmt.Sprintf("AddService %s %s %d %d [%s] domain=%q host=%q",
				name, typ, port, flags, strings.Join(items, " "), domain, host))

			return nil
		},
		"AddAddress": func(msg dbus.Message, _, _ int32, flags uint32, name, addr string) *dbus.Error {
			fake.add(path(msg), fmt.Sprintf("AddAddress %s %s %d", name, addr, flags))

			return nil
		},
		"Commit": func(msg dbus.Message) *dbus.Error {
			fake.mu.Lock()
			fake.committed[path(msg)] = true
			fake.mu.Unlock()

			return nil
		},
		"Reset": func(msg dbus.Message) *dbus.Error {
			fake.mu.Lock()
			fake.groups[path(msg)] = nil
			fake.committed[path(msg)] = false
			fake.mu.Unlock()

			return nil
		},
		"Free": func(msg dbus.Message) *dbus.Error {
			fake.mu.Lock()
			delete(fake.groups, path(msg))
			delete(fake.committed, path(msg))
			fake.mu.Unlock()

			return nil
		},
	}

	browser := map[string]any{"Free": func(dbus.Message) *dbus.Error { return nil }}
	for _, table := range []struct {
		methods map[string]any
		path    dbus.ObjectPath
		iface   string
		subtree bool
	}{
		{server, "/", avahiServer, false},
		{group, "/Client1", avahiEntryGroup, true},
		{browser, "/Client1", avahiBrowser, true},
	} {
		export := fake.conn.ExportMethodTable
		if table.subtree {
			export = fake.conn.ExportSubtreeMethodTable
		}

		err := export(table.methods, table.path, table.iface)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err := fake.conn.RequestName(avahiName, 0)
	if err != nil {
		t.Fatal(err)
	}

	return fake
}

// setService makes ResolveService answer for "<instance>.<type>".
func (f *fakeAvahi) setService(key string, r resolved) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.services[key] = r
}

func (f *fakeAvahi) add(p dbus.ObjectPath, what string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.groups[p] = append(f.groups[p], what)
	f.calls = append(f.calls, what)
}

// published lists what the live, committed groups hold, sorted.
func (f *fakeAvahi) published() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string

	for p, entries := range f.groups {
		if f.committed[p] {
			out = append(out, entries...)
		}
	}

	slices.Sort(out)

	return out
}

// groupOf returns the live group holding an entry that starts with prefix.
func (f *fakeAvahi) groupOf(prefix string) dbus.ObjectPath {
	f.mu.Lock()
	defer f.mu.Unlock()

	for p, entries := range f.groups {
		for _, e := range entries {
			if strings.HasPrefix(e, prefix) {
				return p
			}
		}
	}

	return ""
}

func (f *fakeAvahi) emit(path dbus.ObjectPath, name string, body ...any) {
	err := f.conn.Emit(path, name, body...)
	if err != nil {
		f.t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()

	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}

	t.Fatalf("timed out waiting for %s", what)
}

// The tests' retry periods, so they need not wait for a speaker's.
const (
	testSharedRetry  = 100 * time.Millisecond
	testRestartDelay = 50 * time.Millisecond
)

// responder runs a speaker named Keuken on bus until the test ends, retrying fast.
// browseRefresh 0 keeps a speaker's.
func responder(t *testing.T, bus *dbustest.Bus, browseRefresh time.Duration) (*Responder, chan Peer) {
	t.Helper()

	speaker := New("192.0.2.10", "Keuken", slog.New(slog.DiscardHandler))
	speaker.bus = bus.Addr
	speaker.sharedRetry, speaker.restartDelay = testSharedRetry, testRestartDelay

	if browseRefresh != 0 {
		speaker.browseRefresh = browseRefresh
	}

	peers := make(chan Peer, 16)
	speaker.EnableDNSService(
		Service{ID: "SERIAL0000000010A", Model: "ST10", Firmware: "0.1.0", API: 1, Port: 80}, peers)
	speaker.EnableSharedName("SoundTap")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() { _ = speaker.Run(ctx); close(done) }()

	t.Cleanup(func() { cancel(); <-done })

	return speaker, peers
}

const wantService = `AddService Keuken _soundtap._tcp 80 0 ` +
	`[id=SERIAL0000000010A name=Keuken model=ST10 fw=0.1.0 api=1] domain="" host=""`

// What avahi-set-host-name, avahi-publish -s and avahi-publish -a -R did, over D-Bus.
func TestPublishesHostServiceAndSharedName(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	speaker, _ := responder(t, bus, 0)
	want := []string{"AddAddress soundtap.local 192.0.2.10 16", wantService}

	waitFor(t, "service and shared name", func() bool { return slices.Equal(fake.published(), want) })
	waitFor(t, "the host name read back", func() bool {
		return speaker.Hostname() == "soundtap-serial0000000010a.local"
	})
	// The host name is set before anything is published (the browser runs on its own).
	fake.mu.Lock()
	calls := slices.DeleteFunc(slices.Clone(fake.calls), func(call string) bool {
		return strings.HasPrefix(call, "ServiceBrowserNew")
	})
	fake.mu.Unlock()

	if len(calls) == 0 || calls[0] != "SetHostName soundtap-serial0000000010a" {
		t.Fatalf("calls %q, want the host name set first", calls)
	}
}

// A rename withdraws the old service and publishes the new one; the shared name follows.
func TestRenameRepublishes(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	speaker, _ := responder(t, bus, 0)
	waitFor(t, "first publish", func() bool { return len(fake.published()) == 2 })
	speaker.SetName("Zolder")
	speaker.SetIP("192.0.2.11")

	want := []string{
		"AddAddress soundtap.local 192.0.2.11 16",
		strings.Replace(strings.Replace(wantService, "Keuken", "Zolder", 1), "name=Keuken", "name=Zolder", 1),
	}

	waitFor(t, "the new name and address", func() bool { return slices.Equal(fake.published(), want) })
}

// An instance name another host has is renamed as avahi-publish -s does ("Keuken #2").
func TestServiceCollisionTakesTheAlternativeName(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	responder(t, bus, 0)
	waitFor(t, "the service", func() bool { return fake.groupOf("AddService Keuken ") != "" })
	fake.emit(fake.groupOf("AddService Keuken "), avahiEntryGroup+".StateChanged", groupCollision, "")
	waitFor(t, "the renamed service", func() bool { return fake.groupOf("AddService Keuken #2 ") != "" })

	for _, e := range fake.published() {
		if strings.HasPrefix(e, "AddService Keuken _") {
			t.Fatalf("the colliding name is still published: %v", fake.published())
		}
	}
}

// The shared name is never renamed: on a collision it is withdrawn and claimed again later,
// and not while another speaker answers for it.
func TestSharedNameCollisionWithdrawsAndRetries(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	responder(t, bus, 0)
	waitFor(t, "the shared name", func() bool { return fake.groupOf("AddAddress") != "" })
	fake.mu.Lock()
	fake.holders["soundtap.local"] = "192.0.2.99" // another speaker holds it now
	fake.mu.Unlock()
	fake.emit(fake.groupOf("AddAddress"), avahiEntryGroup+".StateChanged", groupCollision, "")
	waitFor(t, "the claim withdrawn", func() bool { return fake.groupOf("AddAddress") == "" })
	time.Sleep(3 * testSharedRetry)

	if fake.groupOf("AddAddress") != "" {
		t.Fatal("claimed the shared name while another speaker answers for it")
	}

	for _, e := range fake.published() {
		if strings.Contains(e, "soundtap-2") || strings.Contains(e, "#2") {
			t.Fatalf("renamed: %v", fake.published())
		}
	}

	fake.mu.Lock()
	delete(fake.holders, "soundtap.local") // the holder went away
	fake.mu.Unlock()
	waitFor(t, "the shared name taken over", func() bool { return fake.groupOf("AddAddress") != "" })
}

// A shared name this speaker's own address answers for is still ours to claim.
func TestSharedNameOwnAnswerIsFree(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	fake.mu.Lock()
	fake.holders["soundtap.local"] = "192.0.2.10"
	fake.mu.Unlock()
	responder(t, bus, 0)
	waitFor(t, "the shared name", func() bool { return fake.groupOf("AddAddress") != "" })
}

// avahi-daemon restarting loses every group; the speaker publishes again on the new one.
func TestAvahiRestartRepublishes(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	responder(t, bus, 0)
	waitFor(t, "first publish", func() bool { return len(fake.published()) == 2 })

	_ = fake.conn.Close()
	again := newAvahi(t, bus)
	want := []string{"AddAddress soundtap.local 192.0.2.10 16", wantService}

	waitFor(t, "publish on the new avahi", func() bool { return slices.Equal(again.published(), want) })
}

// A host name change in avahi resets the records; they are published again once it runs.
func TestServerRegisteringRepublishes(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	responder(t, bus, 0)
	waitFor(t, "first publish", func() bool { return len(fake.published()) == 2 })
	fake.emit("/", avahiServer+".StateChanged", serverRegistering, "")
	waitFor(t, "groups withdrawn", func() bool { return len(fake.published()) == 0 })
	fake.emit("/", avahiServer+".StateChanged", serverRunning, "")
	waitFor(t, "published again", func() bool { return len(fake.published()) == 2 })
}

// The browser reports a resolved peer, its departure, and nothing for this speaker or IPv6.
func TestBrowseReportsPeers(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	fake.setService("Self."+serviceType, res("self.local", "192.0.2.10", "id=SERIAL0000000010A"))

	speaker, peers := responder(t, bus, 0)
	waitFor(t, "a browser", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()

		return len(fake.browsers) == 1
	})
	fake.mu.Lock()
	browserPath := fake.browsers[0]
	fake.mu.Unlock()
	fake.emit(browserPath, avahiBrowser+".ItemNew", int32(2), protoIPv4, "Self",
		serviceType, "local", uint32(0))
	fake.emit(browserPath, avahiBrowser+".ItemNew", int32(2), int32(1), "Six", serviceType, "local", uint32(0))
	fake.emit(browserPath, avahiBrowser+".ItemNew", int32(2), protoIPv4, "Ghost",
		serviceType, "local", uint32(0))
	waitFor(t, "the three items noted", func() bool {
		notes := strings.Join(speaker.Recent(), "\n")

		return strings.Contains(notes, "ignored: this speaker: Self") &&
			strings.Contains(notes, "skipped: not an IPv4 instance") &&
			strings.Contains(notes, "not resolved: Ghost")
	})

	select {
	case p := <-peers:
		t.Fatalf("reported %+v", p)
	default:
	}
}

// A peer that resolves to an on-link address is reported, and its removal is a departure.
func TestBrowseReportsAndDepartsAPeer(t *testing.T) {
	t.Parallel()

	peerIP := onLinkPeer(t)
	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	fake.setService("Woonkamer."+serviceType,
		res("soundtap-x.local", peerIP, "id=PEER", "name=Woonkamer", "model=ST20", "fw=0.2.0", "api=1"))

	_, peers := responder(t, bus, 0)
	waitFor(t, "a browser", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()

		return len(fake.browsers) == 1
	})
	fake.mu.Lock()
	browserPath := fake.browsers[0]
	fake.mu.Unlock()
	fake.emit(browserPath, avahiBrowser+".ItemNew", int32(2), protoIPv4, "Woonkamer",
		serviceType, "local", uint32(0))

	select {
	case p := <-peers:
		if p.ID != "PEER" || p.Name != "Woonkamer" || p.Model != "ST20" || p.Firmware != "0.2.0" ||
			p.IP.String() != peerIP || p.Departure {
			t.Fatalf("peer %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no peer reported")
	}

	fake.emit(browserPath, avahiBrowser+".ItemRemove", int32(2), protoIPv4, "Woonkamer",
		serviceType, "local", uint32(0))

	select {
	case p := <-peers:
		if !p.Departure || p.ID != "PEER" {
			t.Fatalf("departure %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no departure reported")
	}
}

// The browser is replaced after browseRefresh, so a peer's changed TXT (new firmware after
// a reboot without a goodbye) is resolved again instead of staying stale.
func TestBrowseRefreshesTheBrowser(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	responder(t, bus, 200*time.Millisecond)
	waitFor(t, "two browsers", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()

		return len(fake.browsers) >= 2
	})
}

func TestResolveService(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)
	fake := newAvahi(t, bus)
	fake.setService("iTunes_Ctrl_ABC._dacp._tcp",
		resolved{host: "phone.local", ip: res("", "192.0.2.7").ip, port: 50123, txt: nil})

	ctx := context.Background()

	got, err := ResolveService(ctx, bus.Addr, "iTunes_Ctrl_ABC", "_dacp._tcp")
	if err != nil || got != "192.0.2.7:50123" {
		t.Fatalf("ResolveService = %q, %v", got, err)
	}

	got, err = ResolveService(ctx, bus.Addr, "iTunes_Ctrl_NONE", "_dacp._tcp")
	if err == nil {
		t.Fatalf("an unknown sender resolved to %q", got)
	}
}

// onLinkPeer returns another address on one of this machine's IPv4 networks, or skips:
// the browser takes only on-link peers.
func onLinkPeer(t *testing.T) string {
	t.Helper()

	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.To4() == nil || ipNet.IP.IsLoopback() {
			continue
		}

		if ones, bits := ipNet.Mask.Size(); bits-ones < 2 {
			continue
		}

		ip := ipNet.IP.Mask(ipNet.Mask).To4()
		for range 2 {
			ip[3]++
			if !ip.Equal(ipNet.IP) && onLink(ip) {
				return ip.String()
			}
		}
	}

	t.Skip("this machine has no IPv4 network to put a peer on")

	return ""
}
