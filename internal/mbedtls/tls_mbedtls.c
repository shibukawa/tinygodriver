//go:build (tinygo || force_tinygo_logic) && (linux || (darwin && darwinstarttlswith13))

#include "tls_mbedtls.h"

#include <stdlib.h>
#include <string.h>

#include "mbedtls/aes.h"
#include "mbedtls/ctr_drbg.h"
#include "mbedtls/entropy.h"
#include "mbedtls/gcm.h"
#include "mbedtls/pk.h"
#include "mbedtls/sha256.h"
#include "mbedtls/sha512.h"
#include "mbedtls/ssl.h"
#include "mbedtls/x509_crt.h"

#if defined(MBEDTLS_AESNI_C) && defined(__x86_64__)
#include "aesni.h"
#endif

/*
 * Platform layer.
 *
 * Linux: TinyGo's libc omits the BSD socket API and poll, which is also why
 * netdev/sys_linux.go issues raw syscalls, so I/O and entropy go through the
 * syscall ABI directly.
 *
 * darwin: TinyGo's libSystem stub does provide read, write and select, so
 * those are called normally. It has no getrandom; arc4random_buf is the
 * platform equivalent and cannot fail.
 *
 * Neither platform exports inet_pton, which x509_crt.c needs for iPAddress
 * SANs; the shim below covers it.
 */
#if defined(__linux__)

#if defined(__aarch64__)
static long rsys(long n, long a, long b, long c, long d) {
	register long x8 __asm__("x8") = n;
	register long x0 __asm__("x0") = a;
	register long x1 __asm__("x1") = b;
	register long x2 __asm__("x2") = c;
	register long x3 __asm__("x3") = d;
	__asm__ volatile("svc 0"
	                 : "+r"(x0)
	                 : "r"(x8), "r"(x1), "r"(x2), "r"(x3)
	                 : "memory", "cc");
	return x0;
}
enum { SYS_read_n = 63, SYS_write_n = 64, SYS_ppoll_n = 73, SYS_getrandom_n = 278 };
#elif defined(__x86_64__)
static long rsys(long n, long a, long b, long c, long d) {
	register long r10 __asm__("r10") = d;
	long r;
	__asm__ volatile("syscall"
	                 : "=a"(r)
	                 : "a"(n), "D"(a), "S"(b), "d"(c), "r"(r10)
	                 : "rcx", "r11", "memory", "cc");
	return r;
}
enum { SYS_read_n = 0, SYS_write_n = 1, SYS_ppoll_n = 271, SYS_getrandom_n = 318 };
#else
#error "unsupported architecture for the mbedTLS backend"
#endif

struct pollfd_min {
	int fd;
	short events;
	short revents;
};

struct timespec_min {
	long tv_sec;
	long tv_nsec;
};

#define POLL_IN 0x001
#define POLL_OUT 0x004

static long plat_read(int fd, void *buf, unsigned long n) {
	return rsys(SYS_read_n, fd, (long)buf, (long)n, 0);
}

static long plat_write(int fd, const void *buf, unsigned long n) {
	return rsys(SYS_write_n, fd, (long)buf, (long)n, 0);
}

/* Returns 0 when ready, -1 on timeout. deadline_ns of 0 waits indefinitely. */
static int plat_wait(int fd, int for_write, int64_t deadline_ns, int64_t now) {
	for (;;) {
		struct pollfd_min p;
		p.fd = fd;
		p.events = for_write ? POLL_OUT : POLL_IN;
		p.revents = 0;

		struct timespec_min ts;
		struct timespec_min *tsp = NULL;
		if (deadline_ns != 0) {
			int64_t remaining = deadline_ns - now;
			if (remaining <= 0) return -1;
			ts.tv_sec = (long)(remaining / 1000000000LL);
			ts.tv_nsec = (long)(remaining % 1000000000LL);
			tsp = &ts;
		}

		long rc = rsys(SYS_ppoll_n, (long)&p, 1, (long)tsp, 0);
		if (rc > 0) return 0;
		if (rc == 0) return -1;
		if (rc == -4 /* -EINTR */) continue;
		return -1;
	}
}

static int plat_random(unsigned char *out, size_t len) {
	size_t got = 0;
	while (got < len) {
		long n = rsys(SYS_getrandom_n, (long)(out + got), (long)(len - got), 0, 0);
		if (n <= 0) {
			if (n == -4 /* -EINTR */) continue;
			return -1;
		}
		got += (size_t)n;
	}
	return 0;
}

#elif defined(__APPLE__)

extern long read(int fd, void *buf, unsigned long n);
extern long write(int fd, const void *buf, unsigned long n);
extern void arc4random_buf(void *buf, unsigned long n);

/* darwin's fd_set is 1024 bits. select is in TinyGo's libSystem stub; poll is
 * not, which is the same constraint the Secure Transport backend works under. */
struct fdset_min {
	unsigned int bits[32];
};

/* tv_sec is long, tv_usec is int32, so the struct is padded to 16. */
struct timeval_min {
	long tv_sec;
	int tv_usec;
	int _pad;
};

extern int select(int nfds, struct fdset_min *r, struct fdset_min *w,
                  struct fdset_min *e, struct timeval_min *timeout);

static long plat_read(int fd, void *buf, unsigned long n) { return read(fd, buf, n); }
static long plat_write(int fd, const void *buf, unsigned long n) { return write(fd, buf, n); }

static int plat_wait(int fd, int for_write, int64_t deadline_ns, int64_t now) {
	if (fd < 0 || fd >= 1024) return -1;
	for (;;) {
		struct fdset_min set;
		memset(&set, 0, sizeof(set));
		set.bits[fd / 32] |= (1u << (fd % 32));

		struct timeval_min tv;
		struct timeval_min *tvp = NULL;
		if (deadline_ns != 0) {
			int64_t remaining = deadline_ns - now;
			if (remaining <= 0) return -1;
			tv.tv_sec = (long)(remaining / 1000000000LL);
			tv.tv_usec = (int)((remaining % 1000000000LL) / 1000);
			tv._pad = 0;
			tvp = &tv;
		}

		int rc = for_write ? select(fd + 1, NULL, &set, NULL, tvp)
		                   : select(fd + 1, &set, NULL, NULL, tvp);
		if (rc > 0) return 0;
		if (rc == 0) return -1;
		continue; /* EINTR; the deadline check above bounds the loop */
	}
}

/* arc4random_buf cannot fail, which is why there is no error path here. */
static int plat_random(unsigned char *out, size_t len) {
	arc4random_buf(out, len);
	return 0;
}

#else
#error "unsupported platform for the mbedTLS backend"
#endif

/* Defined by mbedtls/net_sockets.h, which is not included here because
 * MBEDTLS_NET_C is off and net_sockets.c is not vendored. mbedTLS treats any
 * negative BIO return as fatal; these are the canonical values for the two
 * cases, kept so the reported status stays meaningful. */
#define NET_ERR_RECV_FAILED -0x004C
#define NET_ERR_SEND_FAILED -0x004E

#include <time.h>

static int64_t now_ns(void) {
	struct timespec ts = {0, 0};
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return (int64_t)ts.tv_sec * 1000000000LL + (int64_t)ts.tv_nsec;
}

typedef struct {
	mbedtls_ssl_context ssl;
	mbedtls_ssl_config conf;
	mbedtls_x509_crt cacert;
	mbedtls_x509_crt clicert;
	mbedtls_pk_context pkey;
	mbedtls_entropy_context entropy;
	mbedtls_ctr_drbg_context drbg;
	int fd;
	int has_clicert;
	/* Absolute CLOCK_MONOTONIC deadline for the operation in flight.
	 * Zero means no deadline. */
	int64_t deadline_ns;
	int timed_out;
} https_mbed;

/* Waits until the fd is ready or the deadline passes. Returns 0 on ready,
 * -1 on timeout. A deadline of zero waits indefinitely. */
static int wait_ready(https_mbed *s, int for_write) {
	return plat_wait(s->fd, for_write, s->deadline_ns, now_ns());
}

static int bio_send(void *ctx, const unsigned char *buf, size_t len) {
	https_mbed *s = (https_mbed *)ctx;
	/* With no deadline the fd is blocking and the write itself waits, so the
	 * readiness poll would be a wasted syscall per record; netdev's waitFD
	 * makes the same skip. */
	if (s->deadline_ns != 0 && wait_ready(s, 1) != 0) {
		s->timed_out = 1;
		return MBEDTLS_ERR_SSL_TIMEOUT;
	}
	long n = plat_write(s->fd, buf, len);
	if (n < 0) return NET_ERR_SEND_FAILED;
	return (int)n;
}

static int bio_recv(void *ctx, unsigned char *buf, size_t len) {
	https_mbed *s = (https_mbed *)ctx;
	if (s->deadline_ns != 0 && wait_ready(s, 0) != 0) {
		s->timed_out = 1;
		return MBEDTLS_ERR_SSL_TIMEOUT;
	}
	long n = plat_read(s->fd, buf, len);
	if (n < 0) return NET_ERR_RECV_FAILED;
	return (int)n;
}

/* Entropy from the platform CSPRNG, avoiding mbedTLS platform entropy, which
 * wants /dev/urandom through stdio while MBEDTLS_FS_IO is off. */
static int entropy_platform(void *data, unsigned char *output, size_t len,
                             size_t *olen) {
	(void)data;
	if (plat_random(output, len) != 0) {
		return MBEDTLS_ERR_ENTROPY_SOURCE_FAILED;
	}
	*olen = len;
	return 0;
}

static void free_state(https_mbed *s) {
	if (s == NULL) return;
	mbedtls_ssl_free(&s->ssl);
	mbedtls_ssl_config_free(&s->conf);
	mbedtls_x509_crt_free(&s->cacert);
	if (s->has_clicert) {
		mbedtls_x509_crt_free(&s->clicert);
		mbedtls_pk_free(&s->pkey);
	}
	mbedtls_ctr_drbg_free(&s->drbg);
	mbedtls_entropy_free(&s->entropy);
	free(s);
}

int https_mbed_handshake(int fd, const char *host, int skip_verify,
                         int min_version,
                         const unsigned char *ca_pem, int ca_len,
                         const unsigned char *cert_pem, int cert_len,
                         const unsigned char *key_pem, int key_len,
                         int64_t timeout_ns,
                         uintptr_t *out, int *tls_err,
                         unsigned int *verify_flags) {
	*out = 0;
	*tls_err = 0;
	*verify_flags = 0;

	https_mbed *s = (https_mbed *)calloc(1, sizeof(https_mbed));
	if (s == NULL) return HTTPS_MBED_ERR_ALLOC;
	s->fd = fd;
	s->deadline_ns = timeout_ns > 0 ? now_ns() + timeout_ns : 0;

	mbedtls_ssl_init(&s->ssl);
	mbedtls_ssl_config_init(&s->conf);
	mbedtls_x509_crt_init(&s->cacert);
	mbedtls_entropy_init(&s->entropy);
	mbedtls_ctr_drbg_init(&s->drbg);

	mbedtls_entropy_add_source(&s->entropy, entropy_platform, NULL, 32,
	                           MBEDTLS_ENTROPY_SOURCE_STRONG);
	int rc = mbedtls_ctr_drbg_seed(&s->drbg, mbedtls_entropy_func, &s->entropy,
	                               (const unsigned char *)"tinygodriver/https", 18);
	if (rc != 0) {
		*tls_err = rc;
		free_state(s);
		return HTTPS_MBED_ERR_SETUP;
	}

	if (!skip_verify && ca_pem != NULL && ca_len > 0) {
		rc = mbedtls_x509_crt_parse(&s->cacert, ca_pem, (size_t)ca_len);
		/* A positive return counts certificates that failed to parse while
		 * others succeeded. Tolerate that: system bundles routinely carry a
		 * few entries this build cannot represent. */
		if (rc < 0) {
			*tls_err = rc;
			free_state(s);
			return HTTPS_MBED_ERR_CA;
		}
	}

	rc = mbedtls_ssl_config_defaults(&s->conf, MBEDTLS_SSL_IS_CLIENT,
	                                 MBEDTLS_SSL_TRANSPORT_STREAM,
	                                 MBEDTLS_SSL_PRESET_DEFAULT);
	if (rc != 0) {
		*tls_err = rc;
		free_state(s);
		return HTTPS_MBED_ERR_SETUP;
	}

	mbedtls_ssl_conf_authmode(&s->conf, skip_verify ? MBEDTLS_SSL_VERIFY_NONE
	                                                : MBEDTLS_SSL_VERIFY_REQUIRED);
	if (!skip_verify) mbedtls_ssl_conf_ca_chain(&s->conf, &s->cacert, NULL);
	mbedtls_ssl_conf_rng(&s->conf, mbedtls_ctr_drbg_random, &s->drbg);
	if (min_version != 0) {
		mbedtls_ssl_conf_min_tls_version(&s->conf, min_version);
	}

	if (cert_pem != NULL && cert_len > 0 && key_pem != NULL && key_len > 0) {
		mbedtls_x509_crt_init(&s->clicert);
		mbedtls_pk_init(&s->pkey);
		s->has_clicert = 1;
		rc = mbedtls_x509_crt_parse(&s->clicert, cert_pem, (size_t)cert_len);
		if (rc != 0) {
			*tls_err = rc;
			free_state(s);
			return HTTPS_MBED_ERR_CLIENT_CERT;
		}
		rc = mbedtls_pk_parse_key(&s->pkey, key_pem, (size_t)key_len, NULL, 0,
		                          mbedtls_ctr_drbg_random, &s->drbg);
		if (rc != 0) {
			*tls_err = rc;
			free_state(s);
			return HTTPS_MBED_ERR_CLIENT_CERT;
		}
		rc = mbedtls_ssl_conf_own_cert(&s->conf, &s->clicert, &s->pkey);
		if (rc != 0) {
			*tls_err = rc;
			free_state(s);
			return HTTPS_MBED_ERR_CLIENT_CERT;
		}
	}

	rc = mbedtls_ssl_setup(&s->ssl, &s->conf);
	if (rc != 0) {
		*tls_err = rc;
		free_state(s);
		return HTTPS_MBED_ERR_SETUP;
	}
	/* Sets SNI and the name checked against the certificate together. */
	rc = mbedtls_ssl_set_hostname(&s->ssl, host);
	if (rc != 0) {
		*tls_err = rc;
		free_state(s);
		return HTTPS_MBED_ERR_SETUP;
	}

	mbedtls_ssl_set_bio(&s->ssl, s, bio_send, bio_recv, NULL);

	for (;;) {
		rc = mbedtls_ssl_handshake(&s->ssl);
		if (rc == 0) break;
		if (rc == MBEDTLS_ERR_SSL_WANT_READ || rc == MBEDTLS_ERR_SSL_WANT_WRITE) {
			/* wait_ready already enforces the deadline, but a peer that keeps
			 * the socket readable while sending nothing useful would spin
			 * here, so check it directly too. */
			if (s->deadline_ns != 0 && now_ns() >= s->deadline_ns) {
				free_state(s);
				return HTTPS_MBED_ERR_TIMEOUT;
			}
			continue;
		}
		*tls_err = rc;
		*verify_flags = mbedtls_ssl_get_verify_result(&s->ssl);
		int timed_out = s->timed_out;
		free_state(s);
		return timed_out ? HTTPS_MBED_ERR_TIMEOUT : HTTPS_MBED_ERR_HANDSHAKE;
	}

	s->deadline_ns = 0;
	*out = (uintptr_t)s;
	return HTTPS_MBED_OK;
}

int https_mbed_read(uintptr_t handle, void *buf, int len,
                    int64_t timeout_ns, int *n, int *tls_err) {
	https_mbed *s = (https_mbed *)handle;
	*n = 0;
	*tls_err = 0;
	if (s == NULL) return HTTPS_MBED_ERR_CLOSED;
	if (len <= 0) return HTTPS_MBED_OK;

	s->deadline_ns = timeout_ns > 0 ? now_ns() + timeout_ns : 0;
	s->timed_out = 0;

	for (;;) {
		int rc = mbedtls_ssl_read(&s->ssl, (unsigned char *)buf, (size_t)len);
		if (rc > 0) {
			*n = rc;
			return HTTPS_MBED_OK;
		}
		if (rc == MBEDTLS_ERR_SSL_PEER_CLOSE_NOTIFY || rc == 0) {
			return HTTPS_MBED_OK; /* clean EOF, *n stays 0 */
		}
		if (rc == MBEDTLS_ERR_SSL_WANT_READ || rc == MBEDTLS_ERR_SSL_WANT_WRITE) {
			if (s->deadline_ns != 0 && now_ns() >= s->deadline_ns) {
				return HTTPS_MBED_ERR_TIMEOUT;
			}
			continue;
		}
		*tls_err = rc;
		return s->timed_out ? HTTPS_MBED_ERR_TIMEOUT : HTTPS_MBED_ERR_IO;
	}
}

int https_mbed_write(uintptr_t handle, const void *buf, int len,
                     int64_t timeout_ns, int *n, int *tls_err) {
	https_mbed *s = (https_mbed *)handle;
	*n = 0;
	*tls_err = 0;
	if (s == NULL) return HTTPS_MBED_ERR_CLOSED;
	if (len <= 0) return HTTPS_MBED_OK;

	s->deadline_ns = timeout_ns > 0 ? now_ns() + timeout_ns : 0;
	s->timed_out = 0;

	int written = 0;
	while (written < len) {
		int rc = mbedtls_ssl_write(&s->ssl, (const unsigned char *)buf + written,
		                           (size_t)(len - written));
		if (rc > 0) {
			written += rc;
			continue;
		}
		if (rc == MBEDTLS_ERR_SSL_WANT_READ || rc == MBEDTLS_ERR_SSL_WANT_WRITE) {
			if (s->deadline_ns != 0 && now_ns() >= s->deadline_ns) {
				*n = written;
				return HTTPS_MBED_ERR_TIMEOUT;
			}
			continue;
		}
		*tls_err = rc;
		*n = written;
		return s->timed_out ? HTTPS_MBED_ERR_TIMEOUT : HTTPS_MBED_ERR_IO;
	}
	*n = written;
	return HTTPS_MBED_OK;
}

void https_mbed_close(uintptr_t handle) {
	https_mbed *s = (https_mbed *)handle;
	if (s == NULL) return;
	/* Best effort: a peer that has already gone away must not block Close. */
	s->deadline_ns = now_ns() + 100000000LL; /* 100ms */
	mbedtls_ssl_close_notify(&s->ssl);
	free_state(s);
}

int https_mbed_self_test(void) {
	if (mbedtls_aes_self_test(0) != 0) return -1;
	if (mbedtls_gcm_self_test(0) != 0) return -2;
	if (mbedtls_sha256_self_test(0) != 0) return -3;
	if (mbedtls_sha512_self_test(0) != 0) return -4;
	return 0;
}

#if defined(__aarch64__) && defined(__linux__)
#include <sys/auxv.h>
#ifndef HWCAP_AES
#define HWCAP_AES (1 << 3)
#endif
#ifndef HWCAP_SHA2
#define HWCAP_SHA2 (1 << 6)
#endif
#ifndef HWCAP_SHA512
#define HWCAP_SHA512 (1 << 21)
#endif
#endif

int https_mbed_hwcaps(void) {
#if defined(__aarch64__) && defined(__linux__)
	unsigned long c = getauxval(AT_HWCAP);
	int r = 0;
	if (c & HWCAP_AES) r |= 1;
	if (c & HWCAP_SHA2) r |= 2;
	if (c & HWCAP_SHA512) r |= 4;
	return r;
#elif defined(MBEDTLS_AESNI_C) && defined(__x86_64__)
	/* mbedTLS accelerates only AES on x86; SHA stays software there. */
	return mbedtls_aesni_has_support(MBEDTLS_AESNI_AES) ? 1 : 0;
#else
	return -1;
#endif
}
