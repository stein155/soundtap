# Test map

What is tested, where the tests live, and how to run them. Every command runs in Docker;
nothing needs a speaker unless the row says so.

```sh
make test         # everything below that needs no speaker and no built image
make test-go      # Go: the daemon soundtapd (app/); make test-go-race runs its tests under -race
make test-web     # the web UI (app/frontend)
make test-e2e     # the web UI end to end: Playwright against the demo speaker (docs/tools)
make test-shell   # Python and shell tests in os/tests/<subject>/ (make test-python, make test-firewall)
make test-bco     # C: the ST20 network chip: its app (bco/app/tests/), our WiLD core (bco/wild/fw/tests/), the first stage
make vuln         # known vulnerabilities: govulncheck (Go) and npm audit (UI), against today's databases
tools/ci/run-ci.sh  # all of the above and make lint, side by side, as CI runs them
```

CI calls the same entry points: each job of `build-stu.yml` runs one step of
`tools/ci/run-ci.sh` (`lint`, `web`, `go`, `go-race`, `python`, `firewall`, `bco`, `vuln`; the
`web` job runs `e2e` after it), so a
green `tools/ci/run-ci.sh` here is a green CI run there. Without arguments the script runs every
step side by side, each into `output/ci/<step>.log` (the web build goes to a scratch directory,
not the Go embed directory), so a run takes as long as its slowest step, not the sum.
`make lint` is separate from `make test`: formatting and static analysis, not tests.
`tools/ci/run-build.sh` builds the image and the package and runs the install gate, in Build, in
the OS build of a pull request and on a build host ([ADR 0011](../../decisions/0011-one-buildroot-pipeline.md)).

| Part of the product | What is tested | Where the tests live | Command |
| --- | --- | --- | --- |
| Web UI | Pages, components and hooks render and react to clicks; lint, formatting, types, production build and its size budget (`scripts/bundle-budget.mjs`: every script, and what the first page loads); end to end in Chromium against the demo speaker (`docs/tools/demo-server.mjs`): first-run Wi-Fi setup, the speaker going away and its live stream coming back, switching speakers, a volume slider by keyboard and pointer | `app/frontend/src/`: a `*.test.ts(x)` file beside each component (vitest); `app/frontend/scripts/` for the budget; `app/frontend/e2e/`: `*.spec.mjs` (Playwright, in the Makefile's pinned Playwright image) | `make test-web`, `make test-e2e` |
| API | Every HTTP route of soundtapd: status codes, JSON shape, access rules, the OpenAPI contract, the stable API's recorded contract per level (`web/api_contract_test.go` against `web/testdata/api-contract-1.json`: a removed or retyped route or field fails, ADR 0108), the production filter check in `/api/health` and the support bundle (fake `nft`; `web/support_bundle_test.go` builds the archive from a fake `/tmp`, `/run` and `/mnt/nv` seeded with secrets in every file it reads and fails on any secret in any file of it, `web/redact_internal_test.go` what the redactor must keep; `web/crashreport_test.go` holds the crash report to the same secrets, and offers each crash once) | `app/internal/`: the packages `web`, `web/firmware`, `web/apierror`, `osapi` | `make test-go` |
| Playback | Source lifecycle, decoder/preemption, standby, volume/mute ordering, multiroom ownership and synchronisation, FIFO track boundaries, display and keypad timing | Go: `app/internal/` (`speakerstate` for standby and wake, `audio`, `spotify`, `tunein`, `upnp`, `snapcast`, `multiroom` (`anchor_test.go` for when the group anchor runs), `bluez`, `btctl`, `icy`, `nowplaying`, `hw` for the keypad, remote, lamps and amplifier lines, `hw/model` for the model table, `panel` for the ST20 panel's screens, `player` for radio and UPnP playback, `supervise` for the media daemons' supervision, `counters` for the counts in `/api/health`, ...), `app/cmd/` (`soundtapd/procs_test.go` for the media daemons and the snapclient gate, `soundtapd/audio_taps_test.go` for the fade at a jump in the leader's delay, `soundtapd/snapserver_nice_test.go` and `player/decoder_nice_test.go` for snapserver and ffmpeg at nice 5, `soundtapd/sa5_test.go` for the SA-5 staying muted until its controller agrees, `soundtapd/battery_ui_test.go` for the Portable's battery messages and idle standby), `app/internal/` for the preview boards against fakes (`hw/battery`: the Portable's gauge and its SHIP conditions; `hw/stm32amp`: the SA-5's controller; `abl`: the SoundTouch adapter's Bose link; ADR 0133), `app/oled/`. Python: `os/tests/playback/` | `make test-go`, `make test-shell` |
| Settings and backup | Atomic persistence, restart, stock import, reset and backup/restore of settings, presets, favourites and recents | `app/internal/` (`settings`, `store`, `favorites`, `recent`, `atomicjson`, `web/backup_test.go`), `app/cmd/` (`identity_test.go`, `wifi_test.go` for the reset, `preset_load_test.go` for an unreadable presets file) | `make test-go` |
| State across updates | The update matrix: every release's `/mnt/nv` fixture set loads without loss or quarantine and saves again; current writes match the newest set; after a rollback the older build reads newer files without moving, rewriting or deleting them (newer settings stay read-only); every file the [state contract](../nv-state.md) lists has a fixture or a stated reason | Go: `app/cmd/` (`soundtapd/nvstate_test.go`), `app/internal/` (`web/nvstate_test.go`). Go: `app/internal/wifi` (`manager_test.go`, the 0.1.0 Wi-Fi files joined by the Wi-Fi manager). Python: `os/tests/boot/` (`test_clock_keep.py`, `ntp-servers` through `clock.sh`) | `make test-go`, `make test-shell` |
| Boot and init script | Init supervision, app rollback, model detection, boot confirmation, boot-count fallback, bootloaders and recovery tools, the U-Boot environment formats and that every writer of the redundant one writes the same bytes (`ubootenv/writers_test.go`), U-Boot's boot count across cold and warm resets (`test_uboot_bootcount_env.py`), which U-Boot copy the SPL boots (`test_spl_second_uboot.py`), the USB-stick recovery on every model with an erased, another board's or Bose's environment, the complete environment it saves, that a refused stick, or one that reads back otherwise, selects nothing, and Bose's own file written back in the way-back order (`test_uboot_usb_recovery.py`; no signature, ADR 0130), the stick's slot write over a fake NAND: a block whose erase or program fails marked bad and written around, at most two, and nothing marked for a write-protected chip, a timeout, a read error or a readback that differs (`UsbRecoveryBadBlocks`), the statement verifier in C (`os/bootloader/uboot/soundtap-sig.c`, no longer compiled into U-Boot) built for the host: the same verdict as `release.Verify` on every statement Go signs and every one an edit away, its policy and its keys equal to soundtapd's (`release/uboot_test.go`), the owner key read from the environment as Go reads it and small-order points refused (`release/uboot_owner_test.go`, `release/owner_test.go`), an environment entry written into both copies and surviving a cut at every step (`flash/envvar_test.go`), a copy on a bad block lost and the other read, written and converted while the bad block is never written (`flash/envlost_test.go`), the WLA U-Boot RAM and product SPL checkers on faked build outputs: a good tree passes and each property they hold fails it alone (`test_wla_uboot_ram.py`, `test_wla_product_spl.py`), the NAND U-Boot checker allowing `nboot.e` only in the stick's way back to Bose (`test_check_wla_uboot_nand.py`), the ROM-USB read script against stub commands, refusing an existing or protected output directory (`test_rom_usb_read.py`), and the ssh NAND backup sending the speaker nothing but reads (`test_nand_backup_ssh.py`) | Python: `os/tests/boot/`. Go: `app/cmd/` (`bootconfirm_test.go`), `app/internal/` (`ubootenv`, `release`) | `make test-shell`, `make test-go` |
| Wi-Fi | The Wi-Fi manager against a fake wpa_supplicant control socket: join, fallback to the last network that worked, wrong password (state and WRONG_KEY event), reasons a join failed, saved networks and their bound, setup AP start and failure (channel, country, SSID length, leases only once soundtapd answers), the bounded setup hold and retry, a new address on a network change, a restarted soundtapd keeping the network, no AP mode, regulatory country and region (manufacturing data, U-Boot environment, the WLA's refusal without a country), udhcpc's arguments; the control-socket client; who brings Wi-Fi up at boot and the ST20 own-BCO interfaces; DHCP and the production filter's own-subnet set | Go: `app/internal/wifi`, `app/internal/wpactrl`, `app/cmd/` (`wifi*_test.go`, `network_setup_test.go`, `lpm_test.go` for the ST300's wired or Wi-Fi report to its processor), `app/internal/speakerstate` (live link and scan table over the control socket; wired needs eth0 carrier and an address). Python and shell: `os/tests/wifi/` (`test_init_wifi.py`, `test_lan_allow.py`, `test_dhcp_bt_events.py`, `firewall_setup_ap_test.sh`), `os/tests/boot/test_clock_keep.py` (the clock step after a join) | `make test-go`, `make test-shell` |
| soundtapd's user and the root helper | soundtap-root ([ADR 0066](../../decisions/0066-production-hardening.md)) over a real socket with fake hardware: every NAND command internal/flash and internal/install run comes out as the same command with the staged bytes passed along and the operations lock held; the running slot, `mtd10`-`mtd12`, ranges outside a partition, an environment copy anywhere else and a script entry `flash.EnvKeys` does not name are refused and nothing runs; every script internal/flash writes passes; only soundtapd's uid and root are answered; oversize and unknown-field messages; boot count, BCO power, host name, GPIO lines and the factory reset of other users' state; the socket 0660 root:group (`rootd`, with fuzz tests of the script and host-name checks); `NetTool` and `ClearAmbient` (`childproc`); init handing soundtapd its devices, directories and, once, its state on `/mnt/nv`, and starting it through `soundtap-root exec-soundtapd` (`test_soundtapd_user.py`); `lan-allow.sh` emptying a shared state file (`test_lan_allow.py`); the image refusing a soundtapd started any other way (`test_rootfs_check.py`) | Go: `app/internal/rootd`, `app/internal/childproc`, `app/internal/web/firmware` (`rootrun_internal_test.go`: the install lane through the helper). Python: `os/tests/boot/test_soundtapd_user.py`, `os/tests/wifi/test_lan_allow.py`, `os/tests/build/test_rootfs_check.py` | `make test-go`, `make test-go-race`, `make test-shell` |
| ST20 network chip (BCO) | The app ([ADR 0031](../../decisions/0031-bco-network-card.md)): WPA2 known answers (SHA-1, HMAC, PRF, PBKDF2, AES and its key wrap against published vectors), the 4-way and group handshakes against a self-built AP, the scan table, the command encodings against captured stock bytes, the join's PEER_ADD step with the real `wifi.c` (it waits for room in a full command ring and never starts the 4-way without it, `wifi_join_test.c`), the flow-control credit reports and LINK_STATS (`mgmt_test.c`, `netcard_test.c`: a freed buffer the driver cannot know of is reported, a report the link refuses is sent again), and a fuzz of what the air delivers to it (beacon IEs, rate IEs, GTK KDE, EAPOL-Key frames with a MIC that verifies) under ASan and UBSan (`air_fuzz_test.c`). Our WiLD core, every source on the host against a simulated host, MAC and air, under ASan and UBSan: the rings, the host's command records and their bounds (`cmd_test.c`), the station module's peer table and CCMP keys, scan and join (rate tables, ACK-rate classes, the probe request, the dwell state machines), the RF power-up and oscillator-measurement waits on an RF that never signals done (they fail the core's bring-up, never hang), the RF tune (channel table, synthesiser words, VCO seed search over a bus model, also with a cap measurement that never signals done), the receive filter, read pointer and CCMP receive (packet numbers, replay, MIC failure, key choice, length bounds), the transmit path (ACK wait, retries, backoff, rate ladder, CCMP transmit), and a fuzz of every frame it reads from the air (`air_fuzz_test.c`). The first stage's register writes under Unicorn. The link's two sides held to one protocol, capability bits and report layouts, and every queue the driver stops for credit watched (`test_bcolink_contract.py`); the driver's counters read into `/api/health` (`app/internal/bco/linkstats_test.go`, `web/health_test.go`) | C: `bco/app/tests/`, `bco/wild/fw/tests/`. Python: `os/tests/hardware/`. Go: `app/internal/` (`bco`, `web`) | `make test-bco`, `make test-shell`, `make test-go` |
| Installer and STU | The one table of supported models (`docs/installer/versions.json`), applied alike by `install.sh`, `install.ps1` and the browser installer to every model, whatever its Bose software version (Node.js runs the page, PowerShell `install.ps1` where installed); browser installer gates and verified completion, package validation, inactive-slot writes and the whole-volume readback of the written rootfs, bounded bootloader copies around bad blocks, the installer on stock firmware marking a slot block whose erase or program fails and writing around it, within its limits and never in another partition, with failures injected through its file backend (`TestBadBlockMarking`, `TestMarkBadPartitions` in `test_wla_installer_offline.py`), a matching copy rewritten once when its read needed many ECC corrections (fake ECC statistics, `bootloaders_ecc_test.go`), API installation and operation locking, no release check before the clock is set (`update`, `web/about_internal_test.go`, `mqttbridge`), the owner key (ADR 0048): an owner-signed package installs only while the key is set and under a release key's rules (`web/firmware/ownerkey_test.go`), a restore cannot set or clear it and a factory reset removes it (`web/ownerkey_test.go`), no MQTT command reaches it (`mqttbridge/ownerkey_test.go`), `make stu` asking for all three models and building the preview package beside the universal one, differing in its installer alone (`test_universal_stu_target.py`, `stu_check.py same-payloads` in `test_stu_check.py`; ADR 0133), the preview feed sent only for a confirmed preview (`test_installer_versions.py`, `test_browser_installer.py`), and the install gate's expectation (`os/support/stu_expect.py`) from a package whose answer is known (`test_nandsim_install.py`) | Go: `app/internal/` (`stu`, `flash`, `install`, `update`, `release`, `web/firmware`, `modeltable` for the generated model table), `app/cmd/` (`buttons_test.go` for the chords). Python: `os/tests/installer/` | `make test-go`, `make test-shell`. With a built STU: the [install gate](install-gate.md) (`os/tests/installer/nandsim-install-test.sh`, `rootfs-run-test.sh`) |
| Build and rootfs | Hosted workflow placement, documented dispatch inputs and Core draft shell gates with local candidate fixtures; the CI entry points against a fake tree and a stub `make` (`test_ci_entry.py`: steps, candidate naming, install-gate cells); package pins, container images pinned by digest (`make lint-image-pins`), the shell scripts `make lint-shell` checks, kernel/rootfs policy, release signing (a production signature refuses a key without a passphrase; an owner signs with their own key), the first-install feeds held to a signed release, each to its own models and the preview feed to the preview package (`test_feed_check.py`), the signing workflow `sign.yml` run step by step against a stub `sign-release.sh` (main only, a validated tag, the key in a private file removed whatever happens, the passphrase only through the environment, a Core release only against its committed reports, publishing after the key is gone; `test_release_workflows.py`), the README screenshots' record of the web UI they were made from, which `release.yml` and `dry-run.sh` check against the released commit (`test_screenshots.py`), a CHANGELOG.md section for every tag, which both release lanes and `dry-run.sh` require (`test_changelog.py`), recorded CVE responses (every ignored CVE carries a reason `os/support/ignore_cves.py` checks; `make image-check` checks its config claims against the built kernel), the source archive's own inputs, the reproducibility check's comparison and double build against fake images and a stub `make` (`test_repro_compare.py`), every copy of the model table held to `os/models.json`, each run where it can be offline: the init script's model detection and codec script, the installer's answers per variant, U-Boot's board detection, both post-build scripts against a fake tree, the STU builder and checker, the recovery and release tools, and soundtapd's generated copy (`test_models_table.py`), and `make dev`'s daemon: built with the host's Go, it answers `/api/version` and `/api/health` as the simulated ST20, announces nothing and stops on Ctrl-C (`test_dev_loop.py`, skipped without Go 1.26) | Python: `os/tests/build/`; `make legal-info` checks the `soundtap-inputs/` directories | `make test-shell`. After `make os`: `make image-check`, and with network `make sbom os-cves` (every Build); two full builds: `make repro-check` |
| Docs and licences | Relative links and anchors, docs pages named in code, per-model evidence entries or explicit gaps, the hardware checklist's open counts, ADR index titles, ADR code references, environment-variable coverage, npm and Go module licences, the licence and copyright of every file (`make lint-reuse`, REUSE), provenance, test locations, the installer page's languages (the web UI's 25, each string file with exactly the English keys, filled in, translated, with the same placeholders and markup, and the page's own English equal to `en.json`: `test_installer_i18n.py`), and no maintainer identifiers or private-history references in the public tree | Python: `os/tests/docs/` | `make test-shell` |
| Hardware | Offline report/provenance validation plus separately authorised per-model boot, audio and recovery measurements; the bench scripts in `tools/hil/` run against a fake speaker that reboots with a new boot id and counts boots as U-Boot does (`test_hil_scripts.py`, `tools/ci/fake_speaker.py`): the hold prompt before each install and reboot, the token on an alpha image, slot and confirm from `/api/health`, rollback, an install kept through three power cuts before its confirm, the console on a failure | [`docs/hardware/hardware-test.md`](../../hardware/hardware-test.md), `tools/hil/` ([layer 3](install-gate.md#layer-3-hardware-in-the-loop-by-hand)). Python: `os/tests/hardware/`. C: `tools/recovery/rom-usb-loader/` (USB loader). | By hand; `make test-shell`. C tests, not in `make test`: `make -C tools/recovery/rom-usb-loader test` |

Tests of the init script run its functions rather than search its text: `os/tests/boot/init_script.py`
sources it with `SOUNDTAP_INIT_LIB=1` (so it does not boot), puts stub commands first on `PATH`,
records their calls and points the script's state paths into a temp directory; the script's calls
to itself (`save-log`, `save-clock`) go to a stub through `INIT_SCRIPT`.

`tools/ci/run-ci.sh` and `tools/ci/run-build.sh` are the entry points CI and a build host call.
`os/support/` holds what the image build, `make stu` and `make image-check` call and the
tests exercise, but which are not tests themselves: the STU, rootfs, kernel-config and
bootloader checkers and the reproducibility check. `tools/ci/` holds the CI helpers besides
the two entry points: the repository lints (licences, image pins, the shell-script list),
`buildroot-reuse.sh` and the fake speaker
the bench-script tests use. `os/tests/fixtures/` holds recorded hardware evidence.
A new test file goes into one of the directories above; `os/tests/docs/test_test_map.py`
fails when a test file sits in a directory this table does not list.

## Contract tests and their limits

- API/UI deadlines and limits: `app/internal/web/ui_contract_internal_test.go`.
- JSON/OpenAPI and generated frontend types: `app/internal/web/openapi_schema_test.go`,
  `types_contract_test.go` and `npm run api-types` / `npm run typecheck`.
- Setup network constants: one definition in `app/internal/wifi` (`APAddress`, `APFallback`,
  `MaxSSIDBytes`), which `speakerstate` uses for what the API and the panel name.
- Goroutines: `app/internal/safego/lint_test.go` parses `app/internal` and fails on a bare
  `go` statement; every goroutine there starts through `safego.Go` (ADR 0052).
- Untrusted input: every parser of what arrives from outside has a Go fuzz test (`func FuzzX`
  in a `fuzz_test.go` or `*_fuzz_test.go` beside it): the STU (`stu`), the `.sig` statement and
  the owner key (`release`), the U-Boot environment copies, fw_printenv text and the
  fw_setenv script (`ubootenv`), SSDP, the device description, Browse answers, DIDL-Lite and
  UPnP times (`upnp`), the ICY stream title (`icy`), the ST300's LPM frames
  (`lpm`) and the environment script and host name soundtap-root takes from soundtapd (`rootd`). Each asserts a property (no panic, a round trip, a cap,
  a host or scheme it must keep), not only that it returns. `go test` runs the seed corpus,
  with any input a fuzzer once found under `testdata/fuzz/`; `go test -run '^$' -fuzz '^FuzzX$'
  -fuzztime 1m ./internal/<pkg>` searches further.
- D-Bus: `app/internal/dbustest` is a message bus for tests. The fake BlueZ (`bluez`,
  `btctl`) and the fake avahi-daemon (`mdns`) are godbus connections on it, so both sides
  exchange real D-Bus messages; `sysbus` tests the handshake timeout and the signal queue.
- MQTT: `app/internal/mqtt` and `mqttbridge` run against brokers written from the
  specification in the tests, so they check the bytes Paho sends, the reconnect after a lost
  connection included.
- Licences: `os/tests/docs/test_go_licenses.py` runs the Go module check
  (`tools/ci/go_licenses.py`) against fake module caches and checks that every row of
  `NOTICE.md`'s Go module table is a module in `app/go.mod`. `make lint-go-licenses` reads
  the real modules' licence files from the module cache, as `make lint-npm-licenses` checks
  the frontend's bundle.
- Documentation: `os/tests/docs/test_doc_links.py` checks inline relative links, heading
  anchors, the `docs/*.md` pages named in code (Go, C, shell, Python, TypeScript, DTS) and
  evidence bookkeeping for Observed compatibility cells. It checks that a source
  or explicit gap is present, not whether the claimed physical behaviour is true.
  `os/tests/docs/test_hardware_open_counts.py` keeps each per-model heading of the hardware
  checklist's open list equal to its `open` rows; `os/tests/docs/test_adr_index.py` keeps
  the ADR index's titles equal to the files' own, and `os/tests/docs/test_adr_code_refs.py`
  checks that the code an ADR cites (paths, `pkg.Symbol`, `path/file.go:Symbol`, a symbol
  linked to its file) exists; its allow-list of older broken references only shrinks.
  `os/tests/docs/test_adr_numbers_in_code.py` fails on a tracked file that cites the number of
  an ADR that does not exist (a deleted one).
  `os/tests/docs/test_root_pages.py` runs the docs site's hook that brings the README,
  CHANGELOG, RUNBOOK and SECURITY pages onto the site. `os/tests/docs/test_public_tree.py` keeps the
  maintainer's serials, MACs, home network and build host out of every tracked file (it
  holds them as SHA-256 digests only), and commit hashes, pull request numbers and pointers to
  the old history out of the Markdown and the text files the image ships (`os/overlay/`,
  `os/board/`, patches aside): the public repository starts from one commit.

A limit shared by two code paths needs a named constant on each side and a contract check.
Keep individual regressions and their history beside the tests; this map lists coverage and
entry points rather than every test name.

Some contracts inspect text (DTS/config values, build pins, shell interfaces and documentation).
They can detect drift but cannot prove execution or hardware behaviour, so each such test says in
its docstring why text is the right tool (a pin, an absence, a constant shared across languages,
a switch only a cross build runs). Prefer exercised code
with fake commands/partitions where possible; the init-script harness and offline C installer
are examples. The [install gate](install-gate.md) describes what each test layer can establish.

For speaker-side audio diagnostics, use the [hardware checklist](../../hardware/hardware-test.md#audio-start-order-on-a-speaker).

## Tests that need more than a checkout

- A built image (`make os`): `make image-check`, and the [install gate](install-gate.md)
  (`os/tests/installer/nandsim-install-test.sh`, `os/tests/installer/rootfs-run-test.sh`). The
  rootfs run executes in every Build and in a pull request's OS build; the simulation runs on
  all four models in every Build of `main`. See the gate for its limits.
- Two full builds: `make repro-check` builds the tree twice from an empty `output/os` and
  compares the images and the STU (`os/support/repro_compare.py`); `reproducible.yml` runs it
  weekly ([ADR 0011](../../decisions/0011-one-buildroot-pipeline.md)). Only that run proves the
  build reproducible; the unit tests prove the comparison.
- A speaker: the [hardware test](../../hardware/hardware-test.md), by hand, with explicit permission for that test.
