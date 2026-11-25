# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Module loading ends once the boot has loaded what it needs (ADR 0048).

The kernel checks no module signature (a per-build key would make no two builds equal,
ADR 0011), so init closes module loading with modules_disabled after start_wired, the
last step that loads one (the ST300's CPSW switch).
"""
import re
import tempfile
import unittest

from boot.init_script import INIT, InitScript


class LockModules(unittest.TestCase):
    """lock_modules against a temp /proc/sys."""

    def lock(self, mainline: int, *, writable: bool = True) -> tuple[str | None, str]:
        """Run lock_modules on a mainline or stock kernel; return (modules_disabled after, log)."""
        with tempfile.TemporaryDirectory() as d:
            init = InitScript(d)
            knob = init.proc / "sys/kernel/modules_disabled"
            if writable:
                knob.parent.mkdir(parents=True)
                knob.write_text("0\n")
            else:
                knob.mkdir(parents=True)  # a write fails here even as root
            init.run(f"MAINLINE={mainline}\nlock_modules")
            value = knob.read_text() if knob.is_file() else None
            return value, init.log.read_text() if init.log.exists() else ""

    def test_mainline_closes_module_loading(self) -> None:
        """A mainline kernel closes module loading and logs it."""
        value, log = self.lock(1)
        self.assertEqual(value, "1\n")
        self.assertIn("module loading closed", log)

    def test_stock_kernel_stays_open(self) -> None:
        """A stock kernel keeps module loading open and logs nothing."""
        value, log = self.lock(0)
        self.assertEqual(value, "0\n")
        self.assertEqual(log, "")

    def test_a_refused_write_is_logged_and_the_boot_goes_on(self) -> None:
        """A refused write is logged as a warning and the boot goes on."""
        value, log = self.lock(1, writable=False)
        self.assertIsNone(value)
        self.assertIn("WARNING: module loading stays open", log)


class BootOrder(unittest.TestCase):
    """main's steps, run as recorders: the lock comes after the last modprobe."""

    def test_lock_follows_every_step_that_loads_a_module(self) -> None:
        """lock_modules runs after every step that loads a module and before Wi-Fi starts.

        clock.sh sits at an absolute path on the speaker, so it is left out; the order is what is tested.
        """
        text = INIT.read_text()
        body = re.search(r"^main\(\) \{\n(.*?)^\}", text, re.MULTILINE | re.DOTALL)[1]
        body = "\n".join(line for line in body.splitlines() if "clock.sh" not in line)
        steps = re.findall(r"^\t(\w+)", body, re.MULTILINE)
        with tempfile.TemporaryDirectory() as d:
            init = InitScript(d)
            stubs = "".join(f'{s}() {{ echo {s} >>"{init.calls_file}"; }}\n' for s in steps)
            init.run(f"{stubs}main_steps() {{\n{body}\n}}\nmain_steps")
            order = init.calls()
        self.assertEqual(order, steps)
        self.assertLess(order.index("load_stock_modules"), order.index("lock_modules"))
        self.assertLess(order.index("start_wired"), order.index("lock_modules"))
        self.assertLess(order.index("lock_modules"), order.index("start_wifi"))


if __name__ == "__main__":
    unittest.main()
