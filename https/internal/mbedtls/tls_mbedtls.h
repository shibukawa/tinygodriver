//go:build (tinygo || force_tinygo_logic) && linux

#ifndef TINYGODRIVER_TLS_MBEDTLS_H
#define TINYGODRIVER_TLS_MBEDTLS_H

#include <stdint.h>

// Error classes. Zero is success. The mbedTLS status is reported separately in
// *tls_err so the Go side can map it onto a sentinel error, and *verify_flags
// carries the X.509 bitmask, which is the only way to tell an expired
// certificate from a name mismatch.
#define HTTPS_MBED_OK 0
#define HTTPS_MBED_ERR_ALLOC -1
#define HTTPS_MBED_ERR_SETUP -2
#define HTTPS_MBED_ERR_CA -3
#define HTTPS_MBED_ERR_CLIENT_CERT -4
#define HTTPS_MBED_ERR_HANDSHAKE -5
#define HTTPS_MBED_ERR_TIMEOUT -6
#define HTTPS_MBED_ERR_IO -7
#define HTTPS_MBED_ERR_CLOSED -8

// Performs the TLS handshake over an already connected socket. The fd stays
// owned by Go; this layer only reads and writes it.
//
// ca_pem, cert_pem and key_pem are PEM and must include the terminating NUL in
// their length, which is what mbedtls_x509_crt_parse expects.
int https_mbed_handshake(int fd, const char *host, int skip_verify,
                         int min_version,
                         const unsigned char *ca_pem, int ca_len,
                         const unsigned char *cert_pem, int cert_len,
                         const unsigned char *key_pem, int key_len,
                         int64_t timeout_ns,
                         uintptr_t *out, int *tls_err,
                         unsigned int *verify_flags);

// *n receives the byte count. *n == 0 with a zero return means clean EOF.
int https_mbed_read(uintptr_t handle, void *buf, int len,
                    int64_t timeout_ns, int *n, int *tls_err);

int https_mbed_write(uintptr_t handle, const void *buf, int len,
                     int64_t timeout_ns, int *n, int *tls_err);

void https_mbed_close(uintptr_t handle);

// Runs the mbedTLS known-answer self tests for the primitives this package
// uses. Zero means every one passed. This is what validates the bundled
// arm_neon.h, so it is exposed for the test suite rather than kept internal.
int https_mbed_self_test(void);

// Kernel-reported CPU crypto features: bit0 AES, bit1 SHA-256, bit2 SHA-512.
// Returns -1 where the concept does not apply.
int https_mbed_hwcaps(void);

#endif
