// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/mdns"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/web"
	"github.com/stein155/soundtap/app/internal/wifi"
)

// The API's HTTP servers and the files the network side writes.
const (
	apiReadHeaderTimeout = 5 * time.Second
	// apiIdleTimeout never closes an idle connection before the browser does: iOS sends a
	// write on a kept-alive connection we already closed and reports a network error
	// instead of retrying. Dead peers still go through TCP keepalive.
	apiIdleTimeout    = 30 * time.Minute
	apiMaxHeaderBytes = 16 << 10
	bootDisarmMode    = 0o644
	wifiConfDirMode   = 0o755
	ipRefreshInterval = 15 * time.Second
	mdnsPeersBuffered = 16
)

// listenAddrs is where soundtapd listens: :80 on every model (ADR 0001), or the
// comma-separated -listen list.
func listenAddrs(listen string) []string {
	if listen == "" {
		return []string{defaultListenAddr}
	}

	var addrs []string

	for a := range strings.SplitSeq(listen, ",") {
		if a = strings.TrimSpace(a); a != "" {
			addrs = append(addrs, a)
		}
	}

	return addrs
}

// listen binds the listeners and, once the model's own one is up, tells the front
// panel and the boot watchdog that soundtapd started. It exits when that fails, at once:
// returning would run the daemon's deferred shutdown first, which it never did.
//
//nolint:revive // deep-exit: the immediate exit, skipping main's deferred shutdown, is the behaviour
func listen(ctx context.Context, opt options, mux http.Handler, log *slog.Logger) []*http.Server {
	// Binding is best-effort: a port already in use (or not permitted) is skipped, and
	// we only fail if nothing bound.
	addrs := listenAddrs(opt.listen)

	servers, bound := serveAll(ctx, addrs, mux, log)
	if len(servers) == 0 {
		log.Error("no listen address could be bound", "tried", addrs)
		os.Exit(1)
	}

	if opt.listen == "" && !bound[board.ListenAddr] {
		log.Error("primary listen address unavailable", "required", board.ListenAddr)
		os.Exit(1)
	}
	// The front panel stops chasing here too: the daemon answering is what "started"
	// means to someone standing in front of the speaker.
	daemonUp.Store(true)
	// Disarm only after the model's required listener is confirmed. A secondary
	// listener must not leave a crash-looping RAM boot marked healthy.
	if devMode.Load() {
		return servers // no boot watchdog to disarm on a computer
	}

	err := os.WriteFile(bootDisarmPath, nil, bootDisarmMode)
	if err != nil {
		log.Warn("boot watchdog disarm", "path", bootDisarmPath, "err", err)
	}

	return servers
}

// serveAll serves mux on every address it can bind and reports which ones it bound.
// One that is in use or not permitted is logged and skipped.
// Binding is not cut short by a shutdown that has begun: the listeners are shut down after.
func serveAll(
	ctx context.Context, addrs []string, mux http.Handler, log *slog.Logger,
) ([]*http.Server, map[string]bool) {
	var servers []*http.Server

	bound := map[string]bool{}

	for _, addr := range addrs {
		listener, err := (&net.ListenConfig{}).Listen(context.WithoutCancel(ctx), "tcp", addr)
		if err != nil {
			log.Warn("listen skipped", "addr", addr, "err", err)

			continue
		}

		limited := limitConns(listener)
		server := &http.Server{
			ConnState:         limited.connState,
			Handler:           mux,
			ReadHeaderTimeout: apiReadHeaderTimeout,
			IdleTimeout:       apiIdleTimeout,
			MaxHeaderBytes:    apiMaxHeaderBytes,
			// Handler panics and TLS noise in the same format and stream as the rest.
			ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError),
		}
		servers = append(servers, server)

		bound[addr] = true

		go func() {
			err := server.Serve(limited)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("serve", "addr", addr, "err", err)
			}
		}()

		log.Info("listening", "addr", addr)
	}

	return servers, bound
}

func persistWifiProfile(path, ssid, security, psk string, log *slog.Logger) error {
	err := os.MkdirAll(filepath.Dir(path), wifiConfDirMode)
	if err != nil {
		log.Warn("wifi mkdir", "path", path, "err", err)

		return fmt.Errorf("save the Wi-Fi network: %w", err)
	}
	// A 64-digit hex key goes to wpa_supplicant as the key itself, not as a passphrase.
	err = wifi.WriteConf(path, wifi.NewRecord(ssid, security, psk))
	if err != nil {
		log.Warn("wifi save", "path", path, "err", err)

		return fmt.Errorf("save the Wi-Fi network: %w", err)
	}

	log.Info("wifi profile saved", "path", path, "ssid", ssid)

	return nil
}

// maintainIP keeps sp.IP set to the primary LAN IPv4, refreshing as the network changes
// (Wi-Fi associates after soundtapd starts). It also clears a lease cached before a route
// disappeared, so the API and LEDs do not report a connection that no longer exists.
func maintainIP(
	ctx context.Context, sp *speakerstate.Speaker, webSrv *web.Server, name string, svc mdns.Service, log *slog.Logger,
) {
	maintainIPWith(ctx, sp, webSrv, name, svc, log, ipRefreshInterval, outboundIP)
}

func maintainIPWith(
	ctx context.Context, speaker *speakerstate.Speaker, webSrv *web.Server, name string, svc mdns.Service,
	log *slog.Logger, interval time.Duration, lookup func() string,
) {
	cur := speaker.StatusSnapshot().IP

	var responder *mdns.Responder

	for {
		if addr := lookup(); addr != cur {
			cur = addr
			speaker.SetIP(addr)

			if addr == "" {
				log.Info("ip lost")

				continue
			}

			log.Info("ip", "addr", addr)

			if devMode.Load() {
				continue // a development run announces nothing on the LAN
			}

			if responder == nil {
				responder = startMDNS(ctx, addr, name, svc, webSrv, log)
			} else {
				// A new DHCP lease after a reconnect: re-point the A record, or
				// <name>.local keeps resolving to the old address until a reboot.
				responder.SetIP(addr)
			}
		}
		// Without an address the first one should show at once: right after a boot
		// /api/info reported an empty ip for up to a whole interval.
		wait := interval
		if cur == "" {
			wait = min(interval, time.Second)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// startMDNS starts the responder once we have a real IP (it needs it at creation), so the
// speaker is reachable at <name>.local.
func startMDNS(
	ctx context.Context, ip, name string, svc mdns.Service, webSrv *web.Server, log *slog.Logger,
) *mdns.Responder {
	responder := mdns.New(ip, name, log.With("comp", "mdns"))
	// Advertise and browse _soundtap._tcp, enabled before Run. The peers channel is drained
	// into the web server's device registry -- the list behind GET /api/devices, and what a
	// group or a proxy route resolves through.
	if svc.ID != "" {
		peers := make(chan mdns.Peer, mdnsPeersBuffered)
		responder.EnableDNSService(svc, peers)
		goSafe("drainPeers", func() { drainPeers(ctx, peers, webSrv) })
	}
	// One name for the house, alongside every speaker's own. Whoever is up first answers for
	// it; when that speaker goes away the next one takes it. Not leader election -- avahi's
	// probe-and-claim, pointed at a name nobody renames.
	responder.EnableSharedName(sharedHostName)

	webSrv.SetMDNS(responder)
	go func() {
		err := responder.Run(ctx)
		if err != nil {
			log.Warn("mdns", "err", err)
		}
	}()

	return responder
}

// outboundIP returns the primary non-loopback IPv4 by inspecting the route to a public
// address (no packet is sent), or, on a network without a default route (a router that
// hands out no gateway, a LAN without internet), the IPv4 its interface holds. Empty when
// offline. IPv4 stays the speaker's one address for the API, the A record and multiroom;
// its IPv6 addresses come on top (ADR 0134).
func outboundIP() string {
	if ip := routedIPv4(); ip != "" {
		return ip
	}

	return lanIPv4(interfaceIPs())
}

// routedIPv4 is the source address of the route to a public address, empty without one.
func routedIPv4() string {
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	if local, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return local.IP.String()
	}

	return ""
}

// ifaceIPs is one interface's flags and addresses, as lanIPv4 reads them.
type ifaceIPs struct {
	name  string
	flags net.Flags
	ips   []net.IP
}

func interfaceIPs() []ifaceIPs {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	out := make([]ifaceIPs, 0, len(ifaces))

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		entry := ifaceIPs{name: iface.Name, flags: iface.Flags}

		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				entry.ips = append(entry.ips, ipNet.IP)
			}
		}

		out = append(out, entry)
	}

	return out
}

// lanIPv4 is the first IPv4 of the station or wired interface (wlan0, eth0) that is up with
// a carrier (a joined network, a plugged cable), not link-local. Other interfaces never
// count: the setup AP's wlan1 (the speaker is the router there) and an alpha image's USB
// gadget (usb0, 192.168.7.2 with a bench host on the cable) say nothing about the LAN.
func lanIPv4(ifaces []ifaceIPs) string {
	const live = net.FlagUp | net.FlagRunning

	for _, iface := range ifaces {
		if (iface.name != "wlan0" && iface.name != "eth0") || iface.flags&live != live {
			continue
		}

		for _, ip := range iface.ips {
			v4 := ip.To4()
			if v4 == nil || v4.IsLinkLocalUnicast() {
				continue
			}

			return v4.String()
		}
	}

	return ""
}

// setupAPUp is the setup AP's marker, which the Wi-Fi manager keeps while the AP runs.
//
//nolint:gochecknoglobals // a test seam: tests put the marker in a temp dir
var setupAPUp = "/run/soundtap/setup-ap"

// toggleSetupAP is the keys' setup chord: into the setup AP, or out of it again as on
// stock, when there is a saved network to go back to. openSetup keeps wifi.conf as it is,
// so stock's way out stays too: a power cycle, or nobody joining the AP for 5 minutes,
// goes back to that network.
func toggleSetupAP(wifiConf string, restart, openSetup func() error) error {
	_, up := os.Stat(setupAPUp)

	conf, err := os.ReadFile(wifiConf)
	if up == nil && err == nil && savedNetwork(conf) {
		return restart()
	}

	return openSetup()
}

func savedNetwork(conf []byte) bool {
	for line := range strings.Lines(string(conf)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "SSID_B64="); ok && v != "" {
			return true
		}
	}

	return false
}

// enterSetupMode forgets the network too: factory reset, which has to.
func enterSetupMode(wifiConf string) error {
	// Keep an empty persistent override so the Wi-Fi manager cannot fall back to
	// credentials baked into /etc/soundtap-wifi.conf after setup mode or factory reset.
	err := persistWifiProfile(wifiConf, "", "", "", slog.Default())
	if err != nil {
		return err
	}

	_ = restartWifi(slog.Default()) // off the speaker there is nothing to restart

	return nil
}

// drainPeers copies what the mDNS browser finds into the web server's device registry.
// The browser sends without blocking its receive loop, so this only has to keep up; a
// missed update is re-sent at the next announcement.
func drainPeers(ctx context.Context, peers <-chan mdns.Peer, webSrv *web.Server) {
	for {
		select {
		case <-ctx.Done():
			return
		case peer := <-peers:
			addr := ""
			if peer.IP != nil {
				addr = peer.IP.String()
			}

			webSrv.SetDevice(web.Device{
				ID:           peer.ID,
				Name:         peer.Name,
				Model:        peer.Model,
				Firmware:     peer.Firmware,
				IP:           addr,
				Port:         peer.Port,
				Departure:    peer.Departure,
				Capabilities: nil, // a peer's TXT record does not carry them
			})
		}
	}
}
