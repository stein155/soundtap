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
func mfg(v byte, country, region string) []byte {
	page := make([]byte, mfgPage)
	for i := range page {
		page[i] = 0xff
	}
	page[0], page[1], page[2], page[3] = 0x5e, 0xb0, v, 0
	switch v {
	case 2:
		copy(page[0x150:], country+"\x00")
		copy(page[0x340:], region+"\x00")
	case 1:
		copy(page[0x1c:], country+"\x00")
	}
	return page
}

func TestCountryAndRegionFromManufacturingData(t *testing.T) {
	for name, tc := range map[string]struct {
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
		c, r := fromManufacturing(tc.page)
		if c != tc.country || r != tc.region {
			t.Errorf("%s: read %q %q", name, c, r)
		}
		if got := normalise(c, r); got != tc.normal {
			t.Errorf("%s: normalised %+v, want %+v", name, got, tc.normal)
		}
	}
}

func regRig(t *testing.T, page []byte, env string, withUboot bool) (*rig, Regulatory) {
	t.Helper()
	r := newRig(t, "rhino")
	mtd := "dev:    size   erasesize  name\nmtd5: 00020000 00020000 \"NAND.U-Boot-Env\"\nmtd12: 00100000 00020000 \"NAND.Manufacturing-Data\"\n"
	if withUboot {
		mtd += "mtd4: 00200000 00020000 \"NAND.U-Boot\"\n"
	}
	if err := os.WriteFile(r.p.ProcMTD, []byte(mtd), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.p.Dev, "mtd12"), page, 0o644); err != nil {
		t.Fatal(err)
	}
	r.sys.env = env
	r.m.ensureRegulatory(t.Context())
	return r, r.m.reg
}

func TestTheEnvironmentOverridesManufacturingData(t *testing.T) {
	r, reg := regRig(t, mfg(2, "NL", "US"), "bootcmd=run x\ncountry_code=JP\nregion_code=JP\n", true)
	if reg != (Regulatory{"JP", "JP"}) {
		t.Fatalf("regulatory = %+v", reg)
	}
	// The redundant environment is asked for first.
	if !r.sys.called("fw_printenv -c " + filepath.Join(r.p.State, "fw_env.config")) {
		t.Fatalf("calls = %q", r.sys.Calls())
	}
	if exists(filepath.Join(r.p.State, "fw_env.config")) {
		t.Fatal("fw_env.config left behind")
	}
	link, err := os.Readlink(filepath.Join(r.p.State, "firmware/ti-connectivity/wl18xx-conf.bin"))
	if err != nil || link != "/lib/firmware/ti-connectivity/wl18xx-conf.binJP" {
		t.Fatalf("board configuration link = %q, %v", link, err)
	}
	if got := r.read(r.p.FirmwareParam); got != filepath.Join(r.p.State, "firmware")+"\n" {
		t.Fatalf("firmware path = %q", got)
	}
}

func TestATornEnvironmentLeavesManufacturingData(t *testing.T) {
	_, reg := regRig(t, mfg(2, "NL", "KR"), "", true)
	if reg != (Regulatory{"NL", "KR"}) {
		t.Fatalf("regulatory = %+v", reg)
	}
}

func TestEnvConfigsInOrder(t *testing.T) {
	r := newRig(t, "rhino")
	var seen []string
	mtd := "mtd4: 00200000 00020000 \"NAND.U-Boot\"\nmtd5: 00020000 00020000 \"NAND.U-Boot-Env\"\n"
	if err := os.WriteFile(r.p.ProcMTD, []byte(mtd), 0o644); err != nil {
		t.Fatal(err)
	}
	readRegulatory(r.p, func(cfg string) ([]byte, error) {
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

func TestWirelessLinkWithoutACountryOpensNoSetupAP(t *testing.T) {
	for _, country := range []string{"", "N1", "00"} {
		t.Run(country, func(t *testing.T) {
			r := newRig(t, "binky")
			r.sys.env = "country_code=" + country + "\n"
			if err := os.WriteFile(r.p.ProcMTD, []byte("mtd5: 00020000 00020000 \"NAND.U-Boot-Env\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			r.start()
			r.eventually("the problem", func() bool { return strings.Contains(r.lastProblem(), "no regulatory country") })
			if r.sys.called("iw reg set") || r.sys.called("hostapd") {
				t.Fatalf("calls = %q", r.sys.Calls())
			}
		})
	}
}

func TestWirelessLinkWithACountrySetsIt(t *testing.T) {
	r, _ := regRig(t, mfg(2, "DE", "DF"), "", true)
	r.m.cfg.Variant = "binky"
	r.start()
	r.eventually("the setup AP", func() bool { return r.exists("setup-ap") })
	if !r.sys.called("iw reg set DE") {
		t.Fatalf("calls = %q", r.sys.Calls())
	}
}

func TestWirelessLinkWithASavedNetworkJoinsOnTheWorldDomain(t *testing.T) {
	r := newRig(t, "binky")
	r.save("home", "homepass1")
	r.sys.where["home"] = "ok"
	r.start()
	r.eventually("the join", func() bool { return r.joinCount() == 1 })
	if !r.sys.called("iw reg set 00") {
		t.Fatalf("calls = %q", r.sys.Calls())
	}
}

func TestSetupAPChannel(t *testing.T) {
	bss := func(freqs ...string) string {
		s := ""
		for _, f := range freqs {
			s += "BSS 00:11:22:33:44:55(on wlan0)\n\tlast seen: 10 ms ago\n\tfreq: " + f + "\n"
		}
		return s
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

func TestST20SetupAPStaysOnChannel6WithoutACountry(t *testing.T) {
	r, _ := regRig(t, mfg(1, "NL", ""), "", false)
	r.m.cfg.Variant = "spotty"
	r.sys.iwScan = "BSS x(on wlan0)\n\tfreq: 2437\n"
	r.start()
	r.eventually("the setup AP", func() bool { return r.exists("setup-ap") })
	conf := r.read(filepath.Join(r.p.Tmp, "hostapd.conf"))
	if !strings.Contains(conf, "channel=6\n") || strings.Contains(conf, "country_code") {
		t.Fatalf("hostapd.conf = %q", conf)
	}
}

func TestUdhcpcKeepsItsLease(t *testing.T) {
	args := strings.Join(udhcpcArgs("wlan0", "/run/soundtap-udhcpc.pid", "h"), " ")
	// -q exits once bound, so the lease is never renewed; -s would bypass Buildroot's
	// default.script, which sets address, route and DNS on renew (ADR 0063).
	if !strings.Contains(args, "-b") || strings.Contains(args, "-q") || strings.Contains(args, "-s ") {
		t.Fatalf("udhcpc %s", args)
	}
}
