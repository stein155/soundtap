// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mqttbridge

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/mqtt"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// stateCache is a connection's memory of the last value published per state topic, so
// the poll re-sends a retained value only when it changed. Command handlers publish
// through it too: published around it, a command's reply left the cache stale and a later
// return to the cached value was never sent.
type stateCache struct {
	mu   sync.Mutex
	last map[string]string
}

const (
	// speakerReadTimeout bounds the speaker reads of one poll or one preset list.
	speakerReadTimeout = 6 * time.Second
	// commandTimeout bounds the speaker calls of one command.
	commandTimeout = 12 * time.Second
	// maxVolume is the top of the speaker's volume scale, 0..100.
	maxVolume = 100
)

func newStateCache() *stateCache { return &stateCache{mu: sync.Mutex{}, last: map[string]string{}} }

// publish sends value retained on topic, unless it equals the last one sent; a topic not
// sent on this connection yet always goes out, empty values too: the broker may still
// retain an old song or station.
func (s *stateCache) publish(client *mqtt.Client, topic, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if last, ok := s.last[topic]; ok && last == value {
		return
	}

	s.sendLocked(client, topic, value)
}

// publishAlways sends value retained on topic, also when it equals the last one sent.
func (s *stateCache) publishAlways(client *mqtt.Client, topic, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sendLocked(client, topic, value)
}

// sendLocked sends value retained on topic and remembers it once sent; s.mu is held.
func (s *stateCache) sendLocked(client *mqtt.Client, topic, value string) {
	err := client.Publish(topic, []byte(value), true)
	if err == nil {
		s.last[topic] = value
	}
}

// forget makes the next poll send every topic again.
func (s *stateCache) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.last)
}

// refresh reads the speaker's current state and publishes any values that changed
// since the last poll (retained, so clients see them on reconnect).
func (b *Bridge) refresh(ctx context.Context, client *mqtt.Client, topics topicTree, cache *stateCache) {
	readCtx, cancel := context.WithTimeout(ctx, speakerReadTimeout)
	defer cancel()

	send := func(topic, value string) { cache.publish(client, topic, value) }

	vol, err := b.sp.Volume(readCtx)
	if err == nil {
		send(topics.volumeState(), itoa(vol))
	}

	if mute, ok := b.sp.(muter); ok {
		send(topics.muteState(), muteState(mute))
	}

	playing, err := b.readNowPlaying(readCtx)
	if err != nil {
		return
	}

	send(topics.powerState(), powerState(playing.Source))
	send(topics.station(), playing.Station)
	send(topics.track(), playing.Track)
	send(topics.artist(), playing.Artist)
	send(topics.status(), prettyStatus(playing.PlayStatus))
	// The preset select reflects the current station, so it shows as "selected"
	// on the hub when the speaker is playing one of the preset stations.
	send(topics.presetState(), playing.Station)
}

// readNowPlaying prefers the enriched now-playing (live track/artist) and falls
// back to the speaker's raw read when no enricher is wired in.
func (b *Bridge) readNowPlaying(ctx context.Context) (*speaker.NowPlaying, error) {
	if b.nowPlaying != nil {
		return b.nowPlaying(ctx)
	}

	playing, err := b.sp.NowPlaying(ctx)
	if err != nil {
		return nil, fmt.Errorf("read now playing: %w", err)
	}

	return playing, nil
}

// handle routes one inbound command message to the speaker. It runs on the session's
// command worker (see serve), in the order the broker sent the commands.
func (b *Bridge) handle(
	ctx context.Context, client *mqtt.Client, cache *stateCache, topics topicTree, disc string, info *speaker.Info,
	topic, payload string,
) {
	callCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	switch {
	case topic == topics.volumeSet():
		b.setVolume(callCtx, client, cache, topics, payload)

	case topic == topics.powerSet():
		// Only ON and OFF: anything else read as OFF put a playing speaker in standby.
		switch want := strings.ToUpper(strings.TrimSpace(payload)); want {
		case powerOn, powerOff:
			b.setPower(callCtx, client, cache, topics, want)
		default:
			b.log.Warn("mqtt bad power", "payload", payload)
		}

	case topic == topics.muteSet():
		b.setMute(callCtx, client, cache, topics, strings.ToUpper(strings.TrimSpace(payload)))

	case topic == topics.presetSet():
		b.selectPreset(callCtx, client, cache, topics, strings.TrimSpace(payload))

	case topic == topics.updateInstall():
		// The hub's update entity asks us to install (payload defaults to "install").
		if b.updater == nil {
			return
		}

		err := b.updater.UpdateToLatest(ctx)
		if err != nil {
			b.log.Warn("mqtt update install", "err", err)
		}

	case topic == disc+"/status":
		// The discovering hub restarted; re-publish discovery and refresh state so the
		// device reappears without waiting for the next reconnect.
		if strings.EqualFold(strings.TrimSpace(payload), availabilityOnline) {
			b.announce(ctx, client, topics, disc, info)
			cache.forget()
			b.refresh(ctx, client, topics, cache)
			b.publishUpdateState(ctx, client, topics)
		}

	default:
		b.pressTransport(callCtx, topics, topic)
	}
}

// setVolume sets the volume a volume/set payload asks for.
func (b *Bridge) setVolume(
	ctx context.Context, client *mqtt.Client, cache *stateCache, topics topicTree, payload string,
) {
	level, err := strconv.Atoi(strings.TrimSpace(payload))
	if err != nil {
		b.log.Warn("mqtt bad volume", "payload", payload)

		return
	}

	err = b.sp.SetVolume(ctx, level)
	if err != nil {
		b.log.Warn("mqtt set volume", "err", err)

		return
	}

	cache.publishAlways(client, topics.volumeState(), itoa(clampVol(level)))
}

// pressTransport presses the key of the transport button topic is the command topic of,
// if any: <base>/transport/<key>/set.
func (b *Bridge) pressTransport(ctx context.Context, topics topicTree, topic string) {
	for _, button := range transportButtons() {
		if topic == topics.transportSet(button.key) {
			err := b.sp.Key(ctx, button.speakerKey)
			if err != nil {
				b.log.Warn("mqtt transport", "key", button.speakerKey, "err", err)
			}

			return
		}
	}
}

// setPower turns the speaker on (wake from standby) or off (into standby) as want, ON or
// OFF, asks, only acting when the current state differs so the switch is idempotent. POWER
// toggles, so the read and the key press are one step: two OFFs that both read "on" would
// switch the speaker off and on again.
func (b *Bridge) setPower(ctx context.Context, client *mqtt.Client, cache *stateCache, topics topicTree, want string) {
	b.power.Lock()
	defer b.power.Unlock()

	playing, err := b.sp.NowPlaying(ctx)
	if err != nil {
		b.log.Warn("mqtt power: read state", "err", err)

		return
	}

	wantOn := want == powerOn
	standby := isStandby(playing.Source)

	switch {
	case wantOn && standby:
		// The power key, not a bare wake: it resumes what played last.
		err := b.sp.Key(ctx, "POWER")
		if err != nil {
			b.log.Warn("mqtt power on", "err", err)

			return
		}
	case !wantOn && !standby:
		err := b.sp.Key(ctx, "POWER")
		if err != nil {
			b.log.Warn("mqtt power off", "err", err)

			return
		}
	default: // already as asked
	}

	cache.publishAlways(client, topics.powerState(), want)
}

// selectPreset plays the native preset whose name matches the chosen option.
func (b *Bridge) selectPreset(
	ctx context.Context, client *mqtt.Client, cache *stateCache, topics topicTree, name string,
) {
	if name == "" || name == presetPlaceholder {
		return
	}

	presets, err := b.sp.Presets(ctx)
	if err != nil {
		b.log.Warn("mqtt preset: read presets", "err", err)

		return
	}

	for _, p := range presets {
		if p.Name == name {
			err := b.sp.PlayPreset(ctx, p.Slot)
			if err != nil {
				b.log.Warn("mqtt preset play", "slot", p.Slot, "err", err)

				return
			}

			cache.publishAlways(client, topics.presetState(), name)

			return
		}
	}

	b.log.Warn("mqtt preset: no match", "name", name)
}

// isStandby reports whether the now-playing source means the speaker is off.
func isStandby(source string) bool {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "", "STANDBY", "INVALID_SOURCE":
		return true
	}

	return false
}

func powerState(source string) string {
	if isStandby(source) {
		return powerOff
	}

	return powerOn
}

func clampVol(level int) int {
	if level < 0 {
		return 0
	}

	if level > maxVolume {
		return maxVolume
	}

	return level
}

// prettyStatus turns the firmware's play-state token (e.g. "PLAY_STATE") into a
// short lower-case word for the sensor.
func prettyStatus(token string) string {
	token = strings.TrimSpace(token)

	token = strings.TrimSuffix(strings.ToUpper(token), "_STATE")
	switch token {
	case "PLAY":
		return "playing"
	case "PAUSE":
		return "paused"
	case "STOP":
		return "stopped"
	case "BUFFERING":
		return "buffering"
	case "":
		return ""
	default:
		return strings.ToLower(token)
	}
}

// setMute presses the MUTE toggle only when the speaker is not already as asked, so the
// switch is idempotent like power. Anything but ON and OFF is ignored.
func (b *Bridge) setMute(
	ctx context.Context, client *mqtt.Client, cache *stateCache, topics topicTree, payload string,
) {
	mute, ok := b.sp.(muter)
	if !ok || (payload != powerOn && payload != powerOff) {
		b.log.Warn("mqtt bad mute", "payload", payload)

		return
	}

	if muteState(mute) != payload {
		err := b.sp.Key(ctx, "MUTE")
		if err != nil {
			b.log.Warn("mqtt mute", "err", err)

			return
		}
	}

	cache.publish(client, topics.muteState(), payload)
}

// muteState is the mute switch's state payload: ON when the speaker is muted.
func muteState(mute muter) string {
	if mute.Muted() {
		return powerOn
	}

	return powerOff
}
