//go:build (tinygo || force_tinygo_logic) && (linux || (darwin && darwinstarttlswith13))

// RSASSA-PKCS1-v1_5 signing over the vendored mbedTLS.
//
// This costs no new object code: MBEDTLS_RSA_C, PKCS1_V15, PK_C, PK_PARSE_C,
// ASN1_PARSE_C, PEM_PARSE_C, MD_C and SHA256_C are all already enabled for TLS,
// so pk.c, pkparse.c and rsa.c are in the build either way. The entropy source
// is the same one tls_mbedtls.c seeds from, through mbedtls_entropy_func and
// the platform hardware poll defined there.

#include <stdlib.h>
#include <string.h>

#include "rsasign_mbedtls.h"

#include "mbedtls/ctr_drbg.h"
#include "mbedtls/entropy.h"
#include "mbedtls/md.h"
#include "mbedtls/pk.h"

// The personalization string keeps two DRBGs seeded from the same entropy pool
// from producing the same stream. PKCS#1 v1.5 signing is deterministic, so
// this affects only blinding, but the cost of getting it right is one literal.
static const char rsasign_pers[] = "tinygodriver-rsasign";

typedef struct {
	mbedtls_pk_context pk;
	mbedtls_entropy_context entropy;
	mbedtls_ctr_drbg_context drbg;
} rsasign_key;

int mbedtls_rsasign_load(const unsigned char *key_der, size_t key_len, void **out_key) {
	rsasign_key *k = calloc(1, sizeof(rsasign_key));
	if (k == NULL) {
		return MBED_RSASIGN_ERR_ALLOC;
	}
	mbedtls_pk_init(&k->pk);
	mbedtls_entropy_init(&k->entropy);
	mbedtls_ctr_drbg_init(&k->drbg);

	int rc = mbedtls_ctr_drbg_seed(&k->drbg, mbedtls_entropy_func, &k->entropy,
	                               (const unsigned char *)rsasign_pers,
	                               sizeof(rsasign_pers) - 1);
	if (rc != 0) {
		mbedtls_ctr_drbg_free(&k->drbg);
		mbedtls_entropy_free(&k->entropy);
		mbedtls_pk_free(&k->pk);
		free(k);
		return MBED_RSASIGN_ERR_SEED;
	}

	// mbedTLS 3.x wants an RNG here because a parsed key may need blinding
	// material derived at load time.
	rc = mbedtls_pk_parse_key(&k->pk, key_der, key_len, NULL, 0,
	                          mbedtls_ctr_drbg_random, &k->drbg);
	if (rc != 0) {
		mbedtls_ctr_drbg_free(&k->drbg);
		mbedtls_entropy_free(&k->entropy);
		mbedtls_pk_free(&k->pk);
		free(k);
		return MBED_RSASIGN_ERR_PARSE;
	}
	if (!mbedtls_pk_can_do(&k->pk, MBEDTLS_PK_RSA)) {
		mbedtls_ctr_drbg_free(&k->drbg);
		mbedtls_entropy_free(&k->entropy);
		mbedtls_pk_free(&k->pk);
		free(k);
		return MBED_RSASIGN_ERR_NOT_RSA;
	}

	*out_key = (void *)k;
	return MBED_RSASIGN_OK;
}

int mbedtls_rsasign_bits(void *key) {
	if (key == NULL) {
		return MBED_RSASIGN_ERR_PARSE;
	}
	rsasign_key *k = (rsasign_key *)key;
	return (int)mbedtls_pk_get_bitlen(&k->pk);
}

int mbedtls_rsasign_sign(void *key, const unsigned char *digest, size_t digest_len,
                         unsigned char *out, size_t out_cap, size_t *out_len) {
	if (key == NULL) {
		return MBED_RSASIGN_ERR_SIGN;
	}
	rsasign_key *k = (rsasign_key *)key;

	// mbedtls_pk_sign writes at most MBEDTLS_PK_SIGNATURE_MAX_SIZE, which can
	// exceed the caller's buffer, so it signs into a local one and copies only
	// after the length is known.
	unsigned char sig[MBEDTLS_PK_SIGNATURE_MAX_SIZE];
	size_t olen = 0;
	int rc = mbedtls_pk_sign(&k->pk, MBEDTLS_MD_SHA256, digest, digest_len,
	                         sig, sizeof(sig), &olen,
	                         mbedtls_ctr_drbg_random, &k->drbg);
	if (rc != 0) {
		return MBED_RSASIGN_ERR_SIGN;
	}
	if (olen > out_cap) {
		return MBED_RSASIGN_ERR_SPACE;
	}
	memcpy(out, sig, olen);
	*out_len = olen;
	return MBED_RSASIGN_OK;
}

void mbedtls_rsasign_free(void *key) {
	if (key == NULL) {
		return;
	}
	rsasign_key *k = (rsasign_key *)key;
	mbedtls_pk_free(&k->pk);
	mbedtls_ctr_drbg_free(&k->drbg);
	mbedtls_entropy_free(&k->entropy);
	free(k);
}
