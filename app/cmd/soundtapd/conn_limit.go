// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
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

func limitConns(ln net.Listener) *connLimit {
	return &connLimit{Listener: ln, byAddr: map[string]int{}, idle: map[*limitedConn]uint64{}}
}

func (l *connLimit) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return c, nil
		}
		l.mu.Lock()
		victim := l.oldestIdleAtCap(host)
		l.mu.Unlock()
		if victim != nil {
			_ = victim.Close()
		}
		l.mu.Lock()
		ok := l.total < maxConns && l.byAddr[host] < maxConnsPerAddr
		if ok {
			l.total++
			l.byAddr[host]++
		}
		l.mu.Unlock()
		if !ok {
			_ = c.Close()
			continue
		}
		lc := &limitedConn{Conn: c, host: host}
		lc.release = sync.OnceFunc(func() { l.release(host, lc) })
		return lc, nil
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
func (l *connLimit) connState(c net.Conn, st http.ConnState) {
	lc, ok := c.(*limitedConn)
	if !ok {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if st == http.StateIdle {
		l.seq++
		l.idle[lc] = l.seq
	} else {
		delete(l.idle, lc)
	}
}

func (l *connLimit) release(host string, c *limitedConn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.idle, c)
	l.total--
	if l.byAddr[host]--; l.byAddr[host] <= 0 {
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
	return err
}

// CloseWrite keeps net/http's half-close before it drops a bad request's connection.
func (c *limitedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
