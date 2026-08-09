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
	// AMP_FAULT_GPIO. The line is active low: stock's idle ST20 reads 1. 0 means this model has no fault line backed by hardware evidence, and
	// the monitor stays off: a guessed pin either reads nothing useful or latches a
	// permanent mute on an unrelated signal, and silent audio is worse than no fault
	// detection. Numbering is per-model — see MuteGPIO, 72 on ST10 vs 49 on ST20.
	AmpFaultGPIO int
	LEDDir       string // dir holding the LED byte files (uledy/uledw)
	OLED         string // the front panel over spidev, as oled.NewFramebuffer takes it; "" for none
	KeypadDev    string // one-byte ASCII press/release event device
	IRDev        string // Bose infrared receiver char device ("" = model has none)
	BTUart       string // Bluetooth HCI UART
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
		// No amp fault pin is documented for the ST10: docs/hardware/st10/hardware-contract.md
		// records mute 72 and amp power 75 only, and the analysis of the stock audio modules
		// found the fault input read by stock scmmond over sysfs without saying which GPIO
		// it is. Left off until a live probe names it.
		AmpFaultGPIO: 0,
		LEDDir:       "/dev/shelby-leds",
		KeypadDev:    "/dev/shelby-keypad",
		IRDev:        "/dev/ir", // stock ir.ko, chardev name "ir"; rhino_sm2.dtb has bose,shelby-ir
		BTUart:       "/dev/ttyO2",
		ChimeVolume:  41, // APConfig-rhino.xml
		AuxCapture:   "/dev/snd/pcmC0D0c",
		// thresholdHigh and thresholdLow in BoseApp-rhino.xml, and in BoseApp-spotty.xml for the ST20.
		DefaultVolume: 50,
		WakeVolumeMax: 70,
		WakeVolumeMin: 10,
		// APConfig-rhino.xml, and APConfig-spotty.xml for the ST20: about -8 dB.
		WifiAttenuation: 0.39810699,
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
	m := st10()
	m.Name = ST20
	m.Type = "SoundTouch 20"
	m.AICConfig = "spotty_scm_normal.cfg"
	m.MuteGPIO = "/sys/class/gpio/gpio49/value"
	m.AmpPowerGPIO = "/sys/class/gpio/gpio55/value"
	// From the live probe in os/tests/fixtures/st20-stock/hardware-facts.md:
	// "mute 49, amp standby 55, amp fault 56". The mute number in that same line
	// matches MuteGPIO above and docs/hardware/st20/hardware-contract.md (safe mute gpio1_17), which is what
	// makes 56 trustworthy enough to poll.
	// Mainline: gpio1_24, resolved by the init script into AMP_FAULT_GPIO.
	m.AmpFaultGPIO = 56
	m.BTUart = "" // ST20 controller transport is not proven; never inherit ST10 ttyO2.
	// One lamp: the Wi-Fi lamp above the OLED, white and amber PWM channels with the
	// ST10's labels (stock spotty_scm uledw/uledy). The ST10's other channels have
	// no brightness file here and are skipped; the rest of the panel is the display.
	m.LEDDir = "/sys/class/leds"
	m.OLED = "spidev:/dev/spidev1.0"
	// Our own BCO Wi-Fi has no 802.11 ACK/retry yet: a lost frame waits for a TCP
	// timeout, and 2 s of slack made radio rebuffer often (2026-10-19).
	m.Prebuffer = 6 * time.Second
	m.ChimeVolume = 40 // APConfig-spotty.xml
	m.DefaultVolume = 30
	return m
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
	m := st10()
	m.Name = WLA
	m.NoBass = true // APConfig-binky.xml
	// BoseApp-binky.xml: thresholdLow 10, thresholdHigh 100 -- a line out has no ceiling.
	m.WakeVolumeMax, m.WakeVolumeMin = 100, 10
	m.WifiAttenuation, m.AuxAttenuation = 1, 1.22 // APConfig-binky.xml
	m.Type = "SoundTouch Wireless Link Adapter"
	m.AudioDev = "alsa:plughw:0,0"
	m.ShelbySource = ""
	m.AICConfigured = ""
	m.AICConfigFile = ""
	m.AICConfig = ""
	m.MuteGPIO = ""
	m.AmpPowerGPIO = ""
	m.AmpFaultGPIO = 0
	m.LEDDir = ""
	m.KeypadDev = ""
	m.IRDev = ""
	m.BTUart = ""
	// Stock binky's chimeVolume has not been read off a rootfs yet; 40 is what every
	// model used before the per-model value.
	m.ChimeVolume = 40
	return m
}

// st30 is the SoundTouch 30 Series I (mojo/scm): the ST20's board in a bigger box. Stock's
// mojo_scm and spotty_scm device trees differ only in the codec script's name and the
// sync-out timer, so it is the ST20 everywhere but its name and its own codec script.
func st30() Model {
	m := st20()
	m.Type = "SoundTouch 30"
	m.AICConfig = "mojo_scm_normal.cfg"
	m.ChimeVolume = 28 // APConfig-mojo.xml
	m.DefaultVolume = 30
	return m
}

// seriesIII is the SoundTouch 20 or 30 Series III (spotty3 or mojo3 on SoundTap, spotty or
// mojo on a Shelby 2 module to stock): the ST10's module in the bigger boxes, so the ST10's
// amplifier, keypad, IR and Bluetooth. Stock spotty_sm2 lights one lamp, the Wi-Fi lamp,
// on the ST10's wifi-white and wifi-amber channels; the ST10's other lamp pads drive the
// ST20's OLED panel here, on the same SPI bus and chip select with its own GPIOs.
func seriesIII(variant string) Model {
	m := st10()
	m.DefaultVolume = 30
	m.LEDDir = "/sys/class/leds"
	m.OLED = "spidev:/dev/spidev1.0?ce=gpio0_12&reset=gpio2_9&dc=gpio2_12"
	if variant == "mojo3" {
		m.Type, m.AICConfig, m.ChimeVolume = "SoundTouch 30", "mojo_sm2_normal.cfg", 28 // APConfig-mojo.xml
	} else {
		m.Type, m.AICConfig, m.ChimeVolume = "SoundTouch 20", "spotty_sm2_normal.cfg", 40 // APConfig-spotty.xml
	}
	return m
}

// st300 is the SoundTouch 300 (ginger/sm2): the Wireless Link's module inside a
// soundbar. Its one audio path is S/PDIF into the soundbar's DSP; the
// buttons, lamps, inputs, amplifiers, ADAPTiQ and the bass module all belong to the
// soundbar processor behind UART0, which internal/lpm selects SoundTap's input on and hands
// the volume to. Its user sources, TV, HDMI and Bluetooth, are the processor's inputs.
func st300() Model {
	m := wla()
	m.Name = ST300
	m.Type = "SoundTouch 300"
	m.UserSources = []string{sourceTV, sourceHDMI, sourceBluetooth} // picked over the LPM
	m.WakeVolumeMax = 70                                            // BoseApp-ginger.xml thresholdHigh
	m.DefaultVolume = 30
	m.WifiAttenuation, m.AuxAttenuation = 1, 1 // APConfig-ginger.xml
	return m
}

// profiles builds each supported model's profile by its MODEL name.
var profiles = map[string]func() Model{
	ST10:  st10,
	ST20:  st20,
	WLA:   wla,
	ST300: st300,
}

// Named is the profile of the supported model name, or Unknown for any other name.
func Named(name string) Model {
	if p, ok := profiles[name]; ok {
		return p()
	}
	return Unknown()
}

// SourceGain is the Mixer's fixed input gain for source: stock applies auxAttenuation
// to AUX and wifiAttenuation to everything else, chosen at the source change.
func (m Model) SourceGain(source string) float64 {
	g := m.WifiAttenuation
	if source == sourceAux {
		g = m.AuxAttenuation
	}
	if g == 0 {
		return 1
	}
	return g
}

// Unknown is the profile of a box that could not be identified: no hardware at all.
func Unknown() Model {
	return Model{Name: "unknown", Type: "Unknown SoundTouch hardware", ListenAddr: listenAddr, ChimeVolume: 40}
}

// currentModel prefers immutable kernel hardware identity over environment settings.
// Without either it returns unknownModel and an error: a guessed profile drives the
// wrong GPIOs and codec, so an unidentified box gets no hardware at all.
func currentModel() (Model, error) {
	if variants := install.LiveVariants(); len(variants) > 0 {
		return forVariants(variants)
	}
	name := strings.ToLower(strings.TrimSpace(os.Getenv("MODEL")))
	if p, ok := profiles[name]; ok {
		return p(), nil
	}
	if name == "" {
		return Unknown(), errors.New("hardware model unknown: no kernel variant and MODEL is not set")
	}
	return Unknown(), fmt.Errorf("hardware model unknown: MODEL=%q is not a supported model", name)
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
	m, err := currentModel()
	return withoutUnroutedAux(m), err
}

// withoutUnroutedAux drops AUX when nothing routes the analogue input to the amplifier:
// neither stock snd-shelby2's source node nor a capture node for aux.go is there.
// Selectable but silent reads as a broken speaker.
func withoutUnroutedAux(m Model) Model {
	if !exists(m.ShelbySource) && !exists(m.AuxCapture) {
		m.UserSources = slices.DeleteFunc(slices.Clone(m.UserSources), func(s string) bool { return s == sourceAux })
	}
	return m
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
	for _, source := range m.UserSources {
		if source == sourceAux {
			caps = append(caps, "aux")
			break
		}
	}
	return caps
}
