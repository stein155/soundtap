# SoundTap app

`soundtapd` is the speaker application: JSON API, embedded React UI, TuneIn player,
speaker state, presets, buttons, LEDs, display and source control. It runs without BoseApp.

## Test

From `app/`:

```sh
go vet ./...
go test ./...
cd frontend
npm ci
npm run typecheck
npm test
npm run build
```

Go modules are allowed under [ADR 0124](../docs/decisions/0124-third-party-code.md): pure Go
(soundtapd builds with `CGO_ENABLED=0`), a licence compatible with GPL-3.0-only with a row in
the Go module table of `NOTICE.md`, pinned in `go.mod`/`go.sum`, maintained, of reasonable
size, and fixing a real problem. `make lint-go-licenses` checks the licences. The modules are
vendored in `vendor/` (ADR 0011): after `go get` run `go mod tidy` and `go mod vendor`, and commit
all three; `make lint-go-vendor` fails on a vendor directory that differs from `go.sum`.

## Runtime rules

- State belongs under the configured home (`/mnt/nv/soundtap` on hardware).
- Persistent writes must be atomic and must not mutate memory before disk success.
- Network handlers need bounded reads, timeouts and trust-boundary validation.
- User input is data, never shell syntax.
- The model's primary HTTP listener must bind; secondary listeners are optional.
- Hardware goroutines must remain cancellable and report unavailable devices.
- Frontend output is generated into `internal/web/dist` and embedded by Go.

The rules for touching hardware are in [the contract](../CONTRIBUTING.md#the-contract). The
hardware facts the code must respect are in the per-model hardware contracts it links
([ST10](../docs/hardware/st10/hardware-contract.md), [ST20](../docs/hardware/st20/hardware-contract.md))
and in [`os/README.md`](../os/README.md).
