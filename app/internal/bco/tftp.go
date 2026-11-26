// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"time"
)

// BootloaderTFTP is where bootloader level 2 listens while it waits for an image: the
// BCO's side of the internal link. The SCM must hold 192.168.1.101 on that link.
const BootloaderTFTP = "192.168.1.100:69"

// TFTP (RFC 1350): opcodes, the header before a block's data, and the block size.
const (
	tftpOpWRQ   = 2
	tftpOpData  = 3
	tftpOpAck   = 4
	tftpOpError = 5
	tftpHeader  = 4
	tftpBlock   = 512
	// tftpMaxTries is how often one block is sent before the upload gives up.
	tftpMaxTries = 8
	// tftpAckWait is how long one try waits for its ACK.
	tftpAckWait = 2 * time.Second
)

// The upload's errors; each keeps the text it had before it was a sentinel.
var (
	errTFTPNoAck  = errors.New("no ACK after")
	errTFTPRemote = errors.New("TFTP error")
	// errTFTPTooLarge is data with more blocks than a 16-bit block number counts.
	errTFTPTooLarge = errors.New("TFTP upload too large")
)

// tftpPut writes data as name to a TFTP server (RFC 1350, octet mode, 512-byte blocks:
// what stock's curl upload used against the same bootloader). It waits for the server
// to appear, because level 2 needs a few seconds after its menu to bring its link up.
func tftpPut(ctx context.Context, server, name string, data []byte) error {
	raddr, err := net.ResolveUDPAddr("udp4", server)
	if err != nil {
		return fmt.Errorf("TFTP server %s: %w", server, err)
	}

	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return fmt.Errorf("TFTP socket: %w", err)
	}
	defer conn.Close()
	// Cancelling wakes a pending read at once instead of after its 2 s timeout. A deadline
	// that cannot be set means a closed socket, whose read fails by itself.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	peer, err := tftpRequest(ctx, conn, raddr, server, name)
	if err != nil {
		return err
	}

	blocks := len(data)/tftpBlock + 1 // a final short (possibly empty) block ends the transfer
	if blocks > math.MaxUint16 {
		// RFC 1350 numbers blocks in 16 bits; an app image needs about 10,600.
		return fmt.Errorf("%w: %d bytes", errTFTPTooLarge, len(data))
	}

	last := uint16(blocks)

	for block := uint16(1); ; block++ {
		lo := int(block-1) * tftpBlock
		hi := min(lo+tftpBlock, len(data))
		pkt := make([]byte, tftpHeader, tftpHeader+hi-lo)
		binary.BigEndian.PutUint16(pkt[0:], tftpOpData)
		binary.BigEndian.PutUint16(pkt[2:], block)
		pkt = append(pkt, data[lo:hi]...)

		err = tftpSendBlock(ctx, conn, peer, pkt, block)
		if err != nil {
			return err
		}

		if block == last {
			return nil
		}
	}
}

// tftpRequest sends the write request until the server answers or ctx ends, and returns
// the address of the transfer's port.
func tftpRequest(
	ctx context.Context, conn *net.UDPConn, raddr *net.UDPAddr, server, name string,
) (*net.UDPAddr, error) {
	wrq := append([]byte{0, tftpOpWRQ}, name...)
	wrq = append(wrq, 0)
	wrq = append(wrq, "octet"...)
	wrq = append(wrq, 0)

	var peer *net.UDPAddr
	for peer == nil {
		err := ctx.Err()
		if err != nil {
			return nil, fmt.Errorf("TFTP server %s never answered: %w", server, err)
		}

		_, err = conn.WriteToUDP(wrq, raddr)
		if err != nil {
			// The link may not have the address yet; keep trying until ctx ends.
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}

			continue
		}

		peer, err = tftpAck(ctx, conn, &net.UDPAddr{IP: raddr.IP, Port: 0, Zone: ""}, 0, tftpAckWait)
		if err != nil && !isTimeout(err) {
			return nil, err
		}
	}

	return peer, nil
}

// tftpSendBlock sends one DATA packet until its ACK arrives.
func tftpSendBlock(ctx context.Context, conn *net.UDPConn, peer *net.UDPAddr, pkt []byte, block uint16) error {
	for try := 0; ; try++ {
		// A cancelled upload stops sending firmware, between blocks and between retries.
		err := ctx.Err()
		if err != nil {
			return fmt.Errorf("TFTP block %d: %w", block, err)
		}

		if try == tftpMaxTries {
			return fmt.Errorf("TFTP block %d: %w %d tries", block, errTFTPNoAck, try)
		}

		_, err = conn.WriteToUDP(pkt, peer)
		if err != nil {
			return fmt.Errorf("TFTP block %d: %w", block, err)
		}

		_, err = tftpAck(ctx, conn, peer, block, tftpAckWait)
		if err == nil {
			return nil
		}

		if !isTimeout(err) {
			return err
		}
	}
}

// tftpAck waits for the ACK of block from peer and returns the port it came from. Port 0
// takes any port, for the answer to the request; after that the transfer's port is its ID
// (RFC 1350) and packets from any other port are ignored.
func tftpAck(
	ctx context.Context, conn *net.UDPConn, peer *net.UDPAddr, block uint16, wait time.Duration,
) (*net.UDPAddr, error) {
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	err := conn.SetReadDeadline(deadline)
	if err != nil {
		return nil, fmt.Errorf("TFTP read deadline: %w", err)
	}
	// A cancel that landed before the line above had its "now" deadline overwritten:
	// check again, or this read waits out the full retry.
	err = ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("TFTP ACK: %w", err)
	}

	buf := make([]byte, tftpHeader+tftpBlock)
	for {
		count, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("TFTP ACK: %w", ctx.Err())
			}

			return nil, fmt.Errorf("TFTP ACK: %w", err)
		}

		if !from.IP.Equal(peer.IP) || peer.Port != 0 && from.Port != peer.Port || count < tftpHeader {
			continue
		}

		acked, err := tftpAnswer(buf[:count], from, block)
		if err != nil {
			return nil, err
		}

		if acked {
			return from, nil
		}
	}
}

// tftpAnswer reads one packet from the transfer's peer: true for the ACK of block, an
// error for a TFTP ERROR, and false for anything else (a duplicate ACK of an earlier
// block), which keeps the wait going.
func tftpAnswer(pkt []byte, from *net.UDPAddr, block uint16) (bool, error) {
	switch binary.BigEndian.Uint16(pkt) {
	case tftpOpAck:
		return binary.BigEndian.Uint16(pkt[2:]) == block, nil
	case tftpOpError:
		return false, fmt.Errorf("%w %d from %s: %s",
			errTFTPRemote, binary.BigEndian.Uint16(pkt[2:]), from, cstr(pkt[tftpHeader:]))
	default:
		return false, nil // not an answer to a write
	}
}

func cstr(raw []byte) string {
	for i, c := range raw {
		if c == 0 {
			return string(raw[:i])
		}
	}

	return string(raw)
}

func isTimeout(err error) bool {
	var ne net.Error

	return errors.As(err, &ne) && ne.Timeout()
}
