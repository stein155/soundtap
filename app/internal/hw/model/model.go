// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package model is the table of speaker hardware profiles: which device nodes, GPIOs,
// codec script and inputs each SoundTouch model has, so the daemon is not hard-wired to
// one board.
package model

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/install"
)

// Model is a speaker's hardware profile, so the runtime isn't hard-wired to the ST10.
// Picked from the kernel variant, else the MODEL env; with neither, no model is assumed
// (Current). Individual paths still honour their existing env overrides
// (AUDIO_DEV, LEDS, BUTTON_DEVICES). Adding a model = adding an
// entry to `models` — no changes to player/led/buttons/audio code.
type Model struct {
	Name string
	Type string // human product name reported by /api/info (e.g. "SoundTouch 20")
	// ListenAddr is the API address: :80 on every model (ADR 0001).
	ListenAddr    string
	AudioDev      string // raw S16_LE PCM sink
	ShelbySource  string // McASP "source" sysfs (set to "wifi" to enable the streaming clock)
	AICConfigured string // AIC codec "configured" flag sysfs
	AICConfigFile string // AIC codec "configuredFile" sysfs
	AICConfig     string // codec config name written to AICConfigFile
	MuteGPIO      string // amp mute line (1=unmuted)
	AmpPowerGPIO  string // amp enable line (1=powered; empty when not runtime-controlled)
	// AmpFaultGPIO is the sysfs GPIO number of the amplifier fault input, on the stock
	// kernel's flat numbering; the init script passes the mainline number as
	// AMP_FAULT_GPIO. The line is active low: stock's idle ST20 reads 1. 0 means this model
	// has no fault line backed by hardware evidence, and the monitor stays off: a guessed pin
	// either reads nothing useful or latches a permanent mute on an unrelated signal, and
	// silent audio is worse than no fault detection. Numbering is per-model — see MuteGPIO, 72 on ST10 vs 49 on ST20.
	AmpFaultGPIO int
	LEDDir       string // dir holding the LED byte files (uledy/uledw)
	OLED         string // the front panel over spidev, as oled.NewFramebuffer takes it; "" for none
	KeypadDev    string // one-byte ASCII press/release event device
	IRDev        string // Bose infrared receiver char device ("" = model has none)
	BTUart       string // Bluetooth HCI UART
	// Battery is a smart battery pack and charger (internal/hw/battery): the Portable only.
	Battery bool
	// STM32Amp is the SA-5's amplifier controller on I2C1 (internal/hw/stm32amp).
	STM32Amp bool
	// HostLink is the SoundTouch adapter's Bose link to its host product (internal/abl), and
	// HostLinkDev the serial device of the UART0 it runs on.
	HostLink    bool
	HostLinkDev string
	// UserSources are the inputs a person may select over the LAN API, as opposed to the
	// daemon-only /api/source. It lives on the model because it describes hardware: a box
	// with no analogue input must not offer AUX.
	//
	// This used to be `if hw.Name == ST10` in main.go. wla() and st20() both derive from
	// st10() and then rename themselves, so both silently lost the list, and setUserSource
	// rejects anything not on it -- on the Wireless Link Adapter, whose only analogue input
	// is the one a listening test uses, AUX could not be selected from the web UI at all.
	// Found by a source audit on 2026-09-17 and verified against both call sites.
	UserSources []string
	// Prebuffer is how much PCM the Mixer queues before it plays and after an underrun;
	// 0 keeps the Mixer's 2 s.
	Prebuffer time.Duration
	// ChimeVolume is the level the preset-stored tone plays at, independent of the
	// listening volume, as stock's chimeVolume in APConfig-<model>.xml.
	ChimeVolume int
	// NoBass is stock's hasControlCapabilities="false": no bass control at all.
	NoBass bool
	// DefaultVolume is what a speaker with no saved volume starts at, as stock's defaultValue
	// in BoseApp-<variant>.xml. 0 keeps speakerstate's own default of 30.
	DefaultVolume int
	// WakeVolumeMax is stock's volume thresholdHigh (BoseApp-<variant>.xml): the
	// loudest a wake comes back at. 0 leaves it unbounded.
	WakeVolumeMax int
	// WakeVolumeMin is stock's volume thresholdLow: the quietest a wake comes back at,
	// so a speaker left at 3 does not seem dead. 0 leaves it unbounded.
	WakeVolumeMin int
	// WifiAttenuation and AuxAttenuation are stock's fixed input gains per source path
	// (APConfig-<model>.xml): network sources and AUX. 0 means 1.
	WifiAttenuation, AuxAttenuation float64
	// AuxCapture is the ALSA capture node of the analogue input. Under the mainline
	// kernel AUX plays through it (aux.go); "" means the model has none.
	AuxCapture string
}

// Stock's numbers for the ST10, which the profiles derived from it inherit.
const (
	rhinoChimeVolume = 41 // APConfig-rhino.xml
	// thresholdHigh and thresholdLow in BoseApp-rhino.xml, and in BoseApp-spotty.xml for the ST20.
	rhinoDefaultVolume = 50
	rhinoWakeVolumeMax = 70
	rhinoWakeVolumeMin = 10
	// APConfig-rhino.xml, and APConfig-spotty.xml for the ST20: about -8 dB.
	rhinoWifiAttenuation = 0.39810699
)

// ledClassDir is the mainline gpio-leds tree.
const ledClassDir = "/sys/class/leds"

// st20Prebuffer is the ST20's Mixer prebuffer: our own BCO Wi-Fi has no 802.11 ACK/retry
// yet, so a lost frame waits for a TCP timeout, and 2 s of slack made radio rebuffer often
// (2026-10-19).
const st20Prebuffer = 6 * time.Second

// st10 (rhino sm2) — the confirmed profile; see docs/hardware/st10/hardware-contract.md.
func st10() Model {
	return Model{
		Name:          ST10,
		UserSources:   []string{sourceAux, sourceBluetooth},
		Type:          "SoundTouch 10",
		ListenAddr:    listenAddr,
		AudioDev:      "/dev/snd/snd_shelby", // devtmpfs node; DEVNAME snd/snd_shelby (maj 242), confirmed on-box
		ShelbySource:  "/sys/class/sound/snd_shelby/source",
		AICConfigured: "/sys/class/sound/snd_aic3256/configured",
		AICConfigFile: "/sys/class/sound/snd_aic3256/configuredFile",
		AICConfig:     "rhino_sm2_normal.cfg",
		MuteGPIO:      "/sys/class/gpio/gpio72/value",
		AmpPowerGPIO:  "",
		// No amp fault pin is documented for the ST10: docs/hardware/st10/hardware-contract.md
		// records mute 72 and amp power 75 only, and the analysis of the stock audio modules
		// found the fault input read by stock scmmond over sysfs without saying which GPIO
		// it is. Left off until a live probe names it.
		AmpFaultGPIO:    0,
		LEDDir:          "/dev/shelby-leds",
		OLED:            "",
		KeypadDev:       "/dev/shelby-keypad",
		IRDev:           "/dev/ir", // stock ir.ko, chardev name "ir"; rhino_sm2.dtb has bose,shelby-ir
		BTUart:          "/dev/ttyO2",
		Battery:         false,
		STM32Amp:        false,
		HostLink:        false,
		HostLinkDev:     "",
		Prebuffer:       0,
		ChimeVolume:     rhinoChimeVolume,
		NoBass:          false,
		AuxCapture:      "/dev/snd/pcmC0D0c",
		DefaultVolume:   rhinoDefaultVolume,
		WakeVolumeMax:   rhinoWakeVolumeMax,
		WakeVolumeMin:   rhinoWakeVolumeMin,
		WifiAttenuation: rhinoWifiAttenuation,
		AuxAttenuation:  1,
	}
}

// st20 (SoundTouch 20) — same rhino/shelby *main-CPU* audio platform as the ST10,
// plus the front-panel OLED (SPI on mainline, stock /dev/fb0 when spidev is absent).
//
// The BCO is the main CPU's network card: our own BCO app bridges Wi-Fi to the SCM over
// USB (ADR 0031), so the LAN reaches soundtapd on :80 as on the ST10 (ADR 0001).
// GPIO/keypad wiring comes from the stock spotty DTB and shelby-keypad driver.
func st20() Model {
	profile := st10()
	profile.Name = ST20
	profile.Type = "SoundTouch 20"
	profile.AICConfig = "spotty_scm_normal.cfg"
	profile.MuteGPIO = "/sys/class/gpio/gpio49/value"
	profile.AmpPowerGPIO = "/sys/class/gpio/gpio55/value"
	// From the live probe in os/tests/fixtures/st20-stock/hardware-facts.md:
	// "mute 49, amp standby 55, amp fault 56". The mute number in that same line
	// matches MuteGPIO above and docs/hardware/st20/hardware-contract.md (safe mute gpio1_17), which is what
	// makes 56 trustworthy enough to poll.
	// Mainline: gpio1_24, resolved by the init script into AMP_FAULT_GPIO.
	profile.AmpFaultGPIO = 56
	profile.BTUart = "" // ST20 controller transport is not proven; never inherit ST10 ttyO2.
	// One lamp: the Wi-Fi lamp above the OLED, white and amber PWM channels with the
	// ST10's labels (stock spotty_scm uledw/uledy). The ST10's other channels have
	// no brightness file here and are skipped; the rest of the panel is the display.
	profile.LEDDir = ledClassDir
	profile.OLED = "spidev:/dev/spidev1.0"
	profile.Prebuffer = st20Prebuffer
	profile.ChimeVolume = 40 // APConfig-spotty.xml
	profile.DefaultVolume = 30

	return profile
}

// wla is the SoundTouch Wireless Link Adapter. Stock firmware identifies it as
// binky/sm2. Its buttons come from the device tree as input events (buttons.go
// inputKeymap), the Bluetooth button as KEY_BLUETOOTH.
//
// LEDDir stays empty on purpose. This box has no shelby-leds char devices and none
// of the seven lamps led.go drives: it has two bi-colour lamps on gpio-leds, one of
// whose channels is a PWM, and internal/hw/led_wla.go drives those instead.
// The model names MODEL selects by.
const (
	ST10  = "st10"
	ST20  = "st20"
	WLA   = "wla"
	ST300 = "st300"
)

// The source names a profile offers, as they appear in the API.
const (
	sourceAux       = "AUX"
	sourceBluetooth = "BLUETOOTH"
	sourceTV        = "TV"   // the ST300 processor's TV input
	sourceHDMI      = "HDMI" // the ST300 processor's HDMI input
)

// listenAddr is the API address on every model (ADR 0001).
const listenAddr = ":80"

func wla() Model {
	profile := st10()
	profile.Name = WLA
	profile.NoBass = true // APConfig-binky.xml
	// BoseApp-binky.xml: thresholdLow 10, thresholdHigh 100 -- a line out has no ceiling.
	profile.WakeVolumeMax, profile.WakeVolumeMin = 100, 10
	profile.WifiAttenuation, profile.AuxAttenuation = 1, 1.22 // APConfig-binky.xml
	profile.Type = "SoundTouch Wireless Link Adapter"
	profile.AudioDev = "alsa:plughw:0,0"
	profile.ShelbySource = ""
	profile.AICConfigured = ""
	profile.AICConfigFile = ""
	profile.AICConfig = ""
	profile.MuteGPIO = ""
	profile.AmpPowerGPIO = ""
	profile.AmpFaultGPIO = 0
	profile.LEDDir = ""
	profile.KeypadDev = ""
	profile.IRDev = ""
	profile.BTUart = ""
	// Stock binky's chimeVolume has not been read off a rootfs yet; 40 is what every
	// model used before the per-model value.
	profile.ChimeVolume = 40

	return profile
}

// st30 is the SoundTouch 30 Series I (mojo/scm): the ST20's board in a bigger box. Stock's
// mojo_scm and spotty_scm device trees differ only in the codec script's name and the
// sync-out timer, so it is the ST20 everywhere but its name and its own codec script.
func st30() Model {
	profile := st20()
	profile.Type = "SoundTouch 30"
	profile.AICConfig = "mojo_scm_normal.cfg"
	profile.ChimeVolume = 28 // APConfig-mojo.xml
	profile.DefaultVolume = 30

	return profile
}

// seriesIII is the SoundTouch 20 or 30 Series III (spotty3 or mojo3 on SoundTap, spotty or
// mojo on a Shelby 2 module to stock): the ST10's module in the bigger boxes, so the ST10's
// amplifier, keypad, IR and Bluetooth. Stock spotty_sm2 lights one lamp, the Wi-Fi lamp,
// on the ST10's wifi-white and wifi-amber channels; the ST10's other lamp pads drive the
// ST20's OLED panel here, on the same SPI bus and chip select with its own GPIOs.
func seriesIII(variant string) Model {
	profile := st10()
	profile.DefaultVolume = 30
	profile.LEDDir = ledClassDir

	profile.OLED = "spidev:/dev/spidev1.0?ce=gpio0_12&reset=gpio2_9&dc=gpio2_12"
	if variant == "mojo3" {
		profile.Type, profile.AICConfig = "SoundTouch 30", "mojo_sm2_normal.cfg"
		profile.ChimeVolume = 28 // APConfig-mojo.xml
	} else {
		profile.Type, profile.AICConfig = "SoundTouch 20", "spotty_sm2_normal.cfg"
		profile.ChimeVolume = 40 // APConfig-spotty.xml
	}

	return profile
}

// sa5 is the SoundTouch SA-5 amplifier (burns/sm2): the ST10's module on an amplifier board
// with an STM32 controller. Stock's burns_sm2 tree is rhino_sm2's with the rear Ethernet port,
// the controller at 0x1d on I2C1 and two lamps (the Series III's), and its audio runs as the
// ST10's: McASP0 at 46875 Hz into the AIC3256, which feeds the amplifier board. The controller
// reports the amplifier's state, faults and a thermal gain the host applies (APConfig-burns
// AUDIO_DEVICE_TYPE_SHELBY_STM32, PowerAmpWithStatus). Chime 25 is stock's.
func sa5() Model {
	profile := st10()
	profile.Type = "SoundTouch SA-5"
	profile.AICConfig = "burns_sm2_normal.cfg"
	profile.LEDDir = ledClassDir
	profile.STM32Amp = true
	profile.ChimeVolume = 25 // APConfig-burns.xml

	return profile
}

// adapter is the SoundTouch adapter on the Shelby 2 module (lisa2 under SoundTap, lisa/sm2 to stock): the ST10's module
// without a codec, cabled to a host product (SA-4, Stereo JC, Wave, CineMate, Lifestyle or
// VideoWave). Audio leaves McASP0 as I2S at 46875 Hz to a converter on the adapter board, and
// the host plays it: SoundTap selects the adapter on the host, which applies the volume, over
// the Bose link on UART0 (APConfig-<host> hasAIC=false, hasABL=true, VOLUME_FORWARD_ABL).
// There is no amplifier, IR receiver or panel, and the mute is the host's (ABLMute): the
// device tree holds the converter enabled and mute_out where stock leaves it.
func adapter() Model {
	profile := st10()
	profile.Type = "SoundTouch adapter"
	profile.AICConfigured, profile.AICConfigFile, profile.AICConfig = "", "", ""
	profile.MuteGPIO = ""
	profile.AmpPowerGPIO = ""
	profile.AmpFaultGPIO = 0
	profile.IRDev = ""
	profile.LEDDir = ledClassDir
	profile.HostLink, profile.HostLinkDev = true, "/dev/ttyS0"

	return profile
}

// adapterSCM is the SoundTouch adapter on the Shelby Common Module (lisa/scm): the ST20's
// board with UART0 as the Bose link instead of the IR receiver. Unlike the SM2 adapter it keeps
// the codec (APConfig-<host> on scm: hasAIC=true, lisa_scm_normal.cfg); the host still applies
// the volume, so the codec runs at unity once the host is known. No amplifier, IR or panel.
func adapterSCM() Model {
	profile := st20()
	profile.Type = "SoundTouch adapter"
	profile.AICConfig = "lisa_scm_normal.cfg"
	profile.AmpPowerGPIO = ""
	profile.AmpFaultGPIO = 0
	profile.IRDev = ""
	profile.OLED = ""
	// ttyS0 is the BCO's UART on every SCM tree (the ST20's serial0 alias); the adapter's
	// tree names UART0 serial1.
	profile.HostLink, profile.HostLinkDev = true, "/dev/ttyS1"

	return profile
}

// portable is the SoundTouch Portable (taigan/scm): the ST20's board with a battery. Stock's
// taigan_scm and spotty_scm device trees differ in four things only: the battery gauge at
// 0x0b on I2C0, the RTC alarm's interrupt, the codec script and no sync-out. APConfig-taigan
// has no PowerAmp object, so the amplifiers are never put in standby (the device tree holds
// ampstby high) and a stereo pair is possible; volume 50 and chime 50 are stock's.
func portable() Model {
	profile := st20()
	profile.Type = "SoundTouch Portable"
	profile.AICConfig = "taigan_scm_normal.cfg"
	profile.AmpPowerGPIO = ""
	profile.Battery = true
	profile.ChimeVolume = 50 // APConfig-taigan.xml
	profile.DefaultVolume = 50

	return profile
}

// st300 is the SoundTouch 300 (ginger/sm2): the Wireless Link's module inside a
// soundbar. Its one audio path is S/PDIF into the soundbar's DSP; the
// buttons, lamps, inputs, amplifiers, ADAPTiQ and the bass module all belong to the
// soundbar processor behind UART0, which internal/lpm selects SoundTap's input on and hands
// the volume to. Its user sources, TV, HDMI and Bluetooth, are the processor's inputs.
func st300() Model {
	profile := wla()
	profile.Name = ST300
	profile.Type = "SoundTouch 300"
	profile.UserSources = []string{sourceTV, sourceHDMI, sourceBluetooth} // picked over the LPM
	profile.WakeVolumeMax = 70                                            // BoseApp-ginger.xml thresholdHigh
	profile.DefaultVolume = 30
	profile.WifiAttenuation, profile.AuxAttenuation = 1, 1 // APConfig-ginger.xml

	return profile
}

// lifestyle650 is the Lifestyle 600/650's SoundTouch module (bardeen/sm2): the ST300's
// module, tree and processor link. Stock's bardeen_sm2 and ginger_sm2 device trees hold the same
// properties, and stock runs the same A4VServer on the same UART0 at 921600 to the console
// (device type 8 there, 9 on the ST300), with the same source list, so it runs as the ST300
// under its own name. BoseApp-bardeen.xml keeps the ST300's thresholdHigh.
func lifestyle650() Model {
	profile := st300()
	profile.Type = "Lifestyle 650"

	return profile
}

// profiles builds each supported model's profile by its MODEL name.
func profiles() map[string]func() Model {
	return map[string]func() Model{
		ST10:  st10,
		ST20:  st20,
		WLA:   wla,
		ST300: st300,
	}
}

// errUnknownModel is a box no profile could be picked for.
var errUnknownModel = errors.New("hardware model unknown")

// Named is the profile of the supported model name, or Unknown for any other name.
func Named(name string) Model {
	if build, ok := profiles()[name]; ok {
		return build()
	}

	return Unknown()
}

// SourceGain is the Mixer's fixed input gain for source: stock applies auxAttenuation
// to AUX and wifiAttenuation to everything else, chosen at the source change.
//
//nolint:gocritic // hugeParam: a value receiver, because callers use it on the Model values Named and Current return
func (m Model) SourceGain(source string) float64 {
	gain := m.WifiAttenuation
	if source == sourceAux {
		gain = m.AuxAttenuation
	}

	if gain == 0 {
		return 1
	}

	return gain
}

// unknownChimeVolume is the preset-stored tone's level on a box with no profile, as every
// model had before the per-model value.
const unknownChimeVolume = 40

// Unknown is the profile of a box that could not be identified: no hardware at all.
func Unknown() Model {
	return Model{
		Name:            "unknown",
		Type:            "Unknown SoundTouch hardware",
		ListenAddr:      listenAddr,
		AudioDev:        "",
		ShelbySource:    "",
		AICConfigured:   "",
		AICConfigFile:   "",
		AICConfig:       "",
		MuteGPIO:        "",
		AmpPowerGPIO:    "",
		AmpFaultGPIO:    0,
		LEDDir:          "",
		OLED:            "",
		KeypadDev:       "",
		IRDev:           "",
		BTUart:          "",
		Battery:         false,
		STM32Amp:        false,
		HostLink:        false,
		HostLinkDev:     "",
		UserSources:     nil,
		Prebuffer:       0,
		ChimeVolume:     unknownChimeVolume,
		NoBass:          false,
		DefaultVolume:   0,
		WakeVolumeMax:   0,
		WakeVolumeMin:   0,
		WifiAttenuation: 0,
		AuxAttenuation:  0,
		AuxCapture:      "",
	}
}

// currentModel prefers immutable kernel hardware identity over environment settings.
// Without either it returns unknownModel and an error: a guessed profile drives the
// wrong GPIOs and codec, so an unidentified box gets no hardware at all.
func currentModel() (Model, error) {
	if variants := install.LiveVariants(); len(variants) > 0 {
		return forVariants(variants)
	}

	name := strings.ToLower(strings.TrimSpace(os.Getenv("MODEL")))
	if build, ok := profiles()[name]; ok {
		return build(), nil
	}

	if name == "" {
		return Unknown(), fmt.Errorf("%w: no kernel variant and MODEL is not set", errUnknownModel)
	}

	return Unknown(), fmt.Errorf("%w: MODEL=%q is not a supported model", errUnknownModel, name)
}

// ForVariant is the profile of a board whose kernel names variant, as Current finds it:
// for code that must cover every board, such as soundtap-root's GPIO list.
func ForVariant(variant string) (Model, error) { return forVariants([]string{variant}) }

// forVariants is the profile of a board whose immutable identity names these variants, or
// Unknown and why when they do not name one model.
func forVariants(variants []string) (Model, error) {
	name, err := install.ModelFromVariants(variants...)
	if err != nil {
		return Unknown(), err
	}

	if slices.Contains(variants, "mojo") {
		return st30(), nil
	}

	if slices.Contains(variants, "lisa2") {
		return adapter(), nil
	}

	if slices.Contains(variants, "lisa") {
		return adapterSCM(), nil
	}

	if slices.Contains(variants, "burns") {
		return sa5(), nil
	}

	if slices.Contains(variants, "taigan") {
		return portable(), nil
	}

	if slices.Contains(variants, "bardeen") {
		return lifestyle650(), nil
	}

	for _, v := range []string{"spotty3", "mojo3"} {
		if slices.Contains(variants, v) {
			return seriesIII(v), nil
		}
	}

	return Named(name), nil
}

// Current is this box's profile, without an AUX input nothing routes. The error says why
// the model could not be identified, and then the profile is Unknown.
func Current() (Model, error) {
	profile, err := currentModel()

	return withoutUnroutedAux(&profile), err
}

// withoutUnroutedAux drops AUX when nothing routes the analogue input to the amplifier:
// neither stock snd-shelby2's source node nor a capture node for aux.go is there.
// Selectable but silent reads as a broken speaker.
func withoutUnroutedAux(profile *Model) Model {
	routed := *profile
	if !exists(routed.ShelbySource) && !exists(routed.AuxCapture) {
		isAux := func(source string) bool { return source == sourceAux }
		routed.UserSources = slices.DeleteFunc(slices.Clone(routed.UserSources), isAux)
	}

	return routed
}

func exists(path string) bool {
	if path == "" {
		return false
	}

	_, err := os.Stat(path)

	return err == nil
}

// Capabilities is what this hardware can do, as the multi-device API reports it.
//
// It is derived from the profile rather than from the model name on purpose. A union of
// "st10" | "st20" is wrong the day a new board appears, and there is a Wireless Link in
// this tree already with no keypad, no infrared and no display. The profile knows the
// answers; the UI should not have to guess them from a product string.
//
// display is deliberately absent here: whether a panel is present is a runtime question
// (the ST20 has one only if the SPI device is there), so it comes from the settings API,
// which already answers it as hasDisplay.
//
//nolint:gocritic // hugeParam: a value receiver, because callers use it on the Model values Named and Current return
func (m Model) Capabilities() []string {
	caps := []string{}
	if slices.Contains(m.UserSources, sourceBluetooth) {
		// Whether an adapter is really there is decided at runtime by the web server:
		// ST20s ship with and without Bluetooth.
		caps = append(caps, "bluetooth")
	}

	if m.IRDev != "" {
		caps = append(caps, "ir")
	}

	if m.KeypadDev != "" {
		caps = append(caps, "keypad")
	}

	if slices.Contains(m.UserSources, sourceAux) {
		caps = append(caps, "aux")
	}

	if m.OLED != "" {
		caps = append(caps, "display")
	}

	if m.Battery {
		caps = append(caps, "battery")
	}

	return caps
}
