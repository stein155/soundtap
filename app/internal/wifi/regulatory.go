// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// Regulatory is the radio's country and the WiLink board configuration's region.
//
// Both come from this speaker's own Manufacturing-Data, which stock's U-Boot passed on as
// country= and region=; country_code and region_code in the U-Boot environment override it,
// as on stock. Our kernel command line carries no country of its own. Two layouts are known:
// version 2 (ST10, Wireless Link, ST300) holds the country at 0x150 and the region at 0x340;
// version 1 (ST20) holds the country at 0x1c and no region. Neither, and no override: the
// world domain 00, and the DF board configuration.
type Regulatory struct {
	Country string // two capitals, or 00
	Region  string // US, JP, KR or DF
}

const (
	mfgPartition   = "NAND.Manufacturing-Data"
	envPartition   = "NAND.U-Boot-Env"
	ubootPartition = "NAND.U-Boot"
	mfgPage        = 2048
)

// mfgField is the string at off in the manufacturing page, up to NUL, erased flash or a
// newline, and at most 8 bytes.
func mfgField(page []byte, off int) string {
	if off >= len(page) {
		return ""
	}
	f := page[off:min(off+8, len(page))]
	if i := bytes.IndexAny(f, "\x00\xff\n"); i >= 0 {
		f = f[:i]
	}
	return string(f)
}

// fromManufacturing reads the country and region out of the first page of
// NAND.Manufacturing-Data.
func fromManufacturing(page []byte) (country, region string) {
	if len(page) < 3 {
		return "", ""
	}
	switch page[2] {
	case 2:
		return mfgField(page, 0x150), mfgField(page, 0x340)
	case 1:
		return mfgField(page, 0x1c), ""
	}
	return "", ""
}

// normalise keeps a country of two capitals or 00 and a region the board configuration
// has a file for.
func normalise(country, region string) Regulatory {
	r := Regulatory{Country: "00", Region: "DF"}
	if country == "00" || len(country) == 2 && country[0] >= 'A' && country[0] <= 'Z' && country[1] >= 'A' && country[1] <= 'Z' {
		r.Country = country
	}
	switch region {
	case "US", "JP", "KR", "DF":
		r.Region = region
	}
	return r
}

// readRegulatory finds the country and region: manufacturing data, then the U-Boot
// environment's override. printenv runs fw_printenv with the config file it is given.
func readRegulatory(p Paths, printenv func(cfg string) ([]byte, error)) (Regulatory, string, string) {
	parts := map[string]flash.Partition{}
	if f, err := os.Open(p.ProcMTD); err == nil {
		parts, _ = flash.ParseProcMTD(f)
		_ = f.Close()
	}
	var country, region string
	if part, ok := parts[mfgPartition]; ok {
		if f, err := os.Open(filepath.Join(p.Dev, fmt.Sprintf("mtd%d", part.Index))); err == nil {
			page := make([]byte, mfgPage)
			n, _ := io.ReadFull(f, page)
			_ = f.Close()
			country, region = fromManufacturing(page[:n])
		}
	}
	if env := readEnv(p, parts, printenv); env != nil {
		if v, _ := env.Get("country_code"); v != "" {
			country = v
		}
		if v, _ := env.Get("region_code"); v != "" {
			region = v
		}
	}
	return normalise(country, region), country, region
}

// readEnv reads the U-Boot environment in whichever format the board has (ADR 0010): our
// U-Boot keeps two 32 KiB copies, the primary at the start of NAND.U-Boot-Env and the other
// in NAND.U-Boot's last erase block; Bose's, and ours before that, one 8 KiB copy. The first
// config that fw_printenv reads is the one. A torn environment reads as none.
func readEnv(p Paths, parts map[string]flash.Partition, printenv func(cfg string) ([]byte, error)) *ubootenv.Env {
	env, ok := parts[envPartition]
	if !ok {
		return nil
	}
	cfg := filepath.Join(p.State, "fw_env.config")
	defer os.Remove(cfg)
	var configs []string
	if uboot, ok := parts[ubootPartition]; ok {
		configs = append(configs, fmt.Sprintf("/dev/mtd%d 0x0 0x8000 0x20000 1\n/dev/mtd%d 0x1c0000 0x8000 0x20000 1\n", env.Index, uboot.Index))
	}
	configs = append(configs, fmt.Sprintf("/dev/mtd%d 0x0 0x2000 0x20000 1\n", env.Index))
	for _, c := range configs {
		if err := os.WriteFile(cfg, []byte(c), 0o600); err != nil {
			return nil
		}
		out, err := printenv(cfg)
		if err != nil {
			continue
		}
		if e, err := ubootenv.ParseText(out); err == nil {
			return e
		}
	}
	return nil
}

// linkBoardConfig points the WiLink's wl18xx-conf.bin at the region's file. / is read-only,
// so the link lives under the state directory and the kernel's firmware loader is told to
// look there first; every other file it asks for is not there and comes from /lib/firmware
// as before. The image's own wl18xx-conf.bin is DF.
func linkBoardConfig(p Paths, region string) error {
	dir := filepath.Join(p.State, "firmware", "ti-connectivity")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	link := filepath.Join(dir, "wl18xx-conf.bin")
	_ = os.Remove(link)
	if err := os.Symlink(filepath.Join(p.Firmware, "wl18xx-conf.bin"+region), link); err != nil {
		return err
	}
	// init.d/soundtap sets the parameter at boot, since soundtapd's user may not write it
	// (ADR 0066); written here only when it says something else, as a root run does.
	want := filepath.Join(p.State, "firmware")
	if cur, err := os.ReadFile(p.FirmwareParam); err == nil && strings.TrimSpace(string(cur)) == want {
		return nil
	}
	return os.WriteFile(p.FirmwareParam, []byte(want+"\n"), 0o644)
}

// variant is the variant= word of the kernel command line: binky is the Wireless Link,
// spotty the ST20.
func variant(cmdline string) string {
	b, _ := os.ReadFile(cmdline)
	for _, w := range strings.Fields(string(b)) {
		if v, ok := strings.CutPrefix(w, "variant="); ok {
			return v
		}
	}
	return ""
}

// setCountry tells cfg80211 the country. cfg80211 is built in and asks for regulatory.db
// before the root filesystem is mounted, fails, and never asks again on its own: without the
// reload every `iw reg set` is accepted and ignored, leaving the radio on the world domain
// (no channels 12 and 13). A Wireless Link that has to open its setup AP needs a real
// country: without one it opens none.
func (m *Manager) setCountry(ctx context.Context, setupOnly bool) error {
	if _, err := m.run(ctx, "iw", "reg", "reload"); err != nil {
		m.log.Warn("regulatory.db reload failed; the country setting may not apply", "err", err)
	}
	c := m.reg.Country
	if m.variant == "binky" && setupOnly {
		if c == "00" {
			return fmt.Errorf("the Wireless Link has no regulatory country; setup AP unavailable")
		}
		if _, err := m.run(ctx, "iw", "reg", "set", c); err != nil {
			return fmt.Errorf("regulatory country %s rejected; setup AP unavailable: %w", c, err)
		}
		m.log.Info("regulatory country requested", "country", c)
		return nil
	}
	if _, err := m.run(ctx, "iw", "reg", "set", c); err != nil {
		m.log.Error("regulatory country rejected; continuing on the kernel's world domain", "country", c, "err", err)
		return nil
	}
	m.log.Info("regulatory country", "country", c)
	return nil
}
