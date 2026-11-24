// SPDX-License-Identifier: GPL-2.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors
/* Diagnostic GPIO SPI probe for ST20 AIC3256. No reset, config, mute, or playback. */

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

struct gpio {
	int clk, mosi, miso, cs;
};

static int gpio_base(const char *address)
{
	char link[256], path[300];
	ssize_t length;
	unsigned int base;
	struct dirent *entry;
	DIR *dir = opendir("/sys/class/gpio");

	/* Bases are not multiples of 32: the GPMC's two-line chip moves gpio0 to 610. */
	while (dir && (entry = readdir(dir))) {
		if (sscanf(entry->d_name, "gpiochip%u", &base) != 1)
			continue;
		snprintf(path, sizeof(path), "/sys/class/gpio/%s", entry->d_name);
		length = readlink(path, link, sizeof(link) - 1);
		if (length < 0)
			continue;
		link[length] = '\0';
		if (strstr(link, address)) {
			closedir(dir);
			return (int)base;
		}
	}
	if (dir)
		closedir(dir);
	errno = ENODEV;
	return -1;
}

static int write_file(const char *path, const char *value)
{
	int fd = open(path, O_WRONLY);
	ssize_t n;

	if (fd < 0)
		return -1;
	n = write(fd, value, strlen(value));
	close(fd);
	return n < 0 ? -1 : 0;
}

static int open_gpio(unsigned int number, const char *direction, int initial)
{
	char path[80], value[16];
	int fd;

	snprintf(value, sizeof(value), "%u", number);
	if (write_file("/sys/class/gpio/export", value) < 0 && errno != EBUSY)
		return -1;
	snprintf(path, sizeof(path), "/sys/class/gpio/gpio%u/direction", number);
	if (strcmp(direction, "in") == 0) {
		if (write_file(path, "in") < 0)
			return -1;
	} else if (write_file(path, initial ? "high" : "low") < 0) {
		return -1;
	}
	snprintf(path, sizeof(path), "/sys/class/gpio/gpio%u/value", number);
	fd = open(path, strcmp(direction, "in") == 0 ? O_RDONLY : O_WRONLY);
	return fd;
}

static void set_gpio(int fd, int high)
{
	const char value = high ? '1' : '0';

	if (pwrite(fd, &value, 1, 0) != 1) {
		perror("gpio write");
		exit(1);
	}
}

static int get_gpio(int fd)
{
	char value;

	if (pread(fd, &value, 1, 0) != 1) {
		perror("gpio read");
		exit(1);
	}
	return value == '1';
}

static unsigned char transfer(struct gpio *g, unsigned char out, int mode)
{
	unsigned char in = 0;
	int cpol = mode >> 1, cpha = mode & 1;

	for (int bit = 7; bit >= 0; bit--) {
		if (!cpha)
			set_gpio(g->mosi, (out >> bit) & 1);
		/*
		 * No delay between edges: each sysfs write is a syscall of microseconds, far
		 * slower than the codec's SPI clock limit already. The usleep(5) that used to
		 * sit here slept tens of microseconds each, twice per bit: ~40 s of the boot.
		 */
		set_gpio(g->clk, !cpol);
		if (cpha)
			set_gpio(g->mosi, (out >> bit) & 1);
		if (!cpha)
			in |= get_gpio(g->miso) << bit;
		set_gpio(g->clk, cpol);
		if (cpha)
			in |= get_gpio(g->miso) << bit;
	}
	return in;
}

static unsigned char read_register(struct gpio *g, unsigned char reg, int mode)
{
	unsigned char value;

	set_gpio(g->clk, mode >> 1);
	set_gpio(g->cs, 0);
	transfer(g, (unsigned char)((reg << 1) | 1), mode);
	value = transfer(g, 0, mode);
	set_gpio(g->cs, 1);
	return value;
}

static void write_register(struct gpio *g, unsigned char reg, unsigned char value, int mode)
{
	set_gpio(g->clk, mode >> 1);
	set_gpio(g->cs, 0);
	transfer(g, (unsigned char)(reg << 1), mode);
	transfer(g, value, mode);
	set_gpio(g->cs, 1);
}

static int load_config(struct gpio *g, const char *path, int mode)
{
	char line[4096];
	unsigned int reg = 0, value, writes = 0;
	int continuing = 0;
	FILE *config = fopen(path, "r");

	if (!config)
		return -1;
	while (fgets(line, sizeof(line), config)) {
		if (sscanf(line, "w 30 %x %x", &reg, &value) == 2) {
			if (reg == 0xfe) {
				usleep(value * 1000);
				continuing = 0;
				continue;
			}
			write_register(g, (unsigned char)reg++, (unsigned char)value, mode);
			writes++;
			continuing = 1;
		} else if (sscanf(line, "> %x", &value) == 1) {
			if (!continuing) {
				errno = EINVAL;
				break;
			}
			write_register(g, (unsigned char)reg++, (unsigned char)value, mode);
			writes++;
		} else if (sscanf(line, "d %u", &value) == 1) {
			usleep(value * 1000);
			continuing = 0;
		}
	}
	if (ferror(config) || writes < 9000) {
		fclose(config);
		errno = EINVAL;
		return -1;
	}
	fclose(config);
	printf("AIC3256_BITBANG_CONFIG_WRITES=%u\n", writes);
	return 0;
}

/* gain is words 0 and 1 of the block, word 0 in the high 24 bits: the gain in Q22 and the
 * same level in dB times 65536, the pair stock writes per volume step (0x400000000000 is
 * unity). */
static int set_volume(struct gpio *g, int mode, unsigned long long gain)
{
	unsigned char volume[] = {
		0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x40, 0x00, 0x00, 0x00, 0x32, 0xd6, 0x45, 0x00,
		0x32, 0xd6, 0x45, 0x00, 0x00, 0x00, 0x00, 0x00,
	};
	unsigned int i;

	volume[0] = gain >> 40;
	volume[1] = gain >> 32;
	volume[2] = gain >> 24;
	volume[4] = gain >> 16;
	volume[5] = gain >> 8;
	volume[6] = gain;
	write_register(g, 0x00, 0x2c, mode);
	for (i = 0; i < sizeof(volume); i++)
		write_register(g, (unsigned char)(0x10 + i), volume[i], mode);
	write_register(g, 0x01, 0x05, mode);
	for (i = 0; i < 100 && (read_register(g, 0x01, mode) & 1); i++)
		usleep(1000);
	if (i == 100) {
		errno = ETIMEDOUT;
		return -1;
	}
	write_register(g, 0x00, 0x2c, mode);
	for (i = 0; i < sizeof(volume); i++)
		write_register(g, (unsigned char)(0x10 + i), volume[i], mode);
	for (i = 0; i < sizeof(volume); i++) {
		if (read_register(g, (unsigned char)(0x10 + i), mode) != volume[i]) {
			errno = EIO;
			return -1;
		}
	}
	write_register(g, 0x00, 0x00, mode);
	printf("AIC3256_BITBANG_GAIN=%012llx\n", gain);
	return 0;
}

int main(int argc, char **argv)
{
	int base = gpio_base("44e07000");
	int reset_base = gpio_base("481ae000");
	struct gpio g;
	int volume_only = argc >= 2 && strcmp(argv[1], "--volume") == 0;
	const char *config = argc == 2 && !volume_only ? argv[1] : NULL;
	/* --volume alone is full scale; --volume HEX48 is one volume step. */
	unsigned long long gain = argc == 3 ? strtoull(argv[2], NULL, 16) : 0x400000000000ULL;

	if (argc > 3 || (argc == 3 && !volume_only) || gain > 0xffffffffffffULL) {
		fprintf(stderr, "usage: %s [CONFIG|--volume [HEX48]]\n", argv[0]);
		return 1;
	}

	if (base < 0 || reset_base < 0) {
		perror("GPIO base");
		return 1;
	}
	g.clk = open_gpio(base + 2, "out", 0);
	g.mosi = open_gpio(base + 3, "out", 0);
	g.miso = open_gpio(base + 4, "in", 0);
	g.cs = open_gpio(base + 6, "out", 1);
	if (g.clk < 0 || g.mosi < 0 || g.miso < 0 || g.cs < 0) {
		perror("GPIO setup");
		return 1;
	}
	write_register(&g, 0x00, 1, 1);
	printf("AIC3256_BITBANG mode=1 page1=0x%02x", read_register(&g, 0x00, 1));
	write_register(&g, 0x00, 0, 1);
	printf(" reg1b=0x%02x\n", read_register(&g, 0x1b, 1));
	if (volume_only) {
		if (set_volume(&g, 1, gain)) {
			perror("AIC3256 bitbang volume");
			return 1;
		}
		return 0;
	}
	if (config) {
		int adc = open_gpio((unsigned int)reset_base + 16, "out", 0);
		int dsp = open_gpio((unsigned int)reset_base + 18, "out", 0);
		int dac = open_gpio((unsigned int)reset_base + 19, "out", 0);

		if (adc < 0 || dsp < 0 || dac < 0) {
			perror("reset GPIO setup");
			return 1;
		}
		set_gpio(adc, 0);
		set_gpio(dac, 0);
		set_gpio(dsp, 0);
		usleep(10000);
		set_gpio(dsp, 1);
		usleep(1000000);
		if (load_config(&g, config, 1)) {
			perror("AIC3256 bitbang config");
			return 1;
		}
		set_gpio(adc, 1);
		set_gpio(dac, 1);
		usleep(10000);
		write_register(&g, 0, 0, 1);
		printf("AIC3256_BITBANG_CONFIGURED reg1b=0x%02x\n", read_register(&g, 0x1b, 1));
	}
	return 0;
}
