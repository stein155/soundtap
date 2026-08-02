// SPDX-License-Identifier: GPL-2.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors
#include <errno.h>
#include <fcntl.h>
#include <linux/i2c-dev.h>
#include <linux/spi/spidev.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>

#define ST10_SPI_DEVICE "/dev/spidev1.1"
#define ST20_SPI_DEVICE "/dev/spidev0.1"

static int write_file(const char *path, const char *value)
{
	int fd = open(path, O_WRONLY);
	ssize_t length = strlen(value);
	int saved;

	if (fd < 0)
		return -1;
	if (write(fd, value, length) == length) {
		close(fd);
		return 0;
	}
	saved = errno;
	close(fd);
	errno = saved;
	return -1;
}

static int gpio_base(const char *address)
{
	char link[256];
	char path[64];
	ssize_t length;
	unsigned int base;
	int index;

	for (index = 0; index < 1024; index += 32) {
		snprintf(path, sizeof(path), "/sys/class/gpio/gpiochip%d", index);
		length = readlink(path, link, sizeof(link) - 1);
		if (length < 0)
			continue;
		link[length] = '\0';
		if (strstr(link, address) &&
		    sscanf(path, "/sys/class/gpio/gpiochip%u", &base) == 1)
			return base;
	}
	errno = ENODEV;
	return -1;
}

static int gpio_set(const char *address, unsigned int offset, bool high)
{
	char path[64];
	char number[16];
	int base = gpio_base(address);
	unsigned int gpio;

	if (base < 0)
		return -1;
	gpio = base + offset;

	snprintf(path, sizeof(path), "/sys/class/gpio/gpio%u", gpio);
	if (access(path, F_OK)) {
		snprintf(number, sizeof(number), "%u", gpio);
		if (write_file("/sys/class/gpio/export", number) && errno != EBUSY)
			return -1;
	}
	snprintf(path, sizeof(path), "/sys/class/gpio/gpio%u/direction", gpio);
	return write_file(path, high ? "high" : "low");
}

/*
 * One write() per register, chip select released in between. A change brought back a
 * two-transfer message with cs_change set on its last transfer, which spidev reads as
 * "leave chip select asserted after this message": the codec then saw every following
 * write as more data for the first address. An earlier change had already restored this
 * shape once, for the same reason.
 */
static int spi_write_reg(int fd, unsigned int reg, unsigned int value)
{
	uint8_t data[2] = { (uint8_t)(reg << 1), (uint8_t)value };

	if (reg > 0x7f || value > 0xff) {
		errno = EINVAL;
		return -1;
	}
	return write(fd, data, sizeof(data)) == sizeof(data) ? 0 : -1;
}

static int spi_read_reg(int fd, unsigned int reg, uint8_t *value)
{
	uint8_t command = (reg << 1) | 1;
	struct spi_ioc_transfer transfers[2] = {
		{ .tx_buf = (uintptr_t)&command, .len = 1, .delay_usecs = 1 },
		{ .rx_buf = (uintptr_t)value, .len = 1, .delay_usecs = 1 },
	};

	return ioctl(fd, SPI_IOC_MESSAGE(2), transfers) == 2 ? 0 : -1;
}

/*
 * The same read as one transfer instead of two.
 *
 * spi_read_reg sends the command byte and clocks the answer in a second transfer of the
 * same message. That keeps chip select asserted, but it is not the only shape a codec may
 * expect: a part that samples its address on the first eight clocks and drives the next
 * eight can be upset by the gap. Having both makes "the codec answers 0xff" a statement
 * about the codec rather than about how we asked.
 */
static int spi_read_reg_once(int fd, unsigned int reg, uint8_t *value)
{
	uint8_t tx[2] = { (uint8_t)((reg << 1) | 1), 0 };
	uint8_t rx[2] = { 0, 0 };
	struct spi_ioc_transfer transfer = {
		.tx_buf = (uintptr_t)tx, .rx_buf = (uintptr_t)rx, .len = 2,
	};

	if (ioctl(fd, SPI_IOC_MESSAGE(1), &transfer) != 2)
		return -1;
	*value = rx[1];
	return 0;
}

static int spi_write_block(int fd, unsigned int reg, const uint8_t *data,
			   size_t length)
{
	uint8_t tx[33];

	if (!length || length > 32 || reg + length > 0x80) {
		errno = EINVAL;
		return -1;
	}
	tx[0] = reg << 1;
	memcpy(tx + 1, data, length);
	return write(fd, tx, length + 1) == (ssize_t)(length + 1) ? 0 : -1;
}

static int spi_read_block(int fd, unsigned int reg, uint8_t *data,
			  size_t length)
{
	uint8_t command = (reg << 1) | 1;
	struct spi_ioc_transfer transfers[2] = {
		{ .tx_buf = (uintptr_t)&command, .len = 1, .delay_usecs = 1 },
		{ .rx_buf = (uintptr_t)data, .len = length, .delay_usecs = 1 },
	};

	if (!length || length > 32 || reg + length > 0x80) {
		errno = EINVAL;
		return -1;
	}
	return ioctl(fd, SPI_IOC_MESSAGE(2), transfers) == (int)length + 1 ? 0 : -1;
}

static int poll_flip(int fd, uint8_t page)
{
	uint8_t value;
	int i;

	if (spi_write_reg(fd, 0, page))
		return -1;
	for (i = 0; i < 200; i++) {
		if (spi_read_reg(fd, 1, &value))
			return -1;
		if (!(value & 1))
			return 0;
	}
	errno = ETIMEDOUT;
	return -1;
}

/* Words 0 and 1 are stock's volume step. APServer indexes two 101-entry tables at
 * 0x2a52d0 and 0x2a5468 by the volume 0..100 (read out of the ST10 rootfs 2026-09-22):
 * volume 100 is 0x400000 / 0x000000, volume 10 is 0x0007d9 / 0xbd9c28 -- the words
 * this loader once wrote, so the speaker then ran at stock's volume 10.
 * Words 2 to 7 are stock's too: the block arrived as set_volume_10, a copy
 * of what stock writes at volume 10, and APServer's volume path varies only words 0 and
 * 1. Words 5 and 6 are 0x32D645 = 0.39717, which is Bose's own wifiAttenuation to three
 * decimals. --dsp-word writes the same block with one word replaced, so the question can
 * be answered by listening to a speaker instead of by reading a disassembly. See
 * docs/hardware/st10/stock-audio-path.md. */
static int dsp_word_index = -1;
static uint32_t dsp_word_value;
/* --volume HEX48 / --volume-blind HEX48: words 0 and 1 as one 48-bit coefficient, which is
 * how stock's two tables pair up (volume 50 = 0x01c0a9e0c000 = -31.25 dB against unity
 * 0x400000000000). soundtapd sends one per volume step, so the DSP does the volume as on
 * stock and the PCM stays at full scale. */
static uint64_t dsp_gain = 0x400000000000ULL;

static int set_volume_full(int fd, bool blind)
{
	uint8_t volume[] = {
		0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x40, 0x00, 0x00, 0x00, 0x32, 0xd6, 0x45, 0x00,
		0x32, 0xd6, 0x45, 0x00, 0x00, 0x00, 0x00, 0x00,
	};
	uint8_t readback[sizeof(volume)];

	volume[0] = dsp_gain >> 40;
	volume[1] = dsp_gain >> 32;
	volume[2] = dsp_gain >> 24;
	volume[4] = dsp_gain >> 16;
	volume[5] = dsp_gain >> 8;
	volume[6] = dsp_gain;
	if (dsp_word_index >= 0) {
		uint8_t *word = volume + dsp_word_index * 4;

		word[0] = dsp_word_value >> 16;
		word[1] = dsp_word_value >> 8;
		word[2] = dsp_word_value;
		word[3] = 0;
	}
	if (spi_write_reg(fd, 0, 0x2c) ||
	    spi_write_block(fd, 0x10, volume, sizeof(volume)) ||
	    spi_write_reg(fd, 0, 0x00))
		return -1;
	/*
	 * The miniDSP runs adaptive coefficients: a host write lands in the buffer the DSP is
	 * not using, and only a flip (page 0x2C reg 1 bit 0) swaps it in. A blind pass cannot
	 * poll for the flip, so it waits a fixed time -- the swap happens on the next frame,
	 * tens of microseconds at 46875 Hz -- and then writes the block again so both buffers
	 * agree. Returning after the first write left the level in the idle buffer.
	 */
	if (blind) {
		if (spi_write_reg(fd, 0, 0x2c) || spi_write_reg(fd, 1, 0x05))
			return -1;
		usleep(20000);
		return spi_write_reg(fd, 0, 0x2c) ||
		       spi_write_block(fd, 0x10, volume, sizeof(volume)) ||
		       spi_write_reg(fd, 0, 0x00) ? -1 : 0;
	}
	if (spi_write_reg(fd, 0, 0x2c) || spi_write_reg(fd, 1, 0x05) ||
	    poll_flip(fd, 0x2c) || spi_write_reg(fd, 0, 0x2c) ||
	    spi_write_block(fd, 0x10, volume, sizeof(volume)) ||
	    spi_read_block(fd, 0x10, readback, sizeof(readback)) ||
	    memcmp(readback, volume, sizeof(volume)) || spi_write_reg(fd, 0, 0x00))
		return -1;
	return 0;
}

static int load_config(int fd, FILE *config, unsigned int min_writes)
{
	char line[4096];
	unsigned int reg = 0, value;
	unsigned int writes = 0;
	bool continuing = false;

	while (fgets(line, sizeof(line), config)) {
		if (sscanf(line, "w 30 %x %x", &reg, &value) == 2) {
			if (reg == 0xfe) {
				usleep(value * 1000);
				continuing = false;
				continue;
			}
			if (spi_write_reg(fd, reg, value))
				return -1;
			reg++;
			writes++;
			continuing = true;
		} else if (sscanf(line, "> %x", &value) == 1) {
			if (!continuing || spi_write_reg(fd, reg, value)) {
				errno = EINVAL;
				return -1;
			}
			reg++;
			writes++;
		} else if (sscanf(line, "d %u", &value) == 1) {
			usleep(value * 1000);
			continuing = false;
		}
	}

	if (ferror(config) || writes < min_writes) {
		errno = EINVAL;
		return -1;
	}
	printf("AIC3256_CONFIG_WRITES=%u\n", writes);
	return 0;
}


/*
 * The analog stage is fed by an NCP6922 at i2c address 0x18, and its four rails are
 * enabled by bits in register 0x14: dcdc1 0x02, dcdc2 0x08, ldo3 0x40, ldo4 0x80. Bose's
 * own regulator driver does exactly this; mainline has no driver for the chip, so it is
 * done here, once, before anything talks to the codec.
 *
 * Without it the AIC3256 has no supply and answers 0xff on every register -- measured on
 * the ST10 on 2026-09-20, where the codec was unreachable and the speaker was silent.
 * A speaker whose rails are already on reads 0xca here and nothing is written.
 */
#define NCP6922_BUS "/dev/i2c-1"
#define NCP6922_ADDR 0x18
#define NCP6922_ENABLE_REG 0x14
#define NCP6922_RAILS 0xca

static int ncp6922_rails_on(void)
{
	unsigned char command[2];
	unsigned char reg = NCP6922_ENABLE_REG;
	unsigned char value = 0;
	int fd = open(NCP6922_BUS, O_RDWR);

	if (fd < 0)
		return -1;
	if (ioctl(fd, I2C_SLAVE, NCP6922_ADDR) < 0)
		goto fail;
	if (write(fd, &reg, 1) != 1 || read(fd, &value, 1) != 1)
		goto fail;
	if ((value & NCP6922_RAILS) == NCP6922_RAILS) {
		close(fd);
		printf("NCP6922_RAILS_ALREADY_ON=0x%02x\n", value);
		return 0;
	}
	command[0] = NCP6922_ENABLE_REG;
	command[1] = (unsigned char)(value | NCP6922_RAILS);
	if (write(fd, command, 2) != 2)
		goto fail;
	close(fd);
	printf("NCP6922_RAILS_ENABLED=0x%02x\n", command[1]);
	return 0;
fail:
	{
		int saved = errno;
		close(fd);
		errno = saved;
	}
	return -1;
}

/*
 * The codec scripts are Bose's (miniDSP program and speaker tuning), so the image carries
 * none: the boot script copies each speaker's own script out of its stock firmware into
 * persistent storage (ADR 0082), and the codec gets exactly stock's register writes.
 */
#define CODEC_DIR "/mnt/nv/soundtap/codec/"

int main(int argc, char **argv)
{
	bool spotty;
	bool rhino;
	const char *spi_device;
	const char *config_file;
	const char *gpio_address;
	uint32_t speed = 3000000;
	uint8_t mode = SPI_MODE_1;
	uint8_t value = 0;
	uint8_t probe = 0;
	uint8_t bits = 0;
	FILE *config;
	int fd;
	bool dsp_word = argc == 4 && !strcmp(argv[1], "--dsp-word");
	bool volume_only = ((argc == 2 || argc == 3) && !strcmp(argv[1], "--volume")) || dsp_word;
	bool volume_blind = (argc == 2 || argc == 3) && !strcmp(argv[1], "--volume-blind");
	bool probe_only = argc >= 2 && !strcmp(argv[1], "--probe-only");
	bool probe_sweep = argc >= 2 && !strcmp(argv[1], "--probe-sweep");
	/*
	 * --blind: write the configuration without reading anything back.
	 *
	 * On the ST10 this codec answers 0xff on every register, in all four SPI modes and
	 * whether the address and the answer share a frame or not, with its rails on, its
	 * enable line high and both reset polarities tried. A part that is there but whose
	 * SDOUT is not wired to the SoC looks exactly like that -- and SPI reads are the
	 * only thing such a board loses. So offer the write-only path: configure, then
	 * listen. If the speaker makes a sound, the silence was the read path.
	 */
	bool blind = argc >= 2 && !strcmp(argv[1], "--blind");
	/* --probe-only [device]: which chip select actually reaches the codec is a hardware
	 * question, and on the ST10 the answer at reg=<1> is 0xff. Being able to point the
	 * probe at the other node answers it in one boot instead of one build. */
	const char *probe_device = argc == 3 && (probe_only || probe_sweep) ? argv[2] : NULL;
	FILE *cmdline = fopen("/proc/cmdline", "r");
	char command_line[1024] = "";

	if (cmdline) {
		if (!fgets(command_line, sizeof(command_line), cmdline))
			command_line[0] = '\0';
		fclose(cmdline);
	}
	/* mojo, the SoundTouch 30 Series I, is the ST20's board with its own codec script;
	 * spotty3 and mojo3, the ST20 and ST30 Series III, are the ST10's board with theirs.
	 * Compare the whole value: "variant=spotty" is a prefix of "variant=spotty3". */
	char variant[16] = "";
	const char *v = strstr(command_line, "variant=");
	if (v)
		sscanf(v + strlen("variant="), "%15[a-z0-9]", variant);
	bool mojo = !strcmp(variant, "mojo");
	bool spotty3 = !strcmp(variant, "spotty3"), mojo3 = !strcmp(variant, "mojo3");
	spotty = mojo || !strcmp(variant, "spotty");
	rhino = spotty3 || mojo3 || !strcmp(variant, "rhino");
	spi_device = spotty ? ST20_SPI_DEVICE : ST10_SPI_DEVICE;
	config_file = mojo ? CODEC_DIR "mojo_scm_normal.cfg"
		: spotty ? CODEC_DIR "spotty_scm_normal.cfg"
		: spotty3 ? CODEC_DIR "spotty_sm2_normal.cfg"
		: mojo3 ? CODEC_DIR "mojo_sm2_normal.cfg" : CODEC_DIR "rhino_sm2_normal.cfg";
	gpio_address = spotty ? "481ae000" : "481ac000";

	if (argc == 3 && !strcmp(argv[1], "--mode")) {
		mode = strtoul(argv[2], NULL, 0);
		if (mode > SPI_MODE_3) {
			fprintf(stderr, "SPI mode must be 0..3\n");
			return 2;
		}
	} else if (dsp_word) {
		long index = strtol(argv[2], NULL, 0);

		if (index < 0 || index > 7) {
			fprintf(stderr, "--dsp-word takes word 0..7 of the page 0x2C block\n");
			return 2;
		}
		dsp_word_index = (int)index;
		dsp_word_value = strtoul(argv[3], NULL, 16) & 0xffffff;
	} else if (argc == 3 && (volume_blind || (volume_only && !dsp_word))) {
		char *end;

		dsp_gain = strtoull(argv[2], &end, 16);
		if (*end || dsp_gain > 0xffffffffffffULL) {
			fprintf(stderr, "--volume takes a 48-bit hex coefficient\n");
			return 2;
		}
	} else if ((argc > 2 && !probe_device && !dsp_word) ||
		   (argc == 2 && !volume_only && !volume_blind && !probe_only && !probe_sweep && !blind)) {
		fprintf(stderr,
		    "usage: %s [--probe-only [/dev/spidevN.M]|--probe-sweep [/dev/spidevN.M]|--blind|--volume [HEX48]|--volume-blind [HEX48]|--dsp-word 0..7 HEX24|--mode 0..3]\n",
		    argv[0]);
		return 2;
	}

	if (probe_device)
		spi_device = probe_device;
	fd = open(spi_device, O_RDWR);
	if (fd < 0 || ioctl(fd, SPI_IOC_WR_MODE, &mode) ||
	    ioctl(fd, SPI_IOC_WR_MAX_SPEED_HZ, &speed)) {
		perror(spi_device);
		return 1;
	}
	/*
	 * --probe-sweep: every clock phase and both transfer shapes, in one run.
	 *
	 * A part that is powered and out of reset but listening on a different SPI mode
	 * looks exactly like a part that is not there at all: 0xff on every register. So
	 * does a part that wants its address and its answer in one frame. Asking every way
	 * costs one boot and rules out the measurement path, which is what four failed
	 * measurements in a row on this speaker have already been about.
	 */
	if (probe_sweep) {
		uint8_t m;

		for (m = SPI_MODE_0; m <= SPI_MODE_3; m++) {
			uint8_t split = 0xff, once = 0xff;

			if (ioctl(fd, SPI_IOC_WR_MODE, &m)) {
				perror("AIC3256 SPI mode");
				continue;
			}
			(void)spi_write_reg(fd, 0, 0);
			(void)spi_read_reg(fd, 0x1b, &split);
			(void)spi_read_reg_once(fd, 0x1b, &once);
			printf("AIC3256_SWEEP mode=%u reg1b_split=0x%02x reg1b_once=0x%02x\n",
			       m, split, once);
		}
		close(fd);
		return 0;
	}
	if (probe_only) {
		if (ioctl(fd, SPI_IOC_RD_MODE, &mode) ||
		    ioctl(fd, SPI_IOC_RD_MAX_SPEED_HZ, &speed) ||
		    ioctl(fd, SPI_IOC_RD_BITS_PER_WORD, &bits)) {
			perror("AIC3256 SPI settings");
			return 1;
		}
		if (spi_write_reg(fd, 0, 1)) {
			perror("AIC3256 SPI probe");
			return 1;
		}
		if (spi_read_reg(fd, 0, &value)) {
			spi_write_reg(fd, 0, 0);
			perror("AIC3256 SPI probe");
			return 1;
		}
		probe = value;
		if (spi_write_reg(fd, 0, 0) || spi_read_reg(fd, 0x1b, &value)) {
			perror("AIC3256 SPI restore/read");
			return 1;
		}
		printf("AIC3256_PROBE mode=%u speed=%u bits=%u page1=0x%02x reg1b=0x%02x\n",
		       mode, speed, bits, probe, value);
		return probe == 1 ? 0 : 1;
	}
	if (spotty && !volume_only && !spi_write_reg(fd, 0, 0) &&
	    !spi_read_reg(fd, 0, &value) && value == 0 &&
	    !spi_read_reg(fd, 0x1b, &probe) && probe != 0 && probe != 0xff) {
		if (gpio_set(gpio_address, 16, true) || gpio_set(gpio_address, 19, true)) {
			perror("ADC/DAC reset release");
			return 1;
		}
		close(fd);
		printf("AIC3256_CONFIG_PRESERVED probe=0x%02x\n", probe);
		return 0;
	}

	/*
	 * Only a full configuration goes through reset. --volume-blind skipped this once,
	 * because blind was read as "not the readback path" rather than "volume only": the
	 * gain pass soundtapd runs after the first stream then pulsed the DSP reset, which
	 * wiped the 9976 registers just written blind, and returned before the ADC/DAC reset
	 * line was released again. The ST10 streamed PCM into a codec held in reset and was
	 * silent from the day the blind gain pass shipped.
	 */
	if (!volume_only && !volume_blind) {
		/* Supply first: the enable line the chip sits behind, then its own rails. Both
		 * are no-ops on a board that already has them, and neither is fatal -- a WLA
		 * has no codec to reach and says so on the SPI probe instead.
		 *
		 * Not on the ST10. There gpio2_23 is also the AUX lamp (Bose's rled7), and
		 * holding it high kept that lamp lit in standby. Stock leaves it to the lamp
		 * driver, and the rails do not need it: with the line low the codec still
		 * answers (reg1b=0xe0 on --probe-only, ST10, 2026-09-23). The rails come from
		 * register 0x14 below. */
		if (!spotty && !rhino && gpio_set(gpio_address, 23, true))
			perror("NCP6922 enable");
		if (!spotty && ncp6922_rails_on())
			perror("NCP6922 rails");
		if (spotty ? (gpio_set(gpio_address, 16, false) ||
			      gpio_set(gpio_address, 19, false) ||
			      gpio_set(gpio_address, 18, false)) :
		    (gpio_set(gpio_address, 8, false) || gpio_set(gpio_address, 11, true) ||
			     gpio_set(gpio_address, 10, false) || gpio_set(gpio_address, 22, false))) {
			perror("audio GPIO setup");
			return 1;
		}
		usleep(10000);
		if (gpio_set(gpio_address, spotty ? 18 : 22, true)) {
			perror("DSP reset release");
			return 1;
		}
		usleep(10000);
	}

	if (volume_only || volume_blind) {
		if (set_volume_full(fd, volume_blind)) {
			perror("AIC3256 volume");
			return 1;
		}
		close(fd);
		printf("AIC3256_VOLUME_OK gain=%012llx%s\n", (unsigned long long)dsp_gain,
		       volume_blind ? " (blind)" : "");
		return 0;
	}
	if (spi_write_reg(fd, 0, 0)) {
		perror("AIC3256 SPI write");
		return 1;
	}
	if (!blind) {
		if (spi_read_reg(fd, 0, &value)) {
			perror("AIC3256 SPI read");
			return 1;
		}
		if (value) {
			fprintf(stderr, "AIC3256 SPI readback failed: 0x%02x\n", value);
			return 1;
		}
	}

	config = fopen(config_file, "r");
	if (!config || load_config(fd, config, 9000)) {
		perror(config_file);
		return 1;
	}
	fclose(config);

	if (blind) {
		if (spi_write_reg(fd, 0, 0)) {
			perror("AIC3256 SPI write");
			return 1;
		}
	} else if (spi_write_reg(fd, 0, 0) || spi_read_reg(fd, 0x1b, &value) ||
		   value != 0xe0) {
		fprintf(stderr, "AIC3256 config readback failed: 0x%02x\n", value);
		return 1;
	}
	close(fd);
	if (gpio_set(gpio_address, spotty ? 19 : 10, true)) {
		perror("DAC/ADC reset release");
		return 1;
	}
	printf("AIC3256_CONFIG_OK%s\n",
	       blind ? " (blind, nothing read back)" : "");
	return 0;
}
