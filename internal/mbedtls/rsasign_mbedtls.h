//go:build (tinygo || force_tinygo_logic) && (linux || (darwin && darwinstarttlswith13))

#ifndef TINYGODRIVER_RSASIGN_MBEDTLS_H
#define TINYGODRIVER_RSASIGN_MBEDTLS_H

#include <stddef.h>

// Every entry point returns 0 on success and a stable negative code otherwise,
// and writes its out-parameters only on success. The key is an opaque handle,
// freed by exactly one close path. C never calls into Go.
#define MBED_RSASIGN_OK 0
#define MBED_RSASIGN_ERR_ALLOC -1
#define MBED_RSASIGN_ERR_SEED -2
#define MBED_RSASIGN_ERR_PARSE -3
#define MBED_RSASIGN_ERR_NOT_RSA -4
#define MBED_RSASIGN_ERR_SIGN -5
#define MBED_RSASIGN_ERR_SPACE -6

// key_der is a PKCS#8 PrivateKeyInfo in DER. mbedTLS sniffs PEM against DER,
// so the container needs no unwrapping here, unlike the Security.framework
// backend which accepts PKCS#1 only.
int mbedtls_rsasign_load(const unsigned char *key_der, size_t key_len, void **out_key);

// Returns the modulus size in bits, or a negative code.
int mbedtls_rsasign_bits(void *key);

int mbedtls_rsasign_sign(void *key, const unsigned char *digest, size_t digest_len,
                         unsigned char *out, size_t out_cap, size_t *out_len);

void mbedtls_rsasign_free(void *key);

#endif
