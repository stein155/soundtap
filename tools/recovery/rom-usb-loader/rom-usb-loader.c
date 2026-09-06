/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#define _POSIX_C_SOURCE 200809L
#ifdef __APPLE__
#define _DARWIN_C_SOURCE
#endif

#include "protocol.h"

#include <libusb.h>

#ifdef __APPLE__
#include <CommonCrypto/CommonDigest.h>
#else
#include <openssl/evp.h>
#endif

#include <limits.h>
#include <errno.h>
#include <fcntl.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

#define TI_VID 0x0451
#define AM335X_USB_PID 0x6141
/* Lab SPL shares ROM PID; U-Boot proper uses its configured TI gadget PID. */
#define SPL_USB_PID AM335X_USB_PID
#define UBOOT_USB_PID 0xd022
#define MLO_MAX_SIZE (128U * 1024U)
#define UBOOT_MAX_SIZE (2U * 1024U * 1024U)
#define PAYLOAD_MAX_SIZE (32U * 1024U * 1024U)
#define UBOOT_STAGE_ATTEMPTS 45
#define USB_COMMAND_TIMEOUT_MS 250
#define USB_BULK_TIMEOUT_MS 120
/*
 * How long to wait for the board's mask ROM to turn up. Ten seconds assumed someone
 * had just plugged it in; a run that power-cycles the board into the loader has to
 * start the loader first, and the board takes a few seconds to come back after the
 * switch, which landed on the wrong side of ten. Waiting longer costs only the time a
 * run with no board attached takes to give up.
 */
#define DEVICE_WAIT_MS 60000
#define SPL_HANDOFF_WAIT_MS 60000
#define DEPARTURE_WAIT_MS 20000
#define REQUEST_DEADLINE_MS 15000
#define MLO_TRANSFER_DEADLINE_MS 10000
#define UBOOT_TRANSFER_DEADLINE_MS 30000
#define TFTP_RETRIES 5
#define TFTP_TIMEOUT_MS 180
#define RNDIS_MAX_TRANSFER 16384
/* Consecutive netconsole read failures tolerated before the watch gives up. */
/*
 * How long a run of failing reads may last before the watch gives up. Time, not a
 * count: the errors return immediately, so any count is really a measure of how fast
 * the host is.
 */
#define WATCH_READ_ERROR_GRACE_MS 30000
/*
 * An upload is only abandoned when the board goes quiet, not on a clock: a rootfs
 * partition is tens of megabytes and there is no useful upper bound on how long that
 * takes. Silence for this long means the transfer really has stopped.
 */
#define UPLOAD_IDLE_TIMEOUT_MS 20000

static const char approved_mlo_sha256[] =
	"7d272c4653c978eb3fe8ddc23a7f12216ab0ae17a42594c937ce4d91f323271b";
static const char approved_uart_mlo_sha256[] =
	"9c235f3b591717d3cb90750dd8929d68625186d40fce34b6619802ff136d3b69";
/*
 * Mainline 2026.07 USB SPL, built by os/bootloader/build-wla-uboot.sh.
 * Pin exact reviewed build bytes before ROM transport.
 */
static const char approved_usb_spl_sha256[] =
	"b012182f304bfc0bbb155b43ce9c54f5a63f0f5db4fc6a7f9d245db84fb5345e";
/*
 * The Wireless Link's own stock MLO, from the hash-pinned WLA package, and
 * verified byte-identical to what NAND holds on the board it is served to. Bose
 * MLO knows NAND; chain-loading it over USB is how a cabled board can still boot
 * what is installed in flash, since the ROM always prefers USB while the cable
 * is in. Selected with --stock-wla.
 */
static const char approved_wla_mlo_sha256[] =
	"d49f1081a4066f281775ffc2b5a25c792105b982c606a366c0382bba30d20a7f";
static const uint8_t host_mac[6] = { 0x02, 0x00, 0x00, 0x00, 0x00, 0x01 };
static const uint8_t server_ip[4] = { 192, 168, 2, 1 };
static const uint8_t client_ip[4] = { 192, 168, 2, 2 };
static const uint8_t broadcast_ip[4] = { 255, 255, 255, 255 };
static const uint8_t broadcast_mac[6] = { 0xff, 0xff, 0xff, 0xff, 0xff, 0xff };

struct image {
	const char *name;
	uint8_t *data;
	size_t size;
};

/*
 * One extra file, served only while watching netconsole. U-Boot cannot fetch
 * anything after the SPL hands off -- watch mode answered ARP and nothing else --
 * so an install bootcmd had no way to get an image into RAM. Hash-pinned and
 * name-pinned like every other artifact this loader touches.
 */
/*
 * How long a bulk write to the board may take. The stage peers -- ROM and SPL --
 * always have an OUT buffer queued, so 120 ms is generous there. U-Boot's own
 * gadget re-arms more lazily, and a 602-byte TFTP data block times out at that
 * figure every time while an 86-byte ARP reply goes straight through.
 */
static unsigned rndis_write_timeout_ms = USB_BULK_TIMEOUT_MS;

static struct image watch_file;
static bool have_watch_file;
/* Where an uploaded file lands, if uploads were enabled at all. NULL means refuse. */
static const char *upload_dir;


struct usb_path {
	uint8_t bus;
	uint8_t ports[8];
	int depth;
};

struct hotplug_session {
	libusb_context *ctx;
	libusb_hotplug_callback_handle callback;
	libusb_device *device;
	struct usb_path path;
	const struct usb_path *expected_path;
	uint8_t previous_address;
	bool callback_registered;
	bool departed;
	bool rejected;
	unsigned arrivals;
	unsigned number;
};

struct rndis_device {
	libusb_device_handle *handle;
	libusb_context *ctx;
	uint8_t status_ep;
	uint8_t bulk_in;
	uint8_t bulk_out;
	uint8_t control_if;
	uint8_t data_if;
	uint8_t data_alt;
	uint32_t request_id;
};

struct notification {
	bool complete;
	enum libusb_transfer_status status;
	int actual_length;
	uint8_t bytes[64];
};

static uint64_t started_ms;
static const char *log_stage = "startup";
static unsigned log_session;
static bool serialized_usbip;
static bool same_session_usbip;
static bool spl_only_usbip;
static bool rom_only_usbip;
static bool uart_handoff;
static bool usb_spl;
static bool stock_wla;
/* Stale 68ff… U-Boot uses SPL's 6141 PID and two configurations. */
static bool legacy_uboot;
static unsigned netconsole_seconds;
static unsigned serve_rounds = 1;
static uint16_t expected_usb_pid = AM335X_USB_PID;
static bool stage_two;
static bool accept_any_filename;
static bool fit_payload;
static bool capture_payload;
static uint64_t device_wait_ms = DEVICE_WAIT_MS;
static bool stage_three_rejection_seen;
static uint8_t stage_three_rejection_bus;
static uint8_t stage_three_rejection_address;
static uint16_t stage_three_rejection_vid;
static uint16_t stage_three_rejection_pid;

static bool
uses_spl_rndis_notification(void)
{
	return stage_two || log_session == 3;
}

static bool
tftp_filename_allowed(const char *requested, const struct image *image)
{
	return accept_any_filename || strcmp(requested, image->name) == 0;
}

static uint16_t
stage_usb_pid(unsigned number)
{
	return number == 3 && !legacy_uboot ? UBOOT_USB_PID : AM335X_USB_PID;
}

static bool
stage_device_matches(unsigned number, uint8_t previous_address,
	uint8_t address, uint16_t vendor, uint16_t product)
{
	return vendor == TI_VID && product == stage_usb_pid(number) &&
	    (number != 2 || address != previous_address);
}

static bool
stage_accepts_configurations(unsigned number, uint8_t configurations)
{
	return configurations == 1 ||
	    (configurations == 2 && (number == 2 || number == 3));
}

static bool
needs_rom_departure(bool usb_spl_mode)
{
	return !usb_spl_mode;
}

static uint64_t
now_ms(void)
{
	struct timespec ts;

	(void)clock_gettime(CLOCK_MONOTONIC, &ts);
	return (uint64_t)ts.tv_sec * 1000 + (uint64_t)ts.tv_nsec / 1000000;
}

static void
log_line(const char *format, ...)
{
	va_list ap;

	fprintf(stderr, "[%6llums] stage=%s session=%u ",
	    (unsigned long long)(now_ms() - started_ms), log_stage, log_session);
	va_start(ap, format);
	vfprintf(stderr, format, ap);
	va_end(ap);
	fputc('\n', stderr);
}

static void
put_le32(uint8_t *p, uint32_t value)
{
	p[0] = (uint8_t)value;
	p[1] = (uint8_t)(value >> 8);
	p[2] = (uint8_t)(value >> 16);
	p[3] = (uint8_t)(value >> 24);
}

static uint32_t
get_le32(const uint8_t *p)
{
	return (uint32_t)p[0] | (uint32_t)p[1] << 8 |
	    (uint32_t)p[2] << 16 | (uint32_t)p[3] << 24;
}

static bool
sha256(const uint8_t *data, size_t size, uint8_t digest[32])
{
#ifdef __APPLE__
	if (size > UINT32_MAX)
		return false;
	return CC_SHA256(data, (CC_LONG)size, digest) != NULL;
#else
	EVP_MD_CTX *context;
	unsigned int digest_size = 0;
	bool ok;

	context = EVP_MD_CTX_new();
	if (context == NULL)
		return false;
	ok = EVP_DigestInit_ex(context, EVP_sha256(), NULL) == 1 &&
	    EVP_DigestUpdate(context, data, size) == 1 &&
	    EVP_DigestFinal_ex(context, digest, &digest_size) == 1 &&
	    digest_size == 32;
	EVP_MD_CTX_free(context);
	return ok;
#endif
}

static bool
patch_uart_boot_device(struct image *mlo)
{
	static const size_t offset = 0x1678;
	static const uint8_t instruction[] = { 0x41, 0x00, 0x54, 0xe3 };
	uint8_t actual[32], expected[32];

	if (mlo->size < offset + sizeof(instruction) ||
	    memcmp(mlo->data + offset, instruction, sizeof(instruction)) != 0 ||
	    !parse_sha256(approved_uart_mlo_sha256, expected)) {
		log_line("stock MLO UART boot-device instruction mismatch");
		return false;
	}
	mlo->data[offset] = 0x44; /* BOOT_DEVICE_UART -> BOOT_DEVICE_USBETH */
	if (!sha256(mlo->data, mlo->size, actual) ||
	    memcmp(actual, expected, sizeof(actual)) != 0) {
		log_line("patched UART MLO SHA-256 mismatch");
		return false;
	}
	log_line("verified RAM-only UART MLO patch offset=0x%zx sha256=%s",
	    offset, approved_uart_mlo_sha256);
	return true;
}

static bool
same_path(const struct usb_path *a, const struct usb_path *b)
{
	return a->bus == b->bus && a->depth == b->depth &&
	    memcmp(a->ports, b->ports, (size_t)a->depth) == 0;
}

static bool
device_path(libusb_device *device, struct usb_path *path)
{
	int depth;

	memset(path, 0, sizeof(*path));
	path->bus = libusb_get_bus_number(device);
	depth = libusb_get_port_numbers(device, path->ports,
	    (int)sizeof(path->ports));
	if (depth <= 0 || depth > (int)sizeof(path->ports))
		return false;
	path->depth = depth;
	return true;
}

static void
format_path(const struct usb_path *path, char *out, size_t capacity)
{
	size_t used;
	int i;

	used = (size_t)snprintf(out, capacity, "%u", path->bus);
	for (i = 0; i < path->depth && used < capacity; i++)
		used += (size_t)snprintf(out + used, capacity - used, ".%u",
		    path->ports[i]);
}

static int LIBUSB_CALL
hotplug_callback(libusb_context *ctx, libusb_device *device,
	libusb_hotplug_event event, void *user_data)
{
	struct hotplug_session *session = user_data;
	struct usb_path path;

	(void)ctx;
	if (!device_path(device, &path)) {
		session->rejected = true;
		return 0;
	}
	if (event == LIBUSB_HOTPLUG_EVENT_DEVICE_LEFT) {
		if (session->device == device)
			session->departed = true;
		return 0;
	}
	if (session->number == 2 &&
	    libusb_get_device_address(device) == session->previous_address)
		return 0;
	session->arrivals++;
	if (accept_any_filename)
		log_line("hotplug arrival %u path=%u.%u.%u", session->arrivals,
		    path.bus, path.ports[0], path.ports[1]);
	if (session->arrivals > 1 || session->device != NULL ||
	    (session->expected_path != NULL &&
	    !same_path(&path, session->expected_path))) {
		session->rejected = true;
		return 0;
	}
	session->path = path;
	session->device = libusb_ref_device(device);
	return 0;
}

static bool
pump_events_until(struct hotplug_session *session, bool wait_for_departure,
	uint64_t deadline)
{
	while (!(wait_for_departure ? session->departed : session->device != NULL) &&
	    !session->rejected && now_ms() < deadline) {
		struct timeval timeout = { .tv_sec = 0, .tv_usec = 10000 };
		int rc = libusb_handle_events_timeout(session->ctx, &timeout);
		if (rc != LIBUSB_SUCCESS && rc != LIBUSB_ERROR_INTERRUPTED) {
			log_line("libusb event error: %s", libusb_error_name(rc));
			return false;
		}
	}
	return (wait_for_departure ? session->departed : session->device != NULL) &&
	    !session->rejected;
}

/*
 * libusb's hotplug stops reporting arrivals in a third context in the
 * same process, and U-Boot's gadget appears while its predecessor's node is
 * still settling. Scanning the device list once is enough here: the device is
 * already on the bus by the time we look.
 */
static bool
session_attach_present(struct hotplug_session *session, unsigned number,
	const struct usb_path *expected_path, uint8_t previous_address)
{
	libusb_device **list = NULL;
	ssize_t count, i;
	int rc;

	memset(session, 0, sizeof(*session));
	session->number = number;
	stage_two = number == 2;
	expected_usb_pid = stage_usb_pid(number);
	session->expected_path = expected_path;
	session->previous_address = previous_address;
	log_session = number;
	rc = libusb_init(&session->ctx);
	if (rc != LIBUSB_SUCCESS) {
		log_line("libusb init failed: %s", libusb_error_name(rc));
		return false;
	}
	count = libusb_get_device_list(session->ctx, &list);
	if (count < 0) {
		log_line("device list failed: %s", libusb_error_name((int)count));
		return false;
	}
	for (i = 0; i < count; i++) {
		struct libusb_device_descriptor descriptor;
		struct usb_path path;

		if (libusb_get_device_descriptor(list[i], &descriptor) != LIBUSB_SUCCESS ||
		    !device_path(list[i], &path))
			continue;
		if (number == 3 && expected_path != NULL &&
		    same_path(&path, expected_path) &&
		    (descriptor.idVendor != TI_VID ||
		    descriptor.idProduct != expected_usb_pid) &&
		    (!stage_three_rejection_seen ||
		    stage_three_rejection_bus != path.bus ||
		    stage_three_rejection_address != libusb_get_device_address(list[i]) ||
		    stage_three_rejection_vid != descriptor.idVendor ||
		    stage_three_rejection_pid != descriptor.idProduct)) {
			log_line("rejected USB device %04x:%04x on expected path before %04x:%04x matcher",
			    descriptor.idVendor, descriptor.idProduct, TI_VID,
			    expected_usb_pid);
			stage_three_rejection_seen = true;
			stage_three_rejection_bus = path.bus;
			stage_three_rejection_address = libusb_get_device_address(list[i]);
			stage_three_rejection_vid = descriptor.idVendor;
			stage_three_rejection_pid = descriptor.idProduct;
		}
		if (!stage_device_matches(number, previous_address,
		    libusb_get_device_address(list[i]), descriptor.idVendor,
		    descriptor.idProduct) ||
		    (expected_path != NULL && !same_path(&path, expected_path)))
			continue;
		session->path = path;
		session->device = libusb_ref_device(list[i]);
		break;
	}
	libusb_free_device_list(list, 1);
	if (session->device == NULL) {
		log_line("no %04x:%04x on the expected port yet", TI_VID,
		    expected_usb_pid);
		return false;
	}
	return true;
}

static bool
session_start(struct hotplug_session *session, unsigned number,
	const struct usb_path *expected_path, uint8_t previous_address)
{
	int rc;
	char path_text[40];

	memset(session, 0, sizeof(*session));
	session->number = number;
	stage_two = number == 2;
	expected_usb_pid = stage_usb_pid(number);
	session->expected_path = expected_path;
	session->previous_address = previous_address;
	log_session = number;
	rc = libusb_init(&session->ctx);
	if (rc != LIBUSB_SUCCESS) {
		log_line("libusb init failed: %s", libusb_error_name(rc));
		return false;
	}
	if (!libusb_has_capability(LIBUSB_CAP_HAS_HOTPLUG)) {
		log_line("libusb hotplug capability is required");
		return false;
	}
	rc = libusb_hotplug_register_callback(session->ctx,
	    LIBUSB_HOTPLUG_EVENT_DEVICE_ARRIVED | LIBUSB_HOTPLUG_EVENT_DEVICE_LEFT,
	    LIBUSB_HOTPLUG_ENUMERATE, TI_VID, expected_usb_pid,
	    LIBUSB_HOTPLUG_MATCH_ANY, hotplug_callback, session,
	    &session->callback);
	if (rc != LIBUSB_SUCCESS) {
		log_line("hotplug registration failed: %s", libusb_error_name(rc));
		return false;
	}
	session->callback_registered = true;
	if (!pump_events_until(session, false, now_ms() + device_wait_ms)) {
		log_line(session->rejected ?
		    "rejected multiple devices or physical path change" :
		    "timed out waiting for expected TI USB generation");
		return false;
	}
	format_path(&session->path, path_text, sizeof(path_text));
	log_line("device arrived path=%s", path_text);
	return true;
}

static void
session_stop(struct hotplug_session *session)
{
	if (session->callback_registered)
		libusb_hotplug_deregister_callback(session->ctx, session->callback);
	if (session->device != NULL)
		libusb_unref_device(session->device);
	if (session->ctx != NULL)
		libusb_exit(session->ctx);
	memset(session, 0, sizeof(*session));
}

static bool
read_image(const char *path, const char *required_name, size_t maximum,
	const uint8_t expected_hash[32], struct image *image)
{
	struct stat st;
	uint8_t actual_hash[32];
	char actual_text[65];
	const char *base;
	int fd;
	ssize_t count;
	size_t offset = 0;

	base = strrchr(path, '/');
	base = base == NULL ? path : base + 1;
	if (strcmp(base, required_name) != 0) {
		log_line("artifact basename must be exactly %s", required_name);
		return false;
	}
	fd = open(path, O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
	if (fd < 0) {
		log_line("cannot open %s: %s", required_name, strerror(errno));
		return false;
	}
	if (fstat(fd, &st) != 0 || !S_ISREG(st.st_mode) || st.st_size <= 0 ||
	    (uint64_t)st.st_size > maximum) {
		log_line("%s must be a nonempty regular file no larger than %zu bytes",
		    required_name, maximum);
		close(fd);
		return false;
	}
	image->data = malloc((size_t)st.st_size);
	if (image->data == NULL) {
		log_line("out of memory reading %s", required_name);
		close(fd);
		return false;
	}
	image->name = required_name;
	image->size = (size_t)st.st_size;
	while (offset < image->size) {
		count = read(fd, image->data + offset, image->size - offset);
		if (count <= 0) {
			log_line("failed reading %s: %s", required_name,
			    count == 0 ? "unexpected EOF" : strerror(errno));
			close(fd);
			free(image->data);
			image->data = NULL;
			return false;
		}
		offset += (size_t)count;
	}
	close(fd);
	if (!sha256(image->data, image->size, actual_hash)) {
		log_line("failed hashing %s", required_name);
		free(image->data);
		image->data = NULL;
		return false;
	}
	if (memcmp(actual_hash, expected_hash, sizeof(actual_hash)) != 0) {
		format_sha256(actual_hash, actual_text);
		log_line("%s SHA-256 mismatch: got %s", required_name, actual_text);
		free(image->data);
		image->data = NULL;
		return false;
	}
	format_sha256(actual_hash, actual_text);
	log_line("verified %s size=%zu sha256=%s", required_name, image->size,
	    actual_text);
	return true;
}

static bool
parse_rndis_configuration(const struct libusb_config_descriptor *config,
	struct rndis_device *rndis)
{
	bool found_control = false, found_data = false;
	bool found_union = false;
	uint8_t union_master = 0, union_slave = 0;
	int i, j, k;

	if (config->bNumInterfaces < 2)
		return false;
	for (i = 0; i < config->bNumInterfaces; i++) {
		const struct libusb_interface *iface = &config->interface[i];
		for (j = 0; j < iface->num_altsetting; j++) {
			const struct libusb_interface_descriptor *alt =
			    &iface->altsetting[j];
			if (alt->bInterfaceClass == 0x02 &&
			    alt->bInterfaceSubClass == 0x02 &&
			    alt->bInterfaceProtocol == 0xff) {
				if (found_control || alt->bNumEndpoints != 1 ||
				    (alt->endpoint[0].bmAttributes & 3) !=
				    LIBUSB_TRANSFER_TYPE_INTERRUPT ||
				    (alt->endpoint[0].bEndpointAddress & LIBUSB_ENDPOINT_DIR_MASK)
				    != LIBUSB_ENDPOINT_IN ||
				    (alt->endpoint[0].wMaxPacketSize != 8 &&
				    alt->endpoint[0].wMaxPacketSize != 16)) {
					return false;
				}
				for (k = 0; k < alt->extra_length;) {
					int descriptor_len = alt->extra[k];

					if (alt->extra_length - k < 3 || descriptor_len < 3 ||
					    descriptor_len >
					    alt->extra_length - k)
						return false;
					if (alt->extra[k + 1] == 0x24 &&
					    alt->extra[k + 2] == 0x06) {
						if (found_union || descriptor_len != 5)
							return false;
						found_union = true;
						union_master = alt->extra[k + 3];
						union_slave = alt->extra[k + 4];
					}
					k += descriptor_len;
				}
				found_control = true;
				rndis->control_if = alt->bInterfaceNumber;
				rndis->status_ep = alt->endpoint[0].bEndpointAddress;
			}
		}
	}
	if (!found_control || !found_union ||
	    union_master != rndis->control_if)
		return false;
	for (i = 0; i < config->bNumInterfaces; i++) {
		const struct libusb_interface *iface = &config->interface[i];
		for (j = 0; j < iface->num_altsetting; j++) {
			const struct libusb_interface_descriptor *alt =
			    &iface->altsetting[j];
			if (alt->bInterfaceNumber == union_slave &&
			    alt->bInterfaceClass == 0x0a &&
			    alt->bInterfaceSubClass == 0 &&
			    alt->bInterfaceProtocol == 0 &&
			    alt->bNumEndpoints == 2) {
				uint8_t in = 0, out = 0;
				for (k = 0; k < 2; k++) {
					const struct libusb_endpoint_descriptor *ep =
					    &alt->endpoint[k];
					if ((ep->bmAttributes & 3) != LIBUSB_TRANSFER_TYPE_BULK ||
					    (ep->wMaxPacketSize != 64 &&
					    ep->wMaxPacketSize != 512))
						return false;
					if (ep->bEndpointAddress & LIBUSB_ENDPOINT_IN)
						in = ep->bEndpointAddress;
					else
						out = ep->bEndpointAddress;
				}
				if (found_data || in == 0 || out == 0) {
					return false;
				}
				found_data = true;
				rndis->data_if = alt->bInterfaceNumber;
				rndis->data_alt = alt->bAlternateSetting;
				rndis->bulk_in = in;
				rndis->bulk_out = out;
			}
		}
	}
	if (!found_data ||
	    rndis->control_if == rndis->data_if ||
	    union_slave != rndis->data_if) {
		return false;
	}
	return true;
}

static bool
has_unique_rndis_configuration(unsigned matches)
{
	return matches == 1;
}

static void
format_rndis_descriptor(char *text, size_t size,
	const struct libusb_config_descriptor *config, uint8_t index)
{
	size_t used = 0;
	int i, j, k;

#define APPEND(...) do { \
	if (used < size) { \
		int written = snprintf(text + used, size - used, __VA_ARGS__); \
		if (written > 0) \
			used += (size_t)written < size - used ? (size_t)written : size - used; \
	} \
} while (0)
	APPEND("config[%u] value=%u interfaces=%u attributes=%02x max-power=%u",
	    index, config->bConfigurationValue, config->bNumInterfaces,
	    config->bmAttributes, config->MaxPower);
	for (i = 0; i < config->bNumInterfaces; i++) {
		const struct libusb_interface *iface = &config->interface[i];

		for (j = 0; j < iface->num_altsetting; j++) {
			const struct libusb_interface_descriptor *alt =
			    &iface->altsetting[j];

			APPEND("; if=%u alt=%u class=%02x subclass=%02x protocol=%02x endpoints=%u extra=%d",
			    alt->bInterfaceNumber, alt->bAlternateSetting,
			    alt->bInterfaceClass, alt->bInterfaceSubClass,
			    alt->bInterfaceProtocol, alt->bNumEndpoints, alt->extra_length);
			for (k = 0; k < alt->bNumEndpoints; k++)
				APPEND(" ep=%02x/%02x/%u", alt->endpoint[k].bEndpointAddress,
				    alt->endpoint[k].bmAttributes,
				    alt->endpoint[k].wMaxPacketSize);
			for (k = 0; k < alt->extra_length; k++)
				APPEND("%s%02x", k == 0 ? " raw=" : "",
				    alt->extra[k]);
			for (k = 0; k + 5 <= alt->extra_length; k += alt->extra[k]) {
				if (alt->extra[k] < 3)
					break;
				if (alt->extra[k] == 5 && alt->extra[k + 1] == 0x24 &&
				    alt->extra[k + 2] == 0x06)
					APPEND(" union=%u:%u", alt->extra[k + 3], alt->extra[k + 4]);
			}
		}
	}
#undef APPEND
}

static void
log_rndis_descriptor_diagnostics(libusb_device *device, uint8_t configurations)
{
	struct libusb_config_descriptor *config;
	char text[1024];
	uint8_t i;

	for (i = 0; i < configurations; i++) {
		config = NULL;
		if (libusb_get_config_descriptor(device, i, &config) != LIBUSB_SUCCESS ||
		    config == NULL)
			continue;
		format_rndis_descriptor(text, sizeof(text), config, i);
		log_line("RNDIS descriptor rejection: %s", text);
		libusb_free_config_descriptor(config);
	}
}

static bool
parse_rndis_descriptors(libusb_device *device, struct rndis_device *rndis,
	uint8_t *configuration)
{
	struct libusb_device_descriptor descriptor;
	struct libusb_config_descriptor *config;
	struct rndis_device candidate;
	unsigned matches = 0;
	uint8_t i;

	if (libusb_get_device_descriptor(device, &descriptor) != LIBUSB_SUCCESS ||
	    descriptor.idVendor != TI_VID || descriptor.idProduct != expected_usb_pid ||
	    !stage_accepts_configurations(log_session, descriptor.bNumConfigurations) ||
	    (legacy_uboot && log_session == 3 && descriptor.bNumConfigurations != 2)) {
		log_line("device descriptor is not the expected AM335x RNDIS device");
		return false;
	}
	for (i = 0; i < descriptor.bNumConfigurations; i++) {
		config = NULL;
		if (libusb_get_config_descriptor(device, i, &config) != LIBUSB_SUCCESS ||
		    config == NULL)
			continue;
		/* USB permits empty configurations; they cannot be RNDIS candidates. */
		if (config->bNumInterfaces == 0) {
			libusb_free_config_descriptor(config);
			continue;
		}
		memset(&candidate, 0, sizeof(candidate));
		if (parse_rndis_configuration(config, &candidate)) {
			*rndis = candidate;
			*configuration = config->bConfigurationValue;
			matches++;
		}
		libusb_free_config_descriptor(config);
	}
	if (!has_unique_rndis_configuration(matches)) {
		log_line("device must expose exactly one two-interface RNDIS configuration");
		log_rndis_descriptor_diagnostics(device, descriptor.bNumConfigurations);
		return false;
	}
	return true;
}

static bool
rndis_open(struct hotplug_session *session, struct rndis_device *rndis)
{
	uint8_t configuration;
	int active = 0, rc;

	memset(rndis, 0, sizeof(*rndis));
	rndis->ctx = session->ctx;
	if (!parse_rndis_descriptors(session->device, rndis, &configuration))
		return false;
	rc = libusb_open(session->device, &rndis->handle);
	if (rc != LIBUSB_SUCCESS) {
		log_line("cannot open USB device: %s", libusb_error_name(rc));
		if (rc == LIBUSB_ERROR_ACCESS)
			log_line("install tools/recovery/rom-usb-loader/70-soundtap-rom-usb.rules and reload udev rules");
		return false;
	}
	log_line("USB handle opened");
	rc = libusb_set_auto_detach_kernel_driver(rndis->handle, 1);
	if (rc != LIBUSB_SUCCESS && rc != LIBUSB_ERROR_NOT_SUPPORTED) {
		log_line("cannot enable kernel-driver detach: %s", libusb_error_name(rc));
		return false;
	}
	if (libusb_get_configuration(rndis->handle, &active) != LIBUSB_SUCCESS ||
	    (active != configuration &&
	    libusb_set_configuration(rndis->handle, configuration) != LIBUSB_SUCCESS)) {
		log_line("cannot select exact RNDIS configuration %u", configuration);
		return false;
	}
	log_line("RNDIS configuration %u active", configuration);
	if (libusb_claim_interface(rndis->handle, rndis->control_if) !=
	    LIBUSB_SUCCESS || libusb_claim_interface(rndis->handle, rndis->data_if) !=
	    LIBUSB_SUCCESS || (rndis->data_alt != 0 &&
	    libusb_set_interface_alt_setting(rndis->handle, rndis->data_if,
	    rndis->data_alt) != LIBUSB_SUCCESS)) {
		log_line("cannot claim exact RNDIS interfaces");
		return false;
	}
	log_line("RNDIS endpoints control-if=%u data-if=%u status=0x%02x in=0x%02x out=0x%02x",
	    rndis->control_if, rndis->data_if, rndis->status_ep,
	    rndis->bulk_in, rndis->bulk_out);
	return true;
}

static void
rndis_close(struct rndis_device *rndis)
{
	if (rndis->handle != NULL) {
		(void)libusb_release_interface(rndis->handle, rndis->data_if);
		(void)libusb_release_interface(rndis->handle, rndis->control_if);
		libusb_close(rndis->handle);
	}
	memset(rndis, 0, sizeof(*rndis));
}

static void LIBUSB_CALL
notification_complete(struct libusb_transfer *transfer)
{
	struct notification *notification = transfer->user_data;

	notification->status = transfer->status;
	notification->actual_length = transfer->actual_length;
	notification->complete = true;
}

static bool
valid_rndis_notification(const struct rndis_device *rndis,
	const struct notification *notification)
{
	bool cdc_notification;
	bool spl_notification;

	/* libusb API enum: status 6 is overflow, not a valid RNDIS notification. */
	cdc_notification = notification->status == LIBUSB_TRANSFER_COMPLETED &&
	    notification->actual_length == 8 && notification->bytes[0] == 0xa1 &&
	    notification->bytes[1] == 0x01 && notification->bytes[2] == 0 &&
	    notification->bytes[3] == 0 &&
	    notification->bytes[4] == rndis->control_if &&
	    notification->bytes[5] == 0 && notification->bytes[6] == 0 &&
	    notification->bytes[7] == 0;
	spl_notification = uses_spl_rndis_notification() &&
	    notification->status == LIBUSB_TRANSFER_COMPLETED &&
	    notification->actual_length == 8 && notification->bytes[0] == 1 &&
	    notification->bytes[1] == 0 && notification->bytes[2] == 0 &&
	    notification->bytes[3] == 0 && notification->bytes[4] == 0 &&
	    notification->bytes[5] == 0 && notification->bytes[6] == 0 &&
	    notification->bytes[7] == 0;
	return cdc_notification || spl_notification;
}

static bool
rndis_command(struct rndis_device *rndis, const uint8_t *command,
	size_t command_len, uint8_t *response, size_t response_capacity,
	int *response_len)
{
	struct notification notification = {0};
	struct libusb_transfer *transfer;
	uint64_t deadline;
	bool submitted = false;
	int rc;

	transfer = libusb_alloc_transfer(0);
	if (transfer == NULL)
		return false;
	libusb_fill_interrupt_transfer(transfer, rndis->handle, rndis->status_ep,
	    notification.bytes, sizeof(notification.bytes), notification_complete,
	    &notification, USB_COMMAND_TIMEOUT_MS);
	if (!serialized_usbip) {
		rc = libusb_submit_transfer(transfer);
		if (rc != LIBUSB_SUCCESS) {
			log_line("cannot arm RNDIS status endpoint: %s", libusb_error_name(rc));
			libusb_free_transfer(transfer);
			return false;
		}
		submitted = true;
	}
	rc = libusb_control_transfer(rndis->handle, 0x21, 0x00, 0,
	    rndis->control_if, (unsigned char *)(uintptr_t)command,
	    (uint16_t)command_len, USB_COMMAND_TIMEOUT_MS);
	if (serialized_usbip && rc == (int)command_len) {
		int submit_rc = libusb_submit_transfer(transfer);
		if (submit_rc != LIBUSB_SUCCESS) {
			log_line("cannot read queued RNDIS status: %s",
			    libusb_error_name(submit_rc));
			libusb_free_transfer(transfer);
			return false;
		}
		submitted = true;
	}
	if (rc != (int)command_len) {
		log_line("RNDIS command send failed: %s", rc < 0 ?
		    libusb_error_name(rc) : "short control transfer");
		if (!submitted) {
			libusb_free_transfer(transfer);
			return false;
		}
		(void)libusb_cancel_transfer(transfer);
	}
	deadline = now_ms() + USB_COMMAND_TIMEOUT_MS;
	while (!notification.complete && now_ms() < deadline) {
		struct timeval timeout = { .tv_sec = 0, .tv_usec = 5000 };
		(void)libusb_handle_events_timeout(rndis->ctx, &timeout);
	}
	if (!notification.complete) {
		uint64_t cancel_deadline = now_ms() + 100;

		(void)libusb_cancel_transfer(transfer);
		while (!notification.complete && now_ms() < cancel_deadline) {
			struct timeval timeout = { .tv_sec = 0, .tv_usec = 5000 };
			(void)libusb_handle_events_timeout(rndis->ctx, &timeout);
		}
		if (!notification.complete) {
			log_line("RNDIS status cancellation did not complete within 100ms; terminating safely");
			_exit(EXIT_FAILURE);
		}
	}
	if (rc != (int)command_len ||
	    !valid_rndis_notification(rndis, &notification)) {
		if (!(serialized_usbip && strcmp(log_stage, "ROM") == 0 &&
		    rc == (int)command_len)) {
			log_line("invalid RNDIS notification send=%d status=%d length=%d bytes=%02x%02x%02x%02x%02x%02x%02x%02x:%02x%02x%02x%02x%02x%02x%02x%02x",
			    rc, notification.status, notification.actual_length,
			    notification.bytes[0], notification.bytes[1],
			    notification.bytes[2], notification.bytes[3],
			    notification.bytes[4], notification.bytes[5],
			    notification.bytes[6], notification.bytes[7],
			    notification.bytes[8], notification.bytes[9],
			    notification.bytes[10], notification.bytes[11],
			    notification.bytes[12], notification.bytes[13],
			    notification.bytes[14], notification.bytes[15]);
			libusb_free_transfer(transfer);
			return false;
		}
		log_line("ROM omitted status notification; polling encapsulated response");
	}
	libusb_free_transfer(transfer);
	/* GET is intentionally issued only after the async notification is consumed. */
	rc = libusb_control_transfer(rndis->handle, 0xa1, 0x01, 0,
	    rndis->control_if, response, (uint16_t)response_capacity,
	    USB_COMMAND_TIMEOUT_MS);
	if (rc <= 0) {
		log_line("RNDIS response GET failed: %s", libusb_error_name(rc));
		return false;
	}
	*response_len = rc;
	return true;
}

static bool
rndis_initialize(struct rndis_device *rndis)
{
	uint8_t command[32] = {0}, response[128] = {0};
	uint32_t request_id;
	int length;

	request_id = ++rndis->request_id;
	put_le32(command, 2);
	put_le32(command + 4, 24);
	put_le32(command + 8, request_id);
	put_le32(command + 12, 1);
	put_le32(command + 16, 0);
	put_le32(command + 20, RNDIS_MAX_TRANSFER);
	if (!rndis_command(rndis, command, 24, response, sizeof(response), &length) ||
	    length < 52 || get_le32(response) != 0x80000002 ||
	    get_le32(response + 4) < 52 || get_le32(response + 4) > (uint32_t)length ||
	    get_le32(response + 8) != request_id || get_le32(response + 12) != 0 ||
	    get_le32(response + 28) != 0 || get_le32(response + 32) < 1 ||
	    get_le32(response + 36) < 576) {
		log_line("invalid RNDIS initialize completion");
		return false;
	}
	memset(command, 0, sizeof(command));
	request_id = ++rndis->request_id;
	put_le32(command, 5);
	put_le32(command + 4, 32);
	put_le32(command + 8, request_id);
	put_le32(command + 12, 0x0001010e); /* OID_GEN_CURRENT_PACKET_FILTER */
	put_le32(command + 16, 4);
	put_le32(command + 20, 20);
	put_le32(command + 28, 0x0f);
	if (!rndis_command(rndis, command, 32, response, sizeof(response), &length) ||
	    length < 16 || get_le32(response) != 0x80000005 ||
	    get_le32(response + 4) != 16 ||
	    get_le32(response + 8) != request_id || get_le32(response + 12) != 0) {
		log_line("invalid RNDIS packet-filter completion");
		return false;
	}
	log_line("RNDIS initialized packet-filter=0x0f");
	return true;
}

static bool
rndis_send_ethernet(struct rndis_device *rndis, const uint8_t *packet,
	size_t packet_len)
{
	uint8_t transfer[44 + 1600] = {0};
	int actual, rc;

	if (packet_len > sizeof(transfer) - 44)
		return false;
	put_le32(transfer, 1);
	put_le32(transfer + 4, (uint32_t)(44 + packet_len));
	put_le32(transfer + 8, 36);
	put_le32(transfer + 12, (uint32_t)packet_len);
	memcpy(transfer + 44, packet, packet_len);
	rc = libusb_bulk_transfer(rndis->handle, rndis->bulk_out, transfer,
	    (int)(44 + packet_len), &actual, rndis_write_timeout_ms);
	if (rc != LIBUSB_SUCCESS || actual != (int)(44 + packet_len)) {
		log_line("RNDIS bulk write failed: %s", libusb_error_name(rc));
		return false;
	}
	return true;
}

static int
rndis_receive_ethernet(struct rndis_device *rndis, uint8_t *packet,
	size_t capacity, unsigned timeout_ms)
{
	uint8_t transfer[RNDIS_MAX_TRANSFER];
	uint32_t message_len, data_offset, data_len;
	int actual, rc;

	rc = libusb_bulk_transfer(rndis->handle, rndis->bulk_in, transfer,
	    sizeof(transfer), &actual, timeout_ms);
	if (rc == LIBUSB_ERROR_TIMEOUT)
		return 0;
	if (rc != LIBUSB_SUCCESS) {
		log_line("RNDIS bulk read failed: %s", libusb_error_name(rc));
		return -1;
	}
	if (actual < 44 || get_le32(transfer) != 1)
		return -1;
	message_len = get_le32(transfer + 4);
	data_offset = get_le32(transfer + 8);
	data_len = get_le32(transfer + 12);
	if (message_len > (uint32_t)actual || message_len < 44 ||
	    data_offset > message_len - 8 || data_len > message_len - 8 - data_offset ||
	    data_len > capacity)
		return -1;
	memcpy(packet, transfer + 8 + data_offset, data_len);
	return (int)data_len;
}

static bool
send_udp(struct rndis_device *rndis, const uint8_t dst_mac[6],
	const uint8_t src_ip[4], const uint8_t dst_ip[4], uint16_t src_port,
	uint16_t dst_port, const uint8_t *payload, size_t payload_len,
	uint16_t *ip_id)
{
	uint8_t frame[1600];
	size_t frame_len = build_udp_frame(frame, sizeof(frame), dst_mac, host_mac,
	    src_ip, dst_ip, src_port, dst_port, payload, payload_len, (*ip_id)++);
	return frame_len != 0 && rndis_send_ethernet(rndis, frame, frame_len);
}

static bool
serve_image(struct rndis_device *rndis, const struct image *image)
{
	uint8_t packet[1600], payload[600], peer_mac[6] = {0};
	struct udp_frame frame;
	struct bootp_request request;
	uint16_t ip_id = 1, client_port = 0, block = 1, ack;
	uint64_t request_deadline = now_ms() + REQUEST_DEADLINE_MS;
	uint64_t transfer_deadline;
	size_t offset = 0, chunk, payload_len;
	unsigned retries = 0;
	bool have_peer = false, rrq_received = false;
	char filename[128];

	while (!rrq_received && now_ms() < request_deadline) {
		int length = rndis_receive_ethernet(rndis, packet, sizeof(packet),
		    USB_BULK_TIMEOUT_MS);
		struct arp_request arp;
		size_t arp_reply_len;

		if (length < 0)
			return false;
		if (length == 0)
			continue;
		if (parse_arp_request(packet, (size_t)length, &arp)) {
			if (!have_peer || memcmp(peer_mac, arp.sender_mac, 6) != 0 ||
			    memcmp(arp.sender_ip, client_ip, 4) != 0 ||
			    memcmp(arp.target_ip, server_ip, 4) != 0)
				return false;
			arp_reply_len = build_arp_reply(packet, sizeof(packet), &arp,
			    host_mac, server_ip);
			if (arp_reply_len == 0 ||
			    !rndis_send_ethernet(rndis, packet, arp_reply_len))
				return false;
			log_line("ARP reply for boot server");
			continue;
		}
		if (!parse_udp_frame(packet, (size_t)length, &frame))
			continue;
		if (frame.dst_port == 67 && frame.src_port == 68 &&
		    parse_bootp_request(frame.payload, frame.payload_len, &request)) {
			uint8_t reply_type = request.dhcp_type == 1 ? 2 :
			    request.dhcp_type == 3 ? 5 : 0;
			if (request.hardware_len != 6 ||
			    memcmp(request.client_mac, frame.src_mac, 6) != 0) {
				log_line("BOOTP hardware identity does not match Ethernet source");
				return false;
			}
			if (request.dhcp_type != 0 && reply_type == 0) {
				log_line("unsupported DHCP message type %u", request.dhcp_type);
				return false;
			}
			if (have_peer && memcmp(peer_mac, frame.src_mac, 6) != 0) {
				log_line("BOOTP client changed during stage");
				return false;
			}
			memcpy(peer_mac, frame.src_mac, 6);
			have_peer = true;
			payload_len = build_bootp_reply(payload, sizeof(payload), &request,
			    server_ip, client_ip, image->name, reply_type);
			if (payload_len == 0 || !send_udp(rndis, broadcast_mac, server_ip,
			    broadcast_ip, 67, 68, payload, payload_len, &ip_id))
				return false;
			log_line("BOOTP reply file=%s%s", image->name,
			    reply_type == 2 ? " offer" : reply_type == 5 ? " ack" : "");
			continue;
		}
		/* U-Boot's netconsole, if the image was told to use it: the only way to
		 * read what a U-Boot on this board is doing, since the WLA has no UART. */
		if (frame.dst_port == 6666 && frame.payload_len > 0) {
			char line[513];
			size_t n = frame.payload_len < sizeof(line) - 1 ?
			    frame.payload_len : sizeof(line) - 1;

			memcpy(line, frame.payload, n);
			line[n] = '\0';
			for (size_t i = 0; i < n; i++)
				if (line[i] == '\r' || line[i] == '\n')
					line[i] = ' ';
			log_line("uboot: %s", line);
			continue;
		}
		if (frame.dst_port == 69 && parse_tftp_rrq(frame.payload,
		    frame.payload_len, filename, sizeof(filename))) {
			if (accept_any_filename && strcmp(filename, image->name) != 0)
				log_line("serving %s for requested file=%s",
				    image->name, filename);
			if (!have_peer || memcmp(peer_mac, frame.src_mac, 6) != 0 ||
			    !tftp_filename_allowed(filename, image) ||
			    memcmp(frame.src_ip, client_ip, 4) != 0 ||
			    memcmp(frame.dst_ip, server_ip, 4) != 0 ||
			    memcmp(frame.dst_mac, host_mac, 6) != 0) {
				log_line("rejected unexpected TFTP RRQ");
				return false;
			}
			client_port = frame.src_port;
			rrq_received = true;
		}
	}
	if (!rrq_received) {
		log_line("request deadline expired before exact %s RRQ",
		    image->name);
		return false;
	}
	log_line("TFTP RRQ accepted file=%s size=%zu", image->name, image->size);
	/* A multi-megabyte payload (kernel FIT) needs a deadline that grows with
	 * it: the measured rate is ~700 blocks/s, so 5 ms per block is a floor of
	 * ~100 KB/s with room to spare. */
	transfer_deadline = now_ms() + (strcmp(image->name, "MLO") == 0 ?
	    MLO_TRANSFER_DEADLINE_MS :
	    UBOOT_TRANSFER_DEADLINE_MS + 5 * (image->size / 512 + 1));
	for (;;) {
		if (now_ms() >= transfer_deadline) {
			log_line("whole-transfer deadline reached at block=%u", block);
			return false;
		}
		chunk = image->size - offset;
		if (chunk > 512)
			chunk = 512;
		payload_len = build_tftp_data(payload, sizeof(payload), block,
		    image->data + offset, chunk);
		if (!send_udp(rndis, peer_mac, server_ip, client_ip, 69, client_port,
		    payload, payload_len, &ip_id))
			return false;
		for (;;) {
			int length = rndis_receive_ethernet(rndis, packet, sizeof(packet),
			    TFTP_TIMEOUT_MS);
			if (now_ms() >= transfer_deadline) {
				log_line("whole-transfer deadline reached waiting for block=%u",
				    block);
				return false;
			}
			if (length < 0)
				return false;
			if (length > 0 && parse_udp_frame(packet, (size_t)length, &frame) &&
			    frame.src_port == client_port && frame.dst_port == 69 &&
			    memcmp(frame.src_mac, peer_mac, 6) == 0 &&
			    memcmp(frame.src_ip, client_ip, 4) == 0 &&
			    memcmp(frame.dst_mac, host_mac, 6) == 0 &&
			    memcmp(frame.dst_ip, server_ip, 4) == 0 &&
			    parse_tftp_ack(frame.payload, frame.payload_len, &ack)) {
				if (ack == block)
					break;
				if (ack != (uint16_t)(block - 1)) {
					log_line("invalid TFTP ACK block=%u expected=%u", ack, block);
					return false;
				}
			}
			if (++retries > TFTP_RETRIES) {
				log_line("TFTP retry limit reached at block=%u", block);
				return false;
			}
			if (!send_udp(rndis, peer_mac, server_ip, client_ip, 69,
			    client_port, payload, payload_len, &ip_id))
				return false;
		}
		retries = 0;
		offset += chunk;
		if (chunk < 512)
			break;
		if (block == UINT16_MAX) {
			log_line("TFTP block number overflow");
			return false;
		}
		block++;
	}
	log_line("TFTP complete file=%s blocks=%u bytes=%zu", image->name,
	    block, offset);
	return true;
}

static bool
wait_for_stage_disconnect(struct rndis_device *rndis)
{
	uint8_t buffer[64];
	int actual = 0;
	int rc;

	rc = libusb_bulk_transfer(rndis->handle, rndis->bulk_in, buffer,
	    sizeof(buffer), &actual, SPL_HANDOFF_WAIT_MS);
	if (rc == LIBUSB_ERROR_NO_DEVICE || rc == LIBUSB_ERROR_IO ||
	    rc == LIBUSB_ERROR_PIPE) {
		log_line("stage USB session disconnected after transfer");
		return true;
	}
	log_line("stage did not disconnect after transfer: %s",
	    rc == LIBUSB_SUCCESS ? "unexpected data" : libusb_error_name(rc));
	return false;
}

/*
 * Serve watch_file once, to whoever just asked for it by name. Unlike the stage
 * transfer this has no pre-agreed peer: the requester is whatever U-Boot's netconsole
 * session is, so the reply goes back to the RRQ's own MAC and port.
 */
/*
 * The first block of a watch-mode transfer reliably hits one bulk-write timeout:
 * U-Boot has just sent its RRQ and has no OUT buffer queued yet. The stage path
 * never sees this because its peer is the ROM or the SPL, both of which sit
 * waiting. Retry rather than abandon the transfer.
 */
static bool
send_udp_retrying(struct rndis_device *rndis, const uint8_t *peer_mac,
	uint16_t client_port, const uint8_t *payload, size_t payload_len,
	uint16_t *ip_id)
{
	for (unsigned attempt = 0; attempt < 8; attempt++) {
		if (send_udp(rndis, peer_mac, server_ip, client_ip, 69, client_port,
		    payload, payload_len, ip_id))
			return true;
	}
	return false;
}

static bool
serve_watch_file(struct rndis_device *rndis, const struct udp_frame *rrq)
{
	uint8_t payload[600];
	uint8_t packet[1600];
	uint8_t peer_mac[6];
	uint16_t client_port = rrq->src_port;
	uint16_t block = 1;
	uint16_t ip_id = 0x3000;
	size_t offset = 0;
	unsigned retries = 0;
	uint64_t deadline = now_ms() + UBOOT_TRANSFER_DEADLINE_MS +
	    5 * (watch_file.size / 512 + 1);

	memcpy(peer_mac, rrq->src_mac, sizeof(peer_mac));
	log_line("TFTP RRQ accepted (netconsole) file=%s size=%zu", watch_file.name,
	    watch_file.size);
	for (;;) {
		struct udp_frame frame;
		size_t chunk;
		size_t payload_len;
		uint16_t ack;

		if (now_ms() >= deadline) {
			log_line("watch-file deadline reached at block=%u", block);
			return false;
		}
		chunk = watch_file.size - offset;
		if (chunk > 512)
			chunk = 512;
		payload_len = build_tftp_data(payload, sizeof(payload), block,
		    watch_file.data + offset, chunk);
		if (!send_udp_retrying(rndis, peer_mac, client_port, payload,
		    payload_len, &ip_id))
			return false;
		for (;;) {
			int length = rndis_receive_ethernet(rndis, packet, sizeof(packet),
			    TFTP_TIMEOUT_MS);

			if (now_ms() >= deadline) {
				log_line("watch-file deadline reached waiting for block=%u",
				    block);
				return false;
			}
			if (length < 0)
				return false;
			if (length > 0 && parse_udp_frame(packet, (size_t)length, &frame) &&
			    frame.src_port == client_port && frame.dst_port == 69 &&
			    memcmp(frame.src_mac, peer_mac, 6) == 0 &&
			    memcmp(frame.dst_mac, host_mac, 6) == 0 &&
			    parse_tftp_ack(frame.payload, frame.payload_len, &ack)) {
				if (ack == block)
					break;
				if (ack != (uint16_t)(block - 1)) {
					log_line("invalid watch-file ACK block=%u expected=%u",
					    ack, block);
					return false;
				}
			}
			if (++retries > TFTP_RETRIES) {
				log_line("watch-file retry limit reached at block=%u", block);
				return false;
			}
			if (!send_udp_retrying(rndis, peer_mac, client_port, payload,
			    payload_len, &ip_id))
				return false;
		}
		retries = 0;
		offset += chunk;
		if (chunk < 512)
			break;
		if (block == UINT16_MAX) {
			log_line("watch-file block number overflow");
			return false;
		}
		block++;
	}
	log_line("TFTP complete (netconsole) file=%s blocks=%u bytes=%zu",
	    watch_file.name, block, offset);
	return true;
}

/*
 * Receive one file the board sends us with `tftpput`, and write it to upload_dir.
 *
 * This exists because netconsole is a bad way to move megabytes: `md.b` and reassembling
 * hex from the log is too slow for a rootfs partition. U-Boot can put a region of RAM
 * straight to a TFTP server instead; this is that server.
 *
 * Writing is deliberately narrow: only when --upload-dir was given, only a plain file
 * name, and only inside that directory. The board is not a trusted peer.
 */
static bool
receive_upload(struct rndis_device *rndis, const struct udp_frame *wrq,
	const char *filename)
{
	uint8_t payload[16];
	uint8_t packet[1600];
	uint8_t peer_mac[6];
	char path[PATH_MAX];
	uint16_t client_port = wrq->src_port;
	uint16_t block = 0;
	uint16_t ip_id = 0x4000;
	size_t total = 0;
	unsigned retries = 0;
	uint64_t idle_deadline;
	FILE *out;
	bool done = false;

	if (filename[0] == '\0' || strchr(filename, '/') != NULL ||
	    strcmp(filename, ".") == 0 || strcmp(filename, "..") == 0) {
		log_line("refusing upload of unsafe file name");
		return true;
	}
	if ((size_t)snprintf(path, sizeof(path), "%s/%s", upload_dir, filename) >=
	    sizeof(path)) {
		log_line("refusing upload: path too long");
		return true;
	}
	out = fopen(path, "wb");
	if (out == NULL) {
		log_line("cannot open %s for upload: %s", path, strerror(errno));
		return true;
	}
	memcpy(peer_mac, wrq->src_mac, sizeof(peer_mac));
	log_line("TFTP WRQ accepted file=%s -> %s", filename, path);
	/*
	 * Acknowledge block 0 to open the transfer, then every data block. The board
	 * retransmits when an ACK is lost, so a repeat of the block just written is
	 * answered again rather than treated as an error.
	 */
	idle_deadline = now_ms() + UPLOAD_IDLE_TIMEOUT_MS;
	if (build_tftp_ack(payload, sizeof(payload), 0) == 0 ||
	    !send_udp_retrying(rndis, peer_mac, client_port, payload, 4, &ip_id)) {
		fclose(out);
		return false;
	}
	while (!done) {
		struct udp_frame frame;
		const uint8_t *data;
		size_t data_len;
		uint16_t got;
		int length;

		if (now_ms() >= idle_deadline) {
			log_line("upload idle timeout after %zu bytes", total);
			fclose(out);
			return false;
		}
		length = rndis_receive_ethernet(rndis, packet, sizeof(packet),
		    TFTP_TIMEOUT_MS);
		if (length < 0) {
			fclose(out);
			return false;
		}
		if (length == 0 || !parse_udp_frame(packet, (size_t)length, &frame) ||
		    frame.src_port != client_port || frame.dst_port != 69 ||
		    memcmp(frame.src_mac, peer_mac, 6) != 0 ||
		    memcmp(frame.dst_mac, host_mac, 6) != 0 ||
		    !parse_tftp_data(frame.payload, frame.payload_len, &got, &data,
		    &data_len)) {
			if (length == 0 && ++retries > TFTP_RETRIES) {
				log_line("upload stalled at block=%u", block);
				fclose(out);
				return false;
			}
			continue;
		}
		retries = 0;
		idle_deadline = now_ms() + UPLOAD_IDLE_TIMEOUT_MS;
		if (got == (uint16_t)(block + 1)) {
			if (data_len != 0 && fwrite(data, 1, data_len, out) != data_len) {
				log_line("write to %s failed: %s", path, strerror(errno));
				fclose(out);
				return false;
			}
			block = got;
			total += data_len;
			/* A block shorter than 512 bytes is the end of the file. */
			done = data_len < 512;
		} else if (got != block) {
			log_line("out-of-order upload block=%u expected=%u", got,
			    (uint16_t)(block + 1));
			continue;
		}
		if (build_tftp_ack(payload, sizeof(payload), got) == 0 ||
		    !send_udp_retrying(rndis, peer_mac, client_port, payload, 4,
		    &ip_id)) {
			fclose(out);
			return false;
		}
	}
	if (fclose(out) != 0) {
		log_line("closing %s failed: %s", path, strerror(errno));
		return false;
	}
	log_line("TFTP upload complete file=%s bytes=%zu", filename, total);
	return true;
}

/*
 * Read frames from a stage that has nothing left to serve, purely to print what
 * the board says. U-Boot on this hardware has no UART, so its netconsole over
 * this same gadget is the only way to watch a bootcmd succeed or fail.
 */
static bool
watch_netconsole(struct hotplug_session *session, unsigned seconds,
	const char *marker)
{
	struct rndis_device rndis;
	uint64_t deadline;
	uint8_t packet[1600];
	unsigned read_errors = 0;
	uint64_t first_read_error = 0;
	uint64_t last_read_error_log = 0;
	bool found = false;

	if (!rndis_open(session, &rndis)) {
		rndis_close(&rndis);
		return false;
	}
	if (!rndis_initialize(&rndis)) {
		rndis_close(&rndis);
		return false;
	}
	/* U-Boot's gadget, not the ROM's: give a write room to be re-armed. */
	rndis_write_timeout_ms = 2000;
	log_line("watching netconsole for %us", seconds);
	deadline = now_ms() + (uint64_t)seconds * 1000;
	while (now_ms() < deadline) {
		struct udp_frame frame;
		struct arp_request arp;
		int length = rndis_receive_ethernet(&rndis, packet, sizeof(packet),
		    USB_BULK_TIMEOUT_MS);

		/*
		 * A read error must not end a long capture silently: ride out a burst of
		 * them and say so; give up only if they keep coming.
		 */
		if (length < 0) {
			uint64_t now = now_ms();

			if (read_errors == 0)
				first_read_error = now;
			read_errors++;
			if (now - first_read_error > WATCH_READ_ERROR_GRACE_MS) {
				log_line("netconsole read failing for %ums (%u errors); giving up",
				    (unsigned)(now - first_read_error), read_errors);
				break;
			}
			/*
			 * These come back instantly, so counting them is meaningless: a
			 * gadget busy right after a transfer produced 65 in 35 ms and ended
			 * a backup that was working. What matters is how long the failure
			 * lasts, and not spinning while it does.
			 */
			if (read_errors == 1 || now - last_read_error_log >= 2000) {
				log_line("netconsole read errors for %ums (%u so far), retrying",
				    (unsigned)(now - first_read_error), read_errors);
				last_read_error_log = now;
			}
			nanosleep(&(struct timespec){ .tv_nsec = 1000000L }, NULL);
			continue;
		}
		if (read_errors != 0)
			log_line("netconsole recovered after %u read errors", read_errors);
		read_errors = 0;
		if (length == 0)
			continue;
		/*
		 * U-Boot's netconsole has to resolve ncip before it can send a single
		 * byte, so a watcher that only listens hears nothing. Answer ARP for
		 * the boot server exactly as the transfer path does.
		 */
		if (parse_arp_request(packet, (size_t)length, &arp)) {
			size_t reply_len;

			if (memcmp(arp.target_ip, server_ip, IPV4_ADDR_LEN) != 0)
				continue;
			reply_len = build_arp_reply(packet, sizeof(packet), &arp,
			    host_mac, server_ip);
			if (reply_len == 0 ||
			    !rndis_send_ethernet(&rndis, packet, reply_len))
				break;
			log_line("ARP reply for boot server (netconsole)");
			continue;
		}
		if (!parse_udp_frame(packet, (size_t)length, &frame))
			continue;
		if (upload_dir != NULL && frame.dst_port == 69) {
			char filename[128];

			if (parse_tftp_wrq(frame.payload, frame.payload_len, filename,
			    sizeof(filename))) {
				if (!receive_upload(&rndis, &frame, filename))
					break;
				continue;
			}
		}
		if (have_watch_file && frame.dst_port == 69) {
			char filename[128];

			if (parse_tftp_rrq(frame.payload, frame.payload_len, filename,
			    sizeof(filename))) {
				if (strcmp(filename, watch_file.name) != 0) {
					log_line("watch-file RRQ for unknown file=%s", filename);
					continue;
				}
				if (!serve_watch_file(&rndis, &frame))
					break;
				continue;
			}
		}
		if (frame.dst_port == 6666 && frame.payload_len > 0) {
			char line[513];
			size_t n = frame.payload_len < sizeof(line) - 1 ?
			    frame.payload_len : sizeof(line) - 1;

			memcpy(line, frame.payload, n);
			line[n] = '\0';
			for (size_t i = 0; i < n; i++)
				if (line[i] == '\r' || line[i] == '\n')
					line[i] = ' ';
			log_line("uboot: %s", line);
			if (marker != NULL && strstr(line, marker) != NULL)
				found = true;
		}
	}
	rndis_write_timeout_ms = USB_BULK_TIMEOUT_MS;
	rndis_close(&rndis);
	return marker == NULL || found;
}

static bool
run_stage(struct hotplug_session *session, const struct image *image,
	bool expect_disconnect)
{
	struct rndis_device rndis;
	bool ok;

	if (!rndis_open(session, &rndis)) {
		rndis_close(&rndis);
		return false;
	}
	ok = rndis_initialize(&rndis) && serve_image(&rndis, image);
	if (ok && expect_disconnect)
		ok = wait_for_stage_disconnect(&rndis);
	rndis_close(&rndis);
	return ok;
}

static bool
run_same_session(struct hotplug_session *session, const struct image *mlo,
	const struct image *uboot)
{
	struct rndis_device rndis;
	bool ok;

	if (!rndis_open(session, &rndis)) {
		rndis_close(&rndis);
		return false;
	}
	ok = rndis_initialize(&rndis) && serve_image(&rndis, mlo);
	if (ok) {
		log_stage = "SPL";
		log_session = 1;
		log_line("same-session transition; retaining initialized RNDIS data path");
		ok = serve_image(&rndis, uboot);
	}
	rndis_close(&rndis);
	return ok;
}

static void
usage(const char *program)
{
	fprintf(stderr,
    "usage: %s [--serialized-usbip|--same-session-usbip|--same-session-usb-spl|--rom-only-usbip|--spl-only-usbip|--uart-handoff|--usb-spl|--stock-wla] [--fit-payload|--capture-payload] [--legacy-uboot] [--mlo-sha256 HEX] --uboot-sha256 HEX /path/to/MLO /path/to/u-boot.img [--payload HEX /path/to/file] [--watch SECONDS] [--watch-file NAME HEX /path/to/NAME] [--watch-marker TEXT] [--upload-dir DIR] [--rounds N] [--check]\n",
	    program);
}

int
main(int argc, char **argv)
{
	struct image mlo = {0}, uboot = {0}, payload = {0};
	struct hotplug_session rom = {0}, spl = {0}, third = {0};
	const char *payload_path = NULL;
	uint8_t payload_hash[32];
	struct usb_path physical_path;
	uint8_t mlo_hash[32], uboot_hash[32];
	bool check_only = false;
	const char *watch_marker = NULL;
	int result = EXIT_FAILURE;

	started_ms = now_ms();
	if (argc > 1 && (strcmp(argv[1], "--serialized-usbip") == 0 ||
	    strcmp(argv[1], "--same-session-usbip") == 0 ||
	    strcmp(argv[1], "--same-session-usb-spl") == 0 ||
	    strcmp(argv[1], "--rom-only-usbip") == 0 ||
	    strcmp(argv[1], "--spl-only-usbip") == 0 ||
	    strcmp(argv[1], "--uart-handoff") == 0 ||
	    strcmp(argv[1], "--stock-wla") == 0 ||
	    strcmp(argv[1], "--usb-spl") == 0)) {
		serialized_usbip = true;
		same_session_usbip = strcmp(argv[1], "--same-session-usbip") == 0 ||
		    strcmp(argv[1], "--same-session-usb-spl") == 0;
		spl_only_usbip = strcmp(argv[1], "--spl-only-usbip") == 0;
		uart_handoff = strcmp(argv[1], "--uart-handoff") == 0;
		stock_wla = strcmp(argv[1], "--stock-wla") == 0;
		usb_spl = strcmp(argv[1], "--usb-spl") == 0 ||
		    strcmp(argv[1], "--same-session-usb-spl") == 0;
		rom_only_usbip = strcmp(argv[1], "--rom-only-usbip") == 0 ||
		    uart_handoff;
		argv++;
		argc--;
	}
	if (argc > 1 && strcmp(argv[1], "--legacy-uboot") == 0) {
		legacy_uboot = true;
		argv++;
		argc--;
	}
	if (argc > 1 && strcmp(argv[1], "--fit-payload") == 0) {
		fit_payload = true;
		argv++;
		argc--;
	}
	if (argc > 1 && strcmp(argv[1], "--capture-payload") == 0) {
		capture_payload = true;
		argv++;
		argc--;
	}
	if (fit_payload && capture_payload) {
		usage(argv[0]);
		return EXIT_FAILURE;
	}
	if (argc > 1 && strcmp(argv[argc - 1], "--check") == 0) {
		check_only = true;
		argc--;
	}
	/*
	 * Uploads are off unless a directory is named, so a board can never write to
	 * this host by simply asking. Taken from the end, before the option pairs
	 * below, so it composes with --watch and --watch-file in either order.
	 */
	if (argc > 3 && strcmp(argv[argc - 2], "--upload-dir") == 0) {
		struct stat st;

		if (stat(argv[argc - 1], &st) != 0 || !S_ISDIR(st.st_mode)) {
			log_line("--upload-dir must name an existing directory");
			return EXIT_FAILURE;
		}
		upload_dir = argv[argc - 1];
		argc -= 2;
	}
	if (argc > 2 && strcmp(argv[1], "--mlo-sha256") == 0) {
		if (!usb_spl || !parse_sha256(argv[2], mlo_hash)) {
			usage(argv[0]);
			return EXIT_FAILURE;
		}
		argv += 2;
		argc -= 2;
	} else if (!parse_sha256(usb_spl ? approved_usb_spl_sha256 :
	    stock_wla ? approved_wla_mlo_sha256 : approved_mlo_sha256, mlo_hash)) {
		usage(argv[0]);
		return EXIT_FAILURE;
	}
	if (argc == 10 && strcmp(argv[8], "--rounds") == 0) {
		serve_rounds = (unsigned)strtoul(argv[9], NULL, 10);
		if (serve_rounds == 0 || serve_rounds > 32) {
			usage(argv[0]);
			return EXIT_FAILURE;
		}
		argc -= 2;
	}
	if (argc >= 7 && strcmp(argv[argc - 2], "--watch-marker") == 0) {
		if (argv[argc - 1][0] == '\0') {
			usage(argv[0]);
			return EXIT_FAILURE;
		}
		watch_marker = argv[argc - 1];
		argc -= 2;
	}
	if (argc >= 9 && strcmp(argv[argc - 4], "--watch-file") == 0) {
		uint8_t hash[32];

		if (!parse_sha256(argv[argc - 2], hash)) {
			usage(argv[0]);
			return EXIT_FAILURE;
		}
		/*
		 * A restore serves one NAND piece back to the board, and those are 16 MiB,
		 * so a watch file gets the payload ceiling.
		 */
		if (!read_image(argv[argc - 1], argv[argc - 3], PAYLOAD_MAX_SIZE, hash,
		    &watch_file))
			return EXIT_FAILURE;
		have_watch_file = true;
		argc -= 4;
	}
	if (argc == 7 && strcmp(argv[5], "--watch") == 0) {
		netconsole_seconds = (unsigned)strtoul(argv[6], NULL, 10);
		/*
		 * Ten minutes was enough while a watch only read netconsole. Carrying a
		 * 96 MiB partition off the board takes longer than that, so the ceiling
		 * is an hour; a watch still ends the moment its seconds are up.
		 */
		if (netconsole_seconds == 0 || netconsole_seconds > 3600) {
			usage(argv[0]);
			return EXIT_FAILURE;
		}
		argc -= 2;
	}
	if (watch_marker != NULL && netconsole_seconds == 0) {
		usage(argv[0]);
		return EXIT_FAILURE;
	}
	if (argc == 8 && strcmp(argv[5], "--payload") == 0) {
		if (!parse_sha256(argv[6], payload_hash)) {
			usage(argv[0]);
			return EXIT_FAILURE;
		}
		payload_path = argv[7];
		argc = 5;
	}
	if (argc != 5 || strcmp(argv[1], "--uboot-sha256") != 0 ||
	    !parse_sha256(argv[2], uboot_hash)) {
		usage(argv[0]);
		return EXIT_FAILURE;
	}
	if (geteuid() != 0) {
		log_line("must run as one root process; no helper or privilege prompt is used");
		return EXIT_FAILURE;
	}
	if (serialized_usbip)
		log_line("using serialized USB/IP RNDIS notification order");
	if (!read_image(argv[3], "MLO", MLO_MAX_SIZE, mlo_hash, &mlo) ||
	    !read_image(argv[4], "u-boot.img", UBOOT_MAX_SIZE, uboot_hash, &uboot))
		goto out;
	if (payload_path != NULL) {
		const char *name = fit_payload ? "soundtap-ram-nand.itb" :
		    capture_payload ? "soundtap-ram-capture.itb" : strrchr(payload_path, '/');

		name = (fit_payload || capture_payload) ? name :
		    (name == NULL ? payload_path : name + 1);
		if (!read_image(payload_path, name, PAYLOAD_MAX_SIZE,
		    payload_hash, &payload))
			goto out;
	}
	if (uart_handoff && !patch_uart_boot_device(&mlo))
		goto out;
	if (check_only) {
		log_line("image checks complete; no USB access requested");
		result = EXIT_SUCCESS;
		goto out;
	}

	log_stage = "ROM";
	if (rom_only_usbip) {
		if (!session_start(&rom, 1, NULL, 0) || !run_stage(&rom, &mlo, false))
			goto out;
		log_line("ROM-only MLO transfer complete; host must release generation");
		result = EXIT_SUCCESS;
		goto out;
	}
	if (spl_only_usbip) {
		log_stage = "SPL";
		if (!session_start(&spl, 2, NULL, 0) || !run_stage(&spl, &uboot, false))
			goto out;
		log_line("SPL-only TFTP serving complete for u-boot.img; board copy and branch unproven");
		result = EXIT_SUCCESS;
		goto out;
	}
	if (same_session_usbip) {
		if (!session_start(&rom, 1, NULL, 0) ||
		    !run_same_session(&rom, &mlo, &uboot))
			goto out;
		log_line("same-session TFTP serving complete for u-boot.img; board copy and branch unproven");
		result = EXIT_SUCCESS;
		goto out;
	}
	if (!session_start(&rom, 1, NULL, 0) || !run_stage(&rom, &mlo, true))
		goto out;
	physical_path = rom.path;
	uint8_t rom_address = libusb_get_device_address(rom.device);
	if (needs_rom_departure(usb_spl) &&
	    !pump_events_until(&rom, true, now_ms() + DEPARTURE_WAIT_MS)) {
		log_line("ROM device did not depart after MLO");
		goto out;
	}
	log_line("closing ROM libusb session after handoff");
	session_stop(&rom);
	if (usb_spl)
		nanosleep(&(struct timespec){ .tv_nsec = 500000000L }, NULL);
	/* The SPL gets a new Darwin/libusb session, while retaining the port pin. */
	log_stage = "SPL";
	/* Mainline SPL can take over 20 seconds to re-enumerate after ROM leaves. */
	device_wait_ms = SPL_HANDOFF_WAIT_MS;
	if (!session_start(&spl, 2, &physical_path, rom_address) ||
	    !run_stage(&spl, &uboot, false))
		goto out;
	log_line("TFTP serving complete for u-boot.img; board copy and branch unproven");
	if (payload_path == NULL && netconsole_seconds > 0) {
		/* Nothing to serve: U-Boot was told to boot from NAND, and all we
		 * want is to read its netconsole while it tries. */
		unsigned attempt;
		bool marker_found = watch_marker == NULL;

		log_stage = "UBOOT";
		device_wait_ms = 2500;
		session_stop(&spl);
		for (attempt = 1; attempt <= UBOOT_STAGE_ATTEMPTS; attempt++) {
			nanosleep(&(struct timespec){ .tv_sec = 1 }, NULL);
			session_stop(&third);
			if (session_attach_present(&third, 3, &physical_path, 0)) {
				if (watch_netconsole(&third, netconsole_seconds, watch_marker)) {
					marker_found = true;
					break;
				}
				continue;
			}
			log_line("attempt %u did not reach U-Boot's gadget", attempt);
		}
		if (!marker_found) {
			log_line("required netconsole marker not observed: %s", watch_marker);
			goto out;
		}
	}
	if (payload_path != NULL) {
		/*
		 * The U-Boot now in RAM brings up its own gadget, so it arrives
		 * as a third generation on the same port. It, not us, picks the
		 * filename it asks for.
		 */
		unsigned attempt, round;
		bool served = false;

		log_stage = "UBOOT";
		accept_any_filename = !fit_payload;
		device_wait_ms = 2500;
		session_stop(&spl);
		/*
		 * U-Boot re-registers its gadget on the same MUSB session, so
		 * the device does not always leave the bus. Re-open and retry
		 * until its own RNDIS instance answers.
		 */
		for (attempt = 1; attempt <= UBOOT_STAGE_ATTEMPTS; attempt++) {
			nanosleep(&(struct timespec){ .tv_sec = 1 }, NULL);
			session_stop(&third);
			if (session_attach_present(&third, 3, &physical_path, 0) &&
			    run_stage(&third, &payload, false)) {
				served = true;
				break;
			}
			log_line("attempt %u did not reach U-Boot's gadget", attempt);
		}
		if (!served)
			goto out;
		log_line("payload %s served into RAM; no flash or reset performed",
		    payload.name);
		/*
		 * Every net command in U-Boot halts the ethernet device, so the gadget
		 * leaves the bus and returns as a fresh generation for the next one.
		 * Re-attach between rounds: a bootcmd that reports its progress by
		 * asking for differently named files needs each request answered.
		 */
		for (round = 2; round <= serve_rounds; round++) {
			bool again = false;

			for (attempt = 1; attempt <= UBOOT_STAGE_ATTEMPTS; attempt++) {
				nanosleep(&(struct timespec){ .tv_nsec = 400000000L }, NULL);
				session_stop(&third);
				if (session_attach_present(&third, 3, &physical_path, 0) &&
				    run_stage(&third, &payload, false)) {
					again = true;
					break;
				}
			}
			if (!again) {
				log_line("no further request after round %u", round - 1);
				break;
			}
		}
	}
	result = EXIT_SUCCESS;
out:
	session_stop(&third);
	session_stop(&spl);
	session_stop(&rom);
	free(payload.data);
	free(uboot.data);
	free(mlo.data);
	return result;
}
