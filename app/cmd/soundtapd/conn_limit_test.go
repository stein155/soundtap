// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"fmt"
	"net"
	"net/http"
	"testing"
)

// fakeConn is a connection from addr.
type fakeConn struct {
	net.Conn

	addr   string
	closed *bool
}

func (c fakeConn) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.ParseIP(c.addr), Port: 40000} }

func (c fakeConn) Close() error {
	*c.closed = true

	return nil
}

// fakeListener hands out one connection per address in order.
type fakeListener struct {
	net.Listener

	addrs  []string
	closed []*bool
}

func (l *fakeListener) Accept() (net.Conn, error) {
	if len(l.addrs) == 0 {
		return nil, net.ErrClosed
	}

	addr := l.addrs[0]
	l.addrs = l.addrs[1:]
	closed := new(bool)
	l.closed = append(l.closed, closed)

	return fakeConn{Conn: nil, addr: addr, closed: closed}, nil
}

func TestConnLimitCapsOneAddressAndFreesOnClose(t *testing.T) {
	t.Parallel()

	addrs := make([]string, 0, maxConnsPerAddr+1+3)
	for range maxConnsPerAddr + 1 {
		addrs = append(addrs, "192.168.1.30")
	}

	addrs = append(addrs, "192.168.1.31", "127.0.0.1", "192.168.1.30")
	fake := &fakeListener{Listener: nil, addrs: addrs, closed: nil}
	limiter := limitConns(fake)

	held := make([]net.Conn, 0, maxConnsPerAddr)

	for range maxConnsPerAddr {
		conn, err := limiter.Accept()
		if err != nil {
			t.Fatal(err)
		}

		held = append(held, conn)
	}
	// The next one from .30 is over its cap and closed; .31 and loopback still get in.
	conn, err := limiter.Accept()
	if err != nil || conn.RemoteAddr().(*net.TCPAddr).IP.String() != "192.168.1.31" {
		t.Fatalf("after the cap got %v, %v; want the .31 connection", conn, err)
	}

	if !*fake.closed[maxConnsPerAddr] {
		t.Error("the connection over the cap was not closed")
	}

	conn, err = limiter.Accept()
	if err != nil || !conn.RemoteAddr().(*net.TCPAddr).IP.IsLoopback() {
		t.Fatalf("loopback refused: %v, %v", conn, err)
	}
	// A closed connection frees its slot, once even when closed twice.
	_ = held[0].Close()
	_ = held[0].Close()

	conn, err = limiter.Accept()
	if err != nil || conn.RemoteAddr().(*net.TCPAddr).IP.String() != "192.168.1.30" {
		t.Fatalf("after a close got %v, %v; want a .30 connection", conn, err)
	}

	if l := limiter; l.byAddr["192.168.1.30"] != maxConnsPerAddr || l.total != maxConnsPerAddr+1 {
		t.Errorf("counts = %d for .30, %d total", l.byAddr["192.168.1.30"], l.total)
	}
}

func TestConnLimitEvictsIdleConnAtTotalCap(t *testing.T) {
	t.Parallel()

	addrs := make([]string, 0, maxConns+2)
	for i := range maxConns {
		addrs = append(addrs, fmt.Sprintf("192.168.1.%d", i%8+10))
	}

	addrs = append(addrs, "192.168.2.1", "192.168.2.1")
	fake := &fakeListener{Listener: nil, addrs: addrs, closed: nil}
	limiter := limitConns(fake)

	held := make([]net.Conn, 0, maxConns)

	for range maxConns {
		conn, err := limiter.Accept()
		if err != nil {
			t.Fatal(err)
		}

		limiter.connState(conn, http.StateIdle)
		held = append(held, conn)
	}
	// One connection is serving a request, so only idle ones may be evicted.
	limiter.connState(held[0], http.StateActive)

	for range 2 {
		_, err := limiter.Accept()
		if err != nil {
			t.Fatal(err)
		}
	}

	if *fake.closed[0] {
		t.Error("an active connection was evicted")
	}

	if !*fake.closed[1] || !*fake.closed[2] {
		t.Error("the longest-idle connections were not evicted to admit new clients")
	}
}

// A host at its own cap gets in again by closing its longest-idle connection, so a
// client that leaves keep-alives open is not locked out for IdleTimeout.
func TestConnLimitEvictsOwnIdleConnAtAddressCap(t *testing.T) {
	t.Parallel()

	addrs := make([]string, 0, maxConnsPerAddr+1+1)
	for range maxConnsPerAddr + 1 {
		addrs = append(addrs, "192.168.1.30")
	}

	fake := &fakeListener{Listener: nil, addrs: append(addrs, "192.168.1.31"), closed: nil}

	limiter := limitConns(fake)
	for range maxConnsPerAddr {
		conn, err := limiter.Accept()
		if err != nil {
			t.Fatal(err)
		}

		limiter.connState(conn, http.StateIdle)
	}

	conn, err := limiter.Accept()
	if err != nil || conn.RemoteAddr().(*net.TCPAddr).IP.String() != "192.168.1.30" {
		t.Fatalf("at the cap got %v, %v; want a .30 connection", conn, err)
	}

	if !*fake.closed[0] {
		t.Error("the host's longest-idle connection was not closed to make room")
	}

	if l := limiter; l.byAddr["192.168.1.30"] != maxConnsPerAddr {
		t.Errorf("count for .30 = %d, want %d", l.byAddr["192.168.1.30"], maxConnsPerAddr)
	}
}

// An IPv6 host's /64 is one address against the cap; link-local and IPv4 addresses are
// their own, and loopback is never counted.
func TestLimitKeyCountsAnIPv6HostByItsPrefix(t *testing.T) {
	t.Parallel()

	for remote, want := range map[string]string{
		"192.0.2.7:51234":                 "192.0.2.7",
		"[::ffff:192.0.2.7]:51234":        "192.0.2.7",
		"[2001:db8:1:2::7]:51234":         "2001:db8:1:2::/64",
		"[2001:db8:1:2:a1b2:c3d4::9]:443": "2001:db8:1:2::/64",
		"[fe80::7%wlan0]:51234":           "fe80::7",
		"[fe80::8%wlan0]:51234":           "fe80::8",
	} {
		got, loopback := limitKey(remote)
		if got != want || loopback {
			t.Errorf("limitKey(%s) = %q, %v; want %q, false", remote, got, loopback, want)
		}
	}

	for _, remote := range []string{"127.0.0.1:1", "[::1]:1"} {
		if _, loopback := limitKey(remote); !loopback {
			t.Errorf("limitKey(%s) is not loopback", remote)
		}
	}
}
