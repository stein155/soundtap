# Security policy

SoundTap runs on a speaker in your home, on your network, with your Wi-Fi password and your
Spotify login on it.

## What is in scope

[ADR 0113](docs/decisions/0113-threat-model.md) is SoundTap's one security story: what a
stranger on your network, in Wi-Fi or Bluetooth range, with an update file or with the
speaker in hand can and cannot do, and what SoundTap accepts and why. Read it before you report.
Something it lists as accepted (no login on the home network, the open setup access point,
no secure boot, passwords stored in plain text on the speaker) is not a vulnerability by
itself; a way around anything it says holds is.

It covers production images, which every release is. Alpha images are bench builds with a
root login on purpose ([ADR 0119](docs/decisions/0119-no-ssh-in-production.md)); use
SoundTap 0.x only on a network you trust.

## Reporting a vulnerability

Please **do not open a public issue**. Report privately: on the repository, use
**Security → Report a vulnerability** (GitHub private vulnerability reporting). It opens a
draft security advisory only you and the maintainers can see.

Include what you found, the SoundTap Core and app versions (Settings → Updates, or
`GET /api/version`), the model, and how to reproduce it.

## What happens next

1. **Acknowledgement within 7 days**, with a first view of the severity.
2. **A fix or a mitigation**, by severity:
   - **critical** (code execution or a bricked speaker from the network, getting around the
     update signature or the press on the speaker): within 14 days;
   - **high** (a password or account data leaving the speaker, or changes the threat model
     says need a press made without one): within 30 days;
   - **medium and low**: in a regular release, within 90 days.

   A mitigation can be a setting or a documented workaround until the fix ships.
3. **Disclosure**: a GitHub security advisory and a line in the release notes of the
   release that fixes it, on a date agreed with you. Credit goes to you unless you prefer
   otherwise.

## Supported versions

As [ADR 0108](docs/decisions/0108-http-api-compatibility-for-1-0.md) sets out. SoundTap Core
and the SoundTap app release separately, and each lane is supported on its own:

| Version | Security fixes |
| --- | --- |
| Latest release of each lane | Yes |
| 0.x: anything older | No; update from the speaker's settings page |
| From 1.0: the previous minor release | For 90 days after the next minor release, as a patch release |
