// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings_test

import (
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/settings"
)

// mqttCfg is MQTT settings without credentials or TLS.
func mqttCfg(enabled bool, host string, port int, baseTopic, discoveryPrefix string) settings.MQTT {
	return settings.MQTT{
		Enabled: enabled, Host: host, Port: port, Username: "", Password: "",
		BaseTopic: baseTopic, DiscoveryPrefix: discoveryPrefix, TLS: false,
	}
}

func TestMQTTValidate(t *testing.T) {
	t.Parallel()

	valid := map[string]settings.MQTT{
		"zero value":         mqttCfg(false, "", 0, "", ""),
		"default port":       mqttCfg(true, "broker.lan", 0, "", ""),
		"lowest port":        mqttCfg(true, "broker.lan", 1, "", ""),
		"highest port":       mqttCfg(true, "10.0.0.2", 65535, "", ""),
		"ipv6 host":          mqttCfg(true, "fd00::2", 0, "", ""),
		"off without host":   mqttCfg(false, "", 1883, "", ""),
		"topics":             mqttCfg(true, "broker.lan", 0, "home/speakers/kitchen", "homeassistant"),
		"longest host":       mqttCfg(true, strings.Repeat("a", 253), 0, "", ""),
		"longest base topic": mqttCfg(true, "b", 0, strings.Repeat("t", 128), ""),
	}
	for name, m := range valid {
		err := m.Validate()
		if err != nil {
			t.Errorf("%s: %v, want valid", name, err)
		}
	}

	bad := map[string]settings.MQTT{
		"negative port":          mqttCfg(false, "", -1, "", ""),
		"port too high":          mqttCfg(true, "broker.lan", 65536, "", ""),
		"on without host":        mqttCfg(true, "", 0, "", ""),
		"space in host":          mqttCfg(true, "broker lan", 0, "", ""),
		"newline in host":        mqttCfg(true, "broker.lan\n", 0, "", ""),
		"control in host":        mqttCfg(false, "broker\x00.lan", 0, "", ""),
		"host too long":          mqttCfg(true, strings.Repeat("a", 254), 0, "", ""),
		"host not utf-8":         mqttCfg(true, "broker\xff", 0, "", ""),
		"hash in base topic":     mqttCfg(false, "", 0, "soundtap/#", ""),
		"plus in base topic":     mqttCfg(false, "", 0, "soundtap/+/x", ""),
		"leading slash":          mqttCfg(false, "", 0, "/soundtap", ""),
		"trailing slash":         mqttCfg(false, "", 0, "soundtap/", ""),
		"control in base topic":  mqttCfg(false, "", 0, "sound\ttap", ""),
		"base topic too long":    mqttCfg(false, "", 0, strings.Repeat("t", 129), ""),
		"wildcard in prefix":     mqttCfg(false, "", 0, "", "home+assistant"),
		"trailing slash prefix":  mqttCfg(false, "", 0, "", "homeassistant/"),
		"control in prefix":      mqttCfg(false, "", 0, "", "home\x7fassistant"),
		"prefix not utf-8":       mqttCfg(false, "", 0, "", "home\xc3"),
		"prefix too long":        mqttCfg(false, "", 0, "", strings.Repeat("p", 129)),
		"single slash as prefix": mqttCfg(false, "", 0, "", "/"),
	}
	for name, m := range bad {
		err := m.Validate()
		if err == nil {
			t.Errorf("%s: %+v accepted, want an error", name, m)
		}
	}
}
