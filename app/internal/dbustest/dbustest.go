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

// The RequestName and ReleaseName reply codes of the D-Bus specification.
const (
	requestNamePrimaryOwner uint32 = 1
	requestNameExists       uint32 = 3
	requestNameAlreadyOwner uint32 = 4
	releaseNameReleased     uint32 = 1
	releaseNameNotOwner     uint32 = 3
)

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
	dir, err := os.MkdirTemp("", "dbus") //nolint:usetesting // t.TempDir makes the socket path too long
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "bus")

	listener, err := new(net.ListenConfig).Listen(context.Background(), "unix", sock)
	if err != nil {
		tb.Fatal(err)
	}

	bus := &Bus{
		Addr: "unix:path=" + sock,
		mu:   sync.Mutex{}, next: 0, peers: map[string]*peer{}, owners: map[string]string{},
	}

	var conns sync.WaitGroup

	tb.Cleanup(func() {
		_ = listener.Close()

		bus.mu.Lock()
		for _, p := range bus.peers {
			_ = p.conn.Close()
		}
		bus.mu.Unlock()
		conns.Wait()
	})
	safego.Go("dbustest.accept", func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			conns.Add(1)
			safego.Go("dbustest.serve", func() {
				defer conns.Done()

				bus.serve(conn)
			})
		}
	})

	return bus
}

// Connect opens a godbus connection to the bus, closed when the test ends.
func (b *Bus) Connect(tb testing.TB) *dbus.Conn {
	tb.Helper()

	conn, err := dbus.Connect(b.Addr)
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() { _ = conn.Close() })

	return conn
}

func (b *Bus) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	if !authenticate(conn, reader) {
		return
	}

	b.mu.Lock()
	b.next++
	from := &peer{name: ":1." + strconv.Itoa(b.next), conn: conn, wmu: sync.Mutex{}, rules: nil}
	b.peers[from.name] = from

	b.mu.Unlock()
	defer b.leave(from)

	for {
		msg, err := dbus.DecodeMessage(reader)
		if err != nil {
			return
		}

		msg.Headers[dbus.FieldSender] = dbus.MakeVariant(from.name)
		b.route(from, msg)
	}
}

// authenticate answers the SASL exchange as dbus-daemon does: EXTERNAL is accepted for
// anyone, Unix fd passing is declined.
func authenticate(conn net.Conn, reader *bufio.Reader) bool {
	nul, err := reader.ReadByte()
	if err != nil || nul != 0 {
		return false
	}

	for {
		line, err := reader.ReadString('\n')
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

		_, err = conn.Write([]byte(reply + "\r\n"))
		if err != nil {
			return false
		}
	}
}

// leave drops a peer's names, telling everyone as NameOwnerChanged.
func (b *Bus) leave(left *peer) {
	b.mu.Lock()
	delete(b.peers, left.name)

	var gone []string

	for name, owner := range b.owners {
		if owner == left.name {
			delete(b.owners, name)
			gone = append(gone, name)
		}
	}
	b.mu.Unlock()

	for _, name := range gone {
		b.ownerChanged(name, left.name, "")
	}

	b.ownerChanged(left.name, left.name, "")
}

func (b *Bus) route(from *peer, msg *dbus.Message) {
	dest, _ := msg.Headers[dbus.FieldDestination].Value().(string)
	switch {
	case msg.Type == dbus.TypeMethodCall && dest == busName:
		b.busCall(from, msg)
	case msg.Type == dbus.TypeSignal && dest == "":
		b.broadcast(msg)
	default:
		b.mu.Lock()

		target := b.peers[dest]
		if target == nil {
			target = b.peers[b.owners[dest]]
		}
		b.mu.Unlock()

		if target == nil {
			if msg.Type == dbus.TypeMethodCall && msg.Flags&dbus.FlagNoReplyExpected == 0 {
				replyError(from, msg, "org.freedesktop.DBus.Error.ServiceUnknown",
					"The name "+dest+" was not provided by any .service files")
			}

			return
		}

		target.send(msg)
	}
}

func (b *Bus) busCall(from *peer, msg *dbus.Message) {
	member, _ := msg.Headers[dbus.FieldMember].Value().(string)

	arg := ""
	if len(msg.Body) > 0 {
		arg, _ = msg.Body[0].(string)
	}

	handle, ok := map[string]func(*peer, *dbus.Message, string){
		"Hello":        b.hello,
		"AddMatch":     b.addMatch,
		"RemoveMatch":  b.removeMatch,
		"RequestName":  b.requestName,
		"ReleaseName":  b.releaseName,
		"GetNameOwner": b.replyNameOwner,
		"NameHasOwner": func(from *peer, call *dbus.Message, name string) { reply(from, call, b.resolve(name) != "") },
	}[member]
	if !ok {
		replyError(from, msg, "org.freedesktop.DBus.Error.UnknownMethod", "the test bus has no "+member)

		return
	}

	handle(from, msg, arg)
}

func (b *Bus) hello(from *peer, call *dbus.Message, _ string) {
	reply(from, call, from.name)
	b.ownerChanged(from.name, "", from.name)
}

func (b *Bus) addMatch(from *peer, call *dbus.Message, rule string) {
	b.mu.Lock()

	from.rules = append(from.rules, parseRule(rule))
	b.mu.Unlock()
	reply(from, call)
}

func (b *Bus) removeMatch(from *peer, call *dbus.Message, rule string) {
	want := fmt.Sprint(parseRule(rule))

	b.mu.Lock()
	for i, r := range from.rules {
		if fmt.Sprint(r) == want {
			from.rules = append(from.rules[:i], from.rules[i+1:]...)

			break
		}
	}
	b.mu.Unlock()
	reply(from, call)
}

func (b *Bus) requestName(from *peer, call *dbus.Message, name string) {
	b.mu.Lock()

	owner := b.owners[name]
	if owner == "" {
		b.owners[name] = from.name
	}
	b.mu.Unlock()

	switch owner {
	case "":
		reply(from, call, requestNamePrimaryOwner)
		b.ownerChanged(name, "", from.name)
		signal(from, "NameAcquired", name)
	case from.name:
		reply(from, call, requestNameAlreadyOwner)
	default:
		reply(from, call, requestNameExists)
	}
}

func (b *Bus) releaseName(from *peer, call *dbus.Message, name string) {
	b.mu.Lock()

	mine := b.owners[name] == from.name
	if mine {
		delete(b.owners, name)
	}
	b.mu.Unlock()

	if !mine {
		reply(from, call, releaseNameNotOwner)

		return
	}

	reply(from, call, releaseNameReleased)
	b.ownerChanged(name, from.name, "")
}

func (b *Bus) replyNameOwner(from *peer, call *dbus.Message, name string) {
	if owner := b.resolve(name); owner != "" {
		reply(from, call, owner)

		return
	}

	replyError(from, call, "org.freedesktop.DBus.Error.NameHasNoOwner", "no owner for "+name)
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

func reply(target *peer, call *dbus.Message, body ...any) {
	msg := &dbus.Message{Type: dbus.TypeMethodReply, Flags: 0, Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldReplySerial: dbus.MakeVariant(call.Serial()),
		dbus.FieldDestination: dbus.MakeVariant(target.name),
		dbus.FieldSender:      dbus.MakeVariant(busName),
	}, Body: body}
	if len(body) > 0 {
		msg.Headers[dbus.FieldSignature] = dbus.MakeVariant(dbus.SignatureOf(body...))
	}

	target.send(msg)
}

func replyError(target *peer, call *dbus.Message, name, text string) {
	target.send(&dbus.Message{Type: dbus.TypeError, Flags: 0, Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldReplySerial: dbus.MakeVariant(call.Serial()),
		dbus.FieldDestination: dbus.MakeVariant(target.name),
		dbus.FieldSender:      dbus.MakeVariant(busName),
		dbus.FieldErrorName:   dbus.MakeVariant(name),
		dbus.FieldSignature:   dbus.MakeVariant(dbus.SignatureOf(text)),
	}, Body: []any{text}})
}

// signal sends the bus's own signal member to one peer.
func signal(target *peer, member string, body ...any) {
	target.send(busSignal(member, target.name, body...))
}

func busSignal(member, dest string, body ...any) *dbus.Message {
	msg := &dbus.Message{Type: dbus.TypeSignal, Flags: 0, Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldPath:      dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/DBus")),
		dbus.FieldInterface: dbus.MakeVariant(busName),
		dbus.FieldMember:    dbus.MakeVariant(member),
		dbus.FieldSender:    dbus.MakeVariant(busName),
		dbus.FieldSignature: dbus.MakeVariant(dbus.SignatureOf(body...)),
	}, Body: body}
	if dest != "" {
		msg.Headers[dbus.FieldDestination] = dbus.MakeVariant(dest)
	}

	return msg
}

func (b *Bus) ownerChanged(name, old, owner string) {
	b.broadcast(busSignal("NameOwnerChanged", "", name, old, owner))
}

// broadcast sends a signal to every peer with a rule it matches.
func (b *Bus) broadcast(msg *dbus.Message) {
	b.mu.Lock()

	var targets []*peer

	for _, p := range b.peers {
		for _, r := range p.rules {
			if b.matchLocked(r, msg) {
				targets = append(targets, p)

				break
			}
		}
	}
	b.mu.Unlock()

	for _, p := range targets {
		p.send(msg)
	}
}

func (b *Bus) matchLocked(rule map[string]string, msg *dbus.Message) bool {
	for key, want := range rule {
		if !b.fieldMatchesLocked(key, want, msg) {
			return false
		}
	}

	return true
}

// fieldMatchesLocked checks one key='value' of a match rule against a signal.
func (b *Bus) fieldMatchesLocked(key, want string, msg *dbus.Message) bool {
	str := func(f dbus.HeaderField) string {
		v, _ := msg.Headers[f].Value().(string)

		return v
	}
	path, _ := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)

	switch key {
	case "type":
		return want == "signal" && msg.Type == dbus.TypeSignal
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
		if err != nil || i >= len(msg.Body) {
			return false
		}

		s, _ := msg.Body[i].(string)

		return s == want
	}

	return true // a key this bus does not know does not filter
}

// parseRule reads key='value' pairs; the rules godbus and soundtapd write carry no
// escaped quotes.
func parseRule(text string) map[string]string {
	rule := map[string]string{}

	for text != "" {
		key, rest, ok := strings.Cut(text, "='")
		if !ok {
			break
		}

		value, rest, _ := strings.Cut(rest, "'")
		rule[strings.TrimSpace(key)] = value
		text = strings.TrimPrefix(rest, ",")
	}

	return rule
}

func (p *peer) send(msg *dbus.Message) {
	p.wmu.Lock()
	defer p.wmu.Unlock()

	_ = msg.EncodeTo(p.conn, binary.LittleEndian)
}
