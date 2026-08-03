//go:build (tinygo || force_tinygo_logic) && (darwin || windows)

#ifndef TINYGODRIVER_RSASIGN_H
#define TINYGODRIVER_RSASIGN_H

#include <stddef.h>

// Every entry point returns 0 on success and a stable negative code otherwise,
// and writes its out-parameters only on success. The key is an opaque handle,
// freed by exactly one close path. C never calls into Go and never retains a Go
// pointer past the call that received it.
#define RSASIGN_OK 0
#define RSASIGN_ERR_LOAD -1
#define RSASIGN_ERR_SIGN -2
#define RSASIGN_ERR_SPACE -3

// key_der is a PKCS#1 RSAPrivateKey.
int rsasign_load(const unsigned char *key_der, size_t key_len, void **out_key);

int rsasign_sign(void *key, const unsigned char *digest, size_t digest_len,
                 unsigned char *out, size_t out_cap, size_t *out_len);

void rsasign_free(void *key);

#endif
