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
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func countPrefix(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// The IEEE 802.11i test vector: what wpa_passphrase gives for "password" on "IEEE".
func TestPassphraseBecomesTheKeyWpaPassphraseGives(t *testing.T) {
	n, err := decode(NewRecord("IEEE", "password"))
	if err != nil {
		t.Fatal(err)
	}
	if n.keyHex != "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e" {
		t.Fatalf("key = %s", n.keyHex)
	}
	raw := strings.Repeat("Ab", 32)
	if n, err := decode(NewRecord("home", raw)); err != nil || n.keyHex != strings.ToLower(raw) {
		t.Fatalf("raw key = %q, %v", n.keyHex, err)
	}
	if n, err := decode(NewRecord("cafe", "")); err != nil || n.keyHex != "" {
		t.Fatalf("open network = %q, %v", n.keyHex, err)
	}
	for name, r := range map[string]Record{
		"bad base64":       {SSIDB64: "!!"},
		"short passphrase": NewRecord("home", "short"),
		"bad hex":          {SSIDB64: b64("home"), HexPSK: strings.Repeat("z", 64)},
	} {
		if _, err := decode(r); err == nil {
			t.Errorf("%s decoded", name)
		}
	}
}

func TestJoinSavedNetwork(t *testing.T) {
	r := newRig(t, "rhino")
	r.save("home", "homepass1")
	r.sys.where["home"] = "ok"
	r.sys.keys["home"] = "homepass1"
	r.start()
	r.eventually("network-ready", func() bool { return r.exists("network-ready") })
	r.eventually("the join's bookkeeping", func() bool { return r.joinCount() == 1 })

	conf := r.read(filepath.Join(r.p.Tmp, "wpa_supplicant.conf"))
	if conf != "ctrl_interface="+r.p.CtrlDir+"\n" {
		t.Fatalf("wpa_supplicant.conf = %q; want the control socket alone", conf)
	}
	cmds := r.sys.sup.srv.Commands()
	for _, want := range []string{"ATTACH", "SET_NETWORK 0 ssid 686f6d65", "SET_NETWORK 0 scan_ssid 1",
		"SET_NETWORK 0 priority 6", "ENABLE_NETWORK 0"} {
		if !slices.Contains(cmds, want) {
			t.Errorf("wpa_supplicant never got %q: %q", want, cmds)
		}
	}
	for _, c := range cmds {
		if strings.Contains(c, "homepass1") {
			t.Errorf("the passphrase reached wpa_supplicant: %q", c)
		}
	}
	if !r.sys.called("udhcpc -i wlan0 -p " + filepath.Join(r.p.Pid, "soundtap-udhcpc.pid") + " -b -A 3 -O ntpsrv -x hostname:soundtap-test") {
		t.Errorf("udhcpc not started as before: %q", r.sys.Calls())
	}
	if !r.sys.called("iw dev wlan0 set power_save off") || !r.sys.called("iw reg set 00") {
		t.Errorf("station setup missing: %q", r.sys.Calls())
	}
	if r.read(r.conf+".good") != r.read(r.conf) {
		t.Error("wifi.conf.good is not the network that joined")
	}
	if got := r.read(r.conf + ".known"); got != NewRecord("home", "homepass1").Line()+"\n" {
		t.Errorf("known = %q", got)
	}
	if !strings.Contains(r.read(r.p.BootLog), " wifi: associated after ") {
		t.Errorf("boot log = %q", r.read(r.p.BootLog))
	}
	if r.exists("wifi-join-failed") || r.exists("setup-ap") || r.sys.called("hostapd") {
		t.Error("a join that worked left setup state behind")
	}
	if r.lastProblem() != "" {
		t.Errorf("problem = %q", r.lastProblem())
	}
}

func TestWrongPasswordFallsBackToTheLastNetworkThatWorked(t *testing.T) {
	r := newRig(t, "rhino")
	if err := WriteConf(r.conf+".good", NewRecord("home", "homepass1")); err != nil {
		t.Fatal(err)
	}
	r.save("home", "typo-pass")
	r.sys.where["home"] = "ok"
	r.sys.keys["home"] = "homepass1"
	r.start()
	r.eventually("the join after the fallback", func() bool { return r.joinCount() == 1 })
	if r.read(r.conf) != string(ConfBytes(NewRecord("home", "homepass1"))) {
		t.Fatalf("wifi.conf = %q; want the last good one back", r.read(r.conf))
	}
	if got := strings.TrimSpace(r.read(filepath.Join(r.p.State, "wifi-fallback-reason"))); got != "wrong_password" {
		t.Fatalf("fallback reason = %q", got)
	}
	if r.exists("wifi-join-failed") || r.sys.called("hostapd") {
		t.Fatal("the fallback opened the setup AP or kept the failure")
	}
	if n := countPrefix(r.sys.Calls(), "wpa_supplicant "); n != 2 {
		t.Fatalf("wpa_supplicant ran %d times; want the failed join and the fallback", n)
	}
}

func TestWrongPasswordWithoutALastGoodOpensTheSetupAP(t *testing.T) {
	r := newRig(t, "rhino")
	r.save("home", "typo-pass")
	r.sys.where["home"] = "ok"
	r.sys.keys["home"] = "homepass1"
	r.sys.iwScan = "BSS 00:11(on wlan0)\n\tfreq: 2437\nBSS 00:12(on wlan0)\n\tfreq: 2412.0\n"
	r.sys.env = "country_code=NL\n"
	if err := os.WriteFile(r.p.ProcMTD, []byte("dev:    size   erasesize  name\nmtd5: 00020000 00020000 \"NAND.U-Boot-Env\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.start()
	r.eventually("the setup AP", func() bool { return r.exists("setup-ap") && r.exists("network-ready") })
	if got := strings.TrimSpace(r.read(filepath.Join(r.p.State, "wifi-join-failed"))); got != "wrong_password" {
		t.Fatalf("join failed = %q", got)
	}
	hostapd := r.read(filepath.Join(r.p.Tmp, "hostapd.conf"))
	want := "interface=wlan1\ndriver=nl80211\nssid=SoundTap ST10 C3D411\nhw_mode=g\nchannel=11\nwmm_enabled=0\ncountry_code=NL\nieee80211d=1\n"
	if hostapd != want {
		t.Fatalf("hostapd.conf = %q", hostapd)
	}
	for _, c := range []string{"iw phy phy0 interface add wlan1 type __ap", "ip addr add 192.168.42.1/24 dev wlan1",
		"lan-allow add wlan1 192.168.42.1 24", "dnsd -i 192.168.42.1", "udhcpd -f"} {
		if !r.sys.called(c) {
			t.Errorf("setup AP step %q missing", c)
		}
	}
	if !strings.Contains(r.read(filepath.Join(r.p.Tmp, "udhcpd.conf")), "option lease 600") {
		t.Error("udhcpd lease is not 600 s")
	}
	if r.read(r.p.ScanCache) != r.sys.iwScan {
		t.Error("the setup page's scan is not cached")
	}
	// Idle AP: after the long pause the saved network is tried again.
	r.eventually("the retry", func() bool { return countPrefix(r.sys.Calls(), "wpa_supplicant ") >= 2 })
}

func TestNetworkNotThereIsTriedSooner(t *testing.T) {
	r := newRig(t, "rhino")
	r.save("home", "homepass1")
	if got := r.m.retryPause("not_found"); got != r.m.cfg.Timing.ShortPause {
		t.Fatalf("not_found pause = %s", got)
	}
	r.m.tries = 10
	if got := r.m.retryPause("not_found"); got != r.m.cfg.Timing.LongPause {
		t.Fatalf("tenth retry pause = %s", got)
	}
	r.m.tries = 0
	if got := r.m.retryPause("wrong_password"); got != r.m.cfg.Timing.LongPause {
		t.Fatalf("wrong_password pause = %s", got)
	}
	r.start()
	r.eventually("the setup AP", func() bool { return r.exists("setup-ap") })
	if got := strings.TrimSpace(r.read(filepath.Join(r.p.State, "wifi-join-failed"))); got != "not_found" {
		t.Fatalf("join failed = %q", got)
	}
	// The router comes back: the next try joins and takes the AP down.
	r.sys.mu.Lock()
	r.sys.where["home"] = "ok"
	r.sys.mu.Unlock()
	r.eventually("the join on a retry", func() bool { return r.joinCount() == 1 })
	if r.exists("setup-ap") || r.exists("wifi-join-failed") {
		t.Fatal("the setup AP's state stayed after the join")
	}
	r.stop()
	if r.m.tries != 0 {
		t.Fatalf("tries = %d after a join", r.m.tries)
	}
}

func TestJoinFailureReasons(t *testing.T) {
	r := newRig(t, "rhino")
	r.save("home", "homepass1")
	r.sys.scan = []string{"home"}
	r.start()
	r.eventually("the setup AP", func() bool { return r.exists("setup-ap") })
	if got := strings.TrimSpace(r.read(filepath.Join(r.p.State, "wifi-join-failed"))); got != "timeout" {
		t.Fatalf("a network in the scan that never answered = %q", got)
	}
	s := &session{}
	for seen, want := range map[string]string{
		"SCANNING ASSOCIATING 4WAY_HANDSHAKE COMPLETED":    "no_address",
		"SCANNING ASSOCIATING 4WAY_HANDSHAKE DISCONNECTED": "wrong_password",
		"SCANNING ASSOCIATING DISCONNECTED":                "refused",
	} {
		if got := r.m.joinFailure(context.Background(), s, strings.Fields(seen), "home"); got != want {
			t.Errorf("%s: %s, want %s", seen, got, want)
		}
	}
	s.wrongKey = true
	if got := r.m.joinFailure(context.Background(), s, []string{"SCANNING", "ASSOCIATING"}, "home"); got != "wrong_password" {
		t.Errorf("a WRONG_KEY event read as %s", got)
	}
}

func TestNoWifiConfOpensTheSetupAPForGood(t *testing.T) {
	r := newRig(t, "rhino")
	r.start()
	r.eventually("the setup AP", func() bool { return r.exists("setup-ap") && r.exists("network-ready") })
	time.Sleep(3 * r.m.cfg.Timing.LongPause)
	if r.sys.called("wpa_supplicant") {
		t.Fatal("nothing saved, yet a join was tried")
	}
	if !strings.Contains(r.read(filepath.Join(r.p.Tmp, "hostapd.conf")), "channel=6\n") {
		t.Fatal("no scan: not channel 6")
	}
}

func TestSetupHoldIsBounded(t *testing.T) {
	r := newRig(t, "rhino")
	touch(filepath.Join(r.p.State, "setup-ap"))
	ctx := context.Background()
	if !r.m.holdAP(ctx, 10*time.Millisecond) || countPrefix(r.sys.Calls(), "iw dev wlan1 station dump") != 1 {
		t.Fatalf("an idle AP did not retry at once: %q", r.sys.Calls())
	}
	r.sys.station = "Station 12:34:56:78:9a:bc (on wlan1)\n"
	r.m.cfg.Timing.Hold = 50 * time.Millisecond
	if !r.m.holdAP(ctx, 10*time.Millisecond) {
		t.Fatal("a parked device held the AP for good")
	}
	if n := countPrefix(r.sys.Calls(), "iw dev wlan1 station dump"); n != 1+5 {
		t.Fatalf("station looks = %d; want five pauses up to the hold", n)
	}
	remove(filepath.Join(r.p.State, "setup-ap"))
	if r.m.holdAP(ctx, time.Millisecond) {
		t.Fatal("retried with the AP gone")
	}
	if r.m.tries != 2 {
		t.Fatalf("tries = %d", r.m.tries)
	}
}

func TestOpenSetupKeepsTheSavedNetworkAndGoesBack(t *testing.T) {
	r := newRig(t, "rhino")
	r.save("home", "homepass1")
	r.sys.where["home"] = "ok"
	r.start()
	r.eventually("the join", func() bool { return r.joinCount() == 1 })
	before := r.read(r.conf)
	r.m.OpenSetup()
	r.eventually("the setup AP", func() bool { return r.exists("setup-ap") })
	if r.read(r.conf) != before {
		t.Fatal("asking for the setup AP changed wifi.conf")
	}
	r.eventually("the way back after the idle pause", func() bool { return r.joinCount() == 2 })
}

func TestNewCredentialsJoinAndAreRemembered(t *testing.T) {
	r := newRig(t, "rhino")
	r.save("home", "homepass1")
	r.sys.where["home"] = "ok"
	r.sys.where["office"] = "ok"
	r.start()
	r.eventually("the join", func() bool { return r.joinCount() == 1 })
	r.save("office", "officepass")
	r.m.Restart()
	r.eventually("the second join", func() bool { return r.joinCount() == 2 })
	known := ParseKnown([]byte(r.read(r.conf + ".known")))
	if len(known) != 2 || known[0].SSIDB64 != b64("office") || known[1].SSIDB64 != b64("home") {
		t.Fatalf("known = %+v", known)
	}
	cmds := r.sys.sup.srv.Commands()
	if !slices.Contains(cmds, "SET_NETWORK 0 ssid "+fmt.Sprintf("%x", "office")) ||
		!slices.Contains(cmds, "SET_NETWORK 1 ssid "+fmt.Sprintf("%x", "home")) ||
		!slices.Contains(cmds, "SET_NETWORK 1 priority 5") {
		t.Fatalf("networks = %q", cmds)
	}
}

func TestAKnownNetworkInRangeBecomesWifiConf(t *testing.T) {
	r := newRig(t, "rhino")
	r.save("home", "homepass1")
	known := ""
	for i := range 6 {
		known += NewRecord(fmt.Sprintf("n%d", i), "password").Line() + "\n"
	}
	if err := os.WriteFile(r.conf+".known", []byte(known), 0o600); err != nil {
		t.Fatal(err)
	}
	r.sys.where["n2"] = "ok"
	r.start()
	r.eventually("the join", func() bool { return r.joinCount() == 1 })
	if got := ParseConf([]byte(r.read(r.conf))); got.SSIDB64 != b64("n2") {
		t.Fatalf("wifi.conf = %+v", got)
	}
	got := ParseKnown([]byte(r.read(r.conf + ".known")))
	var names []string
	for _, k := range got {
		raw, _ := base64.StdEncoding.DecodeString(k.SSIDB64)
		names = append(names, string(raw))
	}
	if strings.Join(names, " ") != "n2 n0 n1 n3 n4" {
		t.Fatalf("known = %v; want the joined one first and five kept", names)
	}
	// wifi.conf and five known networks reach wpa_supplicant, the sixth does not.
	if n := countPrefix(r.sys.sup.srv.Commands(), "ADD_NETWORK"); n != 6 {
		t.Fatalf("networks offered = %d", n)
	}
}

func TestReleaseZeroOneZeroFilesStillJoin(t *testing.T) {
	r := newRig(t, "rhino")
	for _, name := range []string{"wifi.conf", "wifi.conf.good", "wifi.conf.known"} {
		b, err := os.ReadFile(filepath.Join("../../testdata/nv-0.1.0", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(r.conf), name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	conf, _ := os.ReadFile(r.conf)
	nets, err := r.m.networks(r.conf, ParseConf(conf))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range nets {
		got = append(got, fmt.Sprintf("%s/%v", n.ssid, n.keyHex != ""))
	}
	if strings.Join(got, " ") != "home/true work/true cafe/false" {
		t.Fatalf("networks = %v", got)
	}
	good, _ := os.ReadFile(r.conf + ".good")
	if n, err := decode(ParseConf(good)); err != nil || n.ssid != "home" {
		t.Fatalf("wifi.conf.good = %+v, %v", n, err)
	}
}

func TestAnotherNetworkGetsANewAddress(t *testing.T) {
	r := newRig(t, "rhino")
	r.save("home", "homepass1")
	known := NewRecord("test-ap", "password").Line() + "\n"
	if err := os.WriteFile(r.conf+".known", []byte(known), 0o600); err != nil {
		t.Fatal(err)
	}
	r.sys.where["home"] = "ok"
	r.start()
	r.eventually("the join", func() bool { return r.joinCount() == 1 })
	dhcp := readPid(filepath.Join(r.p.Pid, "soundtap-udhcpc.pid"))
	usr1 := fmt.Sprintf("%d %s", dhcp, syscall.SIGUSR1)
	usr2 := fmt.Sprintf("%d %s", dhcp, syscall.SIGUSR2)
	r.eventually("the first renew", func() bool { return slices.Contains(r.sys.Signals(), usr1) })
	if slices.Contains(r.sys.Signals(), usr2) {
		t.Fatal("the first connection released the lease")
	}
	// The same network again (a router back after a power cut): renew only.
	r.sys.sup.srv.Event("CTRL-EVENT-CONNECTED - Connection to aa:bb:cc:dd:ee:ff completed [id=0 id_str=]")
	r.eventually("the second renew", func() bool { return countPrefix(r.sys.Signals(), usr1) == 2 })
	if slices.Contains(r.sys.Signals(), usr2) {
		t.Fatal("the same network released the lease")
	}
	// wpa_supplicant moves to the other saved network by itself.
	r.sys.sup.mu.Lock()
	r.sys.sup.current = 1
	r.sys.sup.mu.Unlock()
	r.sys.sup.srv.Event("CTRL-EVENT-CONNECTED - Connection to aa:bb:cc:dd:ee:01 completed [id=1 id_str=]")
	r.eventually("release and renew", func() bool { return countPrefix(r.sys.Signals(), usr1) == 3 })
	sig := r.sys.Signals()
	if i := slices.Index(sig, usr2); i < 0 || i > slices.Index(sig[i:], usr1)+i {
		t.Fatalf("signals = %q; want a release before the renew", sig)
	}
}

func TestARestartedDaemonKeepsTheNetwork(t *testing.T) {
	r := newRig(t, "rhino")
	r.save("home", "homepass1")
	r.sys.where["home"] = "ok"
	r.start()
	r.eventually("the join", func() bool { return r.joinCount() == 1 })
	r.stop()
	wpa := readPid(filepath.Join(r.p.Pid, "soundtap-wpa.pid"))

	next := New(r.m.cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); next.Run(ctx) }()
	r.eventually("the new daemon attached", func() bool {
		return r.sys.sup.srv.Attached() >= 1 && countPrefix(r.sys.sup.srv.Commands(), "STATUS") > 0
	})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if n := countPrefix(r.sys.Calls(), "wpa_supplicant "); n != 1 {
		t.Fatalf("wpa_supplicant ran %d times; want it kept", n)
	}
	for _, s := range r.sys.Signals() {
		if strings.HasPrefix(s, fmt.Sprintf("%d ", wpa)) {
			t.Fatalf("the running wpa_supplicant got %s", s)
		}
	}
}

func TestNoAPModeKeepsTheStationTrying(t *testing.T) {
	r := newRig(t, "spotty")
	r.save("home", "homepass1")
	r.sys.apMode = false
	r.sys.where["home"] = "ok"
	r.sys.noIP = true
	r.start()
	r.eventually("the failed join", func() bool { return r.exists("wifi-join-failed") })
	if r.sys.called("hostapd") {
		t.Fatal("a phy without AP mode got hostapd")
	}
	r.sys.mu.Lock()
	r.sys.noIP = false
	r.sys.mu.Unlock()
	r.eventually("the late join", func() bool { return r.exists("network-ready") && r.joinCount() == 1 })
	if r.exists("wifi-join-failed") {
		t.Fatal("the late join kept the failure")
	}
}

func TestNoInterfaceIsAProblem(t *testing.T) {
	r := newRig(t, "rhino")
	os.RemoveAll(filepath.Join(r.p.SysNet, "wlan0"))
	r.save("home", "homepass1")
	r.start()
	r.eventually("the problem", func() bool { return strings.Contains(r.lastProblem(), "never appeared") })
	if r.sys.called("wpa_supplicant") || r.sys.called("hostapd") {
		t.Fatal("brought something up without wlan0")
	}
}

func TestSetupAPFailureIsAProblem(t *testing.T) {
	r := newRig(t, "rhino")
	r.sys.fail["hostapd"] = true
	r.start()
	r.eventually("the problem", func() bool { return strings.Contains(r.lastProblem(), "hostapd failed") })
	if r.exists("setup-ap") || exists(filepath.Join(r.p.SysNet, "wlan1")) {
		t.Fatal("a failed AP left its interface or marker")
	}
	r.sys.fail = map[string]bool{"udhcpd": true}
	r.m.Restart()
	r.eventually("the udhcpd problem", func() bool { return strings.Contains(r.lastProblem(), "udhcpd failed") })
	if r.exists("setup-ap") || r.exists("network-ready") || exists(filepath.Join(r.p.Pid, "soundtap-dnsd.pid")) {
		t.Fatal("a failed AP left its state")
	}
}

func TestSoundtapdThatDoesNotAnswerHoldsBackTheLeases(t *testing.T) {
	r := newRig(t, "rhino")
	r.m.cfg.Ready = func() bool { return false }
	r.start()
	r.eventually("the problem", func() bool { return strings.Contains(r.lastProblem(), "does not answer") })
	if r.sys.called("udhcpd") {
		t.Fatal("leases handed out before the setup page answers")
	}
}

func TestLongSpeakerNameFallsBackForTheSSID(t *testing.T) {
	r := newRig(t, "rhino")
	r.m.cfg.SetupSSID = func() string { return strings.Repeat("é", 17) } // 34 bytes
	if got := r.m.setupSSID(); got != APFallback {
		t.Fatalf("ssid = %q", got)
	}
}
