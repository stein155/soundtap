// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// XmodemApp writes image into the app slot through level 2's "Download Image Only": XMODEM-CRC
// over the BCO UART, 128-byte blocks, about ten minutes for a full image. It is the way back
// when the TFTP path fails (2026-10-02: level 2 rejected every TFTP download with "Wrong BCD
// version" while XMODEM of the same stock image went through) and the only one that needs no
// network at all.
func (f *Flasher) XmodemApp(ctx context.Context, image []byte) (string, error) {
	_, err := CheckAppImage(image)
	if err != nil {
		return "", err
	}

	cons := &console{f: f, buf: nil}
	defer func() {
		perr := f.Power(true)
		if perr != nil {
			cons.notef("power on after XMODEM: %v", perr)
		}
	}()

	err = cons.enterLevel2(ctx)
	if err != nil {
		return "", err
	}

	key, err := cons.menuKey(ctx, "Bootloader Level 2 Menu", "Download Image Only", "Erase Application")
	if err != nil {
		return "", err
	}

	err = cons.send(ctx, key)
	if err != nil {
		return "", err
	}
	// The receiver asks for CRC mode by sending 'C' until the first block arrives; two in a
	// row cannot be menu text.
	_, err = cons.expect(ctx, cons.d(xmodemStartWait), "CC")
	if err != nil {
		return "", fmt.Errorf("BCO XMODEM did not start: %w", err)
	}

	cons.notef("XMODEM download of %d bytes", len(image))

	err = cons.xmodemSend(ctx, image)
	if err != nil {
		return "", err
	}
	// Level 2 decrypts and programs the image only after the EOT; cutting power before it
	// reports back leaves "Application: invalid image" (2026-10-14).
	_, err = cons.expect(ctx, cons.d(xmodemProgramWait), "download passed")
	if err != nil {
		return "", fmt.Errorf("BCO did not confirm the XMODEM download: %w", err)
	}

	cons.notef("XMODEM download done, rebooting the BCO")

	return cons.boot(ctx)
}

const (
	xSOH = 0x01
	xEOT = 0x04
	xACK = 0x06
	xNAK = 0x15
	xCAN = 0x18
)

// XMODEM-CRC framing: SOH, block number and its complement, 128 data bytes, CRC-16 (big
// endian), and the CCITT polynomial of that CRC.
const (
	xmodemData      = 128
	xmodemHeader    = 3
	xmodemPacket    = xmodemHeader + xmodemData + 2
	xmodemPad       = 0x1a // SUB fills the last block
	xmodemMaxRetry  = 10
	crc16Poly       = 0x1021
	crc16TopBit     = 0x8000
	bitsPerByte     = 8
	xmodemStartWait = 30 * time.Second
	// xmodemProgramWait bounds level 2's decrypt and program after the EOT.
	xmodemProgramWait = 3 * time.Minute
	xmodemReplyWait   = 5 * time.Second
)

// errXmodemAborted means the receiver cancelled the transfer or kept refusing a block.
var errXmodemAborted = errors.New("BCO XMODEM aborted")

func crc16XMODEM(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << bitsPerByte
		for range bitsPerByte {
			if crc&crc16TopBit != 0 {
				crc = crc<<1 ^ crc16Poly
			} else {
				crc <<= 1
			}
		}
	}

	return crc
}

func (c *console) xmodemSend(ctx context.Context, image []byte) error {
	blk := byte(1)

	for off := 0; off < len(image); off += xmodemData {
		var pkt [xmodemPacket]byte

		pkt[0], pkt[1], pkt[2] = xSOH, blk, ^blk

		payload := pkt[xmodemHeader : xmodemHeader+xmodemData]
		for i := range payload {
			payload[i] = xmodemPad
		}

		copy(payload, image[off:])
		binary.BigEndian.PutUint16(pkt[xmodemHeader+xmodemData:], crc16XMODEM(payload))

		err := c.xmodemBlock(ctx, pkt[:], off)
		if err != nil {
			return err
		}

		blk++
	}

	_, err := c.f.Serial.Write([]byte{xEOT})
	if err != nil {
		return fmt.Errorf("BCO XMODEM write: %w", err)
	}

	_, err = c.xmodemReply(ctx, c.d(xmodemReplyWait))

	return err
}

// xmodemBlock sends one packet until it is acknowledged; off is its place in the image.
func (c *console) xmodemBlock(ctx context.Context, pkt []byte, off int) error {
	for try := 0; ; try++ {
		_, err := c.f.Serial.Write(pkt)
		if err != nil {
			return fmt.Errorf("BCO XMODEM write: %w", err)
		}

		reply, err := c.xmodemReply(ctx, c.d(xmodemReplyWait))
		if err != nil {
			return err
		}

		if reply == xACK {
			return nil
		}

		if reply == xCAN || try >= xmodemMaxRetry {
			return fmt.Errorf("%w at byte %d (reply %#x)", errXmodemAborted, off, reply)
		}
	}
}

// xmodemReply waits for ACK, NAK or CAN; 0 after timeout (counted as a NAK by the caller).
// The bytes are read straight off the UART: they are protocol, not console text.
func (c *console) xmodemReply(ctx context.Context, timeout time.Duration) (byte, error) {
	var replies [64]byte

	for end := time.Now().Add(timeout); time.Now().Before(end); {
		err := ctx.Err()
		if err != nil {
			return 0, fmt.Errorf("BCO XMODEM: %w", err)
		}

		count, err := c.f.Serial.Read(replies[:])
		for _, x := range replies[:count] {
			if x == xACK || x == xNAK || x == xCAN {
				return x, nil
			}
		}

		if err != nil || count == 0 {
			err = sleepCtx(ctx, time.Millisecond)
			if err != nil {
				return 0, err
			}
		}
	}

	return 0, nil
}
