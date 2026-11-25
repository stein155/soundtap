# 0120. From 0.1.0 on, every update carries the on-box state over

Status: accepted — 2026-11-12

## Context

Everything a speaker remembers lives on `/mnt/nv`: its name and settings, presets, favourites,
Wi-Fi networks, the group it follows. An update replaces the slot and leaves `/mnt/nv` alone
(`mtd10`, [the NAND contract](../../CONTRIBUTING.md#nand-and-boot-contract)), so the new build
reads files the old one wrote. From the first public release an update must never lose a user's
settings, presets, name, Wi-Fi or any other state.

Only the settings file has a version and a list of migrations (`app/internal/settings`). The
other files have none, and most need none: their loaders read by key or ignore fields they do
not know.

## Decision

From 0.1.0 on:

- **Every format change ships its migration.** A change to the format of a file on `/mnt/nv`
  comes, in the same PR, with the code that reads the previous release's file and a test that
  loads that file and finds every value in it. A file with a version (settings) gets a step in
  its migrations list. A file without one gets a loader that takes both the old and the new
  shape; a version field is added only when the shapes cannot be told apart.
- **Each release that changes a format leaves a fixture set.** `app/testdata/nv-<release>/`
  holds one file per format, laid out as on `/mnt/nv` and written by that release's own
  writers. The tests load every set through the loaders soundtapd uses
  (`app/cmd/soundtapd/nvstate_test.go`, `app/internal/web/nvstate_test.go`,
  `app/internal/wifi/manager_internal_test.go`). The newest set must equal what the current writers
  produce, so a format change cannot go unnoticed. A released set is never edited; a later format change adds a new set beside it.
- **[`docs/contributors/nv-state.md`](../contributors/nv-state.md) lists every file**
  soundtapd, the init script and the speaker's scripts keep on `/mnt/nv`, with its owner and
  format. A PR that adds a file or
  changes a format updates it.
- **Machine state, logs and caches** (boot logs, the clock, the app override, the BCO image
  marker) may be dropped by a new build, but the new build starts with the old file in place:
  it ignores or replaces it, it does not fail on it.
- **Files other programs own** (BlueZ, bluealsa, librespot, snapserver, Dropbear) keep those
  programs' formats. A version bump of such a package checks in its release notes that it
  still reads its own old files.

**The settings file migrates forward in code.** The settings file outlives every soundtapd
that writes it, so it carries a schema version, which binds already before 0.1.0:

- `Settings.SchemaVersion` (`schemaVersion` in JSON) is stamped on every save. The current
  version is `len(migrations)`; a file without the field is version 0.
- `settings.Migrate` runs the migrations from the file's version on, in order, on the raw JSON
  map before it is decoded. A change to the stored shape adds one function to that list;
  nothing is edited in place.
- A file from a newer soundtapd stays at its path: known fields are read, but the store refuses
  writes, so a rollback cannot discard fields it does not understand. A newer schema is not
  corruption and is not moved aside.
- A corrupt file is kept (`.corrupt`, `.corrupt.1`: the last two copies) before defaults are
  used, so a second corruption does not overwrite the only copy.
- A backup is accepted from version 1 up to the current one through `backupMigrations`, and its
  settings are written in one `settings.Store.Update`, so a failure cannot leave half a restore.
  Live side effects (name, volume, MQTT) are applied only after that store succeeded.
- `PUT /api/settings` rejects unknown fields with 400, so a typo is an error, not a silent
  no-op.

Until 0.1.0 the rest of this does not bind: formats may change without a migration, and
`go test ./cmd/soundtapd -run TestNVFormats -update-nv` rewrites `nv-0.1.0` from the current
writers. The 0.1.0 tag freezes that set.

Alternatives that lost: a version field in every file (most files are a JSON list or a few
`KEY=value` lines whose loaders already accept older shapes); migrating in the installer (the
installer runs from the build being replaced, which does not know the new formats; the build
that reads a file is the one that can upgrade it).

## Consequences

- A format change costs a migration and a fixture set. Most changes are additions, which a JSON
  loader reads from an old file as the zero value; those need only the new set and, where the
  zero value is not the default, an inverse field as the settings already use (`DissolveGroups`,
  `SpotifyOff`).
- A rollback to the other slot runs the older build on newer files. The settings refuse writes
  from an older build; the other loaders ignore what they do not know. A rollback is not
  promised to keep a value only the newer build knows. The tests play the older build on newer
  files and check that opening them changes nothing on disk.
- A user's backup download (`/api/backup`) is not on `/mnt/nv` and is not covered here.

## Code

- `app/internal/settings/settings.go`: `settings.SchemaVersion`, `settings.Migrate` and the
  migrations list; `settings.ErrNewerSchema` and `settings.Store.Update`.
- `app/internal/web/backup_validate.go`: `backupMigrations`, the backup's own versions.
- `app/internal/web/settings.go`: `PUT /api/settings`, which rejects unknown fields.
- `app/testdata/nv-0.1.0`: the fixture set.
- `app/cmd/soundtapd/nvstate_test.go`: `TestNVFormatsAreTheWritersOwn`,
  `TestNVFixturesLoadWithoutLoss`, `TestNVRollbackLeavesNewerFilesInPlace`,
  `TestNVEveryListedFileHasAFixture`.
- `app/internal/web/nvstate_test.go` and `app/internal/wifi/manager_internal_test.go`: the web server's
  and the Wi-Fi manager's files.
