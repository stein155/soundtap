/* SPDX-License-Identifier: GPL-3.0-only */
/* SPDX-FileCopyrightText: 2026 SoundTap contributors */

#include "protocol.h"

#include <ctype.h>
#include <string.h>

static uint16_t
get_be16(const uint8_t *p)
{
	return (uint16_t)((uint16_t)p[0] << 8 | p[1]);
}

static void
put_be16(uint8_t *p, uint16_t value)
{
	p[0] = (uint8_t)(value >> 8);
	p[1] = (uint8_t)value;
}

static void
put_be32(uint8_t *p, uint32_t value)
{
	p[0] = (uint8_t)(value >> 24);
	p[1] = (uint8_t)(value >> 16);
	p[2] = (uint8_t)(value >> 8);
	p[3] = (uint8_t)value;
}

static uint32_t
get_be32(const uint8_t *p)
{
	return (uint32_t)p[0] << 24 | (uint32_t)p[1] << 16 |
	    (uint32_t)p[2] << 8 | p[3];
}

static uint16_t
checksum(const uint8_t *data, size_t length)
{
	uint32_t sum = 0;

	while (length >= 2) {
		sum += get_be16(data);
		data += 2;
		length -= 2;
	}
	if (length != 0)
		sum += (uint16_t)data[0] << 8;
	while (sum >> 16)
		sum = (sum & 0xffffU) + (sum >> 16);
	return (uint16_t)~sum;
}

static bool
valid_udp_checksum(const uint8_t *ip, const uint8_t *udp, size_t udp_len)
{
	uint32_t sum = 0;
	size_t i;

	if (get_be16(udp + 6) == 0)
		return true;
	for (i = 12; i < 20; i += 2)
		sum += get_be16(ip + i);
	sum += 17;
	sum += (uint16_t)udp_len;
	for (i = 0; i + 1 < udp_len; i += 2)
		sum += get_be16(udp + i);
	if ((udp_len & 1U) != 0)
		sum += (uint16_t)udp[udp_len - 1] << 8;
	while (sum >> 16)
		sum = (sum & 0xffffU) + (sum >> 16);
	return (uint16_t)sum == 0xffffU;
}

bool
parse_sha256(const char *text, uint8_t out[32])
{
	size_t i;

	if (text == NULL || strlen(text) != 64)
		return false;
	for (i = 0; i < 32; i++) {
		int hi = text[i * 2];
		int lo = text[i * 2 + 1];

		if (!isxdigit((unsigned char)hi) || !isxdigit((unsigned char)lo))
			return false;
		hi = isdigit((unsigned char)hi) ? hi - '0' :
		    tolower((unsigned char)hi) - 'a' + 10;
		lo = isdigit((unsigned char)lo) ? lo - '0' :
		    tolower((unsigned char)lo) - 'a' + 10;
		out[i] = (uint8_t)(hi << 4 | lo);
	}
	return true;
}

void
format_sha256(const uint8_t hash[32], char out[65])
{
	static const char hex[] = "0123456789abcdef";
	size_t i;

	for (i = 0; i < 32; i++) {
		out[i * 2] = hex[hash[i] >> 4];
		out[i * 2 + 1] = hex[hash[i] & 15];
	}
	out[64] = '\0';
}

bool
parse_udp_frame(const uint8_t *packet, size_t length, struct udp_frame *frame)
{
	size_t ihl, total, udp_len;

	if (packet == NULL || frame == NULL || length < 14 + 20 + 8)
		return false;
	if (get_be16(packet + 12) != 0x0800 || (packet[14] >> 4) != 4)
		return false;
	ihl = (size_t)(packet[14] & 0x0f) * 4;
	if (ihl < 20 || length < 14 + ihl + 8 || packet[23] != 17)
		return false;
	total = get_be16(packet + 16);
	if (total < ihl + 8 || total > length - 14)
		return false;
	if ((get_be16(packet + 20) & 0x3fffU) != 0)
		return false;
	if (checksum(packet + 14, ihl) != 0)
		return false;
	udp_len = get_be16(packet + 14 + ihl + 4);
	if (udp_len < 8 || udp_len != total - ihl)
		return false;
	if (!valid_udp_checksum(packet + 14, packet + 14 + ihl, udp_len))
		return false;

	memcpy(frame->dst_mac, packet, 6);
	memcpy(frame->src_mac, packet + 6, 6);
	memcpy(frame->src_ip, packet + 26, 4);
	memcpy(frame->dst_ip, packet + 30, 4);
	frame->src_port = get_be16(packet + 14 + ihl);
	frame->dst_port = get_be16(packet + 14 + ihl + 2);
	frame->payload = packet + 14 + ihl + 8;
	frame->payload_len = udp_len - 8;
	return true;
}

size_t
build_udp_frame(uint8_t *packet, size_t capacity,
	const uint8_t dst_mac[6], const uint8_t src_mac[6],
	const uint8_t src_ip[4], const uint8_t dst_ip[4], uint16_t src_port,
	uint16_t dst_port, const uint8_t *payload, size_t payload_len,
	uint16_t ip_id)
{
	size_t length = 14 + 20 + 8 + payload_len;

	if (packet == NULL || payload == NULL || length > capacity ||
	    payload_len > UINT16_MAX - 28)
		return 0;
	memset(packet, 0, 14 + 20 + 8);
	memcpy(packet, dst_mac, 6);
	memcpy(packet + 6, src_mac, 6);
	put_be16(packet + 12, 0x0800);
	packet[14] = 0x45;
	put_be16(packet + 16, (uint16_t)(20 + 8 + payload_len));
	put_be16(packet + 18, ip_id);
	put_be16(packet + 20, 0x4000);
	packet[22] = 64;
	packet[23] = 17;
	memcpy(packet + 26, src_ip, 4);
	memcpy(packet + 30, dst_ip, 4);
	put_be16(packet + 24, checksum(packet + 14, 20));
	put_be16(packet + 34, src_port);
	put_be16(packet + 36, dst_port);
	put_be16(packet + 38, (uint16_t)(8 + payload_len));
	/* A zero UDP checksum is valid for IPv4 and avoids pseudo-header ambiguity. */
	memcpy(packet + 42, payload, payload_len);
	return length;
}

bool
parse_arp_request(const uint8_t *packet, size_t length,
	struct arp_request *request)
{
	const uint8_t *arp;

	if (packet == NULL || request == NULL || length < 42 ||
	    get_be16(packet + 12) != 0x0806)
		return false;
	arp = packet + 14;
	if (get_be16(arp) != 1 || get_be16(arp + 2) != 0x0800 ||
	    arp[4] != 6 || arp[5] != 4 || get_be16(arp + 6) != 1 ||
	    memcmp(packet + 6, arp + 8, ETH_ADDR_LEN) != 0)
		return false;
	memcpy(request->sender_mac, arp + 8, ETH_ADDR_LEN);
	memcpy(request->sender_ip, arp + 14, IPV4_ADDR_LEN);
	memcpy(request->target_ip, arp + 24, IPV4_ADDR_LEN);
	return true;
}

size_t
build_arp_reply(uint8_t *packet, size_t capacity,
	const struct arp_request *request, const uint8_t host_mac[ETH_ADDR_LEN],
	const uint8_t server_ip[IPV4_ADDR_LEN])
{
	uint8_t *arp;

	if (packet == NULL || request == NULL || capacity < 42)
		return 0;
	memset(packet, 0, 42);
	memcpy(packet, request->sender_mac, ETH_ADDR_LEN);
	memcpy(packet + 6, host_mac, ETH_ADDR_LEN);
	put_be16(packet + 12, 0x0806);
	arp = packet + 14;
	put_be16(arp, 1);
	put_be16(arp + 2, 0x0800);
	arp[4] = 6;
	arp[5] = 4;
	put_be16(arp + 6, 2);
	memcpy(arp + 8, host_mac, ETH_ADDR_LEN);
	memcpy(arp + 14, server_ip, IPV4_ADDR_LEN);
	memcpy(arp + 18, request->sender_mac, ETH_ADDR_LEN);
	memcpy(arp + 24, request->sender_ip, IPV4_ADDR_LEN);
	return 42;
}

size_t
build_arp_request(uint8_t *packet, size_t capacity,
	const uint8_t host_mac[ETH_ADDR_LEN],
	const uint8_t host_ip[IPV4_ADDR_LEN],
	const uint8_t target_ip[IPV4_ADDR_LEN])
{
	uint8_t *arp;

	if (packet == NULL || capacity < 42)
		return 0;
	memset(packet, 0, 42);
	memset(packet, 0xff, ETH_ADDR_LEN);
	memcpy(packet + 6, host_mac, ETH_ADDR_LEN);
	put_be16(packet + 12, 0x0806);
	arp = packet + 14;
	put_be16(arp, 1);
	put_be16(arp + 2, 0x0800);
	arp[4] = ETH_ADDR_LEN;
	arp[5] = IPV4_ADDR_LEN;
	put_be16(arp + 6, 1);
	memcpy(arp + 8, host_mac, ETH_ADDR_LEN);
	memcpy(arp + 14, host_ip, IPV4_ADDR_LEN);
	memcpy(arp + 24, target_ip, IPV4_ADDR_LEN);
	return 42;
}

bool
parse_arp_reply(const uint8_t *packet, size_t length,
	const uint8_t host_mac[ETH_ADDR_LEN],
	const uint8_t host_ip[IPV4_ADDR_LEN],
	const uint8_t target_ip[IPV4_ADDR_LEN], uint8_t target_mac[ETH_ADDR_LEN])
{
	const uint8_t *arp;

	if (packet == NULL || length < 42 || get_be16(packet + 12) != 0x0806)
		return false;
	arp = packet + 14;
	if (get_be16(arp) != 1 || get_be16(arp + 2) != 0x0800 ||
	    arp[4] != ETH_ADDR_LEN || arp[5] != IPV4_ADDR_LEN ||
	    get_be16(arp + 6) != 2 ||
	    memcmp(packet, host_mac, ETH_ADDR_LEN) != 0 ||
	    memcmp(packet + 6, arp + 8, ETH_ADDR_LEN) != 0 ||
	    memcmp(arp + 14, target_ip, IPV4_ADDR_LEN) != 0 ||
	    memcmp(arp + 18, host_mac, ETH_ADDR_LEN) != 0 ||
	    memcmp(arp + 24, host_ip, IPV4_ADDR_LEN) != 0)
		return false;
	memcpy(target_mac, arp + 8, ETH_ADDR_LEN);
	return true;
}

static bool
valid_tcp_checksum(const uint8_t *ip, const uint8_t *tcp, size_t tcp_len)
{
	uint32_t sum = 0;
	size_t i;

	for (i = 12; i < 20; i += 2)
		sum += get_be16(ip + i);
	sum += 6;
	sum += (uint16_t)tcp_len;
	for (i = 0; i + 1 < tcp_len; i += 2)
		sum += get_be16(tcp + i);
	if ((tcp_len & 1U) != 0)
		sum += (uint16_t)tcp[tcp_len - 1] << 8;
	while (sum >> 16)
		sum = (sum & 0xffffU) + (sum >> 16);
	return (uint16_t)sum == 0xffffU;
}

bool
parse_tcp_frame(const uint8_t *packet, size_t length, struct tcp_frame *frame)
{
	const uint8_t *ip, *tcp;
	size_t ihl, total, tcp_len, tcp_hlen, off;

	if (packet == NULL || frame == NULL || length < 14 + 20 + 20 ||
	    get_be16(packet + 12) != 0x0800)
		return false;
	ip = packet + 14;
	ihl = (size_t)(ip[0] & 0x0f) * 4;
	if ((ip[0] >> 4) != 4 || ihl < 20 || length < 14 + ihl + 20 ||
	    ip[9] != 6 || (get_be16(ip + 6) & 0x3fffU) != 0 ||
	    checksum(ip, ihl) != 0)
		return false;
	total = get_be16(ip + 2);
	if (total < ihl + 20 || total > length - 14)
		return false;
	tcp = ip + ihl;
	tcp_len = total - ihl;
	tcp_hlen = (size_t)(tcp[12] >> 4) * 4;
	if (tcp_hlen < 20 || tcp_hlen > tcp_len ||
	    !valid_tcp_checksum(ip, tcp, tcp_len))
		return false;
	memset(frame, 0, sizeof(*frame));
	memcpy(frame->dst_mac, packet, ETH_ADDR_LEN);
	memcpy(frame->src_mac, packet + 6, ETH_ADDR_LEN);
	memcpy(frame->src_ip, ip + 12, IPV4_ADDR_LEN);
	memcpy(frame->dst_ip, ip + 16, IPV4_ADDR_LEN);
	frame->src_port = get_be16(tcp);
	frame->dst_port = get_be16(tcp + 2);
	frame->seq = get_be32(tcp + 4);
	frame->ack = get_be32(tcp + 8);
	frame->flags = tcp[13];
	frame->window = get_be16(tcp + 14);
	frame->payload = tcp + tcp_hlen;
	frame->payload_len = tcp_len - tcp_hlen;
	for (off = 20; off < tcp_hlen;) {
		uint8_t kind = tcp[off];
		uint8_t option_len;

		if (kind == 0)
			break;
		if (kind == 1) {
			off++;
			continue;
		}
		if (off + 2 > tcp_hlen || (option_len = tcp[off + 1]) < 2 ||
		    option_len > tcp_hlen - off)
			return false;
		if (kind == 2 && option_len == 4)
			frame->mss = get_be16(tcp + off + 2);
		off += option_len;
	}
	return true;
}

size_t
build_tcp_frame(uint8_t *packet, size_t capacity,
	const uint8_t dst_mac[ETH_ADDR_LEN], const uint8_t src_mac[ETH_ADDR_LEN],
	const uint8_t src_ip[IPV4_ADDR_LEN], const uint8_t dst_ip[IPV4_ADDR_LEN],
	uint16_t src_port, uint16_t dst_port, uint32_t seq, uint32_t ack,
	uint8_t flags, uint16_t window, uint16_t mss, const uint8_t *payload,
	size_t payload_len, uint16_t ip_id)
{
	size_t tcp_hlen = mss == 0 ? 20 : 24;
	size_t tcp_len = tcp_hlen + payload_len;
	size_t length = 14 + 20 + tcp_len;
	uint8_t *ip, *tcp;
	uint32_t sum = 0;
	size_t i;

	if (packet == NULL || (payload_len != 0 && payload == NULL) ||
	    length > capacity || tcp_len > UINT16_MAX - 20)
		return 0;
	memset(packet, 0, 14 + 20 + tcp_hlen);
	memcpy(packet, dst_mac, ETH_ADDR_LEN);
	memcpy(packet + 6, src_mac, ETH_ADDR_LEN);
	put_be16(packet + 12, 0x0800);
	ip = packet + 14;
	ip[0] = 0x45;
	put_be16(ip + 2, (uint16_t)(20 + tcp_len));
	put_be16(ip + 4, ip_id);
	put_be16(ip + 6, 0x4000);
	ip[8] = 64;
	ip[9] = 6;
	memcpy(ip + 12, src_ip, IPV4_ADDR_LEN);
	memcpy(ip + 16, dst_ip, IPV4_ADDR_LEN);
	put_be16(ip + 10, checksum(ip, 20));
	tcp = ip + 20;
	put_be16(tcp, src_port);
	put_be16(tcp + 2, dst_port);
	put_be32(tcp + 4, seq);
	put_be32(tcp + 8, ack);
	tcp[12] = (uint8_t)(tcp_hlen / 4) << 4;
	tcp[13] = flags;
	put_be16(tcp + 14, window);
	if (mss != 0) {
		tcp[20] = 2;
		tcp[21] = 4;
		put_be16(tcp + 22, mss);
	}
	if (payload_len != 0)
		memcpy(tcp + tcp_hlen, payload, payload_len);
	for (i = 12; i < 20; i += 2)
		sum += get_be16(ip + i);
	sum += 6;
	sum += (uint16_t)tcp_len;
	for (i = 0; i + 1 < tcp_len; i += 2)
		sum += get_be16(tcp + i);
	if ((tcp_len & 1U) != 0)
		sum += (uint16_t)tcp[tcp_len - 1] << 8;
	while (sum >> 16)
		sum = (sum & 0xffffU) + (sum >> 16);
	put_be16(tcp + 16, (uint16_t)~sum);
	return length;
}

bool
parse_bootp_request(const uint8_t *p, size_t length, struct bootp_request *r)
{
	size_t off;

	if (p == NULL || r == NULL || length < 236 || p[0] != 1 || p[1] != 1 ||
	    p[2] == 0 || p[2] > 16)
		return false;
	memset(r, 0, sizeof(*r));
	r->xid = ((uint32_t)p[4] << 24) | ((uint32_t)p[5] << 16) |
	    ((uint32_t)p[6] << 8) | p[7];
	r->hardware_len = p[2];
	memcpy(r->client_mac, p + 28, r->hardware_len);
	if (length == 236)
		return true;
	if (length < 240 || memcmp(p + 236, "\x63\x82\x53\x63", 4) != 0)
		return false;
	for (off = 240; off < length;) {
		uint8_t code = p[off++];
		uint8_t option_len;

		if (code == 0)
			continue;
		if (code == 255)
			return true;
		if (off >= length)
			return false;
		option_len = p[off++];
		if (option_len > length - off)
			return false;
		if (code == 53) {
			if (option_len != 1)
				return false;
			r->dhcp_type = p[off];
		}
		off += option_len;
	}
	return true;
}

size_t
build_bootp_reply(uint8_t *p, size_t capacity, const struct bootp_request *r,
	const uint8_t server_ip[4], const uint8_t client_ip[4],
	const char *filename, uint8_t dhcp_type)
{
	size_t filename_len, length = dhcp_type == 0 ? 236 : 250;

	if (p == NULL || r == NULL || filename == NULL || capacity < length)
		return 0;
	filename_len = strlen(filename);
	if (filename_len == 0 || filename_len >= 128 || r->hardware_len == 0 ||
	    r->hardware_len > 16)
		return 0;
	memset(p, 0, length);
	p[0] = 2;
	p[1] = 1;
	p[2] = r->hardware_len;
	put_be32(p + 4, r->xid);
	memcpy(p + 16, client_ip, 4);
	memcpy(p + 20, server_ip, 4);
	memcpy(p + 28, r->client_mac, r->hardware_len);
	memcpy(p + 108, filename, filename_len);
	if (dhcp_type != 0) {
		memcpy(p + 236, "\x63\x82\x53\x63", 4);
		p[240] = 53; p[241] = 1; p[242] = dhcp_type;
		p[243] = 54; p[244] = 4; memcpy(p + 245, server_ip, 4);
		p[249] = 255;
	}
	return length;
}

static bool
parse_tftp_request(const uint8_t *p, size_t length, uint16_t opcode,
	char *filename, size_t filename_capacity)
{
	const uint8_t *name_end, *mode_end;
	size_t name_len, mode_len, i;

	if (p == NULL || filename == NULL || length < 2 + 1 + 1 ||
	    get_be16(p) != opcode)
		return false;
	name_end = memchr(p + 2, '\0', length - 2);
	if (name_end == NULL || name_end == p + 2)
		return false;
	name_len = (size_t)(name_end - (p + 2));
	if (name_len >= filename_capacity)
		return false;
	mode_end = memchr(name_end + 1, '\0', length - (size_t)(name_end + 1 - p));
	/*
	 * Anything after the mode is RFC 2347 option text - U-Boot's TFTP asks
	 * for "timeout". A server may ignore options and answer with DATA, so
	 * only require that the trailing text is NUL-terminated.
	 */
	if (mode_end == NULL || p[length - 1] != '\0')
		return false;
	mode_len = (size_t)(mode_end - (name_end + 1));
	if (mode_len != 5)
		return false;
	for (i = 0; i < mode_len; i++)
		if (tolower((unsigned char)name_end[1 + i]) != "octet"[i])
			return false;
	memcpy(filename, p + 2, name_len);
	filename[name_len] = '\0';
	return true;
}

bool
parse_tftp_rrq(const uint8_t *p, size_t length, char *filename,
	size_t filename_capacity)
{
	return parse_tftp_request(p, length, 1, filename, filename_capacity);
}

/*
 * A write request is a read request with a different opcode. U-Boot sends one for
 * `tftpput`, which is how a partition too large to hex-dump over netconsole gets off
 * this board: read it into RAM, put it to the loader, at transfer speed.
 */
bool
parse_tftp_wrq(const uint8_t *p, size_t length, char *filename,
	size_t filename_capacity)
{
	return parse_tftp_request(p, length, 2, filename, filename_capacity);
}

bool
parse_tftp_data(const uint8_t *p, size_t length, uint16_t *block,
	const uint8_t **data, size_t *data_len)
{
	if (p == NULL || block == NULL || data == NULL || data_len == NULL ||
	    length < 4 || length > 4 + 512 || get_be16(p) != 3)
		return false;
	*block = get_be16(p + 2);
	*data = p + 4;
	*data_len = length - 4;
	return true;
}

size_t
build_tftp_ack(uint8_t *p, size_t capacity, uint16_t block)
{
	if (p == NULL || capacity < 4)
		return 0;
	put_be16(p, 4);
	put_be16(p + 2, block);
	return 4;
}

bool
parse_tftp_ack(const uint8_t *p, size_t length, uint16_t *block)
{
	if (p == NULL || block == NULL || length != 4 || get_be16(p) != 4)
		return false;
	*block = get_be16(p + 2);
	return true;
}

size_t
build_tftp_data(uint8_t *p, size_t capacity, uint16_t block,
	const uint8_t *data, size_t data_len)
{
	if (p == NULL || data == NULL || data_len > 512 || capacity < 4 + data_len)
		return 0;
	put_be16(p, 3);
	put_be16(p + 2, block);
	memcpy(p + 4, data, data_len);
	return 4 + data_len;
}
