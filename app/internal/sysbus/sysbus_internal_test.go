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

var errPlain = errors.New("plain")

// A bus that takes the connection and never answers the handshake must not hold a caller
// beyond the timeout.
func TestDialGivesUpOnASilentBus(t *testing.T) {
	t.Parallel()

	// A unix socket path must stay short: t.TempDir under a long test name exceeds it.
	dir, err := os.MkdirTemp("", "sb") //nolint:usetesting // t.TempDir makes the socket path too long
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	sock := filepath.Join(dir, "bus")

	listener, err := new(net.ListenConfig).Listen(context.Background(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		var held []net.Conn // held open, never answered

		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				for _, open := range held {
					_ = open.Close()
				}

				return
			}

			held = append(held, conn)
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
	t.Parallel()

	_, err := Dial(context.Background(), "unix:path=/nonexistent/bus", time.Second)
	if err == nil {
		t.Fatal("Dial to a missing socket succeeded")
	}
}

// Signals arrive in order; a reader that falls behind loses the newest, and calls still
// get their replies.
func TestSignalsInOrderAndBounded(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)

	ctx := t.Context()

	conn, err := Dial(ctx, bus.Addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	err = conn.AddMatchSignalContext(ctx, dbus.WithMatchInterface("org.example.T"))
	if err != nil {
		t.Fatal(err)
	}

	emitter := bus.Connect(t)
	for i := range signalBuffer + 10 {
		err = emitter.Emit("/t", "org.example.T.Tick", int32(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	// The emitter's call returns once the bus has routed every signal before it, and this
	// connection's call returns once its reader has handled them: the reply comes after them
	// on the socket, and must not wait for the signals' reader.
	cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
	defer ccancel()

	err = emitter.BusObject().CallWithContext(cctx, "org.freedesktop.DBus.NameHasOwner", 0, "x").Err
	if err != nil {
		t.Fatal(err)
	}

	if !conn.HasName(cctx, conn.Names()[0]) {
		t.Fatal("the bus does not know this connection")
	}

	for tick := range signalBuffer {
		select {
		case s := <-conn.Signals():
			if s.Body[0] != int32(tick) {
				t.Fatalf("signal %d carries %v", tick, s.Body[0])
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("signal %d never came", tick)
		}
	}

	select {
	case s := <-conn.Signals():
		t.Fatalf("a signal beyond the buffer was kept: %v", s.Body)
	case <-time.After(100 * time.Millisecond):
	}
}

// Closing the connection closes Done.
func TestDoneOnClose(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)

	conn, err := Dial(context.Background(), bus.Addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	_ = conn.Close()
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done open after Close")
	}
}

func TestErrorName(t *testing.T) {
	t.Parallel()

	bus := dbustest.New(t)

	conn, err := Dial(context.Background(), bus.Addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = conn.Call(context.Background(), "org.example.Nobody", "/", "org.example.X", "Y")
	if got := ErrorName(err); got != "org.freedesktop.DBus.Error.ServiceUnknown" {
		t.Fatalf("ErrorName = %q (%v)", got, err)
	}

	if ErrorName(errPlain) != "" || ErrorName(nil) != "" {
		t.Fatal("a plain error has a D-Bus name")
	}
}
