# ST20 standby clock: how stock keeps the OLED from burning in

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).
Static analysis only; nothing here was measured on a speaker.

The stock clock in standby is drawn by BoseApp (`CClockScreenTO`, moved by
`CWalkingClockItem`). Its one measure against burn-in is that it walks:

- **Path.** The clock group (four digits and the colon, plus the PM marker or the music cue
  when shown) moves one pixel in x and one in y together, diagonally, and turns back on the
  axis that reaches an edge. The first direction is one of the four diagonals at random.
- **Area.** y runs over 0 to 71 and x over 0 to 122 minus the group's width, with the group
  drawn 3 pixels right and 7 down of that place: over time it covers columns 3 to 124 and
  rows 7 to 99. A one-digit hour on a 12-hour clock shifts x to keep the group in place.
- **Speed.** One step about every 4.9 s (a 5000 ms interval counted on a 120 ms animation
  tick): a vertical round trip takes about 12 minutes.
- **No dimming.** The clock's brightness setting (0 to 100, default 70) only switches the
  clock off at 0; any other value draws the same full-white digits. Nothing in userspace
  changes the panel's contrast, precharge or grey-scale table, and there is no night
  dimming.
- **No timeout.** The clock stays up for as long as the speaker is in standby. The panel
  goes dark only with the clock switched off, or when the low-power standby cuts the
  display's power.

## Where stock keeps the clock's settings

BoseApp keeps them in `/mnt/nv/BoseApp-Persistence/1/ClockDisplay.xml` (its persistable
`ClockDisplay`), one `clockConfig` element inside `clockDisplay` with every field an attribute
(seen on the owner's ST20, 2026-11-24):

| Attribute | Default (`ClockConfiguration.proto` in `libCore.so`) | Meaning |
| --- | --- | --- |
| `timezoneInfo` | empty | IANA zone, as `Europe/Amsterdam` |
| `userEnable` | `true` | the clock on or off |
| `timeFormat` | `TIME_FORMAT_12HOUR_ID` | or `TIME_FORMAT_24HOUR_ID` |
| `brightnessLevel` | 70 | 0 switches the clock off; other values draw the same |
| `userOffsetMinute`, `userUtcTime` | 0 | a hand-set time |

It also links `/mnt/nv/localtime` to `/usr/share/zoneinfo/<zone>`. On the first start after an
install from stock soundtapd takes these over once
([ADR 0129](../../decisions/0129-first-install-keeps-settings.md)).

## SoundTap

SoundTap's clock (`app/internal/panel/clock.go`) does not walk the panel as stock's does: it
is dim, grey 7 of 15, and every minute moves to the next of nine places no more than 4 px from
the middle (`driftPath` in `app/internal/panel/panel.go`). Like stock it shows the time alone,
with AM/PM under it on a 12-hour clock: no date and no weather, which would light more pixels
for the whole of standby ([ADR 0111](../../decisions/0111-st20-standby-clock.md)). Its digits are
Montserrat 600, a lighter weight than the panel's titles, nearer stock's thin clock.

While music plays the panel has no status bar: no clock, source or room is drawn, as those
would stay lit in one place for as long as something plays. A pause or stop icon, a preset's
number, the radio's badge, a stereo pair's L or R and the Portable's battery are drawn only
when they apply. The playing screen orbits: every minute it moves one pixel around its place
and back (`orbitFrame`). Thirty seconds after the last key it keeps only its title, at grey 8,
drifting as the clock does.
