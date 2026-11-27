# ST20 BCO recovery: current limits and stock evidence

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

The BCO is the DM870 network processor. SoundTap runs its own app and WiLD core above the
existing BFL and BSL loaders. This page preserves read-only analysis of stock
`Update_ti_27.0.6.46330.5043500.scm.stu`; it is not a recovery session authorisation.

## Current recovery boundaries

- Follow [the contract](../../../CONTRIBUTING.md#the-contract) for every write, reset and power cycle.
  `SMSC_BSL_Update`, `SMSCControlApp bsl`, raw memory writes and guessed commands are
  forbidden, including on sacrificial hardware. BFL and BSL stay stock.
- App recovery uses the existing loaders' **app** TFTP/XMODEM path (ADR 0031). Entering
  BSL to load an app is different from replacing BSL. The 2026-10-16 app boot through
  real BFL is recorded in [the network card notes](bco-network-card.md#soundtaps-first-stage-through-the-real-bfl);
  it is not a fault-injection test of every interrupted-flash case.
- A running SoundTap ST20 installs its own BCO app at boot; the web API has no route to
  flash another one. A manual restore goes through the `bcoflash` development tool, which
  checks the image against the allowlist. A finished transfer is not evidence of completed
  recovery.
- Before an authorised app restore, verify the stock image and an independent SCM access
  route for this board. The BCO carries the LAN: replacing its app can remove LAN access.
  USB TAP is SCM-owned but still depends on a working SCM kernel, USB configuration and
  usable service. It does not remain reachable through every possible fault.
- SoundTap's rootfs does not supply Bose's ELF flashers or the stock `S97shelby_local`
  boot hook. Do not rely on `/opt/Bose/` or a staged `/mnt/nv/rc.local` on SoundTap.
  SCM recovery is described in [the install records](../install-records.md#going-back-to-bose)
  and [ROM-USB notes](rom-usb-recovery.md); it does not repair BCO loaders.

## Reading the evidence

**[STATIC]** means found in an extracted script, symbol table or string, not executed
on hardware. **[INFERRED]** means an interpretation of those observations. The original
notes called static observations "PROVEN"; that did not establish recovery success.
See the [evidence rules](../../contributors/evidence.md).

"BCO", "DM870" and "SMSC" name the same processor in these sources.
`App_BD_DMP*.bcd` is the application; `Bootloader_CE2*.bcd` is the BSL image.
The stock binary and script hashes were not recorded individually in these notes;
addresses or strings must be re-associated with exact source files before reuse.

## 1. What each binary does

All four are the **same C++ class `SMSCUpdateControl`** (identical symbol set in
each ELF) wrapped as different entry points. They talk to the BCO over the
**internal** serial line `/dev/ttyO5` at 115200 (not the external recovery
console `ttyO3`), and power-cycle the BCO via GPIO14. **[STATIC]**

| Binary | Usage string | Role | Risk |
| --- | --- | --- | --- |
| `SMSC_App_Update` | `Usage: SMSC_App_Update <app.bcd>` | Flash the BCO **application** only | Recovery conditional |
| `SMSC_BSL_Update` | `Usage: SMSC_BSL_Update <bsl.bcd>` | Flash the BCO **second-stage bootloader** | **Dangerous** |
| `SMSCControlApp` | `Usage: SMSCControlApp <command: save, restore, erase, app, bsl>` | Combined tool, including MAC save/restore | app write / BSL forbidden |
| `SMSC_Update` | (no bare usage; driven by `SoftwareUpdateInstaller`) | The **production** updater run during a real software update; version-checked orchestration | app + BSL per manifest |

Evidence (strings on `rootfs/opt/Bose/…`):

- `SMSC_App_Update`: `Usage: SMSC_App_Update <app.bcd>`, `/dev/ttyO5`. **[STATIC]**
- `SMSC_BSL_Update`: `Usage: SMSC_BSL_Update <bsl.bcd>`,
  `Waiting for xmodem Write to complete`, `/dev/ttyO5`. **[STATIC]**
- `SMSCControlApp`: `Usage: SMSCControlApp <command: save, restore, erase, app, bsl>`,
  `/dev/ttyO5`, `/mnt/update/BCO_MAC.txt`, `TTY5Serial`. **[STATIC]**
- Shared `SMSCUpdateControl` methods across all four: `PullSMSCOutOfReset`,
  `SendControlA`, `EnteredFirstBootLoaderMenu`, `EnterSecondBootLoaderMenu`,
  `SelectEraseFlash`/`ConfirmEraseFlash`, `SelectWriteApp`, `SelectUpdateBSLOption`,
  `EnableAppUpdateAction`, `EnableBSLUpdateAction`, `WaitForAppUpdateComplete`,
  `AppCompleted`, `SaveMACAddress`/`RestoreMAC`, `SelectWriteMAC`,
  `FirstBootloaderTimeout`/`SecondBootloaderTimeout`. **[STATIC]**

`SMSC_Update` is the module that `SoftwareUpdateInstaller` runs from
`rootfs/opt/Bose/etc/SCM_Update.xml`. That manifest is the authoritative
description of the stock update configuration: **[STATIC]**

```
<UpdateModule VersionCheck="true" Path="./" Name="SMSC_Update" Retries="0">
  <Image Path="[BCO_RELEASE_DIR]/InputData/bootloader/SecondLevelBootloader/"
         Name="Bootloader_CE2_Release_8MB_3.x-3_8_0-9423_ER.bcd" Revision="B201306111041"/>
  <Image Path="[BCO_RELEASE_DIR]/OutputData/"
         Name="App_BD_DMP 3.x-3_8_0-9423_ER.bcd"           Revision="I2014102015199423"/>
</UpdateModule>
```

So a stock update carries both the BSL and the app, and gates each on a pinned
`Revision` and `VersionCheck="true"`: it does not blindly rewrite. **[STATIC]**

### Images present in the extracted stock SCM rootfs **[STATIC]**

`rootfs/opt/Bose/`:

- `App_BD_DMP 3.x-3_8_0-9423_ER.bcd`: sha256 `9446fca43b6bfe5001bd6e79d66bef75213ee5b8a4adfa70fad05b2b4a8d3829`
- `Bootloader_CE2_Release_8MB_3.x-3_8_0-9423_ER.bcd`: sha256 `1eec9dc2bfb5cbd2344e04aa425d44ad80f92407cee6539bb550fc2402c3967a`

The original analysis recorded the app hash as identical to the STU module. These hashes
identify historical source inputs, not a newly verified recovery candidate. Verify local
inputs against the source inventory before an authorised restore; SoundTap does not carry
these stock files.

### GPIO / power wiring **[STATIC]**

`rootfs/usr/local/etc/gpio_helper` (only the SCM has a BCO, `module_type = scm`):

- `DM870_power_on()` → `echo -n 0 > /sys/class/gpio/gpio14/value`
- `DM870_power_off()` → `echo -n 1 > /sys/class/gpio/gpio14/value`

GPIO14 low = BCO powered; high = BCO off. BoseApp also opens the node
`/dev/gpiodev/dm870pwr` for the same rail. `has-bco` (`rootfs/usr/bin/has-bco`)
is simply `[ "$(cat /proc/module_type)" = scm ]`.

## 2. Flash sequences found in stock scripts (not instructions to execute)

Two reference shell scripts on the rootfs spell out the wire protocol literally.
**They cannot run unmodified on the stock rootfs** (section 2a), but they are the clearest
documentation of the sequence; the stock manifest invokes the ELF `SMSC_Update`. They do not
prove a successful recovery.

### App flash — `rootfs/usr/bin/updatebcoapp.sh` **[STATIC]**

1. `stty -F /dev/ttyO5 raw; stty -F /dev/ttyO5 115200`
2. GPIO14=1 (BCO off) → sleep 1 → GPIO14=0 (BCO on): **power-cycle into the loader**
3. `printf '\001'` (Ctrl-A) → prints `In BFL ...` (First Boot Loader menu)
4. `printf '4'` → `In BSL ...`, then **sleep 10** (jump into the Second/BSL menu)
5. `printf '1'` → `Expecting xmodem download ...`
6. `lsz -X … $bcoapp` — XMODEM-send the **app** `.bcd`
7. power-cycle again (GPIO14 1→0) to boot the new app

- Sanity guard: refuses a file `< 5,000,000` bytes ("appears to be too small").

### BSL flash — `rootfs/usr/bin/updatebcobsl.sh` **[STATIC]**

1. same stty + power-cycle
2. `printf '\001'` (Ctrl-A) → `In BFL ...`
3. `printf '1'` → `Expecting xmodem download ...` **directly** (no `4`, no BSL jump)
4. `lsz -X … $bcobsl` — XMODEM-send the **BSL** `.bcd`
5. power-cycle

- Sanity guard: refuses a file `> 2,000,000` bytes ("BSL file appears to be too big").

The structural difference:

- The **First Boot Loader (BFL)**, entered by Ctrl-A after every power-cycle, is
  the intended loader entry. Neither script explicitly requests a BFL write; that
  does not establish physical write protection or recovery after an interrupted erase.
- From BFL, menu option **`4`** enters the already-flashed BSL, whose option `1`
  programs the **application**. That is the app path, two loaders deep, so the
  primary BFL is untouched. **[STATIC sequence] / [INFERRED menu semantics]**
- From BFL, menu option **`1`** programs the **BSL/second-stage** region
  directly. This overwrites the second-stage loader itself. **[STATIC sequence] /
  [INFERRED that "1 at BFL" targets the BSL region]**

This matches the ELF symbols exactly: `SendControlA` →
`EnteredFirstBootLoaderMenu` → (`EnterSecondBootLoaderMenu` +
`SelectWriteApp`) for app, vs (`SelectUpdateBSLOption` / `EnableBSLUpdateAction`)
for BSL; `SelectEraseFlash`/`ConfirmEraseFlash` before the write;
`WaitForAppUpdateComplete`/`AppCompleted` after. **[STATIC]**

The runtime (non-flash) BCO messaging channel is a **different** serial port,
`/dev/ttyO4` (`SMSCSerialDriver`, `MSG_ID_BCO_*`); only flashing uses `ttyO5`.
`reset-airplay.sh` clears `/var/run/bco_spi_*` on reset. **[STATIC]**

### 2a. Missing dependencies in the extracted rootfs **[STATIC]**

`updatebcoapp.sh` / `updatebcobsl.sh` both call **`lsz -X`** (lrzsz XMODEM send),
and the logging branch calls **`socat`**. Neither `lsz`/`lrzsz` nor `socat` exists
anywhere on the stock rootfs (only `/usr/bin/microcom` is present). So these two
scripts **cannot run as written on a stock device**; they are documentation or
bench tools. The ELF binaries expose the corresponding menu/XMODEM symbols; that is
static evidence of their implementation, not a recommendation to run them on SoundTap.
`SMSCControlApp` also exposes MAC save/restore using `/mnt/update/BCO_MAC.txt`.
`microcom` is present and `reset-airplay.sh` names `/dev/ttyO5`. **[STATIC]**

## 3. What the static analysis does not prove

The scripts separate application and BSL updates, suggesting app recovery can preserve
both loaders. Whether an interrupted write leaves the app recoverable depends on the
actual erase/write regions and functioning loader entry. Neither menu strings nor the
presence of an image in the rootfs proves this. No permission to test a BSL write follows.

## 4. Stock management vocabulary

These verbs were found in BoseApp's `AirplayController` help/log strings. Their existence
is static evidence; their completeness, idempotence and recovery effects were not measured.
They are not a SoundTap troubleshooting ladder. A settings reset can change persistent
state even when it does not replace executable firmware.

| Stock verb | Meaning suggested by help/log strings |
| --- | --- |
| `airplay cne rs mfg` | Restore BCO MAC, HOSTMAC, country and persistent CNE settings from MFGDATA |
| `airplay reset cne settings` | Reset CNE settings to defaults |
| `airplay cne pr` / `airplay cne rs` | Print / restore CNE settings |
| `airplay per pr` / `airplay per rs` | Print / restore persistence |
| `airplay bco reset control` | Request BCO reset; help lists delay, attempt and NTP-sync parameters |
| `airplay restore factory defaults` | BCO factory reset; logs mention a subsequent reboot |
| `airplay graceful shutdown` | Quiesce the stock AirPlay stack |
| `airplay boot into bsl` | Request loader entry; not proof of a successful flash |

Other stock strings: `BCORestoreFactoryDefaults`, `EVT_AIRPLAY_CLI_RESTORE_CNE_SETTINGS_MFGDATA_ONLY`,
`EVT_AIRPLAY_CLI_BCO_RESET_CONTROL`, `/mnt/nv/bco_needs_factory_reset`, `/setBCOReset`,
`/getBCOReset`, `DM8X0_NEEDS_TRIGGER_TO_ENTER_BSL`.

## 5. Stock SCM observations

The extracted `S97shelby_local` runs executable `/mnt/nv/rc.local` as root before
`S99SoundTouch`; its stop action calls `DM870_power_off` and `sync`. This describes
stock init only. `SCM_Update.xml` names `SoftwareUpdateInstaller`, `SoftwareUpdate`,
`SCMReset_Update`, `ARMUpdate` and `UbootUpdate`; none is part of SoundTap's STU.
`rootfs/etc/init.d/shelby_usb` uses `microbswitch` on lisa/ST20, otherwise `g_ether`.

Stock sdsShell on USB port 8000 was not reachable in the recorded provisioned and
factory-reset observations. `/dev/TelnetShell` was found as a device-node name; it does
not establish a remotely reachable recovery shell.

## 6. The stock app's saved network

Bose's BCO app keeps the Wi-Fi network it joins in the BCO's own NOR flash; the SCM holds no
copy. SoundTap reads it once, on the first boot after an install from stock, before it
replaces that app ([ADR 0129](../../decisions/0129-first-install-keeps-settings.md);
`app/internal/bco/stockprofile.go`). The facts below come from offline analysis of stock BCO
app 9423 **[STATIC]**, except where marked as read on hardware. Numbers are that app version's.

- **The store.** Stock's "CnE User" settings store is an append-only log in a NOR sector. Two
  sectors take turns: one active, one free. When the active one is full, stock copies the
  valid records into the other. Candidate addresses are `0x40030000` (read on hardware
  2026-11-11; the owner's dump began with the u16 `0x0008`) and `0x407e0000` (unproven).
- **Sector header.** The low nibble of the sector's first u16 is its state: `0xf` not used,
  `0xe` free, `0xc` being written, `0x8` active, `0x0` being removed.
- **Records** start at sector offset 2: a little-endian u16 header, the value as a
  NUL-terminated string, and one pad byte (written as 0) when the next record would start at
  an odd address. The header's low nibble is the record's state: `0xc` valid, `0xe` being
  written, `0x8` superseded, `0xf` erased (the end of the log). Bits 15..4 name the setting.
- **Which value holds.** The last valid record of a setting wins. The order on flash is the
  order of writing, so a profile's records need not sit next to each other. A setting never
  written keeps its factory default.
- **Settings** (valid-record headers, state `0xc`):

  | Setting | SSID | Security | Passphrase |
  | --- | --- | --- | --- |
  | Profile0 | `0x50ec` | `0x50cc` | `0x50bc` |
  | Profile1 | `0x51bc` | `0x519c` | `0x518c` |
  | Profile2 | `0x528c` | `0x526c` | `0x525c` |
  | Profile3 | `0x535c` | `0x533c` | `0x532c` |
  | Profile4 (stock's own setup AP) | `0x544c` | `0x540c` | `0x53fc` |

  Profile1's Cipher is `0x517c`. `cne/Networking/DrvCfg/WlanCfg/ActiveProfile` is `0x549c`
  (factory default `1`), DeviceName `0x4fbc`, CountryCode `0x54ec`. Profile1's factory
  defaults are Security `WPAPSK` and Cipher `AES`; other profiles' defaults were not traced.
- **Values.** Security is one of `NONE`, `WEP`, `WPAPSK`, `WPA2PSK`, `WPAWPA2PSK`. The
  passphrase is stored in plain text: 8 to 63 characters, or 64 hex digits.
- **Shell access.** The sdsShell's `rd <address> <words>` prints four little-endian words to a
  line (read on hardware). It also has `get [-e|-s|-b] <attr>` (for example
  `get -s cne/Networking/DrvCfg/WlanCfg/Profile1/SSID`) and `ls [-r -e -v -s] <dir>` (for
  example `ls -s -r cne/Networking/DrvCfg/WlanCfg`); their output format, and whether `get`
  prints the passphrase, are not verified on hardware, so SoundTap reads the log with `rd`.

SoundTap takes the active profile's network when its security is one of the three PSK values
and its passphrase is a WPA passphrase, or when it is `NONE` (an open network; a passphrase left
in the profile is not used). WEP networks, stock's setup profile, and a profile with no network
are left to the setup network.

## Evidence index (all read-only, from the extracted STU)

- `rootfs/opt/Bose/SMSC_App_Update`, `SMSC_BSL_Update`, `SMSCControlApp`,
  `SMSC_Update`: usage strings and `SMSCUpdateControl` symbols.
- `rootfs/opt/Bose/etc/SCM_Update.xml`: the `SMSC_Update` module, both `.bcd` images
  with pinned `Revision` and `VersionCheck="true"`.
- `rootfs/usr/bin/updatebcoapp.sh`, `updatebcobsl.sh`, `reset-airplay.sh`,
  `switch-tap-to-airplay-reset.sh`: the literal `ttyO5` / Ctrl-A / menu / XMODEM flow;
  `lsz` and `socat` invocations (both absent from the rootfs, which has only `microcom`).
- `rootfs/usr/local/etc/gpio_helper`: `DM870_power_on/off` on GPIO14.
- `rootfs/usr/bin/has-bco`: `module_type = scm`.
- `rootfs/etc/init.d/shelby_local` (`S97shelby_local`): the `/mnt/nv/rc.local` root hook.
- `rootfs/etc/init.d/shelby_usb`: USB gadget bring-up.
- `boseapp.txt`: `:17000` CLI verbs (`airplay cne rs mfg`, `reset cne settings`,
  `bco reset control`, `restore factory defaults`, `boot into bsl`,
  `graceful shutdown`), MFGDATA restore logs, `/dev/gpiodev/dm870pwr`,
  `/mnt/nv/bco_needs_factory_reset`, `DM8X0_NEEDS_TRIGGER_TO_ENTER_BSL`.
- [The contract](../../../CONTRIBUTING.md#the-contract): current permissions and recovery constraints.
