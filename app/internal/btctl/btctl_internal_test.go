// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package btctl

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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

var errNoBus = errors.New("no bus")

func adapter() map[string]props {
	return map[string]props{adapterIface: {
		"Name":         dbus.MakeVariant("soundtap"),
		"Alias":        dbus.MakeVariant("Zolder"),
		"Powered":      dbus.MakeVariant(true),
		"Discoverable": dbus.MakeVariant(true),
	}}
}

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
	fake := &fakeBlueZ{mu: sync.Mutex{}, removed: nil, calls: 0}

	err := conn.ExportMethodTable(map[string]any{
		"GetManagedObjects": func() (objects, *dbus.Error) {
			fake.mu.Lock()
			fake.calls++
			fake.mu.Unlock()

			return objs, nil
		},
	}, "/", "org.freedesktop.DBus.ObjectManager")
	if err != nil {
		t.Fatal(err)
	}

	err = conn.ExportSubtreeMethodTable(map[string]any{
		"RemoveDevice": func(msg dbus.Message, dev dbus.ObjectPath) *dbus.Error {
			fake.mu.Lock()
			adapter, _ := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
			fake.removed = append(fake.removed, string(adapter)+" "+string(dev))
			fake.mu.Unlock()

			return nil
		},
	}, "/org/bluez", adapterIface)
	if err != nil {
		t.Fatal(err)
	}

	_, err = conn.RequestName(service, 0)
	if err != nil {
		t.Fatal(err)
	}

	return func(ctx context.Context) (*sysbus.Conn, error) {
		return sysbus.Dial(ctx, bus.Addr, 5*time.Second)
	}, fake
}

func down(context.Context) (*sysbus.Conn, error) { return nil, errNoBus }

func TestReadReportsAdapterAndDevices(t *testing.T) {
	t.Parallel()

	bus, fake := serve(t, objects{
		adapterPath:                             adapter(),
		phonePath:                               device("60:F4:45:11:22:33", "Test iPhone", true, true),
		pixelPath:                               device("AA:BB:CC:DD:EE:FF", "Pixel 8", true, false),
		"/org/bluez/hci0/dev_11_22_33_44_55_66": device("11:22:33:44:55:66", "Seen only", false, false),
	})

	status := Read(context.Background(), bus)
	if !status.Adapter || !status.Powered || !status.Discoverable {
		t.Errorf("adapter=%v powered=%v discoverable=%v", status.Adapter, status.Powered, status.Discoverable)
	}
	// The alias, not the Name: the alias is what the agent sets and what a phone shows.
	if status.Alias != "Zolder" {
		t.Errorf("alias = %q", status.Alias)
	}

	want := []Device{
		{Address: "60:F4:45:11:22:33", Name: "Test iPhone", Connected: true, Path: phonePath},
		{Address: "AA:BB:CC:DD:EE:FF", Name: "Pixel 8", Connected: false, Path: pixelPath},
	}
	if !reflect.DeepEqual(status.Devices, want) {
		t.Errorf("devices = %#v", status.Devices)
	}

	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()

	if calls != 1 {
		t.Errorf("GetManagedObjects called %d times", calls)
	}
}

// A speaker whose radio never came up must answer, not fail: the UI shows "no
// Bluetooth" rather than an error, and the WLA spent a day in exactly that state.
func TestReadWithoutAnAdapterReportsNoBluetooth(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	bus, _ := serve(t, objects{
		adapterPath: adapter(),
		phonePath:   device("60:F4:45:11:22:33", "Connected: yes\" boolean true", true, false),
	})

	st := Read(context.Background(), bus)
	if len(st.Devices) != 1 || st.Devices[0].Connected || st.Devices[0].Name != "Connected: yes\" boolean true" {
		t.Fatalf("devices = %#v", st.Devices)
	}
}

// An adapter with nothing paired keeps the adapter and lists no devices. The list is
// "devices": [], never null: the settings page reads its length and failed on null.
func TestReadWithNoPairingsKeepsTheAdapter(t *testing.T) {
	t.Parallel()

	bus, _ := serve(t, objects{adapterPath: adapter()})

	st := Read(context.Background(), bus)
	if !st.Adapter || len(st.Devices) != 0 {
		t.Fatalf("status = %#v", st)
	}

	for _, s := range []Status{st, Read(context.Background(), down)} {
		encoded, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(string(encoded), `"devices":[]`) {
			t.Errorf("encoded %s, want \"devices\":[]", encoded)
		}
	}
}

func TestPairedSkipsBadAddressesAndFailures(t *testing.T) {
	t.Parallel()

	bus, _ := serve(t, objects{
		adapterPath: adapter(),
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
	t.Parallel()

	dev := device("60:F4:45:11:22:33", "", true, false)
	dev[deviceIface]["Name"] = dbus.MakeVariant("Phone")

	bus, _ := serve(t, objects{adapterPath: adapter(), phonePath: dev})
	if got := Paired(context.Background(), bus); len(got) != 1 || got[0].Name != "Phone" {
		t.Fatalf("paired = %v", got)
	}
}

func TestRemoveCallsTheOwningAdapter(t *testing.T) {
	t.Parallel()

	bus, fake := serve(t, objects{adapterPath: adapter()})

	err := Remove(context.Background(), bus, pathOnly(phonePath))
	if err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	removed := fake.removed
	fake.mu.Unlock()

	if want := []string{adapterPath + " " + phonePath}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed = %v, want %v", removed, want)
	}

	for _, p := range []string{"/etc/passwd", "/org/bluez/hci0/dev_x/../../x", "/org/bluez/"} {
		err = Remove(context.Background(), bus, pathOnly(p))
		if err == nil {
			t.Errorf("%q was accepted", p)
		}
	}

	err = Remove(context.Background(), down, pathOnly(phonePath))
	if err == nil {
		t.Error("a missing bus was not reported")
	}
}

func TestForgetRemovesOnlyThatPairedDevice(t *testing.T) {
	t.Parallel()

	bus, fake := serve(t, objects{
		adapterPath:                             adapter(),
		phonePath:                               device("60:F4:45:11:22:33", "Test iPhone", true, true),
		pixelPath:                               device("AA:BB:CC:DD:EE:FF", "Pixel 8", true, false),
		"/org/bluez/hci0/dev_11_22_33_44_55_66": device("11:22:33:44:55:66", "Seen only", false, false),
	})
	// The address as a page may send it: any case.
	connected, err := Forget(context.Background(), bus, "aa:bb:cc:dd:ee:ff")
	if err != nil || connected {
		t.Fatalf("Forget(pixel) = %v, %v; want not connected, nil", connected, err)
	}

	connected, err = Forget(context.Background(), bus, "60:F4:45:11:22:33")
	if err != nil || !connected {
		t.Fatalf("Forget(phone) = %v, %v; want connected, nil", connected, err)
	}
	// Seen but never paired, or unknown: nothing to forget.
	for _, addr := range []string{"11:22:33:44:55:66", "00:00:00:00:00:00", ""} {
		_, err = Forget(context.Background(), bus, addr)
		if !errors.Is(err, ErrNotPaired) {
			t.Errorf("Forget(%q) = %v, want ErrNotPaired", addr, err)
		}
	}

	fake.mu.Lock()
	removed := fake.removed
	fake.mu.Unlock()

	want := []string{adapterPath + " " + pixelPath, adapterPath + " " + phonePath}
	if !reflect.DeepEqual(removed, want) {
		t.Errorf("removed = %v, want %v", removed, want)
	}

	_, err = Forget(context.Background(), down, "60:F4:45:11:22:33")
	if err == nil || errors.Is(err, ErrNotPaired) {
		t.Errorf("a missing bus gave %v", err)
	}
}

// IsPaired answers for a paired device only, in any case, and removes nothing.
func TestIsPairedChangesNothing(t *testing.T) {
	t.Parallel()

	bus, fake := serve(t, objects{
		adapterPath:                             adapter(),
		phonePath:                               device("60:F4:45:11:22:33", "Test iPhone", true, true),
		"/org/bluez/hci0/dev_11_22_33_44_55_66": device("11:22:33:44:55:66", "Seen only", false, false),
	})
	for addr, want := range map[string]bool{
		"60:f4:45:11:22:33": true, "11:22:33:44:55:66": false, "00:00:00:00:00:00": false,
	} {
		got, err := IsPaired(context.Background(), bus, addr)
		if err != nil || got != want {
			t.Errorf("IsPaired(%q) = %v, %v; want %v", addr, got, err, want)
		}
	}

	fake.mu.Lock()
	removed := fake.removed
	fake.mu.Unlock()

	if len(removed) != 0 {
		t.Errorf("IsPaired removed %v", removed)
	}

	_, err := IsPaired(context.Background(), down, "60:F4:45:11:22:33")
	if err == nil {
		t.Error("a missing bus gave no error")
	}
}

// pathOnly is a device known by its object path alone.
func pathOnly(objectPath string) Device {
	return Device{Address: "", Name: "", Connected: false, Path: objectPath}
}
