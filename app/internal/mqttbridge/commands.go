// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mqttbridge

import (
	"context"
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

func newStateCache() *stateCache { return &stateCache{last: map[string]string{}} }

// publish sends value retained on topic. Unless always is set, a value equal to the
// last one sent is skipped; a topic not sent on this connection yet always goes out,
// empty values too: the broker may still retain an old song or station.
func (s *stateCache) publish(client *mqtt.Client, topic, value string, always bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if last, ok := s.last[topic]; ok && last == value && !always {
		return
	}

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
func (b *Bridge) refresh(ctx context.Context, client *mqtt.Client, tp topics, cache *stateCache) {
	c, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	send := func(topic, value string) { cache.publish(client, topic, value, false) }

	if vol, err := b.sp.Volume(c); err == nil {
		send(tp.volumeState(), itoa(vol))
	}

	if m, ok := b.sp.(muter); ok {
		send(tp.muteState(), onOff(m.Muted()))
	}

	np, err := b.readNowPlaying(c)
	if err != nil {
		return
	}

	send(tp.powerState(), powerState(np.Source))
	send(tp.station(), np.Station)
	send(tp.track(), np.Track)
	send(tp.artist(), np.Artist)
	send(tp.status(), prettyStatus(np.PlayStatus))
	// The preset select reflects the current station, so it shows as "selected"
	// on the hub when the speaker is playing one of the preset stations.
	send(tp.presetState(), np.Station)
}

// readNowPlaying prefers the enriched now-playing (live track/artist) and falls
// back to the speaker's raw read when no enricher is wired in.
func (b *Bridge) readNowPlaying(ctx context.Context) (*speaker.NowPlaying, error) {
	if b.nowPlaying != nil {
		return b.nowPlaying(ctx)
	}

	return b.sp.NowPlaying(ctx)
}

// handle routes one inbound command message to the speaker. It runs on the session's
// command worker (see serve), in the order the broker sent the commands.
func (b *Bridge) handle(ctx context.Context, client *mqtt.Client, cache *stateCache, tp topics, disc string, info *speaker.Info, topic, payload string) {
	c, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	switch {
	case topic == tp.volumeSet():
		level, err := strconv.Atoi(strings.TrimSpace(payload))
		if err != nil {
			b.log.Warn("mqtt bad volume", "payload", payload)

			return
		}

		if err := b.sp.SetVolume(c, level); err != nil {
			b.log.Warn("mqtt set volume", "err", err)

			return
		}

		cache.publish(client, tp.volumeState(), itoa(clampVol(level)), true)

	case topic == tp.powerSet():
		// Only ON and OFF: anything else read as OFF put a playing speaker in standby.
		switch p := strings.ToUpper(strings.TrimSpace(payload)); p {
		case powerOn, powerOff:
			b.setPower(c, client, cache, tp, p == powerOn)
		default:
			b.log.Warn("mqtt bad power", "payload", payload)
		}

	case topic == tp.muteSet():
		b.setMute(c, client, cache, tp, strings.ToUpper(strings.TrimSpace(payload)))

	case topic == tp.presetSet():
		b.selectPreset(c, client, cache, tp, strings.TrimSpace(payload))

	case topic == tp.updateInstall():
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
			b.announce(ctx, client, tp, disc, info)
			cache.forget()
			b.refresh(ctx, client, tp, cache)
			b.publishUpdateState(ctx, client, tp)
		}

	default:
		// Transport buttons: <base>/transport/<key>/set.
		for _, tb := range transportButtons {
			if topic == tp.transportSet(tb.key) {
				err := b.sp.Key(c, tb.speakerKey)
				if err != nil {
					b.log.Warn("mqtt transport", "key", tb.speakerKey, "err", err)
				}

				return
			}
		}
	}
}

// setPower turns the speaker on (wake from standby) or off (into standby), only
// acting when the current state differs so the switch is idempotent. POWER toggles, so
// the read and the key press are one step: two OFFs that both read "on" would switch
// the speaker off and on again.
func (b *Bridge) setPower(ctx context.Context, client *mqtt.Client, cache *stateCache, tp topics, on bool) {
	b.power.Lock()
	defer b.power.Unlock()

	np, err := b.sp.NowPlaying(ctx)
	if err != nil {
		b.log.Warn("mqtt power: read state", "err", err)

		return
	}

	standby := isStandby(np.Source)
	switch {
	case on && standby:
		// The power key, not a bare wake: it resumes what played last.
		err := b.sp.Key(ctx, "POWER")
		if err != nil {
			b.log.Warn("mqtt power on", "err", err)

			return
		}
	case !on && !standby:
		err := b.sp.Key(ctx, "POWER")
		if err != nil {
			b.log.Warn("mqtt power off", "err", err)

			return
		}
	}

	cache.publish(client, tp.powerState(), boolOnOff(on), true)
}

// selectPreset plays the native preset whose name matches the chosen option.
func (b *Bridge) selectPreset(ctx context.Context, client *mqtt.Client, cache *stateCache, tp topics, name string) {
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

			cache.publish(client, tp.presetState(), name, true)

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

func boolOnOff(on bool) string {
	if on {
		return powerOn
	}

	return powerOff
}

func clampVol(v int) int {
	if v < 0 {
		return 0
	}

	if v > 100 {
		return 100
	}

	return v
}

// prettyStatus turns the firmware's play-state token (e.g. "PLAY_STATE") into a
// short lower-case word for the sensor.
func prettyStatus(s string) string {
	s = strings.TrimSpace(s)

	s = strings.TrimSuffix(strings.ToUpper(s), "_STATE")
	switch s {
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
		return strings.ToLower(s)
	}
}

// setMute presses the MUTE toggle only when the speaker is not already as asked, so the
// switch is idempotent like power. Anything but ON and OFF is ignored.
func (b *Bridge) setMute(ctx context.Context, client *mqtt.Client, cache *stateCache, tp topics, payload string) {
	m, ok := b.sp.(muter)
	if !ok || (payload != powerOn && payload != powerOff) {
		b.log.Warn("mqtt bad mute", "payload", payload)

		return
	}

	want := payload == powerOn
	if m.Muted() != want {
		err := b.sp.Key(ctx, "MUTE")
		if err != nil {
			b.log.Warn("mqtt mute", "err", err)

			return
		}
	}

	cache.publish(client, tp.muteState(), onOff(want), false)
}

func onOff(on bool) string {
	if on {
		return powerOn
	}

	return powerOff
}
