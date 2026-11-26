// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package mqttbridge couples a SoundTap speaker to MQTT clients.
//
// MQTT discovery defines no media_player, so the speaker is modelled as one MQTT
// device (grouped via the discovery `device` block) made of standard entities:
//
//	number  volume        set/read the volume 0..100
//	switch  power         turn the speaker on / put it in standby
//	select  preset        switch between the six native presets (stations)
//	button  play/pause/…  transport keys
//	sensor  station/…     current channel, track, artist, play status
//	sensor  sw_version    speaker software version + model
//	button  ota           trigger a SoundTap self-update (optional)
//
// The bridge publishes MQTT discovery so the device appears in discovering hubs
// automatically, mirrors the speaker's state on a poll, and routes command topics
// back to the speaker. It self-gates on the persisted MQTT settings and can be
// told to reconnect after a settings change via Reload.
package mqttbridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/mqtt"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// Updater backs the `update` entity. UpdateInfo reports the running
// version, the latest available version, a release URL, and whether updating is
// possible here (only on an installed speaker); UpdateToLatest performs the install.
// The web server implements this; kept as an interface so mqttbridge doesn't depend on
// the web package.
type Updater interface {
	UpdateInfo(ctx context.Context) (installed, latest, releaseURL string, updatable bool, err error)
	UpdateToLatest(ctx context.Context) error
}

// Config is the MQTT connection configuration, sourced from the settings store.
type Config struct {
	Enabled         bool
	Host            string
	Port            int
	Username        string
	Password        string
	BaseTopic       string // topic root; default "soundtap/<deviceID>"
	DiscoveryPrefix string // default "homeassistant"
	TLS             bool
}

// The broker port when the settings leave it 0. The web UI shows them as the port
// field's placeholder (DEFAULT_PORT, DEFAULT_TLS_PORT).
const (
	DefaultPort    = 1883
	DefaultTLSPort = 8883
)

func (c *Config) addr() string {
	port := c.Port
	if port == 0 {
		if c.TLS {
			port = DefaultTLSPort
		} else {
			port = DefaultPort
		}
	}

	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

// availabilityOnline / availabilityOffline are the availability payloads (also the
// discovery defaults).
const (
	availabilityOnline  = "online"
	availabilityOffline = "offline"
	pollInterval        = 5 * time.Second
	updateCheckInterval = 6 * time.Hour
	// clockRetries bounds the update checks run again, clockRetry apart, while the
	// clock is not set (update.ErrClockNotSet): half an hour, then the six-hourly check.
	clockRetries = 30
	// maxQueuedCommands caps the commands waiting for the worker, so a flood of
	// PUBLISHes from a misbehaving broker can't exhaust memory on the speaker.
	maxQueuedCommands = 32
	// infoTimeout bounds the read of the speaker's identity a session starts with.
	infoTimeout = 8 * time.Second
	// brokerKeepAlive is the MQTT keep-alive the bridge asks the broker for.
	brokerKeepAlive = 30 * time.Second
)

// errNoDeviceID ends a session that would have no device id to name its topics by.
var errNoDeviceID = errors.New("speaker has no device id yet")

// Speaker is the slice of the speaker the bridge drives (web.Backend has it all).
type Speaker interface {
	Info(ctx context.Context) (*speaker.Info, error)
	NowPlaying(ctx context.Context) (*speaker.NowPlaying, error)
	Presets(ctx context.Context) ([]speaker.Preset, error)
	Volume(ctx context.Context) (int, error)
	SetVolume(ctx context.Context, level int) error
	Key(ctx context.Context, key string) error
	PlayPreset(ctx context.Context, slot int) error
}

// muter is a Speaker that knows whether it is muted; only such a speaker gets a mute switch.
// MUTE is a toggle key, so the switch needs the state to press it only when it differs.
type muter interface {
	Muted() bool
}

// Bridge runs the MQTT integration for the life of the process.
type Bridge struct {
	sp    Speaker
	cfgFn func() Config
	// updater, when non-nil, backs an `update` entity: it reports the
	// installed/available versions (so hubs show an update notification) and performs
	// the install when the hub asks for it.
	updater Updater
	// nowPlaying, when set, returns the enriched now-playing (live track/artist
	// from the stream metadata). The speaker's raw /now_playing only carries the
	// station, so without this clients would never see the actual song. Falls back to
	// the raw speaker read when nil.
	nowPlaying func(context.Context) (*speaker.NowPlaying, error)
	log        *slog.Logger

	reload chan struct{}
	// power serialises power commands; see setPower.
	power sync.Mutex

	mu        sync.Mutex
	connected bool
	lastErr   string
}

// New builds a Bridge. cfgFn is read on every (re)connect so a settings change is
// picked up by Reload. updater is optional (nil disables the update entity).
func New(sp Speaker, cfgFn func() Config, updater Updater, log *slog.Logger) *Bridge {
	return &Bridge{
		sp:         sp,
		cfgFn:      cfgFn,
		updater:    updater,
		nowPlaying: nil,
		log:        log,
		reload:     make(chan struct{}, 1),
		power:      sync.Mutex{},
		mu:         sync.Mutex{},
		connected:  false,
		lastErr:    "",
	}
}

// SetNowPlaying supplies the enriched now-playing source (internal/nowplaying's
// EnrichedNowPlaying), so clients see the live track and artist, not just
// the station. Optional; without it the bridge uses the speaker's raw read.
func (b *Bridge) SetNowPlaying(fn func(context.Context) (*speaker.NowPlaying, error)) {
	b.nowPlaying = fn
}

// Reload signals the run loop to drop any current connection and reconnect using
// the latest settings. Non-blocking and safe to call from HTTP handlers.
func (b *Bridge) Reload() {
	select {
	case b.reload <- struct{}{}:
	default: // a reload is already pending
	}
}

// Status reports whether the broker connection is currently up, and the last error.
func (b *Bridge) Status() (bool, string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.connected, b.lastErr
}

// Run is the background service entry point (matches app.RegisterService). It
// blocks until ctx is cancelled.
func (b *Bridge) Run(ctx context.Context) {
	for {
		cfg := b.cfgFn()
		if !cfg.Enabled || cfg.Host == "" {
			b.markDisconnected(nil)

			if !b.wait(ctx, 0) { // wait for a reload or shutdown
				return
			}

			continue
		}

		err := b.serve(ctx, &cfg)
		if ctx.Err() != nil {
			return
		}

		if errors.Is(err, errReload) {
			continue // new settings: reconnect now, not after reconnectDelay
		}

		b.markDisconnected(err)

		if err != nil {
			b.log.Warn("mqtt connection ended", "err", err)
		}

		if !b.wait(ctx, reconnectDelay) {
			return
		}
	}
}

// markConnected records a connection that is up, which clears the last error.
func (b *Bridge) markConnected() {
	b.mu.Lock()
	b.connected = true
	b.lastErr = ""
	b.mu.Unlock()
}

// markDisconnected records a connection that is down; err, when set, becomes the last
// error, and without one the last error stays.
func (b *Bridge) markDisconnected(err error) {
	b.mu.Lock()

	b.connected = false
	if err != nil {
		b.lastErr = err.Error()
	}
	b.mu.Unlock()
}

// wait blocks until a reload arrives, delay elapses (delay<=0 waits indefinitely),
// or ctx is cancelled. It returns false only when ctx is cancelled.
func (b *Bridge) wait(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		case <-b.reload:
			return true
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-b.reload:
		return true
	case <-timer.C:
		return true
	}
}

// errReload ends serve for a settings change; Run reconnects at once.
var errReload = errors.New("mqtt settings reloaded")

// clockRetry is how soon an update check refused for an unset clock runs again; a
// variable for the tests.
//
//nolint:gochecknoglobals // a test seam: a test retries every 10 ms
var clockRetry = time.Minute

// reconnectDelay is how long the bridge waits after a lost or refused connection before
// it reads the settings again and reconnects; a variable for the tests.
//
//nolint:gochecknoglobals // a test seam: a test reconnects after 200 ms
var reconnectDelay = 15 * time.Second

// command is one inbound command message, queued for the session's command worker.
type command struct {
	client         *mqtt.Client
	topic, payload string
}

// session is what one broker connection works with.
type session struct {
	cfg    Config
	info   *speaker.Info
	topics topicTree
	disc   string // the discovery prefix
	cache  *stateCache
}

// serve holds one broker connection open, publishing state and routing commands,
// until the connection drops, a reload is requested, or ctx is cancelled.
func (b *Bridge) serve(ctx context.Context, cfg *Config) error {
	// Everything this session started ends with it: a reload or a lost broker must not
	// leave the old session's commands running against the speaker.
	ctx, endSession := context.WithCancel(ctx)
	defer endSession()

	info, err := b.identity(ctx)
	if err != nil {
		return err
	}

	sess := newSession(cfg, info)

	// One worker runs the commands in the order the broker sent them, so a slow speaker
	// call never stalls the MQTT read loop (and thus the birth message) and a slider's last
	// value is the one that sticks. A goroutine per message lost that: on one core Go runs
	// the newest goroutine first. The queue is bounded: a hostile or misbehaving broker
	// could otherwise stream PUBLISHes faster than the ~12 s speaker calls drain them and
	// OOM the speaker. Excess messages are dropped.
	queue := make(chan command, maxQueuedCommands)

	safego.Go("mqttbridge.commands", func() {
		for {
			select {
			case <-ctx.Done():
				return
			case cmd := <-queue:
				b.handle(ctx, cmd.client, sess.cache, sess.topics, sess.disc, sess.info, cmd.topic, cmd.payload)
			}
		}
	})

	client, err := b.connect(ctx, sess, queue)
	if err != nil {
		return err
	}
	// A clean DISCONNECT tells the broker to discard the Will, so the retained
	// "online" would outlive a disabled bridge, a changed base topic or a reboot.
	// Say "offline" first.
	defer func() {
		_ = client.Publish(sess.topics.availability(), []byte(availabilityOffline), true)
		client.Disconnect()
	}()

	// Connected means commands arrive, so only once the broker granted the subscription.
	// Only the command topics handle routes, not base/#: that also delivered every state
	// the bridge publishes back to it, filling the command queue with its own echoes.
	err = client.Subscribe(append(sess.topics.commands(), sess.disc+"/status")...)
	if err != nil {
		return err //nolint:wrapcheck // its text is lastError in GET /api/settings, unchanged
	}

	b.markConnected()
	b.log.Info("mqtt connected", "broker", cfg.addr(), "base", sess.topics.base)

	_ = client.Publish(sess.topics.availability(), []byte(availabilityOnline), true)

	return b.publishUntilEnd(ctx, client, sess)
}

// identity reads the speaker's identity, which names the session's topics.
func (b *Bridge) identity(ctx context.Context) (*speaker.Info, error) {
	infoCtx, cancel := context.WithTimeout(ctx, infoTimeout)
	info, err := b.sp.Info(infoCtx)

	cancel()

	if err != nil || info.DeviceID == "" {
		if err == nil {
			err = errNoDeviceID
		}

		return nil, fmt.Errorf("read speaker identity: %w", err)
	}

	return info, nil
}

// newSession resolves the topics a session publishes and listens on.
func newSession(cfg *Config, info *speaker.Info) *session {
	base := strings.TrimRight(cfg.BaseTopic, "/")
	if base == "" {
		base = "soundtap/" + info.DeviceID
	}

	disc := cfg.DiscoveryPrefix
	if disc == "" {
		disc = "homeassistant"
	}

	return &session{cfg: *cfg, info: info, topics: topicTree{base: base}, disc: disc, cache: newStateCache()}
}

// connect opens the session's broker connection, its Will announcing "offline", and
// queues every live command it delivers.
func (b *Bridge) connect(ctx context.Context, sess *session, queue chan<- command) (*mqtt.Client, error) {
	client, err := mqtt.Connect(ctx, mqtt.Options{
		Addr:        sess.cfg.addr(),
		ClientID:    "soundtap-" + sess.info.DeviceID,
		Username:    sess.cfg.Username,
		Password:    sess.cfg.Password,
		KeepAlive:   brokerKeepAlive,
		TLS:         sess.cfg.TLS,
		TLSConfig:   nil,
		DialTimeout: 0,
		Will:        &mqtt.Will{Topic: sess.topics.availability(), Payload: []byte(availabilityOffline), Retain: true},
		Handler: func(msgClient *mqtt.Client, msg mqtt.Message) {
			// A retained message is a stored command the broker replays at every
			// subscribe: a power/set or update/install a hub published retained would run
			// again at each reconnect and boot. A command counts only when sent live. The
			// hub's retained birth message is not needed either: serve announces on connect.
			if msg.Retain {
				b.log.Debug("mqtt ignoring retained message", "topic", msg.Topic)

				return
			}

			select {
			case queue <- command{msgClient, msg.Topic, string(msg.Payload)}:
			default:
				b.log.Warn("mqtt command queue full; dropping message", "topic", msg.Topic)
			}
		},
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // its text is lastError in GET /api/settings, unchanged
	}

	return client, nil
}

// publishUntilEnd announces the speaker and publishes its state on the poll until the
// connection drops, a reload is requested, or ctx is cancelled.
func (b *Bridge) publishUntilEnd(ctx context.Context, client *mqtt.Client, sess *session) error {
	// The preset select lists the presets by name; when they change (stored, renamed or
	// cleared on the speaker, the web UI or a client) its discovery config is sent again, or
	// the hub keeps offering the old list. The snapshot is taken before the announce:
	// taken after, a preset stored in between was in it but not in what the hub got.
	options := strings.Join(b.presetOptions(ctx), "\x00")
	b.announce(ctx, client, sess.topics, sess.disc, sess.info)

	// Publish state changes on a poll; cache keeps retained messages from being re-sent
	// when nothing changed.
	b.refresh(ctx, client, sess.topics, sess.cache)

	// The update check hits GitHub, so it runs far less often than the state poll. Right
	// after boot the clock may not be set yet and the check is refused; then it runs again
	// soon, a bounded number of times, rather than six hours later.
	var clockRetryC <-chan time.Time

	retries := 0
	checkUpdate := func() {
		clockRetryC = nil

		if b.publishUpdateState(ctx, client, sess.topics) && retries < clockRetries {
			retries++
			clockRetryC = time.After(clockRetry)
		}
	}
	checkUpdate()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	updateTicker := time.NewTicker(updateCheckInterval)
	defer updateTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-b.reload:
			return errReload
		case <-client.Done():
			return client.Err() //nolint:wrapcheck // its text is lastError in GET /api/settings, unchanged
		case <-ticker.C:
			if now := strings.Join(b.presetOptions(ctx), "\x00"); now != options {
				options = now

				b.announce(ctx, client, sess.topics, sess.disc, sess.info)
			}

			b.refresh(ctx, client, sess.topics, sess.cache)
		case <-updateTicker.C:
			checkUpdate()
		case <-clockRetryC:
			checkUpdate()
		}
	}
}
