// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/wpactrl"
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

	decoded, err := decode(NewRecord("IEEE", "password"))
	if err != nil {
		t.Fatal(err)
	}

	if decoded.keyHex != "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e" {
		t.Fatalf("key = %s", decoded.keyHex)
	}

	raw := strings.Repeat("Ab", 32)

	decoded, err = decode(NewRecord("home", raw))
	if err != nil || !strings.EqualFold(decoded.keyHex, raw) {
		t.Fatalf("raw key = %q, %v", decoded.keyHex, err)
	}

	decoded, err = decode(NewRecord("cafe", ""))
	if err != nil || decoded.keyHex != "" {
		t.Fatalf("open network = %q, %v", decoded.keyHex, err)
	}

	for name, rec := range map[string]Record{
		"bad base64":       {SSIDB64: "!!", PSKB64: "", HexPSK: ""},
		"short passphrase": NewRecord("home", "short"),
		"bad hex":          {SSIDB64: b64("home"), PSKB64: "", HexPSK: strings.Repeat("z", 64)},
	} {
		_, err := decode(rec)
		if err == nil {
			t.Errorf("%s decoded", name)
		}
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
	if conf != "ctrl_interface="+bench.p.CtrlDir+"\n" {
		t.Fatalf("wpa_supplicant.conf = %q; want the control socket alone", conf)
	}

	cmds := bench.sys.sup.srv.Commands()
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
			t.Errorf("the passphrase reached wpa_supplicant: %q", c)
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

	if got := bench.read(bench.conf + ".known"); got != NewRecord("home", "homepass1").Line()+"\n" {
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

	err := WriteConf(bench.conf+".good", NewRecord("home", "homepass1"))
	if err != nil {
		t.Fatal(err)
	}

	bench.save("home", "typo-pass")
	bench.sys.where["home"] = "ok"
	bench.sys.keys["home"] = "homepass1"
	bench.start()
	bench.eventually("the join after the fallback", func() bool { return bench.joinCount() == 1 })

	if bench.read(bench.conf) != string(ConfBytes(NewRecord("home", "homepass1"))) {
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
		"lan-allow add wlan1 192.168.42.1 24", "dnsd -i 192.168.42.1", "udhcpd -f",
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

	cmds := bench.sys.sup.srv.Commands()
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
		knownSb302.WriteString(NewRecord(fmt.Sprintf("n%d", i), "password").Line() + "\n")
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
	if n := countPrefix(bench.sys.sup.srv.Commands(), "ADD_NETWORK"); n != 6 {
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

	known := NewRecord("test-ap", "password").Line() + "\n"

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
	bench.sys.sup.srv.Event("CTRL-EVENT-CONNECTED - Connection to aa:bb:cc:dd:ee:ff completed [id=0 id_str=]")
	bench.eventually("the second renew", func() bool { return countPrefix(bench.sys.Signals(), usr1) == 2 })

	if slices.Contains(bench.sys.Signals(), usr2) {
		t.Fatal("the same network released the lease")
	}
	// wpa_supplicant moves to the other saved network by itself.
	bench.sys.sup.mu.Lock()
	bench.sys.sup.current = 1
	bench.sys.sup.mu.Unlock()
	bench.sys.sup.srv.Event("CTRL-EVENT-CONNECTED - Connection to aa:bb:cc:dd:ee:01 completed [id=1 id_str=]")
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
		return bench.sys.sup.srv.Attached() >= 1 && countPrefix(bench.sys.sup.srv.Commands(), "STATUS") > 0
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

	bench.sys.fail = map[string]bool{"udhcpd": true}
	bench.m.Restart()
	bench.eventually("the udhcpd problem", func() bool {
		return strings.Contains(bench.lastProblem(), "udhcpd failed")
	})

	dnsd := filepath.Join(bench.p.Pid, "soundtap-dnsd.pid")
	if bench.exists("setup-ap") || bench.exists("network-ready") || exists(dnsd) {
		t.Fatal("a failed AP left its state")
	}
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
