// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package snapcast talks to snapserver's newline-delimited JSON-RPC control port.
package snapcast

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	address          = "127.0.0.1:1705"
	dialTimeout      = 2 * time.Second
	writeTimeout     = 2 * time.Second
	readTimeout      = 4 * time.Second
	maxResponseSize  = 1 << 20
	maxNotifications = 16
)

type Volume struct {
	Percent int  `json:"percent"`
	Muted   bool `json:"muted"`
}

type ClientInfo struct {
	ID        string `json:"id"`
	Connected bool   `json:"connected"`
	Host      struct {
		Name string `json:"name"`
		IP   string `json:"ip"`
	} `json:"host"`
	Config struct {
		Name string `json:"name"`
		// Latency is the per-client playback offset in milliseconds. Snapcast keeps
		// clients in sync by delaying everyone to the slowest; a client with a large
		// offset is the one dragging the group.
		Latency int    `json:"latency"`
		Volume  Volume `json:"volume"`
	} `json:"config"`
	// LastSeen is when the server last heard from the client. For a disconnected
	// client it is how long ago it vanished, which is the difference between "still
	// rebooting" and "gone since yesterday".
	LastSeen struct {
		Sec  int64 `json:"sec"`
		Usec int64 `json:"usec"`
	} `json:"lastSeen"`
}

type Group struct {
	ID      string       `json:"id"`
	Clients []ClientInfo `json:"clients"`
}

type Status struct {
	Groups []Group `json:"groups"`
}

type Client struct {
	addr string
}

func New() *Client { return &Client{addr: address} }

func (c *Client) GetStatus(ctx context.Context) (Status, error) {
	var result struct {
		Server Status `json:"server"`
	}
	if err := c.call(ctx, "Server.GetStatus", nil, &result); err != nil {
		return Status{}, err
	}
	return result.Server, nil
}

func (c *Client) SetVolume(ctx context.Context, id string, volume Volume) error {
	return c.call(ctx, "Client.SetVolume", map[string]any{"id": id, "volume": volume}, nil)
}

// SetLatency sets how many milliseconds of its own output chain a client must play ahead
// of; snapserver passes it to the client, which applies it without reconnecting.
func (c *Client) SetLatency(ctx context.Context, id string, ms int) error {
	return c.call(ctx, "Client.SetLatency", map[string]any{"id": id, "latency": ms}, nil)
}

func (c *Client) SetClients(ctx context.Context, groupID string, clients []string) error {
	return c.call(ctx, "Group.SetClients", map[string]any{"id": groupID, "clients": clients}, nil)
}

func (c *Client) call(ctx context.Context, method string, params any, result any) (err error) {
	req, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", 1, method, params})
	if err != nil {
		return err
	}
	req = append(req, '\n')

	addr := c.addr
	if addr == "" {
		addr = address
	}
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("snapcast dial: %w", err)
	}
	defer conn.Close()
	// The socket deadlines only bound a context deadline known up front; closing the
	// connection is what makes a later cancellation end the call, and a reply that
	// arrives after it does not count.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
	}()

	if err := conn.SetWriteDeadline(deadline(ctx, writeTimeout)); err != nil {
		return err
	}
	for len(req) > 0 {
		n, err := conn.Write(req)
		if err != nil {
			return fmt.Errorf("snapcast write: %w", err)
		}
		if n == 0 {
			return errors.New("snapcast write: no progress")
		}
		req = req[n:]
	}
	if err := conn.SetReadDeadline(deadline(ctx, readTimeout)); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	for range maxNotifications + 1 {
		line, err := readLine(reader)
		if err != nil {
			return fmt.Errorf("snapcast read: %w", err)
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			return fmt.Errorf("snapcast response: %w", err)
		}
		if resp.JSONRPC != "2.0" {
			return errors.New("snapcast response has invalid version")
		}
		if resp.ID != 1 {
			// Snapserver sends asynchronous notifications on this connection.
			if resp.ID == 0 && resp.Error == nil && len(resp.Result) == 0 {
				continue
			}
			return errors.New("snapcast response has invalid id")
		}
		if resp.Error != nil {
			return fmt.Errorf("snapcast rpc %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if result != nil {
			if len(resp.Result) == 0 {
				return errors.New("snapcast response has no result")
			}
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("snapcast result: %w", err)
			}
		}
		return nil
	}
	return errors.New("snapcast response has too many notifications")
}

func deadline(ctx context.Context, timeout time.Duration) time.Time {
	d := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(d) {
		return ctxDeadline
	}
	return d
}

func readLine(r io.Reader) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 1)
	for {
		n, err := r.Read(chunk)
		for i, b := range chunk[:n] {
			if b == '\n' {
				if len(buf)+i > maxResponseSize {
					return nil, errors.New("response too large")
				}
				return append(buf, chunk[:i]...), nil
			}
		}
		if len(buf)+n > maxResponseSize {
			return nil, errors.New("response too large")
		}
		buf = append(buf, chunk[:n]...)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("response missing newline")
			}
			return nil, err
		}
	}
}
