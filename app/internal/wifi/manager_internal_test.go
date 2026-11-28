// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/wpactrl"
	"github.com/stein155/soundtap/app/internal/wpactrl/wpactrltest"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func countPrefix(calls []string, prefix string) int {
	count := 0

	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			count++
		}
	}

	return count
}

// The IEEE 802.11i test vector: what wpa_passphrase gives for "password" on "IEEE".
func TestPassphraseBecomesTheKeyWpaPassphraseGives(t *testing.T) {
	t.Parallel()

	decoded, err := decode(NewRecord("IEEE", "", "password"))
	if err != nil {
		t.Fatal(err)
	}

	if decoded.keyHex != "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e" {
		t.Fatalf("key = %s", decoded.keyHex)
	}

	raw := strings.Repeat("Ab", 32)

	decoded, err = decode(NewRecord("home", "", raw))
	if err != nil || !strings.EqualFold(decoded.keyHex, raw) {
		t.Fatalf("raw key = %q, %v", decoded.keyHex, err)
	}

	decoded, err = decode(NewRecord("cafe", "", ""))
	if err != nil || decoded.keyHex != "" {
		t.Fatalf("open network = %q, %v", decoded.keyHex, err)
	}

	for name, rec := range map[string]Record{
		"bad base64":       {SSIDB64: "!!", PSKB64: "", HexPSK: "", Security: "", WEPKeyIndex: 0},
		"short passphrase": NewRecord("home", "", "short"),
		"bad hex": {
			SSIDB64: b64("home"), PSKB64: "", HexPSK: strings.Repeat("z", 64), Security: "", WEPKeyIndex: 0,
		},
	} {
		_, err := decode(rec)
		if err == nil {
			t.Errorf("%s decoded", name)
		}
	}
}

// A WEP network (ADR 0136) is stored with its key as typed and SECURITY=wep, the known list's
// fourth field says wep, and wpa_supplicant gets the key in hex.
func TestWEPRecord(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ key, hex string }{
		{"abcde", "6162636465"},
		{"0123456789abc", "30313233343536373839616263"},
		{"A1B2C3D4E5", "a1b2c3d4e5"},
		{strings.Repeat("Fe", 13), strings.Repeat("fe", 13)},
	} {
		rec := NewRecord("old net", SecurityWEP, test.key)
		if rec.Security != SecurityWEP || rec.HexPSK != "" {
			t.Fatalf("%q: record %+v", test.key, rec)
		}

		conf := string(ConfBytes(rec))
		if !strings.Contains(conf, "\nSECURITY=wep\n") || ParseConf([]byte(conf)) != rec {
			t.Errorf("%q: wifi.conf %q", test.key, conf)
		}

		if line := rec.Line(); !strings.HasSuffix(line, " wep") || ParseKnown([]byte(line + "\n"))[0] != rec {
			t.Errorf("%q: known line %q", test.key, line)
		}

		decoded, err := decode(rec)
		if err != nil || decoded.wepHex != test.hex || decoded.keyHex != "" {
			t.Errorf("%q: decoded %+v, %v", test.key, decoded, err)
		}
	}

	for _, bad := range []string{"", "abcd", "abcdef", "12345678901", "zz23456789", "abc\x01e"} {
		if IsWEPKey(bad) {
			t.Errorf("%q is a WEP key", bad)
		}

		_, err := decode(NewRecord("old net", SecurityWEP, bad))
		if err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
	// a WPA passphrase of five characters is no network at all, as before
	_, err := decode(NewRecord("home", "", "abcde"))
	if err == nil {
		t.Error("a 5-character WPA passphrase decoded")
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestRawKeyJoinsWithoutSAE(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", strings.Repeat("ab", 32))
	bench.sys.where["home"] = "ok"
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })

	cmds := bench.sys.supServer().Commands()
	if !slices.Contains(cmds, "SET_NETWORK 0 key_mgmt WPA-PSK WPA-PSK-SHA256") {
		t.Errorf("a raw key's network: %q", cmds)
	}

	for _, c := range cmds {
		if strings.Contains(c, "sae_password") {
			t.Errorf("a raw key has no SAE password: %q", c)
		}
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestWEPNetworkJoins(t *testing.T) {
	bench := newRig(t, "rhino")

	err := WriteConf(bench.conf, NewRecord("oldnet", SecurityWEP, "abcde"))
	if err != nil {
		t.Fatal(err)
	}

	bench.sys.where["oldnet"] = "ok"
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })

	cmds := bench.sys.supServer().Commands()
	for _, want := range []string{
		"SET_NETWORK 0 key_mgmt NONE", "SET_NETWORK 0 wep_key0 6162636465", "SET_NETWORK 0 wep_tx_keyidx 0",
		"SET_NETWORK 0 auth_alg OPEN SHARED",
	} {
		if !slices.Contains(cmds, want) {
			t.Errorf("wpa_supplicant never got %q: %q", want, cmds)
		}
	}

	for _, c := range cmds {
		if strings.HasPrefix(c, "SET_NETWORK 0 psk") {
			t.Errorf("a WEP network got a PSK: %q", c)
		}
	}
}

// A WEP key saved at another index (stock's BCO had key 2) is stored with it, in wifi.conf
// and in the known list, and wpa_supplicant gets the key at that index and sends with it.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestWEPKeyIndexJoins(t *testing.T) {
	rec := NewRecord("oldnet", SecurityWEP, "abcde")
	rec.WEPKeyIndex = 2

	conf := string(ConfBytes(rec))
	if !strings.HasSuffix(conf, "SECURITY=wep\nWEP_KEYIDX=2\n") || ParseConf([]byte(conf)) != rec {
		t.Fatalf("wifi.conf %q reads as %+v", conf, ParseConf([]byte(conf)))
	}

	if line := rec.Line(); !strings.HasSuffix(line, " wep 2") || ParseKnown([]byte(line + "\n"))[0] != rec {
		t.Fatalf("known line %q", line)
	}

	// an index out of range, or on a network that is not WEP, reads as key 0
	for _, bad := range []string{"SECURITY=wep\nWEP_KEYIDX=4\n", "SECURITY=wep\nWEP_KEYIDX=x\n", "WEP_KEYIDX=-1\n"} {
		if got := ParseConf([]byte(bad)); got.WEPKeyIndex != 0 {
			t.Errorf("%q: index %d", bad, got.WEPKeyIndex)
		}
	}

	if got := ParseKnown([]byte("b2xk - - - 2\n")); got[0].WEPKeyIndex != 0 {
		t.Errorf("a network that is not WEP has a WEP key index: %+v", got[0])
	}

	bench := newRig(t, "rhino")

	err := WriteConf(bench.conf, rec)
	if err != nil {
		t.Fatal(err)
	}

	bench.sys.where["oldnet"] = "ok"
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })

	cmds := bench.sys.supServer().Commands()
	for _, want := range []string{"SET_NETWORK 0 wep_key2 6162636465", "SET_NETWORK 0 wep_tx_keyidx 2"} {
		if !slices.Contains(cmds, want) {
			t.Errorf("wpa_supplicant never got %q: %q", want, cmds)
		}
	}

	if slices.Contains(cmds, "SET_NETWORK 0 wep_key0 6162636465") {
		t.Errorf("the key went in at index 0: %q", cmds)
	}
}

// lockedWriter is a log sink the manager's goroutine and the test share.
type lockedWriter struct {
	w  *strings.Builder
	mu sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.w.Write(p) //nolint:wrapcheck // strings.Builder never fails
}

func (l *lockedWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.w.String()
}

// noSAE is a wpa_supplicant built without CONFIG_SAE: it refuses SAE in key_mgmt and its
// password, as the one in a stale incremental build did on an ST20.
func noSAE(variable, value string) bool {
	return variable == "sae_password" || variable == keyMgmt && slices.Contains(strings.Fields(value), "SAE")
}

// A wpa_supplicant without SAE still joins a WPA2 network over PSK, says once what it
// refused, and the next join no longer offers it.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestJoinWithoutSAEFallsBackToPSK(t *testing.T) {
	bench := newRig(t, "spotty")

	logs := &lockedWriter{w: &strings.Builder{}, mu: sync.Mutex{}}
	bench.m.log = slog.New(slog.NewTextHandler(logs, nil))
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.sys.refuse = noSAE
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })

	cmds := bench.sys.supServer().Commands()
	if !slices.Contains(cmds, "SET_NETWORK 0 key_mgmt WPA-PSK WPA-PSK-SHA256") ||
		!slices.Contains(cmds, "ENABLE_NETWORK 0") {
		t.Fatalf("the network did not fall back to WPA-PSK: %q", cmds)
	}

	bench.stop()

	if !bench.m.refused[refusedSAE] || bench.m.refused[refusedSHA256] {
		t.Errorf("refused = %v; want SAE alone", bench.m.refused)
	}

	if n := strings.Count(logs.String(), "refused an optional setting"); n != 1 {
		t.Errorf("logged the refusal %d times:\n%s", n, logs.String())
	}

	// the next join does not try SAE again
	ctx := context.Background()
	srv := wpactrltest.Listen(t, wpactrltest.ShortDir(t), "wlan9", func(cmd string) string {
		if cmd == "ADD_NETWORK" {
			return "0\n"
		}

		if strings.Contains(cmd, "SAE") || strings.Contains(cmd, "sae_password") {
			t.Errorf("SAE tried again: %q", cmd)
		}

		return "OK\n"
	})

	conn, err := wpactrl.Dial(srv.Path)
	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	nets, err := networks(bench.conf, NewRecord("home", "", "homepass1"))
	if err != nil {
		t.Fatal(err)
	}

	sess := &session{conn: conn, records: map[int]Record{}, wrongKey: false, security: false, lost: false}

	err = bench.m.configure(ctx, sess, nets)
	if err != nil || len(sess.records) != 1 {
		t.Fatalf("configure = %v, records %v", err, sess.records)
	}
}

// A wpa_supplicant that knows neither SAE nor PSK-SHA256 joins over plain WPA-PSK.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestJoinWithoutPSKSHA256FallsBackToWPAPSK(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.sys.refuse = func(variable, value string) bool {
		return noSAE(variable, value) || variable == keyMgmt && strings.Contains(value, "SHA256")
	}
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })

	if cmds := bench.sys.supServer().Commands(); !slices.Contains(cmds, "SET_NETWORK 0 key_mgmt WPA-PSK") {
		t.Fatalf("the network did not fall back to WPA-PSK: %q", cmds)
	}
}

// A wpa_supplicant that does not start with pmf=1 and sae_check_mfp=1 starts without them.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSupplicantStartsWithoutPMF(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.sys.badConf = "sae_check_mfp=1"
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })

	conf := bench.read(filepath.Join(bench.p.Tmp, "wpa_supplicant.conf"))
	if conf != "ctrl_interface="+bench.p.CtrlDir+"\n" {
		t.Fatalf("wpa_supplicant.conf = %q; want the control socket alone", conf)
	}
}

// A wpa_supplicant start that fails for a reason of the moment (the interface not up yet)
// joins without pmf and sae_check_mfp that once, but the next start offers them again: only
// a refusal that names them is kept.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSupplicantKeepsPMFAfterAPassingFailure(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.sys.busy = 1
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })
	bench.stop()

	if bench.m.refused[refusedGlobal] {
		t.Fatalf("a busy interface was taken for a refused pmf=1: refused = %v", bench.m.refused)
	}

	err := bench.m.startSupplicant(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	conf := bench.read(filepath.Join(bench.p.Tmp, "wpa_supplicant.conf"))
	if conf != supplicantConf(bench.p.CtrlDir) {
		t.Fatalf("wpa_supplicant.conf = %q; want pmf=1 and sae_check_mfp=1 again", conf)
	}
}

// A wpa_supplicant that knows SAE but not PSK-SHA256 still offers WPA3: the refused combined
// suite is not blamed on SAE, and the network joins over WPA-PSK with SAE.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestJoinWithoutPSKSHA256KeepsSAE(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.sys.refuse = func(variable, value string) bool {
		return variable == keyMgmt && strings.Contains(value, "SHA256")
	}
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })

	if cmds := bench.sys.supServer().Commands(); !slices.Contains(cmds, "SET_NETWORK 0 key_mgmt WPA-PSK SAE") {
		t.Fatalf("the network did not keep SAE: %q", cmds)
	}

	bench.stop()

	if !bench.m.refused[refusedSHA256] || bench.m.refused[refusedSAE] {
		t.Errorf("refused = %v; want PSK-SHA256 alone", bench.m.refused)
	}
}

// A combined suite refused for want of SAE, with the SAE password taken, is put down to SAE
// once PSK-SHA256 is taken.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSetPSKBlamesSAEWhenPSKSHA256IsTaken(t *testing.T) {
	bench := newRig(t, "rhino")
	ctx := context.Background()
	srv := wpactrltest.Listen(t, wpactrltest.ShortDir(t), "wlan9", func(cmd string) string {
		if strings.HasPrefix(cmd, "SET_NETWORK 0 key_mgmt") && strings.Contains(cmd, "SAE") {
			return "FAIL\n"
		}

		return "OK\n"
	})

	conn, err := wpactrl.Dial(srv.Path)
	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	nets, err := networks(bench.conf, NewRecord("home", "", "homepass1"))
	if err != nil {
		t.Fatal(err)
	}

	ok, err := bench.m.setPSK(ctx, conn, 0, &nets[0])
	if !ok || err != nil {
		t.Fatalf("setPSK = %v, %v", ok, err)
	}

	if !bench.m.refused[refusedSAE] || bench.m.refused[refusedSHA256] {
		t.Errorf("refused = %v; want SAE alone", bench.m.refused)
	}

	if cmds := srv.Commands(); !slices.Contains(cmds, "SET_NETWORK 0 key_mgmt WPA-PSK WPA-PSK-SHA256") {
		t.Errorf("never fell back to PSK-SHA256: %q", cmds)
	}
}

// A WEP network on a build without CONFIG_WEP is skipped and the next saved network joins.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestWEPWithoutSupportJoinsTheNextNetwork(t *testing.T) {
	bench := newRig(t, "rhino")

	err := WriteConf(bench.conf, NewRecord("oldnet", SecurityWEP, "abcde"))
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(bench.conf+".known", []byte(NewRecord("home", "", "homepass1").Line()+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	bench.sys.where["oldnet"] = "ok"
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.sys.refuse = func(variable, _ string) bool { return variable == "wep_key0" }
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })

	cmds := bench.sys.supServer().Commands()
	if !slices.Contains(cmds, "REMOVE_NETWORK 0") || slices.Contains(cmds, "ENABLE_NETWORK 0") ||
		!slices.Contains(cmds, "ENABLE_NETWORK 1") {
		t.Fatalf("the WEP network was not skipped for the next one: %q", cmds)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestJoinSavedNetwork(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.start()
	bench.eventually("network-ready", func() bool { return bench.exists("network-ready") })
	bench.eventually("the join's bookkeeping", func() bool { return bench.joinCount() == 1 })

	conf := bench.read(filepath.Join(bench.p.Tmp, "wpa_supplicant.conf"))
	if conf != "ctrl_interface="+bench.p.CtrlDir+"\npmf=1\nsae_check_mfp=1\n" {
		t.Fatalf("wpa_supplicant.conf = %q; want the control socket and PMF where possible, no key", conf)
	}

	cmds := bench.sys.supServer().Commands()
	for _, want := range []string{
		"ATTACH", "SET_NETWORK 0 ssid 686f6d65", "SET_NETWORK 0 scan_ssid 1",
		"SET_NETWORK 0 priority 6", "ENABLE_NETWORK 0",
	} {
		if !slices.Contains(cmds, want) {
			t.Errorf("wpa_supplicant never got %q: %q", want, cmds)
		}
	}

	for _, c := range cmds {
		if strings.Contains(c, "homepass1") {
			t.Errorf("the passphrase reached wpa_supplicant in the clear: %q", c)
		}
	}
	// every PSK network type (ADR 0136): WPA/WPA2, PSK-SHA256 and WPA3's SAE with its password in
	// hex; management frame protection is the global pmf=1 above
	for _, want := range []string{
		"SET_NETWORK 0 key_mgmt WPA-PSK WPA-PSK-SHA256 SAE",
		"SET_NETWORK 0 sae_password " + hex.EncodeToString([]byte("homepass1")),
	} {
		if !slices.Contains(cmds, want) {
			t.Errorf("wpa_supplicant never got %q: %q", want, cmds)
		}
	}

	udhcpc := "udhcpc -i wlan0 -p " + filepath.Join(bench.p.Pid, "soundtap-udhcpc.pid") +
		" -b -A 3 -O ntpsrv -x hostname:soundtap-test"
	if !bench.sys.called(udhcpc) {
		t.Errorf("udhcpc not started as before: %q", bench.sys.Calls())
	}

	if !bench.sys.called("iw dev wlan0 set power_save off") || !bench.sys.called("iw reg set 00") {
		t.Errorf("station setup missing: %q", bench.sys.Calls())
	}

	if bench.read(bench.conf+".good") != bench.read(bench.conf) {
		t.Error("wifi.conf.good is not the network that joined")
	}

	if got := bench.read(bench.conf + ".known"); got != NewRecord("home", "", "homepass1").Line()+"\n" {
		t.Errorf("known = %q", got)
	}

	if !strings.Contains(bench.read(bench.p.BootLog), " wifi: associated after ") {
		t.Errorf("boot log = %q", bench.read(bench.p.BootLog))
	}

	if bench.exists("wifi-join-failed") || bench.exists("setup-ap") || bench.sys.called("hostapd") {
		t.Error("a join that worked left setup state behind")
	}

	if bench.lastProblem() != "" {
		t.Errorf("problem = %q", bench.lastProblem())
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestWrongPasswordFallsBackToTheLastNetworkThatWorked(t *testing.T) {
	bench := newRig(t, "rhino")

	err := WriteConf(bench.conf+".good", NewRecord("home", "", "homepass1"))
	if err != nil {
		t.Fatal(err)
	}

	bench.save("home", "typo-pass")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.start()
	bench.eventually("the join after the fallback", func() bool { return bench.joinCount() == 1 })

	if bench.read(bench.conf) != string(ConfBytes(NewRecord("home", "", "homepass1"))) {
		t.Fatalf("wifi.conf = %q; want the last good one back", bench.read(bench.conf))
	}

	got := strings.TrimSpace(bench.read(filepath.Join(bench.p.State, "wifi-fallback-reason")))
	if got != "wrong_password" {
		t.Fatalf("fallback reason = %q", got)
	}

	if bench.exists("wifi-join-failed") || bench.sys.called("hostapd") {
		t.Fatal("the fallback opened the setup AP or kept the failure")
	}

	if n := countPrefix(bench.sys.Calls(), "wpa_supplicant "); n != 2 {
		t.Fatalf("wpa_supplicant ran %d times; want the failed join and the fallback", n)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestWrongPasswordWithoutALastGoodOpensTheSetupAP(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "typo-pass")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.sys.iwScan = "BSS 00:11(on wlan0)\n\tfreq: 2437\nBSS 00:12(on wlan0)\n\tfreq: 2412.0\n"

	bench.sys.env = "country_code=NL\n"

	mtd := "dev:    size   erasesize  name\nmtd5: 00020000 00020000 \"NAND.U-Boot-Env\"\n"

	err := os.WriteFile(bench.p.ProcMTD, []byte(mtd), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	bench.start()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") && bench.exists("network-ready") })

	if got := strings.TrimSpace(bench.read(filepath.Join(bench.p.State, "wifi-join-failed"))); got != "wrong_password" {
		t.Fatalf("join failed = %q", got)
	}

	hostapd := bench.read(filepath.Join(bench.p.Tmp, "hostapd.conf"))

	want := "interface=wlan1\ndriver=nl80211\nssid=SoundTap ST10 C3D411\nhw_mode=g\nchannel=11\nwmm_enabled=0\n" +
		"country_code=NL\nieee80211d=1\n"
	if hostapd != want {
		t.Fatalf("hostapd.conf = %q", hostapd)
	}

	if got := bench.read(filepath.Join(bench.p.IPv6Conf, "wlan1", "accept_ra")); got != "0\n" {
		t.Fatalf("wlan1 accept_ra = %q, want router advertisements refused", got)
	}

	for _, c := range []string{
		"iw phy phy0 interface add wlan1 type __ap", "ip addr add 192.168.42.1/24 dev wlan1",
		"lan-allow add wlan1 192.168.42.1 24", "dnsd -i 192.168.42.1", "udhcpd -f -v",
	} {
		if !bench.sys.called(c) {
			t.Errorf("setup AP step %q missing", c)
		}
	}

	if !strings.Contains(bench.read(filepath.Join(bench.p.Tmp, "udhcpd.conf")), "option lease 600") {
		t.Error("udhcpd lease is not 600 s")
	}

	if bench.read(bench.p.ScanCache) != bench.sys.iwScan {
		t.Error("the setup page's scan is not cached")
	}
	// Idle AP: after the long pause the saved network is tried again.
	bench.eventually("the retry", func() bool { return countPrefix(bench.sys.Calls(), "wpa_supplicant ") >= 2 })
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestNetworkNotThereIsTriedSooner(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")

	if got := bench.m.retryPause("not_found"); got != bench.m.cfg.Timing.ShortPause {
		t.Fatalf("not_found pause = %s", got)
	}

	bench.m.tries = 10
	if got := bench.m.retryPause("not_found"); got != bench.m.cfg.Timing.LongPause {
		t.Fatalf("tenth retry pause = %s", got)
	}

	bench.m.tries = 0
	if got := bench.m.retryPause("wrong_password"); got != bench.m.cfg.Timing.LongPause {
		t.Fatalf("wrong_password pause = %s", got)
	}

	bench.start()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") })

	if got := strings.TrimSpace(bench.read(filepath.Join(bench.p.State, "wifi-join-failed"))); got != "not_found" {
		t.Fatalf("join failed = %q", got)
	}
	// The router comes back: the next try joins and takes the AP down.
	bench.sys.mu.Lock()
	bench.sys.where["home"] = "ok"
	bench.sys.mu.Unlock()
	bench.eventually("the join on a retry", func() bool { return bench.joinCount() == 1 })

	if bench.exists("setup-ap") || bench.exists("wifi-join-failed") {
		t.Fatal("the setup AP's state stayed after the join")
	}

	bench.stop()

	if bench.m.tries != 0 {
		t.Fatalf("tries = %d after a join", bench.m.tries)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestJoinFailureReasons(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.scan = []string{"home"}
	bench.start()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") })

	if got := strings.TrimSpace(bench.read(filepath.Join(bench.p.State, "wifi-join-failed"))); got != "timeout" {
		t.Fatalf("a network in the scan that never answered = %q", got)
	}

	sess := &session{conn: nil, records: nil, wrongKey: false, security: false, lost: false}
	for seen, want := range map[string]string{
		"SCANNING ASSOCIATING 4WAY_HANDSHAKE COMPLETED":    "no_address",
		"SCANNING ASSOCIATING 4WAY_HANDSHAKE DISCONNECTED": "wrong_password",
		"SCANNING ASSOCIATING DISCONNECTED":                "refused",
	} {
		if got := joinFailure(context.Background(), sess, strings.Fields(seen), "home"); got != want {
			t.Errorf("%s: %s, want %s", seen, got, want)
		}
	}

	sess.wrongKey = true

	got := joinFailure(context.Background(), sess, []string{"SCANNING", "ASSOCIATING"}, "home")
	if got != "wrong_password" {
		t.Errorf("a WRONG_KEY event read as %s", got)
	}

	// the ST20's BCO reports its own failures as association rejects
	for code, want := range map[string]string{"15": "wrong_password", "43": "refused", "72": "refused"} {
		rejected := &session{conn: nil, records: nil, wrongKey: false, security: false, lost: false}
		bench.m.event(context.Background(), rejected, wpactrl.Event{
			Level: 3, Name: "CTRL-EVENT-ASSOC-REJECT",
			Text: "CTRL-EVENT-ASSOC-REJECT bssid=02:00:00:00:00:01 status_code=" + code,
		})

		got := joinFailure(context.Background(), rejected, []string{"SCANNING"}, "home")
		if got != want {
			t.Errorf("assoc reject status %s read as %s, want %s", code, got, want)
		}
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestNoWifiConfOpensTheSetupAPForGood(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.start()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") && bench.exists("network-ready") })
	time.Sleep(3 * bench.m.cfg.Timing.LongPause)

	if bench.sys.called("wpa_supplicant") {
		t.Fatal("nothing saved, yet a join was tried")
	}

	if !strings.Contains(bench.read(filepath.Join(bench.p.Tmp, "hostapd.conf")), "channel=6\n") {
		t.Fatal("no scan: not channel 6")
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSetupHoldIsBounded(t *testing.T) {
	bench := newRig(t, "rhino")
	touch(filepath.Join(bench.p.State, "setup-ap"))

	ctx := context.Background()
	if !bench.m.holdAP(ctx, 10*time.Millisecond) || countPrefix(bench.sys.Calls(), "iw dev wlan1 station dump") != 1 {
		t.Fatalf("an idle AP did not retry at once: %q", bench.sys.Calls())
	}

	bench.sys.station = "Station 12:34:56:78:9a:bc (on wlan1)\n"

	bench.m.cfg.Timing.Hold = 50 * time.Millisecond
	if !bench.m.holdAP(ctx, 10*time.Millisecond) {
		t.Fatal("a parked device held the AP for good")
	}

	if n := countPrefix(bench.sys.Calls(), "iw dev wlan1 station dump"); n != 1+5 {
		t.Fatalf("station looks = %d; want five pauses up to the hold", n)
	}

	remove(filepath.Join(bench.p.State, "setup-ap"))

	if bench.m.holdAP(ctx, time.Millisecond) {
		t.Fatal("retried with the AP gone")
	}

	if bench.m.tries != 2 {
		t.Fatalf("tries = %d", bench.m.tries)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestOpenSetupKeepsTheSavedNetworkAndGoesBack(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.start()
	bench.eventually("the join", func() bool { return bench.joinCount() == 1 })
	before := bench.read(bench.conf)
	bench.m.OpenSetup()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") })

	if bench.read(bench.conf) != before {
		t.Fatal("asking for the setup AP changed wifi.conf")
	}

	bench.eventually("the way back after the idle pause", func() bool { return bench.joinCount() == 2 })
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestNewCredentialsJoinAndAreRemembered(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.sys.where["office"] = "ok"
	bench.start()
	bench.eventually("the join", func() bool { return bench.joinCount() == 1 })
	bench.save("office", "officepass")
	bench.m.Restart()
	bench.eventually("the second join", func() bool { return bench.joinCount() == 2 })

	known := ParseKnown([]byte(bench.read(bench.conf + ".known")))
	if len(known) != 2 || known[0].SSIDB64 != b64("office") || known[1].SSIDB64 != b64("home") {
		t.Fatalf("known = %+v", known)
	}

	cmds := bench.sys.supServer().Commands()
	if !slices.Contains(cmds, "SET_NETWORK 0 ssid "+fmt.Sprintf("%x", "office")) ||
		!slices.Contains(cmds, "SET_NETWORK 1 ssid "+fmt.Sprintf("%x", "home")) ||
		!slices.Contains(cmds, "SET_NETWORK 1 priority 5") {
		t.Fatalf("networks = %q", cmds)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestAKnownNetworkInRangeBecomesWifiConf(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")

	known := ""

	var knownSb302 strings.Builder
	for i := range 6 {
		knownSb302.WriteString(NewRecord(fmt.Sprintf("n%d", i), "", "password").Line() + "\n")
	}

	known += knownSb302.String()

	err := os.WriteFile(bench.conf+".known", []byte(known), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	bench.sys.where["n2"] = "ok"
	bench.start()
	bench.eventually("the join", func() bool { return bench.joinCount() == 1 })

	if got := ParseConf([]byte(bench.read(bench.conf))); got.SSIDB64 != b64("n2") {
		t.Fatalf("wifi.conf = %+v", got)
	}

	got := ParseKnown([]byte(bench.read(bench.conf + ".known")))

	names := make([]string, 0, len(got))

	for _, k := range got {
		raw, _ := base64.StdEncoding.DecodeString(k.SSIDB64)
		names = append(names, string(raw))
	}

	if strings.Join(names, " ") != "n2 n0 n1 n3 n4" {
		t.Fatalf("known = %v; want the joined one first and five kept", names)
	}
	// wifi.conf and five known networks reach wpa_supplicant, the sixth does not.
	if n := countPrefix(bench.sys.supServer().Commands(), "ADD_NETWORK"); n != 6 {
		t.Fatalf("networks offered = %d", n)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestReleaseZeroOneZeroFilesStillJoin(t *testing.T) {
	bench := newRig(t, "rhino")

	for _, name := range []string{"wifi.conf", "wifi.conf.good", "wifi.conf.known"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "nv-0.1.0", name))
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(filepath.Dir(bench.conf), name), b, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	conf, _ := os.ReadFile(bench.conf)

	nets, err := networks(bench.conf, ParseConf(conf))
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(nets))
	for _, n := range nets {
		got = append(got, fmt.Sprintf("%s/%v", n.ssid, n.keyHex != ""))
		// a file from before ADR 0136 has no SECURITY: nothing in it is WEP
		if n.rec.Security != "" || n.wepHex != "" || n.wepIndex != 0 {
			t.Errorf("%s reads as WEP: %+v", n.ssid, n.rec)
		}
	}

	if strings.Join(got, " ") != "home/true work/true cafe/false" {
		t.Fatalf("networks = %v", got)
	}

	good, _ := os.ReadFile(bench.conf + ".good")

	decoded, err := decode(ParseConf(good))
	if err != nil || decoded.ssid != "home" {
		t.Fatalf("wifi.conf.good = %+v, %v", decoded, err)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestAnotherNetworkGetsANewAddress(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")

	known := NewRecord("test-ap", "", "password").Line() + "\n"

	err := os.WriteFile(bench.conf+".known", []byte(known), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	bench.sys.where["home"] = "ok"
	bench.start()
	bench.eventually("the join", func() bool { return bench.joinCount() == 1 })
	dhcp := readPid(filepath.Join(bench.p.Pid, "soundtap-udhcpc.pid"))
	usr1 := fmt.Sprintf("%d %s", dhcp, syscall.SIGUSR1)
	usr2 := fmt.Sprintf("%d %s", dhcp, syscall.SIGUSR2)

	bench.eventually("the first renew", func() bool { return slices.Contains(bench.sys.Signals(), usr1) })

	if slices.Contains(bench.sys.Signals(), usr2) {
		t.Fatal("the first connection released the lease")
	}
	// The same network again (a router back after a power cut): renew only.
	bench.sys.supServer().Event("CTRL-EVENT-CONNECTED - Connection to aa:bb:cc:dd:ee:ff completed [id=0 id_str=]")
	bench.eventually("the second renew", func() bool { return countPrefix(bench.sys.Signals(), usr1) == 2 })

	if slices.Contains(bench.sys.Signals(), usr2) {
		t.Fatal("the same network released the lease")
	}
	// wpa_supplicant moves to the other saved network by itself.
	bench.sys.sup.mu.Lock()
	bench.sys.sup.current = 1
	bench.sys.sup.mu.Unlock()
	bench.sys.supServer().Event("CTRL-EVENT-CONNECTED - Connection to aa:bb:cc:dd:ee:01 completed [id=1 id_str=]")
	bench.eventually("release and renew", func() bool { return countPrefix(bench.sys.Signals(), usr1) == 3 })

	sig := bench.sys.Signals()
	if i := slices.Index(sig, usr2); i < 0 || i > slices.Index(sig[i:], usr1)+i {
		t.Fatalf("signals = %q; want a release before the renew", sig)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestARestartedDaemonKeepsTheNetwork(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.start()
	bench.eventually("the join", func() bool { return bench.joinCount() == 1 })
	bench.stop()
	wpa := readPid(filepath.Join(bench.p.Pid, "soundtap-wpa.pid"))

	next := New(bench.m.cfg)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { defer close(done); next.Run(ctx) }()

	bench.eventually("the new daemon attached", func() bool {
		return bench.sys.supServer().Attached() >= 1 && countPrefix(bench.sys.supServer().Commands(), "STATUS") > 0
	})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if n := countPrefix(bench.sys.Calls(), "wpa_supplicant "); n != 1 {
		t.Fatalf("wpa_supplicant ran %d times; want it kept", n)
	}

	for _, s := range bench.sys.Signals() {
		if strings.HasPrefix(s, fmt.Sprintf("%d ", wpa)) {
			t.Fatalf("the running wpa_supplicant got %s", s)
		}
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestNoAPModeKeepsTheStationTrying(t *testing.T) {
	bench := newRig(t, "spotty")
	bench.save("home", "homepass1")
	bench.sys.apMode = false
	bench.sys.where["home"] = "ok"
	bench.sys.noIP = true
	bench.start()
	bench.eventually("the failed join", func() bool { return bench.exists("wifi-join-failed") })

	if bench.sys.called("hostapd") {
		t.Fatal("a phy without AP mode got hostapd")
	}

	bench.sys.mu.Lock()
	bench.sys.noIP = false
	bench.sys.mu.Unlock()
	bench.eventually("the late join", func() bool { return bench.exists("network-ready") && bench.joinCount() == 1 })

	if bench.exists("wifi-join-failed") {
		t.Fatal("the late join kept the failure")
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestNoInterfaceIsAProblem(t *testing.T) {
	bench := newRig(t, "rhino")
	os.RemoveAll(filepath.Join(bench.p.SysNet, "wlan0"))
	bench.save("home", "homepass1")
	bench.start()
	bench.eventually("the problem", func() bool { return strings.Contains(bench.lastProblem(), "never appeared") })

	if bench.sys.called("wpa_supplicant") || bench.sys.called("hostapd") {
		t.Fatal("brought something up without wlan0")
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSetupAPFailureIsAProblem(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.sys.fail["hostapd"] = true
	bench.start()
	bench.eventually("the problem", func() bool { return strings.Contains(bench.lastProblem(), "hostapd failed") })

	if bench.exists("setup-ap") || exists(filepath.Join(bench.p.SysNet, "wlan1")) {
		t.Fatal("a failed AP left its interface or marker")
	}

	bench.sys.mu.Lock()
	bench.sys.fail = map[string]bool{"udhcpd": true}
	bench.sys.mu.Unlock()
	bench.m.Restart()

	// The failed AP is tried again, so its state comes and goes; what counts is that each
	// failure clears it, which a check right after the problem appears raced with.
	dnsd := filepath.Join(bench.p.Pid, "soundtap-dnsd.pid")
	bench.eventually("the udhcpd problem with the AP's state cleared", func() bool {
		return strings.Contains(bench.lastProblem(), "udhcpd failed") &&
			!bench.exists("setup-ap") && !bench.exists("network-ready") && !exists(dnsd)
	})
}

// With nothing saved, a setup AP that fails to open is tried again until it opens.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSetupAPThatFailsIsTriedAgain(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.sys.fail["hostapd"] = true
	bench.start()
	bench.eventually("two tries", func() bool { return countPrefix(bench.sys.Calls(), "hostapd") >= 2 })

	bench.sys.mu.Lock()
	delete(bench.sys.fail, "hostapd")
	bench.sys.mu.Unlock()
	bench.eventually("the setup AP once hostapd works", func() bool {
		return bench.exists("setup-ap") && bench.exists("network-ready")
	})
	bench.eventually("the problem cleared", func() bool { return bench.lastProblem() == "" })
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSoundtapdThatDoesNotAnswerHoldsBackTheLeases(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.m.cfg.Ready = func() bool { return false }
	bench.start()
	bench.eventually("the problem", func() bool { return strings.Contains(bench.lastProblem(), "does not answer") })

	if bench.sys.called("udhcpd") {
		t.Fatal("leases handed out before the setup page answers")
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestLongSpeakerNameFallsBackForTheSSID(t *testing.T) {
	bench := newRig(t, "rhino")

	bench.m.cfg.SetupSSID = func() string { return strings.Repeat("é", 17) } // 34 bytes
	if got := bench.m.setupSSID(); got != APFallback {
		t.Fatalf("ssid = %q", got)
	}
}

// A setup AP that fails after a failed join is not the end: the saved network is tried
// again, and joins once the router is back.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestFailedSetupAPAfterAFailedJoinTriesAgain(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.keys["home"] = "homepass1"
	bench.sys.fail["hostapd"] = true
	bench.start()
	bench.eventually("the AP problem", func() bool { return strings.Contains(bench.lastProblem(), "hostapd failed") })

	bench.sys.mu.Lock()
	bench.sys.fail = map[string]bool{}
	bench.sys.where["home"] = "ok"
	bench.sys.mu.Unlock()
	bench.eventually("the join on a retry", func() bool { return bench.joinCount() == 1 })
}

// wpa_supplicant that does not start opens the setup AP until the next try, not for good.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSupplicantThatDoesNotStartIsTriedAgain(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.sys.fail["wpa_supplicant"] = true
	bench.start()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") })

	bench.sys.mu.Lock()
	bench.sys.fail = map[string]bool{}
	bench.sys.mu.Unlock()
	bench.eventually("the join on a retry", func() bool { return bench.joinCount() == 1 && !bench.exists("setup-ap") })
}

// A wpa_supplicant that dies after the join is brought up again and joins again.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSupplicantThatDiesIsBroughtBack(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.save("home", "homepass1")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.start()
	bench.eventually("the join", func() bool { return bench.joinCount() == 1 })

	bench.sys.mu.Lock()
	for pid, srv := range bench.sys.servers {
		srv.Close() // the crash: the process and its control socket are gone
		delete(bench.sys.servers, pid)
		delete(bench.sys.alive, pid)
	}
	bench.sys.mu.Unlock()
	bench.eventually("the second join", func() bool { return bench.joinCount() == 2 })
}

// A setup AP daemon that ends takes its pid file along: its pid, once reused, is never
// stopped as dnsd.
//
//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestAnEndedDaemonTakesItsPidFileAlong(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.start()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") })

	dnsd := filepath.Join(bench.p.Pid, "soundtap-dnsd.pid")

	pid := readPid(dnsd)
	if pid == 0 {
		t.Fatal("no dnsd pid file")
	}

	bench.sys.end(pid)
	bench.eventually("the pid file gone", func() bool { return !exists(dnsd) })
}
