# 0119. Production images have no SSH and nothing replaces it

Status: accepted — 2026-11-12

## Context

Stock SoundTouch firmware offers its owner no root login. Bring-up and the bench need a shell on
the speaker, so alpha images carry one ([ADR 0011](0011-one-buildroot-pipeline.md) keeps the two
profiles apart). The question this ADR answers is whether support for a release speaker needs some
other way in.

## Decision

- **Production images** (every release) carry no SSH server, no SSH client or key tool, no host
  key and no `authorized_keys`, and root cannot log in, on the UART getty either. The USB gadget
  gives no shell, the inbound filter ([ADR 0066](0066-production-hardening.md)) has no rule for
  port 22, and nothing on the image (avahi's adverts included) names SSH.
- **Nothing replaces it.** No key-only login, no debug switch, no remote shell over the web API.
  Support goes through what an owner already has: the support bundle in the web UI
  (*Settings → System → Support bundle*, `GET /api/debug`, behind a key held on the speaker,
  [ADR 0055](0055-household-token-and-windows.md)), the read-only boot state in
  `GET /api/health`, and for recovery the USB update at power-on and the web UI.
- **Alpha images keep root SSH without a password** for development. They are for the bench and
  are not published as releases.

Alternatives that lost: key-only SSH, off by default (still a listener and a key store to get
right on every speaker, for a support route the project does not want); a shell or command route
in the web API (the same exposure as SSH, behind a weaker door).

## Consequences

- A fault on a release speaker is diagnosed from the support bundle and the logs it carries.
  Anything support needs to see has to be in that bundle.
- The bench scripts in `tools/hil/` test the release candidate itself through the HTTP API. They
  read the boot (boot id, running slot, whether soundtapd confirmed it, how the boot before ended)
  from `GET /api/health`'s `boot`, which is read-only, needs no hold and holds nothing secret; it is
  not on the cross-origin `/api/version`, where a boot id would let any web page recognise the
  speaker until its next boot ([ADR 0113](0113-threat-model.md)). Each install and reboot waits for
  the hold, which the script asks for, so a production run needs a person at the speaker. Only the
  rollback check's watchdog route, which freezes soundtapd, still needs a shell
  ([install gate, layer 3](../contributors/testing/install-gate.md#layer-3-hardware-in-the-loop-by-hand)).
- `tools/recovery/nand-backup-ssh.sh` works over root SSH and needs an alpha image. User
  documentation never tells a user to ssh into the speaker.
- A tester who needs a shell on a release build installs the alpha build of the same commit.

## Code

- `os/configs/production.fragment` deselects Dropbear and root login;
  `os/board/rootfs/post-build.sh` removes whatever else names SSH, and the gadget shell, from a
  production image.
- `os/support/rootfs_check.py` (`--profile production`) fails a production rootfs that carries
  any of it.
- `app/internal/web/health.go`: the `boot` object of `/api/health`.
- `tools/hil/lib.sh`, `tools/hil/install-candidate.sh`: the bench scripts over HTTP, asking for
  the hold.
