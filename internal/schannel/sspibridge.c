//go:build windows && cgo

// Schannel client, reached through SSPI.
//
// Unlike the darwin backend this file does use the system headers. SSPI and
// crypt32 structures are far too intricate to redeclare by hand safely, and a
// silently wrong layout would corrupt memory on the one platform this
// repository cannot run. The winsock declarations in netdev/sys_windows.go are
// hand written because sockaddr_in is three fields; SecBufferDesc,
// CERT_CHAIN_POLICY_PARA and friends are not that.
//
// The structures that are *not* taken from the headers are the credential
// structures, which are redeclared below under private names. Those are the
// only ones whose presence varies across mingw-w64 releases, and their layouts
// are stable and documented.
//
// Schannel never touches the socket. It transforms buffers and this file moves
// the bytes, which is why the same code serves both a fresh dial and an in-band
// upgrade of a socket that has already carried plaintext.

#define WIN32_LEAN_AND_MEAN
#define SECURITY_WIN32

#include <winsock2.h>
#include <windows.h>
#include <wincrypt.h>
#include <security.h>
#include <schannel.h>
#include <ncrypt.h>

#include <stdlib.h>
#include <string.h>

#include "sspibridge.h"

// --------------------------------------------------------------- constants

// Protocol bits. Redeclared because the TLS 1.3 value postdates some mingw-w64
// releases and the rest must agree with it.
#define SC_PROT_SSL2_CLIENT 0x00000008
#define SC_PROT_SSL3_CLIENT 0x00000020
#define SC_PROT_TLS1_0_CLIENT 0x00000080
#define SC_PROT_TLS1_1_CLIENT 0x00000200
#define SC_PROT_TLS1_2_CLIENT 0x00000800
#define SC_PROT_TLS1_3_CLIENT 0x00002000

#define SC_CRED_NO_SYSTEM_MAPPER 0x00000002
#define SC_CRED_MANUAL_CRED_VALIDATION 0x00000008
#define SC_CRED_NO_DEFAULT_CREDS 0x00000010
#define SC_USE_STRONG_CRYPTO 0x00400000

#define SC_SCHANNEL_CRED_VERSION 0x00000004
#define SC_CREDENTIALS_VERSION 0x00000005

#define SC_CHAIN_EXCLUSIVE_ENABLE_CA_FLAG 0x00000001

// CERT_TRUST_* bits that must not fail a chain here. The first two are only
// meaningful when revocation checking was requested, which it never is; the
// third is a legacy flag Windows no longer sets.
#define SC_TRUST_IS_NOT_TIME_NESTED 0x00000002
#define SC_TRUST_REVOCATION_STATUS_UNKNOWN 0x00000040
#define SC_TRUST_IS_OFFLINE_REVOCATION 0x01000000

// Reported when the caller asks for a client certificate whose key this file
// cannot import. Chosen from the SEC_E space so it never collides with a real
// status, and mapped to a distinct sentinel on the Go side.
#define SC_STATUS_KEY_UNSUPPORTED ((int)0x80090302) // SEC_E_UNSUPPORTED_FUNCTION

// One TLS record cannot exceed 16 KiB of plaintext plus expansion. The input
// buffer starts above that so a whole record almost always lands in one read,
// and grows only if a peer sends a larger one.
#define SC_ENC_INITIAL 32768
#define SC_ENC_MAX (512 * 1024)

// Bounds the close_notify exchange so a peer that has already gone away cannot
// block Close.
#define SC_SHUTDOWN_NS 100000000LL // 100ms

// ----------------------------------------------- credential structures

// SCHANNEL_CRED, dwVersion 4. Deprecated by Microsoft but still the only
// structure some Schannel implementations accept, so it is the fallback.
typedef struct {
	DWORD dwVersion;
	DWORD cCreds;
	PCCERT_CONTEXT *paCred;
	HCERTSTORE hRootStore;
	DWORD cMappers;
	void *aphMappers;
	DWORD cSupportedAlgs;
	ALG_ID *palgSupportedAlgs;
	DWORD grbitEnabledProtocols;
	DWORD dwMinimumCipherStrength;
	DWORD dwMaximumCipherStrength;
	DWORD dwSessionLifespan;
	DWORD dwFlags;
	DWORD dwCredFormat;
} sc_schannel_cred;

// TLS_PARAMETERS. The two list pointers are declared void* because their
// element types drag in UNICODE_STRING, which is not reachable from the public
// headers on every toolchain. Both are always NULL here, so the layout is what
// matters and the layout is identical.
typedef struct {
	DWORD cAlpnIds;
	void *rgstrAlpnIds;
	DWORD grbitDisabledProtocols;
	DWORD cDisabledCrypto;
	void *pDisabledCrypto;
	DWORD dwFlags;
} sc_tls_parameters;

// SCH_CREDENTIALS, dwVersion 5. This is the structure that lets Schannel
// negotiate TLS 1.3; SCHANNEL_CRED tops out at 1.2.
typedef struct {
	DWORD dwVersion;
	DWORD dwCredFormat;
	DWORD cCreds;
	PCCERT_CONTEXT *paCred;
	HCERTSTORE hRootStore;
	DWORD cMappers;
	void *aphMappers;
	DWORD dwSessionLifespan;
	DWORD dwFlags;
	DWORD cTlsParameters;
	sc_tls_parameters *pTlsParameters;
} sc_credentials;

// CERT_CHAIN_ENGINE_CONFIG. Redeclared for the same reason: hExclusiveRoot and
// dwExclusiveFlags are the two fields this file depends on and they are the
// two most recently added.
typedef struct {
	DWORD cbSize;
	HCERTSTORE hRestrictedRoot;
	HCERTSTORE hRestrictedTrust;
	HCERTSTORE hRestrictedOther;
	DWORD cAdditionalStore;
	HCERTSTORE *rghAdditionalStore;
	DWORD dwFlags;
	DWORD dwUrlRetrievalTimeout;
	DWORD MaximumCachedCertificates;
	DWORD CycleDetectionModulo;
	HCERTSTORE hExclusiveRoot;
	HCERTSTORE hExclusiveTrustedPeople;
	DWORD dwExclusiveFlags;
} sc_chain_engine_config;

// ------------------------------------------------------------------- state

typedef struct {
	PCCERT_CONTEXT cert;
	NCRYPT_PROV_HANDLE prov;
} sc_clientcert;

typedef struct {
	SOCKET sock;

	CredHandle cred;
	int have_cred;
	CtxtHandle ctx;
	int have_ctx;
	SecPkgContext_StreamSizes sizes;

	HCERTSTORE castore; // extra anchors; owned once the handshake succeeds
	int ca_only;
	int skip_verify;
	WCHAR *hostw;
	PCCERT_CONTEXT clientcert; // borrowed, freed by https_sc_clientcert_free

	// Bytes read from the socket that Schannel has not consumed yet.
	unsigned char *enc;
	int enc_cap;
	int enc_len;

	// Plaintext decrypted but not yet handed to the caller.
	unsigned char *dec;
	int dec_cap;
	int dec_len;
	int dec_off;

	// Scratch for EncryptMessage: header + message + trailer.
	unsigned char *out;
	int out_cap;

	// Absolute millisecond-resolution deadline for the operation in flight.
	// Zero means no deadline.
	int64_t deadline_ns;
	int eof;
} https_sc;

static int64_t now_ns(void) {
	return (int64_t)GetTickCount64() * 1000000LL;
}

// ------------------------------------------------------------- socket I/O

// Waits until the socket is ready or the deadline passes.
// Returns 0 on ready, -1 on timeout, -2 on socket error.
static int wait_ready(https_sc *s, int for_write) {
	for (;;) {
		fd_set set;
		FD_ZERO(&set);
		FD_SET(s->sock, &set);

		struct timeval tv;
		struct timeval *tvp = NULL;
		if (s->deadline_ns != 0) {
			int64_t remaining = s->deadline_ns - now_ns();
			if (remaining <= 0) return -1;
			tv.tv_sec = (long)(remaining / 1000000000LL);
			tv.tv_usec = (long)((remaining % 1000000000LL) / 1000);
			tvp = &tv;
		}

		// The first argument is ignored on winsock.
		int rc = for_write ? select(0, NULL, &set, NULL, tvp)
		                   : select(0, &set, NULL, NULL, tvp);
		if (rc > 0) return 0;
		if (rc == 0) return -1;
		if (WSAGetLastError() == WSAEINTR) continue;
		return -2;
	}
}

// Returns the byte count, 0 on clean EOF, -1 on timeout, -2 on socket error.
static int sock_recv(https_sc *s, void *buf, int len) {
	int w = wait_ready(s, 0);
	if (w != 0) return w;
	int n = recv(s->sock, (char *)buf, len, 0);
	if (n == 0) {
		s->eof = 1;
		return 0;
	}
	if (n == SOCKET_ERROR) return -2;
	return n;
}

// Returns 0, -1 on timeout, -2 on socket error.
static int sock_send_all(https_sc *s, const void *buf, int len) {
	int sent = 0;
	while (sent < len) {
		int w = wait_ready(s, 1);
		if (w != 0) return w;
		int n = send(s->sock, (const char *)buf + sent, len - sent, 0);
		if (n == SOCKET_ERROR) return -2;
		sent += n;
	}
	return 0;
}

// Maps a socket helper result onto an error class. Only called for negatives.
static int sock_class(int rc) {
	return rc == -1 ? HTTPS_SC_ERR_TIMEOUT : HTTPS_SC_ERR_IO;
}

// ------------------------------------------------------------- buffers

// Grows *buf to hold at least want bytes, preserving the first used bytes.
// Returns 0 on success.
static int grow(unsigned char **buf, int *cap, int want, int used, int limit) {
	if (*cap >= want) return 0;
	if (want > limit) return -1;
	int ncap = *cap > 0 ? *cap : 1024;
	while (ncap < want) ncap *= 2;
	if (ncap > limit) ncap = limit;
	unsigned char *nb = (unsigned char *)malloc((size_t)ncap);
	if (nb == NULL) return -1;
	if (used > 0 && *buf != NULL) memcpy(nb, *buf, (size_t)used);
	free(*buf);
	*buf = nb;
	*cap = ncap;
	return 0;
}

// ---------------------------------------------------------------- anchors

uintptr_t https_sc_calist_new(void) {
	// An in-memory store: the user's certificate store is never touched.
	return (uintptr_t)CertOpenStore(CERT_STORE_PROV_MEMORY, 0, 0,
	                                CERT_STORE_CREATE_NEW_FLAG, NULL);
}

int https_sc_calist_add(uintptr_t list, const void *der, int len) {
	HCERTSTORE store = (HCERTSTORE)list;
	if (store == NULL || der == NULL || len <= 0) return HTTPS_SC_ERR_SETUP;
	if (!CertAddEncodedCertificateToStore(store, X509_ASN_ENCODING,
	                                      (const BYTE *)der, (DWORD)len,
	                                      CERT_STORE_ADD_ALWAYS, NULL)) {
		return HTTPS_SC_ERR_CA; // not a valid DER certificate
	}
	return HTTPS_SC_OK;
}

void https_sc_calist_free(uintptr_t list) {
	if (list != 0) CertCloseStore((HCERTSTORE)list, 0);
}

// ---------------------------------------------------------- client certs

// Decodes a PKCS#8 PrivateKeyInfo down to the bare PKCS#1 RSAPrivateKey it
// wraps. *out must be freed with LocalFree. Returns 0 on success.
static int unwrap_pkcs8(const void *der, int len, BYTE **out, DWORD *outlen) {
	CRYPT_PRIVATE_KEY_INFO *info = NULL;
	DWORD infolen = 0;
	if (!CryptDecodeObjectEx(X509_ASN_ENCODING, PKCS_PRIVATE_KEY_INFO,
	                         (const BYTE *)der, (DWORD)len,
	                         CRYPT_DECODE_ALLOC_FLAG, NULL, &info, &infolen)) {
		return -1;
	}
	// Only RSA is importable through the legacy blob path below.
	if (info->Algorithm.pszObjId == NULL ||
	    strcmp(info->Algorithm.pszObjId, szOID_RSA_RSA) != 0) {
		LocalFree(info);
		return -2;
	}
	BYTE *copy = (BYTE *)malloc(info->PrivateKey.cbData);
	if (copy == NULL) {
		LocalFree(info);
		return -1;
	}
	memcpy(copy, info->PrivateKey.pbData, info->PrivateKey.cbData);
	*out = copy;
	*outlen = info->PrivateKey.cbData;
	LocalFree(info);
	return 0;
}

int https_sc_clientcert_new(const void *cert_der, int cert_len,
                            const void *key_der, int key_len, int key_pkcs8,
                            uintptr_t *out, int *status) {
	*out = 0;
	*status = 0;
	if (cert_der == NULL || cert_len <= 0 || key_der == NULL || key_len <= 0) {
		return HTTPS_SC_ERR_CLIENT_CERT;
	}

	PCCERT_CONTEXT cert = CertCreateCertificateContext(
	    X509_ASN_ENCODING | PKCS_7_ASN_ENCODING, (const BYTE *)cert_der,
	    (DWORD)cert_len);
	if (cert == NULL) {
		*status = (int)GetLastError();
		return HTTPS_SC_ERR_CLIENT_CERT;
	}

	// Reduce whatever the caller gave us to a bare PKCS#1 RSAPrivateKey.
	const BYTE *pkcs1 = (const BYTE *)key_der;
	DWORD pkcs1len = (DWORD)key_len;
	BYTE *unwrapped = NULL;
	if (key_pkcs8) {
		DWORD n = 0;
		int rc = unwrap_pkcs8(key_der, key_len, &unwrapped, &n);
		if (rc != 0) {
			CertFreeCertificateContext(cert);
			// -2 means a key algorithm this file cannot import, which is a
			// capability limit rather than a malformed input.
			*status = rc == -2 ? SC_STATUS_KEY_UNSUPPORTED : (int)GetLastError();
			return HTTPS_SC_ERR_CLIENT_CERT;
		}
		pkcs1 = unwrapped;
		pkcs1len = n;
	}

	// CryptDecodeObjectEx produces a legacy CAPI PRIVATEKEYBLOB, which CNG
	// accepts under LEGACY_RSAPRIVATE_BLOB. Going through CNG rather than
	// CryptAcquireContext is what keeps the key ephemeral: the legacy API has
	// no way to import a private key without naming a key container, and a
	// named container is a file in the user's profile.
	BYTE *blob = NULL;
	DWORD bloblen = 0;
	BOOL ok = CryptDecodeObjectEx(X509_ASN_ENCODING, PKCS_RSA_PRIVATE_KEY,
	                              pkcs1, pkcs1len, CRYPT_DECODE_ALLOC_FLAG,
	                              NULL, &blob, &bloblen);
	if (unwrapped != NULL) free(unwrapped);
	if (!ok) {
		*status = (int)GetLastError();
		CertFreeCertificateContext(cert);
		return HTTPS_SC_ERR_CLIENT_CERT;
	}

	NCRYPT_PROV_HANDLE prov = 0;
	SECURITY_STATUS ss = NCryptOpenStorageProvider(&prov, MS_KEY_STORAGE_PROVIDER, 0);
	if (ss != ERROR_SUCCESS) {
		LocalFree(blob);
		CertFreeCertificateContext(cert);
		*status = (int)ss;
		return HTTPS_SC_ERR_CLIENT_CERT;
	}

	// No NCRYPT_PERSIST_FLAG: the key exists only for this process.
	NCRYPT_KEY_HANDLE key = 0;
	ss = NCryptImportKey(prov, 0, LEGACY_RSAPRIVATE_BLOB, NULL, &key, blob,
	                     bloblen, 0);
	LocalFree(blob);
	if (ss != ERROR_SUCCESS) {
		NCryptFreeObject(prov);
		CertFreeCertificateContext(cert);
		*status = (int)ss;
		return HTTPS_SC_ERR_CLIENT_CERT;
	}

	// The certificate context takes ownership of the key handle, and
	// INHIBIT_PERSIST keeps the association out of any store on disk.
	if (!CertSetCertificateContextProperty(cert, CERT_NCRYPT_KEY_HANDLE_PROP_ID,
	                                       CERT_SET_PROPERTY_INHIBIT_PERSIST_FLAG,
	                                       &key)) {
		*status = (int)GetLastError();
		NCryptFreeObject(key);
		NCryptFreeObject(prov);
		CertFreeCertificateContext(cert);
		return HTTPS_SC_ERR_CLIENT_CERT;
	}

	sc_clientcert *cc = (sc_clientcert *)calloc(1, sizeof(sc_clientcert));
	if (cc == NULL) {
		CertFreeCertificateContext(cert); // frees the key with it
		NCryptFreeObject(prov);
		return HTTPS_SC_ERR_ALLOC;
	}
	cc->cert = cert;
	cc->prov = prov;
	*out = (uintptr_t)cc;
	return HTTPS_SC_OK;
}

void https_sc_clientcert_free(uintptr_t handle) {
	sc_clientcert *cc = (sc_clientcert *)handle;
	if (cc == NULL) return;
	if (cc->cert != NULL) CertFreeCertificateContext(cc->cert);
	if (cc->prov != 0) NCryptFreeObject(cc->prov);
	free(cc);
}

// ------------------------------------------------------------ credentials

static DWORD disabled_below(int min_version) {
	DWORD disabled = SC_PROT_SSL2_CLIENT | SC_PROT_SSL3_CLIENT |
	                 SC_PROT_TLS1_0_CLIENT | SC_PROT_TLS1_1_CLIENT;
	if (min_version >= HTTPS_SC_TLS13) disabled |= SC_PROT_TLS1_2_CLIENT;
	return disabled;
}

static DWORD enabled_from(int min_version) {
	DWORD enabled = SC_PROT_TLS1_2_CLIENT | SC_PROT_TLS1_3_CLIENT;
	if (min_version >= HTTPS_SC_TLS13) enabled = SC_PROT_TLS1_3_CLIENT;
	return enabled;
}

// Verification is always taken over from Schannel, even with no custom anchors
// and no skip-verify, so that the trust decision is made in one place and
// behaves the same as the other backends. verify_chain below is what replaces
// it.
static DWORD cred_flags(void) {
	return SC_CRED_MANUAL_CRED_VALIDATION | SC_CRED_NO_DEFAULT_CREDS |
	       SC_CRED_NO_SYSTEM_MAPPER | SC_USE_STRONG_CRYPTO;
}

// Acquires an outbound credential. SCH_CREDENTIALS is tried first because it
// is the only structure that reaches TLS 1.3; implementations that predate it,
// including Wine's, reject it and get SCHANNEL_CRED instead, which caps the
// connection at TLS 1.2.
static SECURITY_STATUS acquire_cred(https_sc *s, int min_version) {
	PCCERT_CONTEXT certs[1];
	DWORD ccerts = 0;
	if (s->clientcert != NULL) {
		certs[0] = s->clientcert;
		ccerts = 1;
	}

	TimeStamp expiry;

	sc_tls_parameters params;
	memset(&params, 0, sizeof(params));
	params.grbitDisabledProtocols = disabled_below(min_version);

	sc_credentials modern;
	memset(&modern, 0, sizeof(modern));
	modern.dwVersion = SC_CREDENTIALS_VERSION;
	modern.dwFlags = cred_flags();
	modern.cCreds = ccerts;
	modern.paCred = ccerts > 0 ? certs : NULL;
	modern.cTlsParameters = 1;
	modern.pTlsParameters = &params;

	SECURITY_STATUS ss = AcquireCredentialsHandleW(
	    NULL, (SEC_WCHAR *)UNISP_NAME_W, SECPKG_CRED_OUTBOUND, NULL, &modern,
	    NULL, NULL, &s->cred, &expiry);
	if (ss == SEC_E_OK) {
		s->have_cred = 1;
		return SEC_E_OK;
	}

	sc_schannel_cred legacy;
	memset(&legacy, 0, sizeof(legacy));
	legacy.dwVersion = SC_SCHANNEL_CRED_VERSION;
	legacy.dwFlags = cred_flags();
	legacy.cCreds = ccerts;
	legacy.paCred = ccerts > 0 ? certs : NULL;
	legacy.grbitEnabledProtocols = enabled_from(min_version);

	ss = AcquireCredentialsHandleW(NULL, (SEC_WCHAR *)UNISP_NAME_W,
	                               SECPKG_CRED_OUTBOUND, NULL, &legacy, NULL,
	                               NULL, &s->cred, &expiry);
	if (ss == SEC_E_OK) s->have_cred = 1;
	return ss;
}

// ------------------------------------------------------------------ trust

// Runs the SSL chain policy over one built chain. Returns 0 when the chain is
// acceptable, otherwise writes the policy status and returns -1.
static int check_policy(https_sc *s, PCCERT_CHAIN_CONTEXT chain, int *status) {
	SSL_EXTRA_CERT_CHAIN_POLICY_PARA extra;
	memset(&extra, 0, sizeof(extra));
	extra.cbSize = sizeof(extra);
	extra.dwAuthType = AUTHTYPE_SERVER;
	extra.pwszServerName = s->hostw;

	CERT_CHAIN_POLICY_PARA para;
	memset(&para, 0, sizeof(para));
	para.cbSize = sizeof(para);
	para.pvExtraPolicyPara = &extra;

	CERT_CHAIN_POLICY_STATUS st;
	memset(&st, 0, sizeof(st));
	st.cbSize = sizeof(st);

	if (!CertVerifyCertificateChainPolicy(CERT_CHAIN_POLICY_SSL, chain, &para, &st)) {
		*status = (int)GetLastError();
		return -1;
	}
	if (st.dwError != 0) {
		*status = (int)st.dwError;
		return -1;
	}
	// The policy check is authoritative, but a non-zero trust status with a
	// clean policy result would mean trusting something the engine flagged.
	//
	// The revocation bits are masked off first. Revocation is deliberately not
	// checked, and an engine that was never asked to check it still reports
	// "unknown" on some configurations; treating that as fatal would reject
	// every chain.
	DWORD fatal = chain->TrustStatus.dwErrorStatus &
	              ~(DWORD)(SC_TRUST_REVOCATION_STATUS_UNKNOWN |
	                       SC_TRUST_IS_OFFLINE_REVOCATION |
	                       SC_TRUST_IS_NOT_TIME_NESTED);
	if (fatal != CERT_TRUST_NO_ERROR) {
		*status = (int)fatal;
		return -1;
	}
	return 0;
}

// Builds and checks a chain for cert. When exclusive is set the extra anchors
// are the only roots that count; otherwise the system store is used and the
// extra anchors serve as intermediates.
static int verify_with(https_sc *s, PCCERT_CONTEXT cert, int exclusive, int *status) {
	HCERTCHAINENGINE engine = NULL;
	if (exclusive) {
		// A NULL exclusive root restores the system defaults, which is the
		// exact opposite of what an exclusive check means. The Go side always
		// creates the store when RootCAsOnly is set, even with no anchors, so
		// this only fires if the C is driven directly. Fail closed rather than
		// silently widen the trust set.
		if (s->castore == NULL) {
			*status = (int)CERT_E_UNTRUSTEDROOT;
			return -1;
		}

		// Note for anyone testing under wine: its crypt32 accepts only the
		// pre-Vista config size and fails this call with E_INVALIDARG, because
		// it has no hExclusiveRoot. Shrinking cbSize to match would drop the
		// field and silently restore the system roots, so there is no correct
		// fallback and none is attempted. Windows has no additive
		// "system roots plus these" mode either.
		sc_chain_engine_config cfg;
		memset(&cfg, 0, sizeof(cfg));
		cfg.cbSize = sizeof(cfg);
		// An empty store is what actually trusts nothing, which is the intent
		// when the caller sets RootCAsOnly with no anchors.
		cfg.hExclusiveRoot = s->castore;
		cfg.dwExclusiveFlags = SC_CHAIN_EXCLUSIVE_ENABLE_CA_FLAG;
		if (!CertCreateCertificateChainEngine((PCERT_CHAIN_ENGINE_CONFIG)&cfg, &engine)) {
			*status = (int)GetLastError();
			return -1;
		}
	}

	CERT_CHAIN_PARA para;
	memset(&para, 0, sizeof(para));
	para.cbSize = sizeof(para);
	para.RequestedUsage.dwType = USAGE_MATCH_TYPE_AND;
	para.RequestedUsage.Usage.cUsageIdentifier = 0;

	// The intermediates the peer sent live in the certificate's own store,
	// which CertGetCertificateChain searches alongside hAdditionalStore.
	//
	// No revocation flags means no revocation checking, matching crypto/tls and
	// the other two backends. Turning it on would also put a network fetch on
	// the handshake path.
	PCCERT_CHAIN_CONTEXT chain = NULL;
	BOOL built = CertGetCertificateChain(engine, cert, NULL, s->castore, &para,
	                                     0, NULL, &chain);
	if (!built) {
		*status = (int)GetLastError();
		if (engine != NULL) CertFreeCertificateChainEngine(engine);
		return -1;
	}

	int rc = check_policy(s, chain, status);
	CertFreeCertificateChain(chain);
	if (engine != NULL) CertFreeCertificateChainEngine(engine);
	return rc;
}

// Verifies the peer certificate after the handshake completes.
//
// Schannel has no hook for validating mid-handshake the way Secure Transport's
// break-on-server-auth does, so with manual validation the handshake finishes
// first and this runs before any application data moves. That is the shape
// every Schannel client has.
static int verify_chain(https_sc *s, int *status) {
	if (s->skip_verify) return 0;

	PCCERT_CONTEXT cert = NULL;
	SECURITY_STATUS ss = QueryContextAttributesW(
	    &s->ctx, SECPKG_ATTR_REMOTE_CERT_CONTEXT, &cert);
	if (ss != SEC_E_OK || cert == NULL) {
		*status = (int)ss;
		return -1;
	}

	int rc;
	if (s->ca_only) {
		rc = verify_with(s, cert, 1, status);
	} else {
		// System roots first: they are the default trust source. Custom
		// anchors are the narrower addition, so their failure is the more
		// useful one to report when neither path works.
		rc = verify_with(s, cert, 0, status);
		if (rc != 0 && s->castore != NULL) {
			rc = verify_with(s, cert, 1, status);
		}
	}

	CertFreeCertificateContext(cert);
	return rc;
}

// -------------------------------------------------------------- handshake

static DWORD isc_flags(void) {
	return ISC_REQ_SEQUENCE_DETECT | ISC_REQ_REPLAY_DETECT |
	       ISC_REQ_CONFIDENTIALITY | ISC_RET_EXTENDED_ERROR |
	       ISC_REQ_ALLOCATE_MEMORY | ISC_REQ_STREAM;
}

// Drives InitializeSecurityContext until the handshake completes. Serves both
// the initial handshake and a renegotiation, which differ only in whether a
// context already exists and whether the first token is generated locally.
static int do_handshake(https_sc *s, int *status) {
	DWORD req = isc_flags();
	DWORD attrs = 0;
	TimeStamp expiry;
	SECURITY_STATUS ss;
	int need_read;

	if (!s->have_ctx) {
		SecBuffer outbuf;
		memset(&outbuf, 0, sizeof(outbuf));
		outbuf.BufferType = SECBUFFER_TOKEN;
		SecBufferDesc outdesc = {SECBUFFER_VERSION, 1, &outbuf};

		ss = InitializeSecurityContextW(&s->cred, NULL, s->hostw, req, 0, 0,
		                                NULL, 0, &s->ctx, &outdesc, &attrs,
		                                &expiry);
		if (ss != SEC_I_CONTINUE_NEEDED) {
			*status = (int)ss;
			if (outbuf.pvBuffer != NULL) FreeContextBuffer(outbuf.pvBuffer);
			return HTTPS_SC_ERR_HANDSHAKE;
		}
		s->have_ctx = 1;

		if (outbuf.cbBuffer > 0 && outbuf.pvBuffer != NULL) {
			int rc = sock_send_all(s, outbuf.pvBuffer, (int)outbuf.cbBuffer);
			FreeContextBuffer(outbuf.pvBuffer);
			if (rc != 0) return sock_class(rc);
		} else if (outbuf.pvBuffer != NULL) {
			FreeContextBuffer(outbuf.pvBuffer);
		}
		need_read = 1;
	} else {
		// Renegotiation: whatever is already buffered is the first input.
		need_read = s->enc_len == 0;
	}

	int retried_creds = 0;
	for (;;) {
		if (need_read) {
			if (s->enc_len >= s->enc_cap &&
			    grow(&s->enc, &s->enc_cap, s->enc_cap * 2, s->enc_len, SC_ENC_MAX) != 0) {
				return HTTPS_SC_ERR_ALLOC;
			}
			int n = sock_recv(s, s->enc + s->enc_len, s->enc_cap - s->enc_len);
			if (n < 0) return sock_class(n);
			if (n == 0) {
				// The peer hung up mid-handshake.
				*status = (int)SEC_E_INCOMPLETE_MESSAGE;
				return HTTPS_SC_ERR_HANDSHAKE;
			}
			s->enc_len += n;
		}

		SecBuffer inbuf[2];
		memset(inbuf, 0, sizeof(inbuf));
		inbuf[0].BufferType = SECBUFFER_TOKEN;
		inbuf[0].pvBuffer = s->enc;
		inbuf[0].cbBuffer = (unsigned long)s->enc_len;
		inbuf[1].BufferType = SECBUFFER_EMPTY;
		SecBufferDesc indesc = {SECBUFFER_VERSION, 2, inbuf};

		SecBuffer outbuf[2];
		memset(outbuf, 0, sizeof(outbuf));
		outbuf[0].BufferType = SECBUFFER_TOKEN;
		outbuf[1].BufferType = SECBUFFER_ALERT;
		SecBufferDesc outdesc = {SECBUFFER_VERSION, 2, outbuf};

		attrs = 0;
		ss = InitializeSecurityContextW(&s->cred, &s->ctx, NULL, req, 0, 0,
		                                &indesc, 0, NULL, &outdesc, &attrs,
		                                &expiry);

		if (ss == SEC_E_INCOMPLETE_MESSAGE) {
			// Not a whole record yet. Keep what we have and read more.
			if (s->enc_len >= s->enc_cap &&
			    grow(&s->enc, &s->enc_cap, s->enc_cap * 2, s->enc_len, SC_ENC_MAX) != 0) {
				return HTTPS_SC_ERR_ALLOC;
			}
			need_read = 1;
			continue;
		}

		// A token is produced on success and, when the peer needs to be told
		// why, on failure too.
		int send_rc = 0;
		if (outbuf[0].cbBuffer > 0 && outbuf[0].pvBuffer != NULL) {
			send_rc = sock_send_all(s, outbuf[0].pvBuffer, (int)outbuf[0].cbBuffer);
		}
		if (outbuf[0].pvBuffer != NULL) FreeContextBuffer(outbuf[0].pvBuffer);
		if (outbuf[1].pvBuffer != NULL) FreeContextBuffer(outbuf[1].pvBuffer);
		if (send_rc != 0 && (ss == SEC_E_OK || ss == SEC_I_CONTINUE_NEEDED)) {
			return sock_class(send_rc);
		}

		if (ss == SEC_I_INCOMPLETE_CREDENTIALS && !retried_creds) {
			// The server asked for a client certificate we do not have. Most
			// servers accept an empty certificate list, so say so explicitly
			// rather than failing here.
			retried_creds = 1;
			req |= ISC_REQ_USE_SUPPLIED_CREDS;
			need_read = 0;
			continue;
		}

		// Consume the input Schannel accepted, keeping any trailing bytes that
		// belong to the next record.
		if (inbuf[1].BufferType == SECBUFFER_EXTRA && inbuf[1].cbBuffer > 0) {
			int extra = (int)inbuf[1].cbBuffer;
			memmove(s->enc, s->enc + (s->enc_len - extra), (size_t)extra);
			s->enc_len = extra;
		} else {
			s->enc_len = 0;
		}

		if (ss == SEC_E_OK) return HTTPS_SC_OK;
		if (ss == SEC_I_CONTINUE_NEEDED) {
			need_read = s->enc_len == 0;
			continue;
		}

		*status = (int)ss;
		return HTTPS_SC_ERR_HANDSHAKE;
	}
}

// ----------------------------------------------------------------- teardown

static void free_state(https_sc *s) {
	if (s == NULL) return;
	if (s->have_ctx) DeleteSecurityContext(&s->ctx);
	if (s->have_cred) FreeCredentialsHandle(&s->cred);
	free(s->enc);
	free(s->dec);
	free(s->out);
	free(s->hostw);
	free(s);
}

int https_sc_handshake(uintptr_t sock, const char *host, int skip_verify,
                       uintptr_t calist, int ca_only, int min_version,
                       uintptr_t clientcert, int64_t timeout_ns,
                       uintptr_t *out, int *status) {
	*out = 0;
	*status = 0;

	https_sc *s = (https_sc *)calloc(1, sizeof(https_sc));
	if (s == NULL) return HTTPS_SC_ERR_ALLOC;
	s->sock = (SOCKET)sock;
	s->castore = (HCERTSTORE)calist;
	s->ca_only = ca_only;
	s->skip_verify = skip_verify;
	s->deadline_ns = timeout_ns > 0 ? now_ns() + timeout_ns : 0;
	if (clientcert != 0) {
		s->clientcert = ((sc_clientcert *)clientcert)->cert;
	}

	if (host == NULL || host[0] == '\0') {
		free_state(s);
		return HTTPS_SC_ERR_SETUP;
	}
	// One UTF-16 copy serves both SNI and the name the chain policy checks.
	int wlen = MultiByteToWideChar(CP_UTF8, 0, host, -1, NULL, 0);
	if (wlen <= 0) {
		free_state(s);
		return HTTPS_SC_ERR_SETUP;
	}
	s->hostw = (WCHAR *)malloc((size_t)wlen * sizeof(WCHAR));
	if (s->hostw == NULL) {
		free_state(s);
		return HTTPS_SC_ERR_ALLOC;
	}
	if (MultiByteToWideChar(CP_UTF8, 0, host, -1, s->hostw, wlen) <= 0) {
		free_state(s);
		return HTTPS_SC_ERR_SETUP;
	}

	if (grow(&s->enc, &s->enc_cap, SC_ENC_INITIAL, 0, SC_ENC_MAX) != 0) {
		free_state(s);
		return HTTPS_SC_ERR_ALLOC;
	}

	SECURITY_STATUS ss = acquire_cred(s, min_version);
	if (ss != SEC_E_OK) {
		*status = (int)ss;
		free_state(s);
		return HTTPS_SC_ERR_SETUP;
	}

	int rc = do_handshake(s, status);
	if (rc != HTTPS_SC_OK) {
		free_state(s);
		return rc;
	}

	if (verify_chain(s, status) != 0) {
		free_state(s);
		return HTTPS_SC_ERR_HANDSHAKE;
	}

	ss = QueryContextAttributesW(&s->ctx, SECPKG_ATTR_STREAM_SIZES, &s->sizes);
	if (ss != SEC_E_OK) {
		*status = (int)ss;
		free_state(s);
		return HTTPS_SC_ERR_SETUP;
	}

	int outcap = (int)(s->sizes.cbHeader + s->sizes.cbMaximumMessage + s->sizes.cbTrailer);
	if (grow(&s->out, &s->out_cap, outcap, 0, SC_ENC_MAX) != 0) {
		free_state(s);
		return HTTPS_SC_ERR_ALLOC;
	}

	s->deadline_ns = 0;
	*out = (uintptr_t)s;
	return HTTPS_SC_OK;
}

// ---------------------------------------------------------------- records

// Appends one decrypted record to the pending plaintext.
static int stash_plaintext(https_sc *s, const void *data, int len) {
	if (len <= 0) return 0;
	// Compact first: the buffer only ever holds what one Read has not taken.
	if (s->dec_off > 0) {
		memmove(s->dec, s->dec + s->dec_off, (size_t)(s->dec_len - s->dec_off));
		s->dec_len -= s->dec_off;
		s->dec_off = 0;
	}
	if (grow(&s->dec, &s->dec_cap, s->dec_len + len, s->dec_len, SC_ENC_MAX) != 0) {
		return -1;
	}
	memcpy(s->dec + s->dec_len, data, (size_t)len);
	s->dec_len += len;
	return 0;
}

static int take_plaintext(https_sc *s, void *buf, int len) {
	int avail = s->dec_len - s->dec_off;
	if (avail <= 0) return 0;
	int n = avail < len ? avail : len;
	memcpy(buf, s->dec + s->dec_off, (size_t)n);
	s->dec_off += n;
	if (s->dec_off == s->dec_len) {
		s->dec_off = 0;
		s->dec_len = 0;
	}
	return n;
}

int https_sc_read(uintptr_t handle, void *buf, int len,
                  int64_t timeout_ns, int *n, int *status) {
	https_sc *s = (https_sc *)handle;
	*n = 0;
	*status = 0;
	if (s == NULL || !s->have_ctx) return HTTPS_SC_ERR_CLOSED;
	if (len <= 0) return HTTPS_SC_OK;

	if (s->dec_len > s->dec_off) {
		*n = take_plaintext(s, buf, len);
		return HTTPS_SC_OK;
	}

	s->deadline_ns = timeout_ns > 0 ? now_ns() + timeout_ns : 0;

	for (;;) {
		if (s->enc_len > 0) {
			SecBuffer b[4];
			memset(b, 0, sizeof(b));
			b[0].BufferType = SECBUFFER_DATA;
			b[0].pvBuffer = s->enc;
			b[0].cbBuffer = (unsigned long)s->enc_len;
			b[1].BufferType = SECBUFFER_EMPTY;
			b[2].BufferType = SECBUFFER_EMPTY;
			b[3].BufferType = SECBUFFER_EMPTY;
			SecBufferDesc desc = {SECBUFFER_VERSION, 4, b};

			SECURITY_STATUS ss = DecryptMessage(&s->ctx, &desc, 0, NULL);

			if (ss == SEC_E_OK || ss == SEC_I_RENEGOTIATE ||
			    ss == SEC_I_CONTEXT_EXPIRED) {
				SecBuffer *data = NULL;
				SecBuffer *extra = NULL;
				for (int i = 1; i < 4; i++) {
					if (data == NULL && b[i].BufferType == SECBUFFER_DATA) {
						data = &b[i];
					} else if (extra == NULL && b[i].BufferType == SECBUFFER_EXTRA) {
						extra = &b[i];
					}
				}

				// Both buffers point into s->enc, so the plaintext has to be
				// copied out before the leftovers are moved to the front.
				if (data != NULL && data->cbBuffer > 0) {
					if (stash_plaintext(s, data->pvBuffer, (int)data->cbBuffer) != 0) {
						return HTTPS_SC_ERR_ALLOC;
					}
				}
				if (extra != NULL && extra->cbBuffer > 0) {
					int len2 = (int)extra->cbBuffer;
					memmove(s->enc, extra->pvBuffer, (size_t)len2);
					s->enc_len = len2;
				} else {
					s->enc_len = 0;
				}

				if (ss == SEC_I_CONTEXT_EXPIRED) {
					s->eof = 1; // the peer sent close_notify
				} else if (ss == SEC_I_RENEGOTIATE) {
					int rc = do_handshake(s, status);
					if (rc != HTTPS_SC_OK) return rc;
				}

				if (s->dec_len > s->dec_off) break;
				if (s->eof) break;
				continue;
			}

			if (ss != SEC_E_INCOMPLETE_MESSAGE) {
				*status = (int)ss;
				return HTTPS_SC_ERR_IO;
			}
			// Fall through and read the rest of the record.
			if (s->enc_len >= s->enc_cap &&
			    grow(&s->enc, &s->enc_cap, s->enc_cap * 2, s->enc_len, SC_ENC_MAX) != 0) {
				return HTTPS_SC_ERR_ALLOC;
			}
		}

		if (s->eof) break;

		int rn = sock_recv(s, s->enc + s->enc_len, s->enc_cap - s->enc_len);
		if (rn < 0) return sock_class(rn);
		if (rn == 0) break; // sock_recv set eof; a truncated record is dropped
		s->enc_len += rn;
	}

	*n = take_plaintext(s, buf, len);
	return HTTPS_SC_OK;
}

int https_sc_write(uintptr_t handle, const void *buf, int len,
                   int64_t timeout_ns, int *n, int *status) {
	https_sc *s = (https_sc *)handle;
	*n = 0;
	*status = 0;
	if (s == NULL || !s->have_ctx) return HTTPS_SC_ERR_CLOSED;
	if (len <= 0) return HTTPS_SC_OK;

	s->deadline_ns = timeout_ns > 0 ? now_ns() + timeout_ns : 0;

	int sent = 0;
	while (sent < len) {
		int chunk = len - sent;
		if (chunk > (int)s->sizes.cbMaximumMessage) {
			chunk = (int)s->sizes.cbMaximumMessage;
		}
		memcpy(s->out + s->sizes.cbHeader, (const char *)buf + sent, (size_t)chunk);

		SecBuffer b[4];
		memset(b, 0, sizeof(b));
		b[0].BufferType = SECBUFFER_STREAM_HEADER;
		b[0].pvBuffer = s->out;
		b[0].cbBuffer = s->sizes.cbHeader;
		b[1].BufferType = SECBUFFER_DATA;
		b[1].pvBuffer = s->out + s->sizes.cbHeader;
		b[1].cbBuffer = (unsigned long)chunk;
		b[2].BufferType = SECBUFFER_STREAM_TRAILER;
		b[2].pvBuffer = s->out + s->sizes.cbHeader + chunk;
		b[2].cbBuffer = s->sizes.cbTrailer;
		b[3].BufferType = SECBUFFER_EMPTY;
		SecBufferDesc desc = {SECBUFFER_VERSION, 4, b};

		SECURITY_STATUS ss = EncryptMessage(&s->ctx, 0, &desc, 0);
		if (ss != SEC_E_OK) {
			*status = (int)ss;
			*n = sent;
			return HTTPS_SC_ERR_IO;
		}

		// The three buffers are contiguous by construction, so the record is
		// one write even though EncryptMessage may have resized the trailer.
		int total = (int)(b[0].cbBuffer + b[1].cbBuffer + b[2].cbBuffer);
		int rc = sock_send_all(s, s->out, total);
		if (rc != 0) {
			*n = sent;
			return sock_class(rc);
		}
		sent += chunk;
		*n = sent;
	}
	return HTTPS_SC_OK;
}

void https_sc_close(uintptr_t handle) {
	https_sc *s = (https_sc *)handle;
	if (s == NULL) return;

	if (s->have_ctx && s->have_cred) {
		// Best effort close_notify. A peer that has already gone away must not
		// block Close, hence the short deadline.
		s->deadline_ns = now_ns() + SC_SHUTDOWN_NS;

		DWORD token = SCHANNEL_SHUTDOWN;
		SecBuffer tb;
		memset(&tb, 0, sizeof(tb));
		tb.BufferType = SECBUFFER_TOKEN;
		tb.pvBuffer = &token;
		tb.cbBuffer = sizeof(token);
		SecBufferDesc tdesc = {SECBUFFER_VERSION, 1, &tb};

		if (ApplyControlToken(&s->ctx, &tdesc) == SEC_E_OK) {
			SecBuffer ob;
			memset(&ob, 0, sizeof(ob));
			ob.BufferType = SECBUFFER_TOKEN;
			SecBufferDesc odesc = {SECBUFFER_VERSION, 1, &ob};

			DWORD attrs = 0;
			TimeStamp expiry;
			SECURITY_STATUS ss = InitializeSecurityContextW(
			    &s->cred, &s->ctx, NULL, isc_flags(), 0, 0, NULL, 0, NULL,
			    &odesc, &attrs, &expiry);
			if ((ss == SEC_E_OK || ss == SEC_I_CONTEXT_EXPIRED) &&
			    ob.cbBuffer > 0 && ob.pvBuffer != NULL) {
				sock_send_all(s, ob.pvBuffer, (int)ob.cbBuffer);
			}
			if (ob.pvBuffer != NULL) FreeContextBuffer(ob.pvBuffer);
		}
	}

	// The connection owns the anchors once the handshake succeeded.
	if (s->castore != NULL) {
		CertCloseStore(s->castore, 0);
		s->castore = NULL;
	}
	free_state(s);
}
