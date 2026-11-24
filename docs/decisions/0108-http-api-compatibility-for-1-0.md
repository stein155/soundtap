# 0108. HTTP API compatibility from the first public release

Status: accepted — 2026-11-13

## Context

From the first public release other software depends on the HTTP API: other speakers (multiroom,
the device proxy), Home Assistant and MQTT bridges, scripts, and the web UI of one release lane
running against the other ([ADR 0035](0035-releases.md)). Clients need to know what they may rely
on, what a level bump means and how long old behaviour is kept. Before that release the API changes
in one go, without shims ([ADR 0091](0091-web-api-split-into-lanes.md)).

## Decision

**When it binds.** The rules below bind from the first public release, 0.1.0, not from 1.0.
Unreleased alpha builds before it remain mutable.

**Stable and internal.** The stable API is exactly what `app/internal/web/openapi.json` documents
(served at `/api/openapi.json`, rendered at `/api/docs`). A test keeps the document and the
registered routes equal, so a route is either documented or listed as internal in the
document's own `x-soundtap-internal`, where clients can read it too. The internal set is the loopback hooks for the co-located player and receivers
(`POST /api/source`, `POST /api/nowplaying`, `POST /api/position`,
`POST /api/bluetooth/connected`, `GET /api/speaker/audio/{name}`) and the UI itself (`GET /`).
Internal routes may change or vanish in any release. `GET /api/debug` is documented, but its text
is a diagnostic for people, not a format to parse.

`POST /api/speaker/notify` accepts an `appKey` or `app_key`, in the JSON body or the query, and
ignores it: clients written for Bose's SoundTouch API send one, and SoundTap keeps no Bose
developer key.

**The `api` integer.** `GET /api/version` returns `"api"`, and the mDNS TXT record `api=` carries
the same value (`web.APILevel`). It is the level of the stable API. It rises in the commit that
makes a breaking change, and never otherwise. A client built for level N refuses, or degrades,
when a speaker reports a level it does not know.

**Breaking** (raises `api`): removing a stable route or method; renaming a route or a field;
changing a field's type, unit or meaning; removing a response field or an enum value; making an
optional request field required; tightening validation so a previously valid request fails;
changing a success status; dropping `error` or `code` from the error shape, changing the meaning of
an error code, or moving a status class ([ADR 0091](0091-web-api-split-into-lanes.md)).

**Not breaking** (no bump): a new route; a new optional request field; a new response field
(clients ignore unknown fields); a new enum value in a response documented as open; clearer error
text; a new error `code`; a 4xx for input that was already invalid; timing and performance.
`PUT /api/settings` rejects unknown request fields, so settings clients send only the fields
documented for the level they target.

**Detecting capabilities.** `api` gives the contract; `version`, `os` and `app` give the builds of
the two lanes; `GET /api/info` gives the model. `GET /api/version` lists `capabilities`, what this
speaker has (`bluetooth` when an adapter is present, `ir`, `keypad`, `aux`, `display`,
`battery`), an open list clients ignore unknown values of. Anything else that exists only on some
models or builds is detected by its documented route answering or its documented field being
present, not by parsing version strings.

**Checking it.** `testdata/api-contract-<level>.json` records each level's contract: every
documented route and method, every success response field with its type, and the request fields
a route requires. `TestAPIContract` fails a change that removes or retypes one of them, or makes
a request field required, unless `web.APILevel` rises and the new level's contract is recorded.
Additions pass. Before 0.1.0 level 1 may be re-recorded; from then on a level's file stays.

**Deprecation window.** A stable route or field is marked `deprecated: true` in `openapi.json` and
keeps working for at least one full minor release and at least 90 days before a release removes it
and raises `api`. A security fix may shorten this. Stored data follows
[ADR 0120](0120-on-box-state-migrates.md) instead.

**Maintenance.** SoundTap Core and the SoundTap app release independently. During alpha, fixes go
to the latest release of each lane. From 1.0, after a new minor release, the previous minor gets
security and data-loss fixes for 90 days, as a patch release on the same `api` level. Older
releases update forward; no downgrade is tested.

Alternatives that lost: a `/api/v1` path prefix (worth it only when two levels must be served at
once; one integer is enough while a speaker serves one level); semantic versioning of the API apart
from the integer (more states than clients can use, and additive changes need no signal); promising
nothing until 2.0 (Home Assistant and multiroom peers would guess across updates).

## Consequences

- Third parties get one contract, the OpenAPI document, and the test keeps it equal to the routes.
- Every API change gets one review question: does it break by the list above? If so, the route is
  deprecated first and `api` rises when it goes, which costs a release cycle.
- The internal set is machine-readable in `openapi.json`, and clients read capabilities instead
  of probing routes.
- A breaking change cannot land by accident: the recorded contract catches it in `make test`.

## Code

- `app/internal/web/openapi.json`: the stable API.
- `app/internal/web/openapi_test.go`: `TestOpenAPIMatchesRoutes` and `internalRoutes`, which reads
  `x-soundtap-internal`.
- `app/internal/web/api_contract_test.go`: `TestAPIContract` and `app/internal/web/testdata/api-contract-1.json`.
- `app/internal/web/about.go`: `capabilities` in `GET /api/version`; `app/internal/hw/model/model.go:Capabilities`.
- `app/internal/web/about.go:APILevel`.
- `app/internal/web/speaker.go`: `POST /api/speaker/notify`, which ignores `appKey`.
