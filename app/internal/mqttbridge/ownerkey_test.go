// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mqttbridge

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The owner key (ADR 0048) is written by internal/web/firmware, through internal/flash and
// libubootenv, behind a key held on the speaker. The bridge reaches none of them, so no
// MQTT command, now or added later without changing this, can set or clear the key: the
// broker is a third party, and the key decides which signed packages a speaker installs.
func TestNoMQTTCommandReachesTheOwnerKey(t *testing.T) {
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
	want := []string{"b/volume/set", "b/power/set", "b/mute/set", "b/preset/set", "b/update/install"}
	for _, tb := range transportButtons {
		want = append(want, "b/transport/"+tb.key+"/set")
	}
	if got := (topics{base: "b"}).commands(); !slices.Equal(got, want) {
		t.Errorf("command topics %q, want %q", got, want)
	}
}
