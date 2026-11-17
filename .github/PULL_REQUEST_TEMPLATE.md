## What and why

<!-- What changes, and why. Link the issue: "Fixes #123". -->

## How it was tested

<!-- Unit tests, QEMU, or which speaker with which build (commit and package SHA-256). -->

- [ ] `go vet ./... && go test ./...` in `app/`
- [ ] `npm run typecheck && npm run lint && npm run format:check && npm test` in `app/frontend/` (UI changes)
- [ ] On hardware: model …, build …

## Checklist

- [ ] Changes an install/update route, the flash layout, the boot chain, an external protocol or a
      safety boundary? Then this PR adds an ADR in `docs/decisions/` and updates `docs/contributors/design.md`.
- [ ] Touches a hardware path that was not run on a speaker? Then `docs/hardware/hardware-test.md` has an
      `open` row for it (a new row, or the matching cell reset to `open`).
- [ ] Nothing here writes to a speaker without the confirmations the contract in `CONTRIBUTING.md` requires.
- [ ] User-visible change? The README, `docs/owners/install.md` or `docs/owners/faq.md` says so.
