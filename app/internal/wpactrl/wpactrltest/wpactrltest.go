// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package wpactrltest stands in for wpa_supplicant's control socket in tests: a Unix
// datagram server that answers each command through a handler, remembers ATTACHed clients
// and sends them events, as wpa_supplicant's ctrl_iface_unix.c does.
package wpactrltest

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/wpactrl"
)

// readBuffer is the largest command read, as wpa_supplicant's own 4 KiB buffer.
const readBuffer = 4096

// Server is one fake control socket.
type Server struct {
	Path     string
	sock     *net.UnixConn
	handle   func(cmd string) string
	mu       sync.Mutex
	attached map[string]*net.UnixAddr
	cmds     []string
	done     chan struct{}
}

// ShortDir is a fresh directory with a short path, removed after the test: a Unix socket
// path has about 100 bytes, and t.TempDir's carry the test's name. It also points
// wpactrl.ClientDir there for the test.
func ShortDir(tb testing.TB) string {
	tb.Helper()

	dir, err := os.MkdirTemp("", "wpa") //nolint:usetesting // t.TempDir's path is too long for a socket
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() { _ = os.RemoveAll(dir) })

	old := wpactrl.ClientDir
	wpactrl.ClientDir = dir

	tb.Cleanup(func() { wpactrl.ClientDir = old })

	return dir
}

// Listen opens dir/iface and answers every command with handle until the test ends.
// ATTACH, DETACH and PING are answered here; handle sees every other command and returns
// the reply ("" reads as UNKNOWN COMMAND).
func Listen(tb testing.TB, dir, iface string, handle func(cmd string) string) *Server {
	tb.Helper()

	srv, err := Open(filepath.Join(dir, iface), handle)
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(srv.Close)

	return srv
}

// Open is Listen without a test: the caller closes it. A fake that wpa_supplicant's
// start creates, the way the real one appears only after it ran, uses this.
func Open(path string, handle func(cmd string) string) (*Server, error) {
	_ = os.Remove(path)

	sock, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, fmt.Errorf("wpactrltest: %w", err)
	}

	srv := &Server{
		Path:     path,
		sock:     sock,
		handle:   handle,
		mu:       sync.Mutex{},
		attached: map[string]*net.UnixAddr{},
		cmds:     nil,
		done:     make(chan struct{}),
	}
	safego.Go("wpactrltest-serve", srv.serve)

	return srv, nil
}

// Event sends "<3>msg" to every attached client.
func (s *Server) Event(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, to := range s.attached {
		_, err := s.sock.WriteToUnix([]byte("<3>"+msg), to)
		if err != nil {
			delete(s.attached, name) // gone, as wpa_supplicant drops a client it cannot reach
		}
	}
}

// Attached is how many clients take events.
func (s *Server) Attached() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.attached)
}

// Commands is every command received so far, in order.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.cmds...)
}

// Has reports whether a command starting with prefix arrived.
func (s *Server) Has(prefix string) bool {
	for _, c := range s.Commands() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}

	return false
}

// Close stops answering and removes the socket, as a wpa_supplicant that exits.
func (s *Server) Close() {
	_ = s.sock.Close()
	<-s.done
	_ = os.Remove(s.Path)
}

func (s *Server) serve() {
	defer close(s.done)

	buf := make([]byte, readBuffer)
	for {
		n, from, err := s.sock.ReadFromUnix(buf)
		if err != nil {
			return
		}

		cmd := string(buf[:n])

		s.mu.Lock()
		s.cmds = append(s.cmds, cmd)
		s.mu.Unlock()

		var reply string

		switch cmd {
		case "ATTACH":
			s.mu.Lock()
			s.attached[from.Name] = from
			s.mu.Unlock()

			reply = "OK\n"
		case "DETACH":
			s.mu.Lock()
			delete(s.attached, from.Name)
			s.mu.Unlock()

			reply = "OK\n"
		case "PING":
			reply = "PONG\n"
		default:
			if s.handle != nil {
				reply = s.handle(cmd)
			}

			if reply == "" {
				reply = "UNKNOWN COMMAND\n"
			}
		}

		if from != nil && from.Name != "" {
			_, _ = s.sock.WriteToUnix([]byte(reply), from)
		}
	}
}
