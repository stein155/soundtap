# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""bt.sh's give-up budget counts bluetoothd deaths in quick succession, not over the whole uptime."""
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
BT = (ROOT / "os/overlay/opt/soundtap/bt.sh").read_text()
BUDGET = re.search(r"^BT_STABLE_SECONDS=.*?^note_bt_death\(\) \{\n.*?^\}\n", BT, re.DOTALL | re.MULTILINE)


def restarts_after_death(before: int, up_for: int) -> int:
    """Return the count after one more death, for a stack that had been up `up_for` seconds.

    The function reads the uptime from a file standing in for /proc/uptime, so the test runs
    on any host.
    """
    with tempfile.NamedTemporaryFile("w", suffix="uptime") as uptime:
        uptime.write("10000.42 9000.00\n")
        uptime.flush()
        script = (
            BUDGET.group(0).replace("/proc/uptime", uptime.name)
            + f"restarts={before}\n"
            + f"bt_up_since=$(( 10000 - {up_for} ))\n"
            + "note_bt_death\necho $restarts\n"
        )
        out = subprocess.run(["sh", "-ec", script], capture_output=True, text=True, check=True)
    return int(out.stdout)


class RestartBudget(unittest.TestCase):
    """note_bt_death's restart count."""

    def test_a_stack_that_ran_for_a_while_starts_the_count_over(self) -> None:
        """A death after a long stable run resets the count to one."""
        self.assertEqual(restarts_after_death(5, up_for=3600), 1)

    def test_quick_deaths_add_up_to_the_limit(self) -> None:
        """A death soon after the last restart adds one to the count."""
        self.assertEqual(restarts_after_death(5, up_for=4), 6)

    def test_the_poll_loop_goes_through_the_budget(self) -> None:
        """The count is raised in one place only, inside note_bt_death."""
        self.assertEqual(BT.count("restarts=$((restarts + 1))"), 1)  # only inside note_bt_death


if __name__ == "__main__":
    unittest.main()
