// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package sysbus

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/dbustest"
)

// A bus that takes the connection and never answers the handshake must not hold a caller
// beyond the timeout.
func TestDialGivesUpOnASilentBus(t *testing.T) {
	dir, err := os.MkdirTemp("", "sb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "bus")
	ln, err := new(net.ListenConfig).Listen(context.Background(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // held open, never answered
		}
	}()
	start := time.Now()
	_, err = Dial(context.Background(), "unix:path="+sock, 200*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("Dial = %v, want ErrTimeout", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("Dial took %s", took)
	}
}

func TestDialFailsWithoutABus(t *testing.T) {
	if _, err := Dial(context.Background(), "unix:path=/nonexistent/bus", time.Second); err == nil {
		t.Fatal("Dial to a missing socket succeeded")
	}
}

// Signals arrive in order; a reader that falls behind loses the newest, and calls still
// get their replies.
func TestSignalsInOrderAndBounded(t *testing.T) {
	bus := dbustest.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := Dial(ctx, bus.Addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.AddMatchSignalContext(ctx, dbus.WithMatchInterface("org.example.T")); err != nil {
		t.Fatal(err)
	}
	emitter := bus.Connect(t)
	for i := range signalBuffer + 10 {
		if err := emitter.Emit("/t", "org.example.T.Tick", int32(i)); err != nil {
			t.Fatal(err)
		}
	}
	// The emitter's call returns once the bus has routed every signal before it, and this
	// connection's call returns once its reader has handled them: the reply comes after them
	// on the socket, and must not wait for the signals' reader.
	cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
	defer ccancel()
	if err := emitter.BusObject().CallWithContext(cctx, "org.freedesktop.DBus.NameHasOwner", 0, "x").Err; err != nil {
		t.Fatal(err)
	}
	if !c.HasName(cctx, c.Names()[0]) {
		t.Fatal("the bus does not know this connection")
	}
	for i := range signalBuffer {
		select {
		case s := <-c.Signals():
			if s.Body[0] != int32(i) {
				t.Fatalf("signal %d carries %v", i, s.Body[0])
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("signal %d never came", i)
		}
	}
	select {
	case s := <-c.Signals():
		t.Fatalf("a signal beyond the buffer was kept: %v", s.Body)
	case <-time.After(100 * time.Millisecond):
	}
}

// Closing the connection closes Done.
func TestDoneOnClose(t *testing.T) {
	bus := dbustest.New(t)
	c, err := Dial(context.Background(), bus.Addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done open after Close")
	}
}

func TestErrorName(t *testing.T) {
	bus := dbustest.New(t)
	c, err := Dial(context.Background(), bus.Addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Call(context.Background(), "org.example.Nobody", "/", "org.example.X", "Y")
	if got := ErrorName(err); got != "org.freedesktop.DBus.Error.ServiceUnknown" {
		t.Fatalf("ErrorName = %q (%v)", got, err)
	}
	if ErrorName(errors.New("plain")) != "" || ErrorName(nil) != "" {
		t.Fatal("a plain error has a D-Bus name")
	}
}
