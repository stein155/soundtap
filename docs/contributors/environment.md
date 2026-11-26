# Environment variables

The init script `os/overlay/etc/init.d/soundtap` hands configuration to `soundtapd` through
the environment. The table lists every variable the daemon reads, plus the ones it sets for
helper processes. `os/tests/docs/test_environment_doc.py` fails when Go code reads a variable
that is not in this table, or when the init script exports one that is not.

Most of these are set by the init script per model; the rest are debug and test knobs.
The *unset* column gives the behaviour when the variable is absent; where the init script
normally sets a value, that is noted there too. These switches are not a no-hardware mode:
`AUDIO_NOINIT=1` does not disable playback, the amplifier fault monitor, buttons, LEDs or
other hardware workers.

| variable | read in | meaning | unset |
|---|---|---|---|
| `MODEL` | `internal/hw/model/model.go` | model key (`st10`, `st20`, `wla`, ...), used only when the board reports no variant | `st10` |
| `MUTE_GPIO` | `cmd/soundtapd/hardware.go`, `internal/hw/amp.go` | sysfs value file of the amplifier mute line | model table |
| `AMP_POWER_GPIO` | `cmd/soundtapd/hardware.go`, `internal/hw/amp.go` | sysfs value file of the amplifier power line | model table |
| `AMP_FAULT_GPIO` | `internal/hw/ampfault.go` | GPIO number of the amplifier fault input (ST20) | model table (`56` for ST20); `0` disables |
| `AIC_GPIO_SPI` | `cmd/soundtapd/codec_gain.go` | `1`: the codec is reached over bit-banged GPIO SPI, not spidev | spidev |
| `AUDIO_NOINIT` | `internal/hw/amp.go`, `cmd/soundtapd/codec_gain.go`, `cmd/soundtapd/player_setup.go` | `1`: skip daemon codec initialisation/gain setup and mute/amp-power writes | hardware is driven |
| `AUDIO_EXTERNAL_INIT` | `cmd/soundtapd/player_setup.go` | `1`: the init script already brought the codec up | soundtapd does it |
| `AUDIO_DEV` | `cmd/soundtapd/audio_mix.go`, `cmd/soundtapd/codec_gain.go`, `cmd/soundtapd/player_setup.go` | playback device, `alsa:<card>` | model default |
| `AUDIO_MIX` | `cmd/soundtapd/audio_mix.go`, `internal/web/debug.go` | `1`: every source goes through the mixer (reported in the debug bundle) | off; init normally sets `1` |
| `AUDIO_BUS` | `cmd/soundtapd/audio_mix.go`, `internal/web/debug.go` | the source bus FIFO (debug bundle) | `/run/soundtap/source.pcm` |
| `AUDIO_TAP` | `cmd/soundtapd/audio_mix.go`, `internal/web/debug.go` | the mixer's copy for Snapcast (debug bundle) | no tap; init sets `/run/soundtap/snapcast.pcm` |
| `AUDIO_PHYSICAL_RATE` | `cmd/soundtapd/audio_mix.go` | positive output sample-rate override for the mixer | 48000; not detected from the card |
| `AUDIO_CLOCK_STATUS` | `cmd/soundtapd/codec_gain.go`, `cmd/soundtapd/latency.go` | ALSA status file used to measure the DAC delay | `/proc/asound/card0/pcm0p/sub0/status` |
| `WLA_AUDIO_READY` | `cmd/soundtapd/wiring.go` | `0`: the Wireless Link's audio did not come up; skip audio | ready |
| `BUTTON_DEVICES` | `cmd/soundtapd/buttons_device.go` | keypad device to read keys from | model table, else the GPIO matrix |
| `BUTTON_MATRIX` | `internal/hw/keypad.go` | `1`/`0`: scan the keypad matrix over GPIO, or not | ST10/ST20 only, when the required GPIO banks are found; used if the keypad device is absent |
| `IR_DEVICE` | `internal/hw/ir.go` | IR receiver device | model table (none on the Wireless Link) |
| `IR_MAP` | `internal/hw/ir.go` | IR code map | `/mnt/nv/ir.map`, then `/etc/soundtap/ir.map` |
| `IR_LEARN` | `internal/hw/ir.go` | `1`: log decoded IR codes without acting on keys | off |
| `IR_RELEASE_GRACE_MS` | `internal/hw/ir.go` | how long a key counts as held without a repeat | 150 ms |
| `LEDS` | `internal/hw/led.go`, `internal/hw/led_wla.go` | lamp directory override | model table; ST10/ST20 prefer detected gpio-leds in `/sys/class/leds`; WLA uses `/sys/class/leds` |
| `GOMEMLIMIT` | `cmd/soundtapd/main.go` | Go memory limit; soundtapd sets its own when unset | soundtapd's limit |
| `SOUNDTAP_FIRMWARE` | `cmd/soundtapd/audio_sink.go`, `cmd/soundtapd/main.go`, `internal/speakerstate/backend.go`, `internal/update/update.go` | `1`: enable firmware-specific runtime behaviour; OS and app updates use their separate lanes | non-firmware runtime defaults |
| `SOUNDTAP_LOG_LEVEL` | `cmd/soundtapd/main.go` | `debug`, `info`, `warn`, `error` | `info` |
| `SOUNDTAP_DEVICE_ID` | `cmd/soundtapd/identity.go` | override the device id | the SoC's MAC |
| `SOUNDTAP_DEFAULT_NAME` | `cmd/soundtapd/identity.go` | the name before the user picks one (the init script passes stock's name) | `SoundTap <MODEL> <last 6 of device id>` |
| `SOUNDTAP_SETUP_NAME` | `internal/speakerstate/backend.go` | file holding the setup network's SSID | `/run/soundtap/default-name` |
| `SOUNDTAP_SETUP_AP_MARKER` | `internal/speakerstate/backend.go`, `internal/web/web.go` | file that exists while the setup AP is up | none |
| `SOUNDTAP_WIFI_MODE` | `cmd/soundtapd/wifi.go`, `internal/speakerstate/backend.go`, `internal/speakerstate/backend_wifi.go`, `internal/speakerstate/wifilink.go` | `scm`: Linux `wlan0` (including ST20 with SoundTap's BCO app), brought up by soundtapd's Wi-Fi manager on a flashed speaker; `bco`: legacy stock-BCO control; `disabled`: refuse Wi-Fi setup and return no scan results | Linux `wlan0`, but no Wi-Fi manager |
| `SOUNDTAP_BCO_TTY` | `cmd/soundtapd/main.go`, `internal/bco/lanwatch.go`, `internal/speakerstate/backend_wifi.go`, `internal/speakerstate/wifilink.go` | serial port to the ST20's BCO | none |
| `SOUNDTAP_SPOTIFY_BOUNDARY` | `spotify-source.sh`, set by `cmd/soundtapd/procs.go` for librespot | `1`: use synchronous sink boundaries with upstream `--disable-gapless` | legacy asynchronous hooks |
| `SOUNDTAP_AIRPLAY_MODE` | `cmd/soundtapd/procs.go` | `scm-ap2`: permit shairport and run nqptp; `bco-ap1`: skip both; the integration setting also controls shairport | shairport enabled except on ST20; nqptp off. Init normally sets `scm-ap2` |
| `SOUNDTAP_MULTIROOM` | `cmd/soundtapd/wiring.go` | `1`: multiroom is switched on | off |
| `SOUNDTAP_SNAPCLIENT_ID` | `cmd/soundtapd/procs.go`, `cmd/soundtapd/wiring.go` | this speaker's Snapclient id | none |
| `SOUNDTAP_SNAPSERVER_CONF` | `cmd/soundtapd/procs.go` | snapserver configuration the init script prepared; set means soundtapd runs snapserver and snapclient | none |

The defaults above follow the daemon readers, especially
[`audio_mix.go`](../../app/cmd/soundtapd/audio_mix.go),
[`ampfault.go`](../../app/internal/hw/ampfault.go) and
[`procs.go`](../../app/cmd/soundtapd/procs.go). The firmware-provided values come from
[`os/overlay/etc/init.d/soundtap`](../../os/overlay/etc/init.d/soundtap). The documentation
check covers variable names, not the meanings or defaults in this table.
