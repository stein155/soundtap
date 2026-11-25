// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package mqtt is the MQTT connection the bridge uses, on Eclipse Paho's MQTT 3.1.1
// client (github.com/eclipse/paho.mqtt.golang, ADR 0110). Paho does the protocol; this
// package fixes how the speaker uses it:
//
//   - MQTT 3.1.1 only, QoS 0, a clean session, a retained Last Will;
//   - no automatic reconnect: when the connection ends, Done closes and the caller
//     (internal/mqttbridge) connects again, reading the settings anew;
//   - ctx bounds the dial and the handshake, so a shutdown is not held up by a broker that
//     takes the connection and never answers;
//   - a subscription the broker refuses is an error.
package mqtt

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Will is the broker's Last Will and Testament: the message the broker publishes
// on the client's behalf if the connection drops without a clean DISCONNECT. The
// bridge uses it so clients see the speaker go offline when SoundTap dies.
type Will struct {
	Topic   string
	Payload []byte
	Retain  bool
}

// Options configures a Connect.
type Options struct {
	Addr        string        // broker "host:port"
	ClientID    string        // client identifier (unique per broker)
	Username    string        // optional
	Password    string        // optional (only sent when Username is set)
	KeepAlive   time.Duration // 0 -> 60s
	TLS         bool          // dial over TLS
	TLSConfig   *tls.Config   // optional; nil uses defaults for the host
	DialTimeout time.Duration // 0 -> 10s; bounds the dial and the handshake
	Will        *Will         // optional LWT
	// Handler receives every inbound PUBLISH. The *Client is passed in (rather than
	// captured by the caller) so a PUBLISH that arrives before Connect returns can't
	// race the assignment of the returned client. Paho calls it in the order the
	// messages arrived, one at a time, so it must not block; hand work off to another
	// goroutine if it might.
	Handler func(c *Client, m Message)
}

// Message is one inbound PUBLISH.
//
// Retain is the PUBLISH's RETAIN flag. A broker sets it only on a stored message it
// replays because of a new subscription (MQTT 3.1.1 §3.3.1.3); a message forwarded
// live arrives without it, even when its publisher asked the broker to retain it.
type Message struct {
	Topic   string
	Payload []byte
	Retain  bool
}

// Client is a live MQTT connection. It is safe for concurrent Publish/Subscribe.
type Client struct {
	pc paho.Client

	done      chan struct{}
	closeOnce sync.Once
	errMu     sync.Mutex
	err       error
}

// writeWait bounds a Publish, as the write deadline did before Paho.
const writeWait = 10 * time.Second

// subAckWait bounds how long Subscribe waits for the broker's SUBACK.
const subAckWait = 10 * time.Second

// disconnectWait is how long Disconnect lets Paho send the DISCONNECT: without it the
// broker sees a dropped connection and publishes the Will.
const disconnectWait = 250 // milliseconds, Paho's unit

// Connect dials the broker, performs the MQTT handshake, and returns a Client ready for
// Publish/Subscribe. ctx bounds only the dial and handshake, not the connection lifetime.
func Connect(ctx context.Context, opts Options) (*Client, error) {
	if opts.KeepAlive <= 0 {
		opts.KeepAlive = 60 * time.Second
	}

	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}

	hsTimeout := opts.DialTimeout
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < hsTimeout {
		hsTimeout = max(time.Until(dl), time.Millisecond)
	}

	c := &Client{done: make(chan struct{})}

	// The dial is ours rather than Paho's: it honours ctx, and the connection it returns
	// is closed when ctx ends during the handshake, so a broker that takes the TCP
	// connection and never answers CONNECT does not hold up a shutdown.
	var (
		connMu sync.Mutex
		conn   net.Conn
	)

	dial := func(*url.URL, paho.ClientOptions) (net.Conn, error) {
		d := net.Dialer{Timeout: opts.DialTimeout}

		var (
			nc  net.Conn
			err error
		)
		if opts.TLS {
			nc, err = (&tls.Dialer{NetDialer: &d, Config: opts.TLSConfig}).DialContext(ctx, "tcp", opts.Addr)
		} else {
			nc, err = d.DialContext(ctx, "tcp", opts.Addr)
		}

		if err != nil {
			return nil, fmt.Errorf("dial broker: %w", err)
		}

		connMu.Lock()
		conn = nc
		connMu.Unlock()

		return nc, nil
	}
	stopCancel := context.AfterFunc(ctx, func() {
		connMu.Lock()
		defer connMu.Unlock()

		if conn != nil {
			_ = conn.Close()
		}
	})

	po := paho.NewClientOptions().
		AddBroker("tcp://" + opts.Addr). // the scheme is not used: dial connects
		SetCustomOpenConnectionFn(dial).
		SetClientID(opts.ClientID).
		SetProtocolVersion(4). // MQTT 3.1.1, never the 3.1 fallback
		SetCleanSession(true).
		SetResumeSubs(false).
		SetAutoReconnect(false).
		SetConnectRetry(false).
		SetKeepAlive(opts.KeepAlive).
		SetConnectTimeout(hsTimeout).
		SetWriteTimeout(writeWait).
		SetOrderMatters(true).
		SetConnectionLostHandler(func(_ paho.Client, err error) {
			if err == nil {
				err = errors.New("connection lost")
			}

			c.close(err)
		})
	if opts.Username != "" {
		po.SetUsername(opts.Username)
		po.SetPassword(opts.Password) // Paho sends it only with a username
	}

	if opts.Will != nil {
		po.SetBinaryWill(opts.Will.Topic, opts.Will.Payload, 0, opts.Will.Retain)
	}

	if h := opts.Handler; h != nil {
		po.SetDefaultPublishHandler(func(_ paho.Client, m paho.Message) {
			h(c, Message{Topic: m.Topic(), Payload: m.Payload(), Retain: m.Retained()})
		})
	}

	c.pc = paho.NewClient(po)
	tok := c.pc.Connect()
	<-tok.Done() // Paho bounds it with the connect timeout; ctx closes the connection
	err := tok.Error()

	if !stopCancel() {
		// ctx ended during or right after the handshake: the connection is closed.
		c.pc.Disconnect(0)

		return nil, ctx.Err()
	}

	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		return nil, err
	}

	return c, nil
}

// Done is closed when the connection is lost or Disconnect is called. Err reports
// the cause (nil for a clean local Disconnect).
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns the error that closed the connection, or nil if it is still up or was
// closed cleanly.
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()

	return c.err
}

func (c *Client) close(err error) {
	c.closeOnce.Do(func() {
		c.errMu.Lock()
		c.err = err
		c.errMu.Unlock()
		close(c.done)
	})
}

// Publish sends an application message at QoS 0 and waits until it is written.
func (c *Client) Publish(topic string, payload []byte, retain bool) error {
	tok := c.pc.Publish(topic, 0, retain, payload)
	if !tok.WaitTimeout(writeWait) {
		return errors.New("publish timed out")
	}

	return tok.Error()
}

// Subscribe registers interest in one or more topic filters at QoS 0 and waits for
// the broker's SUBACK. A filter the broker refuses (return code 0x80, typically an
// ACL) is an error: without it the bridge reported "connected" while no command could
// ever arrive.
func (c *Client) Subscribe(topics ...string) error {
	if len(topics) == 0 {
		return nil
	}

	filters := make(map[string]byte, len(topics))
	for _, t := range topics {
		filters[t] = 0
	}

	tok := c.pc.SubscribeMultiple(filters, nil)

	t := time.NewTimer(subAckWait)
	defer t.Stop()

	select {
	case <-tok.Done():
	case <-c.done:
		err := c.Err()
		if err != nil {
			return err
		}

		return errors.New("connection closed before SUBACK")
	case <-t.C:
		return errors.New("no SUBACK from broker")
	}

	err := tok.Error()
	if err != nil {
		return err
	}

	st, ok := tok.(*paho.SubscribeToken)
	if !ok {
		return errors.New("no SUBACK result from the client")
	}

	granted := st.Result()
	for _, topic := range topics {
		if code, ok := granted[topic]; !ok || code&0x80 != 0 {
			return fmt.Errorf("broker refused subscription to %q", topic)
		}
	}

	return nil
}

// Disconnect sends DISCONNECT and closes the connection. Safe to call repeatedly.
func (c *Client) Disconnect() {
	c.pc.Disconnect(disconnectWait)
	c.close(nil)
}
