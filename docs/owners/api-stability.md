# API stability

SoundTap's HTTP API is what the web page, Music Assistant and your own scripts talk to. This page
says what you can rely on. `GET /api/openapi.json` on a speaker lists every route, and
`http://<speaker>/api/docs` shows the same list as a browsable page.

## Finding out what a speaker offers

- `GET /api/version` returns `api`, the API's major version, today `1`. Check it before you depend
  on a route.
- A test in `app/internal/web/openapi_test.go` fails when a registered route is missing from the
  document, so every route is either documented or explicitly internal. The test covers the list
  of routes, not how a speaker behaves.

## Before the first public release

Until then, routes can change without notice. The promise below starts at 0.1.0, not at 1.0.

## From the first public release (0.1.0)

- Every route in the document is stable within one `api` version: it keeps its path, method and
  fields, and a client that ignores unknown fields keeps working.
- New routes and new fields can arrive in any release.
- Removing or renaming something raises `api`, and the release notes name the change.
- Internal routes can change at any time. The documented debug bundle's text is diagnostic
  output for people, not a stable machine-readable format.
- Deprecation lasts at least one full minor release and 90 days before removal and an `api`
  increase; a security fix may shorten that window. See [ADR 0108](../decisions/0108-http-api-compatibility-for-1-0.md).

## Outside this promise

Log lines and the update package's layout can change in any release. Files on the speaker can
change format too, but from 0.1.0 on an update carries what they hold over
([ADR 0120](../decisions/0120-on-box-state-migrates.md)).
