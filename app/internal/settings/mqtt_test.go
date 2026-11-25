// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"strings"
	"testing"
)

func TestMQTTValidate(t *testing.T) {
	ok := map[string]MQTT{
		"zero value":         {},
		"default port":       {Enabled: true, Host: "broker.lan"},
		"lowest port":        {Enabled: true, Host: "broker.lan", Port: 1},
		"highest port":       {Enabled: true, Host: "10.0.0.2", Port: 65535},
		"ipv6 host":          {Enabled: true, Host: "fd00::2"},
		"off without host":   {Port: 1883},
		"topics":             {Enabled: true, Host: "broker.lan", BaseTopic: "home/speakers/kitchen", DiscoveryPrefix: "homeassistant"},
		"longest host":       {Enabled: true, Host: strings.Repeat("a", 253)},
		"longest base topic": {Enabled: true, Host: "b", BaseTopic: strings.Repeat("t", 128)},
	}
	for name, m := range ok {
		err := m.Validate()
		if err != nil {
			t.Errorf("%s: %v, want valid", name, err)
		}
	}

	bad := map[string]MQTT{
		"negative port":          {Port: -1},
		"port too high":          {Enabled: true, Host: "broker.lan", Port: 65536},
		"on without host":        {Enabled: true},
		"space in host":          {Enabled: true, Host: "broker lan"},
		"newline in host":        {Enabled: true, Host: "broker.lan\n"},
		"control in host":        {Host: "broker\x00.lan"},
		"host too long":          {Enabled: true, Host: strings.Repeat("a", 254)},
		"host not utf-8":         {Enabled: true, Host: "broker\xff"},
		"hash in base topic":     {BaseTopic: "soundtap/#"},
		"plus in base topic":     {BaseTopic: "soundtap/+/x"},
		"leading slash":          {BaseTopic: "/soundtap"},
		"trailing slash":         {BaseTopic: "soundtap/"},
		"control in base topic":  {BaseTopic: "sound\ttap"},
		"base topic too long":    {BaseTopic: strings.Repeat("t", 129)},
		"wildcard in prefix":     {DiscoveryPrefix: "home+assistant"},
		"trailing slash prefix":  {DiscoveryPrefix: "homeassistant/"},
		"control in prefix":      {DiscoveryPrefix: "home\x7fassistant"},
		"prefix not utf-8":       {DiscoveryPrefix: "home\xc3"},
		"prefix too long":        {DiscoveryPrefix: strings.Repeat("p", 129)},
		"single slash as prefix": {DiscoveryPrefix: "/"},
	}
	for name, m := range bad {
		err := m.Validate()
		if err == nil {
			t.Errorf("%s: %+v accepted, want an error", name, m)
		}
	}
}
