# 0031. The BCO is the ST20's network card and runs our own app

Status: accepted — 2026-10-21

## Context

On the ST20 the BCO (DM870) owns the Wi-Fi radio and the Ethernet MAC; the SCM, the main CPU that
runs SoundTap, has no network of its own. Stock BCO firmware NATs the LAN to the SCM and lets only
selected inbound ports through ([the ST20's hardware contract](../hardware/st20/hardware-contract.md#network-as-stock-left-it)).
The project needs every port to reach the SCM (SoundTap on :80, the stock install ports, Spotify,
mDNS, multiroom and whatever comes later), Wi-Fi configured from the SoundTap web interface, and
Wi-Fi on the SCM exactly as on the ST10: plain mainline Linux with `wlan0` and Ethernet as its own
interface, so SoundTap's network code does not branch per model. Stock's BCO app and its first
stage are Bose code and cannot ship ([ADR 0049](0049-no-stock-bytes.md)).

## Decision

The BCO runs our own app as the SCM's network card. The SCM sees a real `wlan0` and `eth0`, as on
the ST10. The protocol, register and measurement detail is in
[the BCO network card in detail](../hardware/st20/bco-network-card.md).

### One USB function, one SCM driver

The BCO presents one vendor-specific USB function with a bulk IN and a bulk OUT endpoint. The SCM
driver `soundtap_bco`, a usbnet minidriver like `rndis_wlan`, owns it and creates the interfaces
itself, the way Linux attaches other Wi-Fi co-processors. Every message carries a two-byte header
that names its channel: 0 control, 1 Wi-Fi, 2 the wired port; data messages are whole Ethernet
frames.

- `wlan0` is a cfg80211 FullMAC station with the device's own Wi-Fi MAC. `wpa_supplicant` hands
  the driver a PMK; the PSK never leaves the SCM. The BCO runs the 4-way and group handshakes.
- `eth0` is the wired port. Neither interface is a bridge of the other, and the SCM must not
  bridge them: that loops through the BCO.
- SoundTap is model-agnostic: the ST10's Wi-Fi code drives `wlan0`, and `eth0` gets DHCP when it
  has carrier. Wi-Fi and wired can be up together; route choice is ordinary Linux metrics.

### The BCO demultiplexes and filters nothing

Channel 1 goes to the Wi-Fi station, channel 2 to the wired MAC, channel 0 to the management
server. There is no port filter, no NAT and no IP service on the data path, so every port and
every multicast protocol passes.

### The setup access point runs on the BCO, driven by hostapd on wlan1

The BCO can run an open access point whose BSSID is the device's MAC, on one 2.4 GHz channel, for
up to six clients; there is no WPA2 access point. On the SCM it is standard cfg80211:
`soundtap_bco` advertises AP mode with the authentication and association done in the device, so
`iw … interface add wlan1 type __ap` and `hostapd` work as on the ST10, with the ST10's Wi-Fi
script and `hostapd.conf` unchanged. The core beacons and answers probes from hostapd's beacon;
our app answers authentication and association.

### The link carries a protocol level, and a resent request is answered, not re-run

HELLO reports a protocol level and capability bits. The driver refuses a BCO app of a different
level, with a log line saying to flash a matching one, and accepts one that reports no level with
a warning, because refusing it would take the ST20 off the LAN through which a new app arrives.
The app answers a resent state-changing request (same type and seq) with the reply it kept,
instead of running it again, and keeps only the reply, never the request, because SET_PROFILE
carries the PMK. The level goes up in the commit that changes a message's layout or meaning, and
a contract test holds the app and the driver to the same framing values.

### Wi-Fi runs in our own WiLD core, and the keys stay on the BCO

The WLAN core firmware is ours (`bco/wild/fw`), linked into the app. CCMP runs on the core's
cipher engine, as stock does it: the core enciphers every protected frame it sends and deciphers
every protected frame it receives, drops a frame it cannot encipher rather than sending it in
clear, and checks the PN. The temporal keys never leave the BCO; the SCM driver never sees a key.
The core contends for the medium with an 802.11 DCF backoff on the queue it transmits on and
adapts the data rate between 54 and 24 Mb/s. Stock's transmit code stays unread.

### The image ships with SoundTap Core, and soundtapd installs it

The ST20 part of SoundTap Core carries one BCO image, built by `bco/tools/bco-pack` from our code
alone: our first stage, which the stock loader BFL starts and which does the PLL and SDRAM init,
and our app with our WiLD core. The register values the first stage writes are hardware facts read
from stock; the code is ours. The rootfs carries it as `/usr/share/soundtap/bco-app.bcd`. BFL and
BSL stay stock ([the contract](../../CONTRIBUTING.md#the-contract) forbids writing them).

On an ST20, 30 s after boot, soundtapd flashes the image when its sha256 differs from the marker
`/mnt/nv/soundtap/bco-image`, which it writes with the attempt count before flashing and replaces
with ` ok` on success. Each attempt holds the operations lock and is cut off after 15 minutes.
A failed attempt is not retried while the BCO still runs our app, because the LAN runs through
it. While it runs no app of ours (a flash cut off after the erase, or stock firmware), the
speaker has no network at all, so the install is retried without a limit, 5 minutes after a
failure and doubling up to an hour, on this boot and later ones. A marker that says ` ok` while
the BCO no longer runs our app, or a deleted marker, asks for another install. The first install
over stock BCO firmware first adopts the Wi-Fi network stock had joined, and reboots afterwards
to start the new network path. The boot confirmation waits for this step and for `bcolink0`
([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)). The image is trusted because it came with
the signed SoundTap Core, not because it matches a stock size.

`/api/firmware` does not flash BCO payloads. Going back to stock means restoring the matching
stock BCO app separately with the `bcoflash` development tool before booting the stock SCM image;
because that removes SoundTap's LAN path, the restore plan first establishes independent SCM
access ([install records](../hardware/install-records.md#going-back-to-bose),
[BCO recovery](../hardware/st20/bco-recovery.md)).

### The BCO is power-cycled only on evidence that the radio is dead

The BCO app can wedge with its USB link alive and nothing on the air. soundtapd's LAN watch then
switches the BCO's supply off and on, which takes the ST20 off the network for about a minute and
stops playback, so a cycle needs evidence about the BCO itself:

- A check, every 20 s, passes when the gateway answers a ping or when the BCO link's netdev
  received frames since the previous check. A working radio on a live Wi-Fi always receives
  something (ARP, mDNS, broadcasts, the answer to the failed ping's own ARP request); the wedge
  receives nothing.
- Three failed checks in a row lead to a cycle. The threshold doubles after each cycle that did
  not help, up to 32.
- There is no cycle while a BCO flash holds the watch (`/run/soundtap/bco-flash`), while there is
  no gateway (setup AP, no lease yet), or with stock BCO firmware.
- Before each cycle the watch logs the BCO's UART and the netdev counters.

The same watch power-cycles the BCO when its link misses the boot, a bounded number of times. No
other watchdog reboots the SCM or restarts its networking because the gateway does not answer
([the contract](../../CONTRIBUTING.md#the-contract)).

Alternatives that lost:

- **One CDC-ECM link with the BCO as a transparent bridge and a control-only `wlan0`.** SoundTap
  would have to know that this model's Wi-Fi traffic lives on `eth0`, wired and Wi-Fi could not be
  told apart, and CDC-ECM treated one 512-byte USB packet as one frame.
- **CDC-ECM with tagged Wi-Fi frames, or two CDC-ECM functions.** Either rides on another driver's
  netdev and still needs a cfg80211 driver around it; not how any mainline driver attaches a
  co-processor.
- **An IP service or NAT on the BCO.** Needs an IP stack, breaks mDNS and multicast, and needs a
  port list, while every port must pass.
- **The handshake or CCMP on the SCM.** Ships the keys to the SCM and needs key installs, EAPOL
  forwarding or a third link channel over the link for the same result.
- **CCMP in software on the core or the app.** Too slow for the link
  ([measurements](../hardware/st20/bco-network-card.md#throughput-with-the-cipher-engine)).
- **Management over the BCO UART.** About 42 ms per character, and the UART is the debug console.
- **Our stage 2 inside the speaker's own stock container, packed on the speaker.** The release
  would stay Bose-free, but the BCO would still run Bose's first stage.
- **Keeping the BCO app out of SoundTap Core.** The driver and the app drift apart, and a
  converted speaker never gets the network card.
- **Judging the radio by a ping alone.** It tests the gateway, not the radio: a router that drops
  ICMP or has lost its uplink makes a working BCO look wedged.

## Consequences

- Normal setup uses the common `wlan0`/`eth0` userspace path; the ST20-specific parts are the
  kernel driver, the image install and the link recovery in `internal/bco`.
- SoundTap does not support stock BCO firmware: without our BCO app the ST20 has no network.
- We must not add a port filter or IP service on the BCO's data path.
- A newer or older protocol level stops the driver until the matching app is flashed.
- The release stays free of Bose bytes, and so does the BCO above BFL/BSL.
- The BCO needs a USB vendor/product ID of its own. Development uses the pid.codes test ID
  `1209:0001`; allocating a real one is the maintainer's call.
- Clients of the setup access point that doze may lose unicast frames (no power-save buffering),
  and a scan while it runs returns the scan made before it.
- An access point that is switched off looks like a dead radio; the doubling threshold keeps
  those cycles rare.
- Hardware proof is per candidate, on the ST20 itself: [the measurements](../hardware/st20/bco-network-card.md#measurements-on-the-bench-st20)
  are historical, and release evidence belongs in the [hardware checklist](../hardware/hardware-test.md).

## Code

- `os/board/rootfs/linux-patches/0007-net-usb-add-soundtap-bco-link-driver.patch`: the SCM driver
  `soundtap_bco` (`bcolink0`, `wlan0`, `wlan1`, `eth0`).
- `bco/app/src/bcolink.h`: the link framing, channels, protocol level and capability bits;
  `bco/app/src/netcard.c` demultiplexes, `bco/app/src/usblink.c` is the USB device side.
- `bco/app/src/mgmt.c`: the control messages and the kept reply for resent requests.
- `bco/app/src/wifi.c` (station and setup access point), `bco/app/src/wpa.c` (handshakes),
  `bco/app/src/wired.c` (the wired port).
- `bco/wild/fw`: our WiLD core; `bco/wild/fw/src/xport.c` (cipher engine, backoff, rate
  adaptation) and `bco/wild/fw/src/sta.c` (keys per peer slot).
- `bco/app/src/first.c`, `bco/tools/bco-pack` and `os/package/soundtap-bco-app`: the first stage,
  the image and its build.
- `app/internal/bco/autoinstall.go`: `bco.AutoInstall`, `bco.RetryInstall`, `bco.RetryDelay`;
  `app/cmd/soundtapd/bco_install.go`: `wireBCOFlash`, `keepBCOInstalled`; `bco.Flash` flashes.
- `app/internal/bco/lanwatch.go`: `bco.LANWatch`, `bco.NewLANWatch`, `bco.FlashHold` and
  `radioCheck`.
- `os/tests/hardware/test_bcolink_contract.py`: holds the app and the driver to one protocol.
