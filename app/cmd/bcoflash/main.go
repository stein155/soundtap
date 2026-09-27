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

func main() {
	image := flag.String("image", "", "application .bcd (must be an allowed image)")
	tty := flag.String("tty", "/dev/ttyS0", "BCO UART (stock: /dev/ttyO5)")
	allow := flag.String("allow-sha256", "", "also accept the image with exactly this sha256 (approved experiment; must match the file)")
	yes := flag.Bool("yes", false, "really flash")
	cycle := flag.Bool("cycle", false, "only power-cycle the BCO (1 s off) and print its console")
	watch := flag.Duration("watch", 60*time.Second, "how long -cycle prints the console")
	flag.Parse()

	if *cycle {
		if err := powerCycle(*tty, *watch); err != nil {
			fail(err)
		}
		return
	}

	data, err := os.ReadFile(*image)
	if err != nil {
		fail(err)
	}
	shipped, _ := os.ReadFile(shippedImage) // absent off the speaker or on stock: no extra trust
	name, err := checkImage(data, shipped, *allow, *yes)
	if name != "" {
		fmt.Printf("[bcoflash] image %s: %s\n", *image, name)
	}
	if err != nil {
		fail(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	ver, err := bco.Flash(ctx, *tty, data, os.Stdout)
	if err != nil {
		fmt.Printf("\n[bcoflash] FAILED: %v\n", err)
		if errors.Is(err, bco.ErrAppDead) {
			os.Exit(3)
		}
		os.Exit(1)
	}
	fmt.Printf("\n[bcoflash] OK: BCO runs %s\n", ver)
}

// powerCycle is the stock loaders' reset (GPIO14 high 1 s, then low) without a flash, and
// shows what the BCO prints while it comes up: its app, or a loader that stays waiting.
func powerCycle(tty string, watch time.Duration) error {
	port, err := bco.SerialPort(context.Background(), tty)
	if err != nil {
		return err
	}
	defer port.Close()
	power, closePower, err := bco.MemPower()
	if err != nil {
		return err
	}
	defer closePower()
	fmt.Println("[bcoflash] BCO off")
	power(false)
	time.Sleep(time.Second)
	power(true)
	fmt.Println("[bcoflash] BCO on")
	buf := make([]byte, 512)
	for end := time.Now().Add(watch); time.Now().Before(end); {
		n, err := port.Read(buf)
		if err != nil {
			return err
		}
		if n == 0 {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		os.Stdout.Write(buf[:n])
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
func checkImage(data, shipped []byte, allow string, yes bool) (string, error) {
	if allow == "" && len(shipped) > 0 && bytes.Equal(data, shipped) {
		sum := sha256.Sum256(data)
		allow = hex.EncodeToString(sum[:])
	}
	if allow != "" {
		if err := bco.AllowImage(data, allow); err != nil {
			return "", err
		}
	}
	name, err := bco.CheckAppImage(data)
	if err != nil {
		return "", err
	}
	if !yes {
		return name, errors.New("refusing without -yes")
	}
	return name, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bcoflash:", err)
	os.Exit(1)
}
