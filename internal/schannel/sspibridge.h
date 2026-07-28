#ifndef TINYGODRIVER_SSPIBRIDGE_H
#define TINYGODRIVER_SSPIBRIDGE_H

// Named sspibridge rather than schannel because cgo puts the package directory
// on the include path, so a local schannel.h would shadow the mingw-w64 header
// of that name that sspibridge.c needs.

#include <stdint.h>

// Error classes. Zero is success. The SECURITY_STATUS or HRESULT is reported
// separately so the Go side can map it onto a sentinel error.
#define HTTPS_SC_OK 0
#define HTTPS_SC_ERR_ALLOC -1
#define HTTPS_SC_ERR_SETUP -2
#define HTTPS_SC_ERR_CA -3
#define HTTPS_SC_ERR_CLIENT_CERT -4
#define HTTPS_SC_ERR_HANDSHAKE -5
#define HTTPS_SC_ERR_TIMEOUT -6
#define HTTPS_SC_ERR_IO -7
#define HTTPS_SC_ERR_CLOSED -8

// Minimum TLS version, given as the wire value so the Go side can pass
// https.Version straight through.
#define HTTPS_SC_TLS12 0x0303
#define HTTPS_SC_TLS13 0x0304

// Trust anchor store, built from DER certificates on the Go side. This is an
// in-memory HCERTSTORE; nothing is written to the user's certificate store.
uintptr_t https_sc_calist_new(void);
int https_sc_calist_add(uintptr_t list, const void *der, int len);
void https_sc_calist_free(uintptr_t list);

// Builds a client credential from a DER certificate and its DER private key.
// key_pkcs8 tells the decoder whether the key is a PKCS#8 PrivateKeyInfo
// rather than a bare PKCS#1 RSAPrivateKey.
//
// The private key is imported as an ephemeral CNG key, so no key container is
// created in the user's profile and nothing outlives the process.
int https_sc_clientcert_new(const void *cert_der, int cert_len,
                            const void *key_der, int key_len, int key_pkcs8,
                            uintptr_t *out, int *status);
void https_sc_clientcert_free(uintptr_t cert);

// Performs the TLS handshake over an already connected socket. The socket
// stays owned by Go; this layer only reads and writes it.
//
// Schannel is a buffer transformer: it never sees the socket, it only produces
// and consumes token bytes. The socket may therefore already have carried
// plaintext, which is what an in-band upgrade such as the PostgreSQL or MySQL
// STARTTLS exchange needs.
//
// Ownership of calist transfers to the connection only on success. On failure
// the caller still owns it and must call https_sc_calist_free. clientcert is
// borrowed for the lifetime of the connection and is never freed here.
int https_sc_handshake(uintptr_t sock, const char *host, int skip_verify,
                       uintptr_t calist, int ca_only, int min_version,
                       uintptr_t clientcert, int64_t timeout_ns,
                       uintptr_t *out, int *status);

// *n receives the byte count. *n == 0 with a zero return means clean EOF.
int https_sc_read(uintptr_t handle, void *buf, int len,
                  int64_t timeout_ns, int *n, int *status);

int https_sc_write(uintptr_t handle, const void *buf, int len,
                   int64_t timeout_ns, int *n, int *status);

void https_sc_close(uintptr_t handle);

#endif
