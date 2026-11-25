// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// bcoflash writes an allowed application image into the ST20's BCO (bco allowlist), the
// way stock's SMSC_Update does. It runs on the SCM, under
// SoundTap or stock. On stock, stop BoseApp first: its BCO reset timer power-cycles a BCO
// that stops answering.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/bco"
)

// Exit codes besides 0 (flashed) and 1 (failed).
const exitAppDead = 3 // the flash ended with no application answering (bco.ErrAppDead)

const (
	defaultWatch = 60 * time.Second // how long -cycle prints the console by default
	flashTimeout = 15 * time.Minute // the longest a flash may take
	consoleBuf   = 512              // bytes read from the console at a time
	consolePoll  = 20 * time.Millisecond
)

// errNeedsYes refuses an image that passed every check but was not confirmed with -yes.
var errNeedsYes = errors.New("refusing without -yes")

// imageApproval is what the operator passed besides the image: an extra approved sha256
// and whether they confirmed the flash.
type imageApproval struct {
	allow string // also accept the image with exactly this sha256
	yes   bool   // really flash
}

// options are the command line.
type options struct {
	image, tty string
	approval   imageApproval
	cycle      bool
	watch      time.Duration
}

func main() {
	image := flag.String("image", "", "application .bcd (must be an allowed image)")
	tty := flag.String("tty", "/dev/ttyS0", "BCO UART (stock: /dev/ttyO5)")
	allow := flag.String("allow-sha256", "",
		"also accept the image with exactly this sha256 (approved experiment; must match the file)")
	yes := flag.Bool("yes", false, "really flash")
	cycle := flag.Bool("cycle", false, "only power-cycle the BCO (1 s off) and print its console")
	watch := flag.Duration("watch", defaultWatch, "how long -cycle prints the console")

	flag.Parse()

	os.Exit(run(options{
		image:    *image,
		tty:      *tty,
		approval: imageApproval{allow: *allow, yes: *yes},
		cycle:    *cycle,
		watch:    *watch,
	}))
}

// run is the command; it returns the process's exit code so its deferred calls run
// before the exit.
func run(opt options) int {
	if opt.cycle {
		err := powerCycle(opt.tty, opt.watch)
		if err != nil {
			return fail(err)
		}

		return 0
	}

	data, err := os.ReadFile(opt.image)
	if err != nil {
		return fail(err)
	}

	shipped, _ := os.ReadFile(shippedImage) // absent off the speaker or on stock: no extra trust

	name, err := checkImage(data, shipped, opt.approval)
	if name != "" {
		fmt.Printf("[bcoflash] image %s: %s\n", opt.image, name)
	}

	if err != nil {
		return fail(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, flashTimeout)
	defer cancel()

	ver, err := bco.Flash(ctx, opt.tty, data, os.Stdout)
	if err != nil {
		fmt.Printf("\n[bcoflash] FAILED: %v\n", err)

		if errors.Is(err, bco.ErrAppDead) {
			return exitAppDead
		}

		return 1
	}

	fmt.Printf("\n[bcoflash] OK: BCO runs %s\n", ver)

	return 0
}

// powerCycle is the stock loaders' reset (GPIO14 high 1 s, then low) without a flash, and
// shows what the BCO prints while it comes up: its app, or a loader that stays waiting.
func powerCycle(tty string, watch time.Duration) error {
	port, err := bco.SerialPort(context.Background(), tty)
	if err != nil {
		return fmt.Errorf("open %s: %w", tty, err)
	}
	defer port.Close()

	power, closePower, err := bco.MemPower()
	if err != nil {
		return fmt.Errorf("BCO power: %w", err)
	}
	defer closePower()

	// A failed power write shows on the console below: the BCO does not come back.
	fmt.Println("[bcoflash] BCO off")

	_ = power(false)

	time.Sleep(time.Second)

	_ = power(true)

	fmt.Println("[bcoflash] BCO on")

	buf := make([]byte, consoleBuf)
	for end := time.Now().Add(watch); time.Now().Before(end); {
		count, err := port.Read(buf)
		if err != nil {
			return fmt.Errorf("read the BCO console: %w", err)
		}

		if count == 0 {
			time.Sleep(consolePoll)

			continue
		}

		_, _ = os.Stdout.Write(buf[:count]) // the operator's terminal
	}

	fmt.Println("\n[bcoflash] watch done")

	return nil
}

// shippedImage is the BCO image the signed OS carries; soundtapd's AutoInstall trusts it for
// the same reason, so restoring it by hand needs no -allow-sha256 either.
const shippedImage = "/usr/share/soundtap/bco-app.bcd"

// checkImage names the application image in data and refuses one that is not on the
// allowlist, not the OS's own image (shipped), nor approved by its exact sha256 in allow,
// or any image without yes.
func checkImage(data, shipped []byte, approval imageApproval) (string, error) {
	allow := approval.allow
	if allow == "" && len(shipped) > 0 && bytes.Equal(data, shipped) {
		sum := sha256.Sum256(data)
		allow = hex.EncodeToString(sum[:])
	}

	if allow != "" {
		err := bco.AllowImage(data, allow)
		if err != nil {
			return "", fmt.Errorf("approve the image: %w", err)
		}
	}

	name, err := bco.CheckAppImage(data)
	if err != nil {
		return "", fmt.Errorf("check the image: %w", err)
	}

	if !approval.yes {
		return name, errNeedsYes
	}

	return name, nil
}

// fail reports err and returns the exit code of a failed run.
func fail(err error) int {
	fmt.Fprintln(os.Stderr, "bcoflash:", err)

	return 1
}
