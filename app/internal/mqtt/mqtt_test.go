// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mqtt

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// The fake brokers below speak the wire format themselves, from the specification, so
// the tests check the bytes Paho sends rather than Paho against itself.

// Packet type codes (MQTT 3.1.1 §2.2.1), in the high nibble of the fixed header.
const (
	pktConnect    = 1
	pktConnack    = 2
	pktPublish    = 3
	pktSubscribe  = 8
	pktSuback     = 9
	pktPingreq    = 12
	pktPingresp   = 13
	pktDisconnect = 14
)

// readPacket reads one whole packet: its type, header flags and the remaining bytes.
func readPacket(br *bufio.Reader) (typ, flags byte, body []byte, err error) {
	first, err := br.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}

	var n, mult int

	for i := 0; ; i++ {
		b, err := br.ReadByte()
		if err != nil {
			return 0, 0, nil, err
		}

		if i == 4 {
			return 0, 0, nil, errors.New("malformed remaining length")
		}

		n += int(b&0x7f) << mult
		if b&0x80 == 0 {
			break
		}

		mult += 7
	}

	body = make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		return 0, 0, nil, err
	}

	return first >> 4, first & 0x0f, body, nil
}

// appendRemainingLength encodes n as an MQTT variable-length integer.
func appendRemainingLength(b []byte, n int) []byte {
	for {
		digit := byte(n % 128)

		n /= 128
		if n > 0 {
			digit |= 0x80
		}

		b = append(b, digit)
		if n == 0 {
			return b
		}
	}
}

// appendString appends a length-prefixed UTF-8 string (MQTT §1.5.3).
func appendString(b []byte, s string) []byte {
	b = append(b, byte(len(s)>>8), byte(len(s)))

	return append(b, s...)
}

// takeString reads a length-prefixed string off the front of b.
func takeString(b []byte) (s string, rest []byte, ok bool) {
	if len(b) < 2 {
		return "", nil, false
	}

	n := int(b[0])<<8 | int(b[1])
	if len(b) < 2+n {
		return "", nil, false
	}

	return string(b[2 : 2+n]), b[2+n:], true
}

// parsePublish reads a QoS 0 PUBLISH body.
func parsePublish(flags byte, body []byte) (m Message, ok bool) {
	if (flags>>1)&0x03 != 0 {
		return Message{}, false
	}

	topic, rest, ok := takeString(body)
	if !ok {
		return Message{}, false
	}

	return Message{Topic: topic, Payload: rest, Retain: flags&0x01 != 0}, true
}

// parseConnect pulls the fields the test cares about out of a CONNECT body.
func parseConnect(t *testing.T, body []byte) (clientID, willTopic, username, password string, willRetain bool) {
	t.Helper()

	proto, rest, ok := takeString(body)
	if !ok || proto != "MQTT" {
		t.Fatalf("CONNECT protocol name = %q ok=%v", proto, ok)
	}

	if len(rest) < 4 {
		t.Fatal("CONNECT variable header too short")
	}

	flags := rest[1]
	rest = rest[4:] // skip level + flags + 2-byte keepalive

	clientID, rest, _ = takeString(rest)
	if flags&0x04 != 0 { // will flag
		willTopic, rest, _ = takeString(rest)
		_, rest, _ = takeString(rest) // will payload
		willRetain = flags&0x20 != 0
	}

	if flags&0x80 != 0 { // username
		username, rest, _ = takeString(rest)
	}

	if flags&0x40 != 0 { // password
		password, _, _ = takeString(rest)
	}

	return clientID, willTopic, username, password, willRetain
}

// TestConnectPublishSubscribe drives the client against a fake broker built on the
// same codec: it verifies the CONNECT payload, that Subscribe/Publish are framed as
// the right packet types, and that an inbound PUBLISH reaches the handler.
func TestConnectPublishSubscribe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	type pkt struct {
		typ, flags byte
		body       []byte
	}

	connectCh := make(chan pkt, 1)
	clientPkts := make(chan pkt, 4)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		br := bufio.NewReader(conn)

		typ, flags, body, err := readPacket(br)
		if err != nil {
			return
		}

		connectCh <- pkt{typ, flags, body}

		// CONNACK: fixed header + [session present, return code].
		if _, err := conn.Write([]byte{pktConnack << 4, 2, 0, 0}); err != nil {
			return
		}

		// Push a live PUBLISH down to the client, then a retained one (RETAIN flag set),
		// as a broker replays a stored message on subscribe.
		for _, p := range []struct {
			flags          byte
			topic, payload string
		}{{0, "soundtap/x/cmd", "hello"}, {0x01, "soundtap/x/old", "stored"}} {
			var vh []byte

			vh = appendString(vh, p.topic)
			vh = append(vh, []byte(p.payload)...)
			frame := append([]byte{pktPublish<<4 | p.flags}, appendRemainingLength(nil, len(vh))...)

			frame = append(frame, vh...)
			if _, err := conn.Write(frame); err != nil {
				return
			}
		}

		// Read the SUBSCRIBE and PUBLISH the client sends back.
		for range 2 {
			typ, flags, body, err := readPacket(br)
			if err != nil {
				return
			}

			if typ == pktSubscribe {
				writeSuback(conn, body, 0x00)
			}

			clientPkts <- pkt{typ, flags, body}
		}
	}()

	msgs := make(chan Message, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := Connect(ctx, Options{
		Addr:     ln.Addr().String(),
		ClientID: "cid",
		Username: "user",
		Password: "pass",
		Will:     &Will{Topic: "soundtap/x/availability", Payload: []byte("offline"), Retain: true},
		Handler:  func(_ *Client, m Message) { msgs <- m },
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Disconnect()

	select {
	case cp := <-connectCh:
		if cp.typ != pktConnect {
			t.Fatalf("first packet type = %d, want CONNECT", cp.typ)
		}

		id, willTopic, user, pass, retain := parseConnect(t, cp.body)
		if id != "cid" || user != "user" || pass != "pass" {
			t.Fatalf("CONNECT creds: id=%q user=%q pass=%q", id, user, pass)
		}

		if willTopic != "soundtap/x/availability" || !retain {
			t.Fatalf("CONNECT will: topic=%q retain=%v", willTopic, retain)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for CONNECT")
	}

	// Inbound PUBLISHes reach the handler with their RETAIN flag.
	for _, want := range []Message{
		{Topic: "soundtap/x/cmd", Payload: []byte("hello")},
		{Topic: "soundtap/x/old", Payload: []byte("stored"), Retain: true},
	} {
		select {
		case m := <-msgs:
			if m.Topic != want.Topic || !bytes.Equal(m.Payload, want.Payload) || m.Retain != want.Retain {
				t.Fatalf("handler got topic=%q payload=%q retain=%v, want %q %q %v",
					m.Topic, m.Payload, m.Retain, want.Topic, want.Payload, want.Retain)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for inbound publish")
		}
	}

	if err := c.Subscribe("soundtap/x/#"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := c.Publish("soundtap/x/state", []byte("v"), true); err != nil {
		t.Fatalf("publish: %v", err)
	}

	got := map[byte]pkt{}

	for range 2 {
		select {
		case p := <-clientPkts:
			got[p.typ] = p
		case <-ctx.Done():
			t.Fatal("timed out waiting for client packets")
		}
	}

	if _, ok := got[pktSubscribe]; !ok {
		t.Fatal("broker never received a SUBSCRIBE")
	}

	pub, ok := got[pktPublish]
	if !ok {
		t.Fatal("broker never received a PUBLISH")
	}

	if pub.flags&0x01 == 0 {
		t.Fatal("PUBLISH retain flag not set")
	}

	m, ok := parsePublish(pub.flags, pub.body)
	if !ok || m.Topic != "soundtap/x/state" || string(m.Payload) != "v" || !m.Retain {
		t.Fatalf("PUBLISH parsed as topic=%q payload=%q retain=%v ok=%v", m.Topic, m.Payload, m.Retain, ok)
	}
}

// writeSuback answers a SUBSCRIBE body with one return code per request.
func writeSuback(conn net.Conn, subscribe []byte, code byte) {
	_, _ = conn.Write([]byte{pktSuback << 4, 3, subscribe[0], subscribe[1], code})
}

// fakeBroker accepts one client, answers CONNECT unless silent, and hands each later
// packet to onPacket.
func fakeBroker(t *testing.T, silent bool, onPacket func(net.Conn, byte, []byte)) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		br := bufio.NewReader(conn)
		if _, _, _, err := readPacket(br); err != nil {
			return
		}

		if silent {
			_, _ = io.Copy(io.Discard, br)

			return
		}

		_, _ = conn.Write([]byte{pktConnack << 4, 2, 0, 0})

		for {
			typ, _, body, err := readPacket(br)
			if err != nil {
				return
			}

			onPacket(conn, typ, body)
		}
	}()

	return ln.Addr().String()
}

// A subscription the broker refuses (an ACL) is an error, not a healthy connection.
func TestSubscribeReportsADeniedSubscription(t *testing.T) {
	addr := fakeBroker(t, false, func(conn net.Conn, typ byte, body []byte) {
		if typ == pktSubscribe {
			writeSuback(conn, body, 0x80)
		}
	})

	c, err := Connect(context.Background(), Options{Addr: addr, ClientID: "cid"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect()

	if err := c.Subscribe("soundtap/x/#"); err == nil {
		t.Fatal("a refused subscription returned nil")
	}
}

// Cancelling ctx ends a handshake the broker never answers, without waiting out DialTimeout.
func TestConnectHonoursCancelDuringHandshake(t *testing.T) {
	addr := fakeBroker(t, true, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, err := Connect(ctx, Options{Addr: addr, ClientID: "cid", DialTimeout: 10 * time.Second})
		done <- err
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Connect = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Connect still blocked after cancellation")
	}
}

// A broker that goes away closes Done with the cause, which is what makes the bridge
// connect again: Paho's own reconnect is off.
func TestLostConnectionClosesDone(t *testing.T) {
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}

		br := bufio.NewReader(conn)
		if _, _, _, err := readPacket(br); err != nil {
			return
		}

		_, _ = conn.Write([]byte{pktConnack << 4, 2, 0, 0})

		time.Sleep(50 * time.Millisecond)

		_ = conn.Close()
	}()

	c, err := Connect(context.Background(), Options{Addr: ln.Addr().String(), ClientID: "cid"})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-c.Done():
		if c.Err() == nil {
			t.Fatal("a lost connection reported no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Done still open after the broker closed the connection")
	}

	if err := c.Publish("soundtap/x/state", []byte("v"), false); err == nil {
		t.Fatal("Publish on a lost connection returned nil")
	}
}

// Disconnect sends DISCONNECT, so the broker discards the Will, and closes Done cleanly.
func TestDisconnectSendsDisconnect(t *testing.T) {
	got := make(chan byte, 4)
	addr := fakeBroker(t, false, func(_ net.Conn, typ byte, _ []byte) { got <- typ })

	c, err := Connect(context.Background(), Options{
		Addr: addr, ClientID: "cid",
		Will: &Will{Topic: "soundtap/x/availability", Payload: []byte("offline"), Retain: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	c.Disconnect()

	select {
	case typ := <-got:
		if typ != pktDisconnect {
			t.Fatalf("broker got packet type %d, want DISCONNECT", typ)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("broker never got a DISCONNECT")
	}

	select {
	case <-c.Done():
		if c.Err() != nil {
			t.Fatalf("a clean Disconnect reported %v", c.Err())
		}
	default:
		t.Fatal("Done open after Disconnect")
	}

	c.Disconnect() // safe to repeat
}

// MQTT 3.1.1 forbids a password without a user name (§3.1.2.9): none is sent.
func TestPasswordOnlyWithAUsername(t *testing.T) {
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	connect := make(chan []byte, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		_, _, body, err := readPacket(bufio.NewReader(conn))
		if err != nil {
			return
		}

		connect <- body

		_, _ = conn.Write([]byte{pktConnack << 4, 2, 0, 0})

		time.Sleep(time.Second)
	}()

	c, err := Connect(context.Background(), Options{Addr: ln.Addr().String(), ClientID: "cid", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect()

	body := <-connect
	// Protocol name "MQTT", level 4 (3.1.1), then the connect flags.
	if proto, rest, ok := takeString(body); !ok || proto != "MQTT" || len(rest) < 2 || rest[0] != 4 {
		t.Fatalf("CONNECT protocol %q, rest %v", proto, rest)
	}

	if flags := body[7]; flags&0xc0 != 0 || flags&0x02 == 0 {
		t.Fatalf("CONNECT flags %#x: want a clean session, no user name, no password", flags)
	}
}
