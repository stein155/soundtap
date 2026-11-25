// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mfg is a manufacturing page of layout version v with country and region at their
// offsets, the rest erased flash.
func mfg(version byte, country, region string) []byte {
	page := make([]byte, mfgPage)
	for i := range page {
		page[i] = 0xff
	}

	page[0], page[1], page[2], page[3] = 0x5e, 0xb0, version, 0
	switch version {
	case 2:
		copy(page[0x150:], country+"\x00")
		copy(page[0x340:], region+"\x00")
	case 1:
		copy(page[0x1c:], country+"\x00")
	default:
		// An unknown layout: erased.
	}

	return page
}

func TestCountryAndRegionFromManufacturingData(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		page     []byte
		country  string
		region   string
		normal   Regulatory
		mfgFound bool
	}{
		"layout 2":       {mfg(2, "NL", "US"), "NL", "US", Regulatory{"NL", "US"}, true},
		"layout 1":       {mfg(1, "GB", ""), "GB", "", Regulatory{"GB", "DF"}, true},
		"unknown layout": {mfg(7, "NL", "US"), "", "", Regulatory{"00", "DF"}, false},
		"erased":         {mfg(0xff, "", ""), "", "", Regulatory{"00", "DF"}, false},
		"odd values":     {mfg(2, "nl", "EU"), "nl", "EU", Regulatory{"00", "DF"}, true},
	} {
		country, region := fromManufacturing(test.page)
		if country != test.country || region != test.region {
			t.Errorf("%s: read %q %q", name, country, region)
		}

		if got := normalise(country, region); got != test.normal {
			t.Errorf("%s: normalised %+v, want %+v", name, got, test.normal)
		}
	}
}

// ubootPart is the /proc/mtd line of NAND.U-Boot, for regRig.
const ubootPart = "mtd4: 00200000 00020000 \"NAND.U-Boot\"\n"

// regRig is a rig whose /proc/mtd has the environment, the manufacturing data and the
// extra lines given (ubootPart, or "").
func regRig(t *testing.T, page []byte, env, extra string) (*rig, Regulatory) {
	t.Helper()
	bench := newRig(t, "rhino")

	mtd := "dev:    size   erasesize  name\nmtd5: 00020000 00020000 \"NAND.U-Boot-Env\"\n" +
		"mtd12: 00100000 00020000 \"NAND.Manufacturing-Data\"\n" + extra

	err := os.WriteFile(bench.p.ProcMTD, []byte(mtd), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(bench.p.Dev, "mtd12"), page, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	bench.sys.env = env
	bench.m.ensureRegulatory(t.Context())

	return bench, bench.m.reg
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestTheEnvironmentOverridesManufacturingData(t *testing.T) {
	bench, reg := regRig(t, mfg(2, "NL", "US"), "bootcmd=run x\ncountry_code=JP\nregion_code=JP\n", ubootPart)
	if reg != (Regulatory{"JP", "JP"}) {
		t.Fatalf("regulatory = %+v", reg)
	}
	// The redundant environment is asked for first.
	if !bench.sys.called("fw_printenv -c " + filepath.Join(bench.p.State, "fw_env.config")) {
		t.Fatalf("calls = %q", bench.sys.Calls())
	}

	if exists(filepath.Join(bench.p.State, "fw_env.config")) {
		t.Fatal("fw_env.config left behind")
	}

	link, err := os.Readlink(filepath.Join(bench.p.State, "firmware", "ti-connectivity", "wl18xx-conf.bin"))
	if err != nil || link != "/lib/firmware/ti-connectivity/wl18xx-conf.binJP" {
		t.Fatalf("board configuration link = %q, %v", link, err)
	}

	if got := bench.read(bench.p.FirmwareParam); got != filepath.Join(bench.p.State, "firmware")+"\n" {
		t.Fatalf("firmware path = %q", got)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestATornEnvironmentLeavesManufacturingData(t *testing.T) {
	_, reg := regRig(t, mfg(2, "NL", "KR"), "", ubootPart)
	if reg != (Regulatory{"NL", "KR"}) {
		t.Fatalf("regulatory = %+v", reg)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestEnvConfigsInOrder(t *testing.T) {
	bench := newRig(t, "rhino")

	var seen []string

	mtd := "mtd4: 00200000 00020000 \"NAND.U-Boot\"\nmtd5: 00020000 00020000 \"NAND.U-Boot-Env\"\n"

	err := os.WriteFile(bench.p.ProcMTD, []byte(mtd), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	readRegulatory(&bench.p, func(cfg string) ([]byte, error) {
		b, _ := os.ReadFile(cfg)
		seen = append(seen, string(b))

		return nil, os.ErrInvalid
	})

	want := []string{
		"/dev/mtd5 0x0 0x8000 0x20000 1\n/dev/mtd4 0x1c0000 0x8000 0x20000 1\n",
		"/dev/mtd5 0x0 0x2000 0x20000 1\n",
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("configs = %q", seen)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestWirelessLinkWithoutACountryOpensNoSetupAP(t *testing.T) {
	for _, country := range []string{"", "N1", "00"} {
		t.Run(country, func(t *testing.T) {
			bench := newRig(t, "binky")

			bench.sys.env = "country_code=" + country + "\n"

			err := os.WriteFile(bench.p.ProcMTD, []byte("mtd5: 00020000 00020000 \"NAND.U-Boot-Env\"\n"), 0o644)
			if err != nil {
				t.Fatal(err)
			}

			bench.start()
			bench.eventually("the problem", func() bool {
				return strings.Contains(bench.lastProblem(), "no regulatory country")
			})

			if bench.sys.called("iw reg set") || bench.sys.called("hostapd") {
				t.Fatalf("calls = %q", bench.sys.Calls())
			}
		})
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestWirelessLinkWithACountrySetsIt(t *testing.T) {
	bench, _ := regRig(t, mfg(2, "DE", "DF"), "", ubootPart)
	bench.m.cfg.Variant = "binky"
	bench.start()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") })

	if !bench.sys.called("iw reg set DE") {
		t.Fatalf("calls = %q", bench.sys.Calls())
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestWirelessLinkWithASavedNetworkJoinsOnTheWorldDomain(t *testing.T) {
	bench := newRig(t, "binky")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.start()
	bench.eventually("the join", func() bool { return bench.joinCount() == 1 })

	if !bench.sys.called("iw reg set 00") {
		t.Fatalf("calls = %q", bench.sys.Calls())
	}
}

func TestSetupAPChannel(t *testing.T) {
	t.Parallel()

	bss := func(freqs ...string) string {
		var out strings.Builder
		for _, f := range freqs {
			out.WriteString("BSS 00:11:22:33:44:55(on wlan0)\n\tlast seen: 10 ms ago\n\tfreq: " + f + "\n")
		}

		return out.String()
	}
	for scan, want := range map[string]int{
		bss("2437", "2437", "2412", "2462", "2462"): 1,
		bss("2437", "2412"):                         11,
		bss("2437.0", "2412.0"):                     11,
		bss("2412", "2437", "2462"):                 6,
		bss("5180", "2422"):                         6,
		"":                                          6,
	} {
		if got := apChannel(scan); got != want {
			t.Errorf("%q: channel %d, want %d", scan, got, want)
		}
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestST20SetupAPStaysOnChannel6WithoutACountry(t *testing.T) {
	bench, _ := regRig(t, mfg(1, "NL", ""), "", "")
	bench.m.cfg.Variant = "spotty"
	bench.sys.iwScan = "BSS x(on wlan0)\n\tfreq: 2437\n"
	bench.start()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") })

	conf := bench.read(filepath.Join(bench.p.Tmp, "hostapd.conf"))
	if !strings.Contains(conf, "channel=6\n") || strings.Contains(conf, "country_code") {
		t.Fatalf("hostapd.conf = %q", conf)
	}
}

func TestUdhcpcKeepsItsLease(t *testing.T) {
	t.Parallel()

	args := strings.Join(udhcpcArgs("wlan0", "/run/soundtap-udhcpc.pid", "h"), " ")
	// -q exits once bound, so the lease is never renewed; -s would bypass Buildroot's
	// default.script, which sets address, route and DNS on renew (ADR 0063).
	if !strings.Contains(args, "-b") || strings.Contains(args, "-q") || strings.Contains(args, "-s ") {
		t.Fatalf("udhcpc %s", args)
	}
}
