// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package sysbus opens private connections to the D-Bus system bus, through godbus
// (github.com/godbus/dbus/v5, ADR 0114). BlueZ and avahi are reached this way.
//
// Each connection is soundtapd's own (never godbus's shared one), so closing it, or the bus
// going away, ends exactly what was registered over it: an agent, an avahi entry group, a
// browser. Its signals arrive in order on one bounded channel; a reader that falls behind
// loses signals rather than stalling the replies to its calls.
package sysbus

import (
	"context"
	"errors"
	"time"

	"github.com/godbus/dbus/v5"
)

// Address is where the system bus listens on the speakers.
const Address = "unix:path=/run/dbus/system_bus_socket"

// signalBuffer bounds the signals waiting for the reader.
const signalBuffer = 32

// Conn is one private connection to a bus.
type Conn struct {
	*dbus.Conn

	signals chan *dbus.Signal
}

// Signals delivers the signals the connection's match rules let through, in the order
// the bus sent them. When the reader falls signalBuffer behind, new signals are dropped.
func (c *Conn) Signals() <-chan *dbus.Signal { return c.signals }

// Done is closed when the connection ends: Close, the bus hanging up, or the context
// Dial was given.
func (c *Conn) Done() <-chan struct{} { return c.Context().Done() }

// Call calls iface.member on dest's object at path and returns the reply's values.
func (c *Conn) Call(ctx context.Context, dest string, path dbus.ObjectPath, iface, member string, args ...any) ([]any, error) {
	call := c.Object(dest, path).CallWithContext(ctx, iface+"."+member, 0, args...)

	return call.Body, call.Err
}

// queue is the connection's signal handler: it runs on godbus's read goroutine, in
// order, and never blocks it.
type queue chan *dbus.Signal

func (q queue) DeliverSignal(_, _ string, s *dbus.Signal) {
	select {
	case q <- s:
	default:
	}
}

// ErrTimeout is Dial's error when the bus does not complete the handshake in time.
var ErrTimeout = errors.New("dbus: no answer from the bus")

// Dial connects to the bus at addr (a D-Bus address such as Address), authenticates and
// says Hello, all within timeout. The connection lives until Close, the bus hanging up, or
// ctx ending.
func Dial(ctx context.Context, addr string, timeout time.Duration) (*Conn, error) {
	life, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(timeout, cancel) // closes the connection a stalled handshake holds
	q := make(queue, signalBuffer)

	conn, err := dbus.Connect(addr, dbus.WithContext(life), dbus.WithSignalHandler(q))
	if !timer.Stop() {
		if conn != nil {
			_ = conn.Close()
		}

		cancel()

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, ErrTimeout
	}

	if err != nil {
		cancel()

		return nil, err
	}

	context.AfterFunc(conn.Context(), cancel) // a closed connection releases life

	return &Conn{Conn: conn, signals: q}, nil
}

// HasName reports whether name has an owner on the bus.
func (c *Conn) HasName(ctx context.Context, name string) bool {
	var has bool

	err := c.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, name).Store(&has)

	return err == nil && has
}

// ErrorName is the D-Bus error name of err, or "" when err is not a D-Bus error reply.
func ErrorName(err error) string {
	if e, ok := errors.AsType[dbus.Error](err); ok {
		return e.Name
	}

	var pe *dbus.Error
	if errors.As(err, &pe) && pe != nil {
		return pe.Name
	}

	return ""
}
