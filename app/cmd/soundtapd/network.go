// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
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

// listenAddrs is where soundtapd listens: :80 on every model (ADR 0001), or the
// comma-separated -listen list.
func listenAddrs(listen string) []string {
	if listen == "" {
		return []string{defaultListenAddr}
	}
	var addrs []string
	for _, a := range strings.Split(listen, ",") {
		if a = strings.TrimSpace(a); a != "" {
			addrs = append(addrs, a)
		}
	}
	return addrs
}

// listen binds the listeners and, once the model's own one is up, tells the front
// panel and the boot watchdog that soundtapd started. It exits when that fails.
func listen(opt options, mux http.Handler, log *slog.Logger) []*http.Server {
	// Binding is best-effort: a port already in use (or not permitted) is skipped, and
	// we only fail if nothing bound.
	addrs := listenAddrs(opt.listen)
	servers, bound := serveAll(addrs, mux, log)
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
	if err := os.WriteFile(bootDisarmPath, nil, 0o644); err != nil {
		log.Warn("boot watchdog disarm", "path", bootDisarmPath, "err", err)
	}
	return servers
}

// serveAll serves mux on every address it can bind and reports which ones it bound.
// One that is in use or not permitted is logged and skipped.
func serveAll(addrs []string, mux http.Handler, log *slog.Logger) ([]*http.Server, map[string]bool) {
	var servers []*http.Server
	bound := map[string]bool{}
	for _, a := range addrs {
		ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", a)
		if err != nil {
			log.Warn("listen skipped", "addr", a, "err", err)
			continue
		}
		limited := limitConns(ln)
		s := &http.Server{
			ConnState:         limited.connState,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			// Never close an idle connection before the browser does: iOS sends a write
			// on a kept-alive connection we already closed and reports a network error
			// instead of retrying. Dead peers still go through TCP keepalive.
			IdleTimeout:    30 * time.Minute,
			MaxHeaderBytes: 16 << 10,
			// Handler panics and TLS noise in the same format and stream as the rest.
			ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError),
		}
		servers = append(servers, s)
		bound[a] = true
		go func(s *http.Server, ln net.Listener, a string) {
			if err := s.Serve(limited); err != nil && err != http.ErrServerClosed {
				log.Error("serve", "addr", a, "err", err)
			}
		}(s, limited, a)
		log.Info("listening", "addr", a)
	}
	return servers, bound
}

func persistWifiProfile(path, ssid, psk string, log *slog.Logger) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Warn("wifi mkdir", "path", path, "err", err)
		return err
	}
	// A 64-digit hex key goes to wpa_supplicant as the key itself, not as a passphrase.
	if err := wifi.WriteConf(path, wifi.NewRecord(ssid, psk)); err != nil {
		log.Warn("wifi save", "path", path, "err", err)
		return err
	}
	log.Info("wifi profile saved", "path", path, "ssid", ssid)
	return nil
}

// maintainIP keeps sp.IP set to the primary LAN IPv4, refreshing as the network changes
// (Wi-Fi associates after soundtapd starts). It also clears a lease cached before a route
// disappeared, so the API and LEDs do not report a connection that no longer exists.
func maintainIP(ctx context.Context, sp *speakerstate.Speaker, webSrv *web.Server, name string, svc mdns.Service, log *slog.Logger) {
	maintainIPWith(ctx, sp, webSrv, name, svc, log, 15*time.Second, outboundIP)
}

func maintainIPWith(ctx context.Context, sp *speakerstate.Speaker, webSrv *web.Server, name string, svc mdns.Service, log *slog.Logger, interval time.Duration, lookup func() string) {
	cur := sp.StatusSnapshot().IP
	var md *mdns.Responder
	for {
		if ip := lookup(); ip != cur {
			cur = ip
			sp.SetIP(ip)
			if ip == "" {
				log.Info("ip lost")
				continue
			}
			log.Info("ip", "addr", ip)
			if devMode.Load() {
				continue // a development run announces nothing on the LAN
			}
			if md == nil {
				md = startMDNS(ctx, ip, name, svc, webSrv, log)
			} else {
				// A new DHCP lease after a reconnect: re-point the A record, or
				// <name>.local keeps resolving to the old address until a reboot.
				md.SetIP(ip)
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
func startMDNS(ctx context.Context, ip, name string, svc mdns.Service, webSrv *web.Server, log *slog.Logger) *mdns.Responder {
	md := mdns.New(ip, name, log.With("comp", "mdns"))
	// Advertise and browse _soundtap._tcp, enabled before Run. The peers channel is drained
	// into the web server's device registry -- the list behind GET /api/devices, and what a
	// group or a proxy route resolves through.
	if svc.ID != "" {
		peers := make(chan mdns.Peer, 16)
		md.EnableDNSService(svc, peers)
		goSafe("drainPeers", func() { drainPeers(ctx, peers, webSrv) })
	}
	// One name for the house, alongside every speaker's own. Whoever is up first answers for
	// it; when that speaker goes away the next one takes it. Not leader election -- avahi's
	// probe-and-claim, pointed at a name nobody renames.
	md.EnableSharedName(sharedHostName)
	webSrv.SetMDNS(md)
	go func() {
		if err := md.Run(ctx); err != nil {
			log.Warn("mdns", "err", err)
		}
	}()
	return md
}

// outboundIP returns the primary non-loopback IPv4 by inspecting the route to a public
// address (no packet is sent). Empty when offline.
func outboundIP() string {
	c, err := (&net.Dialer{}).DialContext(context.Background(), "udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer func() { _ = c.Close() }()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// setupAPUp is the setup AP's marker, which the Wi-Fi manager keeps while the AP runs.
var setupAPUp = "/run/soundtap/setup-ap"

// toggleSetupAP is the keys' setup chord: into the setup AP, or out of it again as on
// stock, when there is a saved network to go back to. openSetup keeps wifi.conf as it is,
// so stock's way out stays too: a power cycle, or nobody joining the AP for 5 minutes,
// goes back to that network.
func toggleSetupAP(wifiConf string, restart, openSetup func() error) error {
	_, up := os.Stat(setupAPUp)
	if conf, err := os.ReadFile(wifiConf); up == nil && err == nil && savedNetwork(conf) {
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
	if err := persistWifiProfile(wifiConf, "", "", slog.Default()); err != nil {
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
			ip := ""
			if peer.IP != nil {
				ip = peer.IP.String()
			}
			webSrv.SetDevice(web.Device{
				ID:        peer.ID,
				Name:      peer.Name,
				Model:     peer.Model,
				Firmware:  peer.Firmware,
				IP:        ip,
				Port:      peer.Port,
				Departure: peer.Departure,
			})
		}
	}
}
