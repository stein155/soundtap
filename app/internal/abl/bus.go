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

	// roomCount is the rooms a master polls, A to O.
	roomCount = 15
	// probeEvery is how long each silent room is the one a round looks at.
	probeEvery = 150 * time.Millisecond
	// ownSlot is the wait before our own client frame after our room's poll: the 0.4 ms
	// reply slot.
	ownSlot = 400 * time.Microsecond
	// echoWait is how long a byte of a frame may take after the one before.
	echoWait = 4 * byteTime
)

// Client frame types, by their first byte.
const (
	clientPollReply   = 0x80
	clientLengthAt3   = 0x8a // its length in byte 3
	clientQueryReply  = 0x8b
	clientQueryReply2 = 0x8c
	clientLengthAt2   = 0xa0 // and up: its length in byte 2
	clientDatagram    = 0xe0
	// minClientFrame is the shortest client frame: [type][room][arg][xor].
	minClientFrame = 4
	// lengthAt3Head and lengthAt2Head are what is read of those frames to find the length.
	lengthAt3Head = 4
	lengthAt2Head = 3
)

// The lengths of the query replies, by what was asked.
const (
	shortQueryReplyLen   = 4
	versionQueryReplyLen = 9
	serialQueryReplyLen  = 20
)

// The P2P datagram frame: [0xE0][src room][length][dst room][header 6][data][xor].
const (
	p2pHeadLen        = 4
	datagramHeaderLen = 6
	p2pOverhead       = p2pHeadLen + datagramHeaderLen + 1
	// maxDatagramData is the most data one segment carries.
	maxDatagramData = 244
	// segmentFirst marks the first segment of a datagram.
	segmentFirst = 0x08
)

// Errors of the bus; each message goes on with the details where it has any.
var (
	errRepositoryPort   = errors.New("abl: the repository port is never used")
	errBadLength        = errors.New("abl: bad length")
	errNeedsSegments    = errors.New("needs segments")
	errNotDatagram      = errors.New("abl: not a datagram frame")
	errDatagramChecksum = errors.New("abl: datagram checksum")
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
	rooms   [roomCount]roomState
	nextIdx int

	sleep func(time.Duration)
	now   func() time.Time
}

// NewMaster is a master on port.
func NewMaster(port Port, events func(Event)) *Master {
	return &Master{
		port:    port,
		Events:  events,
		mu:      sync.Mutex{},
		queue:   nil,
		own:     nil,
		rooms:   [roomCount]roomState{},
		nextIdx: 0,
		sleep:   time.Sleep,
		now:     time.Now,
	}
}

// Command queues a master command for room; it refuses what MasterFrame refuses.
func (m *Master) Command(cmd, room, arg byte) error {
	frame, err := MasterFrame(cmd, room, arg)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if cmd == CmdQueryInfo && room < roomCount {
		m.rooms[room].lastQuery = arg
	}

	m.queue = append(m.queue, frame)

	return nil
}

// SendDatagram queues a single-segment datagram from our room, sent when our room is polled.
// Port 16 (the repository: host firmware updates) is refused.
func (m *Master) SendDatagram(dstRoom, dstPort, srcPort byte, data []byte) error {
	if dstPort == PortRepository || srcPort == PortRepository {
		return errRepositoryPort
	}

	frame, err := P2PDatagram(OwnRoom, dstRoom, dstPort, srcPort, data)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.own = append(m.own, frame)
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
		_, err := m.port.ReadByteWithin(syncGap)
		if errors.Is(err, ErrTimeout) {
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

	var (
		frame []byte
		own   []byte
	)
	switch {
	case len(m.queue) > 0:
		frame, m.queue = m.queue[0], m.queue[1:]
	case len(m.own) > 0:
		frame, own, m.own = PollFrame(OwnRoom), m.own[0], m.own[1:]
	default:
		frame = PollFrame(m.nextRoomLocked())
	}
	m.mu.Unlock()

	room := frame[1] & roomMask

	err := m.send(frame)
	if err != nil {
		return err
	}

	if room == OwnRoom {
		if own == nil {
			return nil
		}

		m.sleep(ownSlot)

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
	for range roomCount {
		m.nextIdx = (m.nextIdx + 1) % roomCount
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
	return 1 + int(m.now().UnixMilli()/probeEvery.Milliseconds())%(roomCount-1)
}

func (m *Master) send(frame []byte) error {
	_, err := m.port.Write(frame)
	if err != nil {
		return fmt.Errorf("abl: write: %w", err)
	}
	// The bus echoes every byte: read ours back, and a difference is someone else talking.
	for i, want := range frame {
		got, err := m.port.ReadByteWithin(echoWait)
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
	first, err := m.port.ReadByteWithin(clientFirst)
	if err != nil {
		return nil, fmt.Errorf("abl: client frame from room %d: %w", room, err)
	}

	frame := []byte{first}
	read := func(n int) error {
		for len(frame) < n {
			next, readErr := m.port.ReadByteWithin(echoWait)
			if readErr != nil {
				return fmt.Errorf("abl: client frame from room %d after %d bytes: %w", room, len(frame), readErr)
			}

			frame = append(frame, next)
		}

		return nil
	}

	m.mu.Lock()
	query, dev := m.rooms[room].lastQuery, m.rooms[room].devType
	m.mu.Unlock()

	need := minClientFrame

	switch {
	case first == clientQueryReply || first == clientQueryReply2:
		need = queryReplyLen(query, dev)
	case first == clientLengthAt3:
		err = read(lengthAt3Head)
		if err != nil {
			return nil, err
		}

		need = int(frame[3])
	case first >= clientLengthAt2:
		err = read(lengthAt2Head)
		if err != nil {
			return nil, err
		}

		need = int(frame[2])
	default:
		// A poll reply and the rest: the shortest frame.
	}

	if need < minClientFrame {
		return nil, fmt.Errorf("%w %d from room %d", errBadLength, need, room)
	}

	err = read(need)
	if err != nil {
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
		if devType == speakerTypeColin {
			return shortQueryReplyLen
		}

		return versionQueryReplyLen
	case QuerySerial:
		return serialQueryReplyLen
	default:
		return shortQueryReplyLen
	}
}

func (m *Master) miss(room byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r := &m.rooms[room]

	r.misses++
	if r.misses > inactiveAfter {
		r.active = false
	}
}

func (m *Master) seen(room byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rooms[room].active, m.rooms[room].misses = true, 0
}

func (m *Master) dispatch(room byte, frame []byte) {
	if m.Events == nil {
		return
	}

	switch frame[0] {
	case clientPollReply:
		reply, err := ParsePollReply(frame)
		if err == nil {
			m.Events(Event{Room: room, Poll: &reply, Query: nil, QueryOf: 0, Datagram: nil})
		}
	case clientQueryReply, clientQueryReply2:
		if len(frame) >= 3 && m.lastQuery(room) == QuerySpeakerType {
			m.mu.Lock()
			m.rooms[room].devType = frame[2]
			m.mu.Unlock()
		}

		m.Events(Event{Room: room, Poll: nil, Query: frame, QueryOf: m.lastQuery(room), Datagram: nil})
	case clientDatagram:
		datagram, err := ParseP2PDatagram(frame)
		if err == nil {
			m.Events(Event{Room: room, Poll: nil, Query: nil, QueryOf: 0, Datagram: datagram})
		}
	default:
		// Nothing the session takes.
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
	if len(data) > maxDatagramData {
		return nil, fmt.Errorf("abl: datagram of %d bytes %w", len(data), errNeedsSegments)
	}

	if !P2PLengthOK(len(data) + p2pOverhead) {
		// A P2P frame never has a multiple of 14 bytes (stock pads around a host firmware
		// that crashes on them): one more byte of data.
		data = append(append([]byte(nil), data...), 0)
	}

	payload := make([]byte, 0, datagramHeaderLen+len(data))
	payload = append(payload, segmentFirst, dstPort, srcPort, 0, 0, 0)
	payload = append(payload, data...)
	sum := InternetChecksum(payload)
	payload[4], payload[5] = byte(sum>>byteBits), byte(sum&byteMask)
	// At most 250 bytes: data is capped at maxDatagramData above.
	length := byte(len(payload) + p2pHeadLen + 1) //nolint:gosec // capped above
	frame := append([]byte{clientDatagram, src & roomMask, length, dst & roomMask}, payload...)

	return append(frame, Checksum(frame)), nil
}

// ParseP2PDatagram checks a P2P 0xE0 frame and its datagram checksum.
func ParseP2PDatagram(frame []byte) (*Datagram, error) {
	if len(frame) < p2pOverhead || frame[0] != clientDatagram || int(frame[2]) != len(frame) {
		return nil, fmt.Errorf("%w: % x", errNotDatagram, frame)
	}

	if Checksum(frame) != 0 {
		return nil, ErrChecksum
	}

	payload := frame[p2pHeadLen : len(frame)-1]
	if InternetChecksum(payload) != 0 {
		return nil, errDatagramChecksum
	}

	return &Datagram{
		SrcRoom: frame[1] & roomMask, DstRoom: frame[3] & roomMask, DstPort: payload[1], SrcPort: payload[2],
		Data: append([]byte(nil), payload[datagramHeaderLen:]...),
	}, nil
}
