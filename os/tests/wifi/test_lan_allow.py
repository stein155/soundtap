# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""lan-allow.sh keeps the production filter's lan_nets equal to the speaker's own subnets."""

import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "os/overlay/opt/soundtap/lan-allow.sh"
HOOK = ROOT / "os/overlay/usr/share/udhcpc/default.script.d/soundtap-log"


class LanAllow:
    """lan-allow.sh run against a recording nft stub in a temp directory."""

    def __init__(self, tmp: str, *, ruleset: bool = True) -> None:
        """Put the nft stub in tmp; `list set` answers whether the production ruleset is loaded, everything else is logged."""
        self.tmp = Path(tmp)
        self.calls = self.tmp / "nft.calls"
        nft = self.tmp / "nft"
        nft.write_text("#!/bin/sh\n"
                       f'[ "$1" = list ] && exit {0 if ruleset else 1}\n'
                       f'echo "$*" >>{self.calls}\n')
        nft.chmod(0o755)
        self.env = {"PATH": "/usr/bin:/bin", "SOUNDTAP_NFT": str(nft),
                    "SOUNDTAP_LAN_STATE": str(self.tmp / "state/lan-net")}

    def __call__(self, *args: str) -> subprocess.CompletedProcess[str]:
        """Run lan-allow.sh with args."""
        return subprocess.run(["sh", str(SCRIPT), *args], check=False, env=self.env,
                              capture_output=True, text=True, timeout=10)

    def nft(self) -> list[str]:
        """Return the logged nft calls."""
        return self.calls.read_text().splitlines() if self.calls.exists() else []


class LanAllowTest(unittest.TestCase):
    """lan-allow.sh and the udhcpc hook that calls it."""

    def test_a_lease_adds_its_network_not_its_address(self) -> None:
        """A lease adds its network to lan_nets, not its address."""
        with tempfile.TemporaryDirectory() as tmp:
            lan = LanAllow(tmp)
            self.assertEqual(lan("add", "wlan0", "192.168.178.57", "255.255.255.0").returncode, 0)
            lan("add", "wlan1", "192.168.42.1", "24")
            lan("add", "eth0", "10.1.2.3/16")
            self.assertEqual(lan.nft(), [
                "add element ip filter lan_nets { 192.168.178.0/24 }",
                "add element ip filter lan_nets { 192.168.42.0/24 }",
                "add element ip filter lan_nets { 10.1.0.0/16 }",
            ])

    def test_a_renewal_changes_nothing_and_a_new_subnet_replaces_the_old(self) -> None:
        """A renewal changes nothing and a lease on a new subnet replaces the old one."""
        with tempfile.TemporaryDirectory() as tmp:
            lan = LanAllow(tmp)
            lan("add", "wlan0", "192.168.1.20", "255.255.255.0")
            lan("add", "wlan0", "192.168.1.20", "255.255.255.0")
            lan("add", "wlan0", "10.0.0.9", "255.0.0.0")
            self.assertEqual(lan.nft(), [
                "add element ip filter lan_nets { 192.168.1.0/24 }",
                "delete element ip filter lan_nets { 192.168.1.0/24 }",
                "add element ip filter lan_nets { 10.0.0.0/8 }",
            ])

    def test_two_interfaces_on_one_subnet_share_one_element(self) -> None:
        """Two interfaces on one subnet share one element, removed only with the last."""
        with tempfile.TemporaryDirectory() as tmp:
            lan = LanAllow(tmp)
            lan("add", "wlan0", "192.168.1.20", "255.255.255.0")
            lan("add", "eth0", "192.168.1.21", "255.255.255.0")
            lan("del", "wlan0")
            self.assertEqual(lan.nft(), ["add element ip filter lan_nets { 192.168.1.0/24 }"])
            lan("del", "eth0")
            self.assertEqual(lan.nft()[-1], "delete element ip filter lan_nets { 192.168.1.0/24 }")

    def test_del_empties_the_state_file_init_made_shared(self) -> None:
        """Del empties the shared state file instead of removing it.

        init makes lan-net.<iface> root's and writable for soundtapd's DHCP in a sticky
        directory (ADR 0066): neither may remove the other's file, so del empties it, and an
        empty file is no subnet.
        """
        with tempfile.TemporaryDirectory() as tmp:
            lan = LanAllow(tmp)
            state = Path(tmp) / "state"
            state.mkdir()
            for iface in ("wlan0", "eth0"):
                (state / f"lan-net.{iface}").write_text("")
            lan("add", "wlan0", "192.168.1.20", "255.255.255.0")
            lan("del", "wlan0")
            self.assertTrue((state / "lan-net.wlan0").is_file())
            self.assertEqual((state / "lan-net.wlan0").read_text(), "")
            lan("add", "eth0", "192.168.1.21", "255.255.255.0")
            self.assertEqual(lan.nft(), ["add element ip filter lan_nets { 192.168.1.0/24 }",
                                         "delete element ip filter lan_nets { 192.168.1.0/24 }",
                                         "add element ip filter lan_nets { 192.168.1.0/24 }"])

    def test_del_of_an_interface_without_address_is_quiet(self) -> None:
        """Del of an interface that never had an address succeeds and changes nothing."""
        with tempfile.TemporaryDirectory() as tmp:
            lan = LanAllow(tmp)
            self.assertEqual(lan("del", "wlan1").returncode, 0)
            self.assertEqual(lan.nft(), [])

    def test_a_malformed_lease_is_refused_and_adds_nothing(self) -> None:
        """A malformed address or mask is refused and adds nothing."""
        with tempfile.TemporaryDirectory() as tmp:
            lan = LanAllow(tmp)
            for addr, mask in (("", ""), ("192.168.1.300", "24"), ("1.2.3", "255.255.255.0"),
                               ("1.2.3.4", "33"), ("1.2.3.4", "x"), ("1.2.3.4;reboot", "24")):
                with self.subTest(addr=addr, mask=mask):
                    self.assertEqual(lan("add", "wlan0", addr, mask).returncode, 1)
            self.assertEqual(lan.nft(), [])

    def test_without_the_production_ruleset_it_does_nothing(self) -> None:
        """Without the production ruleset loaded it changes nothing and keeps no state."""
        with tempfile.TemporaryDirectory() as tmp:
            lan = LanAllow(tmp, ruleset=False)
            self.assertEqual(lan("add", "wlan0", "192.168.1.20", "255.255.255.0").returncode, 0)
            self.assertEqual(lan.nft(), [])
            self.assertFalse((Path(tmp) / "state").exists())

    def test_the_udhcpc_hook_hands_the_lease_to_it(self) -> None:
        """The udhcpc hook hands bound and renew leases to it and a deconfig as del."""
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp, "out")
            stub = Path(tmp, "lan-allow")
            stub.write_text(f'#!/bin/sh\necho "$*" >>{out}\n')
            stub.chmod(0o755)
            env = {"PATH": "/usr/bin:/bin", "SOUNDTAP_LAN_ALLOW": str(stub), "interface": "wlan0",
                   "ip": "192.168.1.20", "subnet": "255.255.255.0"}
            for event in ("bound", "renew", "deconfig"):
                # bound and renew chmod resolv.conf; that may fail off the speaker, the hook goes on.
                subprocess.run(["sh", str(HOOK), event], check=False, env=env, capture_output=True, timeout=10)
            self.assertEqual(out.read_text().splitlines(), [
                "add wlan0 192.168.1.20 255.255.255.0",
                "add wlan0 192.168.1.20 255.255.255.0",
                "del wlan0",
            ])


if __name__ == "__main__":
    unittest.main()
