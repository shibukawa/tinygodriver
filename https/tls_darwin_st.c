//go:build (tinygo || force_tinygo_logic) && darwin && !darwintls13

#include "tls_darwin_st.h"

// Secure Transport, CoreFoundation and the few libc calls used here are all
// declared by hand. TinyGo compiles cgo C with -nostdlibinc against a minimal
// macOS SDK and rejects -F/-iframework in CFLAGS, so framework headers are
// unreachable. This also decouples the build from the installed SDK version.
//
// Unlike the Network.framework backend this uses no Clang blocks and no
// libdispatch: SSLSetIOFuncs takes plain function pointers.

#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

// ------------------------------------------------------------------- libc

extern long read(int fd, void *buf, unsigned long count);
extern long write(int fd, const void *buf, unsigned long count);

// darwin's fd_set is 1024 bits; select is present in TinyGo's libSystem stub
// while poll is not.
struct fdset_min {
	unsigned int bits[32];
};

#define FDSET_ZERO(s) memset((s), 0, sizeof(*(s)))
#define FDSET_SET(fd, s) ((s)->bits[(fd) / 32] |= (1u << ((fd) % 32)))

// darwin: tv_sec is long, tv_usec is int32, so the struct is padded to 16.
struct timeval_min {
	long tv_sec;
	int tv_usec;
	int _pad;
};

struct timespec_min {
	long tv_sec;
	long tv_nsec;
};

extern int select(int nfds, struct fdset_min *readfds, struct fdset_min *writefds,
                  struct fdset_min *errorfds, struct timeval_min *timeout);
extern int clock_gettime(int clk, struct timespec_min *ts);

#define CLOCK_MONOTONIC_DARWIN 6

static int64_t now_ns(void) {
	struct timespec_min ts = {0, 0};
	clock_gettime(CLOCK_MONOTONIC_DARWIN, &ts);
	return (int64_t)ts.tv_sec * 1000000000LL + (int64_t)ts.tv_nsec;
}

// ------------------------------------------------------------ CoreFoundation

typedef void *CFTypeRef;
typedef void *CFAllocatorRef;
typedef void *CFDataRef;
typedef void *CFArrayRef;
typedef void *CFMutableArrayRef;
typedef void *CFErrorRef;
typedef long CFIndex;

extern const uint8_t kCFTypeArrayCallBacks[];
extern CFDataRef CFDataCreate(CFAllocatorRef allocator, const uint8_t *bytes, CFIndex length);
extern CFMutableArrayRef CFArrayCreateMutable(CFAllocatorRef allocator, CFIndex capacity,
                                              const void *callbacks);
extern void CFArrayAppendValue(CFMutableArrayRef array, const void *value);
extern CFIndex CFArrayGetCount(CFArrayRef array);
extern void CFRelease(CFTypeRef cf);

// ------------------------------------------------------ Security / SecureTransport

typedef void *SecCertificateRef;
typedef void *SecTrustRef;
typedef void *SSLContextRef;
typedef const void *SSLConnectionRef;
typedef int32_t OSStatus;

// SSLProtocolSide
#define kSSLServerSide 0
#define kSSLClientSide 1
// SSLConnectionType
#define kSSLStreamType 0
// SSLProtocol. Secure Transport stops at TLS 1.2; TLS 1.3 exists only in
// Network.framework, which is what the darwintls13 build tag selects.
#define kSSLProtocolUnknown 0
#define kTLSProtocol1 4
#define kTLSProtocol11 7
#define kTLSProtocol12 8
// SSLSessionOption
#define kSSLSessionOptionBreakOnServerAuth 0

#define errSecSuccess 0
#define errSSLWouldBlock -9803
#define errSSLClosedGraceful -9805
#define errSSLClosedAbort -9806
#define errSSLServerAuthCompleted -9841
#define errSSLPeerAuthCompleted -9841
#define errSSLXCertChainInvalid -9807
#define errSSLClosedNoNotify -9816

typedef OSStatus (*SSLReadFunc)(SSLConnectionRef connection, void *data, unsigned long *dataLength);
typedef OSStatus (*SSLWriteFunc)(SSLConnectionRef connection, const void *data, unsigned long *dataLength);

extern SSLContextRef SSLCreateContext(CFAllocatorRef alloc, int protocolSide, int connectionType);
extern OSStatus SSLSetIOFuncs(SSLContextRef context, SSLReadFunc readFunc, SSLWriteFunc writeFunc);
extern OSStatus SSLSetConnection(SSLContextRef context, SSLConnectionRef connection);
extern OSStatus SSLSetPeerDomainName(SSLContextRef context, const char *peerName, unsigned long peerNameLen);
extern OSStatus SSLSetProtocolVersionMin(SSLContextRef context, int minVersion);
extern OSStatus SSLSetSessionOption(SSLContextRef context, int option, unsigned char value);
extern OSStatus SSLCopyPeerTrust(SSLContextRef context, SecTrustRef *trust);
extern OSStatus SSLHandshake(SSLContextRef context);
extern OSStatus SSLRead(SSLContextRef context, void *data, unsigned long dataLength, unsigned long *processed);
extern OSStatus SSLWrite(SSLContextRef context, const void *data, unsigned long dataLength, unsigned long *processed);
extern OSStatus SSLClose(SSLContextRef context);

extern SecCertificateRef SecCertificateCreateWithData(CFAllocatorRef allocator, CFDataRef data);
extern OSStatus SecTrustSetAnchorCertificates(SecTrustRef trust, CFArrayRef anchors);
extern OSStatus SecTrustSetAnchorCertificatesOnly(SecTrustRef trust, unsigned char only);
extern bool SecTrustEvaluateWithError(SecTrustRef trust, CFErrorRef *error);

// ------------------------------------------------------------------- state

typedef struct {
	SSLContextRef ctx;
	int fd;
	CFMutableArrayRef anchors;
	int ca_only;
	int skip_verify;
	// Absolute CLOCK_MONOTONIC deadline for the operation in flight.
	// Zero means no deadline.
	int64_t deadline_ns;
	int timed_out;
	int eof;
} https_st;

// Waits until the descriptor is ready or the deadline passes.
// Returns 0 on ready, -1 on timeout.
static int wait_ready(https_st *s, int for_write) {
	if (s->fd < 0 || s->fd >= 1024) return -1;
	for (;;) {
		struct fdset_min set;
		FDSET_ZERO(&set);
		FDSET_SET(s->fd, &set);

		struct timeval_min tv;
		struct timeval_min *tvp = NULL;
		if (s->deadline_ns != 0) {
			int64_t remaining = s->deadline_ns - now_ns();
			if (remaining <= 0) return -1;
			tv.tv_sec = (long)(remaining / 1000000000LL);
			tv.tv_usec = (int)((remaining % 1000000000LL) / 1000);
			tv._pad = 0;
			tvp = &tv;
		}

		int rc = for_write ? select(s->fd + 1, NULL, &set, NULL, tvp)
		                   : select(s->fd + 1, &set, NULL, NULL, tvp);
		if (rc > 0) return 0;
		if (rc == 0) return -1; // timed out
		// EINTR and friends: retry, the deadline check above bounds the loop.
		continue;
	}
}

// Secure Transport asks for an exact byte count and expects errSSLWouldBlock
// with a partial count when it cannot be met.
static OSStatus st_read(SSLConnectionRef connection, void *data, unsigned long *dataLength) {
	https_st *s = (https_st *)connection;
	unsigned long want = *dataLength, got = 0;

	while (got < want) {
		if (wait_ready(s, 0) != 0) {
			s->timed_out = 1;
			*dataLength = got;
			return errSSLWouldBlock;
		}
		long n = read(s->fd, (char *)data + got, want - got);
		if (n > 0) {
			got += (unsigned long)n;
			continue;
		}
		*dataLength = got;
		if (n == 0) {
			s->eof = 1;
			return errSSLClosedGraceful;
		}
		return errSSLClosedAbort;
	}
	*dataLength = got;
	return errSecSuccess;
}

static OSStatus st_write(SSLConnectionRef connection, const void *data, unsigned long *dataLength) {
	https_st *s = (https_st *)connection;
	unsigned long want = *dataLength, sent = 0;

	while (sent < want) {
		if (wait_ready(s, 1) != 0) {
			s->timed_out = 1;
			*dataLength = sent;
			return errSSLWouldBlock;
		}
		long n = write(s->fd, (const char *)data + sent, want - sent);
		if (n > 0) {
			sent += (unsigned long)n;
			continue;
		}
		*dataLength = sent;
		return errSSLClosedAbort;
	}
	*dataLength = sent;
	return errSecSuccess;
}

// ---------------------------------------------------------------- anchors

uintptr_t https_st_calist_new(void) {
	return (uintptr_t)CFArrayCreateMutable(NULL, 0, kCFTypeArrayCallBacks);
}

int https_st_calist_add(uintptr_t list, const void *der, int len) {
	CFMutableArrayRef array = (CFMutableArrayRef)list;
	if (array == NULL || der == NULL || len <= 0) return HTTPS_ST_ERR_SETUP;

	CFDataRef data = CFDataCreate(NULL, (const uint8_t *)der, (CFIndex)len);
	if (data == NULL) return HTTPS_ST_ERR_ALLOC;

	SecCertificateRef cert = SecCertificateCreateWithData(NULL, data);
	CFRelease(data);
	if (cert == NULL) return HTTPS_ST_ERR_SETUP; // not a valid DER certificate

	CFArrayAppendValue(array, cert);
	CFRelease(cert); // the array retains it
	return HTTPS_ST_OK;
}

void https_st_calist_free(uintptr_t list) {
	if (list != 0) CFRelease((CFTypeRef)list);
}

// ------------------------------------------------------------------ trust

// Runs when SSLHandshake stops at errSSLServerAuthCompleted, which is what
// kSSLSessionOptionBreakOnServerAuth arranges. Returning non-zero aborts.
static int evaluate_trust(https_st *s) {
	if (s->skip_verify) return 0;

	SecTrustRef trust = NULL;
	if (SSLCopyPeerTrust(s->ctx, &trust) != errSecSuccess || trust == NULL) {
		return -1;
	}

	if (s->anchors != NULL) {
		// An empty array with anchorsOnly must trust nothing. Passing NULL
		// would restore the system defaults instead, which is the opposite.
		SecTrustSetAnchorCertificates(trust, s->anchors);
		SecTrustSetAnchorCertificatesOnly(trust, s->ca_only ? 1 : 0);
	}

	CFErrorRef err = NULL;
	bool ok = SecTrustEvaluateWithError(trust, &err);
	if (err != NULL) CFRelease(err);
	CFRelease(trust);
	return ok ? 0 : -1;
}

// Does not release s->anchors: ownership only transfers to the connection on
// success, so a failed handshake leaves the caller to free the list. Releasing
// here as well would double free it.
static void free_state(https_st *s) {
	if (s == NULL) return;
	if (s->ctx != NULL) CFRelease(s->ctx);
	free(s);
}

int https_st_handshake(int fd, const char *host, int skip_verify,
                       uintptr_t calist, int ca_only, int min_version,
                       int64_t timeout_ns, uintptr_t *out, int *status) {
	*out = 0;
	*status = 0;

	https_st *s = (https_st *)calloc(1, sizeof(https_st));
	if (s == NULL) return HTTPS_ST_ERR_ALLOC;
	s->fd = fd;
	s->anchors = (CFMutableArrayRef)calist;
	s->ca_only = ca_only;
	s->skip_verify = skip_verify;
	s->deadline_ns = timeout_ns > 0 ? now_ns() + timeout_ns : 0;

	s->ctx = SSLCreateContext(NULL, kSSLClientSide, kSSLStreamType);
	if (s->ctx == NULL) {
		free_state(s);
		return HTTPS_ST_ERR_SETUP;
	}
	if (SSLSetIOFuncs(s->ctx, st_read, st_write) != errSecSuccess ||
	    SSLSetConnection(s->ctx, s) != errSecSuccess) {
		free_state(s);
		return HTTPS_ST_ERR_SETUP;
	}
	if (host != NULL && host[0] != '\0') {
		// Sets SNI and the name Secure Transport checks against the certificate.
		if (SSLSetPeerDomainName(s->ctx, host, strlen(host)) != errSecSuccess) {
			free_state(s);
			return HTTPS_ST_ERR_SETUP;
		}
	}
	if (min_version != 0) {
		// Secure Transport tops out at TLS 1.2. Anything higher is clamped by
		// the Go side before it gets here.
		SSLSetProtocolVersionMin(s->ctx, min_version);
	}
	// Take over verification so custom anchors and skip-verify behave the same
	// way they do on the other backends.
	SSLSetSessionOption(s->ctx, kSSLSessionOptionBreakOnServerAuth, 1);

	for (;;) {
		OSStatus rc = SSLHandshake(s->ctx);
		if (rc == errSecSuccess) break;

		if (rc == errSSLServerAuthCompleted) {
			if (evaluate_trust(s) != 0) {
				*status = errSSLXCertChainInvalid;
				free_state(s);
				return HTTPS_ST_ERR_HANDSHAKE;
			}
			continue; // resume the handshake
		}
		if (rc == errSSLWouldBlock) {
			if (s->timed_out || (s->deadline_ns != 0 && now_ns() >= s->deadline_ns)) {
				free_state(s);
				return HTTPS_ST_ERR_TIMEOUT;
			}
			continue;
		}
		*status = (int)rc;
		int timed_out = s->timed_out;
		free_state(s);
		return timed_out ? HTTPS_ST_ERR_TIMEOUT : HTTPS_ST_ERR_HANDSHAKE;
	}

	s->deadline_ns = 0;
	*out = (uintptr_t)s;
	return HTTPS_ST_OK;
}

int https_st_read(uintptr_t handle, void *buf, int len,
                  int64_t timeout_ns, int *n, int *status) {
	https_st *s = (https_st *)handle;
	*n = 0;
	*status = 0;
	if (s == NULL || s->ctx == NULL) return HTTPS_ST_ERR_CLOSED;
	if (len <= 0) return HTTPS_ST_OK;

	s->deadline_ns = timeout_ns > 0 ? now_ns() + timeout_ns : 0;
	s->timed_out = 0;

	unsigned long processed = 0;
	OSStatus rc = SSLRead(s->ctx, buf, (unsigned long)len, &processed);
	if (processed > 0) {
		*n = (int)processed;
		return HTTPS_ST_OK;
	}
	switch (rc) {
	case errSecSuccess:
		return HTTPS_ST_OK;
	case errSSLClosedGraceful:
	case errSSLClosedNoNotify:
		return HTTPS_ST_OK; // clean EOF, *n stays 0
	case errSSLWouldBlock:
		return HTTPS_ST_ERR_TIMEOUT;
	default:
		*status = (int)rc;
		return s->timed_out ? HTTPS_ST_ERR_TIMEOUT : HTTPS_ST_ERR_IO;
	}
}

int https_st_write(uintptr_t handle, const void *buf, int len,
                   int64_t timeout_ns, int *n, int *status) {
	https_st *s = (https_st *)handle;
	*n = 0;
	*status = 0;
	if (s == NULL || s->ctx == NULL) return HTTPS_ST_ERR_CLOSED;
	if (len <= 0) return HTTPS_ST_OK;

	s->deadline_ns = timeout_ns > 0 ? now_ns() + timeout_ns : 0;
	s->timed_out = 0;

	unsigned long processed = 0;
	OSStatus rc = SSLWrite(s->ctx, buf, (unsigned long)len, &processed);
	*n = (int)processed;
	if (rc == errSecSuccess) return HTTPS_ST_OK;
	if (rc == errSSLWouldBlock) return HTTPS_ST_ERR_TIMEOUT;
	*status = (int)rc;
	return s->timed_out ? HTTPS_ST_ERR_TIMEOUT : HTTPS_ST_ERR_IO;
}

void https_st_close(uintptr_t handle) {
	https_st *s = (https_st *)handle;
	if (s == NULL) return;
	if (s->ctx != NULL) {
		// Best effort: a peer that has already gone away must not block Close.
		s->deadline_ns = now_ns() + 100000000LL; // 100ms
		SSLClose(s->ctx);
	}
	// The connection owns the anchors once the handshake succeeded.
	if (s->anchors != NULL) {
		CFRelease(s->anchors);
		s->anchors = NULL;
	}
	free_state(s);
}
