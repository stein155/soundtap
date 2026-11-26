// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

// The SCM reaches the BCO over USB CDC-ECM, not a wire: the app enumerates as the LAN
// link (eth0), bootloader level 2 as USB 5432:1235 (stock names it eth7 and gives it
// 192.168.1.101/24). Every BCO power cycle therefore replaces the netdev, so a flash
// addresses the level-2 link itself and puts the LAN link's addresses back afterwards.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/childproc"
)

// level2USB is the USB vendor:product of bootloader level 2's link.
const level2USB = "5432:1235"

// The flash's network waits: for level 2's link, and for the app's LAN link afterwards.
const (
	level2LinkPoll = 500 * time.Millisecond
	lanRestoreWait = 2 * time.Minute
)

// Flash writes image into the BCO on this SCM: tty is the BCO UART. It holds the UART
// for the whole run, so Wi-Fi reads wait instead of typing into a bootloader menu.
func Flash(ctx context.Context, tty string, image []byte, log io.Writer) (string, error) {
	_, err := CheckAppImage(image)
	if err != nil {
		return "", err
	}

	err = lockSerial(ctx)
	if err != nil {
		return "", err
	}

	defer unlockSerial()

	port, err := SerialPort(ctx, tty)
	if err != nil {
		return "", err
	}
	defer port.Close()

	power, unmap, err := Power()
	if err != nil {
		return "", err
	}
	defer unmap()

	// soundtapd's LAN watch would see the link drop and power-cycle the BCO mid-flash.
	release, holdErr := HoldLANWatch()
	if holdErr != nil {
		fmt.Fprintf(log, "[bcoflash] could not hold the LAN watch: %v\n", holdErr)
	} else {
		defer release()
	}

	// The LAN link is read and put back even when the flash is cancelled.
	netCtx := context.WithoutCancel(ctx)
	lan := lanLink(netCtx)
	fmt.Fprintf(log, "[bcoflash] LAN link before: %+v\n", lan)
	flasher := scmFlasher(port, power, log)

	ver, err := flasher.FlashApp(ctx, image)
	if errors.Is(err, ErrUpload) && ctx.Err() == nil {
		fmt.Fprintf(log, "\n[bcoflash] %v; writing the image over XMODEM instead\n", err)
		ver, err = flasher.XmodemApp(ctx, image)
	}

	restoreLAN(netCtx, lan, log)

	return ver, err
}

// scmFlasher is the Flasher on this SCM's UART and supply switch, uploading over level 2's
// USB link.
func scmFlasher(port io.ReadWriter, power func(on bool) error, log io.Writer) *Flasher {
	return &Flasher{
		Serial: port,
		Power:  power,
		Log:    log,
		Upload: func(ctx context.Context, image []byte) error {
			err := addressLevel2(ctx, log)
			if err != nil {
				return err
			}

			return UploadTFTP(ctx, image)
		},
		Scale: 0,
	}
}

// usbID is the USB vendor:product behind a netdev, "" if it is not USB.
func usbID(iface string) string {
	dev, err := filepath.EvalSymlinks("/sys/class/net/" + iface + "/device")
	if err != nil {
		return ""
	}

	v, _ := os.ReadFile(filepath.Join(dev, "..", "idVendor"))
	p, _ := os.ReadFile(filepath.Join(dev, "..", "idProduct"))

	return strings.TrimSpace(string(v)) + ":" + strings.TrimSpace(string(p))
}

func netdevs() []string {
	ents, _ := os.ReadDir("/sys/class/net")

	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}

	return out
}

// ip runs ip(8) with args and returns its combined output.
func ip(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "ip", args...)
	childproc.NetTool(cmd)
	out, err := cmd.CombinedOutput()

	return string(out), err
}

// addressLevel2 waits for bootloader level 2's USB link and gives it 192.168.1.101/24.
func addressLevel2(ctx context.Context, log io.Writer) error {
	// The address goes on even when the upload's ctx ends meanwhile.
	ipCtx := context.WithoutCancel(ctx)

	for {
		for _, netdev := range netdevs() {
			if usbID(netdev) != level2USB {
				continue
			}

			_, _ = ip(ipCtx, "link", "set", netdev, "up")
			_, _ = ip(ipCtx, "addr", "add", "192.168.1.101/24", "dev", netdev) // "exists" is fine
			fmt.Fprintf(log, "\n[bcoflash] level-2 link %s = 192.168.1.101/24\n", netdev)

			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("bootloader level 2 never showed its USB link: %w", ctx.Err())
		case <-time.After(level2LinkPoll):
		}
	}
}

type link struct {
	MAC   string
	Addrs []string // "a.b.c.d/nn"
	GW    string
}

// lanLink records the app's LAN link: the USB netdev that is not level 2 and has an IPv4.
func lanLink(ctx context.Context) link {
	for _, netdev := range netdevs() {
		if id := usbID(netdev); id == ":" || id == "" || id == level2USB {
			continue
		}

		mac, _ := os.ReadFile("/sys/class/net/" + netdev + "/address")
		lan := link{MAC: strings.TrimSpace(string(mac)), Addrs: nil, GW: ""}

		out, _ := ip(ctx, "-4", "-o", "addr", "show", "dev", netdev)
		for f := range strings.SplitSeq(out, "\n") {
			fs := strings.Fields(f)
			for i := range fs {
				if fs[i] == "inet" && i+1 < len(fs) {
					lan.Addrs = append(lan.Addrs, fs[i+1])
				}
			}
		}

		if out, _ := ip(ctx, "route", "show", "default", "dev", netdev); strings.HasPrefix(out, "default via ") {
			lan.GW = strings.Fields(out)[2]
		}

		if len(lan.Addrs) > 0 {
			return lan
		}
	}

	return link{MAC: "", Addrs: nil, GW: ""}
}

// restoreLAN waits for the app's link (same MAC) to come back and re-applies what it had.
func restoreLAN(ctx context.Context, saved link, log io.Writer) {
	if saved.MAC == "" {
		return
	}

	deadline := time.Now().Add(lanRestoreWait)
	for time.Now().Before(deadline) {
		for _, netdev := range netdevs() {
			mac, _ := os.ReadFile("/sys/class/net/" + netdev + "/address")
			if strings.TrimSpace(string(mac)) != saved.MAC {
				continue
			}

			// Best effort, step by step: a step that fails is not retried.
			_, _ = ip(ctx, "link", "set", netdev, "up")

			for _, a := range saved.Addrs {
				_, _ = ip(ctx, "addr", "add", a, "dev", netdev)
			}

			if saved.GW != "" {
				_, _ = ip(ctx, "route", "replace", "default", "via", saved.GW, "dev", netdev)
			}

			fmt.Fprintf(log, "\n[bcoflash] LAN link %s restored: %v via %s\n", netdev, saved.Addrs, saved.GW)

			return
		}

		time.Sleep(time.Second)
	}

	fmt.Fprintf(log, "\n[bcoflash] LAN link %s did not come back within 2 minutes\n", saved.MAC)
}
