/* SPDX-License-Identifier: GPL-2.0+ */
/*
 * The signed package statement of ADR 0048 for U-Boot's USB-stick recovery
 * (uboot-2026.07-usb-recovery.patch). soundtap-sig.c says how it is read.
 *
 * Built into U-Boot and, by app/internal/release/uboot_test.go, on the host, so it
 * includes nothing but the C library's types and string functions.
 */
#ifndef SOUNDTAP_SIG_H
#define SOUNDTAP_SIG_H

#ifdef __UBOOT__
#include <linux/string.h>
#include <linux/types.h>
#else
#include <stddef.h>
#include <string.h>
#endif

/* release.SigMax: a statement with its signature is about 210 bytes. */
#define ST_SIG_MAX		1024

/* release.OwnerKeyEnv: the environment variable that holds the owner's own key. */
#define ST_OWNER_KEY_ENV	"soundtap_owner_key"

/* What a statement says, once it verifies. The strings are NUL terminated. */
struct st_statement {
	char kind[8];		/* core or app */
	char profile[16];	/* production or alpha */
	char version[48];	/* MAJOR.MINOR.PATCH, MAJOR.MINOR.PATCH-alpha.N, or stock */
	unsigned char digest[32];	/* the package's SHA-256 */
};

/* Why a statement was refused. 0 is accepted. */
enum st_result {
	ST_OK = 0,
	ST_NOT_STATEMENT,	/* not a v1 statement, or a field that is not valid */
	ST_BAD_ENCODING,	/* sig= is not 64 bytes of padded standard base64 */
	ST_BAD_SIGNATURE,	/* no release key, nor the owner key, verifies it */
	ST_WRONG_KIND,		/* signed, but for another route than the caller's */
	ST_WRONG_PROFILE,	/* an alpha package on a production image */
	ST_STOCK,		/* a Bose package signed for the way back: not a stick's */
	ST_WRONG_FILE,		/* the package does not hash to the statement */
};

/*
 * release.Verify: text is the whole .sig file, len bytes of it, keys the trusted keys
 * (st_trusted_keys over soundtap-keys.h, which leaves out an empty one as Go skips it). Fills *st and returns
 * ST_OK when one key verifies it and it is of the given kind.
 */
int st_verify(const char *text, size_t len, const unsigned char (*keys)[32], int nkeys,
	      const char *kind, struct st_statement *st);

/*
 * The owner key from the environment's value (ADR 0048): the 32 raw bytes of
 * padded standard base64, 44 characters, unused bits zero, nothing around them, and not a
 * point of small order. 0 when value is such a key, -1 for anything else, NULL included.
 */
int st_owner_key(const char *value, unsigned char *key);

/*
 * release.TrustedKeys: the nrelease release keys, and the owner key when owner is one,
 * copied into keys, which has room for nrelease + 1. Returns how many there are. A value
 * that is not a key is left out, so the release keys keep working.
 */
int st_trusted_keys(const unsigned char (*release)[32], int nrelease, const char *owner,
		    unsigned char (*keys)[32]);

/*
 * What the stick takes on top of a verified core statement (ADR 0048): any SoundTap
 * version, older ones included, but not stock, and not alpha on an image whose profile is
 * not alpha.
 */
int st_stick_policy(const struct st_statement *st, const char *image_profile);

/* ST_OK when digest, the package's SHA-256, is the one *st names. */
int st_matches(const struct st_statement *st, const unsigned char *digest);

/* A few words for the console. */
const char *st_result_text(int result);

#endif
