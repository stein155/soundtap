// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"bytes"
	"context"
	"errors"
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
	// mfgFieldMax is the longest field read from the manufacturing page.
	mfgFieldMax = 8
	// The manufacturing page's layout version at byte 2, and where each keeps its fields.
	mfgVersionAt     = 2
	mfgV1            = 1
	mfgV2            = 2
	mfgV1CountryAt   = 0x1c
	mfgV2CountryAt   = 0x150
	mfgV2RegionAt    = 0x340
	countryCodeChars = 2
)

// errNoCountry is a Wireless Link that would open its setup AP without a country.
var errNoCountry = errors.New("the Wireless Link has no regulatory country; setup AP unavailable")

// mfgField is the string at off in the manufacturing page, up to NUL, erased flash or a
// newline, and at most 8 bytes.
func mfgField(page []byte, off int) string {
	if off >= len(page) {
		return ""
	}

	f := page[off:min(off+mfgFieldMax, len(page))]
	if i := bytes.IndexAny(f, "\x00\xff\n"); i >= 0 {
		f = f[:i]
	}

	return string(f)
}

// fromManufacturing reads the country and region out of the first page of
// NAND.Manufacturing-Data.
func fromManufacturing(page []byte) (string, string) {
	if len(page) <= mfgVersionAt {
		return "", ""
	}

	switch page[mfgVersionAt] {
	case mfgV2:
		return mfgField(page, mfgV2CountryAt), mfgField(page, mfgV2RegionAt)
	case mfgV1:
		return mfgField(page, mfgV1CountryAt), ""
	default:
		return "", ""
	}
}

// normalise keeps a country of two capitals or 00 and a region the board configuration
// has a file for.
func normalise(country, region string) Regulatory {
	reg := Regulatory{Country: "00", Region: "DF"}

	capital := func(c byte) bool { return c >= 'A' && c <= 'Z' }
	if country == "00" || len(country) == countryCodeChars && capital(country[0]) && capital(country[1]) {
		reg.Country = country
	}

	switch region {
	case "US", "JP", "KR", "DF":
		reg.Region = region
	default:
		// No board configuration for it: DF.
	}

	return reg
}

// readRegulatory finds the country and region: manufacturing data, then the U-Boot
// environment's override. printenv runs fw_printenv with the config file it is given.
func readRegulatory(paths *Paths, printenv func(cfg string) ([]byte, error)) (Regulatory, string, string) {
	parts := map[string]flash.Partition{}

	mtd, err := os.Open(paths.ProcMTD)
	if err == nil {
		parts, _ = flash.ParseProcMTD(mtd)
		_ = mtd.Close()
	}

	var country, region string

	if part, ok := parts[mfgPartition]; ok {
		f, err := os.Open(filepath.Join(paths.Dev, fmt.Sprintf("mtd%d", part.Index)))
		if err == nil {
			page := make([]byte, mfgPage)
			n, _ := io.ReadFull(f, page)
			_ = f.Close()
			country, region = fromManufacturing(page[:n])
		}
	}

	if env := readEnv(paths, parts, printenv); env != nil {
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
func readEnv(paths *Paths, parts map[string]flash.Partition, printenv func(cfg string) ([]byte, error)) *ubootenv.Env {
	env, ok := parts[envPartition]
	if !ok {
		return nil
	}

	cfg := filepath.Join(paths.State, "fw_env.config")
	defer os.Remove(cfg)

	var configs []string
	if uboot, ok := parts[ubootPartition]; ok {
		configs = append(configs, fmt.Sprintf(
			"/dev/mtd%d 0x0 0x8000 0x20000 1\n/dev/mtd%d 0x1c0000 0x8000 0x20000 1\n", env.Index, uboot.Index))
	}

	configs = append(configs, fmt.Sprintf("/dev/mtd%d 0x0 0x2000 0x20000 1\n", env.Index))
	for _, c := range configs {
		err := os.WriteFile(cfg, []byte(c), privateFileMode)
		if err != nil {
			return nil
		}

		out, err := printenv(cfg)
		if err != nil {
			continue
		}

		parsed, err := ubootenv.ParseText(out)
		if err == nil {
			return parsed
		}
	}

	return nil
}

// linkBoardConfig points the WiLink's wl18xx-conf.bin at the region's file. / is read-only,
// so the link lives under the state directory and the kernel's firmware loader is told to
// look there first; every other file it asks for is not there and comes from /lib/firmware
// as before. The image's own wl18xx-conf.bin is DF.
func linkBoardConfig(paths *Paths, region string) error {
	dir := filepath.Join(paths.State, "firmware", "ti-connectivity")

	err := os.MkdirAll(dir, publicDirMode)
	if err != nil {
		return fmt.Errorf("board configuration directory: %w", err)
	}

	link := filepath.Join(dir, "wl18xx-conf.bin")

	_ = os.Remove(link)

	err = os.Symlink(filepath.Join(paths.Firmware, "wl18xx-conf.bin"+region), link)
	if err != nil {
		return fmt.Errorf("board configuration link: %w", err)
	}
	// init.d/soundtap sets the parameter at boot, since soundtapd's user may not write it
	// (ADR 0066); written here only when it says something else, as a root run does.
	want := filepath.Join(paths.State, "firmware")

	cur, err := os.ReadFile(paths.FirmwareParam)
	if err == nil && strings.TrimSpace(string(cur)) == want {
		return nil
	}

	err = os.WriteFile(paths.FirmwareParam, []byte(want+"\n"), publicFileMode)
	if err != nil {
		return fmt.Errorf("firmware search path: %w", err)
	}

	return nil
}

// variant is the variant= word of the kernel command line: binky is the Wireless Link,
// spotty the ST20.
func variant(cmdline string) string {
	b, _ := os.ReadFile(cmdline)
	for w := range strings.FieldsSeq(string(b)) {
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
func (m *Manager) setCountry(ctx context.Context) {
	m.reloadRegulatory(ctx)

	country := m.reg.Country

	_, err := m.runCmd(ctx, "iw", "reg", "set", country)
	if err != nil {
		m.log.Error("regulatory country rejected; continuing on the kernel's world domain",
			"country", country, "err", err)

		return
	}

	m.log.Info("regulatory country", "country", country)
}

// setSetupCountry is setCountry for a Wireless Link that has only its setup AP to open: it
// needs a real country, so none, or one the kernel rejects, is an error.
func (m *Manager) setSetupCountry(ctx context.Context) error {
	m.reloadRegulatory(ctx)

	country := m.reg.Country
	if country == "00" {
		return errNoCountry
	}

	_, err := m.runCmd(ctx, "iw", "reg", "set", country)
	if err != nil {
		return fmt.Errorf("regulatory country %s rejected; setup AP unavailable: %w", country, err)
	}

	m.log.Info("regulatory country requested", "country", country)

	return nil
}

// reloadRegulatory makes cfg80211 read regulatory.db.
func (m *Manager) reloadRegulatory(ctx context.Context) {
	_, err := m.runCmd(ctx, "iw", "reg", "reload")
	if err != nil {
		m.log.Warn("regulatory.db reload failed; the country setting may not apply", "err", err)
	}
}
