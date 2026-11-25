// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"fmt"
	"net"
	"net/http"
	"sync"
)

// The most connections soundtapd keeps open for one remote address and for all of them.
// An idle keep-alive connection is held for IdleTimeout (30 min), so without a cap one
// LAN host could open enough of them to run the speaker out of memory, which is a panic
// and a reboot. A browser opens about six per host and a peer speaker a few.
const (
	maxConnsPerAddr = 32
	maxConns        = 256
)

// connLimit closes, at accept, a connection over either cap. At a cap it first closes
// the longest-idle keep-alive connection that counts against it to make room, so idle
// connections cannot lock everyone else, or their own host, out for IdleTimeout; connections
// serving a request (events streams, long polls) are never evicted. Loopback is the speaker
// itself (the receivers' hooks) and is never counted.
type connLimit struct {
	net.Listener

	mu     sync.Mutex
	total  int
	byAddr map[string]int
	idle   map[*limitedConn]uint64 // idle keep-alive connections, by when they went idle
	seq    uint64
}

func limitConns(listener net.Listener) *connLimit {
	return &connLimit{
		Listener: listener,
		mu:       sync.Mutex{},
		total:    0,
		byAddr:   map[string]int{},
		idle:     map[*limitedConn]uint64{},
		seq:      0,
	}
}

func (l *connLimit) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			// Unwrapped: http.Server retries an Accept error only when the error itself is a
			// temporary net.Error, which it checks by type assertion.
			return nil, err //nolint:wrapcheck // http.Server type-asserts net.Error on it
		}

		host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return conn, nil
		}

		l.mu.Lock()
		victim := l.oldestIdleAtCap(host)
		l.mu.Unlock()

		if victim != nil {
			_ = victim.Close()
		}

		l.mu.Lock()

		admitted := l.total < maxConns && l.byAddr[host] < maxConnsPerAddr
		if admitted {
			l.total++
			l.byAddr[host]++
		}
		l.mu.Unlock()

		if !admitted {
			_ = conn.Close()

			continue
		}

		limited := &limitedConn{Conn: conn, host: host, release: nil}
		limited.release = sync.OnceFunc(func() { l.release(host, limited) })

		return limited, nil
	}
}

// oldestIdleAtCap returns the longest-idle connection when the total cap is reached, or
// host's own longest-idle one when host is at its cap. The caller holds l.mu.
func (l *connLimit) oldestIdleAtCap(host string) *limitedConn {
	all := l.total >= maxConns
	if !all && l.byAddr[host] < maxConnsPerAddr {
		return nil
	}

	var oldest *limitedConn
	for c, at := range l.idle {
		if (all || c.host == host) && (oldest == nil || at < l.idle[oldest]) {
			oldest = c
		}
	}

	return oldest
}

// connState is the http.Server.ConnState hook that tells the limiter which connections
// are idle between requests.
func (l *connLimit) connState(conn net.Conn, state http.ConnState) {
	limited, ok := conn.(*limitedConn)
	if !ok {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if state == http.StateIdle {
		l.seq++
		l.idle[limited] = l.seq
	} else {
		delete(l.idle, limited)
	}
}

func (l *connLimit) release(host string, c *limitedConn) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.idle, c)

	l.total--

	l.byAddr[host]--
	if l.byAddr[host] <= 0 {
		delete(l.byAddr, host)
	}
}

// limitedConn gives its slot back on the first Close.
type limitedConn struct {
	net.Conn

	host    string
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.release()

	if err != nil {
		return fmt.Errorf("close: %w", err)
	}

	return nil
}

// CloseWrite keeps net/http's half-close before it drops a bad request's connection.
func (c *limitedConn) CloseWrite() error {
	if halfCloser, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		err := halfCloser.CloseWrite()
		if err != nil {
			return fmt.Errorf("close write: %w", err)
		}
	}

	return nil
}
