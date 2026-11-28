# 0055. Who may change things: the home network, the Bluetooth window, a key held on the speaker

Status: accepted — 2026-11-20

## Context

SoundTap speakers are local speakers, like stock SoundTouch and Sonos, and are not meant to be
reachable from outside the home network. A household token on every write, with each browser
paired to each speaker, kept locking the owner out: Safari empties a page's localStorage after
seven days without a visit, and every other name or address the UI is opened on is a new origin
with its own storage. Nobody in the house should have to pair a browser.

Without a token, anyone on the LAN or on the open setup hotspot could factory-reset a speaker,
move it to another network, install firmware or read its diagnostics. Dangerous things need a
confirmation on the device, not a password. Bluetooth is reachable by anyone in range, and every
speaker has a settings page to start pairing from, so pairing needs no always-open radio.

A production image has no shell ([ADR 0119](0119-no-ssh-in-production.md)), so HTTP on :80 is
the only way to see inside a speaker.

## Decision

### The home network is trusted

Writes need no token, and speakers call each other without one. What stays is protection
against web pages, not against LAN hosts: the Host check (DNS rebinding) and the
Origin/Sec-Fetch-Site check (CSRF) in `guard.go`, and `{"confirm":true}` on destructive routes.
Before the device proxy forwards to a peer, it checks that the announced address answers as
that speaker (`peerVerified`): the registry is filled from mDNS, which any LAN host can answer.
That check prevents misrouting; it is not authentication. Signed updates are
[ADR 0048](0048-signed-updates.md).

### Dangerous actions need a key held on the speaker

- **Which requests.** `needsPress` names them: `POST /api/factory-reset`, `/api/restore`,
  `/api/reboot`, `/api/wifi`, `/api/setup`, `/api/bluetooth/clear`, `/api/bluetooth/forget`,
  `/api/soundtapd`, `POST /api/firmware?install=1`, `DELETE /api/soundtapd`, and
  `GET /api/debug`. The install uploads wait for every package, signed or not: a release's
  `.sig` can be sent with another body, and the press is also what lets an unsigned one install
  ([ADR 0048](0048-signed-updates.md)). `POST /api/update` and `POST /api/core-update`, where
  the speaker fetches a signed release itself, never wait
  ([ADR 0118](0118-update-button-without-press.md)). Uploading a firmware file without
  installing it does not wait. `POST /api/bluetooth/forget` checks its body and that the
  address is paired before it waits, so a bad or stale address is refused at once rather than
  after the walk to the speaker; every other route waits before it reads its body.
- **The gesture.** One key on the speaker's own panel, other than volume, held for two seconds
  (`confirmHold`), confirms the request that was waiting when the key went down. A shorter press
  confirms nothing. While a request waits that key belongs to the confirmation: it does not
  play, store or switch anything. Volume keys never confirm and keep working, so the speaker can
  still be turned down.
- **Which keys.** The ST10's and ST20's keypad and the Wireless Link's two buttons confirm. Their
  IR remote never does: a confirmation stands for someone at the speaker, and a remote works from
  the next room. The SoundTouch 300 has no buttons of its own, so the keys its soundbar processor
  reports from its remote confirm, and soundtapd logs the producer of every confirming hold;
  refusing them would leave a production ST300 unable to install an upload, reset or go back
  to Bose.
- **One waits, the newest wins.** A newer dangerous request replaces the waiting one, which fails
  with 409 `confirm_superseded`. A host that repeats a request then only replaces itself, and the
  owner's newest request still gets the lane, so no rate limit is needed. After 60 s a request
  fails with 403 `not_confirmed`.
- **The speaker shows what waits.** ST10 lamps blink white, the WLA blinks amber fast, the ST20
  display asks under CONFIRM what it confirms ("Reset?", "Install?", "Change Wi-Fi?") over "Hold a
  button". The UI says
  "Hold a button on the speaker, not volume, for two seconds to confirm" and tells a browser
  whose request was replaced.
- **Root skips the hold.** A one-shot token in `/run/soundtap/confirm-token` (mode 0600), sent as
  `X-SoundTap-Device-Token`, confirms at once and rotates on every use. It needs root on the
  speaker, so on a production image the hold is the only way.
- Speaker-to-speaker routes (proxy, multiroom invite and follow) are not gated: a peer that is
  asked to do something dangerous waits for its own hold.
- The gate applies only under `SOUNDTAP_FIRMWARE=1`, so tests and desktop runs are unchanged.

### Bluetooth pairs only inside a five-minute window

soundtapd's agent ([ADR 0114](0114-bluetooth-agent-in-soundtapd.md)) owns the window. `Pair`,
called by `POST /api/bluetooth/pair` and by the Bluetooth key's hold, opens a five-minute window
(`bluez.PairWindow`) and makes the adapter pairable and discoverable; selecting Bluetooth with
nothing paired opens it too. Inside the window the agent accepts just-works pairing and
authorizes only the A2DP/AVRCP UUIDs (0x110a–0x110e); outside it, new authorization requests are
refused. A successful pairing is trusted and shortens the rest of the window to 15 seconds.
Paired, trusted devices reconnect outside the window. soundtapd closes the window itself
(Discoverable, then Pairable off, then Connectable back on, retried until it works), and BlueZ's
own discoverable timeout stays 0, because that timeout leaves the controller answering inquiries
([WLA Bluetooth notes](../hardware/wla/bluetooth-and-analog-output.md#bluetooth)). Pairings are cleared
on the web page only, behind the hold.

### The support bundle is redacted text behind the hold

`GET /api/debug` is a plain-text bundle (version, network, resources, CPU, discovery, installer
and slot state, filtered kernel messages, 32 KiB log tails); `?download=1` is the support archive
behind the same route and hold: the text, `/api/version`, `/api/health`, the whole kernel ring
buffer, every log in full (at most 512 KiB each) and `<home>/last-boot`. Nothing on the debug
route changes state. Every line of the text and of every file in the archive passes one
function, `redactor.scrub`: it replaces by value what the speaker holds (Wi-Fi names and keys,
the network it is on, the MQTT password, librespot's credentials, the press token, a household
token older firmware left), then by pattern (credential assignments, URL userinfo, network names
given as a key, public IP addresses). Private, loopback and link-local addresses stay.

Alternatives that lost: a household token, also kept in a server-set cookie (survives Safari's
seven-day cap, not a second name or address, and still asks for pairing); exempting only private
addresses (the same result at home with more code); any key as confirmation, the IR remote
included (a volume key or a remote in the next room would confirm); refusing a second request
while one waits (a host that repeats a request keeps the owner's out for good); always
discoverable Bluetooth with restricted profiles (anyone in range can still pair and stream);
leaving the close to BlueZ's `DiscoverableTimeout` (the controller keeps answering inquiries).

## Consequences

- Anyone on the home network or the setup hotspot can play and change settings, with curl or the
  UI. Reset, restore, Wi-Fi changes, unsigned installs and diagnostics need a hold; a signed
  release installs without one, older ones included ([ADR 0048](0048-signed-updates.md)).
- A speaker must not be exposed beyond the LAN (no port forward); there is no second line.
- Someone on the LAN can replace the owner's waiting request with their own. The ST20 panel names
  the action and the owner's page says its request was replaced; on the ST10 and the Wireless
  Link the page is the only sign.
- The proxy and the UI allow 75 s for a gated request instead of the usual timeout. Scripted
  manual installs on a production image need a person at the speaker.
- A stranger in Bluetooth range can pair only in the minutes after someone asked for it.
- Anything added to the support bundle goes through the redactor. A new kind of secret on the
  speaker needs its source in `newRedactor`, and `TestSupportBundleCarriesNoSecrets` must still
  pass. A network called by a common word replaces that word wherever it stands alone: a mangled
  line beats a leaked name.
- Debugging over HTTP opens no way around signing; test builds still go through the signed
  upload routes.
- The "Confirmation on the speaker" rows of
  [the hardware checklist](../hardware/hardware-test.md#release-checklist-per-model) prove the hold per
  model.

## Code

- `app/internal/web/guard.go`: `Server.guard`, the Host and Origin checks.
- `app/internal/web/deviceproxy.go:peerVerified`: the peer identity check.
- `app/internal/web/confirm.go`: `needsPress`, `Server.onDevice`, `web.DeviceConfirm`,
  `web.ErrConfirmSuperseded`, `web.DeviceTokenHeader`; `app/internal/web/web.go:Server.updateApp`
  asks only when a tag is named.
- `app/cmd/soundtapd/confirm.go`: `deviceConfirm` (the lane, the token, the panel text) and
  `confirmHold`; `app/cmd/soundtapd/buttons.go:buttonHandler.confirmPressed` times the hold;
  `app/cmd/soundtapd/soundbar.go:newSoundbarKeys` marks the ST300's processor keys as confirming.
- `app/internal/bluez/agent.go`: `Agent.Pair`, `Agent.tick` (the close), `bluez.PairWindow`;
  `os/overlay/etc/bluetooth/main.conf` keeps BlueZ's timeouts at 0.
- `app/internal/web/debug.go:debugBundle`, `app/internal/web/support.go:supportArchive`,
  `app/internal/web/redact.go` (`Server.newRedactor`, `redactor.scrub`).
- `app/internal/web/route_access_test.go` gives every route its class;
  `app/internal/web/support_bundle_test.go:TestSupportBundleCarriesNoSecrets`.
