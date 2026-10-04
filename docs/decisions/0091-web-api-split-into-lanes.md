# 0091. The install lane is its own package; the API has one error shape and a level

Status: accepted — 2026-10-23

## Context

`app/internal/web` holds the HTTP routes the speaker serves. NAND installs, app updates and the
ST20 front CPU's flash sitting next to presets, multiroom and alarms, sharing fields and helpers,
would let a change to one reach the others. The install lane is the boot-critical part; it does its
NAND work in `internal/install` and `internal/flash` and needs little of the server: the logger,
the home directory and the error helpers.

Clients of the API (the web UI, other speakers, Home Assistant, scripts) need to tell a caller's
mistake from the speaker's own failure and from a failed peer, to read one error format, and to
know which API they are talking to. One bad payload must not blank the whole UI.

## Decision

### The install lane is its own package

`internal/web` keeps the router, authentication, the request guard, the UI and every other route.
The install lane is `internal/web/firmware`: `/api/firmware`, `/api/soundtapd`, `/api/reboot`,
boot confirmation and signature checks. It has a `Handler` with its own fields, a constructor and
`Register(mux)`. The server holds one pointer to it and hands it the hooks soundtapd gives in
`web.Options.Firmware`. A backup restore takes the install lock through `TryLock`/`Unlock` instead
of reaching into the lane's state.

soundtapd builds the server with `web.New(web.Options)`: the required stores and clients, then one
group per concern (`Speaker`, `Health`, `Multiroom`, `Firmware`, `Setup`, `Bluetooth`, `Panel`,
`Changed`, `MQTT`), where a nil hook means the speaker lacks that piece. The audio side starts
after the server, because it needs the server itself, so it hands its groups over with
`AttachAudio`, `AttachSpotify`, `AttachSoundbar` and `AttachUPnP` before serving starts; a speaker
without its audio side (`-no-audio`, `-dev`) attaches none. The other methods that change the
server carry runtime data only: a discovered device (`SetDevice`), the mDNS responder
(`SetMDNS`), a builtin clip (`SetBuiltinClip`).

It is the only lane. Multiroom's group state lives in `internal/multiroom`
([ADR 0122](0122-multiroom-group-package.md)); its routes stay in `internal/web`.

### One error shape and statuses that say whose fault it was

- Every error is `application/json` `{"error": "<message for a person>", "code": "<code for a
  program>"}`, written through `internal/web/apierror`. The message is English and may be reworded
  in any release. The code is stable: `invalid_request`, `not_found`, `not_confirmed` (nobody held
  a button on the speaker), `confirm_superseded`, `busy` (another operation runs), `unavailable`
  and the rest listed in `openapi.json`. A status implies a code (409 `conflict`, 503
  `unavailable`, ...); a call site names a more precise one where it has it. The list is open: a
  client treats a code it does not know as the failure its status says. An endpoint may add fields,
  never drop `error` or `code`.
- 4xx is the caller's to fix (400 malformed, 409 busy, 412/428 a missing precondition or
  confirmation, 413 too large). The speaker's own failure is 500, or 504 on a deadline. 502 only
  where a peer or an upstream (another speaker, the GitHub release list) failed, written at that
  call site.
- The UI shows its own translated failure and, for a code the user can act on, a translated reason.
  The speaker's English never reaches the page; an unknown code, or none, leaves the translated
  failure on its own.
- A top-level error boundary in the UI shows the message, a reload button and a link to
  `/api/debug`.

### A level, not a prefix

Routes stay under `/api`, unversioned. `GET /api/version` and the mDNS TXT `api` carry
`web.APILevel`; a client (another speaker, Home Assistant, the app lane) compares it instead of
guessing. When the level rises and what a client may rely on are the compatibility rules of
[ADR 0108](0108-http-api-compatibility-for-1-0.md).

Alternatives that lost: one package for every route (every change keeps touching boot-critical code
through shared fields); a lane for every route group (one very large diff in a package with
timing-sensitive tests, for routes that are not boot-critical); a `/api/v1` prefix (worth it only
the day two levels must be served at once).

## Consequences

- The install code compiles and tests on its own; its tests build a `Handler`, not a whole server.
- A lane keeps its own copy of the small JSON error helpers, all writing through
  `internal/web/apierror`, so the error shape and its codes are defined once.
- A new install route goes into the lane's `Register`. Body limits and deadlines stay in
  `internal/web`, keyed by the paths the lane exports.
- The frontend reads one shape for reads and writes, and the code reaches the user in the page's
  language.

## Code

- `app/internal/web/firmware/handler.go`: `Handler`, `New` and `Handler.Register`.
- `app/internal/web/options.go`: `Options`, `New`, `Server.AttachAudio` and the other `Attach`
  methods.
- `app/internal/web/apierror/apierror.go`: `Code`, `Codes`, `ForStatus` and `Write`;
  `app/internal/web/respond.go`: `apiError`, `apiErrorCode` and `Server.fail`.
- `app/internal/web/about.go:APILevel`.
- `app/frontend/src/ErrorBoundary.tsx`: the UI's error boundary.
