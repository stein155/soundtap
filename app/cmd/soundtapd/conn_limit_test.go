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
func (c fakeConn) Close() error         { *c.closed = true; return nil }

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
	a := l.addrs[0]
	l.addrs = l.addrs[1:]
	closed := new(bool)
	l.closed = append(l.closed, closed)
	return fakeConn{addr: a, closed: closed}, nil
}

func TestConnLimitCapsOneAddressAndFreesOnClose(t *testing.T) {
	var addrs []string
	for range maxConnsPerAddr + 1 {
		addrs = append(addrs, "192.168.1.30")
	}
	addrs = append(addrs, "192.168.1.31", "127.0.0.1", "192.168.1.30")
	fake := &fakeListener{addrs: addrs}
	ln := limitConns(fake)
	var held []net.Conn
	for range maxConnsPerAddr {
		c, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	// The next one from .30 is over its cap and closed; .31 and loopback still get in.
	c, err := ln.Accept()
	if err != nil || c.RemoteAddr().(*net.TCPAddr).IP.String() != "192.168.1.31" {
		t.Fatalf("after the cap got %v, %v; want the .31 connection", c, err)
	}
	if !*fake.closed[maxConnsPerAddr] {
		t.Error("the connection over the cap was not closed")
	}
	if c, err := ln.Accept(); err != nil || !c.RemoteAddr().(*net.TCPAddr).IP.IsLoopback() {
		t.Fatalf("loopback refused: %v, %v", c, err)
	}
	// A closed connection frees its slot, once even when closed twice.
	_ = held[0].Close()
	_ = held[0].Close()
	if c, err := ln.Accept(); err != nil || c.RemoteAddr().(*net.TCPAddr).IP.String() != "192.168.1.30" {
		t.Fatalf("after a close got %v, %v; want a .30 connection", c, err)
	}
	if l := ln; l.byAddr["192.168.1.30"] != maxConnsPerAddr || l.total != maxConnsPerAddr+1 {
		t.Errorf("counts = %d for .30, %d total", l.byAddr["192.168.1.30"], l.total)
	}
}

func TestConnLimitEvictsIdleConnAtTotalCap(t *testing.T) {
	var addrs []string
	for i := range maxConns {
		addrs = append(addrs, fmt.Sprintf("192.168.1.%d", i%8+10))
	}
	addrs = append(addrs, "192.168.2.1", "192.168.2.1")
	fake := &fakeListener{addrs: addrs}
	ln := limitConns(fake)
	var held []net.Conn
	for range maxConns {
		c, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		ln.connState(c, http.StateIdle)
		held = append(held, c)
	}
	// One connection is serving a request, so only idle ones may be evicted.
	ln.connState(held[0], http.StateActive)
	for range 2 {
		if _, err := ln.Accept(); err != nil {
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
	var addrs []string
	for range maxConnsPerAddr + 1 {
		addrs = append(addrs, "192.168.1.30")
	}
	fake := &fakeListener{addrs: append(addrs, "192.168.1.31")}
	ln := limitConns(fake)
	for range maxConnsPerAddr {
		c, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		ln.connState(c, http.StateIdle)
	}
	c, err := ln.Accept()
	if err != nil || c.RemoteAddr().(*net.TCPAddr).IP.String() != "192.168.1.30" {
		t.Fatalf("at the cap got %v, %v; want a .30 connection", c, err)
	}
	if !*fake.closed[0] {
		t.Error("the host's longest-idle connection was not closed to make room")
	}
	if l := ln; l.byAddr["192.168.1.30"] != maxConnsPerAddr {
		t.Errorf("count for .30 = %d, want %d", l.byAddr["192.168.1.30"], maxConnsPerAddr)
	}
}
