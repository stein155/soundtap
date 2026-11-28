// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/mdns"
)

// A speaker that says goodbye stays in the list, marked offline. Dropping it made a
// rebooting speaker vanish from the UI and reappear as a new entry, and the goodbye
// carries no TXT record, so taking its fields would blank the name and model too.
func TestDepartureKeepsWhatIsKnown(t *testing.T) {
	t.Parallel()

	var srv Server
	srv.SetDevice(Device{
		ID:           "SN1",
		Name:         "Keuken",
		Model:        "SoundTouch 20",
		IP:           "192.168.1.5",
		Port:         80,
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})
	// The goodbye carries the address mDNS cached for the instance, nothing from TXT.
	srv.SetDevice(Device{
		ID:           "SN1",
		IP:           "192.168.1.5",
		Departure:    true,
		Name:         "",
		Model:        "",
		Firmware:     "",
		Port:         0,
		Capabilities: nil,
	})

	list := srv.knownDevices(new(Device))
	if len(list) != 1 {
		t.Fatalf("devices = %d, want 1", len(list))
	}

	got := list[0]
	if !got.Departure {
		t.Fatal("departed speaker is still reported online")
	}

	if got.Name != "Keuken" || got.Model != "SoundTouch 20" || got.IP != "192.168.1.5" {
		t.Fatalf("goodbye blanked the entry: %+v", got)
	}
	// Its address may be someone else's by now: nothing is proxied there.
	if a := srv.peerAddress("SN1"); a != "" {
		t.Fatalf("departed peer still proxied to %s", a)
	}

	srv.SetDevice(Device{
		ID:           "SN1",
		Name:         "Keuken",
		IP:           "192.168.1.5",
		Port:         80,
		Model:        "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})

	if srv.peerAddress("SN1") == "" {
		t.Fatal("a fresh announcement did not bring the peer back")
	}
}

// A speaker unplugged mid-play never sends a goodbye, so the list has to forget on its
// own or it grows a ghost for every speaker that ever joined the network.
func TestStaleSpeakersAreSwept(t *testing.T) {
	t.Parallel()

	now := time.Now()

	var srv Server

	srv.devices.now = func() time.Time { return now }
	srv.SetDevice(Device{
		ID:           "SN1",
		Name:         "Keuken",
		Model:        "",
		Firmware:     "",
		IP:           "",
		Port:         0,
		Departure:    false,
		Capabilities: nil,
	})
	srv.SetDevice(Device{
		ID:           "SN2",
		Name:         "Zolder",
		Model:        "",
		Firmware:     "",
		IP:           "",
		Port:         0,
		Departure:    false,
		Capabilities: nil,
	})

	now = now.Add(deviceStale + time.Minute)

	srv.SetDevice(Device{
		ID:           "SN2",
		Name:         "Zolder",
		Model:        "",
		Firmware:     "",
		IP:           "",
		Port:         0,
		Departure:    false,
		Capabilities: nil,
	}) // still talking

	list := srv.knownDevices(new(Device))
	if len(list) != 1 || list[0].ID != "SN2" {
		t.Fatalf("after the sweep: %+v, want only SN2", list)
	}
}

// Without an identity there is nothing a group or a proxy route could refer to later,
// and two such speakers would collide on the empty key.
func TestSpeakerWithoutAnIdentityIsIgnored(t *testing.T) {
	t.Parallel()

	var srv Server
	srv.SetDevice(Device{
		ID:           "  ",
		Name:         "nameless",
		Model:        "",
		Firmware:     "",
		IP:           "",
		Port:         0,
		Departure:    false,
		Capabilities: nil,
	})

	if list := srv.knownDevices(new(Device)); len(list) != 0 {
		t.Fatalf("devices = %+v, want none", list)
	}
}

// This speaker is always in its own device list, with live state rather than whatever
// its own multicast announcement last carried.
func TestSelfWinsOverItsOwnAnnouncement(t *testing.T) {
	t.Parallel()

	var srv Server
	srv.SetDevice(Device{
		ID:           "SN1",
		Name:         "old name",
		IP:           "192.168.1.5",
		Model:        "",
		Firmware:     "",
		Port:         0,
		Departure:    false,
		Capabilities: nil,
	})

	list := srv.knownDevices(&Device{
		ID:           "SN1",
		Name:         "new name",
		IP:           "192.168.1.9",
		Port:         80,
		Model:        "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})
	if len(list) != 1 {
		t.Fatalf("devices = %d, want 1", len(list))
	}

	if list[0].Name != "new name" || list[0].IP != "192.168.1.9" {
		t.Fatalf("self reported from stale multicast: %+v", list[0])
	}
}

// The registry's sweep and the mDNS browse interval are set in different packages and
// have to agree: if the query backoff ever grows past the sweep, every healthy speaker in
// the house drops out of the list between one browse and the next and reappears when the
// answer arrives. mdns.MaxBrowseDelay is the other half of this; the comment there points
// back here.
func TestTheSweepOutlastsTheBrowseInterval(t *testing.T) {
	t.Parallel()

	if mdns.MaxBrowseDelay >= deviceStale {
		t.Fatalf("mDNS browses at most every %v but the registry forgets after %v; a "+
			"healthy speaker would vanish between queries", mdns.MaxBrowseDelay, deviceStale)
	}
	// Not merely smaller -- comfortably so, or a single lost multicast answer is enough.
	if mdns.MaxBrowseDelay*2 >= deviceStale {
		t.Fatalf("browse %v against sweep %v leaves no room for a lost answer",
			mdns.MaxBrowseDelay, deviceStale)
	}
}

// Loopback and link-local addresses are never peers, and a second host cannot take over
// an id that a live peer holds; once the holder is stale the id may move (DHCP).
//
//nolint:paralleltest // it swaps the package-level peer address check
func TestDeviceRegistryRefusesHijacks(t *testing.T) {
	defer func(f func(net.IP) bool) { peerIPAllowed = f }(peerIPAllowed)

	peerIPAllowed = privatePeerIP
	now := time.Now()

	var srv Server

	srv.devices.now = func() time.Time { return now }
	for _, addr := range []string{"127.0.0.1", "169.254.1.1", "0.0.0.0", "224.0.0.251"} {
		srv.SetDevice(Device{
			ID:           "evil-" + addr,
			IP:           addr,
			Port:         80,
			Name:         "",
			Model:        "",
			Firmware:     "",
			Departure:    false,
			Capabilities: nil,
		})

		if srv.peerAddress("evil-"+addr) != "" {
			t.Fatalf("%s accepted as a peer address", addr)
		}
	}

	srv.SetDevice(Device{
		ID:           "SN1",
		Name:         "Keuken",
		IP:           "192.168.1.5",
		Port:         80,
		Model:        "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})
	srv.SetDevice(Device{
		ID:           "SN1",
		Name:         "Keuken",
		IP:           "192.168.1.66",
		Port:         80,
		Model:        "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})

	if got := srv.peerAddress("SN1"); got != "http://192.168.1.5:80" {
		t.Fatalf("live peer taken over: %s", got)
	}

	now = now.Add(deviceStale + time.Minute)

	if got := srv.peerAddress("SN1"); got != "" {
		t.Fatalf("stale peer still proxied to: %s", got)
	}

	srv.SetDevice(Device{
		ID:           "SN1",
		Name:         "Keuken",
		IP:           "192.168.1.66",
		Port:         80,
		Model:        "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})

	if got := srv.peerAddress("SN1"); got != "http://192.168.1.66:80" {
		t.Fatalf("a stale id did not move to its new address: %s", got)
	}
}

// A rejected claimant's goodbye is not the holder's: it must neither mark the live
// speaker departed nor open the id to the claimant's next announcement. The
// holder's own goodbye still frees the id for a new DHCP lease.
//
//nolint:paralleltest // it swaps the package-level peer address check
func TestForeignGoodbyeDoesNotFreeALivePeersID(t *testing.T) {
	defer func(f func(net.IP) bool) { peerIPAllowed = f }(peerIPAllowed)

	peerIPAllowed = privatePeerIP

	var srv Server

	srv.peerGone = func(Device) bool { return false } // the holder still answers
	srv.SetDevice(Device{
		ID:           "SN1",
		Name:         "Keuken",
		Model:        "SoundTouch 20",
		IP:           "192.168.1.5",
		Port:         80,
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})
	srv.SetDevice(Device{
		ID:           "SN1",
		Model:        "SoundTouch 20",
		IP:           "192.168.1.66",
		Port:         80,
		Name:         "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})
	srv.SetDevice(Device{
		ID:           "SN1",
		IP:           "192.168.1.66",
		Departure:    true,
		Name:         "",
		Model:        "",
		Firmware:     "",
		Port:         0,
		Capabilities: nil,
	})
	srv.SetDevice(Device{
		ID:           "SN1",
		Model:        "SoundTouch 20",
		IP:           "192.168.1.66",
		Port:         80,
		Name:         "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})

	if got := srv.peerAddress("SN1"); got != "http://192.168.1.5:80" {
		t.Fatalf("a foreign goodbye handed the id over: %s", got)
	}

	if list := srv.knownDevices(new(Device)); len(list) != 1 || list[0].Departure {
		t.Fatalf("a foreign goodbye marked the live speaker offline: %+v", list)
	}

	srv.SetDevice(Device{
		ID:           "SN1",
		IP:           "192.168.1.5",
		Departure:    true,
		Name:         "",
		Model:        "",
		Firmware:     "",
		Port:         0,
		Capabilities: nil,
	})
	srv.SetDevice(Device{
		ID:           "SN1",
		Name:         "Keuken",
		Model:        "SoundTouch 20",
		IP:           "192.168.1.7",
		Port:         80,
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})

	if got := srv.peerAddress("SN1"); got != "http://192.168.1.7:80" {
		t.Fatalf("the holder's own goodbye did not free the id for its new lease: %s", got)
	}
}

// A proxied upload gets the room and the time a local one gets.
func TestProxiedRoutesKeepTheirLimits(t *testing.T) {
	t.Parallel()

	if got := routePath("/api/devices/SN1/api/firmware"); got != firmwareUploadPath {
		t.Fatalf("routePath = %q", got)
	}

	if got := routePath("/api/firmware"); got != firmwareUploadPath {
		t.Fatalf("routePath = %q", got)
	}

	if proxyTimeout(firmwareUploadPath) < 10*time.Minute || proxyTimeout("/api/wifi/scan") < 80*time.Second {
		t.Fatal("long-running routes get the short proxy timeout")
	}

	if proxyTimeout("/api/now") != peerTimeout {
		t.Fatal("an ordinary route lost its short timeout")
	}
	// The peer gives playing a station 12 s; the proxy must not give up first.
	if proxyTimeout("/api/play/1") <= 12*time.Second || proxyTimeout("/api/bluetooth/clear") <= 30*time.Second {
		t.Fatal("the proxy gives up before the peer's own deadline")
	}
	// A picked release waits for a press on the peer.
	if proxyTimeout("/api/update") <= ConfirmWindow {
		t.Fatal("a proxied picked-release update times out before the press window")
	}
}

func TestRedactSecretsCatchesJSONAndUnderscoredKeys(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		`{"password":"hunter2"}`, `sae_password=hunter2`, `wpa_passphrase=hunter2`,
		// A password with an @ in it goes whole, not only up to that @.
		"play url=http://radio:pa@hunter2@stream.example/live",
		// A plural or a suffix after the key, and a stream token in a query string.
		"passwords=hunter2", "tokens: hunter2", "PASSWORD_FILE=hunter2",
		"play url=https://s.example/live?key=hunter2&type=aac", "url=https://s.example/a?b=1&sig=hunter2",
	} {
		if got := redactSecrets(line); strings.Contains(got, "hunter2") {
			t.Errorf("redactSecrets(%q) = %q", line, got)
		}
	}
}

func TestInviteRefusesLoopback(t *testing.T) { //nolint:paralleltest // it swaps the package-level peer address check
	defer func(f func(net.IP) bool) { peerIPAllowed = f }(peerIPAllowed)

	peerIPAllowed = privatePeerIP

	// IPv6 loopback, also with a zone that net.ParseIP would not read as an address, and
	// the IPv4-mapped spelling of 127.0.0.1.
	for _, target := range []string{
		"127.0.0.1", "127.0.0.1:80", "localhost:80", "::1", "[::1]:80", "[::1%lo]:80", "::ffff:127.0.0.1",
		"fe80::1%wlan0", "2001:db8::5", // link-local and public, like 169.254/16 and a public IPv4
	} {
		if inviteTargetAllowed(target) {
			t.Errorf("invite to %s allowed", target)
		}
	}

	for _, target := range []string{"192.168.1.5", "fd00::5", "[fd00::5]:80"} {
		if !inviteTargetAllowed(target) {
			t.Errorf("invite to the LAN speaker %s refused", target)
		}
	}
}

// A speaker that got a new DHCP lease takes its id along once its old address is gone,
// and only then.
func TestRenumberedSpeakerTakesOverWhenOldAddressIsGone(t *testing.T) {
	t.Parallel()

	for _, gone := range []bool{false, true} {
		var srv Server

		srv.peerGone = func(Device) bool { return gone }
		srv.SetDevice(Device{
			ID:           "SN1",
			Model:        "SoundTouch 10",
			IP:           "192.168.1.5",
			Name:         "",
			Firmware:     "",
			Port:         0,
			Departure:    false,
			Capabilities: nil,
		})
		srv.SetDevice(Device{
			ID:           "SN1",
			Model:        "SoundTouch 10",
			IP:           "192.168.1.9",
			Name:         "",
			Firmware:     "",
			Port:         0,
			Departure:    false,
			Capabilities: nil,
		})

		want := "192.168.1.5"
		if gone {
			want = "192.168.1.9"
		}

		deadline := time.Now().Add(2 * time.Second)

		for {
			srv.devices.mu.Lock()
			addr, probing := srv.devices.by["SN1"].device.IP, srv.devices.by["SN1"].probing
			srv.devices.mu.Unlock()

			if !probing && addr == want {
				break
			}

			if time.Now().After(deadline) {
				t.Fatalf("gone=%v: ip %s probing %v, want %s", gone, addr, probing, want)
			}

			time.Sleep(10 * time.Millisecond)
		}
	}
}

// An unplugged speaker sends no goodbye and the browser keeps re-reporting it. It must
// show offline anyway, stay offline through those reports, and come back when it answers.
func TestPeerThatStopsAnsweringGoesOffline(t *testing.T) {
	t.Parallel()

	gone := true

	var srv Server

	srv.peerGone = func(Device) bool { return gone }
	peer := Device{
		ID:           "kitchen",
		IP:           "192.168.1.9",
		Port:         80,
		Name:         "",
		Model:        "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	}
	offline := func() bool { return srv.knownDevices(new(Device))[0].Departure }

	srv.SetDevice(peer)

	for range peerMissLimit - 1 {
		srv.probePeers()
	}

	if offline() {
		t.Fatal("offline before the miss limit: one hiccup must not hide a room")
	}

	srv.probePeers()

	if !offline() {
		t.Fatal("still online after the miss limit")
	}

	srv.SetDevice(peer)

	if !offline() {
		t.Fatal("a re-report brought back a speaker that does not answer")
	}

	gone = false

	srv.probePeers()

	if offline() {
		t.Fatal("still offline after it answered")
	}
}

// A peer past peerMissLimit failed probes is offline in the device list, so the
// household stream stops following it too.
func TestHouseholdSkipsPeerPastMissLimit(t *testing.T) {
	t.Parallel()

	var srv Server
	srv.SetDevice(Device{
		ID:           "kitchen",
		IP:           "192.168.1.9",
		Port:         80,
		Name:         "",
		Model:        "",
		Firmware:     "",
		Departure:    false,
		Capabilities: nil,
	})

	if _, ok := srv.peerAddresses(8)["kitchen"]; !ok {
		t.Fatal("online peer not followed")
	}

	srv.devices.mu.Lock()
	srv.devices.by["kitchen"].misses = peerMissLimit
	srv.devices.mu.Unlock()

	if _, ok := srv.peerAddresses(8)["kitchen"]; ok {
		t.Fatal("peer past the miss limit still followed")
	}
}
