# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""image_pins.py finds every container image the repository pulls and fails on one that is
not pinned by digest. Run against the real tree, and against small fake trees that show it
reads each place an image can be named."""

import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools/ci"))
import image_pins  # noqa: E402

PIN = "@sha256:" + "a" * 64


class ImagePinsTest(unittest.TestCase):
    def tree(self, files):
        tmp = Path(self.enterContext(tempfile.TemporaryDirectory()))
        for name, text in files.items():
            (tmp / name).parent.mkdir(parents=True, exist_ok=True)
            (tmp / name).write_text(text)
        return image_pins.problems(tmp)

    def test_the_repository_pins_every_image(self):
        self.assertEqual(image_pins.problems(ROOT), [])

    def test_a_makefile_image_needs_a_digest(self):
        found = self.tree({"Makefile": "PYTHON_IMAGE := python:3.12-slim\n"
                                       f"GO_IMAGE := golang:$(V)-bookworm{PIN}\n"})
        self.assertEqual(found, ["Makefile:1: python:3.12-slim is not pinned by @sha256"])

    def test_a_docker_run_resolves_its_variable(self):
        script = ("#!/bin/sh\nNODE_IMAGE=${NODE_IMAGE:-node:24}\nKVM=\n"
                  "[ -c /dev/kvm ] && KVM=\"--device /dev/kvm\"\n"
                  "docker run --rm $KVM -v \"$PWD\":/w \\\n  -w /w \"$NODE_IMAGE\" npm ci\n")
        found = self.tree({"build": script})
        self.assertIn("build:2: node:24 is not pinned by @sha256", found)
        self.assertIn("build:5: node:24 is not pinned by @sha256", found)
        self.assertEqual(len(found), 2, found)

    def test_a_folded_workflow_command_is_read_across_lines(self):
        workflow = ("jobs:\n  build:\n    steps:\n      - run: >-\n"
                    "          docker run --rm -v \"$PWD:/docs\" -w /docs\n"
                    "          squidfunk/mkdocs-material:9.7.7\n          build\n"
                    "      - uses: docker://alpine:3.20\n")
        found = self.tree({".github/workflows/docs.yml": workflow})
        self.assertEqual(found, [
            ".github/workflows/docs.yml:5: squidfunk/mkdocs-material:9.7.7 is not pinned by @sha256",
            ".github/workflows/docs.yml:8: alpine:3.20 is not pinned by @sha256"])

    def test_dockerfile_from_needs_a_digest_but_not_a_stage(self):
        found = self.tree({"Dockerfile": "FROM debian:bookworm-slim AS base\nFROM base\n"
                                         f"FROM alpine:3.20{PIN}\n"})
        self.assertEqual(found, ["Dockerfile:1: debian:bookworm-slim is not pinned by @sha256"])

    def test_images_built_here_and_comments_pass(self):
        found = self.tree({"run.sh": "# docker run is how this works\n"
                                     "docker run --rm soundtap-rom-usb-loader:bookworm make\n"})
        self.assertEqual(found, [])

    def test_an_unknown_option_is_reported_not_guessed(self):
        found = self.tree({"run.sh": "docker run --gpus all debian:bookworm\n"})
        self.assertEqual(len(found), 1)
        self.assertIn("unknown docker run option --gpus", found[0])

    def test_every_local_image_says_where_it_comes_from(self):
        for name, reason in image_pins.LOCAL.items():
            self.assertTrue(reason.strip(), name)


if __name__ == "__main__":
    unittest.main()
