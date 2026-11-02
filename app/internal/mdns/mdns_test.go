// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mdns

import (
	"net"
	"strings"
	"testing"
	"unicode/utf8"
)

func lan(ip net.IP) bool {
	return ip.Equal(net.IPv4(192, 168, 2, 27)) || ip.Equal(net.IPv4(192, 168, 2, 7))
}

func res(host, ip string, txt ...string) resolved {
	items := make([][]byte, len(txt))
	for i, s := range txt {
		items[i] = []byte(s)
	}
	return resolved{host: host, ip: net.ParseIP(ip).To4(), port: 80, txt: parseTXT(items)}
}

// A speaker on two interfaces, then its departure.
func TestTableFollowsAvahi(t *testing.T) {
	tab := &table{self: "me", known: map[string]map[string]Peer{}}
	got := tab.resolved("wlan0", "Keuken #2", res("keuken-2.local", "192.168.2.27",
		"id=abc", "name=Keuken", "model=ST20", "fw=0.1.0", "api=1"), lan)
	if len(got) != 1 {
		t.Fatalf("resolved instance gave %v", got)
	}
	p := got[0]
	if p.Instance != "Keuken #2" || p.Host != "keuken-2.local" || p.IP.String() != "192.168.2.27" ||
		p.Port != 80 || p.ID != "abc" || p.Name != "Keuken" || p.Model != "ST20" || p.Firmware != "0.1.0" || p.API != 1 {
		t.Fatalf("peer = %+v", p)
	}
	tab.resolved("usb0", "Keuken #2", res("keuken-2.local", "192.168.2.27", "id=abc"), lan)
	if got := tab.removed("usb0", "Keuken #2"); got != nil {
		t.Fatalf("gone from one of two interfaces reported %v", got)
	}
	if all := tab.all(); len(all) != 1 {
		t.Fatalf("all = %v", all)
	}
	got = tab.removed("wlan0", "Keuken #2")
	if len(got) != 1 || !got[0].Departure || got[0].ID != "abc" {
		t.Fatalf("departure = %v", got)
	}
	if len(tab.all()) != 0 {
		t.Fatal("departed peer still known")
	}
}

func TestTableRefusesWhatItMustNotProxyTo(t *testing.T) {
	tab := &table{self: "me", known: map[string]map[string]Peer{}}
	for _, c := range []struct {
		inst string
		r    resolved
	}{
		{"Self", res("self.local", "192.168.2.27", "id=me")},       // this speaker
		{"Router", res("r.local", "192.168.2.1", "id=x")},          // not on-link per lan()
		{"Loop", res("l.local", "127.0.0.1", "id=y")},              // loopback
		{"NoID", res("n.local", "192.168.2.7", "model=ST10")},      // no id
		{"NoAddress", res("s.local", "fe80::1", "id=z")},           // no IPv4 address
		{"Garbage", res("g.local", "192.168.2.7", "id", "=x", "")}, // no id=… among the strings
	} {
		if got := tab.resolved("wlan0", c.inst, c.r, lan); got != nil {
			t.Errorf("%s gave %v", c.inst, got)
		}
	}
	for i := range maxPeers + 5 {
		tab.resolved("wlan0", "Flood"+string(rune('A'+i%26))+string(rune('a'+i/26)), res("f.local", "192.168.2.7", "id=f"), lan)
	}
	if len(tab.known) != maxPeers {
		t.Fatalf("table grew to %d, cap %d", len(tab.known), maxPeers)
	}
}

// Recent says why a peer is or is not listed, for a speaker with no shell.
func TestTableSaysWhatEachEventDid(t *testing.T) {
	tab := &table{self: "me", known: map[string]map[string]Peer{}}
	yes := func(net.IP) bool { return true }
	off := func(net.IP) bool { return false }
	for _, c := range []struct {
		inst   string
		r      resolved
		usable func(net.IP) bool
		want   string
	}{
		{"Keuken", res("keuken.local", "192.168.2.27", "id=abc"), yes, "peer"},
		{"Zolder", res("zolder.local", "192.168.7.2", "id=def"), off, "ignored: address not on a local network"},
		{"Self", res("self.local", "192.168.2.9", "id=me"), yes, "ignored: this speaker"},
		{"Anon", res("anon.local", "192.168.2.8", "name=x"), yes, "ignored: no id in TXT"},
	} {
		tab.resolved("wlan0", c.inst, c.r, c.usable)
		if tab.why != c.want {
			t.Errorf("%s: why = %q, want %q", c.inst, tab.why, c.want)
		}
	}
	for _, c := range []struct{ iface, inst, want string }{
		{"wlan0", "Keuken", "departed"},
		{"wlan0", "Keuken", "gone, was not known"},
	} {
		tab.removed(c.iface, c.inst)
		if tab.why != c.want {
			t.Errorf("remove %s: why = %q, want %q", c.inst, tab.why, c.want)
		}
	}
}

func TestTXTRecordAndParse(t *testing.T) {
	txt := txtRecord("Keuken", Service{ID: "abc", Model: "ST10", Firmware: "0.1.0", API: 1, Port: 80})
	var got []string
	for _, item := range txt {
		got = append(got, string(item))
	}
	// The order avahi-publish -s passes its arguments in, so the record on the wire is the same.
	if want := "id=abc name=Keuken model=ST10 fw=0.1.0 api=1"; strings.Join(got, " ") != want {
		t.Fatalf("TXT = %q, want %q", got, want)
	}
	parsed := parseTXT(append(txt, []byte("noequals"), []byte("k=v=w")))
	if parsed["id"] != "abc" || parsed["api"] != "1" || parsed["k"] != "v=w" || len(parsed) != 6 {
		t.Fatalf("parsed = %v", parsed)
	}
}

func TestSlug(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Keuken", "keuken"}, {"  Living Room_2 ", "living-room-2"}, {"Zölder!", "zlder"}, {"", "soundtap"},
	} {
		if got := slug(c.in); got != c.want {
			t.Errorf("slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Only soundtap.local is for people: the own host name comes from the device id (ADR 0043).
func TestHostLabelComesFromTheDeviceID(t *testing.T) {
	if got := hostLabel("Keuken", &Service{ID: "SERIAL0000000020A"}); got != "soundtap-serial0000000020a" {
		t.Fatalf("hostLabel = %q", got)
	}
	if got := hostLabel("Keuken", nil); got != "keuken" {
		t.Fatalf("hostLabel without a service = %q", got)
	}
}

func TestInstanceCutsWholeRunes(t *testing.T) {
	got := instance("Zolder " + strings.Repeat("\U0001F50A", 20)) // 87 bytes
	if len(got) > 63 || !utf8.ValidString(got) {
		t.Fatalf("instance = %q (%d bytes), want valid UTF-8 within one 63-byte label", got, len(got))
	}
}
