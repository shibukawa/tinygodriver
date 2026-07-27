//go:build (tinygo || force_tinygo_logic) && darwin && darwintls13

#include "tls_darwin_nw.h"

// Network.framework, Security, CoreFoundation, and libdispatch are all
// declared by hand here. TinyGo compiles cgo C files with -nostdlibinc against
// a minimal macOS SDK and rejects -F/-iframework in CFLAGS, so system headers
// for these frameworks cannot be included. Declaring the small surface we use
// also decouples the build from the installed SDK version. netdev/tls_openssl.h
// takes the same approach for OpenSSL.

#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

// ---------------------------------------------------------------- libdispatch

typedef void *dispatch_queue_t;
typedef void *dispatch_semaphore_t;
typedef void *dispatch_data_t;
typedef uint64_t dispatch_time_t;

#define DISPATCH_TIME_NOW 0ull
#define DISPATCH_DATA_DESTRUCTOR_DEFAULT NULL

extern dispatch_queue_t dispatch_queue_create(const char *label, void *attr);
extern dispatch_queue_t dispatch_get_global_queue(intptr_t identifier, uintptr_t flags);
extern dispatch_semaphore_t dispatch_semaphore_create(intptr_t value);
extern intptr_t dispatch_semaphore_wait(dispatch_semaphore_t sema, dispatch_time_t timeout);
extern intptr_t dispatch_semaphore_signal(dispatch_semaphore_t sema);
extern dispatch_time_t dispatch_time(dispatch_time_t base, int64_t delta);
extern dispatch_data_t dispatch_data_create(const void *buffer, size_t size,
                                            dispatch_queue_t queue, void *destructor);
extern dispatch_data_t dispatch_data_create_map(dispatch_data_t data,
                                                const void **buffer_ptr, size_t *size_ptr);
extern void dispatch_release(void *object);
extern void os_release(void *object);

// ------------------------------------------------------------ CoreFoundation

typedef void *CFTypeRef;
typedef void *CFAllocatorRef;
typedef void *CFDataRef;
typedef void *CFArrayRef;
typedef void *CFMutableArrayRef;
typedef void *CFErrorRef;
typedef long CFIndex;

// Declared as an array so the identifier yields the address of the callback
// struct, which is what CFArrayCreateMutable expects.
extern const uint8_t kCFTypeArrayCallBacks[];

extern CFDataRef CFDataCreate(CFAllocatorRef allocator, const uint8_t *bytes, CFIndex length);
extern CFMutableArrayRef CFArrayCreateMutable(CFAllocatorRef allocator, CFIndex capacity,
                                              const void *callbacks);
extern void CFArrayAppendValue(CFMutableArrayRef array, const void *value);
extern CFIndex CFArrayGetCount(CFArrayRef array);
extern void CFRelease(CFTypeRef cf);

// ------------------------------------------------------------------ Security

typedef void *SecCertificateRef;
typedef void *SecTrustRef;
typedef void *sec_trust_t;
typedef void *sec_protocol_options_t;
typedef void *sec_protocol_metadata_t;

extern SecCertificateRef SecCertificateCreateWithData(CFAllocatorRef allocator, CFDataRef data);
extern int32_t SecTrustSetAnchorCertificates(SecTrustRef trust, CFArrayRef anchors);
extern int32_t SecTrustSetAnchorCertificatesOnly(SecTrustRef trust, unsigned char only);
extern bool SecTrustEvaluateWithError(SecTrustRef trust, CFErrorRef *error);

extern SecTrustRef sec_trust_copy_ref(sec_trust_t trust);
extern void sec_protocol_options_set_verify_block(sec_protocol_options_t options,
                                                  void *verify_block, dispatch_queue_t queue);
extern void sec_protocol_options_set_tls_server_name(sec_protocol_options_t options,
                                                     const char *server_name);
extern void sec_protocol_options_set_min_tls_protocol_version(sec_protocol_options_t options,
                                                              uint16_t version);

// ----------------------------------------------------------------- Network

typedef void *nw_endpoint_t;
typedef void *nw_parameters_t;
typedef void *nw_connection_t;
typedef void *nw_protocol_options_t;
typedef void *nw_content_context_t;
typedef void *nw_error_t;

enum {
	nw_connection_state_invalid = 0,
	nw_connection_state_waiting = 1,
	nw_connection_state_preparing = 2,
	nw_connection_state_ready = 3,
	nw_connection_state_failed = 4,
	nw_connection_state_cancelled = 5,
};

extern void *const _nw_parameters_configure_protocol_default_configuration;
extern const nw_content_context_t _nw_content_context_default_message;

extern nw_endpoint_t nw_endpoint_create_host(const char *hostname, const char *port);
extern nw_parameters_t nw_parameters_create_secure_tcp(void *configure_tls, void *configure_tcp);
extern nw_connection_t nw_connection_create(nw_endpoint_t endpoint, nw_parameters_t parameters);
extern void nw_connection_set_queue(nw_connection_t connection, dispatch_queue_t queue);
extern void nw_connection_set_state_changed_handler(nw_connection_t connection, void *handler);
extern void nw_connection_start(nw_connection_t connection);
extern void nw_connection_cancel(nw_connection_t connection);
extern void nw_connection_send(nw_connection_t connection, dispatch_data_t content,
                               nw_content_context_t context, bool is_complete, void *completion);
extern void nw_connection_receive(nw_connection_t connection, uint32_t minimum,
                                  uint32_t maximum, void *completion);
extern int nw_error_get_error_code(nw_error_t error);
extern sec_protocol_options_t nw_tls_copy_sec_protocol_options(nw_protocol_options_t options);

// ------------------------------------------------------------------- state

typedef struct {
	nw_connection_t conn;
	dispatch_queue_t queue;
	CFMutableArrayRef anchors; // retained trust anchors, freed on close
	uint8_t *pending;          // bytes from a receive larger than the caller's buffer
	size_t pending_len;
	size_t pending_off;
	int eof;
} https_nw_conn;

uintptr_t https_nw_calist_new(void) {
	return (uintptr_t)CFArrayCreateMutable(NULL, 0, kCFTypeArrayCallBacks);
}

int https_nw_calist_add(uintptr_t list, const void *der, int len) {
	CFMutableArrayRef array = (CFMutableArrayRef)list;
	if (array == NULL || der == NULL || len <= 0) return HTTPS_NW_ERR_SETUP;

	CFDataRef data = CFDataCreate(NULL, (const uint8_t *)der, (CFIndex)len);
	if (data == NULL) return HTTPS_NW_ERR_ALLOC;

	SecCertificateRef cert = SecCertificateCreateWithData(NULL, data);
	CFRelease(data);
	if (cert == NULL) return HTTPS_NW_ERR_SETUP; // not a valid DER certificate

	CFArrayAppendValue(array, cert);
	CFRelease(cert); // the array retains it
	return HTTPS_NW_OK;
}

void https_nw_calist_free(uintptr_t list) {
	if (list != 0) CFRelease((CFTypeRef)list);
}

int https_nw_dial(const char *host, const char *port, const char *server_name,
                  int skip_verify, uintptr_t calist, int ca_only,
                  int min_version, int64_t timeout_ns,
                  uintptr_t *out, int *status) {
	*out = 0;
	*status = 0;

	CFMutableArrayRef anchors = (CFMutableArrayRef)calist;
	bool custom_anchors = anchors != NULL && CFArrayGetCount(anchors) > 0;

	nw_endpoint_t endpoint = nw_endpoint_create_host(host, port);
	if (endpoint == NULL) return HTTPS_NW_ERR_SETUP;

	// Blocks below run on libdispatch threads. Per rule:cgo-bridge-contract
	// they never call into Go; they only touch C memory and signal semaphores.
	void (^configure_tls)(nw_protocol_options_t) = ^(nw_protocol_options_t options) {
		sec_protocol_options_t sec = nw_tls_copy_sec_protocol_options(options);
		if (sec == NULL) return;

		if (server_name != NULL && server_name[0] != '\0') {
			sec_protocol_options_set_tls_server_name(sec, server_name);
		}
		if (min_version != 0) {
			sec_protocol_options_set_min_tls_protocol_version(sec, (uint16_t)min_version);
		}

		if (skip_verify) {
			sec_protocol_options_set_verify_block(
				sec,
				(void *)^(sec_protocol_metadata_t metadata, sec_trust_t trust,
				          void (^complete)(bool)) {
					(void)metadata;
					(void)trust;
					complete(true);
				},
				dispatch_get_global_queue(0, 0));
		} else if (custom_anchors || ca_only) {
			sec_protocol_options_set_verify_block(
				sec,
				(void *)^(sec_protocol_metadata_t metadata, sec_trust_t trust,
				          void (^complete)(bool)) {
					(void)metadata;
					SecTrustRef ref = sec_trust_copy_ref(trust);
					if (ref == NULL) {
						complete(false);
						return;
					}
					// The trust object already carries an SSL policy bound to
					// the hostname, so evaluation still checks the name.
					// anchors may be NULL when ca_only was requested with no
					// certificates. That must trust nothing, not fall back to
					// the system store, which is what the other backends do.
					SecTrustSetAnchorCertificates(ref, anchors);
					SecTrustSetAnchorCertificatesOnly(ref, ca_only ? 1 : 0);
					CFErrorRef err = NULL;
					bool ok = SecTrustEvaluateWithError(ref, &err);
					if (err != NULL) CFRelease(err);
					CFRelease(ref);
					complete(ok);
				},
				dispatch_get_global_queue(0, 0));
		}
		// Otherwise leave the framework's default verification in place.

		os_release(sec);
	};

	nw_parameters_t params = nw_parameters_create_secure_tcp(
		(void *)configure_tls, _nw_parameters_configure_protocol_default_configuration);
	if (params == NULL) {
		os_release(endpoint);
		return HTTPS_NW_ERR_SETUP;
	}

	https_nw_conn *c = (https_nw_conn *)calloc(1, sizeof(https_nw_conn));
	if (c == NULL) {
		os_release(params);
		os_release(endpoint);
		return HTTPS_NW_ERR_ALLOC;
	}

	c->conn = nw_connection_create(endpoint, params);
	os_release(params);
	os_release(endpoint);
	if (c->conn == NULL) {
		free(c);
		return HTTPS_NW_ERR_SETUP;
	}
	c->anchors = anchors;
	c->queue = dispatch_queue_create("tinygodriver.https", NULL);
	nw_connection_set_queue(c->conn, c->queue);

	dispatch_semaphore_t ready = dispatch_semaphore_create(0);
	__block int state_status = 0;
	__block int state_rc = HTTPS_NW_OK;

	nw_connection_set_state_changed_handler(
		c->conn, (void *)^(int state, nw_error_t error) {
			switch (state) {
			case nw_connection_state_ready:
				state_rc = HTTPS_NW_OK;
				dispatch_semaphore_signal(ready);
				break;
			case nw_connection_state_waiting:
				// nw_connection parks recoverable errors here and retries
				// indefinitely. A rejected certificate lands here, so a client
				// dial must treat waiting as terminal or it hangs until the
				// caller's timeout.
				state_status = error ? nw_error_get_error_code(error) : 0;
				state_rc = HTTPS_NW_ERR_HANDSHAKE;
				dispatch_semaphore_signal(ready);
				break;
			case nw_connection_state_failed:
				state_status = error ? nw_error_get_error_code(error) : 0;
				state_rc = HTTPS_NW_ERR_HANDSHAKE;
				dispatch_semaphore_signal(ready);
				break;
			case nw_connection_state_cancelled:
				state_rc = HTTPS_NW_ERR_CLOSED;
				dispatch_semaphore_signal(ready);
				break;
			default:
				break;
			}
		});

	nw_connection_start(c->conn);

	int rc = HTTPS_NW_OK;
	if (dispatch_semaphore_wait(ready, dispatch_time(DISPATCH_TIME_NOW, timeout_ns)) != 0) {
		rc = HTTPS_NW_ERR_TIMEOUT;
	} else if (state_rc != HTTPS_NW_OK) {
		rc = state_rc;
		*status = state_status;
	}
	if (rc != HTTPS_NW_OK) {
		nw_connection_cancel(c->conn);
		os_release(c->conn);
		dispatch_release(c->queue);
		free(c);
		return rc;
	}

	*out = (uintptr_t)c;
	return HTTPS_NW_OK;
}

int https_nw_send(uintptr_t handle, const void *buf, int len,
                  int64_t timeout_ns, int *status) {
	https_nw_conn *c = (https_nw_conn *)handle;
	*status = 0;
	if (c == NULL || c->conn == NULL) return HTTPS_NW_ERR_CLOSED;
	if (len <= 0) return HTTPS_NW_OK;

	dispatch_data_t data = dispatch_data_create(buf, (size_t)len, c->queue,
	                                            DISPATCH_DATA_DESTRUCTOR_DEFAULT);
	if (data == NULL) return HTTPS_NW_ERR_ALLOC;

	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	__block int send_status = 0;
	nw_connection_send(c->conn, data, _nw_content_context_default_message, false,
	                   (void *)^(nw_error_t error) {
		                   if (error) send_status = nw_error_get_error_code(error);
		                   dispatch_semaphore_signal(done);
	                   });
	dispatch_release(data);

	if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, timeout_ns)) != 0) {
		return HTTPS_NW_ERR_TIMEOUT;
	}
	if (send_status != 0) {
		*status = send_status;
		return HTTPS_NW_ERR_IO;
	}
	return HTTPS_NW_OK;
}

int https_nw_recv(uintptr_t handle, void *buf, int len,
                  int64_t timeout_ns, int *n, int *status) {
	https_nw_conn *c = (https_nw_conn *)handle;
	*n = 0;
	*status = 0;
	if (c == NULL || c->conn == NULL) return HTTPS_NW_ERR_CLOSED;
	if (len <= 0) return HTTPS_NW_OK;

	// Drain leftovers from a previous oversized receive first.
	if (c->pending != NULL && c->pending_off < c->pending_len) {
		size_t avail = c->pending_len - c->pending_off;
		size_t take = avail < (size_t)len ? avail : (size_t)len;
		memcpy(buf, c->pending + c->pending_off, take);
		c->pending_off += take;
		if (c->pending_off >= c->pending_len) {
			free(c->pending);
			c->pending = NULL;
			c->pending_len = 0;
			c->pending_off = 0;
		}
		*n = (int)take;
		return HTTPS_NW_OK;
	}
	if (c->eof) return HTTPS_NW_OK;

	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	__block int recv_status = 0;
	__block uint8_t *got = NULL;
	__block size_t got_len = 0;
	__block int is_eof = 0;

	nw_connection_receive(c->conn, 1, (uint32_t)len,
	                      (void *)^(dispatch_data_t content, nw_content_context_t context,
	                                bool complete, nw_error_t error) {
		                      (void)context;
		                      if (content != NULL) {
			                      const void *mapped = NULL;
			                      size_t mapped_len = 0;
			                      dispatch_data_t flat =
				                      dispatch_data_create_map(content, &mapped, &mapped_len);
			                      if (mapped_len > 0) {
				                      got = (uint8_t *)malloc(mapped_len);
				                      if (got != NULL) {
					                      memcpy(got, mapped, mapped_len);
					                      got_len = mapped_len;
				                      }
			                      }
			                      if (flat != NULL) dispatch_release(flat);
		                      }
		                      if (error) recv_status = nw_error_get_error_code(error);
		                      if (complete) is_eof = 1;
		                      dispatch_semaphore_signal(done);
	                      });

	if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, timeout_ns)) != 0) {
		return HTTPS_NW_ERR_TIMEOUT;
	}
	if (recv_status != 0) {
		free(got);
		*status = recv_status;
		return HTTPS_NW_ERR_IO;
	}
	if (is_eof) c->eof = 1;

	if (got_len > 0) {
		size_t take = got_len < (size_t)len ? got_len : (size_t)len;
		memcpy(buf, got, take);
		*n = (int)take;
		if (take < got_len) {
			// Keep the remainder for the next call.
			c->pending = got;
			c->pending_len = got_len;
			c->pending_off = take;
			return HTTPS_NW_OK;
		}
	}
	free(got);
	return HTTPS_NW_OK;
}

void https_nw_close(uintptr_t handle) {
	https_nw_conn *c = (https_nw_conn *)handle;
	if (c == NULL) return;
	if (c->conn != NULL) {
		nw_connection_cancel(c->conn);
		os_release(c->conn);
		c->conn = NULL;
	}
	if (c->queue != NULL) {
		dispatch_release(c->queue);
		c->queue = NULL;
	}
	if (c->anchors != NULL) {
		CFRelease(c->anchors);
		c->anchors = NULL;
	}
	free(c->pending);
	free(c);
}
