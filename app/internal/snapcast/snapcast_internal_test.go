// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package snapcast

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

// statusReply is snapserver's answer to Server.GetStatus with one group of one client.
const statusReply = `{"jsonrpc":"2.0","id":1,"result":{"server":{"groups":[{"id":"g1","clients":[` +
	`{"id":"c1","connected":true,"host":{"name":"Kitchen","ip":"10.0.0.2"},` +
	`"config":{"name":"","volume":{"percent":37,"muted":true}}}]}]}}}` + "\n"

func TestRPCMethods(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	type request struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      int            `json:"id"`
		Method  string         `json:"method"`
		Params  map[string]any `json:"params"`
	}

	requests := make(chan request, 3)

	go func() {
		for range 3 {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			var req request

			line, readErr := bufio.NewReader(conn).ReadBytes('\n')
			if readErr == nil {
				readErr = json.Unmarshal(line, &req)
			}

			if readErr == nil {
				requests <- req
			}

			if req.Method == "Server.GetStatus" {
				_, _ = conn.Write([]byte(statusReply))
			} else {
				_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))
			}

			_ = conn.Close()
		}
	}()

	client := &Client{addr: listener.Addr().String()}

	status, err := client.GetStatus(context.Background())
	if err != nil || len(status.Groups) != 1 || status.Groups[0].Clients[0].Config.Volume.Percent != 37 {
		t.Fatalf("GetStatus = %+v, %v", status, err)
	}

	err = client.SetVolume(context.Background(), "c1", Volume{Percent: 42, Muted: false})
	if err != nil {
		t.Fatal(err)
	}

	err = client.SetClients(context.Background(), "g1", []string{"c1", "c2"})
	if err != nil {
		t.Fatal(err)
	}

	get, volume, clients := <-requests, <-requests, <-requests
	if get.JSONRPC != "2.0" || get.ID != 1 || get.Method != "Server.GetStatus" {
		t.Fatalf("status request = %+v", get)
	}

	gotVolume := volume.Params["volume"].(map[string]any)
	if volume.Method != "Client.SetVolume" || volume.Params["id"] != "c1" ||
		gotVolume["percent"] != float64(42) || gotVolume["muted"] != any(false) {
		t.Fatalf("volume request = %+v", volume)
	}

	gotClients := clients.Params["clients"].([]any)
	if clients.Method != "Group.SetClients" || clients.Params["id"] != "g1" ||
		len(gotClients) != 2 || gotClients[1] != "c2" {
		t.Fatalf("clients request = %+v", clients)
	}
}

func TestReadLineBound(t *testing.T) {
	t.Parallel()

	_, err := readLine(bytes.NewReader(append(bytes.Repeat([]byte{'x'}, maxResponseSize+1), '\n')))
	if err == nil {
		t.Fatal("oversize response accepted")
	}

	_, err = readLine(bytes.NewReader([]byte(`{}`)))
	if err == nil {
		t.Fatal("response without newline accepted")
	}
}

func TestRPCIgnoresNotificationBeforeResponse(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()

		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","method":"Server.OnUpdate","params":{}}` + "\n"))
		_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"server":{"groups":[]}}}` + "\n"))
	}()

	status, err := (&Client{addr: listener.Addr().String()}).GetStatus(context.Background())
	if err != nil || len(status.Groups) != 0 {
		t.Fatalf("GetStatus = %+v, %v", status, err)
	}
}

// Cancelling the caller's context ends a call whose reply is still outstanding.
func TestRPCReturnsOnCancellation(t *testing.T) {
	t.Parallel()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	read := make(chan struct{})

	release := make(chan struct{})
	defer close(release)

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()

		_, _ = bufio.NewReader(conn).ReadBytes('\n')

		close(read)
		<-release // never answers
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, statusErr := (&Client{addr: listener.Addr().String()}).GetStatus(ctx)
		done <- statusErr
	}()

	<-read
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("GetStatus still blocked after cancellation")
	}
}
