// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package oled draws to the SoundTouch front-panel OLED.
//
// The display is a 128x100 8-bit grayscale panel exposed through /dev/fb0 on
// stock kernels or spidev on ST20 mainline. The package provides a Framebuffer that
// preserves the previous known frame while drawing; the screens are in oled/screen.
package oled

import (
	"fmt"
	"os"
	"sync"
)

// Panel dimensions of the SoundTouch 20 OLED.
const (
	Width  = 128
	Height = 100
)

// Framebuffer writes frames to the panel and can restore what was on it.
// The first Draw snapshots the current contents; Restore puts them back.
// Safe for concurrent use.
type Framebuffer struct {
	path string

	mu      sync.Mutex
	backup  []byte
	drawing bool
	spi     *spiOLED
}

// NewFramebuffer targets /dev/fb0 or spidev:/dev/spidev1.0. SPI is not opened
// until the first Draw.
func NewFramebuffer(path string) *Framebuffer {
	return &Framebuffer{path: path}
}

// Available reports whether path is a readable framebuffer with the panel's
// exact size. Only the SoundTouch 20 exposes a 128x100 grayscale panel this
// way; on other models (or off-speaker) this returns false and callers must
// skip drawing entirely.
func Available(path string) bool {
	if spiPath, ok := parseSPIPath(path); ok {
		_, err := os.Stat(spiPath)
		return err == nil
	}
	b, err := os.ReadFile(path)
	return err == nil && len(b) == Width*Height
}

// Draw writes a full frame (Width*Height bytes) to the panel, snapshotting
// the previous contents on the first call since the last Restore.
func (f *Framebuffer) Draw(pix []byte) error {
	if len(pix) != Width*Height {
		return fmt.Errorf("oled: frame is %d bytes, want %d", len(pix), Width*Height)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if spiPath, ok := parseSPIPath(f.path); ok {
		if f.spi == nil {
			spi, err := openSPIOLED(spiPath, f.path)
			if err != nil {
				return err
			}
			f.spi = spi
		}
		if !f.drawing && len(f.spi.latest) == Width*Height {
			f.backup = append([]byte(nil), f.spi.latest...)
		}
		f.drawing = true
		return f.spi.draw(pix)
	}
	if !f.drawing {
		if b, err := os.ReadFile(f.path); err == nil && len(b) == Width*Height {
			f.backup = append([]byte(nil), b...)
		}
		f.drawing = true
	}
	return os.WriteFile(f.path, pix, 0o600)
}

// Restore writes the snapshot taken by the first Draw back to the panel.
// A no-op when nothing was drawn since the last Restore.
func (f *Framebuffer) Restore() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.backup
	wasDrawing := f.drawing
	f.drawing = false
	f.backup = nil
	if wasDrawing && len(b) == Width*Height {
		if f.spi != nil {
			return f.spi.draw(b)
		}
		return os.WriteFile(f.path, b, 0o600)
	}
	return nil
}
