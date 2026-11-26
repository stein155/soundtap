// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MQTT is the persisted configuration for the MQTT bridge (see
// internal/mqttbridge). Password is stored in the clear in the local JSON, like the
// other on-box state — the file never leaves the speaker.
type MQTT struct {
	Enabled         bool   `json:"enabled"`
	Host            string `json:"host"`
	Port            int    `json:"port"`            // 0 -> 1883 (8883 with TLS)
	Username        string `json:"username"`        // optional
	Password        string `json:"password"`        // optional
	BaseTopic       string `json:"baseTopic"`       // default "soundtap/<deviceID>"
	DiscoveryPrefix string `json:"discoveryPrefix"` // default "homeassistant"
	TLS             bool   `json:"tls"`
}

// Limits on the MQTT settings: a host is at most a full DNS name, and a topic root
// leaves room for the levels the bridge adds below it.
const (
	maxMQTTHost  = 253
	maxMQTTTopic = 128
	maxPort      = 65535
)

// Why MQTT settings are refused; the text is what the web UI shows.
var (
	errPortRange       = errors.New("is outside 1..65535")
	errHostRequired    = errors.New("host is required when MQTT is on")
	errHostTooLong     = errors.New("host must be at most")
	errHostNotUTF8     = errors.New("host is not valid UTF-8")
	errHostSpace       = errors.New("host contains whitespace or a control character")
	errTopicTooLong    = errors.New("must be at most")
	errTopicNotUTF8    = errors.New("is not valid UTF-8")
	errTopicWildcard   = errors.New("must not contain the wildcards # or +")
	errTopicControl    = errors.New("contains a control character")
	errTopicOuterSlash = errors.New("must not start or end with /")
)

// Validate refuses MQTT settings the bridge cannot use: a port outside 1..65535 (0 is
// the default), no host while enabled, a host with whitespace or control characters, and
// a base topic or discovery prefix that is not a plain topic root. Empty topics are the
// defaults.
//
//nolint:gocritic // hugeParam: a value receiver, as callers validate the copy a function returns
func (m MQTT) Validate() error {
	if m.Port < 0 || m.Port > maxPort {
		return fmt.Errorf("port %d %w", m.Port, errPortRange)
	}

	if m.Enabled && m.Host == "" {
		return errHostRequired
	}

	switch {
	case len(m.Host) > maxMQTTHost:
		return fmt.Errorf("%w %d bytes", errHostTooLong, maxMQTTHost)
	case !utf8.ValidString(m.Host):
		return errHostNotUTF8
	case strings.IndexFunc(m.Host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return errHostSpace
	default:
	}

	err := validTopicRoot(m.BaseTopic)
	if err != nil {
		return fmt.Errorf("base topic %w", err)
	}

	err = validTopicRoot(m.DiscoveryPrefix)
	if err != nil {
		return fmt.Errorf("discovery prefix %w", err)
	}

	return nil
}

// validTopicRoot checks a topic the bridge publishes and subscribes below: no
// wildcards, which would make every command filter match foreign topics; no control
// characters; and no '/' at either end, which leaves an empty level.
func validTopicRoot(topic string) error {
	switch {
	case topic == "":
		return nil
	case len(topic) > maxMQTTTopic:
		return fmt.Errorf("%w %d bytes", errTopicTooLong, maxMQTTTopic)
	case !utf8.ValidString(topic):
		return errTopicNotUTF8
	case strings.ContainsAny(topic, "#+"):
		return errTopicWildcard
	case strings.IndexFunc(topic, unicode.IsControl) >= 0:
		return errTopicControl
	case strings.HasPrefix(topic, "/") || strings.HasSuffix(topic, "/"):
		return errTopicOuterSlash
	default:
		return nil
	}
}
