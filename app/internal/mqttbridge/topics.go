// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mqttbridge

// topicTree builds every MQTT topic the bridge uses from a single base root, so the
// discovery payloads and the command router agree on the layout.
//
//	<base>/availability
//	<base>/volume/state   <base>/volume/set
//	<base>/power/state    <base>/power/set
//	<base>/mute/state     <base>/mute/set
//	<base>/preset/state   <base>/preset/set
//	<base>/transport/<key>/set          (button: play|pause|stop|next|prev)
//	<base>/update/state  <base>/update/install   (update entity)
//	<base>/station  <base>/track  <base>/artist  <base>/status
//	<base>/sw_version  <base>/model
type topicTree struct{ base string }

// commands lists every topic the command router (Bridge.handle) acts on under base, and
// nothing else: it is what the bridge subscribes to, so the states it publishes never
// come back to it.
func (t topicTree) commands() []string {
	fixed := []string{t.volumeSet(), t.powerSet(), t.muteSet(), t.presetSet(), t.updateInstall()}
	buttons := transportButtons()

	cmds := make([]string, 0, len(fixed)+len(buttons))

	cmds = append(cmds, fixed...)
	for _, button := range buttons {
		cmds = append(cmds, t.transportSet(button.key))
	}

	return cmds
}

func (t topicTree) availability() string { return t.base + "/availability" }

func (t topicTree) volumeState() string { return t.base + "/volume/state" }
func (t topicTree) volumeSet() string   { return t.base + "/volume/set" }

func (t topicTree) powerState() string { return t.base + "/power/state" }
func (t topicTree) powerSet() string   { return t.base + "/power/set" }

func (t topicTree) muteState() string { return t.base + "/mute/state" }
func (t topicTree) muteSet() string   { return t.base + "/mute/set" }

func (t topicTree) presetState() string { return t.base + "/preset/state" }
func (t topicTree) presetSet() string   { return t.base + "/preset/set" }

func (t topicTree) transportSet(key string) string { return t.base + "/transport/" + key + "/set" }

func (t topicTree) updateState() string   { return t.base + "/update/state" }
func (t topicTree) updateInstall() string { return t.base + "/update/install" }

func (t topicTree) station() string   { return t.base + "/station" }
func (t topicTree) track() string     { return t.base + "/track" }
func (t topicTree) artist() string    { return t.base + "/artist" }
func (t topicTree) status() string    { return t.base + "/status" }
func (t topicTree) swVersion() string { return t.base + "/sw_version" }
func (t topicTree) model() string     { return t.base + "/model" }
