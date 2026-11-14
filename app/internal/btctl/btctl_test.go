// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package btctl

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/dbustest"
	"github.com/stein155/soundtap/app/internal/sysbus"
)

const (
	adapterPath = "/org/bluez/hci0"
	phonePath   = "/org/bluez/hci0/dev_60_F4_45_11_22_33"
	pixelPath   = "/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF"
)

type props = map[string]dbus.Variant

func device(addr, alias string, paired, connected bool) map[string]props {
	return map[string]props{
		deviceIface: {
			"Address":   dbus.MakeVariant(addr),
			"Alias":     dbus.MakeVariant(alias),
			"Paired":    dbus.MakeVariant(paired),
			"Connected": dbus.MakeVariant(connected),
			"UUIDs":     dbus.MakeVariant([]string{"0000110b-0000-1000-8000-00805f9b34fb"}),
		},
		"org.freedesktop.DBus.Properties": {},
	}
}

var adapter = map[string]props{adapterIface: {
	"Name":         dbus.MakeVariant("soundtap"),
	"Alias":        dbus.MakeVariant("Zolder"),
	"Powered":      dbus.MakeVariant(true),
	"Discoverable": dbus.MakeVariant(true),
}}

// fakeBlueZ serves objs on a test bus and records RemoveDevice calls.
type fakeBlueZ struct {
	mu      sync.Mutex
	removed []string // "adapter device"
	calls   int
}

func serve(t *testing.T, objs objects) (Bus, *fakeBlueZ) {
	t.Helper()
	bus := dbustest.New(t)
	conn := bus.Connect(t)
	f := &fakeBlueZ{}
	if err := conn.ExportMethodTable(map[string]any{
		"GetManagedObjects": func() (objects, *dbus.Error) {
			f.mu.Lock()
			f.calls++
			f.mu.Unlock()
			return objs, nil
		},
	}, "/", "org.freedesktop.DBus.ObjectManager"); err != nil {
		t.Fatal(err)
	}
	if err := conn.ExportSubtreeMethodTable(map[string]any{
		"RemoveDevice": func(m dbus.Message, d dbus.ObjectPath) *dbus.Error {
			f.mu.Lock()
			f.removed = append(f.removed, string(m.Headers[dbus.FieldPath].Value().(dbus.ObjectPath))+" "+string(d))
			f.mu.Unlock()
			return nil
		},
	}, "/org/bluez", adapterIface); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(service, 0); err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context) (*sysbus.Conn, error) {
		return sysbus.Dial(ctx, bus.Addr, 5*time.Second)
	}, f
}

func down(context.Context) (*sysbus.Conn, error) { return nil, errors.New("no bus") }

func TestReadReportsAdapterAndDevices(t *testing.T) {
	bus, f := serve(t, objects{
		adapterPath:                             adapter,
		phonePath:                               device("60:F4:45:11:22:33", "Test iPhone", true, true),
		pixelPath:                               device("AA:BB:CC:DD:EE:FF", "Pixel 8", true, false),
		"/org/bluez/hci0/dev_11_22_33_44_55_66": device("11:22:33:44:55:66", "Seen only", false, false),
	})
	st := Read(context.Background(), bus)
	if !st.Adapter || !st.Powered || !st.Discoverable {
		t.Errorf("adapter=%v powered=%v discoverable=%v", st.Adapter, st.Powered, st.Discoverable)
	}
	// The alias, not the Name: the alias is what the agent sets and what a phone shows.
	if st.Alias != "Zolder" {
		t.Errorf("alias = %q", st.Alias)
	}
	want := []Device{
		{Address: "60:F4:45:11:22:33", Name: "Test iPhone", Connected: true, Path: phonePath},
		{Address: "AA:BB:CC:DD:EE:FF", Name: "Pixel 8", Path: pixelPath},
	}
	if !reflect.DeepEqual(st.Devices, want) {
		t.Errorf("devices = %#v", st.Devices)
	}
	if f.calls != 1 {
		t.Errorf("GetManagedObjects called %d times", f.calls)
	}
}

// A speaker whose radio never came up must answer, not fail: the UI shows "no
// Bluetooth" rather than an error, and the WLA spent a day in exactly that state.
func TestReadWithoutAnAdapterReportsNoBluetooth(t *testing.T) {
	if st := Read(context.Background(), down); st.Adapter {
		t.Error("adapter reported present")
	}
	// bluetoothd up but no controller: the object tree holds no Adapter1.
	bus, _ := serve(t, objects{})
	if st := Read(context.Background(), bus); st.Adapter {
		t.Error("an empty object tree was read as an adapter")
	}
	// No bluetoothd on the bus at all.
	empty := dbustest.New(t)
	none := func(ctx context.Context) (*sysbus.Conn, error) { return sysbus.Dial(ctx, empty.Addr, 5*time.Second) }
	if st := Read(context.Background(), none); st.Adapter {
		t.Error("a bus without BlueZ was read as an adapter")
	}
}

// A phone chooses its own name, so a name may contain anything; the properties are typed
// values, so a device cannot decide its own state.
func TestConnectedStateCannotBeFakedByADeviceName(t *testing.T) {
	bus, _ := serve(t, objects{adapterPath: adapter, phonePath: device("60:F4:45:11:22:33", "Connected: yes\" boolean true", true, false)})
	st := Read(context.Background(), bus)
	if len(st.Devices) != 1 || st.Devices[0].Connected || st.Devices[0].Name != "Connected: yes\" boolean true" {
		t.Fatalf("devices = %#v", st.Devices)
	}
}

// An adapter with nothing paired keeps the adapter and lists no devices.
func TestReadWithNoPairingsKeepsTheAdapter(t *testing.T) {
	bus, _ := serve(t, objects{adapterPath: adapter})
	if st := Read(context.Background(), bus); !st.Adapter || len(st.Devices) != 0 {
		t.Fatalf("status = %#v", st)
	}
}

func TestPairedSkipsBadAddressesAndFailures(t *testing.T) {
	bus, _ := serve(t, objects{
		adapterPath: adapter,
		phonePath:   device("60:F4:45:11:22:33", "Phone", true, false),
		pixelPath:   device("nope", "Bad", true, false),
	})
	got := Paired(context.Background(), bus)
	if len(got) != 1 || got[0].Address != "60:F4:45:11:22:33" {
		t.Fatalf("paired = %v", got)
	}
	if Paired(context.Background(), down) != nil {
		t.Error("a failed call returned devices")
	}
}

// Without an alias the device's own name is shown.
func TestPairedFallsBackToTheName(t *testing.T) {
	dev := device("60:F4:45:11:22:33", "", true, false)
	dev[deviceIface]["Name"] = dbus.MakeVariant("Phone")
	bus, _ := serve(t, objects{adapterPath: adapter, phonePath: dev})
	if got := Paired(context.Background(), bus); len(got) != 1 || got[0].Name != "Phone" {
		t.Fatalf("paired = %v", got)
	}
}

func TestRemoveCallsTheOwningAdapter(t *testing.T) {
	bus, f := serve(t, objects{adapterPath: adapter})
	if err := Remove(context.Background(), bus, Device{Path: phonePath}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	removed := f.removed
	f.mu.Unlock()
	if want := []string{adapterPath + " " + phonePath}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed = %v, want %v", removed, want)
	}
	for _, p := range []string{"/etc/passwd", "/org/bluez/hci0/dev_x/../../x", "/org/bluez/"} {
		if err := Remove(context.Background(), bus, Device{Path: p}); err == nil {
			t.Errorf("%q was accepted", p)
		}
	}
	if err := Remove(context.Background(), down, Device{Path: phonePath}); err == nil {
		t.Error("a missing bus was not reported")
	}
}
