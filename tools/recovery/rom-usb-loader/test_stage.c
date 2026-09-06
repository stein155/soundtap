/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#define main loader_main
#include "rom-usb-loader.c"
#undef main

#include <assert.h>

static const struct libusb_endpoint_descriptor status_endpoint = {
	.bEndpointAddress = LIBUSB_ENDPOINT_IN | 2,
	.bmAttributes = LIBUSB_TRANSFER_TYPE_INTERRUPT,
	.wMaxPacketSize = 16,
};
static const struct libusb_endpoint_descriptor data_endpoints[] = {
	{ .bEndpointAddress = LIBUSB_ENDPOINT_IN | 1,
	  .bmAttributes = LIBUSB_TRANSFER_TYPE_BULK, .wMaxPacketSize = 512 },
	{ .bEndpointAddress = 1, .bmAttributes = LIBUSB_TRANSFER_TYPE_BULK,
	  .wMaxPacketSize = 512 },
};
static const uint8_t union_descriptor[] = {
	5, 0x24, 0x00, 0x10, 0x01, 5, 0x24, 0x01, 0, 1,
	4, 0x24, 0x02, 0, 5, 0x24, 0x06, 0, 1,
};
static const uint8_t rndis_union_descriptor[] = { 5, 0x24, 0x06, 2, 3 };

static bool
rndis_configuration(const struct libusb_endpoint_descriptor *status,
	const struct libusb_endpoint_descriptor data[2], const uint8_t *extra,
	size_t extra_length, struct rndis_device *rndis)
{
	const struct libusb_interface_descriptor alts[] = {
		{ .bInterfaceNumber = 0, .bInterfaceClass = 0x02,
		  .bInterfaceSubClass = 0x02, .bInterfaceProtocol = 0xff,
		  .bNumEndpoints = 1, .endpoint = status,
		  .extra = extra, .extra_length = (int)extra_length },
		{ .bInterfaceNumber = 1, .bInterfaceClass = 0x0a, .bNumEndpoints = 2,
		  .endpoint = data },
	};
	const struct libusb_interface interfaces[] = {
		{ .altsetting = &alts[0], .num_altsetting = 1 },
		{ .altsetting = &alts[1], .num_altsetting = 1 },
	};
	const struct libusb_config_descriptor config = {
		.bNumInterfaces = 2, .interface = interfaces,
	};

	return parse_rndis_configuration(&config, rndis);
}

static bool
valid_rndis_configuration(struct rndis_device *rndis)
{
	return rndis_configuration(&status_endpoint, data_endpoints, union_descriptor,
	    sizeof(union_descriptor), rndis);
}

static bool
valid_composite_rndis_configuration(struct rndis_device *rndis)
{
	const struct libusb_interface_descriptor alts[] = {
		{ .bInterfaceNumber = 0, .bInterfaceClass = 0x02,
		  .bInterfaceSubClass = 0x02, .bInterfaceProtocol = 1 },
		{ .bInterfaceNumber = 1, .bInterfaceClass = 0x0a,
		  .bNumEndpoints = 2, .endpoint = data_endpoints },
		{ .bInterfaceNumber = 2, .bInterfaceClass = 0x02,
		  .bInterfaceSubClass = 0x02, .bInterfaceProtocol = 0xff,
		  .bNumEndpoints = 1, .endpoint = &status_endpoint,
		  .extra = rndis_union_descriptor,
		  .extra_length = sizeof(rndis_union_descriptor) },
		{ .bInterfaceNumber = 3, .bInterfaceClass = 0x0a, .bNumEndpoints = 2,
		  .endpoint = data_endpoints },
	};
	const struct libusb_interface interfaces[] = {
		{ .altsetting = &alts[0], .num_altsetting = 1 },
		{ .altsetting = &alts[1], .num_altsetting = 1 },
		{ .altsetting = &alts[2], .num_altsetting = 1 },
		{ .altsetting = &alts[3], .num_altsetting = 1 },
	};
	const struct libusb_config_descriptor config = {
		.bNumInterfaces = 4, .interface = interfaces,
	};

	return parse_rndis_configuration(&config, rndis);
}

int
main(void)
{
	{
		struct image image = { .name = "soundtap-ram-nand.itb" };
		uint8_t rrq[] = { 0, 1, 'w', 'r', 'o', 'n', 'g', 0, 'o', 'c', 't', 'e', 't', 0 };
		char filename[32];

		assert(parse_tftp_rrq(rrq, sizeof(rrq), filename, sizeof(filename)));
		accept_any_filename = false;
		assert(!tftp_filename_allowed(filename, &image));
		assert(tftp_filename_allowed("soundtap-ram-nand.itb", &image));
		accept_any_filename = true;
		assert(tftp_filename_allowed(filename, &image));
	}
	/* ROM 6141 leaves; only new-address SPL 6141 may enter stage two. */
	assert(!stage_device_matches(2, 7, 7, TI_VID, AM335X_USB_PID));
	assert(stage_device_matches(2, 7, 8, TI_VID, AM335X_USB_PID));
	assert(!stage_device_matches(2, 7, 8, TI_VID, UBOOT_USB_PID));
	assert(stage_device_matches(3, 0, 8, TI_VID, UBOOT_USB_PID));
	assert(!stage_accepts_configurations(1, 2));
	assert(stage_accepts_configurations(2, 2));
	assert(stage_accepts_configurations(3, 2));
	assert(!stage_accepts_configurations(3, 3));
	{
		struct rndis_device rndis;

		assert(valid_rndis_configuration(&rndis));
		assert(rndis.control_if == 0 && rndis.data_if == 1);
	}
	{
		struct rndis_device rndis;

		assert(valid_composite_rndis_configuration(&rndis));
		assert(rndis.control_if == 2 && rndis.data_if == 3);
	}
	{
		const struct libusb_interface_descriptor alt = {
			.bInterfaceNumber = 2, .bAlternateSetting = 1,
			.bInterfaceClass = 0x02, .bInterfaceSubClass = 0x02,
			.bInterfaceProtocol = 0xff, .bNumEndpoints = 1,
			.endpoint = &status_endpoint,
			.extra = rndis_union_descriptor,
			.extra_length = sizeof(rndis_union_descriptor),
		};
		const struct libusb_interface iface = {
			.altsetting = &alt, .num_altsetting = 1,
		};
		const struct libusb_config_descriptor config = {
			.bConfigurationValue = 2, .bNumInterfaces = 1,
			.bmAttributes = 0xc0, .MaxPower = 50, .interface = &iface,
		};
		char text[256];

		format_rndis_descriptor(text, sizeof(text), &config, 0);
		assert(strcmp(text, "config[0] value=2 interfaces=1 attributes=c0 max-power=50; if=2 alt=1 class=02 subclass=02 protocol=ff endpoints=1 extra=5 ep=82/03/16 raw=0524060203 union=2:3") == 0);
		format_rndis_descriptor(text, 8, &config, 0);
		assert(strcmp(text, "config[") == 0);
	}
	{
		const struct libusb_interface_descriptor alts[] = {
			{ .bInterfaceNumber = 0, .bInterfaceClass = 0x02,
			  .bInterfaceSubClass = 0x02, .bInterfaceProtocol = 0xff,
			  .bNumEndpoints = 1, .endpoint = &status_endpoint,
			  .extra = union_descriptor, .extra_length = sizeof(union_descriptor) },
			{ .bInterfaceNumber = 1, .bInterfaceClass = 0x0a,
			  .bNumEndpoints = 2, .endpoint = data_endpoints },
		};
		const struct libusb_interface interfaces[] = {
			{ .altsetting = &alts[0], .num_altsetting = 1 },
			{ .altsetting = &alts[1], .num_altsetting = 1 },
		};
		const struct libusb_config_descriptor valid = {
			.bConfigurationValue = 2, .bNumInterfaces = 2,
			.bmAttributes = 0xc0, .MaxPower = 1, .interface = interfaces,
		};
		const struct libusb_config_descriptor empty = {
			.bConfigurationValue = 1, .bmAttributes = 0xc0, .MaxPower = 1,
		};
		char text[512];

		format_rndis_descriptor(text, sizeof(text), &valid, 0);
		assert(strcmp(text, "config[0] value=2 interfaces=2 attributes=c0 max-power=1; if=0 alt=0 class=02 subclass=02 protocol=ff endpoints=1 extra=19 ep=82/03/16 raw=05240010010524010001042402000524060001 union=0:1; if=1 alt=0 class=0a subclass=00 protocol=00 endpoints=2 extra=0 ep=81/02/512 ep=01/02/512") == 0);
		format_rndis_descriptor(text, sizeof(text), &empty, 1);
		assert(strcmp(text, "config[1] value=1 interfaces=0 attributes=c0 max-power=1") == 0);
		assert(valid_rndis_configuration(&(struct rndis_device){ 0 }));
	}
	{
		struct libusb_endpoint_descriptor wrong_status = status_endpoint;
		struct libusb_endpoint_descriptor wrong_data[2] = {
			data_endpoints[0], data_endpoints[1],
		};
		static const uint8_t wrong_union[] = { 5, 0x24, 0x06, 0, 2 };

		wrong_status.bEndpointAddress = 2;
		assert(!rndis_configuration(&wrong_status, data_endpoints,
		    union_descriptor, sizeof(union_descriptor), &(struct rndis_device){ 0 }));
		wrong_status = status_endpoint;
		wrong_status.bmAttributes = LIBUSB_TRANSFER_TYPE_BULK;
		assert(!rndis_configuration(&wrong_status, data_endpoints,
		    union_descriptor, sizeof(union_descriptor), &(struct rndis_device){ 0 }));
		wrong_data[0].bEndpointAddress = 1;
		assert(!rndis_configuration(&status_endpoint, wrong_data,
		    union_descriptor, sizeof(union_descriptor), &(struct rndis_device){ 0 }));
		assert(!rndis_configuration(&status_endpoint, data_endpoints, wrong_union,
		    sizeof(wrong_union), &(struct rndis_device){ 0 }));
	}
	assert(!has_unique_rndis_configuration(0));
	assert(has_unique_rndis_configuration(1));
	assert(!has_unique_rndis_configuration(2));
	{
		const struct rndis_device rndis = { .control_if = 0 };
		struct notification uboot = {
			.status = LIBUSB_TRANSFER_COMPLETED,
			.actual_length = 8,
			.bytes = { 1 },
		};
		const struct notification overflow = {
			.status = LIBUSB_TRANSFER_OVERFLOW,
			.actual_length = 0,
		};

		legacy_uboot = false;
		stage_two = false;
		log_session = 3;
		assert(valid_rndis_notification(&rndis, &uboot));
		uboot.bytes[1] = 1;
		assert(!valid_rndis_notification(&rndis, &uboot));
		assert(!valid_rndis_notification(&rndis, &overflow));
	}
	legacy_uboot = true;
	assert(!stage_device_matches(3, 0, 8, TI_VID, UBOOT_USB_PID));
	assert(stage_device_matches(3, 0, 8, TI_VID, AM335X_USB_PID));
	stage_two = false;
	log_session = 3;
	assert(uses_spl_rndis_notification());
	assert(!needs_rom_departure(true));
	assert(needs_rom_departure(false));
	return 0;
}
