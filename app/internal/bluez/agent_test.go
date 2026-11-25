// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bluez

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/dbustest"
	"github.com/stein155/soundtap/app/internal/sysbus"
)

// fakeBlueZ is BlueZ on a test bus: it answers the agent's calls, records every property
// write, and can call the agent or send it a signal.
type fakeBlueZ struct {
	t    *testing.T
	bus  *dbustest.Bus
	conn *dbus.Conn

	mu      sync.Mutex
	sets    []string // "path iface.Name=value"
	removed []dbus.ObjectPath
	devices []device
	agent   string // the unique name that registered the agent
	ready   chan struct{}
	// adapterLate is how many more adapter Gets fail before hci0 exists; until then every
	// adapter call fails too, as on a bluetoothd that has the bus name but no adapter yet.
	adapterLate int
}

var errUnknownObject = dbus.NewError("org.freedesktop.DBus.Error.UnknownObject", []any{"no such object"})

func newFake(t *testing.T, devs ...device) *fakeBlueZ {
	t.Helper()

	return newFakeOn(t, dbustest.New(t), devs...)
}

// newFakeOn puts a BlueZ on bus, as a restarted bluetoothd does.
func newFakeOn(t *testing.T, bus *dbustest.Bus, devs ...device) *fakeBlueZ {
	t.Helper()
	f := &fakeBlueZ{t: t, bus: bus, conn: bus.Connect(t), devices: devs, ready: make(chan struct{})}

	var once sync.Once
	// Properties answer on every object under /org/bluez. godbus looks a path up as an
	// export of its own before its parents' subtrees, so the adapter carries them too.
	props := map[string]any{
		"Set": func(m dbus.Message, iface, name string, v dbus.Variant) *dbus.Error {
			path := m.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
			if f.late(path, false) {
				return errUnknownObject
			}

			f.mu.Lock()
			f.sets = append(f.sets, fmt.Sprintf("%s %s.%s=%v", path, strings.TrimPrefix(iface, "org.bluez."), name, v.Value()))
			f.mu.Unlock()

			return nil
		},
		"Get": func(m dbus.Message, _, _ string) (dbus.Variant, *dbus.Error) {
			if f.late(m.Headers[dbus.FieldPath].Value().(dbus.ObjectPath), true) {
				return dbus.Variant{}, errUnknownObject
			}

			return dbus.MakeVariant("02:00:00:00:00:01"), nil
		},
	}

	exports := []struct {
		path    dbus.ObjectPath
		iface   string
		methods map[string]any
		subtree bool
	}{
		{"/org/bluez", "org.bluez.AgentManager1", map[string]any{
			"RegisterAgent": func(from dbus.Sender, _ dbus.ObjectPath, _ string) *dbus.Error {
				f.mu.Lock()
				f.agent = string(from)
				f.mu.Unlock()

				return nil
			},
			"RequestDefaultAgent": func(dbus.ObjectPath) *dbus.Error {
				once.Do(func() { close(f.ready) })

				return nil
			},
		}, false},
		{"/", "org.freedesktop.DBus.ObjectManager", map[string]any{
			"GetManagedObjects": func() (managedObjects, *dbus.Error) { return f.objects(), nil },
		}, false},
		{"/org/bluez", propsIface, props, true},
		{adapterPath, propsIface, props, true},
		{adapterPath, "org.bluez.Adapter1", map[string]any{
			"RemoveDevice": func(d dbus.ObjectPath) *dbus.Error {
				f.mu.Lock()
				f.removed = append(f.removed, d)
				f.mu.Unlock()

				return nil
			},
		}, true},
	}
	for _, e := range exports {
		export := f.conn.ExportMethodTable
		if e.subtree {
			export = f.conn.ExportSubtreeMethodTable
		}

		err := export(e.methods, e.path, e.iface)
		if err != nil {
			t.Fatal(err)
		}
	}

	if reply, err := f.conn.RequestName(service, 0); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("RequestName(%s) = %v, %v", service, reply, err)
	}

	return f
}

// late reports whether a call on path finds no adapter yet; a Get counts down.
func (f *fakeBlueZ) late(path dbus.ObjectPath, get bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	late := f.adapterLate > 0 && strings.HasPrefix(string(path), string(adapterPath))
	if late && get {
		f.adapterLate--
	}

	return late
}

func (f *fakeBlueZ) objects() managedObjects {
	f.mu.Lock()
	defer f.mu.Unlock()

	objs := managedObjects{adapterPath: {"org.bluez.Adapter1": {}}}
	for _, d := range f.devices {
		objs[d.path] = map[string]map[string]dbus.Variant{deviceIface: {
			"Paired": dbus.MakeVariant(d.paired), "Trusted": dbus.MakeVariant(d.trusted),
		}}
	}

	return objs
}

func (f *fakeBlueZ) takeSets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := f.sets
	f.sets = nil

	return out
}

// ask calls the agent like bluetoothd does and returns its error name, "" for yes.
func (f *fakeBlueZ) ask(member string, args ...any) string {
	f.mu.Lock()
	agent := f.agent
	f.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := f.conn.Object(agent, agentPath).CallWithContext(ctx, agentIface+"."+member, 0, args...).Err
	if errors.Is(err, context.DeadlineExceeded) {
		f.t.Fatal("agent did not answer " + member)
	}

	return sysbus.ErrorName(err)
}

// emit sends a signal from BlueZ.
func (f *fakeBlueZ) emit(path dbus.ObjectPath, name string, body ...any) {
	err := f.conn.Emit(path, name, body...)
	if err != nil {
		f.t.Fatal(err)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func start(t *testing.T, f *fakeBlueZ) (*Agent, *clock, context.Context) {
	t.Helper()
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	a := New(slog.New(slog.DiscardHandler), func() string { return "Keuken" })
	a.bus, a.now = f.bus.Addr, clk.now
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go a.Run(ctx)

	select {
	case <-f.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("agent never registered")
	}

	waitFor(t, a.registered)

	return a, clk, ctx
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()

	for range 500 {
		if ok() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("condition never held")
}

const a2dpSink = "0000110b-0000-1000-8000-00805f9b34fb"

func TestStartTrustsPairedClosesWindowAndNames(t *testing.T) {
	f := newFake(t,
		device{path: adapterPath + "/dev_A", paired: true},
		device{path: adapterPath + "/dev_B", paired: true, trusted: true},
		device{path: adapterPath + "/dev_C"})
	start(t, f)

	want := []string{
		"/org/bluez/hci0 Adapter1.Powered=true",
		"/org/bluez/hci0 Adapter1.Alias=Keuken",
		"/org/bluez/hci0/dev_A Device1.Trusted=true",
		"/org/bluez/hci0 Adapter1.Discoverable=false",
		"/org/bluez/hci0 Adapter1.Pairable=false",
		"/org/bluez/hci0 Adapter1.Connectable=true",
	}
	if got := f.takeSets(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes at start:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestStartWaitsForTheAdapter(t *testing.T) {
	f := newFake(t)
	f.mu.Lock()
	f.adapterLate = 3
	f.mu.Unlock()
	start(t, f)

	if got := f.takeSets(); len(got) == 0 || got[0] != "/org/bluez/hci0 Adapter1.Powered=true" {
		t.Fatalf("writes at start: %v, want the adapter powered once it appeared", got)
	}
}

func TestAgentSaysNoOutsideTheWindow(t *testing.T) {
	f := newFake(t)
	start(t, f)

	dev := adapterPath + "/dev_X"
	for _, q := range []struct {
		member string
		args   []any
	}{
		{"RequestAuthorization", []any{dev}},
		{"RequestConfirmation", []any{dev, uint32(123456)}},
		{"AuthorizeService", []any{dev, a2dpSink}},
		{"RequestPinCode", []any{dev}},
		{"RequestPasskey", []any{dev}},
	} {
		if got := f.ask(q.member, q.args...); got != "org.bluez.Error.Rejected" {
			t.Errorf("%s outside the window: %q, want Rejected", q.member, got)
		}
	}
	// What only informs the agent is acknowledged, in or out of the window.
	for _, q := range []struct {
		member string
		args   []any
	}{
		{"Cancel", nil},
		{"DisplayPasskey", []any{dev, uint32(123456), uint16(0)}},
	} {
		if got := f.ask(q.member, q.args...); got != "" {
			t.Errorf("%s: %q, want an empty answer", q.member, got)
		}
	}
	// A call with other arguments than Agent1's is refused, not taken as a yes.
	if got := f.ask("RequestAuthorization", "not a path"); got == "" {
		t.Error("RequestAuthorization with a string argument was answered yes")
	}
}

func TestWindowPairTrustAndClose(t *testing.T) {
	f := newFake(t)
	a, clk, ctx := start(t, f)
	f.takeSets()

	err := a.Pair(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if got := f.takeSets(); strings.Join(got, ",") != "/org/bluez/hci0 Adapter1.Pairable=true,/org/bluez/hci0 Adapter1.Discoverable=true" {
		t.Fatalf("open writes %v", got)
	}

	dev := adapterPath + "/dev_X"
	if got := f.ask("RequestAuthorization", dev); got != "" {
		t.Errorf("authorization in the window: %q", got)
	}

	if got := f.ask("RequestConfirmation", dev, uint32(123456)); got != "" {
		t.Errorf("confirmation in the window: %q", got)
	}

	if got := f.ask("AuthorizeService", dev, a2dpSink); got != "" {
		t.Errorf("A2DP in the window: %q", got)
	}

	if got := f.ask("AuthorizeService", dev, "00001105-0000-1000-8000-00805f9b34fb"); got != "org.bluez.Error.Rejected" {
		t.Errorf("OBEX push in the window: %q, want Rejected", got)
	}

	if got := f.ask("RequestPinCode", dev); got != "org.bluez.Error.Rejected" {
		t.Errorf("PIN code in the window: %q, want Rejected", got)
	}

	// The phone paired: it is trusted, and the window ends 15 s later.
	f.emit(dev, propsIface+".PropertiesChanged", deviceIface,
		map[string]dbus.Variant{"Paired": dbus.MakeVariant(true)}, []string{})
	waitFor(t, func() bool {
		return slices.Contains(f.takeSets(), "/org/bluez/hci0/dev_X Device1.Trusted=true")
	})
	clk.add(afterPair + time.Second)
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	a.tick(ctx, c)

	if got := f.takeSets(); len(got) < 3 || got[0] != "/org/bluez/hci0 Adapter1.Discoverable=false" {
		t.Fatalf("close writes %v", got)
	}

	if got := f.ask("RequestAuthorization", adapterPath+"/dev_Y"); got != "org.bluez.Error.Rejected" {
		t.Errorf("after the window: %q", got)
	}
}

// A device that pairs outside the window is not trusted: only the window's pairings are.
func TestPairedOutsideTheWindowIsNotTrusted(t *testing.T) {
	f := newFake(t)
	start(t, f)
	f.takeSets()
	f.emit(adapterPath+"/dev_X", propsIface+".PropertiesChanged", deviceIface,
		map[string]dbus.Variant{"Paired": dbus.MakeVariant(true)}, []string{})
	time.Sleep(200 * time.Millisecond)

	for _, s := range f.takeSets() {
		if strings.Contains(s, "dev_X") {
			t.Fatalf("a pairing outside the window was written to: %s", s)
		}
	}
}

func TestBlueZRestartReregisters(t *testing.T) {
	f := newFake(t)
	a, _, _ := start(t, f)
	_ = f.conn.Close() // bluetoothd leaves the bus
	// The session ends: Pair says Bluetooth is not running until BlueZ is back.
	waitFor(t, func() bool { return !a.registered() })

	err := a.Pair(context.Background())
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Pair while BlueZ is away = %v", err)
	}

	again := newFakeOn(t, f.bus)
	select {
	case <-again.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not register with the new bluetoothd")
	}

	waitFor(t, a.registered)

	if got := again.ask("RequestAuthorization", adapterPath+"/dev_X"); got != "org.bluez.Error.Rejected" {
		t.Errorf("the new bluetoothd's agent said %q outside the window", got)
	}
}

func TestClear(t *testing.T) {
	f := newFake(t, device{path: adapterPath + "/dev_A", paired: true}, device{path: adapterPath + "/dev_C"})
	a, _, ctx := start(t, f)

	n, err := a.Clear(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Clear = %d, %v", n, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.removed) != 1 || f.removed[0] != adapterPath+"/dev_A" {
		t.Fatalf("removed %v", f.removed)
	}
}

func TestPairWithoutBlueZ(t *testing.T) {
	a := New(slog.New(slog.DiscardHandler), func() string { return "" })

	err := a.Pair(context.Background())
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Pair without BlueZ = %v", err)
	}
}

func TestAudioProfile(t *testing.T) {
	for uuid, want := range map[string]bool{
		"0000110a-0000-1000-8000-00805f9b34fb": true, "0000110E-0000-1000-8000-00805F9B34FB": true,
		"0000110f-0000-1000-8000-00805f9b34fb": false, "00001105-0000-1000-8000-00805f9b34fb": false,
		"0000110b": false,
	} {
		if audioProfile(uuid) != want {
			t.Errorf("audioProfile(%s) != %v", uuid, want)
		}
	}
}
