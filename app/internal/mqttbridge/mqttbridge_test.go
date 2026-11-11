// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mqttbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/update"
)

// fakeBroker is a throwaway MQTT broker for tests: it completes the handshake,
// records every retained PUBLISH by topic, and can push a command to the client. It
// re-implements just enough of the wire format (QoS 0) to avoid depending on the
// mqtt package internals.
type fakeBroker struct {
	ln net.Listener

	mu    sync.Mutex
	pub   map[string]string
	subs  []string // topic filters of the client's SUBSCRIBEs
	conn  net.Conn
	conns int // connections that completed the handshake
}

func newFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	b := &fakeBroker{ln: ln, pub: map[string]string{}}
	go b.accept()
	t.Cleanup(func() { _ = ln.Close() })
	return b
}

func (b *fakeBroker) addr() (host string, port int) {
	a := b.ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

func (b *fakeBroker) accept() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			return
		}
		go b.serveConn(conn)
	}
}

func (b *fakeBroker) serveConn(conn net.Conn) {
	br := bufio.NewReader(conn)
	// CONNECT
	if _, _, err := readFrame(br); err != nil {
		return
	}
	// CONNACK
	if _, err := conn.Write([]byte{2 << 4, 2, 0, 0}); err != nil {
		return
	}
	b.mu.Lock()
	b.conn = conn
	b.conns++
	b.mu.Unlock()

	for {
		typ, body, err := readFrame(br)
		if err != nil {
			return
		}
		if typ == 8 { // SUBSCRIBE
			if len(body) >= 2 {
				// Packet id, then per filter a length-prefixed topic and a QoS byte.
				var filters []string
				for rest := body[2:]; len(rest) >= 2; {
					n := int(rest[0])<<8 | int(rest[1])
					if len(rest) < 2+n+1 {
						break
					}
					filters = append(filters, string(rest[2:2+n]))
					rest = rest[2+n+1:]
				}
				b.mu.Lock()
				b.subs = append(b.subs, filters...)
				b.mu.Unlock()
				// MQTT 3.1.1 SUBACK: packet id + one granted QoS per topic filter.
				ack := append([]byte{9 << 4, byte(2 + len(filters)), body[0], body[1]}, make([]byte, len(filters))...)
				_, _ = conn.Write(ack)
			}
			continue
		}
		if typ == 3 { // PUBLISH (QoS 0: no packet id)
			topic, payload := parsePub(body)
			b.mu.Lock()
			b.pub[topic] = string(payload)
			b.mu.Unlock()
		}
	}
}

// drop closes the client's connection without a DISCONNECT, as a broker restart or a
// lost network does, and forgets what the client published and subscribed to.
func (b *fakeBroker) drop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		_ = b.conn.Close()
		b.conn = nil
	}
	b.pub = map[string]string{}
	b.subs = nil
}

// connections reports how many client connections completed the handshake.
func (b *fakeBroker) connections() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.conns
}

// published returns the last retained payload seen for topic.
func (b *fakeBroker) published(topic string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.pub[topic]
	return v, ok
}

// subscriptions returns the topic filters the client subscribed to.
func (b *fakeBroker) subscriptions() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.subs...)
}

// send pushes a live PUBLISH (a command) to the connected client.
func (b *fakeBroker) send(t *testing.T, topic, payload string) {
	t.Helper()
	b.write(t, 0, topic, payload)
}

// sendRetained pushes a PUBLISH with the RETAIN flag, as a broker replays a stored
// message to a new subscription.
func (b *fakeBroker) sendRetained(t *testing.T, topic, payload string) {
	t.Helper()
	b.write(t, 0x01, topic, payload)
}

func (b *fakeBroker) write(t *testing.T, flags byte, topic, payload string) {
	t.Helper()
	b.mu.Lock()
	conn := b.conn
	b.mu.Unlock()
	if conn == nil {
		t.Fatal("broker has no client connection yet")
	}
	var vh []byte
	vh = append(vh, byte(len(topic)>>8), byte(len(topic)))
	vh = append(vh, topic...)
	vh = append(vh, payload...)
	frame := append([]byte{3<<4 | flags}, encodeRemaining(len(vh))...)
	frame = append(frame, vh...)
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("broker send: %v", err)
	}
}

func readFrame(br *bufio.Reader) (typ byte, body []byte, err error) {
	first, err := br.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	var n, mult int
	for {
		b, err := br.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n += int(b&0x7f) << mult
		if b&0x80 == 0 {
			break
		}
		mult += 7
	}
	body = make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		return 0, nil, err
	}
	return first >> 4, body, nil
}

func parsePub(body []byte) (topic string, payload []byte) {
	if len(body) < 2 {
		return "", nil
	}
	n := int(body[0])<<8 | int(body[1])
	if len(body) < 2+n {
		return "", nil
	}
	return string(body[2 : 2+n]), body[2+n:]
}

func encodeRemaining(n int) []byte {
	var out []byte
	for {
		d := byte(n % 128)
		n /= 128
		if n > 0 {
			d |= 0x80
		}
		out = append(out, d)
		if n == 0 {
			return out
		}
	}
}

// newSimSpeaker returns an in-process speaker with the identity soundtapd gives it.
func newSimSpeaker(t *testing.T) *speakerstate.Speaker {
	t.Helper()
	sp := speakerstate.New()
	sp.DeviceID = "000000000000"
	return sp
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestBridgeDiscoveryAndCommands(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	sp := newSimSpeaker(t)

	cfg := Config{
		Enabled:         true,
		Host:            host,
		Port:            port,
		BaseTopic:       "soundtap/test",
		DiscoveryPrefix: "homeassistant",
	}
	b := New(sp, func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	// The simulator's device id (see internal/sim).
	const dev = "000000000000"

	// Discovery configs are published for the entities.
	for _, topic := range []string{
		"homeassistant/number/" + dev + "/volume/config",
		"homeassistant/switch/" + dev + "/power/config",
		"homeassistant/select/" + dev + "/preset/config",
		"homeassistant/button/" + dev + "/play/config",
		"homeassistant/button/" + dev + "/next/config",
		"homeassistant/sensor/" + dev + "/station/config",
	} {
		topic := topic
		waitFor(t, topic, func() bool {
			_, ok := broker.published(topic)
			return ok
		})
	}

	// Availability comes up online.
	waitFor(t, "availability online", func() bool {
		v, ok := broker.published("soundtap/test/availability")
		return ok && v == "online"
	})

	// Volume state is published.
	waitFor(t, "volume state", func() bool {
		_, ok := broker.published("soundtap/test/volume/state")
		return ok
	})

	// A volume command routes through to the speaker.
	broker.send(t, "soundtap/test/volume/set", "37")
	waitFor(t, "volume applied to speaker", func() bool {
		v, err := sp.Volume(context.Background())
		return err == nil && v == 37
	})

	// The update entity is absent when no updater is wired in.
	if _, ok := broker.published("homeassistant/update/" + dev + "/update/config"); ok {
		t.Error("update entity should not be published without an updater")
	}
}

// fakeUpdater implements mqttbridge.Updater for tests.
type fakeUpdater struct {
	installed, latest string
	called            chan struct{}
}

func (f *fakeUpdater) UpdateInfo(context.Context) (string, string, string, bool, error) {
	return f.installed, f.latest, "https://example.test/tag/" + f.latest, true, nil
}

func (f *fakeUpdater) UpdateToLatest(context.Context) error {
	select {
	case f.called <- struct{}{}:
	default:
	}
	return nil
}

func TestBridgePublishesEnrichedTrack(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	sp := newSimSpeaker(t)

	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/np", DiscoveryPrefix: "homeassistant"}
	b := New(sp, func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Enriched source: the live track the speaker's raw read never carries.
	b.SetNowPlaying(func(context.Context) (*speaker.NowPlaying, error) {
		return &speaker.NowPlaying{Source: "TUNEIN", Station: "Radio X", Track: "Song Y", Artist: "Artist Z", PlayStatus: "PLAY_STATE"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	waitFor(t, "track state", func() bool {
		v, ok := broker.published("soundtap/np/track")
		return ok && v == "Song Y"
	})
	if v, _ := broker.published("soundtap/np/artist"); v != "Artist Z" {
		t.Errorf("artist = %q, want Artist Z", v)
	}
	if v, _ := broker.published("soundtap/np/station"); v != "Radio X" {
		t.Errorf("station = %q, want Radio X", v)
	}
}

func TestBridgeUpdateEntity(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	sp := newSimSpeaker(t)

	up := &fakeUpdater{installed: "v1.0.0", latest: "v1.1.0", called: make(chan struct{}, 1)}
	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/up", DiscoveryPrefix: "homeassistant"}
	b := New(sp, func() Config { return cfg }, up, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	const dev = "000000000000"
	waitFor(t, "update discovery", func() bool {
		_, ok := broker.published("homeassistant/update/" + dev + "/update/config")
		return ok
	})

	// The state carries both versions so the hub can show the update as available.
	waitFor(t, "update state", func() bool {
		v, ok := broker.published("soundtap/up/update/state")
		return ok && strings.Contains(v, `"installed_version":"v1.0.0"`) && strings.Contains(v, `"latest_version":"v1.1.0"`)
	})

	// Installing from the hub calls through to the updater.
	broker.send(t, "soundtap/up/update/install", "install")
	select {
	case <-up.called:
	case <-time.After(3 * time.Second):
		t.Fatal("update install was not invoked")
	}
}

// clockUpdater refuses the update check (update.ErrClockNotSet) until the clock is set
// after unsetFor checks.
type clockUpdater struct {
	fakeUpdater
	mu       sync.Mutex
	unsetFor int
	checks   int
}

func (c *clockUpdater) UpdateInfo(ctx context.Context) (string, string, string, bool, error) {
	c.mu.Lock()
	c.checks++
	unset := c.checks <= c.unsetFor
	c.mu.Unlock()
	if unset {
		return "", "", "", true, update.ErrClockNotSet
	}
	return c.fakeUpdater.UpdateInfo(ctx)
}

func (c *clockUpdater) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checks
}

// Right after boot NTP may not have set the clock, and the update check is refused. The
// bridge asks again soon, not six hours later, and stops asking after clockRetries.
func TestBridgeUpdateCheckWaitsForTheClock(t *testing.T) {
	defer func(d time.Duration) { clockRetry = d }(clockRetry)
	clockRetry = 10 * time.Millisecond
	sp := newSimSpeaker(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	broker := newFakeBroker(t)
	host, port := broker.addr()
	up := &clockUpdater{fakeUpdater: fakeUpdater{installed: "v1.0.0", latest: "v1.1.0"}, unsetFor: 3}
	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/clk", DiscoveryPrefix: "homeassistant"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go New(sp, func() Config { return cfg }, up, log).Run(ctx)
	waitFor(t, "update state once the clock is set", func() bool {
		v, ok := broker.published("soundtap/clk/update/state")
		return ok && strings.Contains(v, `"latest_version":"v1.1.0"`)
	})
	if n := up.count(); n != 4 {
		t.Fatalf("%d update checks, want 3 refused and 1 that published", n)
	}

	// A clock that never gets set: the retries stop at their bound.
	broker = newFakeBroker(t)
	host, port = broker.addr()
	never := &clockUpdater{unsetFor: 1 << 30}
	cfg2 := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/never", DiscoveryPrefix: "homeassistant"}
	go New(sp, func() Config { return cfg2 }, never, log).Run(ctx)
	waitFor(t, "the retries to run out", func() bool { return never.count() == 1+clockRetries })
	time.Sleep(20 * clockRetry)
	if n := never.count(); n != 1+clockRetries {
		t.Fatalf("%d update checks with a clock that is never set, want %d", n, 1+clockRetries)
	}
	if _, ok := broker.published("soundtap/never/update/state"); ok {
		t.Fatal("published an update state without a check that succeeded")
	}
}

// Turning the bridge off leaves "offline" on the broker: a clean DISCONNECT discards the
// Will, so the retained "online" would otherwise stay. A reload reconnects at once.
func TestBridgeAvailabilityAndReload(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	var mu sync.Mutex
	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/test", DiscoveryPrefix: "homeassistant"}
	b := New(newSimSpeaker(t), func() Config { mu.Lock(); defer mu.Unlock(); return cfg }, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	connected := func() bool { ok, _ := b.Status(); return ok }
	waitFor(t, "connected", connected)

	b.Reload() // same settings: back well inside reconnectDelay
	time.Sleep(50 * time.Millisecond)
	waitFor(t, "reconnected after a reload", connected)

	mu.Lock()
	cfg.Enabled = false
	mu.Unlock()
	b.Reload()
	waitFor(t, "availability offline", func() bool {
		v, _ := broker.published("soundtap/test/availability")
		return v == "offline"
	})
}

// Empty fields are published on a new connection too, so a song the broker still
// retains from before is cleared.
func TestBridgePublishesEmptyStateOnConnect(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	sp := newSimSpeaker(t)

	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/np", DiscoveryPrefix: "homeassistant"}
	b := New(sp, func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.SetNowPlaying(func(context.Context) (*speaker.NowPlaying, error) {
		return &speaker.NowPlaying{Source: "STANDBY", PlayStatus: "STOP_STATE"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	waitFor(t, "empty track state", func() bool {
		v, ok := broker.published("soundtap/np/track")
		return ok && v == ""
	})
	if _, ok := broker.published("soundtap/np/artist"); !ok {
		t.Error("artist was never published; a retained old artist would stay")
	}
}

// A preset stored after connecting shows up in the hub's preset select without a reconnect.
// The bridge polls every few seconds, so this waits a little longer.
func TestBridgeRefreshesPresetOptions(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	sp := newSimSpeaker(t)

	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/p", DiscoveryPrefix: "homeassistant"}
	b := New(sp, func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	const topic = "homeassistant/select/000000000000/preset/config"
	waitFor(t, "preset select config", func() bool {
		_, ok := broker.published(topic)
		return ok
	})
	if err := sp.StorePreset(ctx, 1, "TUNEIN", "stationurl", "/v1/playback/station/s1", "New station", ""); err != nil {
		t.Fatalf("store preset: %v", err)
	}
	deadline := time.Now().Add(pollInterval + 4*time.Second)
	for time.Now().Before(deadline) {
		if v, _ := broker.published(topic); strings.Contains(v, "New station") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("preset select options never listed the new preset")
}

// untouchedSpeaker panics on any call: a rejected command must not reach the speaker.
type untouchedSpeaker struct{ Speaker }

// Only ON and OFF switch power; any other payload used to read as OFF and put a
// playing speaker in standby.
func TestPowerIgnoresAnythingButOnOff(t *testing.T) {
	b := &Bridge{sp: untouchedSpeaker{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	tp := topics{base: "soundtap/test"}
	for _, p := range []string{"garbage", "", "{\"state\":\"ON\"}", "of"} {
		b.handle(context.Background(), nil, newStateCache(), tp, "homeassistant", nil, tp.powerSet(), p)
	}
}

// blockingKeySpeaker holds every Key call until its context ends.
type blockingKeySpeaker struct {
	Speaker
	started, ended chan struct{}
}

func (s blockingKeySpeaker) Key(ctx context.Context, _ string) error {
	s.started <- struct{}{}
	<-ctx.Done()
	close(s.ended)
	return ctx.Err()
}

// A reload ends the old session's commands instead of leaving them running.
func TestReloadCancelsTheOldSessionsCommands(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	sp := blockingKeySpeaker{Speaker: newSimSpeaker(t), started: make(chan struct{}, 1), ended: make(chan struct{})}
	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/test", DiscoveryPrefix: "homeassistant"}
	b := New(sp, func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	waitFor(t, "availability online", func() bool {
		v, ok := broker.published("soundtap/test/availability")
		return ok && v == "online"
	})
	broker.send(t, "soundtap/test/transport/stop/set", "PRESS")
	select {
	case <-sp.started:
	case <-time.After(4 * time.Second):
		t.Fatal("stop command never reached the speaker")
	}
	b.Reload()
	select {
	case <-sp.ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the old session's command still runs after a reload")
	}
}

// storeOnFirstPresets stores a preset right after the bridge's first preset read: the
// moment between its announce and its snapshot that the old order lost.
type storeOnFirstPresets struct {
	*speakerstate.Speaker
	once sync.Once
}

func (s *storeOnFirstPresets) Presets(ctx context.Context) ([]speaker.Preset, error) {
	p, err := s.Speaker.Presets(ctx)
	s.once.Do(func() {
		_ = s.StorePreset(ctx, 1, "TUNEIN", "stationurl", "/v1/playback/station/s1", "Race station", "")
	})
	return p, err
}

func TestBridgeAnnouncesAPresetStoredWhileConnecting(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	sp := &storeOnFirstPresets{Speaker: newSimSpeaker(t)}

	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/r", DiscoveryPrefix: "homeassistant"}
	b := New(sp, func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	const topic = "homeassistant/select/000000000000/preset/config"
	deadline := time.Now().Add(pollInterval + 4*time.Second)
	for time.Now().Before(deadline) {
		if v, _ := broker.published(topic); strings.Contains(v, "Race station") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("a preset stored during the announce never reached the select")
}

// slowReads widens the gap between a power command's state read and its key press.
type slowReads struct{ *speakerstate.Speaker }

func (s slowReads) NowPlaying(ctx context.Context) (*speaker.NowPlaying, error) {
	time.Sleep(150 * time.Millisecond)
	return s.Speaker.NowPlaying(ctx)
}

func startBridge(t *testing.T, sp Speaker) *fakeBroker {
	t.Helper()
	broker := newFakeBroker(t)
	host, port := broker.addr()
	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/test", DiscoveryPrefix: "homeassistant"}
	b := New(sp, func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go b.Run(ctx)
	waitFor(t, "availability online", func() bool {
		v, ok := broker.published("soundtap/test/availability")
		return ok && v == "online"
	})
	return broker
}

// Two OFFs at once both read "on"; POWER toggles, so the second switched it back on.
func TestConcurrentPowerOffLeavesSpeakerOff(t *testing.T) {
	sim := newSimSpeaker(t)
	broker := startBridge(t, slowReads{sim})
	broker.send(t, "soundtap/test/power/set", "ON")
	waitFor(t, "speaker on", func() bool {
		np, err := sim.NowPlaying(context.Background())
		return err == nil && !isStandby(np.Source)
	})
	broker.send(t, "soundtap/test/power/set", "OFF")
	broker.send(t, "soundtap/test/power/set", "OFF")
	off := func() bool {
		np, err := sim.NowPlaying(context.Background())
		return err == nil && isStandby(np.Source)
	}
	waitFor(t, "speaker off", off)
	// The second OFF switching it back on would show within this second.
	time.Sleep(time.Second)
	np, err := sim.NowPlaying(context.Background())
	if err != nil || !isStandby(np.Source) {
		t.Fatalf("after two OFFs: source=%v err=%v", np, err)
	}
}

// A command's reply goes through the poll cache: a later return to the old value is
// published, not skipped as unchanged.
func TestCommandReplyKeepsPollCacheTrue(t *testing.T) {
	sim := newSimSpeaker(t)
	ctx := context.Background()
	if err := sim.SetVolume(ctx, 20); err != nil {
		t.Fatal(err)
	}
	broker := startBridge(t, sim)
	waitFor(t, "volume 20", func() bool {
		v, _ := broker.published("soundtap/test/volume/state")
		return v == "20"
	})
	broker.send(t, "soundtap/test/volume/set", "37")
	waitFor(t, "volume 37", func() bool {
		v, _ := broker.published("soundtap/test/volume/state")
		return v == "37"
	})
	if err := sim.SetVolume(ctx, 20); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2*pollInterval + time.Second)
	for time.Now().Before(deadline) {
		if v, _ := broker.published("soundtap/test/volume/state"); v == "20" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("volume back at 20 never published")
}

// keySpeaker records keys and has a fixed mute state.
type keySpeaker struct {
	Speaker
	muted bool
	keys  *[]string
}

func (s keySpeaker) Muted() bool { return s.muted }
func (s keySpeaker) Key(_ context.Context, key string) error {
	*s.keys = append(*s.keys, key)
	return nil
}

// The mute switch presses the MUTE toggle only when the state differs, and ignores
// anything but ON and OFF.
func TestMuteSwitchIsIdempotent(t *testing.T) {
	var keys []string
	b := &Bridge{sp: keySpeaker{muted: true, keys: &keys}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	tp := topics{base: "soundtap/test"}
	cache := newStateCache()
	cache.last[tp.muteState()] = powerOn // already sent: the cache skips the publish
	for _, p := range []string{"garbage", "", "on"} {
		b.handle(context.Background(), nil, cache, tp, "homeassistant", nil, tp.muteSet(), p)
	}
	if len(keys) != 0 {
		t.Fatalf("keys %v for a muted speaker asked ON", keys)
	}
}

// A burst of volume commands (a slider dragged on the hub) ends at the last value sent:
// one goroutine per message ran them in any order, and on one core newest first.
func TestBridgeCommandsKeepTheirOrder(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	sp := newSimSpeaker(t)
	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/order"}
	b := New(sp, func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	waitFor(t, "availability online", func() bool {
		v, ok := broker.published("soundtap/order/availability")
		return ok && v == "online"
	})
	for round := range 5 {
		for v := 11; v <= 18; v++ {
			broker.send(t, "soundtap/order/volume/set", strconv.Itoa(v+round))
		}
		waitFor(t, "volume settled", func() bool {
			v, ok := broker.published("soundtap/order/volume/state")
			return ok && v == strconv.Itoa(18+round)
		})
		time.Sleep(50 * time.Millisecond) // a stray older value would land now
		if v, _ := sp.Volume(context.Background()); v != 18+round {
			t.Fatalf("round %d: volume %d, want %d", round, v, 18+round)
		}
	}
}

// Without a base topic the bridge uses soundtap/<deviceID>, as the FAQ documents.
func TestBridgeDefaultBaseTopic(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	cfg := Config{Enabled: true, Host: host, Port: port}
	b := New(newSimSpeaker(t), func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	waitFor(t, "availability under soundtap/<deviceID>", func() bool {
		v, ok := broker.published("soundtap/000000000000/availability")
		return ok && v == "online"
	})
}

// matchFilter reports whether an MQTT topic filter matches topic (MQTT 3.1.1 §4.7).
func matchFilter(filter, topic string) bool {
	f, tp := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, level := range f {
		if level == "#" {
			return true
		}
		if i >= len(tp) || (level != "+" && level != tp[i]) {
			return false
		}
	}
	return len(f) == len(tp)
}

func matchesAny(filters []string, topic string) bool {
	for _, f := range filters {
		if matchFilter(f, topic) {
			return true
		}
	}
	return false
}

// The bridge subscribes to the command topics its discovery advertises and the hub's
// status, and to nothing it publishes itself: a broker would deliver every such state
// back to it and fill the command queue with its own echoes.
func TestBridgeSubscribesToCommandsNotItsOwnStates(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	up := &fakeUpdater{installed: "v1.0.0", latest: "v1.1.0", called: make(chan struct{}, 1)}
	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/test", DiscoveryPrefix: "homeassistant"}
	b := New(newSimSpeaker(t), func() Config { return cfg }, up, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	for _, topic := range []string{"soundtap/test/availability", "soundtap/test/volume/state", "soundtap/test/update/state", "soundtap/test/status"} {
		waitFor(t, topic, func() bool { _, ok := broker.published(topic); return ok })
	}

	subs := broker.subscriptions()
	if !matchesAny(subs, "homeassistant/status") {
		t.Errorf("subscriptions %q miss the hub's status topic", subs)
	}
	broker.mu.Lock()
	published := maps.Clone(broker.pub)
	broker.mu.Unlock()
	commands := 0
	for topic, payload := range published {
		if matchesAny(subs, topic) {
			t.Errorf("the bridge subscribes to its own publish %q (subscriptions %q)", topic, subs)
		}
		if !strings.HasSuffix(topic, "/config") {
			continue
		}
		var conf map[string]any
		if err := json.Unmarshal([]byte(payload), &conf); err != nil {
			t.Fatalf("discovery %s: %v", topic, err)
		}
		if cmd, ok := conf["command_topic"].(string); ok {
			commands++
			if !matchesAny(subs, cmd) {
				t.Errorf("command topic %q from %s is not subscribed (subscriptions %q)", cmd, topic, subs)
			}
		}
	}
	if commands == 0 {
		t.Fatal("no discovery config advertised a command topic")
	}
}

// volumeLog records every level the bridge sets.
type volumeLog struct {
	*speakerstate.Speaker
	mu     sync.Mutex
	levels []int
}

func (v *volumeLog) SetVolume(ctx context.Context, level int) error {
	v.mu.Lock()
	v.levels = append(v.levels, level)
	v.mu.Unlock()
	return v.Speaker.SetVolume(ctx, level)
}

// A retained command is one the broker replays at every subscribe, so at every reconnect
// and boot; the bridge ignores it and runs only commands sent live.
// A connection the broker drops is reported as down and made again after reconnectDelay,
// with the subscriptions, the announcement and the state sent again on the new one.
func TestBridgeReconnectsAfterALostConnection(t *testing.T) {
	defer func(d time.Duration) { reconnectDelay = d }(reconnectDelay)
	reconnectDelay = 200 * time.Millisecond
	broker := newFakeBroker(t)
	host, port := broker.addr()
	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/test", DiscoveryPrefix: "homeassistant"}
	b := New(newSimSpeaker(t), func() Config { return cfg }, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	online := func() bool {
		v, ok := broker.published("soundtap/test/availability")
		return ok && v == "online"
	}
	waitFor(t, "availability online", online)

	broker.drop()
	waitFor(t, "the bridge to report the lost connection", func() bool {
		ok, lastErr := b.Status()
		return !ok && lastErr != ""
	})
	waitFor(t, "a second connection", func() bool { return broker.connections() == 2 })
	waitFor(t, "availability online again", online)
	waitFor(t, "the command topics subscribed again", func() bool {
		return slices.Contains(broker.subscriptions(), "soundtap/test/volume/set")
	})
	waitFor(t, "discovery sent again", func() bool {
		_, ok := broker.published("homeassistant/number/000000000000/volume/config")
		return ok
	})
	if ok, lastErr := b.Status(); !ok || lastErr != "" {
		t.Fatalf("status after reconnecting = %v %q", ok, lastErr)
	}

	// Commands reach the speaker over the new connection.
	broker.send(t, "soundtap/test/volume/set", "23")
	waitFor(t, "volume over the new connection", func() bool {
		v, _ := broker.published("soundtap/test/volume/state")
		return v == "23"
	})
}

func TestBridgeIgnoresRetainedCommands(t *testing.T) {
	broker := newFakeBroker(t)
	host, port := broker.addr()
	sp := &volumeLog{Speaker: newSimSpeaker(t)}
	up := &fakeUpdater{installed: "v1.0.0", latest: "v1.1.0", called: make(chan struct{}, 1)}
	cfg := Config{Enabled: true, Host: host, Port: port, BaseTopic: "soundtap/test", DiscoveryPrefix: "homeassistant"}
	b := New(sp, func() Config { return cfg }, up, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	waitFor(t, "availability online", func() bool {
		v, ok := broker.published("soundtap/test/availability")
		return ok && v == "online"
	})

	broker.sendRetained(t, "soundtap/test/update/install", "install")
	broker.sendRetained(t, "soundtap/test/power/set", "ON")
	broker.sendRetained(t, "soundtap/test/volume/set", "80")
	// Commands run in the order they arrive, so once this live one has run, a retained
	// one that was let through would have run before it.
	broker.send(t, "soundtap/test/volume/set", "37")
	waitFor(t, "live volume applied", func() bool {
		v, err := sp.Volume(context.Background())
		return err == nil && v == 37
	})

	sp.mu.Lock()
	levels := append([]int(nil), sp.levels...)
	sp.mu.Unlock()
	if len(levels) != 1 || levels[0] != 37 {
		t.Errorf("volume set to %v, want only the live 37", levels)
	}
	select {
	case <-up.called:
		t.Error("a retained update/install started an update")
	default:
	}
	if np, err := sp.NowPlaying(context.Background()); err != nil || !isStandby(np.Source) {
		t.Errorf("a retained power/set ON woke the speaker: %v %v", np, err)
	}
}
