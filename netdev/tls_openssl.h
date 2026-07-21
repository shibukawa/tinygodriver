#include <stdint.h>
#include <stdlib.h>

#define SSL_VERIFY_PEER_VALUE 1
#define SSL_CTRL_SET_TLSEXT_HOSTNAME_VALUE 55
#define TLSEXT_NAMETYPE_host_name_VALUE 0

typedef struct ssl_ctx_st SSL_CTX;
typedef struct ssl_st SSL;
typedef struct ssl_method_st SSL_METHOD;

const SSL_METHOD *TLS_client_method(void);
SSL_CTX *SSL_CTX_new(const SSL_METHOD *);
void SSL_CTX_free(SSL_CTX *);
void SSL_CTX_set_verify(SSL_CTX *, int, void *);
int SSL_CTX_set_default_verify_paths(SSL_CTX *);
SSL *SSL_new(SSL_CTX *);
void SSL_free(SSL *);
int SSL_set_fd(SSL *, int);
long SSL_ctrl(SSL *, int, long, void *);
int SSL_set1_host(SSL *, const char *);
int SSL_connect(SSL *);
int SSL_read(SSL *, void *, int);
int SSL_write(SSL *, const void *, int);
int SSL_shutdown(SSL *);
int SSL_get_error(const SSL *, int);
long SSL_get_verify_result(const SSL *);

typedef struct {
	SSL_CTX *ctx;
	SSL *ssl;
} netdev_tls;

// Returns zero on success and a stable negative error code on failure.
static int netdev_tls_connect(int fd, const char *hostname, uintptr_t *out) {
	*out = 0;
	netdev_tls *state = (netdev_tls *)calloc(1, sizeof(netdev_tls));
	if (state == NULL) return -101;
	state->ctx = SSL_CTX_new(TLS_client_method());
	if (state->ctx == NULL) goto ctx_error;
	SSL_CTX_set_verify(state->ctx, SSL_VERIFY_PEER_VALUE, NULL);
	if (SSL_CTX_set_default_verify_paths(state->ctx) != 1) goto verify_paths_error;
	state->ssl = SSL_new(state->ctx);
	if (state->ssl == NULL) goto ssl_error;
	if (SSL_set_fd(state->ssl, fd) != 1) goto setup_error;
	if (hostname == NULL || hostname[0] == '\0') goto setup_error;
	if (SSL_ctrl(state->ssl, SSL_CTRL_SET_TLSEXT_HOSTNAME_VALUE,
		TLSEXT_NAMETYPE_host_name_VALUE, (void *)hostname) != 1) goto setup_error;
	if (SSL_set1_host(state->ssl, hostname) != 1) goto setup_error;
	int rc = SSL_connect(state->ssl);
	if (rc != 1) {
		int ssl_error = SSL_get_error(state->ssl, rc);
		SSL_free(state->ssl);
		SSL_CTX_free(state->ctx);
		free(state);
		return -200 - ssl_error;
	}
	if (SSL_get_verify_result(state->ssl) != 0) goto verify_error;
	*out = (uintptr_t)state;
	return 0;

verify_error:
	SSL_shutdown(state->ssl);
setup_error:
	SSL_free(state->ssl);
ssl_error:
	SSL_CTX_free(state->ctx);
verify_paths_error:
ctx_error:
	free(state);
	return -102;
}

static int netdev_tls_read(uintptr_t raw, void *buf, int len, int *out) {
	netdev_tls *state = (netdev_tls *)raw;
	if (state == NULL || state->ssl == NULL) return -300;
	int rc = SSL_read(state->ssl, buf, len);
	if (rc > 0) { *out = rc; return 0; }
	int ssl_error = SSL_get_error(state->ssl, rc);
	if (ssl_error == 6) { *out = 0; return 0; }
	return -400 - ssl_error;
}

static int netdev_tls_write(uintptr_t raw, const void *buf, int len, int *out) {
	netdev_tls *state = (netdev_tls *)raw;
	if (state == NULL || state->ssl == NULL) return -300;
	int rc = SSL_write(state->ssl, buf, len);
	if (rc > 0) { *out = rc; return 0; }
	return -400 - SSL_get_error(state->ssl, rc);
}

static void netdev_tls_close(uintptr_t raw) {
	netdev_tls *state = (netdev_tls *)raw;
	if (state == NULL) return;
	if (state->ssl != NULL) {
		SSL_shutdown(state->ssl);
		SSL_free(state->ssl);
	}
	if (state->ctx != NULL) SSL_CTX_free(state->ctx);
	free(state);
}
