// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// BootloaderTFTP is where bootloader level 2 listens while it waits for an image: the
// BCO's side of the internal link. The SCM must hold 192.168.1.101 on that link.
const BootloaderTFTP = "192.168.1.100:69"

// tftpPut writes data as name to a TFTP server (RFC 1350, octet mode, 512-byte blocks:
// what stock's curl upload used against the same bootloader). It waits for the server
// to appear, because level 2 needs a few seconds after its menu to bring its link up.
func tftpPut(ctx context.Context, server, name string, data []byte) error {
	raddr, err := net.ResolveUDPAddr("udp4", server)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Cancelling wakes a pending read at once instead of after its 2 s timeout.
	stop := context.AfterFunc(ctx, func() { conn.SetReadDeadline(time.Now()) })
	defer stop()

	wrq := append([]byte{0, 2}, name...)
	wrq = append(wrq, 0)
	wrq = append(wrq, "octet"...)
	wrq = append(wrq, 0)

	// Request, retried until the server answers or ctx ends.
	var peer *net.UDPAddr
	for peer == nil {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("TFTP server %s never answered: %w", server, err)
		}
		if _, err := conn.WriteToUDP(wrq, raddr); err != nil {
			// The link may not have the address yet; keep trying until ctx ends.
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		peer, err = tftpAck(ctx, conn, &net.UDPAddr{IP: raddr.IP}, 0, 2*time.Second)
		if err != nil && !isTimeout(err) {
			return err
		}
	}

	blocks := len(data)/512 + 1 // a final short (possibly empty) block ends the transfer
	for b := 1; b <= blocks; b++ {
		lo := (b - 1) * 512
		hi := min(lo+512, len(data))
		pkt := make([]byte, 4, 4+hi-lo)
		binary.BigEndian.PutUint16(pkt[0:], 3)
		binary.BigEndian.PutUint16(pkt[2:], uint16(b))
		pkt = append(pkt, data[lo:hi]...)
		for try := 0; ; try++ {
			// A cancelled upload stops sending firmware, between blocks and between retries.
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("TFTP block %d: %w", b, err)
			}
			if try == 8 {
				return fmt.Errorf("TFTP block %d: no ACK after %d tries", b, try)
			}
			if _, err := conn.WriteToUDP(pkt, peer); err != nil {
				return err
			}
			if _, err := tftpAck(ctx, conn, peer, uint16(b), 2*time.Second); err == nil {
				break
			} else if !isTimeout(err) {
				return err
			}
		}
	}
	return nil
}

// tftpAck waits for the ACK of block from peer and returns the port it came from. Port 0
// takes any port, for the answer to the request; after that the transfer's port is its ID
// (RFC 1350) and packets from any other port are ignored.
func tftpAck(ctx context.Context, conn *net.UDPConn, peer *net.UDPAddr, block uint16, wait time.Duration) (*net.UDPAddr, error) {
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetReadDeadline(deadline)
	// A cancel that landed before the line above had its "now" deadline overwritten:
	// check again, or this read waits out the full retry.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	buf := make([]byte, 516)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if !from.IP.Equal(peer.IP) || peer.Port != 0 && from.Port != peer.Port || n < 4 {
			continue
		}
		switch binary.BigEndian.Uint16(buf) {
		case 4:
			if binary.BigEndian.Uint16(buf[2:]) == block {
				return from, nil
			}
			// A duplicate ACK of an earlier block: keep waiting for ours.
		case 5:
			return nil, fmt.Errorf("TFTP error %d from %s: %s", binary.BigEndian.Uint16(buf[2:]), from, cstr(buf[4:n]))
		}
	}
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
