// SPDX-License-Identifier: GPL-2.0+
/*
 * The signed package statement of ADR 0048, read the way app/internal/release reads it,
 * for the USB-stick recovery (uboot-2026.07-usb-recovery.patch):
 *
 *   soundtap-package v1 kind=core profile=production version=0.1.0 sha256=<64 hex> sig=<base64>
 *
 * release.Verify is the reference, and this accepts exactly what it accepts:
 * app/internal/release/uboot_test.go builds this file for the host and runs both over the
 * same statements, the valid ones and every one a byte away from them. Every rule below is
 * one of parse(), Statement.valid() and ValidVersion() in release.go, in that order.
 *
 * The signature is TweetNaCl's ed25519 (tweetnacl.c, public domain, ADR 0124), with the
 * one check Go's crypto/ed25519 makes on top: S below the group order, so a signature
 * has one encoding.
 *
 * The keys are the compiled release keys.
 */
#include "soundtap-sig.h"

#define ST_PREFIX		"soundtap-package v1 "
#define ST_PREFIX_LEN		(sizeof(ST_PREFIX) - 1)
/* tweetnacl.c, as its tweetnacl.h names it; that header is not included here because its
 * macros would rename anything called crypto_* in U-Boot's own headers. */
int crypto_sign_ed25519_tweet_open(unsigned char *m, unsigned long long *mlen,
				   const unsigned char *sm, unsigned long long n,
				   const unsigned char *pk);

static const char *const st_keys[] = { "kind=", "profile=", "version=", "sha256=", "sig=" };

/* The ed25519 group order L, little endian. */
static const unsigned char st_order[32] = {
	0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10,
};

static int st_is(const char *v, size_t n, const char *want)
{
	return n == strlen(want) && !memcmp(v, want, n);
}

/* hex.DecodeString and then "values[3] != strings.ToLower(values[3])": lowercase only. */
static int st_hex(char c)
{
	if (c >= '0' && c <= '9')
		return c - '0';
	if (c >= 'a' && c <= 'f')
		return c - 'a' + 10;
	return -1;
}

#define ST_ALPHA		"-alpha."
#define ST_ALPHA_LEN		(sizeof(ST_ALPHA) - 1)

/* ValidVersion: three dot-separated parts, each one to nine decimal digits, and optionally
 * "-alpha." and N, one to nine digits not starting with 0. */
static int st_valid_version(const char *v, size_t n)
{
	size_t i, run = 0;
	int dots = 0;

	for (i = 0; i < n; i++) {
		if (v[i] == '.') {
			if (!run)
				return 0;
			dots++;
			run = 0;
		} else if (v[i] == '-' && dots == 2 && run) {
			break;
		} else if (v[i] < '0' || v[i] > '9' || ++run > 9) {
			return 0;
		}
	}
	if (dots != 2 || !run)
		return 0;
	if (i == n)
		return 1;
	v += i;
	n -= i;
	if (n <= ST_ALPHA_LEN || n > ST_ALPHA_LEN + 9 || memcmp(v, ST_ALPHA, ST_ALPHA_LEN) ||
	    v[ST_ALPHA_LEN] == '0')
		return 0;
	for (i = ST_ALPHA_LEN; i < n; i++)
		if (v[i] < '0' || v[i] > '9')
			return 0;
	return 1;
}

static int st_b64(char c)
{
	if (c >= 'A' && c <= 'Z')
		return c - 'A';
	if (c >= 'a' && c <= 'z')
		return c - 'a' + 26;
	if (c >= '0' && c <= '9')
		return c - '0' + 52;
	if (c == '+')
		return 62;
	if (c == '/')
		return 63;
	return -1;
}

/*
 * base64.StdEncoding.Strict().DecodeString, for a result of exactly want bytes: that is
 * 4 * ceil(want / 3) characters ending in the padding the remainder needs, and Strict wants
 * the bits after the last byte zero. 64 bytes are 88 characters ending in "==" (a
 * signature), 32 bytes 44 ending in "=" (a key).
 */
static int st_b64_exact(const char *s, size_t n, unsigned char *out, size_t want)
{
	size_t pad = (3 - want % 3) % 3, i, got = 0;
	unsigned int acc = 0;
	int bits = 0, v;

	if (n != (want + 2) / 3 * 4)
		return -1;
	for (i = n - pad; i < n; i++)
		if (s[i] != '=')
			return -1;
	for (i = 0; i < n - pad; i++) {
		v = st_b64(s[i]);
		if (v < 0)
			return -1;
		acc = acc << 6 | v;
		bits += 6;
		if (bits >= 8) {
			bits -= 8;
			out[got++] = acc >> bits;
			acc &= (1u << bits) - 1;
		}
	}
	return got == want && !acc ? 0 : -1;
}

static int st_sig_decode(const char *s, size_t n, unsigned char *sig)
{
	return st_b64_exact(s, n, sig, 64);
}

/* crypto/ed25519: S, the second half of the signature, must be below L. */
static int st_s_canonical(const unsigned char *s)
{
	int i;

	for (i = 31; i >= 0; i--) {
		if (s[i] != st_order[i])
			return s[i] < st_order[i];
	}
	return 0;
}

int st_verify(const char *text, size_t len, const unsigned char (*keys)[32], int nkeys,
	      const char *kind, struct st_statement *st)
{
	/* sig || message for crypto_sign_open, and the room it copies them into. */
	static unsigned char sm[64 + ST_SIG_MAX], m[64 + ST_SIG_MAX];
	const char *val[5], *p, *end, *sp;
	size_t vlen[5], line = len, signed_len = 0, i, klen;
	unsigned long long mlen;
	int f, k, hi, lo;

	memset(st, 0, sizeof(*st));
	if (len > ST_SIG_MAX)
		return ST_NOT_STATEMENT;
	/* strings.TrimSuffix(strings.TrimSuffix(sigText, "\n"), "\r") */
	if (line && text[line - 1] == '\n')
		line--;
	if (line && text[line - 1] == '\r')
		line--;
	if (line < ST_PREFIX_LEN || memcmp(text, ST_PREFIX, ST_PREFIX_LEN))
		return ST_NOT_STATEMENT;
	p = text + ST_PREFIX_LEN;
	end = text + line;
	for (sp = p; sp < end; sp++)
		if (*sp == '\r' || *sp == '\n' || *sp == '\t')
			return ST_NOT_STATEMENT;
	/* strings.Split(rest, " ") into exactly five fields, each opening with its key. */
	for (f = 0; f < 5; f++) {
		sp = f < 4 ? memchr(p, ' ', end - p) : end;
		if (!sp)
			return ST_NOT_STATEMENT;
		klen = strlen(st_keys[f]);
		if ((size_t)(sp - p) < klen || memcmp(p, st_keys[f], klen))
			return ST_NOT_STATEMENT;
		val[f] = p + klen;
		vlen[f] = sp - val[f];
		if (f == 3)
			signed_len = sp - text;	/* everything before " sig=" */
		p = sp + 1;
	}
	if (memchr(val[4], ' ', vlen[4]))
		return ST_NOT_STATEMENT;
	/* sha256: hex.DecodeString, lowercase only. */
	if (vlen[3] % 2)
		return ST_NOT_STATEMENT;
	for (i = 0; i < vlen[3]; i++)
		if (st_hex(val[3][i]) < 0)
			return ST_NOT_STATEMENT;
	/* Statement.valid() */
	if (!st_is(val[0], vlen[0], "core") && !st_is(val[0], vlen[0], "app"))
		return ST_NOT_STATEMENT;
	if (!st_is(val[1], vlen[1], "production") && !st_is(val[1], vlen[1], "alpha"))
		return ST_NOT_STATEMENT;
	if (vlen[3] != 2 * sizeof(st->digest))
		return ST_NOT_STATEMENT;
	if (!(st_is(val[2], vlen[2], "stock") && st_is(val[0], vlen[0], "core")) &&
	    !st_valid_version(val[2], vlen[2]))
		return ST_NOT_STATEMENT;
	/* Each field is now one of a few words or bounded by its rule, and fits. */
	memcpy(st->kind, val[0], vlen[0]);
	memcpy(st->profile, val[1], vlen[1]);
	memcpy(st->version, val[2], vlen[2]);
	for (i = 0; i < sizeof(st->digest); i++) {
		hi = st_hex(val[3][2 * i]);
		lo = st_hex(val[3][2 * i + 1]);
		st->digest[i] = hi << 4 | lo;
	}
	if (st_sig_decode(val[4], vlen[4], sm))
		return ST_BAD_ENCODING;
	/*
	 * The statement text the signature covers is s.String(): with every field checked
	 * above to be in its one canonical spelling, that is the line up to " sig=" as read.
	 */
	memcpy(sm + 64, text, signed_len);
	if (nkeys < 1 || !st_s_canonical(sm + 32))
		return ST_BAD_SIGNATURE;
	for (k = 0; k < nkeys; k++)
		if (!crypto_sign_ed25519_tweet_open(m, &mlen, sm, 64 + signed_len, keys[k]))
			break;
	if (k == nkeys)
		return ST_BAD_SIGNATURE;
	if (strcmp(st->kind, kind))
		return ST_WRONG_KIND;
	return ST_OK;
}

int st_stick_policy(const struct st_statement *st, const char *image_profile)
{
	if (!strcmp(st->version, "stock"))
		return ST_STOCK;
	/* Only an image that says alpha takes alpha; anything else is held to production. */
	if (strcmp(image_profile, "alpha") && strcmp(st->profile, "production"))
		return ST_WRONG_PROFILE;
	return ST_OK;
}

int st_matches(const struct st_statement *st, const unsigned char *digest)
{
	return memcmp(st->digest, digest, sizeof(st->digest)) ? ST_WRONG_FILE : ST_OK;
}

const char *st_result_text(int result)
{
	switch (result) {
	case ST_OK:
		return "signed";
	case ST_NOT_STATEMENT:
		return "not a SoundTap package statement";
	case ST_BAD_ENCODING:
		return "the signature is not valid base64";
	case ST_BAD_SIGNATURE:
		return "the signature does not verify against the release keys";
	case ST_WRONG_KIND:
		return "signed as an app, not SoundTap Core";
	case ST_WRONG_PROFILE:
		return "an alpha package, and this is not an alpha image";
	case ST_STOCK:
		return "signed for the way back to Bose, which the stick does not take";
	case ST_WRONG_FILE:
		return "Update.stu is not the file the signature names";
	}
	return "refused";
}
