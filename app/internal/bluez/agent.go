// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package bluez is soundtapd's Bluetooth pairing policy (ADR 0114): a real
// org.bluez.Agent1 over D-Bus, the pairing window, the adapter's name, and clearing
// pairings. bt.sh still starts the stack (attach, bluetoothd, bluealsa); everything that
// decides who may pair lives here, with tests.
//
// The rules are ADR 0055's: outside a window opened from the app or the key, the speaker
// cannot be found and the agent says no. Inside it, just-works pairing is accepted, and only
// the A2DP and AVRCP profiles. A device that pairs in the window is trusted, so it
// reconnects later without asking, and the window then ends 15 s on.
package bluez

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/sysbus"
)

const (
	service     = "org.bluez"
	adapterPath = dbus.ObjectPath("/org/bluez/hci0")
	agentPath   = dbus.ObjectPath("/org/soundtap/agent")
	propsIface  = "org.freedesktop.DBus.Properties"

	// PairWindow is how long the speaker can be found: stock's ready-to-pair state lasts
	// five minutes ("Starting 5 min deactivation timeout").
	PairWindow = 5 * time.Minute
	// afterPair keeps the window a little longer once a phone paired: its first A2DP link
	// may still need an answer from the agent.
	afterPair = 15 * time.Second
)

// ErrNotRunning is what Pair and Clear return while BlueZ is not on the bus.
var ErrNotRunning = errors.New("bluetooth is not running")

var rejected = dbus.NewError("org.bluez.Error.Rejected", []any{"not in a pairing window"})

// Agent keeps BlueZ in line with the pairing policy. The zero value is not usable; use New.
type Agent struct {
	log  *slog.Logger
	now  func() time.Time
	bus  string
	name func() string

	mu      sync.Mutex
	conn    *sysbus.Conn // nil while BlueZ or the bus is away
	until   time.Time    // end of the pairing window; zero when closed
	closing bool         // the window ended but closing it has not worked yet
	alias   string       // what the adapter was told

	wantTrack func() bool // see OnTrack
	setTrack  func(string, Track)
	linkLost  func() // see OnLinkLost
}

// New returns an agent for the system bus. name gives the speaker's name for the adapter's
// alias, as phones list it.
func New(log *slog.Logger, name func() string) *Agent {
	return &Agent{log: log, now: time.Now, bus: sysbus.Address, name: name}
}

// Run keeps the agent registered until ctx ends: it connects, waits for BlueZ, registers,
// and starts over whenever bluetoothd or the bus goes away. bt.sh restarts bluetoothd, and a
// new bluetoothd knows no agent.
func (a *Agent) Run(ctx context.Context) {
	for wait := time.Second; ctx.Err() == nil; {
		err := a.session(ctx)
		if a.registered() {
			wait = time.Second // it worked: a bluetoothd restart is picked up within a second
		}
		a.setConn(nil)
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, errBlueZGone) {
			a.log.Debug("bluetooth agent", "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		wait = min(wait*2, 10*time.Second)
	}
}

var errBlueZGone = errors.New("bluetoothd left the bus")

// session is one connection to the bus, for as long as BlueZ stays on it.
func (a *Agent) session(ctx context.Context) error {
	c, err := sysbus.Dial(ctx, a.bus, 5*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if err := c.ExportMethodTable(a.agentMethods(), agentPath, agentIface); err != nil {
		return err
	}
	for _, rule := range [][]dbus.MatchOption{
		{dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, service)},
		{dbus.WithMatchSender(service), dbus.WithMatchInterface(propsIface), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchArg(0, deviceIface)},
		{dbus.WithMatchSender(service), dbus.WithMatchInterface(deviceIface), dbus.WithMatchMember("Disconnected")},
	} {
		if err := c.AddMatchSignalContext(ctx, rule...); err != nil {
			return err
		}
	}
	if err := a.setup(ctx, c); err != nil {
		return err
	}
	a.setConn(c)
	a.log.Info("bluetooth pairing agent registered", "alias", a.alias)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.Done():
			return errors.New("bus connection closed")
		case s := <-c.Signals():
			switch s.Name {
			case "org.freedesktop.DBus.NameOwnerChanged":
				if len(s.Body) == 3 && s.Body[0] == service && s.Body[2] == "" {
					a.log.Info("bluetoothd left the bus; waiting for it to come back")
					return errBlueZGone
				}
			case deviceIface + ".Disconnected":
				a.deviceDisconnected(s)
			case propsIface + ".PropertiesChanged":
				if len(s.Body) > 0 && s.Body[0] == playerIface {
					a.followTrack(ctx, c)
					continue
				}
				a.devicePropertiesChanged(ctx, c, s)
			}
		case <-tick.C:
			a.tick(ctx, c)
		}
	}
}

// setup registers the agent on a BlueZ that just appeared and puts the adapter in the
// state the policy says: named, every paired device trusted, the window as it stands.
func (a *Agent) setup(ctx context.Context, c *sysbus.Conn) error {
	if _, err := c.Call(ctx, service, "/org/bluez", "org.bluez.AgentManager1", "RegisterAgent", agentPath, "NoInputNoOutput"); err != nil {
		return err
	}
	if _, err := c.Call(ctx, service, "/org/bluez", "org.bluez.AgentManager1", "RequestDefaultAgent", agentPath); err != nil {
		return err
	}
	waitAdapter(ctx, c)
	if err := setProp(ctx, c, adapterPath, "org.bluez.Adapter1", "Powered", true); err != nil {
		a.log.Warn("bluetooth power on", "err", err)
	}
	a.mu.Lock()
	a.alias = ""
	a.mu.Unlock()
	a.followName(ctx, c)
	// Pairing only ever happens inside a window, so every paired device is one to trust;
	// this also covers pairings made before the window existed.
	devs, err := devices(ctx, c)
	if err != nil {
		return err
	}
	for _, d := range devs {
		if d.paired && !d.trusted {
			if err := setProp(ctx, c, d.path, deviceIface, "Trusted", true); err != nil {
				a.log.Warn("bluetooth trust", "device", d.path, "err", err)
			}
		}
	}
	a.mu.Lock()
	open := a.windowOpenLocked()
	if !open {
		a.closing = true // like stock, a start opens no window
	}
	a.mu.Unlock()
	if open {
		return a.applyOpen(ctx, c)
	}
	a.tick(ctx, c)
	return nil
}

func (a *Agent) setConn(c *sysbus.Conn) {
	a.mu.Lock()
	a.conn = c
	a.mu.Unlock()
}

func (a *Agent) registered() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conn != nil
}

func (a *Agent) windowOpenLocked() bool { return !a.until.IsZero() && a.now().Before(a.until) }

// Pairing reports whether the pairing window is open, for the ST20 panel.
func (a *Agent) Pairing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.windowOpenLocked()
}

// Pair opens the pairing window (POST /api/bluetooth/pair, the Bluetooth key's hold).
func (a *Agent) Pair(ctx context.Context) error {
	a.mu.Lock()
	c := a.conn
	if c == nil {
		a.mu.Unlock()
		return ErrNotRunning
	}
	a.until = a.now().Add(PairWindow)
	a.closing = false
	a.mu.Unlock()
	if err := a.applyOpen(ctx, c); err != nil {
		return err
	}
	a.log.Info("bluetooth pairing window open", "seconds", int(PairWindow.Seconds()))
	return nil
}

func (a *Agent) applyOpen(ctx context.Context, c *sysbus.Conn) error {
	if err := setProp(ctx, c, adapterPath, "org.bluez.Adapter1", "Pairable", true); err != nil {
		return err
	}
	return setProp(ctx, c, adapterPath, "org.bluez.Adapter1", "Discoverable", true)
}

// tick closes an expired window and follows a rename. Closing is retried until it works.
//
// Two things measured on the Wireless Link shape the close: BlueZ's own DiscoverableTimeout
// leaves the controller answering inquiries while it reports Discoverable: no, so main.conf
// has 0 and the close is a real write; and writing Discoverable false also turns connectable
// off, so a paired phone could not connect until Connectable goes back on.
func (a *Agent) tick(ctx context.Context, c *sysbus.Conn) {
	a.mu.Lock()
	if !a.until.IsZero() && !a.windowOpenLocked() {
		a.until = time.Time{}
		a.closing = true
	}
	closing := a.closing
	a.mu.Unlock()
	if closing {
		err := setProp(ctx, c, adapterPath, "org.bluez.Adapter1", "Discoverable", false)
		if err == nil {
			err = setProp(ctx, c, adapterPath, "org.bluez.Adapter1", "Pairable", false)
		}
		if err == nil {
			err = setProp(ctx, c, adapterPath, "org.bluez.Adapter1", "Connectable", true)
		}
		a.mu.Lock()
		if err == nil && a.until.IsZero() {
			a.closing = false
		}
		a.mu.Unlock()
		if err == nil {
			a.log.Info("bluetooth pairing window closed")
		}
	}
	a.followName(ctx, c)
	a.followTrack(ctx, c)
}

// followName sets the adapter's alias to the speaker's name. Without one BlueZ advertises
// the hostname, the same on every speaker we build.
func (a *Agent) followName(ctx context.Context, c *sysbus.Conn) {
	name := strings.TrimSpace(a.name())
	a.mu.Lock()
	same := name == "" || name == a.alias
	a.mu.Unlock()
	if same {
		return
	}
	if err := setProp(ctx, c, adapterPath, "org.bluez.Adapter1", "Alias", name); err != nil {
		a.log.Warn("bluetooth rename", "name", name, "err", err)
		return
	}
	a.mu.Lock()
	a.alias = name
	a.mu.Unlock()
}

// devicePropertiesChanged trusts a device that paired inside the window.
func (a *Agent) devicePropertiesChanged(ctx context.Context, c *sysbus.Conn, s *dbus.Signal) {
	if len(s.Body) < 2 || s.Body[0] != deviceIface {
		return
	}
	changed, _ := s.Body[1].(map[string]dbus.Variant)
	paired, bonded := false, false
	for _, key := range []string{"Paired", "Bonded"} {
		if v, ok := changed[key]; ok && v.Value() == true {
			paired = true
			bonded = bonded || key == "Bonded"
		}
	}
	if !paired {
		return
	}
	a.mu.Lock()
	open := a.windowOpenLocked()
	if open {
		a.until = a.now().Add(afterPair)
	}
	a.mu.Unlock()
	if !open {
		return
	}
	if err := setProp(ctx, c, s.Path, deviceIface, "Trusted", true); err != nil {
		a.log.Warn("bluetooth trust", "device", s.Path, "err", err)
		return
	}
	// Paired alone means no link key was stored: the device is forgotten on disconnect.
	a.log.Info("bluetooth device paired and trusted", "device", s.Path, "bonded", bonded)
}

const agentIface = "org.bluez.Agent1"

// agentMethods is org.bluez.Agent1 as BlueZ calls it, each answer from decide. A call with
// other arguments, or a method not listed, is refused by godbus.
func (a *Agent) agentMethods() map[string]any {
	return map[string]any{
		"Release":              func() *dbus.Error { return a.decide("Release", "") },
		"Cancel":               func() *dbus.Error { return a.decide("Cancel", "") },
		"DisplayPinCode":       func(dbus.ObjectPath, string) *dbus.Error { return a.decide("DisplayPinCode", "") },
		"DisplayPasskey":       func(dbus.ObjectPath, uint32, uint16) *dbus.Error { return a.decide("DisplayPasskey", "") },
		"RequestPinCode":       func(dbus.ObjectPath) (string, *dbus.Error) { return "", a.decide("RequestPinCode", "") },
		"RequestPasskey":       func(dbus.ObjectPath) (uint32, *dbus.Error) { return 0, a.decide("RequestPasskey", "") },
		"RequestConfirmation":  func(dbus.ObjectPath, uint32) *dbus.Error { return a.decide("RequestConfirmation", "") },
		"RequestAuthorization": func(dbus.ObjectPath) *dbus.Error { return a.decide("RequestAuthorization", "") },
		"AuthorizeService": func(_ dbus.ObjectPath, uuid string) *dbus.Error {
			return a.decide("AuthorizeService", uuid)
		},
	}
}

// decide answers one Agent1 call: nil is yes. uuid is AuthorizeService's profile.
func (a *Agent) decide(member, uuid string) *dbus.Error {
	a.mu.Lock()
	open := a.windowOpenLocked()
	a.mu.Unlock()
	switch member {
	case "Release", "Cancel", "DisplayPinCode", "DisplayPasskey":
		return nil
	case "RequestAuthorization", "RequestConfirmation":
		if open {
			return nil
		}
	case "AuthorizeService":
		if open && audioProfile(uuid) {
			return nil
		}
	}
	a.log.Info("bluetooth agent said no", "request", member)
	return rejected
}

// audioProfile reports whether uuid is A2DP (0x110a-0x110d) or AVRCP (0x110e): the only
// profiles a speaker has a use for.
func audioProfile(uuid string) bool {
	u := strings.ToLower(uuid)
	return len(u) == 36 && strings.HasPrefix(u, "0000110") && u[7] >= 'a' && u[7] <= 'e' &&
		strings.HasSuffix(u, "-0000-1000-8000-00805f9b34fb")
}

// Clear forgets every paired device (POST /api/bluetooth/clear; no key hold clears
// them). It returns how many it removed.
func (a *Agent) Clear(ctx context.Context) (int, error) {
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	if c == nil {
		return 0, ErrNotRunning
	}
	devs, err := devices(ctx, c)
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	for _, d := range devs {
		if !d.paired {
			continue
		}
		if _, err := c.Call(ctx, service, adapterPath, "org.bluez.Adapter1", "RemoveDevice", d.path); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	a.log.Info("bluetooth pairings cleared", "removed", removed, "failed", len(errs))
	return removed, errors.Join(errs...)
}

type device struct {
	path            dbus.ObjectPath
	paired, trusted bool
}

// managedObjects is ObjectManager.GetManagedObjects' answer: per object path, per
// interface, its properties.
type managedObjects = map[dbus.ObjectPath]map[string]map[string]dbus.Variant

func getManagedObjects(ctx context.Context, c *sysbus.Conn) (managedObjects, error) {
	var objs managedObjects
	err := c.Object(service, "/").CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objs)
	return objs, err
}

// devices lists the adapter's remote devices from GetManagedObjects, in path order.
func devices(ctx context.Context, c *sysbus.Conn) ([]device, error) {
	objs, err := getManagedObjects(ctx, c)
	if err != nil {
		return nil, err
	}
	var devs []device
	eachOnAdapter(objs, deviceIface, func(path dbus.ObjectPath, props map[string]dbus.Variant) {
		devs = append(devs, device{path: path, paired: props["Paired"].Value() == true, trusted: props["Trusted"].Value() == true})
	})
	return devs, nil
}

// adapterWait bounds how long setup waits for the adapter to appear on the bus.
const adapterWait = 10 * time.Second

// waitAdapter returns once BlueZ serves the adapter, or after adapterWait. bluetoothd
// takes the bus name before it has registered hci0: power, name and trust set in that gap
// failed with UnknownObject at every boot of the Wireless Link, and power is set only here.
func waitAdapter(ctx context.Context, c *sysbus.Conn) {
	for end := time.Now().Add(adapterWait); time.Now().Before(end); {
		cctx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := c.Call(cctx, service, adapterPath, propsIface, "Get", "org.bluez.Adapter1", "Address")
		cancel()
		if err == nil || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func setProp(ctx context.Context, c *sysbus.Conn, path dbus.ObjectPath, iface, name string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.Call(ctx, service, path, propsIface, "Set", iface, name, dbus.MakeVariant(v))
	return err
}
