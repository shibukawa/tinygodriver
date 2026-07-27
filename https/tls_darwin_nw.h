#ifndef TINYGODRIVER_HTTPS_TLS_DARWIN_NW_H
#define TINYGODRIVER_HTTPS_TLS_DARWIN_NW_H

#include <stdint.h>

// Error classes returned by the functions below. A zero return means success.
// The OSStatus reported by Network.framework is written to *status so the Go
// side can map it onto a sentinel error.
#define HTTPS_NW_OK 0
#define HTTPS_NW_ERR_ALLOC -1
#define HTTPS_NW_ERR_SETUP -2
#define HTTPS_NW_ERR_HANDSHAKE -3
#define HTTPS_NW_ERR_TIMEOUT -4
#define HTTPS_NW_ERR_IO -5
#define HTTPS_NW_ERR_CLOSED -6

// Trust anchor list. Built on the Go side from DER certificates, because PEM
// decoding stays in Go (see config.go rootCADER).
uintptr_t https_nw_calist_new(void);
int https_nw_calist_add(uintptr_t list, const void *der, int len);
void https_nw_calist_free(uintptr_t list);

// https_nw_dial performs DNS, TCP, and the TLS handshake, blocking until the
// connection is ready or fails. calist may be 0. On success the connection
// takes ownership of calist.
int https_nw_dial(const char *host, const char *port, const char *server_name,
                  int skip_verify, uintptr_t calist, int ca_only,
                  int min_version, int64_t timeout_ns,
                  uintptr_t *out, int *status);

int https_nw_send(uintptr_t handle, const void *buf, int len,
                  int64_t timeout_ns, int *status);

// On success *n holds the byte count; *n == 0 means end of stream.
int https_nw_recv(uintptr_t handle, void *buf, int len,
                  int64_t timeout_ns, int *n, int *status);

void https_nw_close(uintptr_t handle);

#endif
