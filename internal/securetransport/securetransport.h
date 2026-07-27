#ifndef TINYGODRIVER_SECURETRANSPORT_H
#define TINYGODRIVER_SECURETRANSPORT_H

#include <stdint.h>

// Error classes. Zero is success. The OSStatus is reported separately so the
// Go side can map it onto a sentinel error.
#define HTTPS_ST_OK 0
#define HTTPS_ST_ERR_ALLOC -1
#define HTTPS_ST_ERR_SETUP -2
#define HTTPS_ST_ERR_CA -3
#define HTTPS_ST_ERR_CLIENT_CERT -4
#define HTTPS_ST_ERR_HANDSHAKE -5
#define HTTPS_ST_ERR_TIMEOUT -6
#define HTTPS_ST_ERR_IO -7
#define HTTPS_ST_ERR_CLOSED -8

// Trust anchor list, built from DER certificates on the Go side.
uintptr_t https_st_calist_new(void);
int https_st_calist_add(uintptr_t list, const void *der, int len);
void https_st_calist_free(uintptr_t list);

// Performs the TLS handshake over an already connected socket. The descriptor
// stays owned by Go; this layer only reads and writes it.
//
// Because Secure Transport is a byte transformer with caller-supplied I/O, the
// socket may already have carried plaintext, which is what an in-band upgrade
// such as the PostgreSQL or MySQL STARTTLS exchange needs.
//
// Ownership of calist transfers to the connection only on success. On failure
// the caller still owns it and must call https_st_calist_free.
int https_st_handshake(int fd, const char *host, int skip_verify,
                       uintptr_t calist, int ca_only, int min_version,
                       int64_t timeout_ns, uintptr_t *out, int *status);

// *n receives the byte count. *n == 0 with a zero return means clean EOF.
int https_st_read(uintptr_t handle, void *buf, int len,
                  int64_t timeout_ns, int *n, int *status);

int https_st_write(uintptr_t handle, const void *buf, int len,
                   int64_t timeout_ns, int *n, int *status);

void https_st_close(uintptr_t handle);

#endif
