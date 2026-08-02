// SPDX-License-Identifier: GPL-2.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors
#include <linux/module.h>
#include <linux/of.h>
#include <linux/platform_device.h>
#include <sound/pcm_params.h>
#include <sound/soc.h>

#include "davinci-mcasp.h"

struct soundtap_audio {
	struct snd_soc_card card;
	struct snd_soc_dai_link link;
	struct snd_soc_dai_link_component cpu, codec, platform;
	bool spdif;
	bool auxclk;
};

static bool substream_is_spdif(struct snd_pcm_substream *substream)
{
	struct snd_soc_pcm_runtime *rtd = snd_soc_substream_to_rtd(substream);
	struct soundtap_audio *priv = snd_soc_card_get_drvdata(rtd->card);

	return priv->spdif;
}

/* S/PDIF clocked from AUXCLK rather than a board oscillator: the SoundTouch 300's link
 * into its soundbar DSP. */
static bool substream_is_auxclk(struct snd_pcm_substream *substream)
{
	struct snd_soc_pcm_runtime *rtd = snd_soc_substream_to_rtd(substream);
	struct soundtap_audio *priv = snd_soc_card_get_drvdata(rtd->card);

	return priv->auxclk;
}

/* The one rate each link can make. The ST300's S/PDIF runs where stock snd-bardeen.ko
 * runs it: AUXCLK 24 MHz / 2 / 2 = 6 MHz of biphase clock, 128 x 46875 Hz. */
static unsigned int soundtap_rate(bool spdif, bool auxclk)
{
	return spdif && !auxclk ? 44100 : 46875;
}

/*
 * The clock setup, shared by startup and hw_params.
 *
 * S/PDIF: the high-frequency clock for that link is an oscillator on the board, not
 * anything the SoC can make. Asking AUXCLK for 24.576 MHz was wrong twice over: the
 * AM335x gives 24 MHz whatever is requested, and SND_SOC_CLOCK_OUT drove AHCLKX out of
 * a pin the oscillator is already driving into. Measured 2026-09-16: the card consumed
 * frames at 23437.5 Hz while telling ALSA 48000 -- below the S/PDIF floor of 32 kHz, so
 * no receiver could ever lock, which is why that output has never made a sound.
 * 11.2896 MHz is 256 x 44100, halved to 128 x 44100 of bit clock, and Bose's own
 * APConfig declares exactly that for its SPDIF driver.
 *
 * Analog: 46875, not 48000. AUXCLK is 24 MHz, halved to AHCLKX and quartered
 * to a 3 MHz bit clock. Two 32-bit slots make 3 MHz / 64 = 46875 Hz.
 *
 * Measured on the ST10 on 2026-09-22, against a build that claimed 48000: the card
 * consumed 4,220,771 frames in 90.04 s, which is 46876.4 Hz. Stock arrives at the same
 * number -- snd-shelby.ko writes AHCLKXCTL 0x8001 and ACLKXCTL 0xA3, so AUXCLK / 2 / 4,
 * and its mcasp0_fck is sys_clkin_ck, the 24 MHz crystal. Declaring 48000 does not make
 * the link run faster; it makes everything above it 2.3 % out.
 */
static int soundtap_set_clocks(struct snd_soc_dai *cpu_dai, bool spdif, bool auxclk)
{
	int ret;

	if (spdif && !auxclk) {
		ret = snd_soc_dai_set_sysclk(cpu_dai, MCASP_CLK_HCLK_AHCLK,
					     11289600, SND_SOC_CLOCK_IN);
		if (ret)
			return ret;
		return snd_soc_dai_set_clkdiv(cpu_dai, MCASP_CLKDIV_BCLK, 2);
	}
	ret = snd_soc_dai_set_sysclk(cpu_dai, MCASP_CLK_HCLK_AUXCLK,
				     24000000, SND_SOC_CLOCK_OUT);
	if (ret)
		return ret;
	ret = snd_soc_dai_set_clkdiv(cpu_dai, MCASP_CLKDIV_AUXCLK, 2);
	if (ret)
		return ret;
	return snd_soc_dai_set_clkdiv(cpu_dai, MCASP_CLKDIV_BCLK, spdif ? 2 : 4);
}

static int soundtap_startup(struct snd_pcm_substream *substream)
{
	struct snd_soc_pcm_runtime *rtd = snd_soc_substream_to_rtd(substream);
	struct snd_soc_dai *cpu_dai = snd_soc_rtd_to_cpu(rtd, 0);
	bool soundtap_spdif = substream_is_spdif(substream);
	bool soundtap_auxclk = substream_is_auxclk(substream);
	unsigned int rate = soundtap_rate(soundtap_spdif, soundtap_auxclk);
	int ret;

	/*
	 * S/PDIF capture exists only on a McASP with ti,dit-rx-i2s: the soundbar's
	 * processor sends its input back, on its own clocks, so nothing here is ours to
	 * set but the shape of the stream: stereo S32_LE at 44100 Hz, the rate the
	 * ST300's processor was measured to send on 2026-11-08. An analog
	 * capture (the Wireless Link's AUX in) shares the playback clocks and takes the
	 * path below: forcing 44100 on it left nothing once the McASP's sync mode
	 * pinned it to the running playback's 46875, and AUX could not open.
	 */
	if (substream->stream == SNDRV_PCM_STREAM_CAPTURE && soundtap_spdif) {
		ret = snd_pcm_hw_constraint_mask64(substream->runtime,
						   SNDRV_PCM_HW_PARAM_FORMAT,
						   SNDRV_PCM_FMTBIT_S32_LE);
		if (ret < 0)
			return ret;
		ret = snd_pcm_hw_constraint_minmax(substream->runtime,
						   SNDRV_PCM_HW_PARAM_RATE, 44100, 44100);
		return ret < 0 ? ret : 0;
	}

	/*
	 * The clock has to be set here, not in hw_params. davinci-mcasp builds its
	 * rate rule from mcasp->sysclk_freq, and it does that while the parameters
	 * are being refined -- before hw_params runs. With the clock still zero the
	 * rule accepts nothing, the parameter space comes out empty, and every open
	 * of the card fails with "Broken configuration for this PCM: no
	 * configurations available". Measured on the board 2026-09-18, after the
	 * codec was already fixed: aplay --dump-hw-params printed no table at all,
	 * because the refine failed before there was one.
	 */
	ret = soundtap_set_clocks(cpu_dai, soundtap_spdif, soundtap_auxclk);
	if (ret)
		return ret;
	/*
	 * Diagnostics, because "Broken configuration for this PCM: no configurations
	 * available" says only that the parameter space came out empty, never which
	 * rule emptied it. The ST10 refuses every rate and every format here while the
	 * Wireless Link, on the same driver and the same McASP, accepts 46875 -- so
	 * what the two boards disagree about has to be read off the running board.
	 */
	dev_dbg(rtd->card->dev,
		 "startup: spdif=%d rates=0x%x %u..%u formats=0x%llx channels=%u..%u\n",
		 soundtap_spdif, substream->runtime->hw.rates,
		 substream->runtime->hw.rate_min, substream->runtime->hw.rate_max,
		 (unsigned long long)substream->runtime->hw.formats,
		 substream->runtime->hw.channels_min,
		 substream->runtime->hw.channels_max);
	ret = snd_pcm_hw_constraint_mask64(substream->runtime,
					   SNDRV_PCM_HW_PARAM_FORMAT,
					   soundtap_spdif ? SNDRV_PCM_FMTBIT_S16_LE :
					   SNDRV_PCM_FMTBIT_S32_LE);

	if (ret < 0) {
		dev_dbg(rtd->card->dev, "startup: format constraint failed %d\n", ret);
		return ret;
	}
	/* Say the one rate the hardware can produce, so a client that asks ALSA
	 * what is supported is told the truth and plug resamples to it. */
	ret = snd_pcm_hw_constraint_minmax(substream->runtime,
					   SNDRV_PCM_HW_PARAM_RATE, rate, rate);
	dev_dbg(rtd->card->dev, "startup: rate constraint -> %d\n", ret);
	return ret < 0 ? ret : 0;
}

static int soundtap_hw_params(struct snd_pcm_substream *substream,
			     struct snd_pcm_hw_params *params)
{
	struct snd_soc_pcm_runtime *rtd = snd_soc_substream_to_rtd(substream);
	struct snd_soc_dai *cpu_dai = snd_soc_rtd_to_cpu(rtd, 0);
	bool soundtap_spdif = substream_is_spdif(substream);
	bool soundtap_auxclk = substream_is_auxclk(substream);
	int ret;

	if (substream->stream == SNDRV_PCM_STREAM_CAPTURE)
		return params_channels(params) == 2 &&
		       params_format(params) == SNDRV_PCM_FORMAT_S32_LE ? 0 : -EINVAL;

	if (params_channels(params) != 2 ||
	    params_format(params) != (soundtap_spdif ? SNDRV_PCM_FORMAT_S16_LE :
						 SNDRV_PCM_FORMAT_S32_LE))
		return -EINVAL;
	if (params_rate(params) != soundtap_rate(soundtap_spdif, soundtap_auxclk))
		return -EINVAL;

	/* Again, because a device can be reopened at a different rate and the clock
	 * must describe what this stream is about to do, not what the last one did. */
	ret = soundtap_set_clocks(cpu_dai, soundtap_spdif, soundtap_auxclk);
	if (ret)
		return ret;
	if (soundtap_spdif)
		return 0;

	return snd_soc_dai_set_tdm_slot(cpu_dai, 0x3, 0x3, 2, 32);
}

static const struct snd_soc_ops soundtap_ops = {
	.startup = soundtap_startup,
	.hw_params = soundtap_hw_params,
};

static int soundtap_audio_probe(struct platform_device *pdev)
{
	struct device_node *np = pdev->dev.of_node;
	struct soundtap_audio *priv;
	int ret;

	/*
	 * Per instance, not global. A board with both outputs -- the Wireless
	 * Link has an analog link on one McASP and S/PDIF on the other -- probes
	 * this driver twice, and one shared card and dai_link meant the second
	 * probe quietly rewrote the first one's.
	 */
	priv = devm_kzalloc(&pdev->dev, sizeof(*priv), GFP_KERNEL);
	if (!priv)
		return -ENOMEM;

	priv->auxclk = of_device_is_compatible(np, "bose,soundtap-mcasp-spdif-auxclk");
	priv->spdif = priv->auxclk || of_device_is_compatible(np, "bose,soundtap-mcasp-spdif");
	priv->cpu.of_node = of_parse_phandle(np, "audio-cpu", 0);
	priv->codec.of_node = of_parse_phandle(np, "audio-codec", 0);
	if (!priv->cpu.of_node || !priv->codec.of_node)
		return -EINVAL;
	priv->codec.dai_name = "dit-hifi";
	priv->platform.of_node = priv->cpu.of_node;

	/*
	 * Analog needs the dummy codec because 46875 is outside DIT's discrete rate mask.
	 * S/PDIF keeps DIT at 44.1 kHz; the AUXCLK S/PDIF runs at 46875 and so takes the
	 * dummy codec too.
	 */

	priv->link.name = "SoundTap McASP";
	priv->link.stream_name = "SoundTap Audio";
	priv->link.dai_fmt = SND_SOC_DAIFMT_LEFT_J | SND_SOC_DAIFMT_BC_FC |
			     SND_SOC_DAIFMT_NB_NF;
	priv->link.ops = &soundtap_ops;
	priv->link.cpus = &priv->cpu;
	priv->link.num_cpus = 1;
	priv->link.codecs = priv->spdif && !priv->auxclk ? &priv->codec : &snd_soc_dummy_dlc;
	priv->link.num_codecs = 1;
	priv->link.platforms = &priv->platform;
	priv->link.num_platforms = 1;

	priv->card.owner = THIS_MODULE;
	priv->card.dai_link = &priv->link;
	priv->card.num_links = 1;
	priv->card.dev = &pdev->dev;
	/*
	 * A board with two of these needs two names, or the second card collides
	 * with the first. "model" is the ASoC spelling; a node without one keeps
	 * the name every existing device tree here already produces.
	 */
	priv->card.name = "SoundTap";
	ret = snd_soc_of_parse_card_name(&priv->card, "model");
	if (ret && ret != -EINVAL)
		return ret;
	if (!priv->card.name)
		priv->card.name = "SoundTap";
	snd_soc_card_set_drvdata(&priv->card, priv);

	return devm_snd_soc_register_card(&pdev->dev, &priv->card);
}

static const struct of_device_id soundtap_audio_of_match[] = {
	{ .compatible = "bose,soundtap-mcasp-audio" },
	{ .compatible = "bose,soundtap-mcasp-spdif" },
	{ .compatible = "bose,soundtap-mcasp-spdif-auxclk" },
	{ }
};
MODULE_DEVICE_TABLE(of, soundtap_audio_of_match);

static struct platform_driver soundtap_audio_driver = {
	.probe = soundtap_audio_probe,
	.driver = {
		.name = "soundtap-mcasp-audio",
		.of_match_table = soundtap_audio_of_match,
		.pm = &snd_soc_pm_ops,
	},
};
module_platform_driver(soundtap_audio_driver);

MODULE_DESCRIPTION("SoundTap McASP machine driver");
MODULE_LICENSE("GPL");
