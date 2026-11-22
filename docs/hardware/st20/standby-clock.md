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

SoundTap's clock (`app/internal/panel/clock.go`) walks the same way over the free area of
the panel, one pixel diagonally every 5 s, and keeps its brightness setting on top.
