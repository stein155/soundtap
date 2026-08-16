// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
)

// Serve runs SSDP discovery until ctx ends: it answers M-SEARCH queries for our device
// and service types and periodically NOTIFYs ssdp:alive on the multicast group.
func (r *Renderer) Serve(ctx context.Context, log func(msg string, kv ...any)) {
	safego.Go("upnp.watchEvents", func() { r.watchEvents(ctx) })
	group, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		log("ssdp resolve", "err", err)
		return
	}
	// Joining the group needs a route that covers it, which a speaker still waiting for
	// Wi-Fi (the ST20 at boot, any model on its setup AP) does not have yet: ENODEV. So the
	// join is retried until it works, or SSDP stays dead for the whole uptime.
	conn, err := listenSSDP("udp4", nil, group)
	if err != nil {
		log("ssdp listen", "err", err)
		for err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(ssdpListenRetry):
			}
			conn, err = listenSSDP("udp4", nil, group)
		}
		log("ssdp listening")
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadBuffer(1 << 16)

	safego.Go("upnp.notifyLoop", func() { r.notifyLoop(ctx, group) })

	buf := make([]byte, 2048)
	for ctx.Err() == nil {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue // timeout tick or transient
		}
		msg := string(buf[:n])
		if !strings.HasPrefix(msg, "M-SEARCH") {
			continue
		}
		st := searchTarget(msg)
		for _, tgt := range r.matchTargets(st) {
			_, _ = conn.WriteToUDP([]byte(r.searchResponse(tgt)), src)
		}
	}
}

func (r *Renderer) notifyLoop(ctx context.Context, group *net.UDPAddr) {
	out, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		return
	}
	defer func() { _ = out.Close() }()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	send := func() {
		for _, tgt := range r.allTargets() {
			_, _ = out.Write([]byte(r.notifyAlive(tgt)))
		}
	}
	send()
	for {
		select {
		case <-ctx.Done():
			// Controllers drop the renderer now rather than when max-age runs out.
			for _, tgt := range r.allTargets() {
				_, _ = out.Write([]byte(r.notifyByebye(tgt)))
			}
			return
		case <-t.C:
			send()
		}
	}
}

// allTargets are the NT/ST values we advertise (root device, our uuid, device + services).
func (r *Renderer) allTargets() []string {
	return []string{"upnp:rootdevice", r.UDN, rootDevice, avTransURN, renderURN, connMgrURN}
}

// matchTargets returns the advertised targets that satisfy an M-SEARCH ST.
func (r *Renderer) matchTargets(st string) []string {
	if st == "ssdp:all" {
		return r.allTargets()
	}
	for _, t := range r.allTargets() {
		if t == st {
			return []string{t}
		}
	}
	return nil
}

func (r *Renderer) usn(target string) string {
	if target == r.UDN {
		return r.UDN
	}
	return r.UDN + "::" + target
}

func (r *Renderer) searchResponse(target string) string {
	return "HTTP/1.1 200 OK\r\n" +
		"CACHE-CONTROL: max-age=1800\r\n" +
		"EXT:\r\n" +
		"LOCATION: " + r.location() + "\r\n" +
		"SERVER: soundtap/1 UPnP/1.0 MediaRenderer/1\r\n" +
		"ST: " + target + "\r\n" +
		"USN: " + r.usn(target) + "\r\n\r\n"
}

func (r *Renderer) notifyAlive(target string) string {
	return "NOTIFY * HTTP/1.1\r\n" +
		"HOST: " + ssdpAddr + "\r\n" +
		"CACHE-CONTROL: max-age=1800\r\n" +
		"LOCATION: " + r.location() + "\r\n" +
		"NT: " + target + "\r\n" +
		"NTS: ssdp:alive\r\n" +
		"SERVER: soundtap/1 UPnP/1.0 MediaRenderer/1\r\n" +
		"USN: " + r.usn(target) + "\r\n\r\n"
}

func (r *Renderer) notifyByebye(target string) string {
	return "NOTIFY * HTTP/1.1\r\n" +
		"HOST: " + ssdpAddr + "\r\n" +
		"NT: " + target + "\r\n" +
		"NTS: ssdp:byebye\r\n" +
		"USN: " + r.usn(target) + "\r\n\r\n"
}

// listenSSDP joins the SSDP group, and ssdpListenRetry is how long Serve waits between
// tries. Variables so the test can fail the join without a network.
var (
	listenSSDP      = net.ListenMulticastUDP
	ssdpListenRetry = 5 * time.Second
)
