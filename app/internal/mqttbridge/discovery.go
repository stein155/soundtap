// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mqttbridge

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strconv"
	"time"

	"github.com/stein155/soundtap/app/internal/mqtt"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/update"
)

// Discovery config keys used by most entities.
const (
	keyState   = "state_topic"
	keyCommand = "command_topic"
	keyIcon    = "icon"
	powerOn    = "ON"
	powerOff   = "OFF"
)

// updateCheckTimeout bounds one update check.
const updateCheckTimeout = 15 * time.Second

// transportButton is one transport key exposed as a button: its topic key, the
// SoundTouch remote key sent to the speaker and the entity's name.
type transportButton struct{ key, speakerKey, name string }

// transportButtons are the transport keys exposed as buttons.
// Only keys the speaker acts on are listed. Next/Previous reach the playing source
// through speakerstate's OnTransport handler (librespot for Spotify, AVRCP for
// Bluetooth); on a source that cannot skip they do nothing, as on the remote.
func transportButtons() []transportButton {
	return []transportButton{
		{"play", "PLAY", "Play"},
		{"pause", "PAUSE", "Pause"},
		{"stop", "STOP", "Stop"},
		{"next", "NEXT_TRACK", "Next"},
		{"previous", "PREV_TRACK", "Previous"},
	}
}

// announce publishes the retained MQTT discovery configs so hubs create
// (or refreshes) the device and all its entities, then publishes the static
// diagnostic state (software version, model). Called on connect and whenever the hub
// signals a restart (birth message).
func (b *Bridge) announce(ctx context.Context, client *mqtt.Client, topics topicTree, disc string, info *speaker.Info) {
	ann := newAnnouncer(client, topics, disc, info)
	_, ann.mute = b.sp.(muter)
	ann.controls(b.presetOptions(ctx))
	ann.sensors()
	// An `update` entity: Hubs show it as an update with a
	// notification when a newer release is available, plus an Install button — the
	// native update experience, not just a button on the device.
	if b.updater != nil {
		ann.publish("update", "update", ann.entity("SoundTap", "update", map[string]any{
			keyState:          topics.updateState(),
			keyCommand:        topics.updateInstall(),
			"payload_install": "install",
			"device_class":    "firmware",
			keyIcon:           "mdi:update",
		}))
	}

	// Static diagnostics: publish once (retained) so they survive hub restarts.
	if info.Software != "" {
		_ = client.Publish(topics.swVersion(), []byte(info.Software), true)
	}

	if info.Type != "" {
		_ = client.Publish(topics.model(), []byte(info.Type), true)
	}
}

// announcer publishes one speaker's discovery configs with the shared device and
// origin blocks folded into each.
type announcer struct {
	client *mqtt.Client
	topics topicTree
	disc   string
	info   *speaker.Info
	dev    map[string]any
	origin map[string]any
	mute   bool // the speaker reports its mute state
}

func newAnnouncer(client *mqtt.Client, topics topicTree, disc string, info *speaker.Info) *announcer {
	dev := map[string]any{
		"identifiers":  []string{info.DeviceID},
		"name":         info.Name,
		"manufacturer": "Bose",
	}
	if info.Type != "" {
		dev["model"] = info.Type
	}

	if info.Software != "" {
		dev["sw_version"] = info.Software
	}

	return &announcer{
		client: client, topics: topics, disc: disc, info: info, dev: dev,
		origin: map[string]any{"name": "SoundTap"}, mute: false,
	}
}

// entity assembles one discovery payload with the shared device/availability/
// origin blocks folded in.
func (a *announcer) entity(name, key string, extra map[string]any) map[string]any {
	cfg := map[string]any{
		"name":               name,
		"unique_id":          a.info.DeviceID + "_" + key,
		"availability_topic": a.topics.availability(),
		"device":             a.dev,
		"origin":             a.origin,
	}
	maps.Copy(cfg, extra)

	return cfg
}

func (a *announcer) publish(component, key string, cfg map[string]any) {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return
	}

	topic := a.disc + "/" + component + "/" + a.info.DeviceID + "/" + key + "/config"
	_ = a.client.Publish(topic, payload, true)
}

// controls: volume, power, preset and the transport buttons.
func (a *announcer) controls(presets []string) {
	topics := a.topics
	a.publish("number", "volume", a.entity("Volume", "volume", map[string]any{
		keyCommand: topics.volumeSet(),
		keyState:   topics.volumeState(),
		"min":      0,
		"max":      maxVolume,
		"step":     1,
		keyIcon:    "mdi:volume-high",
	}))
	a.publish("switch", "power", a.entity("Power", "power", map[string]any{
		keyCommand:    topics.powerSet(),
		keyState:      topics.powerState(),
		"payload_on":  powerOn,
		"payload_off": powerOff,
		keyIcon:       "mdi:power",
	}))

	if a.mute {
		a.publish("switch", "mute", a.entity("Mute", "mute", map[string]any{
			keyCommand:    topics.muteSet(),
			keyState:      topics.muteState(),
			"payload_on":  powerOn,
			"payload_off": powerOff,
			keyIcon:       "mdi:volume-off",
		}))
	}

	a.publish("select", "preset", a.entity("Preset", "preset", map[string]any{
		keyCommand: topics.presetSet(),
		keyState:   topics.presetState(),
		"options":  presets,
		keyIcon:    "mdi:radio",
	}))

	for _, button := range transportButtons() {
		a.publish("button", button.key, a.entity(button.name, button.key, map[string]any{
			keyCommand: topics.transportSet(button.key),
		}))
	}
}

// sensors: what is playing, plus the software version and model as diagnostics.
func (a *announcer) sensors() {
	topics := a.topics
	for _, sensor := range []struct {
		name, key, topic, icon string
		diagnostic             bool
	}{
		{"Station", "station", topics.station(), "mdi:radio", false},
		{"Track", "track", topics.track(), "mdi:music-note", false},
		{"Artist", "artist", topics.artist(), "mdi:account-music", false},
		{"Status", "status", topics.status(), "mdi:play-pause", false},
		{"Software version", "sw_version", topics.swVersion(), "", true},
		{"Model", "model", topics.model(), "", true},
	} {
		extra := map[string]any{keyState: sensor.topic}
		if sensor.icon != "" {
			extra[keyIcon] = sensor.icon
		}

		if sensor.diagnostic {
			extra["entity_category"] = "diagnostic"
		}

		a.publish("sensor", sensor.key, a.entity(sensor.name, sensor.key, extra))
	}
}

// publishUpdateState publishes the `update` entity's JSON state (installed and
// latest version + release URL). MQTT Update reads installed_version/latest_version
// from this payload and raises the update notification when they differ. A transient
// version-check failure leaves the last retained state in place rather than clearing
// it. No-op when no updater is wired in. It reports whether the check was refused because
// NTP has not set the clock yet (update.ErrClockNotSet), so the caller asks again soon.
func (b *Bridge) publishUpdateState(ctx context.Context, client *mqtt.Client, topics topicTree) bool {
	if b.updater == nil {
		return false
	}

	checkCtx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()

	installed, latest, releaseURL, updatable, err := b.updater.UpdateInfo(checkCtx)
	if errors.Is(err, update.ErrClockNotSet) {
		b.log.Info("mqtt update check waits for the clock to be set")

		return true
	}

	if err != nil {
		b.log.Warn("mqtt update check", "err", err)

		return false
	}

	if !updatable || latest == "" {
		latest = installed // nothing to offer -> the hub shows "up to date"
	}

	payload := map[string]string{"installed_version": installed, "latest_version": latest}
	if releaseURL != "" && latest != installed {
		payload["release_url"] = releaseURL
	}

	data, err := json.Marshal(payload)
	if err == nil {
		_ = client.Publish(topics.updateState(), data, true)
	}

	return false
}

// presetOptions returns the names of the speaker's non-empty presets, for the
// select. Discovery requires a non-empty options list, so it falls back to a placeholder
// (ignored by the command handler) when the speaker has no presets yet.
func (b *Bridge) presetOptions(ctx context.Context) []string {
	readCtx, cancel := context.WithTimeout(ctx, speakerReadTimeout)
	defer cancel()

	presets, err := b.sp.Presets(readCtx)
	if err != nil {
		return []string{presetPlaceholder}
	}

	var names []string

	for _, p := range presets {
		if name := p.Name; name != "" {
			names = append(names, name)
		}
	}

	if len(names) == 0 {
		return []string{presetPlaceholder}
	}

	return names
}

// presetPlaceholder is the select option shown when there are no presets. Selecting
// it is a no-op.
const presetPlaceholder = "—"

// itoa is a tiny helper so callers reading volume don't import strconv.
func itoa(n int) string { return strconv.Itoa(n) }
