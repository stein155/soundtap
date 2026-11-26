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
	t.Parallel()

	listen := func() *net.UDPConn {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0, Zone: ""})
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { conn.Close() })

		return conn
	}
	client, peer, other := listen(), listen(), listen()
	ack := func(from *net.UDPConn, block uint16) {
		pkt := []byte{0, 4, 0, 0}
		binary.BigEndian.PutUint16(pkt[2:], block)

		_, err := from.WriteToUDP(pkt, client.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
	}
	ack(other, 1)

	_, err := tftpAck(context.Background(), client, peer.LocalAddr().(*net.UDPAddr), 1, 300*time.Millisecond)
	if !isTimeout(err) {
		t.Fatalf("ACK from another port: err = %v, want a timeout", err)
	}

	ack(peer, 1)

	from, err := tftpAck(context.Background(), client, peer.LocalAddr().(*net.UDPAddr), 1, time.Second)
	if err != nil || from.Port != peer.LocalAddr().(*net.UDPAddr).Port {
		t.Fatalf("ACK from the transfer port: from = %v, err = %v", from, err)
	}
}
