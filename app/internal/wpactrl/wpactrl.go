// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package wpactrl speaks wpa_supplicant's control interface: datagrams on the Unix socket
// wpa_supplicant keeps for each interface under its ctrl_interface directory
// (/var/run/wpa_supplicant/wlan0 on the speaker). A client binds a socket of its own, sends
// one command per datagram and gets one reply. After ATTACH the same socket also carries
// unsolicited events, each starting with "<level>", such as
// "<3>CTRL-EVENT-CONNECTED - Connection to … completed [id=0 id_str=]".
//
// This is what wpa_cli does with wpa_ctrl.c, in Go: soundtapd needs a handful of commands
// (STATUS, ADD_NETWORK, SET_NETWORK, ENABLE_NETWORK, SCAN_RESULTS, SIGNAL_POLL) and the event
// stream, so it neither forks wpa_cli nor carries a library for it (ADR 0115).
package wpactrl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
)

// Dir is where wpa_supplicant keeps its control sockets on the speaker: the
// ctrl_interface soundtapd writes into its configuration.
const Dir = "/var/run/wpa_supplicant"

// ClientDir is where a client binds its own socket. wpa_ctrl.c uses /tmp; /run/soundtap is
// soundtapd's and not writable by the media daemons' users.
var ClientDir = "/run/soundtap"

// DefaultTimeout bounds a request without a deadline of its own, as wpa_ctrl.c's 10 s.
const DefaultTimeout = 10 * time.Second

// maxDatagram is the largest reply read. SCAN_RESULTS with many networks is the longest;
// wpa_cli reads 4 KiB, wpa_supplicant replies up to its own 4 KiB buffer per command, and
// a bigger buffer costs nothing here.
const maxDatagram = 64 << 10

// ErrClosed is a request on a connection that is closed, or whose reader stopped.
var ErrClosed = errors.New("wpactrl: connection closed")

// Event is one unsolicited message after ATTACH.
type Event struct {
	Level int    // the priority in <N>, MSG_INFO = 3
	Name  string // the first word, such as CTRL-EVENT-CONNECTED
	Text  string // the message without the <N> prefix
}

// Field returns the value of key=value in the event's text, unquoted when quoted, and
// whether it was there: CTRL-EVENT-SSID-TEMP-DISABLED's reason=WRONG_KEY, or [id=0].
func (e Event) Field(key string) (string, bool) {
	for _, w := range strings.Fields(strings.Trim(e.Text, "[]")) {
		w = strings.Trim(w, "[]")
		if v, ok := strings.CutPrefix(w, key+"="); ok {
			if u, err := strconv.Unquote(v); err == nil {
				v = u
			}
			return v, true
		}
	}
	return "", false
}

// ParseEvent reads "<3>CTRL-EVENT-…": ok is false for anything without the <N> prefix.
func ParseEvent(msg string) (Event, bool) {
	rest, ok := strings.CutPrefix(msg, "<")
	if !ok {
		return Event{}, false
	}
	n, text, ok := strings.Cut(rest, ">")
	level, err := strconv.Atoi(n)
	if !ok || err != nil {
		return Event{}, false
	}
	text = strings.TrimRight(text, "\n")
	name, _, _ := strings.Cut(text, " ")
	return Event{Level: level, Name: name, Text: text}, true
}

// Conn is one control connection. Requests are serialised; events arrive on Events once
// Attach succeeded.
type Conn struct {
	sock    *net.UnixConn
	local   string
	reqMu   sync.Mutex
	replies chan string
	events  chan Event
	done    chan struct{}
	closing atomic.Bool
	once    sync.Once
}

var clientSeq atomic.Uint64

// Dial connects to the control socket at path, such as Dir+"/wlan0".
func Dial(path string) (*Conn, error) {
	local := filepath.Join(ClientDir, fmt.Sprintf("wpa_ctrl_%d-%d", os.Getpid(), clientSeq.Add(1)))
	_ = os.Remove(local) // a leftover of an earlier process with this pid
	sock, err := net.DialUnix("unixgram",
		&net.UnixAddr{Name: local, Net: "unixgram"}, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		_ = os.Remove(local)
		return nil, fmt.Errorf("wpactrl: %w", err)
	}
	c := &Conn{
		sock:    sock,
		local:   local,
		replies: make(chan string, 1),
		events:  make(chan Event, 64),
		done:    make(chan struct{}),
	}
	safego.Go("wpactrl-read", c.read)
	return c, nil
}

// read sorts every datagram into a reply or an event until the socket closes. An event
// that finds Events full is dropped, as wpa_cli drops what it cannot print: the reader
// never blocks requests behind a slow consumer.
func (c *Conn) read() {
	defer close(c.done)
	defer close(c.events)
	buf := make([]byte, maxDatagram)
	for {
		n, err := c.sock.Read(buf)
		if err != nil {
			return
		}
		msg := string(buf[:n])
		if ev, ok := ParseEvent(msg); ok {
			select {
			case c.events <- ev:
			default:
			}
			continue
		}
		select {
		case c.replies <- msg:
		default: // nobody asked: a reply to a request that already gave up
		}
	}
}

// Events delivers what wpa_supplicant reports after Attach. It is closed with the
// connection.
func (c *Conn) Events() <-chan Event { return c.events }

// Request sends cmd and returns the reply as wpa_supplicant wrote it. A request waits for
// its reply until ctx ends, or DefaultTimeout without a deadline.
func (c *Conn) Request(ctx context.Context, cmd string) (string, error) {
	if strings.ContainsAny(cmd, "\n\x00") {
		return "", fmt.Errorf("wpactrl: command must be one line")
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()
	select { // a late reply to a request that gave up is not this one's
	case <-c.replies:
	default:
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	if _, err := c.sock.Write([]byte(cmd)); err != nil {
		if c.closing.Load() {
			return "", ErrClosed
		}
		return "", fmt.Errorf("wpactrl %s: %w", verb(cmd), err)
	}
	select {
	case r := <-c.replies:
		return r, nil
	case <-c.done:
		return "", ErrClosed
	case <-ctx.Done():
		return "", fmt.Errorf("wpactrl %s: %w", verb(cmd), ctx.Err())
	}
}

// verb is the command's first word, so a key in SET_NETWORK never reaches an error text.
func verb(cmd string) string {
	v, _, _ := strings.Cut(cmd, " ")
	return v
}

// OK sends cmd and fails unless wpa_supplicant answered OK.
func (c *Conn) OK(ctx context.Context, cmd string) error {
	r, err := c.Request(ctx, cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(r) != "OK" {
		return fmt.Errorf("wpactrl %s: %s", verb(cmd), firstLine(r))
	}
	return nil
}

// Attach subscribes this connection to events.
func (c *Conn) Attach(ctx context.Context) error { return c.OK(ctx, "ATTACH") }

// Ping reports whether wpa_supplicant still answers. A datagram socket has no end of
// stream: a supplicant that went away shows only as a request without a reply.
func (c *Conn) Ping(ctx context.Context) bool {
	r, err := c.Request(ctx, "PING")
	return err == nil && strings.TrimSpace(r) == "PONG"
}

// Status is STATUS as key=value pairs: wpa_state, ssid, id, ip_address and the rest.
func (c *Conn) Status(ctx context.Context) (map[string]string, error) {
	r, err := c.Request(ctx, "STATUS")
	if err != nil {
		return nil, err
	}
	return ParseKV(r), nil
}

// AddNetwork creates an empty, disabled network block and returns its id.
func (c *Conn) AddNetwork(ctx context.Context) (int, error) {
	r, err := c.Request(ctx, "ADD_NETWORK")
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(strings.TrimSpace(r))
	if err != nil {
		return 0, fmt.Errorf("wpactrl ADD_NETWORK: %s", firstLine(r))
	}
	return id, nil
}

// SetNetwork sets one variable of a network block. value is passed as wpa_supplicant's
// configuration syntax: a quoted string, or hex and numbers bare.
func (c *Conn) SetNetwork(ctx context.Context, id int, key, value string) error {
	if strings.ContainsAny(key, " \n") {
		return fmt.Errorf("wpactrl SET_NETWORK: bad variable name")
	}
	return c.OK(ctx, fmt.Sprintf("SET_NETWORK %d %s %s", id, key, value))
}

// EnableNetwork lets wpa_supplicant pick network id, by priority, among the enabled ones.
func (c *Conn) EnableNetwork(ctx context.Context, id int) error {
	return c.OK(ctx, fmt.Sprintf("ENABLE_NETWORK %d", id))
}

// SelectNetwork enables network id alone and disables every other one.
func (c *Conn) SelectNetwork(ctx context.Context, id int) error {
	return c.OK(ctx, fmt.Sprintf("SELECT_NETWORK %d", id))
}

// RemoveAllNetworks forgets every network block.
func (c *Conn) RemoveAllNetworks(ctx context.Context) error {
	return c.OK(ctx, "REMOVE_NETWORK all")
}

// Scan asks for a new scan; the results follow with CTRL-EVENT-SCAN-RESULTS. A scan
// already running answers FAIL-BUSY, which is reported as nil: its results come too.
func (c *Conn) Scan(ctx context.Context) error {
	r, err := c.Request(ctx, "SCAN")
	if err != nil {
		return err
	}
	switch strings.TrimSpace(r) {
	case "OK", "FAIL-BUSY":
		return nil
	}
	return fmt.Errorf("wpactrl SCAN: %s", firstLine(r))
}

// Reconfigure makes wpa_supplicant read its configuration file again.
func (c *Conn) Reconfigure(ctx context.Context) error { return c.OK(ctx, "RECONFIGURE") }

// BSS is one row of SCAN_RESULTS.
type BSS struct {
	BSSID  string
	Freq   int // MHz
	Signal int // dBm
	Flags  string
	SSID   string // as wpa_supplicant prints it: non-printable bytes escaped
}

// ScanResults is the last scan's table.
func (c *Conn) ScanResults(ctx context.Context) ([]BSS, error) {
	r, err := c.Request(ctx, "SCAN_RESULTS")
	if err != nil {
		return nil, err
	}
	return ParseScanResults(r), nil
}

// ParseScanResults reads SCAN_RESULTS: a header, then bssid, frequency, signal level, flags
// and ssid separated by tabs. Spaces are part of an SSID and stay.
func ParseScanResults(out string) []BSS {
	var res []BSS
	for i, line := range strings.Split(out, "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 5)
		if len(f) < 5 {
			continue
		}
		freq, _ := strconv.Atoi(strings.TrimSpace(f[1]))
		sig, err := strconv.Atoi(strings.TrimSpace(f[2]))
		if err != nil {
			continue
		}
		res = append(res, BSS{BSSID: f[0], Freq: freq, Signal: sig, Flags: f[3], SSID: f[4]})
	}
	return res
}

// SignalPoll is SIGNAL_POLL as key=value pairs: RSSI, LINKSPEED, FREQUENCY.
func (c *Conn) SignalPoll(ctx context.Context) (map[string]string, error) {
	r, err := c.Request(ctx, "SIGNAL_POLL")
	if err != nil {
		return nil, err
	}
	return ParseKV(r), nil
}

// ParseKV reads key=value lines. Values keep inner spaces; leading indentation goes.
func ParseKV(out string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimLeft(strings.TrimRight(line, "\r"), " \t"), "=")
		if ok && k != "" {
			kv[k] = v
		}
	}
	return kv
}

// Close detaches and closes the connection and removes its socket file.
func (c *Conn) Close() error {
	var err error
	c.once.Do(func() {
		c.closing.Store(true)
		err = c.sock.Close()
		<-c.done
		_ = os.Remove(c.local)
	})
	return err
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}
