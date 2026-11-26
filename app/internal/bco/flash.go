// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

// Flashing the BCO application, the way stock's SMSC_Update does it, read from its own
// logs on the bench ST20 (/mnt/nv/h.log, 2026-08-07) and a menu probe on 2026-09-23:
//
//  1. power-cycle the BCO (gpio0_14 high, then low);
//  2. Ctrl-A until "Bootloader Level 1  Menu", then the key for "Start Bootloader Level 2";
//  3. in "Bootloader Level 2 Menu", the key for "Download Image and Resources (TFTP)" --
//     the menu line SMSC_Update itself matches on. It leaves the CnE and the MAC alone;
//  4. TFTP-put smsc_app.bcd to the BCO's private address 192.168.1.100 from 192.168.1.101;
//  5. wait for "download with TFTP was successful", power-cycle, read the app's version.
//
// Level 1's "Download Bootloader Level 2" and "Erase Flash" are never selected: keys are
// looked up by their exact label, and only the two labels above are ever asked for.
//
// A dead app is not a brick: level 1 then prints "Forced start of Bootloader Level 2" and
// level 2 takes a new image the same way (seen after the 9418 flash, 2026-08-07).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The stock ST20 application image, App_BD_DMP 3.x-3_8_0-9423_ER.bcd.
const (
	StockAppSHA256 = "9446fca43b6bfe5001bd6e79d66bef75213ee5b8a4adfa70fad05b2b4a8d3829"
	StockAppSize   = 5407744
)

// appImages are the only application images the flasher accepts, by sha256. AllowImage
// adds to it from the boot-time AutoInstall goroutine while HTTP handlers check uploads
// against it, so every access goes through appImagesMu.
//
//nolint:gochecknoglobals // one allowlist per process: an image AllowImage approves holds for every later flash
var (
	appImagesMu sync.RWMutex
	appImages   = map[string]string{
		StockAppSHA256: "stock 9423",
		"6cf9d334b342ae9922432e7de02e70ddd8c38f708c9877da0fdbffc64060894a": "soundtap shell 9416",
		// Step 13 of BCO-PLAN: one log-string byte edit, all four checksums repaired by
		// build-code-experiment.py --experiment log-byte (removed with the stock tooling,
		// ADR 0049). Dead app = flash stock.
		"f4b8376f95c974907d474d6ad290f1a17dcb3773b50565ea0074122f23430347": "patched-code experiment 9414",
	}
)

// ErrAppDead means the image was written but the BCO refused to start it and fell back
// to bootloader level 2. Flash a known-good image (stock 9423) next.
var ErrAppDead = errors.New("BCO app does not start: bootloader level 1 forced level 2")

// ErrUpload means the TFTP download into level 2 failed or was not confirmed; the app slot
// may be empty. Level 2's XMODEM download over the UART does not depend on that path.
var ErrUpload = errors.New("BCO TFTP download failed")

// The image checks' and the console's errors. Each keeps the text the flasher printed
// before it was a sentinel; the details are added around it.
var (
	errImageMismatch   = errors.New("does not match the image")
	errImageSize       = errors.New("BCO image size")
	errImageNotAllowed = errors.New("is not an allowed image")
	errNoAppVersion    = errors.New("BCO app started but printed no APP= version")
	errConsoleTimeout  = errors.New("BCO: no")
	errNoLevel1Menu    = errors.New("BCO: bootloader level 1 menu did not appear")
	errMenuTwice       = errors.New("BCO menu lists")
	errMenuMissing     = errors.New("BCO menu has no entry")
)

// The flasher's waits, before Scale.
const (
	uploadTimeout     = 6 * time.Minute
	uploadConfirmWait = 90 * time.Second
	appStartWait      = 15 * time.Second
	appReadyWait      = 90 * time.Second
	consolePoll       = 20 * time.Millisecond
	level1Window      = 5 * time.Second
	ctrlAInterval     = 100 * time.Millisecond
	menuTitleWait     = 30 * time.Second
	menuBodyWait      = 10 * time.Second
)

// consoleChunk is the most one UART read takes.
const consoleChunk = 1024

// AllowImage adds image to the allowed images for this process when its sha256 equals want
// (lowercase hex). It is the explicit, per-image approval for an experiment build.
func AllowImage(image []byte, want string) error {
	sum := sha256.Sum256(image)

	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("-allow-sha256 %s %w (%s)", want, errImageMismatch, got)
	}

	appImagesMu.Lock()
	appImages[want] = "approved experiment"
	appImagesMu.Unlock()

	return nil
}

// CheckAppImage returns the name of an allowed image, or an error for anything else.
func CheckAppImage(image []byte) (string, error) {
	// Our own image (ADR 0031) is far smaller than stock's; nothing fits the slot above it.
	if len(image) == 0 || len(image) > StockAppSize {
		return "", fmt.Errorf("%w %d, want 1..%d", errImageSize, len(image), StockAppSize)
	}

	sum := sha256.Sum256(image)

	appImagesMu.RLock()

	name, ok := appImages[hex.EncodeToString(sum[:])]

	appImagesMu.RUnlock()

	if !ok {
		return "", fmt.Errorf("BCO image sha256 %x %w", sum, errImageNotAllowed)
	}

	return name, nil
}

// Flasher is what FlashApp needs from the hardware.
type Flasher struct {
	// Serial is the BCO UART. Read must not block: it returns 0 when nothing is waiting.
	Serial io.ReadWriter
	// Power switches the BCO's supply (gpio0_14 low = on).
	Power func(on bool) error
	// Upload sends the image to the level-2 bootloader's TFTP server.
	Upload func(ctx context.Context, image []byte) error
	// Log receives everything the BCO prints, and our own steps.
	Log io.Writer
	// Scale shortens every wait; tests set it, hardware leaves it 0 (= 1).
	Scale float64
}

// FlashApp writes image into the BCO application slot and returns the version string the
// new app reports at boot ("APP=9416.47.9416...").
func (f *Flasher) FlashApp(ctx context.Context, image []byte) (string, error) {
	_, err := CheckAppImage(image)
	if err != nil {
		return "", err
	}

	cons := &console{f: f, buf: nil}

	defer func() { cons.leavePowered(ctx, err) }()

	ver, err := cons.flashTFTP(ctx, image)

	return ver, err
}

// console is the BCO UART seen as a stream of text with deadlines.
type console struct {
	f   *Flasher
	buf []byte // received and not yet consumed by expect
}

// leavePowered runs when a flash ends, however it ended: it leaves the BCO powered,
// because off means no Wi-Fi and no Ethernet. After a failure the BCO may also be sitting
// in a bootloader menu, which has no network either, so it is cycled, even when ctx is
// cancelled: level 1 starts the app on its own. A dead app is sent on to level 2
// regardless, which is what stock does too.
func (c *console) leavePowered(ctx context.Context, err error) {
	if err != nil && !errors.Is(err, ErrAppDead) {
		c.notef("flash failed (%v): power-cycling the BCO back into its app", err)

		perr := c.powerCycle(context.WithoutCancel(ctx))
		if perr != nil {
			c.notef("power cycle after failure: %v", perr)
		}
	}

	perr := c.f.Power(true)
	if perr != nil {
		c.notef("power on after flash: %v", perr)
	}
}

// flashTFTP is FlashApp's sequence: level 2, the TFTP download, the new app's boot.
func (c *console) flashTFTP(ctx context.Context, image []byte) (string, error) {
	err := c.enterLevel2(ctx)
	if err != nil {
		return "", err
	}

	key, err := c.menuKey(ctx, "Bootloader Level 2 Menu", "Download Image and Resources (TFTP)", "Erase Application")
	if err != nil {
		return "", err
	}

	err = c.send(ctx, key)
	if err != nil {
		return "", err
	}

	c.notef("TFTP upload of %d bytes", len(image))
	uctx, cancel := context.WithTimeout(ctx, c.d(uploadTimeout))
	err = c.f.Upload(uctx, image)

	cancel()

	if err != nil {
		return "", fmt.Errorf("%w: upload: %w", ErrUpload, err)
	}

	_, err = c.expect(ctx, c.d(uploadConfirmWait), "download with TFTP was successful")
	if err != nil {
		return "", fmt.Errorf("%w: not confirmed: %w", ErrUpload, err)
	}

	c.notef("download confirmed, rebooting the BCO")

	return c.boot(ctx)
}

// enterLevel2 power-cycles the BCO, stops it in level 1 and starts level 2 from its menu.
func (c *console) enterLevel2(ctx context.Context) error {
	err := c.powerCycle(ctx)
	if err != nil {
		return err
	}

	err = c.enterLevel1(ctx)
	if err != nil {
		return err
	}

	key, err := c.menuKey(ctx, "Bootloader Level 1  Menu", "Start Bootloader Level 2", "6) Erase Flash")
	if err != nil {
		return err
	}

	return c.send(ctx, key)
}

// boot power-cycles the BCO into the freshly written app and returns its version.
func (c *console) boot(ctx context.Context) (string, error) {
	err := c.powerCycle(ctx)
	if err != nil {
		return "", err
	}

	_, err = c.expect(ctx, c.d(appStartWait), "Starting Application")
	if err != nil {
		return "", err
	}
	// A dead app is thrown back to level 1 within seconds, which then forces level 2.
	// Stock's shell says "sdsShell ready"; our own app (ADR 0031) says "netcard up".
	got, boot, err := c.expectText(ctx, c.d(appReadyWait),
		"sdsShell ready", "netcard up", "Forced start of Bootloader Level 2")
	if err != nil {
		return "", err
	}

	if got == "netcard up" {
		return "own app", nil
	}

	if got != "sdsShell ready" {
		return "", ErrAppDead
	}

	ver := appVersion.FindString(boot)
	if ver == "" {
		return "", errNoAppVersion
	}

	return ver, nil
}

func (c *console) d(d time.Duration) time.Duration {
	if c.f.Scale > 0 {
		return time.Duration(float64(d) * c.f.Scale)
	}

	return d
}

func (c *console) notef(format string, a ...any) {
	if c.f.Log != nil {
		fmt.Fprintf(c.f.Log, "\n[bcoflash] "+format+"\n", a...)
	}
}

// sleepCtx waits for d, or returns ctx's error if it ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("BCO wait: %w", ctx.Err())
	case <-time.After(d):
		return nil
	}
}

// A poll reads at most pollReads chunks, and buf keeps at most the last consoleKeep
// bytes. Before, poll read until the UART went quiet: a BCO that kept printing held the
// flasher inside poll past every deadline and cancellation, with buf growing without end.
// Every marker and menu the flasher waits for is far shorter than consoleKeep.
const (
	pollReads   = 16
	consoleKeep = 64 << 10
)

// poll moves what the UART holds into buf, up to pollReads reads.
func (c *console) poll() error {
	var chunk [consoleChunk]byte

	defer func() {
		if len(c.buf) > consoleKeep {
			c.buf = append([]byte(nil), c.buf[len(c.buf)-consoleKeep:]...)
		}
	}()

	for range pollReads {
		count, err := c.f.Serial.Read(chunk[:])
		if count > 0 {
			c.buf = append(c.buf, chunk[:count]...)
			if c.f.Log != nil {
				// The transcript is for the reader; a log that fails does not stop a flash.
				_, _ = c.f.Log.Write(chunk[:count])
			}
		}

		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("BCO serial read: %w", err)
		}

		if count == 0 || err != nil {
			return nil
		}
	}

	return nil
}

func (c *console) send(ctx context.Context, keys string) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("BCO send: %w", err)
	}

	_, err = c.f.Serial.Write([]byte(keys))
	if err != nil {
		return fmt.Errorf("BCO send: %w", err)
	}

	return nil
}

// expect waits for the first of wants, consumes the text up to and including it, and
// returns which one it was.
func (c *console) expect(ctx context.Context, timeout time.Duration, wants ...string) (string, error) {
	got, _, err := c.expectText(ctx, timeout, wants...)

	return got, err
}

// expectText is expect that also returns the text it skipped before the match.
func (c *console) expectText(ctx context.Context, timeout time.Duration, wants ...string) (string, string, error) {
	deadline := time.Now().Add(timeout)

	for {
		err := ctx.Err()
		if err != nil {
			return "", "", fmt.Errorf("BCO console: %w", err)
		}

		err = c.poll()
		if err != nil {
			return "", "", err
		}

		best, first := "", -1
		for _, want := range wants {
			if i := bytes.Index(c.buf, []byte(want)); i >= 0 && (first < 0 || i < first) {
				best, first = want, i
			}
		}

		if first >= 0 {
			skipped := string(c.buf[:first])
			c.buf = c.buf[first+len(best):]

			return best, skipped, nil
		}

		if time.Now().After(deadline) {
			return "", "", fmt.Errorf("%w %q within %s", errConsoleTimeout, wants, timeout)
		}

		err = sleepCtx(ctx, consolePoll)
		if err != nil {
			return "", "", err
		}
	}
}

func (c *console) powerCycle(ctx context.Context) error {
	err := c.f.Power(false)
	if err != nil {
		return fmt.Errorf("BCO power off: %w", err)
	}

	err = sleepCtx(ctx, c.d(time.Second))
	if err != nil {
		return err
	}

	c.buf = c.buf[:0]

	err = c.poll()
	if err != nil { // drop what the dying app printed
		return err
	}

	c.buf = c.buf[:0]

	err = c.f.Power(true)
	if err != nil {
		return fmt.Errorf("BCO power on: %w", err)
	}

	return nil
}

// enterLevel1 sends Ctrl-A until level 1 shows its menu. Level 1 starts the app by itself
// about two seconds after power-on, so this has to begin right away.
func (c *console) enterLevel1(ctx context.Context) error {
	deadline := time.Now().Add(c.d(level1Window))
	for time.Now().Before(deadline) {
		err := c.send(ctx, "\x01")
		if err != nil {
			return err
		}

		err = sleepCtx(ctx, c.d(ctrlAInterval))
		if err != nil {
			return err
		}

		err = c.poll()
		if err != nil {
			return err
		}

		if bytes.Contains(c.buf, []byte("Bootloader Level 1  Menu")) {
			return nil
		}
	}

	return errNoLevel1Menu
}

var appVersion = regexp.MustCompile(`APP=[^,\r\n]*`)

var menuLine = regexp.MustCompile(`(?m)^\s*([0-9a-z])\) (.+?)\s*$`)

// menuKey waits for the menu titled title to be printed up to its last line, and returns
// the key of the entry whose label is exactly label.
func (c *console) menuKey(ctx context.Context, title, label, last string) (string, error) {
	_, err := c.expect(ctx, c.d(menuTitleWait), title)
	if err != nil {
		return "", err
	}

	_, menu, err := c.expectText(ctx, c.d(menuBodyWait), last)
	if err != nil {
		return "", err
	}
	// The last entry itself is consumed with its key; put it back for the parser.
	return parseMenuKey(menu+last, label)
}

// parseMenuKey finds the key of the entry labelled exactly label in a printed menu.
func parseMenuKey(menu, label string) (string, error) {
	key := ""

	for _, match := range menuLine.FindAllStringSubmatch(strings.ReplaceAll(menu, "\r", "\n"), -1) {
		if match[2] == label {
			if key != "" {
				return "", fmt.Errorf("%w %q twice", errMenuTwice, label)
			}

			key = match[1]
		}
	}

	if key == "" {
		return "", fmt.Errorf("%w %q", errMenuMissing, label)
	}

	return key, nil
}
