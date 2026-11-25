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
	ID        string       `json:"id"`
	Connected bool         `json:"connected"`
	Host      clientHost   `json:"host"`
	Config    clientConfig `json:"config"`
	// LastSeen is when the server last heard from the client. For a disconnected
	// client it is how long ago it vanished, which is the difference between "still
	// rebooting" and "gone since yesterday".
	LastSeen clientLastSeen `json:"lastSeen"`
}

// clientHost is the machine a client runs on, as snapserver saw it connect.
type clientHost struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// clientConfig is a client's settings on the server.
type clientConfig struct {
	Name string `json:"name"`
	// Latency is the per-client playback offset in milliseconds. Snapcast keeps
	// clients in sync by delaying everyone to the slowest; a client with a large
	// offset is the one dragging the group.
	Latency int    `json:"latency"`
	Volume  Volume `json:"volume"`
}

// clientLastSeen is snapserver's timestamp of a client's last message.
type clientLastSeen struct {
	Sec  int64 `json:"sec"`
	Usec int64 `json:"usec"`
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

	err := c.call(ctx, "Server.GetStatus", nil, &result)
	if err != nil {
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

// rpcRequest is one JSON-RPC 2.0 call; this client always uses id 1.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse is a reply or, with id 0, an asynchronous notification.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// rpcError is the error member of a JSON-RPC reply.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("snapcast rpc %d: %s", e.Code, e.Message) }

const (
	jsonRPCVersion = "2.0"
	// requestID is the id of every call; a reply with any other id but a notification's 0
	// is not ours.
	requestID = 1
	// lineBufferSize is the initial buffer of one response line; it grows to maxResponseSize.
	lineBufferSize = 4096
)

var (
	errNoProgress       = errors.New("snapcast write: no progress")
	errInvalidVersion   = errors.New("snapcast response has invalid version")
	errInvalidID        = errors.New("snapcast response has invalid id")
	errNoResult         = errors.New("snapcast response has no result")
	errTooManyNotifies  = errors.New("snapcast response has too many notifications")
	errResponseTooLarge = errors.New("response too large")
	errMissingNewline   = errors.New("response missing newline")
)

func (c *Client) call(ctx context.Context, method string, params, result any) (err error) {
	req, err := json.Marshal(rpcRequest{JSONRPC: jsonRPCVersion, ID: requestID, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("snapcast request: %w", err)
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
		ctxErr := ctx.Err()
		if ctxErr != nil {
			err = ctxErr
		}
	}()

	err = writeRequest(ctx, conn, req)
	if err != nil {
		return err
	}

	err = conn.SetReadDeadline(deadline(ctx, readTimeout))
	if err != nil {
		return fmt.Errorf("snapcast read deadline: %w", err)
	}

	return readResponse(bufio.NewReader(conn), result)
}

// writeRequest sends the whole request line.
func writeRequest(ctx context.Context, conn net.Conn, req []byte) error {
	err := conn.SetWriteDeadline(deadline(ctx, writeTimeout))
	if err != nil {
		return fmt.Errorf("snapcast write deadline: %w", err)
	}

	for len(req) > 0 {
		written, err := conn.Write(req)
		if err != nil {
			return fmt.Errorf("snapcast write: %w", err)
		}

		if written == 0 {
			return errNoProgress
		}

		req = req[written:]
	}

	return nil
}

// readResponse reads lines until the reply to our call, skipping the notifications
// snapserver sends on the same connection, and decodes its result into result.
func readResponse(reader io.Reader, result any) error {
	var resp rpcResponse

	for range maxNotifications + 1 {
		line, err := readLine(reader)
		if err != nil {
			return fmt.Errorf("snapcast read: %w", err)
		}

		err = json.Unmarshal(line, &resp)
		if err != nil {
			return fmt.Errorf("snapcast response: %w", err)
		}

		if resp.JSONRPC != jsonRPCVersion {
			return errInvalidVersion
		}

		if resp.ID != requestID {
			// Snapserver sends asynchronous notifications on this connection.
			if resp.ID == 0 && resp.Error == nil && len(resp.Result) == 0 {
				continue
			}

			return errInvalidID
		}

		return decodeResult(&resp, result)
	}

	return errTooManyNotifies
}

// decodeResult turns the reply to our call into its error or its result.
func decodeResult(resp *rpcResponse, result any) error {
	if resp.Error != nil {
		return resp.Error
	}

	if result == nil {
		return nil
	}

	if len(resp.Result) == 0 {
		return errNoResult
	}

	err := json.Unmarshal(resp.Result, result)
	if err != nil {
		return fmt.Errorf("snapcast result: %w", err)
	}

	return nil
}

func deadline(ctx context.Context, timeout time.Duration) time.Time {
	limit := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(limit) {
		return ctxDeadline
	}

	return limit
}

func readLine(reader io.Reader) ([]byte, error) {
	buf := make([]byte, 0, lineBufferSize)

	chunk := make([]byte, 1)
	for {
		count, err := reader.Read(chunk)
		for idx, b := range chunk[:count] {
			if b == '\n' {
				if len(buf)+idx > maxResponseSize {
					return nil, errResponseTooLarge
				}

				return append(buf, chunk[:idx]...), nil
			}
		}

		if len(buf)+count > maxResponseSize {
			return nil, errResponseTooLarge
		}

		buf = append(buf, chunk[:count]...)

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errMissingNewline
			}

			return nil, fmt.Errorf("line: %w", err)
		}
	}
}
