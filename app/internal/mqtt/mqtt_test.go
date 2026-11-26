// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mqtt_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/mqtt"
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

// errMalformedLength is a remaining length longer than the four bytes MQTT allows.
var errMalformedLength = errors.New("malformed remaining length")

// packet is one whole packet: its type, header flags and the remaining bytes.
type packet struct {
	typ, flags byte
	body       []byte
}

// readPacket reads one whole packet.
func readPacket(reader *bufio.Reader) (packet, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return packet{}, fmt.Errorf("read fixed header: %w", err)
	}

	var length, shift int

	for index := 0; ; index++ {
		digit, readErr := reader.ReadByte()
		if readErr != nil {
			return packet{}, fmt.Errorf("read remaining length: %w", readErr)
		}

		if index == 4 {
			return packet{}, errMalformedLength
		}

		length += int(digit&0x7f) << shift
		if digit&0x80 == 0 {
			break
		}

		shift += 7
	}

	body := make([]byte, length)

	_, err = io.ReadFull(reader, body)
	if err != nil {
		return packet{}, fmt.Errorf("read packet body: %w", err)
	}

	return packet{typ: first >> 4, flags: first & 0x0f, body: body}, nil
}

// appendRemainingLength encodes length as an MQTT variable-length integer.
func appendRemainingLength(buf []byte, length int) []byte {
	for {
		digit := byte(length % 128)

		length /= 128
		if length > 0 {
			digit |= 0x80
		}

		buf = append(buf, digit)
		if length == 0 {
			return buf
		}
	}
}

// appendString appends a length-prefixed UTF-8 string (MQTT §1.5.3).
func appendString(b []byte, s string) []byte {
	b = append(b, byte(len(s)>>8), byte(len(s)))

	return append(b, s...)
}

// takeString reads a length-prefixed string off the front of b: the string, the rest and
// whether buf held one.
func takeString(buf []byte) (string, []byte, bool) {
	if len(buf) < 2 {
		return "", nil, false
	}

	n := int(buf[0])<<8 | int(buf[1])
	if len(buf) < 2+n {
		return "", nil, false
	}

	return string(buf[2 : 2+n]), buf[2+n:], true
}

// parsePublish reads a QoS 0 PUBLISH body.
func parsePublish(flags byte, body []byte) (mqtt.Message, bool) {
	if (flags>>1)&0x03 != 0 {
		return mqtt.Message{Topic: "", Payload: nil, Retain: false}, false
	}

	topic, rest, ok := takeString(body)
	if !ok {
		return mqtt.Message{Topic: "", Payload: nil, Retain: false}, false
	}

	return mqtt.Message{Topic: topic, Payload: rest, Retain: flags&0x01 != 0}, true
}

// connectFields are the fields of a CONNECT the tests care about.
type connectFields struct {
	clientID, willTopic, username, password string
	willRetain                              bool
}

// parseConnect pulls the fields the test cares about out of a CONNECT body.
func parseConnect(t *testing.T, body []byte) connectFields {
	t.Helper()

	proto, rest, ok := takeString(body)
	if !ok || proto != "MQTT" {
		t.Fatalf("CONNECT protocol name = %q ok=%v", proto, ok)
	}

	if len(rest) < 4 {
		t.Fatal("CONNECT variable header too short")
	}

	var fields connectFields

	flags := rest[1]
	rest = rest[4:] // skip level + flags + 2-byte keepalive

	fields.clientID, rest, _ = takeString(rest)
	if flags&0x04 != 0 { // will flag
		fields.willTopic, rest, _ = takeString(rest)
		_, rest, _ = takeString(rest) // will payload
		fields.willRetain = flags&0x20 != 0
	}

	if flags&0x80 != 0 { // username
		fields.username, rest, _ = takeString(rest)
	}

	if flags&0x40 != 0 { // password
		fields.password, _, _ = takeString(rest)
	}

	return fields
}

// listen opens a loopback listener the test closes.
func listen(t *testing.T) net.Listener {
	t.Helper()

	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	return listener
}

// options are the Options with only addr and the client id set.
func options(addr string) mqtt.Options {
	return mqtt.Options{
		Addr: addr, ClientID: "cid", Username: "", Password: "", KeepAlive: 0, TLS: false,
		TLSConfig: nil, DialTimeout: 0, Will: nil, Handler: nil,
	}
}

// availabilityWill is the retained Will the bridge sets.
func availabilityWill() *mqtt.Will {
	return &mqtt.Will{Topic: "soundtap/x/availability", Payload: []byte("offline"), Retain: true}
}

// serveConnectPublishSubscribe is the broker of TestConnectPublishSubscribe: it hands on the
// CONNECT, pushes a live and a retained PUBLISH, then hands on the client's SUBSCRIBE and
// PUBLISH, answering the SUBSCRIBE.
func serveConnectPublishSubscribe(listener net.Listener, connectCh, clientPkts chan<- packet) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	connect, err := readPacket(reader)
	if err != nil {
		return
	}

	connectCh <- connect

	// CONNACK: fixed header + [session present, return code].
	_, err = conn.Write([]byte{pktConnack << 4, 2, 0, 0})
	if err != nil {
		return
	}

	// Push a live PUBLISH down to the client, then a retained one (RETAIN flag set),
	// as a broker replays a stored message on subscribe.
	for _, publish := range []struct {
		flags          byte
		topic, payload string
	}{{0, "soundtap/x/cmd", "hello"}, {0x01, "soundtap/x/old", "stored"}} {
		var header []byte

		header = appendString(header, publish.topic)
		header = append(header, []byte(publish.payload)...)
		frame := append([]byte{pktPublish<<4 | publish.flags}, appendRemainingLength(nil, len(header))...)

		frame = append(frame, header...)

		_, err = conn.Write(frame)
		if err != nil {
			return
		}
	}

	// Read the SUBSCRIBE and PUBLISH the client sends back.
	for range 2 {
		pkt, err := readPacket(reader)
		if err != nil {
			return
		}

		if pkt.typ == pktSubscribe {
			writeSuback(conn, pkt.body, 0x00)
		}

		clientPkts <- pkt
	}
}

// checkConnect checks the CONNECT of TestConnectPublishSubscribe.
func checkConnect(ctx context.Context, t *testing.T, connectCh <-chan packet) {
	t.Helper()

	select {
	case connect := <-connectCh:
		if connect.typ != pktConnect {
			t.Fatalf("first packet type = %d, want CONNECT", connect.typ)
		}

		fields := parseConnect(t, connect.body)
		if fields.clientID != "cid" || fields.username != "user" || fields.password != "pass" {
			t.Fatalf("CONNECT creds: id=%q user=%q pass=%q", fields.clientID, fields.username, fields.password)
		}

		if fields.willTopic != "soundtap/x/availability" || !fields.willRetain {
			t.Fatalf("CONNECT will: topic=%q retain=%v", fields.willTopic, fields.willRetain)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for CONNECT")
	}
}

// checkInbound checks that the inbound PUBLISHes reach the handler with their RETAIN flag.
func checkInbound(ctx context.Context, t *testing.T, msgs <-chan mqtt.Message) {
	t.Helper()

	for _, want := range []mqtt.Message{
		{Topic: "soundtap/x/cmd", Payload: []byte("hello"), Retain: false},
		{Topic: "soundtap/x/old", Payload: []byte("stored"), Retain: true},
	} {
		select {
		case msg := <-msgs:
			if msg.Topic != want.Topic || !bytes.Equal(msg.Payload, want.Payload) || msg.Retain != want.Retain {
				t.Fatalf("handler got topic=%q payload=%q retain=%v, want %q %q %v",
					msg.Topic, msg.Payload, msg.Retain, want.Topic, want.Payload, want.Retain)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for inbound publish")
		}
	}
}

// checkClientPackets checks that the broker got the SUBSCRIBE and the retained PUBLISH.
func checkClientPackets(ctx context.Context, t *testing.T, clientPkts <-chan packet) {
	t.Helper()

	got := map[byte]packet{}

	for range 2 {
		select {
		case pkt := <-clientPkts:
			got[pkt.typ] = pkt
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

	msg, ok := parsePublish(pub.flags, pub.body)
	if !ok || msg.Topic != "soundtap/x/state" || string(msg.Payload) != "v" || !msg.Retain {
		t.Fatalf("PUBLISH parsed as topic=%q payload=%q retain=%v ok=%v", msg.Topic, msg.Payload, msg.Retain, ok)
	}
}

// TestConnectPublishSubscribe drives the client against a fake broker built on the
// same codec: it verifies the CONNECT payload, that Subscribe/Publish are framed as
// the right packet types, and that an inbound PUBLISH reaches the handler.
func TestConnectPublishSubscribe(t *testing.T) {
	t.Parallel()

	listener := listen(t)
	connectCh := make(chan packet, 1)
	clientPkts := make(chan packet, 4)

	go serveConnectPublishSubscribe(listener, connectCh, clientPkts)

	msgs := make(chan mqtt.Message, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := options(listener.Addr().String())
	opts.Username, opts.Password = "user", "pass"
	opts.Will = availabilityWill()
	opts.Handler = func(_ *mqtt.Client, m mqtt.Message) { msgs <- m }

	client, err := mqtt.Connect(ctx, opts)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	checkConnect(ctx, t, connectCh)
	checkInbound(ctx, t, msgs)

	err = client.Subscribe("soundtap/x/#")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	err = client.Publish("soundtap/x/state", []byte("v"), true)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	checkClientPackets(ctx, t, clientPkts)
}

// writeSuback answers a SUBSCRIBE body with one return code per request.
func writeSuback(conn net.Conn, subscribe []byte, code byte) {
	_, _ = conn.Write([]byte{pktSuback << 4, 3, subscribe[0], subscribe[1], code})
}

// acceptConnect accepts one client and reads its CONNECT; ok is false when either failed.
func acceptConnect(listener net.Listener) (net.Conn, *bufio.Reader, bool) {
	conn, err := listener.Accept()
	if err != nil {
		return nil, nil, false
	}

	reader := bufio.NewReader(conn)

	_, err = readPacket(reader)
	if err != nil {
		_ = conn.Close()

		return nil, nil, false
	}

	return conn, reader, true
}

// fakeBroker accepts one client, answers CONNECT, and hands each later packet to onPacket.
func fakeBroker(t *testing.T, onPacket func(net.Conn, byte, []byte)) string {
	t.Helper()

	listener := listen(t)

	go func() {
		conn, reader, ok := acceptConnect(listener)
		if !ok {
			return
		}
		defer conn.Close()

		_, _ = conn.Write([]byte{pktConnack << 4, 2, 0, 0})

		for {
			pkt, err := readPacket(reader)
			if err != nil {
				return
			}

			onPacket(conn, pkt.typ, pkt.body)
		}
	}()

	return listener.Addr().String()
}

// silentBroker accepts one client and never answers its CONNECT.
func silentBroker(t *testing.T) string {
	t.Helper()

	listener := listen(t)

	go func() {
		conn, reader, ok := acceptConnect(listener)
		if !ok {
			return
		}
		defer conn.Close()

		_, _ = io.Copy(io.Discard, reader)
	}()

	return listener.Addr().String()
}

// A subscription the broker refuses (an ACL) is an error, not a healthy connection.
func TestSubscribeReportsADeniedSubscription(t *testing.T) {
	t.Parallel()

	addr := fakeBroker(t, func(conn net.Conn, typ byte, body []byte) {
		if typ == pktSubscribe {
			writeSuback(conn, body, 0x80)
		}
	})

	client, err := mqtt.Connect(context.Background(), options(addr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect()

	err = client.Subscribe("soundtap/x/#")
	if err == nil {
		t.Fatal("a refused subscription returned nil")
	}
}

// Cancelling ctx ends a handshake the broker never answers, without waiting out DialTimeout.
func TestConnectHonoursCancelDuringHandshake(t *testing.T) {
	t.Parallel()

	addr := silentBroker(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		opts := options(addr)
		opts.DialTimeout = 10 * time.Second

		_, err := mqtt.Connect(ctx, opts)
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
	t.Parallel()

	listener := listen(t)

	go func() {
		conn, _, ok := acceptConnect(listener)
		if !ok {
			return
		}

		_, _ = conn.Write([]byte{pktConnack << 4, 2, 0, 0})

		time.Sleep(50 * time.Millisecond)

		_ = conn.Close()
	}()

	client, err := mqtt.Connect(context.Background(), options(listener.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-client.Done():
		if client.Err() == nil {
			t.Fatal("a lost connection reported no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Done still open after the broker closed the connection")
	}

	err = client.Publish("soundtap/x/state", []byte("v"), false)
	if err == nil {
		t.Fatal("Publish on a lost connection returned nil")
	}
}

// Disconnect sends DISCONNECT, so the broker discards the Will, and closes Done cleanly.
func TestDisconnectSendsDisconnect(t *testing.T) {
	t.Parallel()

	got := make(chan byte, 4)
	addr := fakeBroker(t, func(_ net.Conn, typ byte, _ []byte) { got <- typ })

	opts := options(addr)
	opts.Will = availabilityWill()

	client, err := mqtt.Connect(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	client.Disconnect()

	select {
	case typ := <-got:
		if typ != pktDisconnect {
			t.Fatalf("broker got packet type %d, want DISCONNECT", typ)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("broker never got a DISCONNECT")
	}

	select {
	case <-client.Done():
		if client.Err() != nil {
			t.Fatalf("a clean Disconnect reported %v", client.Err())
		}
	default:
		t.Fatal("Done open after Disconnect")
	}

	client.Disconnect() // safe to repeat
}

// MQTT 3.1.1 forbids a password without a user name (§3.1.2.9): none is sent.
func TestPasswordOnlyWithAUsername(t *testing.T) {
	t.Parallel()

	listener := listen(t)
	connect := make(chan []byte, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		pkt, err := readPacket(bufio.NewReader(conn))
		if err != nil {
			return
		}

		connect <- pkt.body

		_, _ = conn.Write([]byte{pktConnack << 4, 2, 0, 0})

		time.Sleep(time.Second)
	}()

	opts := options(listener.Addr().String())
	opts.Password = "secret"

	client, err := mqtt.Connect(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect()

	body := <-connect
	// Protocol name "MQTT", level 4 (3.1.1), then the connect flags.
	if proto, rest, ok := takeString(body); !ok || proto != "MQTT" || len(rest) < 2 || rest[0] != 4 {
		t.Fatalf("CONNECT protocol %q, rest %v", proto, rest)
	}

	if flags := body[7]; flags&0xc0 != 0 || flags&0x02 == 0 {
		t.Fatalf("CONNECT flags %#x: want a clean session, no user name, no password", flags)
	}
}
