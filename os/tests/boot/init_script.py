# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Run functions of the init script without booting anything.

The script is sourced with SOUNDTAP_INIT_LIB=1, so its main does not run. Commands a test
stubs sit first on PATH and record each call; the state paths the script keeps in variables
point into a temp root. The script's calls to itself (save-log, save-clock) go to the command
init.d-soundtap, which a test stubs like any other.
"""
import os
import subprocess
from pathlib import Path

INIT = Path(__file__).resolve().parents[3] / "os/overlay/etc/init.d/soundtap"


class InitScript:
    """A sourced copy of the init script whose state paths and commands live in a temp root."""

    def __init__(self, tmp: str | Path) -> None:
        """Lay out the temp root under tmp: stub bin directory, call log and the script's state paths."""
        tmp = Path(tmp)
        self.bin = tmp / "bin"
        self.calls_file = tmp / "calls"
        self.log = tmp / "log"
        self.nv = tmp / "mnt/nv"
        self.run_dir = tmp / "run/soundtap"
        self.sys = tmp / "sys"
        self.proc = tmp / "proc"
        self.opt = tmp / "opt/soundtap"
        self.root_run = tmp / "run/soundtap-root"
        self.app = tmp / "run/audio-bin/app"
        self.dev = tmp / "dev"
        self.tmp = tmp / "tmp"
        self.var_run = tmp / "run"
        for d in (self.bin, self.nv, self.run_dir, self.sys, self.proc, self.opt, self.root_run, self.app,
                  self.dev, self.tmp):
            d.mkdir(parents=True, exist_ok=True)

    def stub(self, name: str, body: str = "", where: Path | None = None) -> None:
        """Put a command on PATH (or into where) that records "name args" and then runs body."""
        path = (where or self.bin) / name
        path.write_text(f'#!/bin/sh\necho "{name} $*" >>"{self.calls_file}"\n{body}\n')
        path.chmod(0o755)

    def run(self, code: str, *, check: bool = True) -> subprocess.CompletedProcess[str]:
        """Source the script, point its paths into the temp root and run code after it."""
        script = (f'SOUNDTAP_INIT_LIB=1\n. "{INIT}"\n'
                  f'LOG="{self.log}" NV_DIR="{self.nv}" RUN_DIR="{self.run_dir}" SYS_DIR="{self.sys}"\n'
                  f'PROC_DIR="{self.proc}" OPT_DIR="{self.opt}" INIT_SCRIPT="{self.bin}/init.d-soundtap"\n'
                  f'ROOT_RUN_DIR="{self.root_run}" APP_DIR="{self.app}" DEV_DIR="{self.dev}" TMP_DIR="{self.tmp}"\n'
                  f'VAR_RUN="{self.var_run}"\n'
                  f'{code}\n')
        env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}")
        return subprocess.run(["sh", "-c", script], env=env, check=check, capture_output=True,
                              text=True, timeout=60)

    def calls(self) -> list[str]:
        """Return the recorded stub calls, one "name args" line each."""
        return self.calls_file.read_text().splitlines() if self.calls_file.exists() else []
