//go:build (tinygo || force_tinygo_logic) && windows

// CNG RSA signing.
//
// Unlike the darwin backend, the key cannot be handed over as DER: CNG wants a
// BCRYPT_RSAKEY_BLOB, which is a header followed by the modulus and the CRT
// components as big-endian byte arrays. Taking a PKCS#1 structure apart into
// those pieces by hand would be a real ASN.1 decoder, which is exactly what the
// forty-line walker in pkcs8.go must not become.
//
// crypt32 already knows the conversion, and it is already linked here for
// certificate handling, so CryptDecodeObjectEx does that step and the walker
// stays a walker.
//
// Unlike the linux and darwin backends this one has never been executed. It
// cross-compiles and links under mingw-w64, which is the same state
// system:schannel ships in; see requirement:windows-tinygo-feasibility.

#include "rsasign.h"

#include <windows.h>
#include <wincrypt.h>
#include <bcrypt.h>

#ifndef CNG_RSA_PRIVATE_KEY_BLOB
#define CNG_RSA_PRIVATE_KEY_BLOB ((LPCSTR)83)
#endif

#ifndef PKCS_RSA_PRIVATE_KEY
#define PKCS_RSA_PRIVATE_KEY ((LPCSTR)43)
#endif

#ifndef STATUS_SUCCESS
#define STATUS_SUCCESS ((NTSTATUS)0)
#endif

typedef struct {
	BCRYPT_ALG_HANDLE alg;
	BCRYPT_KEY_HANDLE key;
	ULONG bits;
} rsasign_win_key;

// key_der is a PKCS#1 RSAPrivateKey, unwrapped from PKCS#8 by the Go side, the
// same input the darwin backend takes.
int rsasign_load(const unsigned char *key_der, size_t key_len, void **out_key) {
	if (key_der == NULL || key_len == 0) {
		return RSASIGN_ERR_LOAD;
	}

	// PKCS#1 DER to a BCRYPT_RSAKEY_BLOB, allocated by crypt32.
	BYTE *blob = NULL;
	DWORD blob_len = 0;
	if (!CryptDecodeObjectEx(X509_ASN_ENCODING | PKCS_7_ASN_ENCODING,
	                         CNG_RSA_PRIVATE_KEY_BLOB,
	                         key_der, (DWORD)key_len,
	                         CRYPT_DECODE_ALLOC_FLAG, NULL,
	                         &blob, &blob_len)) {
		return RSASIGN_ERR_LOAD;
	}

	rsasign_win_key *k = (rsasign_win_key *)calloc(1, sizeof(rsasign_win_key));
	if (k == NULL) {
		LocalFree(blob);
		return RSASIGN_ERR_LOAD;
	}

	NTSTATUS st = BCryptOpenAlgorithmProvider(&k->alg, BCRYPT_RSA_ALGORITHM, NULL, 0);
	if (st != STATUS_SUCCESS) {
		LocalFree(blob);
		free(k);
		return RSASIGN_ERR_LOAD;
	}
	st = BCryptImportKeyPair(k->alg, NULL, BCRYPT_RSAPRIVATE_BLOB, &k->key,
	                         blob, blob_len, 0);
	// The blob is finished with either way; the key holds its own copy.
	SecureZeroMemory(blob, blob_len);
	LocalFree(blob);
	if (st != STATUS_SUCCESS) {
		BCryptCloseAlgorithmProvider(k->alg, 0);
		free(k);
		return RSASIGN_ERR_LOAD;
	}

	ULONG written = 0;
	st = BCryptGetProperty(k->key, BCRYPT_KEY_STRENGTH,
	                       (PUCHAR)&k->bits, sizeof(k->bits), &written, 0);
	if (st != STATUS_SUCCESS || k->bits == 0) {
		BCryptDestroyKey(k->key);
		BCryptCloseAlgorithmProvider(k->alg, 0);
		free(k);
		return RSASIGN_ERR_LOAD;
	}

	*out_key = (void *)k;
	return RSASIGN_OK;
}

int rsasign_sign(void *key, const unsigned char *digest, size_t digest_len,
                 unsigned char *out, size_t out_cap, size_t *out_len) {
	if (key == NULL) {
		return RSASIGN_ERR_SIGN;
	}
	rsasign_win_key *k = (rsasign_win_key *)key;

	BCRYPT_PKCS1_PADDING_INFO padding;
	padding.pszAlgId = BCRYPT_SHA256_ALGORITHM;

	ULONG needed = 0;
	NTSTATUS st = BCryptSignHash(k->key, &padding,
	                             (PUCHAR)digest, (ULONG)digest_len,
	                             NULL, 0, &needed, BCRYPT_PAD_PKCS1);
	if (st != STATUS_SUCCESS) {
		return RSASIGN_ERR_SIGN;
	}
	if ((size_t)needed > out_cap) {
		return RSASIGN_ERR_SPACE;
	}
	ULONG written = 0;
	st = BCryptSignHash(k->key, &padding,
	                    (PUCHAR)digest, (ULONG)digest_len,
	                    (PUCHAR)out, needed, &written, BCRYPT_PAD_PKCS1);
	if (st != STATUS_SUCCESS) {
		return RSASIGN_ERR_SIGN;
	}
	*out_len = (size_t)written;
	return RSASIGN_OK;
}

void rsasign_free(void *key) {
	if (key == NULL) {
		return;
	}
	rsasign_win_key *k = (rsasign_win_key *)key;
	if (k->key != NULL) {
		BCryptDestroyKey(k->key);
	}
	if (k->alg != NULL) {
		BCryptCloseAlgorithmProvider(k->alg, 0);
	}
	free(k);
}
