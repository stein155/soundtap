# 0111. The ST20 standby screen shows the time alone

Status: accepted — 2026-11-19, revised 2026-11-24

## Context

The ST20 has an OLED panel; the ST10, the Wireless Link and the ST300 have none. Owners asked
for a clock on it in standby, as stock's software shows.

A clock needs the time and a timezone, and the rootfs has no zoneinfo and no configured zone.
A speaker could guess its place from its public IP address, but a radio should not contact a
lookup service on its own, and the guess is often wrong.

An OLED wears where it is lit: what stays on the panel for the whole of standby, day after
day, ages those pixels first. Stock's clock is the time alone and walks over the panel
([standby clock](../hardware/st20/standby-clock.md)).

## Decision

The standby screen shows the time alone, in the zone of the browser that saved the settings.

- **What it shows.** The time; on a 12-hour clock AM/PM on a line under it. No date, no
  weather. The group walks one pixel diagonally every 5 s over the free area, as stock's does,
  at one brightness: like stock's, the clock does not dim, and there is no setting for it.
- **The zone.** Stock shows its clock without a place, so SoundTap's does too: the web UI stores the
  zone of the browser that opens the speaker's settings for a panel that has none yet, and
  again with every display save. A first install from stock takes stock's clock settings and
  zone instead ([ADR 0129](0129-first-install-keeps-settings.md)). Until a zone is known the
  standby screen stays dark.
- **The zone data** is built into `soundtapd` (`time/tzdata`), because the rootfs ships no
  zoneinfo. The time itself comes from NTP ([ADR 0125](0125-time-sources-and-clock-gating.md)).
- **Settings.** Clock on or off, 12 or 24 hours. Settings files from before 0.1.0 may still
  hold `city`, `lat`, `lon`, `weatherOn` and `brightness`; the loader ignores them, and
  `PUT /api/settings` accepts and drops a `display.brightness` an older page still sends.

Alternatives that lost:

- **A date and weather line under the clock** (with a town looked up at Open-Meteo), which
  alpha builds had. A second line keeps more pixels lit for the whole of standby and leaves
  the walk less room; the weather also made the speaker contact a service, and the town search
  added a route and a page of settings. Removed before 0.1.0.
- **Locating the speaker by its IP address.** The speaker would call a lookup service by itself
  and often get the wrong town.
- **A timezone picker.** One more setting for the case of a browser in another zone than the
  speaker; saving the display settings from a browser at home puts it right.

## Consequences

- The clock shows once the speaker's settings were opened in a browser, or at once after an
  install from stock that had a zone; a speaker without either has no zone and no clock.
- The speaker never guesses where it is and contacts no service for the clock beyond NTP.
- Saving the display settings from a browser in another timezone moves the clock to that zone.
  An alarm keeps its own timezone field (`settings.Alarm`), used only when the display has no
  zone.
- The built-in zone data adds about 450 KB to `soundtapd`.

## Code

- `app/internal/panel/clock.go`: `panel.RunClockScreen` (standby frame and walk) and
  `clockZone`; it imports `time/tzdata`.
- `app/oled/screen/screens.go`: `screen.Standby`.
- `app/internal/settings/settings.go`: `settings.Display` and `settings.Display.ClockOn`.
- `app/frontend/src/hooks/useSpeakerSettings.ts`: stores the browser's zone.
- `os/overlay/opt/soundtap/clock.sh`: sets the clock from NTP.
