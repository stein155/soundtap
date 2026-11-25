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

// Flash writes image into the BCO on this SCM: tty is the BCO UART. It holds the UART
// for the whole run, so Wi-Fi reads wait instead of typing into a bootloader menu.
func Flash(ctx context.Context, tty string, image []byte, log io.Writer) (string, error) {
	if _, err := CheckAppImage(image); err != nil {
		return "", err
	}

	if err := lockSerial(ctx); err != nil {
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
	if release, err := HoldLANWatch(); err != nil {
		fmt.Fprintf(log, "[bcoflash] could not hold the LAN watch: %v\n", err)
	} else {
		defer release()
	}

	lan := lanLink()
	fmt.Fprintf(log, "[bcoflash] LAN link before: %+v\n", lan)
	f := &Flasher{
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
	}

	ver, err := f.FlashApp(ctx, image)
	if errors.Is(err, ErrUpload) && ctx.Err() == nil {
		fmt.Fprintf(log, "\n[bcoflash] %v; writing the image over XMODEM instead\n", err)
		ver, err = f.XmodemApp(ctx, image)
	}

	restoreLAN(lan, log)

	return ver, err
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

	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}

	return out
}

func ip(args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "ip", args...)
	childproc.NetTool(cmd)
	out, err := cmd.CombinedOutput()

	return string(out), err
}

// addressLevel2 waits for bootloader level 2's USB link and gives it 192.168.1.101/24.
func addressLevel2(ctx context.Context, log io.Writer) error {
	for {
		for _, n := range netdevs() {
			if usbID(n) != "5432:1235" {
				continue
			}

			ip("link", "set", n, "up")
			ip("addr", "add", "192.168.1.101/24", "dev", n) // "exists" is fine
			fmt.Fprintf(log, "\n[bcoflash] level-2 link %s = 192.168.1.101/24\n", n)

			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("bootloader level 2 never showed its USB link: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

type link struct {
	MAC   string
	Addrs []string // "a.b.c.d/nn"
	GW    string
}

// lanLink records the app's LAN link: the USB netdev that is not level 2 and has an IPv4.
func lanLink() link {
	for _, n := range netdevs() {
		if id := usbID(n); id == ":" || id == "" || id == "5432:1235" {
			continue
		}

		mac, _ := os.ReadFile("/sys/class/net/" + n + "/address")
		l := link{MAC: strings.TrimSpace(string(mac))}

		out, _ := ip("-4", "-o", "addr", "show", "dev", n)
		for f := range strings.SplitSeq(out, "\n") {
			fs := strings.Fields(f)
			for i := range fs {
				if fs[i] == "inet" && i+1 < len(fs) {
					l.Addrs = append(l.Addrs, fs[i+1])
				}
			}
		}

		if out, _ := ip("route", "show", "default", "dev", n); strings.HasPrefix(out, "default via ") {
			l.GW = strings.Fields(out)[2]
		}

		if len(l.Addrs) > 0 {
			return l
		}
	}

	return link{}
}

// restoreLAN waits for the app's link (same MAC) to come back and re-applies what it had.
func restoreLAN(l link, log io.Writer) {
	if l.MAC == "" {
		return
	}

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		for _, n := range netdevs() {
			mac, _ := os.ReadFile("/sys/class/net/" + n + "/address")
			if strings.TrimSpace(string(mac)) != l.MAC {
				continue
			}

			ip("link", "set", n, "up")

			for _, a := range l.Addrs {
				ip("addr", "add", a, "dev", n)
			}

			if l.GW != "" {
				ip("route", "replace", "default", "via", l.GW, "dev", n)
			}

			fmt.Fprintf(log, "\n[bcoflash] LAN link %s restored: %v via %s\n", n, l.Addrs, l.GW)

			return
		}

		time.Sleep(time.Second)
	}

	fmt.Fprintf(log, "\n[bcoflash] LAN link %s did not come back within 2 minutes\n", l.MAC)
}
