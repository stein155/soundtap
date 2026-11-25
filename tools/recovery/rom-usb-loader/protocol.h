/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#ifndef WLA_USB_BOOT_PROTOCOL_H
#define WLA_USB_BOOT_PROTOCOL_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#define ETH_ADDR_LEN 6
#define IPV4_ADDR_LEN 4

struct udp_frame {
	uint8_t dst_mac[ETH_ADDR_LEN];
	uint8_t src_mac[ETH_ADDR_LEN];
	uint8_t src_ip[IPV4_ADDR_LEN];
	uint8_t dst_ip[IPV4_ADDR_LEN];
	uint16_t src_port;
	uint16_t dst_port;
	const uint8_t *payload;
	size_t payload_len;
};

struct bootp_request {
	uint32_t xid;
	uint8_t client_mac[16];
	uint8_t hardware_len;
	uint8_t dhcp_type;
};

struct arp_request {
	uint8_t sender_mac[ETH_ADDR_LEN];
	uint8_t sender_ip[IPV4_ADDR_LEN];
	uint8_t target_ip[IPV4_ADDR_LEN];
};

bool parse_sha256(const char *text, uint8_t out[32]);
void format_sha256(const uint8_t hash[32], char out[65]);

bool parse_udp_frame(const uint8_t *packet, size_t length,
	struct udp_frame *frame);
size_t build_udp_frame(uint8_t *packet, size_t capacity,
	const uint8_t dst_mac[ETH_ADDR_LEN], const uint8_t src_mac[ETH_ADDR_LEN],
	const uint8_t src_ip[IPV4_ADDR_LEN], const uint8_t dst_ip[IPV4_ADDR_LEN],
	uint16_t src_port, uint16_t dst_port, const uint8_t *payload,
	size_t payload_len, uint16_t ip_id);
bool parse_arp_request(const uint8_t *packet, size_t length,
	struct arp_request *request);
size_t build_arp_reply(uint8_t *packet, size_t capacity,
	const struct arp_request *request, const uint8_t host_mac[ETH_ADDR_LEN],
	const uint8_t server_ip[IPV4_ADDR_LEN]);

bool parse_bootp_request(const uint8_t *p, size_t length,
	struct bootp_request *r);
size_t build_bootp_reply(uint8_t *p, size_t capacity,
	const struct bootp_request *r, const uint8_t server_ip[4],
	const uint8_t client_ip[4], const char *filename, uint8_t dhcp_type);

bool parse_tftp_rrq(const uint8_t *p, size_t length,
	char *filename, size_t filename_capacity);
bool parse_tftp_wrq(const uint8_t *p, size_t length,
	char *filename, size_t filename_capacity);
bool parse_tftp_ack(const uint8_t *p, size_t length, uint16_t *block);
bool parse_tftp_data(const uint8_t *p, size_t length, uint16_t *block,
	const uint8_t **data, size_t *data_len);
size_t build_tftp_ack(uint8_t *p, size_t capacity, uint16_t block);
size_t build_tftp_data(uint8_t *p, size_t capacity, uint16_t block,
	const uint8_t *data, size_t data_len);

#endif
