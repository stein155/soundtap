// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/sysbus"
)

// avahi-daemon's D-Bus API (avahi-daemon/org.freedesktop.Avahi.*.xml), the same calls
// avahi's own client library and tools make.
const (
	avahiName        = "org.freedesktop.Avahi"
	avahiServer      = "org.freedesktop.Avahi.Server"
	avahiEntryGroup  = "org.freedesktop.Avahi.EntryGroup"
	avahiBrowser     = "org.freedesktop.Avahi.ServiceBrowser"
	nameOwnerChanged = "org.freedesktop.DBus.NameOwnerChanged"

	ifaceAny   = int32(-1) // AVAHI_IF_UNSPEC
	protoAny   = int32(-1) // AVAHI_PROTO_UNSPEC
	protoIPv4  = int32(0)  // AVAHI_PROTO_INET
	noReverse  = uint32(16)
	noFlags    = uint32(0)
	anyDomain  = "" // the default, local
	ownHost    = "" // the server's own host name
	callWait   = 5 * time.Second
	resolveCap = 5 * time.Second // avahi-resolve's own wait is about this
)

// avahi's server and entry group states (AvahiServerState, AvahiEntryGroupState).
const (
	serverRegistering = int32(1)
	serverRunning     = int32(2)
	serverCollision   = int32(3)

	groupEstablished = int32(2)
	groupCollision   = int32(3)
	groupFailure     = int32(4)
)

// errAvahiGone ends a session when avahi-daemon leaves the bus.
var errAvahiGone = errors.New("avahi-daemon left the bus")

// errNoIPv4 is a resolved service that has no IPv4 address.
var errNoIPv4 = errors.New("has no IPv4 address")

// call calls one method of avahi's on path with a bounded wait.
func call(ctx context.Context, conn *sysbus.Conn, path dbus.ObjectPath, iface, member string, args ...any) *dbus.Call {
	ctx, cancel := context.WithTimeout(ctx, callWait)
	defer cancel()

	return conn.Object(avahiName, path).CallWithContext(ctx, iface+"."+member, 0, args...)
}

// watchAvahi asks the bus for avahi-daemon leaving and for the signals of iface.
func watchAvahi(ctx context.Context, conn *sysbus.Conn, ifaces ...string) error {
	rules := make([][]dbus.MatchOption, 0, 1+len(ifaces))
	rules = append(rules, []dbus.MatchOption{
		dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, avahiName),
	})

	for _, iface := range ifaces {
		rules = append(rules, []dbus.MatchOption{dbus.WithMatchSender(avahiName), dbus.WithMatchInterface(iface)})
	}

	for _, rule := range rules {
		err := conn.AddMatchSignalContext(ctx, rule...)
		if err != nil {
			return fmt.Errorf("mdns: add match: %w", err)
		}
	}

	return nil
}

// avahiLeft reports whether s says avahi-daemon left the bus.
func avahiLeft(s *dbus.Signal) bool {
	return s.Name == nameOwnerChanged && len(s.Body) == 3 && s.Body[0] == avahiName && s.Body[2] == ""
}

// txtRecord is the TXT record as AddService takes it, in the order avahi-publish passes
// its arguments over D-Bus.
func txtRecord(name string, svc Service) [][]byte {
	return [][]byte{
		[]byte("id=" + svc.ID), []byte("name=" + name), []byte("model=" + svc.Model),
		[]byte("fw=" + svc.Firmware), []byte("api=" + strconv.Itoa(svc.API)),
	}
}

// parseTXT reads a TXT record's key=value strings; a string without "=" is skipped.
func parseTXT(txt [][]byte) map[string]string {
	out := map[string]string{}

	for _, item := range txt {
		if k, v, ok := strings.Cut(string(item), "="); ok {
			out[k] = v
		}
	}

	return out
}

// resolved is one service as ResolveService answers it.
type resolved struct {
	host string
	ip   net.IP
	port uint16
	txt  map[string]string
}

// resolveInstance asks avahi for the IPv4 address, port and TXT of one service instance.
func resolveInstance(
	ctx context.Context, conn *sysbus.Conn, iface int32, instance, serviceType string,
) (resolved, error) {
	ctx, cancel := context.WithTimeout(ctx, resolveCap)
	defer cancel()

	var (
		gotIface, gotProto, gotAProto int32
		name, typ, domain, host, addr string
		port                          uint16
		txt                           [][]byte
		flags                         uint32
	)

	err := conn.Object(avahiName, "/").CallWithContext(ctx, avahiServer+".ResolveService", 0,
		iface, protoIPv4, instance, serviceType, anyDomain, protoIPv4, noFlags).
		Store(&gotIface, &gotProto, &name, &typ, &domain, &host, &gotAProto, &addr, &port, &txt, &flags)
	if err != nil {
		return resolved{}, fmt.Errorf("mdns: resolve %s: %w", instance, err)
	}

	return resolved{host: host, ip: net.ParseIP(addr).To4(), port: port, txt: parseTXT(txt)}, nil
}

// ResolveService finds the IPv4 host:port of one DNS-SD service instance through avahi on
// the bus at addr (sysbus.Address on a speaker), for example an AirPlay sender's
// iTunes_Ctrl_<id> in _dacp._tcp.
func ResolveService(ctx context.Context, addr, instance, serviceType string) (string, error) {
	conn, err := sysbus.Dial(ctx, addr, callWait)
	if err != nil {
		return "", fmt.Errorf("mdns: %w", err)
	}
	defer func() { _ = conn.Close() }()

	res, err := resolveInstance(ctx, conn, ifaceAny, instance, serviceType)
	if err != nil {
		return "", err
	}

	if res.ip == nil {
		return "", fmt.Errorf("mdns: %s %w", instance, errNoIPv4)
	}

	return net.JoinHostPort(res.ip.String(), strconv.Itoa(int(res.port))), nil
}

// interfaceName names an avahi interface index for the table and Recent.
func interfaceName(index int32) string {
	netIface, err := net.InterfaceByIndex(int(index))
	if err == nil {
		return netIface.Name
	}

	return "if" + strconv.Itoa(int(index))
}
