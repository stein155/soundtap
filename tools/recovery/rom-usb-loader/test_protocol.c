/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include "protocol.h"

#include <assert.h>
#include <stdio.h>
#include <string.h>

static void
test_hash(void)
{
	uint8_t hash[32];
	char text[65];
	const char *expected =
	    "7d272c4653c978eb3fe8ddc23a7f12216ab0ae17a42594c937ce4d91f323271b";

	assert(parse_sha256(expected, hash));
	format_sha256(hash, text);
	assert(strcmp(text, expected) == 0);
	assert(!parse_sha256("d49f", hash));
	assert(!parse_sha256(
	    "zd272c4653c978eb3fe8ddc23a7f12216ab0ae17a42594c937ce4d91f323271b",
	    hash));
}

static void
test_udp(void)
{
	const uint8_t dst_mac[6] = {1, 2, 3, 4, 5, 6};
	const uint8_t src_mac[6] = {6, 5, 4, 3, 2, 1};
	const uint8_t src_ip[4] = {192, 168, 2, 1};
	const uint8_t dst_ip[4] = {192, 168, 2, 2};
	const uint8_t payload[] = {0, 3, 0, 7, 1, 2, 3};
	uint8_t packet[128];
	struct udp_frame frame;
	size_t length;

	length = build_udp_frame(packet, sizeof(packet), dst_mac, src_mac,
	    src_ip, dst_ip, 69, 1234, payload, sizeof(payload), 1);
	assert(length == 42 + sizeof(payload));
	assert(parse_udp_frame(packet, length, &frame));
	assert(frame.src_port == 69 && frame.dst_port == 1234);
	assert(frame.payload_len == sizeof(payload));
	assert(memcmp(frame.payload, payload, sizeof(payload)) == 0);
	packet[40] = 0x12;
	packet[41] = 0x34;
	assert(!parse_udp_frame(packet, length, &frame));
	packet[40] = 0;
	packet[41] = 0;
	packet[24] ^= 1;
	assert(!parse_udp_frame(packet, length, &frame));
	assert(!parse_udp_frame(packet, 20, &frame));
}

static void
test_arp(void)
{
	const uint8_t client_mac[6] = {1, 2, 3, 4, 5, 6};
	const uint8_t host_mac[6] = {2, 0, 0, 0, 0, 1};
	const uint8_t client_ip[4] = {192, 168, 2, 2};
	const uint8_t server_ip[4] = {192, 168, 2, 1};
	uint8_t packet[60] = {0}, reply[42];
	struct arp_request request;

	memset(packet, 0xff, 6);
	memcpy(packet + 6, client_mac, 6);
	packet[12] = 0x08; packet[13] = 0x06;
	packet[15] = 1; packet[16] = 0x08;
	packet[18] = 6; packet[19] = 4; packet[21] = 1;
	memcpy(packet + 22, client_mac, 6);
	memcpy(packet + 28, client_ip, 4);
	memset(packet + 32, 0, 6);
	memcpy(packet + 38, server_ip, 4);
	assert(parse_arp_request(packet, sizeof(packet), &request));
	assert(memcmp(request.sender_mac, client_mac, 6) == 0);
	assert(memcmp(request.target_ip, server_ip, 4) == 0);
	assert(parse_arp_request(packet, 42, &request));
	assert(build_arp_reply(reply, sizeof(reply), &request, host_mac,
	    server_ip) == sizeof(reply));
	assert(memcmp(reply, client_mac, 6) == 0);
	assert(memcmp(reply + 6, host_mac, 6) == 0);
	assert(reply[20] == 0 && reply[21] == 2);
	assert(memcmp(reply + 22, host_mac, 6) == 0);
	assert(memcmp(reply + 28, server_ip, 4) == 0);
	packet[13] = 0;
	assert(!parse_arp_request(packet, sizeof(packet), &request));
}

static void
test_bootp(void)
{
	uint8_t request_bytes[244] = {0}, reply[300];
	const uint8_t server[4] = {192, 168, 2, 1};
	const uint8_t client[4] = {192, 168, 2, 2};
	struct bootp_request request;
	size_t length;

	request_bytes[0] = 1;
	request_bytes[1] = 1;
	request_bytes[2] = 6;
	request_bytes[4] = 0x12;
	request_bytes[7] = 0x34;
	memcpy(request_bytes + 28, "ABCDEF", 6);
	memcpy(request_bytes + 236, "\x63\x82\x53\x63", 4);
	request_bytes[240] = 53;
	request_bytes[241] = 1;
	request_bytes[242] = 1;
	request_bytes[243] = 255;
	assert(parse_bootp_request(request_bytes, sizeof(request_bytes), &request));
	assert(request.xid == 0x12000034 && request.dhcp_type == 1);
	length = build_bootp_reply(reply, sizeof(reply), &request, server, client,
	    "MLO", 2);
	assert(length == 250 && reply[0] == 2 && reply[242] == 2);
	request_bytes[241] = 10;
	assert(!parse_bootp_request(request_bytes, sizeof(request_bytes), &request));
}

static void test_tftp_upload(void);

static void
test_tftp(void)
{
	const uint8_t rrq[] = {0, 1, 'M', 'L', 'O', 0, 'o', 'c', 't', 'e', 't', 0};
	const uint8_t bad_rrq[] = {0, 1, 'M', 'L', 'O', 0, 'n', 'e', 't', 'a', 's', 'c', 'i', 'i', 0};
	/* U-Boot's TFTP appends RFC 2347 options after the mode. */
	const uint8_t opt_rrq[] = {0, 1, 'M', 'L', 'O', 0, 'o', 'c', 't', 'e', 't', 0,
	    't', 'i', 'm', 'e', 'o', 'u', 't', 0, '5', 0};
	const uint8_t unterminated_rrq[] = {0, 1, 'M', 'L', 'O', 0, 'o', 'c', 't',
	    'e', 't', 0, 't', 'i', 'm', 'e', 'o', 'u', 't'};
	const uint8_t ack[] = {0, 4, 0x12, 0x34};
	uint8_t data[516];
	uint16_t block;
	char filename[16];

	assert(parse_tftp_rrq(rrq, sizeof(rrq), filename, sizeof(filename)));
	assert(strcmp(filename, "MLO") == 0);
	assert(!parse_tftp_rrq(bad_rrq, sizeof(bad_rrq), filename,
	    sizeof(filename)));
	assert(parse_tftp_rrq(opt_rrq, sizeof(opt_rrq), filename,
	    sizeof(filename)) && strcmp(filename, "MLO") == 0);
	assert(!parse_tftp_rrq(unterminated_rrq, sizeof(unterminated_rrq),
	    filename, sizeof(filename)));
	assert(parse_tftp_ack(ack, sizeof(ack), &block) && block == 0x1234);
	assert(!parse_tftp_ack(ack, 3, &block));
	assert(build_tftp_data(data, sizeof(data), 9, rrq, sizeof(rrq)) ==
	    4 + sizeof(rrq));
	assert(data[1] == 3 && data[3] == 9);
	test_tftp_upload();
}

/*
 * The upload direction: a write request, the data blocks it is followed by, and the
 * acknowledgement each one needs. This is what carries a NAND partition off the board.
 */
static void
test_tftp_upload(void)
{
	static const uint8_t wrq[] = {
		0, 2, 'm', 't', 'd', '6', 0, 'o', 'c', 't', 'e', 't', 0
	};
	static const uint8_t rrq[] = {
		0, 1, 'm', 't', 'd', '6', 0, 'o', 'c', 't', 'e', 't', 0
	};
	uint8_t block_data[4 + 512];
	uint8_t ack[8];
	const uint8_t *payload;
	size_t payload_len;
	uint16_t block;
	char filename[16];

	assert(parse_tftp_wrq(wrq, sizeof(wrq), filename, sizeof(filename)));
	assert(strcmp(filename, "mtd6") == 0);
	/* The two opcodes must not be confused for one another in either direction. */
	assert(!parse_tftp_wrq(rrq, sizeof(rrq), filename, sizeof(filename)));
	assert(!parse_tftp_rrq(wrq, sizeof(wrq), filename, sizeof(filename)));

	memset(block_data, 0, sizeof(block_data));
	block_data[1] = 3;
	block_data[3] = 7;
	block_data[4] = 0xa5;
	assert(parse_tftp_data(block_data, 4 + 512, &block, &payload, &payload_len));
	assert(block == 7 && payload_len == 512 && payload[0] == 0xa5);
	/* A short block is legal and is how a transfer ends; an empty one too. */
	assert(parse_tftp_data(block_data, 4, &block, &payload, &payload_len) &&
	    payload_len == 0);
	assert(!parse_tftp_data(block_data, 3, &block, &payload, &payload_len));
	assert(!parse_tftp_data(block_data, 4 + 513, &block, &payload, &payload_len));
	block_data[1] = 4;
	assert(!parse_tftp_data(block_data, 4 + 512, &block, &payload, &payload_len));

	assert(build_tftp_ack(ack, sizeof(ack), 0x0102) == 4);
	assert(ack[0] == 0 && ack[1] == 4 && ack[2] == 1 && ack[3] == 2);
	assert(build_tftp_ack(ack, 3, 1) == 0);
}

int
main(void)
{
	test_hash();
	test_udp();
	test_arp();
	test_bootp();
	test_tftp();
	puts("protocol tests passed");
	return 0;
}
