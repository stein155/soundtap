// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// An ACK from another port on the same host belongs to another transfer: only the
// transfer's own port counts.
func TestTftpAckIgnoresOtherPorts(t *testing.T) {
	listen := func() *net.UDPConn {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	client, peer, other := listen(), listen(), listen()
	ack := func(c *net.UDPConn, block uint16) {
		pkt := []byte{0, 4, 0, 0}
		binary.BigEndian.PutUint16(pkt[2:], block)
		if _, err := c.WriteToUDP(pkt, client.LocalAddr().(*net.UDPAddr)); err != nil {
			t.Fatal(err)
		}
	}
	ack(other, 1)
	if _, err := tftpAck(context.Background(), client, peer.LocalAddr().(*net.UDPAddr), 1, 300*time.Millisecond); !isTimeout(err) {
		t.Fatalf("ACK from another port: err = %v, want a timeout", err)
	}
	ack(peer, 1)
	from, err := tftpAck(context.Background(), client, peer.LocalAddr().(*net.UDPAddr), 1, time.Second)
	if err != nil || from.Port != peer.LocalAddr().(*net.UDPAddr).Port {
		t.Fatalf("ACK from the transfer port: from = %v, err = %v", from, err)
	}
}
