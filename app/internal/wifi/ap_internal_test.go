// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package wifi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUdhcpdConfNamesItsLeaseFileAndFitsThePool(t *testing.T) {
	t.Parallel()

	got := udhcpdConf("wlan1", "/tmp/udhcpd.leases")
	want := "start 192.168.42.10\nend   192.168.42.50\nmax_leases 41\ninterface wlan1\n" +
		"lease_file /tmp/udhcpd.leases\noption subnet 255.255.255.0\noption router 192.168.42.1\n" +
		"option dns 192.168.42.1\noption lease 600\n"

	if got != want {
		t.Fatalf("udhcpd.conf = %q\nwant %q", got, want)
	}
}

// The ST20's setup AP caps a phone's rates at 24 Mb/s, with the 11b rates basic; the others
// offer every 802.11g rate, as before.
func TestHostapdConfCapsTheST20sRates(t *testing.T) {
	t.Parallel()

	base := "interface=wlan1\ndriver=nl80211\nssid=S\nhw_mode=g\nchannel=6\nwmm_enabled=0\n"
	if got := hostapdConf("wlan1", "S", 6, "", ""); got != base {
		t.Fatalf("hostapd.conf = %q", got)
	}

	want := base + "supported_rates=10 20 55 110 60 90 120 180 240\nbasic_rates=10 20 55 110\n"
	if got := hostapdConf("wlan1", "S", 6, "", st20Rates); got != want {
		t.Fatalf("ST20 hostapd.conf = %q\nwant %q", got, want)
	}
}

//nolint:paralleltest // its rig points wpactrl.ClientDir, a package variable, at a directory of its own
func TestSetupAPGivesUdhcpdAnExistingLeaseFile(t *testing.T) {
	bench := newRig(t, "rhino")
	bench.start()
	bench.eventually("the setup AP", func() bool { return bench.exists("setup-ap") && bench.exists("network-ready") })

	leases := filepath.Join(bench.p.Tmp, "udhcpd.leases")
	if !strings.Contains(bench.read(filepath.Join(bench.p.Tmp, "udhcpd.conf")), "\nlease_file "+leases+"\n") {
		t.Fatalf("udhcpd.conf does not name %s", leases)
	}

	_, err := os.Stat(leases)
	if err != nil {
		t.Fatalf("lease file missing when udhcpd starts: %v", err)
	}
}
