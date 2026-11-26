// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mqttbridge

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// Installs and the U-Boot environment are written by internal/web/firmware, through
// internal/flash and libubootenv, behind a key held on the speaker; an unsigned package
// installs on that press alone (ADR 0048). The bridge reaches none of them, so no MQTT
// command, now or added later without changing this, can install or write the
// environment: the broker is a third party.
func TestNoMQTTCommandReachesTheInstall(t *testing.T) {
	t.Parallel()

	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("needs the go command")
	}

	out, err := exec.CommandContext(t.Context(), gobin, "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}

	const mod = "github.com/stein155/soundtap/app/internal/"

	deps := strings.Fields(string(out))
	for _, pkg := range []string{"flash", "ubootenv", "web", "web/firmware", "install"} {
		if slices.Contains(deps, mod+pkg) {
			t.Errorf("the MQTT bridge depends on internal/%s, which writes the U-Boot environment", pkg)
		}
	}
	// What the bridge acts on: volume, power, mute, a preset, the app update and the
	// transport buttons. Nothing else is subscribed to.
	buttons := transportButtons()

	want := make([]string, 0, 5+len(buttons))

	want = append(want, "b/volume/set", "b/power/set", "b/mute/set", "b/preset/set", "b/update/install")
	for _, button := range buttons {
		want = append(want, "b/transport/"+button.key+"/set")
	}

	if got := (topicTree{base: "b"}).commands(); !slices.Equal(got, want) {
		t.Errorf("command topics %q, want %q", got, want)
	}
}
