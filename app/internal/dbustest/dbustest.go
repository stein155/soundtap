// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package dbustest is a message bus for tests: enough of dbus-daemon for godbus
// connections to authenticate, say Hello, own names, add match rules, call each other and
// receive signals. A test puts the service soundtapd talks to (a fake BlueZ, a fake avahi)
// on it as a godbus connection of its own, so both sides speak real D-Bus messages.
//
// It routes; it does not police. Every connection may call every other, and a signal goes
// to every connection with a match rule it fits.
package dbustest

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/safego"
)

const busName = "org.freedesktop.DBus"

// Bus is a running test bus. Its zero value is not usable; use New.
type Bus struct {
	// Addr is the bus's D-Bus address, for godbus's Connect.
	Addr string

	mu     sync.Mutex
	next   int
	peers  map[string]*peer  // by unique name
	owners map[string]string // well-known name -> unique name
}

type peer struct {
	name  string
	conn  net.Conn
	wmu   sync.Mutex
	rules []map[string]string
}

// New starts a bus that stops when the test ends.
func New(tb testing.TB) *Bus {
	tb.Helper()
	// A unix socket path must stay short: t.TempDir under a long test name exceeds it.
	dir, err := os.MkdirTemp("", "dbus")
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "bus")

	ln, err := new(net.ListenConfig).Listen(context.Background(), "unix", sock)
	if err != nil {
		tb.Fatal(err)
	}

	b := &Bus{Addr: "unix:path=" + sock, peers: map[string]*peer{}, owners: map[string]string{}}

	var conns sync.WaitGroup

	tb.Cleanup(func() {
		_ = ln.Close()

		b.mu.Lock()
		for _, p := range b.peers {
			_ = p.conn.Close()
		}
		b.mu.Unlock()
		conns.Wait()
	})
	safego.Go("dbustest.accept", func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			conns.Add(1)
			safego.Go("dbustest.serve", func() {
				defer conns.Done()

				b.serve(c)
			})
		}
	})

	return b
}

// Connect opens a godbus connection to the bus, closed when the test ends.
func (b *Bus) Connect(tb testing.TB) *dbus.Conn {
	tb.Helper()

	c, err := dbus.Connect(b.Addr)
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() { _ = c.Close() })

	return c
}

func (b *Bus) serve(c net.Conn) {
	defer func() { _ = c.Close() }()

	r := bufio.NewReader(c)
	if !authenticate(c, r) {
		return
	}

	b.mu.Lock()
	b.next++
	p := &peer{name: ":1." + strconv.Itoa(b.next), conn: c}
	b.peers[p.name] = p

	b.mu.Unlock()
	defer b.leave(p)

	for {
		m, err := dbus.DecodeMessage(r)
		if err != nil {
			return
		}

		m.Headers[dbus.FieldSender] = dbus.MakeVariant(p.name)
		b.route(p, m)
	}
}

// authenticate answers the SASL exchange as dbus-daemon does: EXTERNAL is accepted for
// anyone, Unix fd passing is declined.
func authenticate(c net.Conn, r *bufio.Reader) bool {
	if b, err := r.ReadByte(); err != nil || b != 0 {
		return false
	}

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return false
		}

		var reply string

		switch cmd := strings.TrimSpace(line); {
		case cmd == "BEGIN":
			return true
		case strings.HasPrefix(cmd, "AUTH EXTERNAL"):
			reply = "OK 0123456789abcdef0123456789abcdef"
		case strings.HasPrefix(cmd, "AUTH"):
			reply = "REJECTED EXTERNAL"
		default: // NEGOTIATE_UNIX_FD and anything else
			reply = "ERROR"
		}

		if _, err := c.Write([]byte(reply + "\r\n")); err != nil {
			return false
		}
	}
}

// leave drops a peer's names, telling everyone as NameOwnerChanged.
func (b *Bus) leave(p *peer) {
	b.mu.Lock()
	delete(b.peers, p.name)

	var gone []string

	for name, owner := range b.owners {
		if owner == p.name {
			delete(b.owners, name)
			gone = append(gone, name)
		}
	}
	b.mu.Unlock()

	for _, name := range gone {
		b.ownerChanged(name, p.name, "")
	}

	b.ownerChanged(p.name, p.name, "")
}

func (b *Bus) route(from *peer, m *dbus.Message) {
	dest, _ := m.Headers[dbus.FieldDestination].Value().(string)
	switch {
	case m.Type == dbus.TypeMethodCall && dest == busName:
		b.busCall(from, m)
	case m.Type == dbus.TypeSignal && dest == "":
		b.broadcast(m)
	default:
		b.mu.Lock()

		to := b.peers[dest]
		if to == nil {
			to = b.peers[b.owners[dest]]
		}
		b.mu.Unlock()

		if to == nil {
			if m.Type == dbus.TypeMethodCall && m.Flags&dbus.FlagNoReplyExpected == 0 {
				b.replyError(from, m, "org.freedesktop.DBus.Error.ServiceUnknown", "The name "+dest+" was not provided by any .service files")
			}

			return
		}

		to.send(m)
	}
}

func (b *Bus) busCall(from *peer, m *dbus.Message) {
	member, _ := m.Headers[dbus.FieldMember].Value().(string)

	arg := ""
	if len(m.Body) > 0 {
		arg, _ = m.Body[0].(string)
	}

	handle, ok := map[string]func(*peer, *dbus.Message, string){
		"Hello":        b.hello,
		"AddMatch":     b.addMatch,
		"RemoveMatch":  b.removeMatch,
		"RequestName":  b.requestName,
		"ReleaseName":  b.releaseName,
		"GetNameOwner": b.getNameOwner,
		"NameHasOwner": func(from *peer, m *dbus.Message, name string) { b.reply(from, m, b.resolve(name) != "") },
	}[member]
	if !ok {
		b.replyError(from, m, "org.freedesktop.DBus.Error.UnknownMethod", "the test bus has no "+member)

		return
	}

	handle(from, m, arg)
}

func (b *Bus) hello(from *peer, m *dbus.Message, _ string) {
	b.reply(from, m, from.name)
	b.ownerChanged(from.name, "", from.name)
}

func (b *Bus) addMatch(from *peer, m *dbus.Message, rule string) {
	b.mu.Lock()

	from.rules = append(from.rules, parseRule(rule))
	b.mu.Unlock()
	b.reply(from, m)
}

func (b *Bus) removeMatch(from *peer, m *dbus.Message, rule string) {
	want := fmt.Sprint(parseRule(rule))

	b.mu.Lock()
	for i, r := range from.rules {
		if fmt.Sprint(r) == want {
			from.rules = append(from.rules[:i], from.rules[i+1:]...)

			break
		}
	}
	b.mu.Unlock()
	b.reply(from, m)
}

func (b *Bus) requestName(from *peer, m *dbus.Message, name string) {
	b.mu.Lock()

	owner := b.owners[name]
	if owner == "" {
		b.owners[name] = from.name
	}
	b.mu.Unlock()

	switch owner {
	case "":
		b.reply(from, m, uint32(1)) // primary owner
		b.ownerChanged(name, "", from.name)
		b.signal(from, "NameAcquired", name)
	case from.name:
		b.reply(from, m, uint32(4)) // already the owner
	default:
		b.reply(from, m, uint32(3)) // exists
	}
}

func (b *Bus) releaseName(from *peer, m *dbus.Message, name string) {
	b.mu.Lock()

	mine := b.owners[name] == from.name
	if mine {
		delete(b.owners, name)
	}
	b.mu.Unlock()

	if !mine {
		b.reply(from, m, uint32(3)) // not owner

		return
	}

	b.reply(from, m, uint32(1))
	b.ownerChanged(name, from.name, "")
}

func (b *Bus) getNameOwner(from *peer, m *dbus.Message, name string) {
	if owner := b.resolve(name); owner != "" {
		b.reply(from, m, owner)

		return
	}

	b.replyError(from, m, "org.freedesktop.DBus.Error.NameHasNoOwner", "no owner for "+name)
}

// resolve returns the unique name behind name, a unique or well-known one.
func (b *Bus) resolve(name string) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.peers[name] != nil {
		return name
	}

	return b.owners[name]
}

func (b *Bus) reply(to *peer, call *dbus.Message, body ...any) {
	m := &dbus.Message{Type: dbus.TypeMethodReply, Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldReplySerial: dbus.MakeVariant(call.Serial()),
		dbus.FieldDestination: dbus.MakeVariant(to.name),
		dbus.FieldSender:      dbus.MakeVariant(busName),
	}, Body: body}
	if len(body) > 0 {
		m.Headers[dbus.FieldSignature] = dbus.MakeVariant(dbus.SignatureOf(body...))
	}

	to.send(m)
}

func (b *Bus) replyError(to *peer, call *dbus.Message, name, text string) {
	to.send(&dbus.Message{Type: dbus.TypeError, Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldReplySerial: dbus.MakeVariant(call.Serial()),
		dbus.FieldDestination: dbus.MakeVariant(to.name),
		dbus.FieldSender:      dbus.MakeVariant(busName),
		dbus.FieldErrorName:   dbus.MakeVariant(name),
		dbus.FieldSignature:   dbus.MakeVariant(dbus.SignatureOf(text)),
	}, Body: []any{text}})
}

// signal sends the bus's own signal member to one peer.
func (b *Bus) signal(to *peer, member string, body ...any) {
	to.send(busSignal(member, to.name, body...))
}

func busSignal(member, dest string, body ...any) *dbus.Message {
	m := &dbus.Message{Type: dbus.TypeSignal, Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldPath:      dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/DBus")),
		dbus.FieldInterface: dbus.MakeVariant(busName),
		dbus.FieldMember:    dbus.MakeVariant(member),
		dbus.FieldSender:    dbus.MakeVariant(busName),
		dbus.FieldSignature: dbus.MakeVariant(dbus.SignatureOf(body...)),
	}, Body: body}
	if dest != "" {
		m.Headers[dbus.FieldDestination] = dbus.MakeVariant(dest)
	}

	return m
}

func (b *Bus) ownerChanged(name, old, owner string) {
	b.broadcast(busSignal("NameOwnerChanged", "", name, old, owner))
}

// broadcast sends a signal to every peer with a rule it matches.
func (b *Bus) broadcast(m *dbus.Message) {
	b.mu.Lock()

	var to []*peer

	for _, p := range b.peers {
		for _, r := range p.rules {
			if b.matchLocked(r, m) {
				to = append(to, p)

				break
			}
		}
	}
	b.mu.Unlock()

	for _, p := range to {
		p.send(m)
	}
}

func (b *Bus) matchLocked(rule map[string]string, m *dbus.Message) bool {
	for key, want := range rule {
		if !b.fieldMatchesLocked(key, want, m) {
			return false
		}
	}

	return true
}

// fieldMatchesLocked checks one key='value' of a match rule against a signal.
func (b *Bus) fieldMatchesLocked(key, want string, m *dbus.Message) bool {
	str := func(f dbus.HeaderField) string {
		v, _ := m.Headers[f].Value().(string)

		return v
	}
	path, _ := m.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)

	switch key {
	case "type":
		return want == "signal" && m.Type == dbus.TypeSignal
	case "sender":
		sender := str(dbus.FieldSender)

		return want == sender || b.owners[want] == sender
	case "interface":
		return str(dbus.FieldInterface) == want
	case "member":
		return str(dbus.FieldMember) == want
	case "path":
		return string(path) == want
	case "path_namespace":
		return string(path) == want || strings.HasPrefix(string(path), strings.TrimSuffix(want, "/")+"/")
	}

	if n, ok := strings.CutPrefix(key, "arg"); ok {
		i, err := strconv.Atoi(n)
		if err != nil || i >= len(m.Body) {
			return false
		}

		s, _ := m.Body[i].(string)

		return s == want
	}

	return true // a key this bus does not know does not filter
}

// parseRule reads key='value' pairs; the rules godbus and soundtapd write carry no
// escaped quotes.
func parseRule(s string) map[string]string {
	rule := map[string]string{}

	for s != "" {
		key, rest, ok := strings.Cut(s, "='")
		if !ok {
			break
		}

		value, rest, _ := strings.Cut(rest, "'")
		rule[strings.TrimSpace(key)] = value
		s = strings.TrimPrefix(rest, ",")
	}

	return rule
}

func (p *peer) send(m *dbus.Message) {
	p.wmu.Lock()
	defer p.wmu.Unlock()

	_ = m.EncodeTo(p.conn, binary.LittleEndian)
}
