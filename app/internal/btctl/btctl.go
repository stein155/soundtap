// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package btctl reports Bluetooth pairings and removes one at a time.
//
// It asks BlueZ over D-Bus (godbus, ADR 0114) instead of parsing bluetoothctl's text: one
// GetManagedObjects call returns the adapter and every device with typed properties, so a
// name a phone chose can never pose as a property. The pairing agent, the pairing window
// and clearing every pairing are internal/bluez.
package btctl

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"path"
	"slices"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/sysbus"
)

// Bus opens a connection to the bus BlueZ is on, for one call; the caller closes it.
// soundtapd passes the system bus, tests a test bus.
type Bus func(ctx context.Context) (*sysbus.Conn, error)

// Device is one remote Bluetooth device, as BlueZ reports it.
type Device struct {
	Address   string `json:"address"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Path      string `json:"-"` // D-Bus object path, for Remove
}

// Status is what the speaker can say about its own Bluetooth without touching a phone.
type Status struct {
	Adapter      bool     `json:"adapter"`
	Alias        string   `json:"alias"`
	Powered      bool     `json:"powered"`
	Discoverable bool     `json:"discoverable"`
	Devices      []Device `json:"devices"`
}

const (
	service      = "org.bluez"
	adapterIface = "org.bluez.Adapter1"
	deviceIface  = "org.bluez.Device1"
)

// objects is GetManagedObjects' answer: per object path, per interface, its properties.
type objects = map[dbus.ObjectPath]map[string]map[string]dbus.Variant

func managed(ctx context.Context, bus Bus) (objects, error) {
	conn, err := bus(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	var objs objects

	err = conn.Object(service, "/").
		CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objs)
	if err != nil {
		return objs, fmt.Errorf("bluez objects: %w", err)
	}

	return objs, nil
}

// each calls f, in path order, for every object with iface.
func each(objs objects, iface string, f func(objectPath dbus.ObjectPath, props map[string]dbus.Variant)) {
	for _, p := range slices.Sorted(maps.Keys(objs)) {
		if props, ok := objs[p][iface]; ok {
			f(p, props)
		}
	}
}

func str(props map[string]dbus.Variant, key string) string {
	s, _ := props[key].Value().(string)

	return s
}

func flag(props map[string]dbus.Variant, key string) bool {
	b, _ := props[key].Value().(bool)

	return b
}

// Read asks BlueZ what the adapter is doing and which devices it remembers.
// It does not return an error, because every way this can fail means the same thing
// to a caller: this speaker has no Bluetooth it can report on. A missing bus, a dead
// bluetoothd, a board whose radio never came up and a build with the transport left
// out are all Adapter false.
func Read(ctx context.Context, bus Bus) Status {
	var status Status

	status.Devices = []Device{} // "devices": [] without an adapter too; the page reads its length

	objs, err := managed(ctx, bus)
	if err != nil {
		return status
	}

	each(objs, adapterIface, func(_ dbus.ObjectPath, props map[string]dbus.Variant) {
		if !status.Adapter {
			status.Adapter = true
			status.Alias = str(props, "Alias")
			status.Powered = flag(props, "Powered")
			status.Discoverable = flag(props, "Discoverable")
		}
	})

	if status.Adapter {
		status.Devices = pairedOf(objs)
	}

	return status
}

// Paired lists the paired devices; nil when BlueZ cannot be asked.
func Paired(ctx context.Context, bus Bus) []Device {
	objs, err := managed(ctx, bus)
	if err != nil {
		return nil
	}

	return pairedOf(objs)
}

// pairedOf is never nil: nothing paired encodes as "devices": [], not null.
func pairedOf(objs objects) []Device {
	devs := []Device{}

	each(objs, deviceIface, func(objectPath dbus.ObjectPath, props map[string]dbus.Variant) {
		if !flag(props, "Paired") {
			return
		}

		addr := str(props, "Address")

		_, err := net.ParseMAC(addr)
		if err != nil {
			return
		}

		name := str(props, "Alias")
		if name == "" {
			name = str(props, "Name")
		}

		devs = append(devs, Device{
			Address: addr, Name: name, Connected: flag(props, "Connected"), Path: string(objectPath),
		})
	})

	return devs
}

// errNotBlueZPath: Remove refuses a path outside BlueZ's object tree.
var errNotBlueZPath = errors.New("not a BlueZ object path")

// Remove forgets one device: Adapter1.RemoveDevice on the adapter that owns it.
func Remove(ctx context.Context, bus Bus, dev Device) error {
	if !strings.HasPrefix(dev.Path, "/org/bluez/") || !dbus.ObjectPath(dev.Path).IsValid() {
		return fmt.Errorf("%w: %s", errNotBlueZPath, dev.Path)
	}

	conn, err := bus(ctx)
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()

	adapter := dbus.ObjectPath(path.Dir(dev.Path))

	_, err = conn.Call(ctx, service, adapter, adapterIface, "RemoveDevice", dbus.ObjectPath(dev.Path))
	if err != nil {
		return fmt.Errorf("remove %s: %w", dev.Path, err)
	}

	return nil
}

// ErrNotPaired is Forget's answer for an address BlueZ holds no pairing for.
var ErrNotPaired = errors.New("no paired device with that address")

// IsPaired says whether BlueZ holds a pairing for address, without changing anything:
// POST /api/bluetooth/forget asks it before it waits for the press on the speaker.
func IsPaired(ctx context.Context, bus Bus, address string) (bool, error) {
	objs, err := managed(ctx, bus)
	if err != nil {
		return false, err
	}

	for _, d := range pairedOf(objs) {
		if strings.EqualFold(d.Address, address) {
			return true, nil
		}
	}

	return false, nil
}

// Forget forgets the one paired device with this address (POST /api/bluetooth/forget), as
// stock's app forgot a single phone. It reports whether that device was connected, so the
// caller can end a stream that just lost its source.
func Forget(ctx context.Context, bus Bus, address string) (bool, error) {
	objs, err := managed(ctx, bus)
	if err != nil {
		return false, err
	}

	for _, d := range pairedOf(objs) {
		if strings.EqualFold(d.Address, address) {
			return d.Connected, Remove(ctx, bus, d)
		}
	}

	return false, ErrNotPaired
}
