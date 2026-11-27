package main

import (
	"net"
	"testing"

	"github.com/stein155/soundtap/app/internal/wifi"
)

// A network without a default route still gives the speaker its address, so the display
// and the lamp say connected instead of "Connecting" forever.
func TestLANIPv4WithoutDefaultRoute(t *testing.T) {
	t.Parallel()

	const live = net.FlagUp | net.FlagRunning

	loopback := ifaceIPs{name: "lo", flags: live | net.FlagLoopback, ips: []net.IP{net.ParseIP("127.0.0.1")}}
	setupAP := ifaceIPs{name: "wlan1", flags: live, ips: []net.IP{net.ParseIP(wifi.APAddress)}}
	gadget := ifaceIPs{name: "usb0", flags: live, ips: []net.IP{net.ParseIP("192.168.7.2")}}
	noCarrier := ifaceIPs{name: "eth0", flags: net.FlagUp, ips: []net.IP{net.ParseIP("10.0.0.5")}}
	linkLocal := ifaceIPs{name: "wlan0", flags: live, ips: []net.IP{net.ParseIP("169.254.10.2"), net.ParseIP("fe80::1")}}
	joined := ifaceIPs{name: "wlan0", flags: live, ips: []net.IP{net.ParseIP("fe80::2"), net.ParseIP("192.168.8.20")}}
	wired := ifaceIPs{name: "eth0", flags: live, ips: []net.IP{net.ParseIP("192.168.8.30")}}

	cases := []struct {
		name   string
		ifaces []ifaceIPs
		want   string
	}{
		{"joined network without a gateway", []ifaceIPs{loopback, linkLocal, joined}, "192.168.8.20"},
		{"plugged cable without a gateway", []ifaceIPs{loopback, wired}, "192.168.8.30"},
		{"only the setup AP", []ifaceIPs{loopback, setupAP}, ""},
		{"setup AP beside a joined network", []ifaceIPs{setupAP, joined}, "192.168.8.20"},
		{"USB gadget with a bench host, Wi-Fi not joined", []ifaceIPs{gadget, linkLocal}, ""},
		{"USB gadget listed before the joined network", []ifaceIPs{gadget, joined}, "192.168.8.20"},
		{"address left on an interface without a carrier", []ifaceIPs{noCarrier}, ""},
		{"only link-local", []ifaceIPs{linkLocal}, ""},
		{"nothing", nil, ""},
	}

	for _, tc := range cases {
		if got := lanIPv4(tc.ifaces); got != tc.want {
			t.Errorf("%s: lanIPv4 = %q, want %q", tc.name, got, tc.want)
		}
	}
}
