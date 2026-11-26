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
	fake := &fakeBlueZ{
		t: t, bus: bus, conn: bus.Connect(t),
		mu: sync.Mutex{}, sets: nil, removed: nil, devices: devs, agent: "", ready: make(chan struct{}),
		adapterLate: 0,
	}

	var once sync.Once
	// Properties answer on every object under /org/bluez. godbus looks a path up as an
	// export of its own before its parents' subtrees, so the adapter carries them too.
	props := map[string]any{
		"Set": func(msg dbus.Message, iface, name string, value dbus.Variant) *dbus.Error {
			path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
			if fake.adapterMissing(path) {
				return errUnknownObject
			}

			fake.mu.Lock()
			fake.sets = append(fake.sets,
				fmt.Sprintf("%s %s.%s=%v", path, strings.TrimPrefix(iface, "org.bluez."), name, value.Value()))
			fake.mu.Unlock()

			return nil
		},
		"Get": func(msg dbus.Message, _, _ string) (dbus.Variant, *dbus.Error) {
			if fake.adapterMissingOnGet(msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)) {
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
				fake.mu.Lock()
				fake.agent = string(from)
				fake.mu.Unlock()

				return nil
			},
			"RequestDefaultAgent": func(dbus.ObjectPath) *dbus.Error {
				once.Do(func() { close(fake.ready) })

				return nil
			},
		}, false},
		{"/", "org.freedesktop.DBus.ObjectManager", map[string]any{
			"GetManagedObjects": func() (managedObjects, *dbus.Error) { return fake.objects(), nil },
		}, false},
		{"/org/bluez", propsIface, props, true},
		{adapterPath, propsIface, props, true},
		{adapterPath, "org.bluez.Adapter1", map[string]any{
			"RemoveDevice": func(dev dbus.ObjectPath) *dbus.Error {
				fake.mu.Lock()
				fake.removed = append(fake.removed, dev)
				fake.mu.Unlock()

				return nil
			},
		}, true},
	}
	for _, exp := range exports {
		export := fake.conn.ExportMethodTable
		if exp.subtree {
			export = fake.conn.ExportSubtreeMethodTable
		}

		err := export(exp.methods, exp.path, exp.iface)
		if err != nil {
			t.Fatal(err)
		}
	}

	reply, err := fake.conn.RequestName(service, 0)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("RequestName(%s) = %v, %v", service, reply, err)
	}

	return fake
}

// adapterMissing reports whether a call on path finds no adapter yet.
func (f *fakeBlueZ) adapterMissing(path dbus.ObjectPath) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.adapterLate > 0 && strings.HasPrefix(string(path), string(adapterPath))
}

// adapterMissingOnGet is adapterMissing for a Get, which counts down.
func (f *fakeBlueZ) adapterMissingOnGet(path dbus.ObjectPath) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	late := f.adapterLate > 0 && strings.HasPrefix(string(path), string(adapterPath))
	if late {
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

func start(t *testing.T, fake *fakeBlueZ) (*Agent, *clock, context.Context) {
	t.Helper()
	clk := &clock{mu: sync.Mutex{}, t: time.Unix(1_800_000_000, 0)}
	agent := New(slog.New(slog.DiscardHandler), func() string { return "Keuken" })
	agent.bus, agent.now = fake.bus.Addr, clk.now
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go agent.Run(ctx)

	select {
	case <-fake.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("agent never registered")
	}

	waitFor(t, agent.registered)

	return agent, clk, ctx
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

const (
	a2dpSink     = "0000110b-0000-1000-8000-00805f9b34fb"
	rejectedName = "org.bluez.Error.Rejected"
)

func TestStartTrustsPairedClosesWindowAndNames(t *testing.T) {
	t.Parallel()

	fake := newFake(t,
		device{path: adapterPath + "/dev_A", paired: true, trusted: false},
		device{path: adapterPath + "/dev_B", paired: true, trusted: true},
		device{path: adapterPath + "/dev_C", paired: false, trusted: false})
	start(t, fake)

	want := []string{
		"/org/bluez/hci0 Adapter1.Powered=true",
		"/org/bluez/hci0 Adapter1.Alias=Keuken",
		"/org/bluez/hci0/dev_A Device1.Trusted=true",
		"/org/bluez/hci0 Adapter1.Discoverable=false",
		"/org/bluez/hci0 Adapter1.Pairable=false",
		"/org/bluez/hci0 Adapter1.Connectable=true",
	}
	if got := fake.takeSets(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes at start:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestStartWaitsForTheAdapter(t *testing.T) {
	t.Parallel()

	fake := newFake(t)
	fake.mu.Lock()
	fake.adapterLate = 3
	fake.mu.Unlock()
	start(t, fake)

	if got := fake.takeSets(); len(got) == 0 || got[0] != "/org/bluez/hci0 Adapter1.Powered=true" {
		t.Fatalf("writes at start: %v, want the adapter powered once it appeared", got)
	}
}

func TestAgentSaysNoOutsideTheWindow(t *testing.T) {
	t.Parallel()

	fake := newFake(t)
	start(t, fake)

	dev := adapterPath + "/dev_X"
	for _, call := range []struct {
		member string
		args   []any
	}{
		{"RequestAuthorization", []any{dev}},
		{"RequestConfirmation", []any{dev, uint32(123456)}},
		{"AuthorizeService", []any{dev, a2dpSink}},
		{"RequestPinCode", []any{dev}},
		{"RequestPasskey", []any{dev}},
	} {
		if got := fake.ask(call.member, call.args...); got != "org.bluez.Error.Rejected" {
			t.Errorf("%s outside the window: %q, want Rejected", call.member, got)
		}
	}
	// What only informs the agent is acknowledged, in or out of the window.
	for _, call := range []struct {
		member string
		args   []any
	}{
		{"Cancel", nil},
		{"DisplayPasskey", []any{dev, uint32(123456), uint16(0)}},
	} {
		if got := fake.ask(call.member, call.args...); got != "" {
			t.Errorf("%s: %q, want an empty answer", call.member, got)
		}
	}
	// A call with other arguments than Agent1's is refused, not taken as a yes.
	if got := fake.ask("RequestAuthorization", "not a path"); got == "" {
		t.Error("RequestAuthorization with a string argument was answered yes")
	}
}

func TestWindowPairTrustAndClose(t *testing.T) {
	t.Parallel()

	fake := newFake(t)
	agent, clk, ctx := start(t, fake)
	fake.takeSets()

	err := agent.Pair(ctx)
	if err != nil {
		t.Fatal(err)
	}

	got := fake.takeSets()
	if strings.Join(got, ",") != "/org/bluez/hci0 Adapter1.Pairable=true,/org/bluez/hci0 Adapter1.Discoverable=true" {
		t.Fatalf("open writes %v", got)
	}

	dev := adapterPath + "/dev_X"
	if got := fake.ask("RequestAuthorization", dev); got != "" {
		t.Errorf("authorization in the window: %q", got)
	}

	if got := fake.ask("RequestConfirmation", dev, uint32(123456)); got != "" {
		t.Errorf("confirmation in the window: %q", got)
	}

	if got := fake.ask("AuthorizeService", dev, a2dpSink); got != "" {
		t.Errorf("A2DP in the window: %q", got)
	}

	if got := fake.ask("AuthorizeService", dev, "00001105-0000-1000-8000-00805f9b34fb"); got != rejectedName {
		t.Errorf("OBEX push in the window: %q, want Rejected", got)
	}

	if got := fake.ask("RequestPinCode", dev); got != rejectedName {
		t.Errorf("PIN code in the window: %q, want Rejected", got)
	}

	// The phone paired: it is trusted, and the window ends 15 s later.
	fake.emit(dev, propsIface+".PropertiesChanged", deviceIface,
		map[string]dbus.Variant{"Paired": dbus.MakeVariant(true)}, []string{})
	waitFor(t, func() bool {
		return slices.Contains(fake.takeSets(), "/org/bluez/hci0/dev_X Device1.Trusted=true")
	})
	clk.add(afterPair + time.Second)
	agent.mu.Lock()
	conn := agent.conn
	agent.mu.Unlock()
	agent.tick(ctx, conn)

	if got := fake.takeSets(); len(got) < 3 || got[0] != "/org/bluez/hci0 Adapter1.Discoverable=false" {
		t.Fatalf("close writes %v", got)
	}

	if got := fake.ask("RequestAuthorization", adapterPath+"/dev_Y"); got != "org.bluez.Error.Rejected" {
		t.Errorf("after the window: %q", got)
	}
}

// A device that pairs outside the window is not trusted: only the window's pairings are.
func TestPairedOutsideTheWindowIsNotTrusted(t *testing.T) {
	t.Parallel()

	fake := newFake(t)
	start(t, fake)
	fake.takeSets()
	fake.emit(adapterPath+"/dev_X", propsIface+".PropertiesChanged", deviceIface,
		map[string]dbus.Variant{"Paired": dbus.MakeVariant(true)}, []string{})
	time.Sleep(200 * time.Millisecond)

	for _, s := range fake.takeSets() {
		if strings.Contains(s, "dev_X") {
			t.Fatalf("a pairing outside the window was written to: %s", s)
		}
	}
}

func TestBlueZRestartReregisters(t *testing.T) {
	t.Parallel()

	fake := newFake(t)
	agent, _, _ := start(t, fake)
	_ = fake.conn.Close() // bluetoothd leaves the bus
	// The session ends: Pair says Bluetooth is not running until BlueZ is back.
	waitFor(t, func() bool { return !agent.registered() })

	err := agent.Pair(context.Background())
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Pair while BlueZ is away = %v", err)
	}

	again := newFakeOn(t, fake.bus)
	select {
	case <-again.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not register with the new bluetoothd")
	}

	waitFor(t, agent.registered)

	if got := again.ask("RequestAuthorization", adapterPath+"/dev_X"); got != "org.bluez.Error.Rejected" {
		t.Errorf("the new bluetoothd's agent said %q outside the window", got)
	}
}

func TestClear(t *testing.T) {
	t.Parallel()

	fake := newFake(t,
		device{path: adapterPath + "/dev_A", paired: true, trusted: false},
		device{path: adapterPath + "/dev_C", paired: false, trusted: false})
	agent, _, ctx := start(t, fake)

	n, err := agent.Clear(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Clear = %d, %v", n, err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()

	if len(fake.removed) != 1 || fake.removed[0] != adapterPath+"/dev_A" {
		t.Fatalf("removed %v", fake.removed)
	}
}

func TestPairWithoutBlueZ(t *testing.T) {
	t.Parallel()

	agent := New(slog.New(slog.DiscardHandler), func() string { return "" })

	err := agent.Pair(context.Background())
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Pair without BlueZ = %v", err)
	}
}

func TestAudioProfile(t *testing.T) {
	t.Parallel()

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
