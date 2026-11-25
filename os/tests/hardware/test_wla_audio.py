# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The WLA's audio: the one McASP machine driver, its device tree, clocks, rates and the init script."""

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


class WlaAudioTests(unittest.TestCase):
    """The WLA audio path in the kernel, device tree, init script and ALSA configuration."""

    def test_kernel_builds_the_one_machine_driver_source(self) -> None:
        """os/drivers/soundtap-st10-audio.c is the only copy of the McASP machine driver.

        A second copy inside a linux patch once drifted: it matched only bose,soundtap-mcasp-audio
        while the WLA device tree declares bose,soundtap-mcasp-spdif, so no sound card bound and
        the board had no audio at all (measured 2026-09-15). Now the patch only builds the file
        and a post-patch hook copies the source in; a driver edit must rebuild the kernel.
        """
        patch = (ROOT / "os/board/rootfs/linux-patches"
                 "/0002-asoc-add-soundtap-mcasp-machine-driver.patch").read_text()
        self.assertIn("+obj-y += soundtap-st10-audio.o", patch)
        self.assertNotIn("+++ b/sound/soc/ti/soundtap-st10-audio.c", patch, "second copy is back")
        ext = (ROOT / "os/external.mk").read_text()
        self.assertIn("drivers/soundtap-st10-audio.c \\\n"
                      "\t\t$(LINUX_DIR)/sound/soc/ti/soundtap-st10-audio.c", ext)
        self.assertIn("LINUX_POST_PATCH_HOOKS += SOUNDTAP_LINUX_MCASP_DRIVER", ext)
        prepare = (ROOT / "os/board/rootfs/prepare-linux.sh").read_text()
        self.assertIn("os/drivers/soundtap-st10-audio.c", prepare, "a driver edit must dirclean linux")
        source = (ROOT / "os/drivers/soundtap-st10-audio.c").read_text()
        self.assertIn('"bose,soundtap-mcasp-spdif"', source)

    def test_binky_uses_builtin_mcasp1_spdif_without_codec_controls(self) -> None:
        """The WLA plays through McASP1 S/PDIF with no codec or amplifier controls."""
        dts = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-wla.dts").read_text()
        machine = (ROOT / "os/drivers/soundtap-st10-audio.c").read_text()
        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        alsa = (ROOT / "os/overlay/etc/asound.conf.wla").read_text()

        for text in (
            'compatible = "linux,spdif-dit";',
            'compatible = "bose,soundtap-mcasp-spdif";',
            "audio-cpu = <&mcasp1>;",
            "audio-codec = <&audio_codec>;",
            "AM33XX_PADCONF(AM335X_PIN_MII1_COL, PIN_OUTPUT_PULLDOWN, MUX_MODE4)",
            "AM33XX_PADCONF(AM335X_PIN_RMII1_REF_CLK, PIN_INPUT_PULLDOWN, MUX_MODE6)",
            "&mcasp1 {",
            "op-mode = <1>;",
            "ti,tx-clock-only;",
        ):
            self.assertIn(text, dts)
        # No ST10 codec or amplifier node: the binky drives a dumb 3.5 mm pair.
        # The check reads the compatible strings, not the whole file -- the
        # pinmux comments name Bose's own mute_out and ampstby lines, and
        # grepping the text for "mute" or "amp" failed on a comment.
        # Only the ST300's S/PDIF capture is pinned to 44100. The AUX in on mcasp0
        # shares 46875 with playback; pinning it too left no rate and AUX silent.
        self.assertIn("SNDRV_PCM_STREAM_CAPTURE && soundtap_spdif", machine)
        compatibles = " ".join(re.findall(r"compatible\s*=\s*([^;]*);", dts)).lower()
        for codec in ("aic3256", "tlv320", "amplifier"):
            self.assertNotIn(codec, compatibles)
        start = init.index('if [ "$MAINLINE" = 1 ] && [ "$NOCODEC" = 1 ]; then')
        end = init.index('elif [ "$MAINLINE" = 1 ]; then', start)
        wla = init[start:end]
        ready = wla[wla.index("if [ -c /dev/snd/pcmC0D0p ]; then"):wla.index("\telse", wla.index("if [ -c /dev/snd/pcmC0D0p ]; then"))]
        self.assertIn("AUDIO_MIX=1", wla)
        self.assertIn("[ -c /dev/snd/pcmC0D0p ]", wla)
        self.assertIn("WLA_AUDIO_READY=1", wla)
        self.assertNotIn("-no-audio", ready)
        # The local snapclient never opens the card; soundtapd builds its command line
        # (TestSnapclientArgsAnchorOrFollower) and stops it as its own child, with no
        # BusyBox pkill or killall.
        self.assertNotIn("snapclient --player alsa", init)
        soundtapd = (ROOT / "app/cmd/soundtapd/main.go").read_text()
        self.assertNotIn('"pkill"', soundtapd)
        self.assertNotIn('"killall"', soundtapd)
        mmc = dts[dts.index("&mmc1 {"):dts.index("&mcasp1 {")]
        mcasp = dts[dts.index("&mcasp1 {"):]
        self.assertIn("/delete-property/ dmas;", mmc)
        self.assertIn("sdhci-caps-mask = <0 0x00580000>;", mmc)
        self.assertNotIn("/delete-property/ dmas;", mcasp)
        self.assertLess(init.index('if [ "$AUDIO_MIX" = "1" ]'), init.index('elif [ "$NOCODEC" = 1 ]'))
        self.assertIn('slave.pcm "hw:0,0"', alsa)
        self.assertIn("soundtap_spdif ? SNDRV_PCM_FMTBIT_S16_LE", machine)
        # S/PDIF takes the board's 11.2896 MHz oscillator in; the analog link
        # drives AUXCLK out at 24 MHz and divides it to a 46875 Hz frame.
        self.assertIn("MCASP_CLK_HCLK_AHCLK,\n\t\t\t\t\t     11289600, SND_SOC_CLOCK_IN", machine)
        self.assertIn("MCASP_CLK_HCLK_AUXCLK,\n\t\t\t\t     24000000, SND_SOC_CLOCK_OUT", machine)
        # The clock is set in startup, not only in hw_params: davinci-mcasp builds its
        # rate rule from sysclk while the parameters are refined, which happens first.
        # With it still zero the rule accepted nothing and the card could not be opened
        # at all -- measured on the board 2026-09-18.
        self.assertIn("soundtap_startup", machine)
        startup = machine[machine.index("static int soundtap_startup"):machine.index("static int soundtap_hw_params")]
        self.assertIn("soundtap_set_clocks(cpu_dai, soundtap_spdif, soundtap_auxclk)", startup)
        self.assertIn("MCASP_CLKDIV_AUXCLK, 2", machine)
        self.assertIn("MCASP_CLKDIV_BCLK, spdif ? 2 : 4", machine)
        # One rate per link, from one function: the oscillator S/PDIF is 44100, the
        # analog link and the ST300's AUXCLK S/PDIF are 46875 and nothing else.
        self.assertIn("return spdif && !auxclk ? 44100 : 46875;", machine)
        self.assertIn("params_rate(params) != soundtap_rate(soundtap_spdif, soundtap_auxclk)", machine)
        # Neither 46875 link can use spdif-dit: its rate mask has no 46875, so
        # the parameter space comes out empty and every open fails.
        self.assertIn("priv->spdif && !priv->auxclk ? &priv->codec : &snd_soc_dummy_dlc", machine)


if __name__ == "__main__":
    unittest.main()
