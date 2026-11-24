// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package abl

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// The bus at 19200 baud 8N2, as stock's abl.ko times it (docs/hardware/lisa/README.md).
const (
	byteTime      = 573 * time.Microsecond // 11 bits at 19200
	syncGap       = 1400 * time.Microsecond
	clientFirst   = 3 * time.Millisecond // 0.4 ms turnaround plus a byte, with room for jitter
	cycleWindow   = 15 * time.Millisecond
	inactiveAfter = 10 // client timeouts before a room counts as gone

	// OwnRoom is the adapter's room, A: stock's abl_local_address default.
	OwnRoom = 0
	// Broadcast is the datagram destination every room takes.
	Broadcast = 0x0f
)

// Port is the serial line: frames out, bytes in with a timeout.
type Port interface {
	Write(p []byte) (int, error)
	// ReadByteWithin returns the next byte, or ErrTimeout when none came within d.
	ReadByteWithin(d time.Duration) (byte, error)
}

// ErrTimeout is silence on the line.
var ErrTimeout = errors.New("abl: timeout")

// ErrCollision is an echo that differs from what was sent: another node talked.
var ErrCollision = errors.New("abl: collision")

// Event is what the bus saw: a poll reply, a query reply or a datagram.
type Event struct {
	Room     byte
	Poll     *PollReply
	Query    []byte // a 0x8B/0x8C client frame, whole
	QueryOf  byte   // the query type Query answers
	Datagram *Datagram
}

// Datagram is one received datagram segment's payload with its ports.
type Datagram struct {
	SrcRoom, DstRoom byte
	DstPort, SrcPort byte
	Data             []byte
}

type roomState struct {
	active    bool
	misses    int
	lastQuery byte // the query type last sent here, for the reply's length
	devType   byte
}

// Master is the active master of the bus in room A, as stock's adapter is: it polls the
// rooms, sends the queued commands and the adapter's own datagrams, and reports what
// answers.
type Master struct {
	port Port
	// Events receives everything the bus reports; it must not block for long.
	Events func(Event)

	mu      sync.Mutex
	queue   [][]byte // master frames waiting
	own     [][]byte // client frames of our own room waiting for its poll
	rooms   [15]roomState
	nextIdx int

	sleep func(time.Duration)
	now   func() time.Time
}

// NewMaster is a master on port.
func NewMaster(port Port, events func(Event)) *Master {
	return &Master{port: port, Events: events, sleep: time.Sleep, now: time.Now}
}

// Command queues a master command for room; it refuses what MasterFrame refuses.
func (m *Master) Command(cmd, room, arg byte) error {
	f, err := MasterFrame(cmd, room, arg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cmd == CmdQueryInfo && room < 15 {
		m.rooms[room].lastQuery = arg
	}
	m.queue = append(m.queue, f)
	return nil
}

// SendDatagram queues a single-segment datagram from our room, sent when our room is polled.
// Port 16 (the repository: host firmware updates) is refused.
func (m *Master) SendDatagram(dstRoom, dstPort, srcPort byte, data []byte) error {
	if dstPort == PortRepository || srcPort == PortRepository {
		return errors.New("abl: the repository port is never used")
	}
	f, err := P2PDatagram(OwnRoom, dstRoom, dstPort, srcPort, data)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.own = append(m.own, f)
	m.mu.Unlock()
	return nil
}

// Active reports the rooms that answer polls, ours excluded.
func (m *Master) Active() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []byte
	for i, r := range m.rooms {
		if r.active && i != OwnRoom {
			out = append(out, byte(i))
		}
	}
	return out
}

// Sync waits for the line to be quiet for the sync gap, as every node does before a
// master slot.
func (m *Master) Sync() {
	for range 200 {
		if _, err := m.port.ReadByteWithin(syncGap); errors.Is(err, ErrTimeout) {
			return
		}
	}
}

// Cycle runs one master slot: a queued command, our own datagram, or the next poll.
func (m *Master) Cycle() error {
	start := m.now()
	defer func() {
		if d := cycleWindow - m.now().Sub(start); d > 0 {
			m.sleep(d)
		}
	}()
	m.mu.Lock()
	var frame []byte
	var own []byte
	switch {
	case len(m.queue) > 0:
		frame, m.queue = m.queue[0], m.queue[1:]
	case len(m.own) > 0:
		frame, own, m.own = PollFrame(OwnRoom), m.own[0], m.own[1:]
	default:
		frame = PollFrame(m.nextRoomLocked())
	}
	m.mu.Unlock()
	room := frame[1] & 0x0f
	if err := m.send(frame); err != nil {
		return err
	}
	if room == OwnRoom {
		if own == nil {
			return nil
		}
		m.sleep(400 * time.Microsecond)
		return m.send(own)
	}
	reply, err := m.readClient(room)
	if err != nil {
		m.miss(room)
		if errors.Is(err, ErrTimeout) {
			return nil
		}
		return err
	}
	m.seen(room)
	m.dispatch(room, reply)
	return nil
}

// nextRoomLocked is the next room to poll: every active room, and one silent room in turn,
// so a host that comes up is found.
func (m *Master) nextRoomLocked() byte {
	for range 15 {
		m.nextIdx = (m.nextIdx + 1) % 15
		if m.nextIdx == OwnRoom {
			continue
		}
		r := &m.rooms[m.nextIdx]
		if r.active || m.nextIdx == m.probeRoom() {
			return byte(m.nextIdx) //nolint:gosec // 1..14
		}
	}
	return 1
}

// probeRoom is the silent room this round looks at: it turns with the clock, a room per
// 150 ms.
func (m *Master) probeRoom() int {
	return 1 + int(m.now().UnixMilli()/150)%14
}

func (m *Master) send(frame []byte) error {
	if _, err := m.port.Write(frame); err != nil {
		return err
	}
	// The bus echoes every byte: read ours back, and a difference is someone else talking.
	for i, want := range frame {
		got, err := m.port.ReadByteWithin(4 * byteTime)
		if err != nil {
			return fmt.Errorf("abl: echo byte %d: %w", i, err)
		}
		if got != want {
			m.Sync()
			return ErrCollision
		}
	}
	return nil
}

// readClient reads the addressed client's frame, its length by its first byte.
func (m *Master) readClient(room byte) ([]byte, error) {
	b0, err := m.port.ReadByteWithin(clientFirst)
	if err != nil {
		return nil, err
	}
	frame := []byte{b0}
	read := func(n int) error {
		for len(frame) < n {
			b, err := m.port.ReadByteWithin(4 * byteTime)
			if err != nil {
				return fmt.Errorf("abl: client frame from room %d after %d bytes: %w", room, len(frame), err)
			}
			frame = append(frame, b)
		}
		return nil
	}
	m.mu.Lock()
	q, dev := m.rooms[room].lastQuery, m.rooms[room].devType
	m.mu.Unlock()
	need := 4
	switch {
	case b0 == 0x8b || b0 == 0x8c:
		need = queryReplyLen(q, dev)
	case b0 == 0x8a:
		if err := read(4); err != nil {
			return nil, err
		}
		need = int(frame[3])
	case b0 >= 0xa0:
		if err := read(3); err != nil {
			return nil, err
		}
		need = int(frame[2])
	}
	if need < 4 {
		return nil, fmt.Errorf("abl: bad length %d from room %d", need, room)
	}
	if err := read(need); err != nil {
		return nil, err
	}
	if Checksum(frame) != 0 {
		return nil, ErrChecksum
	}
	return frame, nil
}

func queryReplyLen(query, devType byte) int {
	switch query {
	case QueryVersion:
		if devType == 3 {
			return 4
		}
		return 9
	case QuerySerial:
		return 20
	}
	return 4
}

func (m *Master) miss(room byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := &m.rooms[room]
	if r.misses++; r.misses > inactiveAfter {
		r.active = false
	}
}

func (m *Master) seen(room byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rooms[room].active, m.rooms[room].misses = true, 0
}

func (m *Master) dispatch(room byte, f []byte) {
	if m.Events == nil {
		return
	}
	switch f[0] {
	case 0x80:
		if r, err := ParsePollReply(f); err == nil {
			m.Events(Event{Room: room, Poll: &r})
		}
	case 0x8b, 0x8c:
		if len(f) >= 3 && m.lastQuery(room) == QuerySpeakerType {
			m.mu.Lock()
			m.rooms[room].devType = f[2]
			m.mu.Unlock()
		}
		m.Events(Event{Room: room, Query: f, QueryOf: m.lastQuery(room)})
	case 0xe0:
		if d, err := ParseP2PDatagram(f); err == nil {
			m.Events(Event{Room: room, Datagram: d})
		}
	}
}

func (m *Master) lastQuery(room byte) byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rooms[room].lastQuery
}

// Datagram ports.
const (
	PortCLI        = 9
	PortRepository = 16 // host firmware updates: never used
	PortRemote     = 18
	PortDiscovery  = 19
)

// P2PDatagram is a P2P 0xE0 frame carrying one datagram segment:
// [0xE0][src room][length][dst room][header 6][data][xor], the header
// [first segment][dst port][src port][segments left][checksum hi][checksum lo].
func P2PDatagram(src, dst, dstPort, srcPort byte, data []byte) ([]byte, error) {
	if len(data) > 244 {
		return nil, fmt.Errorf("abl: datagram of %d bytes needs segments", len(data))
	}
	if !P2PLengthOK(len(data) + 11) {
		// A P2P frame never has a multiple of 14 bytes (stock pads around a host firmware
		// that crashes on them): one more byte of data.
		data = append(append([]byte(nil), data...), 0)
	}
	payload := make([]byte, 0, 6+len(data))
	payload = append(payload, 0x08, dstPort, srcPort, 0, 0, 0)
	payload = append(payload, data...)
	sum := InternetChecksum(payload)
	payload[4], payload[5] = byte(sum>>8), byte(sum&0xff)
	f := append([]byte{0xe0, src & 0x0f, byte(len(payload) + 5), dst & 0x0f}, payload...) //nolint:gosec // at most 250: data is capped at 244 above
	return append(f, Checksum(f)), nil
}

// ParseP2PDatagram checks a P2P 0xE0 frame and its datagram checksum.
func ParseP2PDatagram(f []byte) (*Datagram, error) {
	if len(f) < 11 || f[0] != 0xe0 || int(f[2]) != len(f) {
		return nil, fmt.Errorf("abl: not a datagram frame: % x", f)
	}
	if Checksum(f) != 0 {
		return nil, ErrChecksum
	}
	payload := f[4 : len(f)-1]
	if InternetChecksum(payload) != 0 {
		return nil, errors.New("abl: datagram checksum")
	}
	return &Datagram{SrcRoom: f[1] & 0x0f, DstRoom: f[3] & 0x0f, DstPort: payload[1], SrcPort: payload[2],
		Data: append([]byte(nil), payload[6:]...)}, nil
}
