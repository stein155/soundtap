# 0111. The ST20 standby screen shows a clock, and the weather for a town the owner picks

Status: accepted — 2026-11-19

## Context

The ST20 has an OLED panel; the ST10, the Wireless Link and the ST300 have none. Stock leaves
that panel dark in standby. Owners asked for a clock on it, optionally with the weather.

A clock needs the time and a timezone, and the rootfs has no zoneinfo and no configured zone.
A speaker could guess its place from its public IP address, but a radio should not contact a
lookup service on its own, and the guess is often wrong. Weather needs a place as well.

## Decision

The standby screen shows a clock in a stored timezone, and optionally the weather for a town
the owner picks in the app.

- **The zone.** Stock shows its clock without a place, so ours does too. Without a town the
  clock uses the zone of the browser that opened the speaker's settings: the web UI stores it
  for a panel that has no zone yet, and again with every display save. Once the owner picks a
  town, its IANA timezone replaces the browser's. Until a zone is known the standby screen stays
  dark.
- **The town and the weather** come from Open-Meteo, which needs no account or key. The town
  search returns the town's coordinates and timezone, and both are stored in the display
  settings. The weather line is off by default. It refreshes every 15 minutes, or every minute
  while there is no reading yet, and every failure is quiet: the clock is shown without a
  weather line. The requests go through `app/internal/safehttp`.
- **The zone data** is built into `soundtapd` (`time/tzdata`), because the rootfs ships no
  zoneinfo. The time itself comes from NTP ([ADR 0125](0125-time-sources-and-clock-gating.md)).

Alternatives that lost:

- **Locating the speaker by its IP address.** The speaker would call a lookup service by itself
  and often get the wrong town.
- **Only the browser's timezone.** It gives no place for the weather, and it is wrong when the
  app is opened from somewhere else; it remains the clock's zone until a town is picked.
- **A weather service with an API key.** It needs a key per installation, or a shared key in a
  public repository.

## Consequences

- The clock shows once the speaker's settings were opened in a browser; a speaker set up and
  never opened there has no zone and no clock.
- The speaker never guesses where it is. It contacts Open-Meteo only after the owner turned the
  weather on, and the town search only while the owner searches.
- Once a town is picked, the timezone follows the town, not the browser. An alarm keeps its own
  timezone field (`settings.Alarm`), used only when the display has no zone.
- The built-in zone data adds about 450 KB to `soundtapd`.
- The feature depends on Open-Meteo staying free and keyless; if that changes, only
  `app/internal/weather` changes.

## Code

- `app/internal/panel/clock.go`: `panel.RunClockScreen` (standby frame, weather refresh) and
  `clockZone`; it imports `time/tzdata`.
- `app/internal/weather/weather.go`: `weather.Geocode` (town search) and `weather.Service`
  (current weather).
- `app/internal/settings/settings.go`: `settings.Display` (`City`, `Lat`, `Timezone`,
  `WeatherOn`), `settings.Display.ClockOn` and `settings.Display.WeatherEnabled`.
- `app/internal/web/settings.go`: the town search route.
- `app/frontend/src/hooks/useSpeakerSettings.ts`: stores the browser's zone for a panel that
  has none.
- `os/overlay/opt/soundtap/clock.sh`: sets the clock from NTP.
