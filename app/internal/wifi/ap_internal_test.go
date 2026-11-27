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
