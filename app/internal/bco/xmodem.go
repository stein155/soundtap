// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"fmt"
	"time"
)

// XmodemApp writes image into the app slot through level 2's "Download Image Only": XMODEM-CRC
// over the BCO UART, 128-byte blocks, about ten minutes for a full image. It is the way back
// when the TFTP path fails (2026-10-02: level 2 rejected every TFTP download with "Wrong BCD
// version" while XMODEM of the same stock image went through) and the only one that needs no
// network at all.
func (f *Flasher) XmodemApp(ctx context.Context, image []byte) (ver string, err error) {
	if _, err := CheckAppImage(image); err != nil {
		return "", err
	}

	c := &console{f: f}
	defer func() {
		perr := f.Power(true)
		if perr != nil {
			c.note("power on after XMODEM: %v", perr)
		}
	}()

	if err := c.powerCycle(ctx); err != nil {
		return "", err
	}

	if err := c.enterLevel1(ctx); err != nil {
		return "", err
	}

	key, err := c.menuKey(ctx, "Bootloader Level 1  Menu", "Start Bootloader Level 2", "6) Erase Flash")
	if err != nil {
		return "", err
	}

	if err := c.send(ctx, key); err != nil {
		return "", err
	}

	key, err = c.menuKey(ctx, "Bootloader Level 2 Menu", "Download Image Only", "Erase Application")
	if err != nil {
		return "", err
	}

	if err := c.send(ctx, key); err != nil {
		return "", err
	}
	// The receiver asks for CRC mode by sending 'C' until the first block arrives; two in a
	// row cannot be menu text.
	if _, err := c.expect(ctx, c.d(30*time.Second), "CC"); err != nil {
		return "", fmt.Errorf("BCO XMODEM did not start: %w", err)
	}

	c.note("XMODEM download of %d bytes", len(image))

	if err := c.xmodemSend(ctx, image); err != nil {
		return "", err
	}
	// Level 2 decrypts and programs the image only after the EOT; cutting power before it
	// reports back leaves "Application: invalid image" (2026-10-14).
	if _, err := c.expect(ctx, c.d(3*time.Minute), "download passed"); err != nil {
		return "", fmt.Errorf("BCO did not confirm the XMODEM download: %w", err)
	}

	c.note("XMODEM download done, rebooting the BCO")

	return c.boot(ctx)
}

const (
	xSOH = 0x01
	xEOT = 0x04
	xACK = 0x06
	xNAK = 0x15
	xCAN = 0x18
)

func crc16XMODEM(p []byte) uint16 {
	var c uint16
	for _, b := range p {
		c ^= uint16(b) << 8
		for range 8 {
			if c&0x8000 != 0 {
				c = c<<1 ^ 0x1021
			} else {
				c <<= 1
			}
		}
	}

	return c
}

func (c *console) xmodemSend(ctx context.Context, image []byte) error {
	blk := byte(1)

	for off := 0; off < len(image); off += 128 {
		var pkt [3 + 128 + 2]byte

		pkt[0], pkt[1], pkt[2] = xSOH, blk, ^blk

		d := pkt[3 : 3+128]
		for i := range d {
			d[i] = 0x1a
		}

		copy(d, image[off:])
		crc := crc16XMODEM(d)

		pkt[131], pkt[132] = byte(crc>>8), byte(crc)
		for try := 0; ; try++ {
			if _, err := c.f.Serial.Write(pkt[:]); err != nil {
				return fmt.Errorf("BCO XMODEM write: %w", err)
			}

			a, err := c.xmodemReply(ctx, c.d(5*time.Second))
			if err != nil {
				return err
			}

			if a == xACK {
				break
			}

			if a == xCAN || try >= 10 {
				return fmt.Errorf("BCO XMODEM aborted at byte %d (reply %#x)", off, a)
			}
		}

		blk++
	}

	if _, err := c.f.Serial.Write([]byte{xEOT}); err != nil {
		return fmt.Errorf("BCO XMODEM write: %w", err)
	}

	_, err := c.xmodemReply(ctx, c.d(5*time.Second))

	return err
}

// xmodemReply waits for ACK, NAK or CAN; 0 after timeout (counted as a NAK by the caller).
// The bytes are read straight off the UART: they are protocol, not console text.
func (c *console) xmodemReply(ctx context.Context, timeout time.Duration) (byte, error) {
	var b [64]byte

	for end := time.Now().Add(timeout); time.Now().Before(end); {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		n, err := c.f.Serial.Read(b[:])
		for _, x := range b[:n] {
			if x == xACK || x == xNAK || x == xCAN {
				return x, nil
			}
		}

		if err != nil || n == 0 {
			err := c.sleep(ctx, time.Millisecond)
			if err != nil {
				return 0, err
			}
		}
	}

	return 0, nil
}
