//go:build (tinygo || force_tinygo_logic) && darwin

// Security.framework RSA signing, hand-declared.
//
// TinyGo compiles C with -nostdlibinc and rejects -F in CFLAGS, so framework
// headers are unreachable; see rule:tinygo-darwin-toolchain. Everything below
// is declared by hand, the same approach securetransport.c takes.

#include <stddef.h>
#include "rsasign.h"

typedef const void *CFTypeRef;
typedef const struct __CFString *CFStringRef;
typedef const struct __CFData *CFDataRef;
typedef const struct __CFDictionary *CFDictionaryRef;
typedef const struct __CFAllocator *CFAllocatorRef;
typedef struct __CFError *CFErrorRef;
typedef struct __SecKey *SecKeyRef;
typedef CFStringRef SecKeyAlgorithm;
typedef signed long CFIndex;

// CoreFoundation callback tables are structs; take their address, never their value.
extern const char kCFTypeDictionaryKeyCallBacks[];
extern const char kCFTypeDictionaryValueCallBacks[];

extern const CFStringRef kSecAttrKeyType;
extern const CFStringRef kSecAttrKeyClass;
extern const CFStringRef kSecAttrKeyTypeRSA;
extern const CFStringRef kSecAttrKeyClassPrivate;
extern const SecKeyAlgorithm kSecKeyAlgorithmRSASignatureDigestPKCS1v15SHA256;

extern CFDataRef CFDataCreate(CFAllocatorRef, const unsigned char *, CFIndex);
extern const unsigned char *CFDataGetBytePtr(CFDataRef);
extern CFIndex CFDataGetLength(CFDataRef);
extern void CFRelease(CFTypeRef);
extern CFDictionaryRef CFDictionaryCreate(CFAllocatorRef, const void **, const void **,
                                          CFIndex, const void *, const void *);
extern SecKeyRef SecKeyCreateWithData(CFDataRef, CFDictionaryRef, CFErrorRef *);
extern CFDataRef SecKeyCreateSignature(SecKeyRef, SecKeyAlgorithm, CFDataRef, CFErrorRef *);

// key_der is a PKCS#1 RSAPrivateKey. SecKeyCreateWithData does not accept the
// PKCS#8 wrapper Google ships, which the Go side strips before calling here.
int rsasign_load(const unsigned char *key_der, size_t key_len, void **out_key) {
	CFDataRef data = CFDataCreate(NULL, key_der, (CFIndex)key_len);
	if (data == NULL) {
		return RSASIGN_ERR_LOAD;
	}
	const void *keys[2] = {(const void *)kSecAttrKeyType, (const void *)kSecAttrKeyClass};
	const void *vals[2] = {(const void *)kSecAttrKeyTypeRSA, (const void *)kSecAttrKeyClassPrivate};
	CFDictionaryRef attrs = CFDictionaryCreate(NULL, keys, vals, 2,
	                                           &kCFTypeDictionaryKeyCallBacks,
	                                           &kCFTypeDictionaryValueCallBacks);
	if (attrs == NULL) {
		CFRelease(data);
		return RSASIGN_ERR_LOAD;
	}
	CFErrorRef err = NULL;
	SecKeyRef key = SecKeyCreateWithData(data, attrs, &err);
	CFRelease(attrs);
	CFRelease(data);
	if (key == NULL) {
		if (err != NULL) {
			CFRelease(err);
		}
		return RSASIGN_ERR_LOAD;
	}
	*out_key = (void *)key;
	return RSASIGN_OK;
}

int rsasign_sign(void *key, const unsigned char *digest, size_t digest_len,
                 unsigned char *out, size_t out_cap, size_t *out_len) {
	CFDataRef d = CFDataCreate(NULL, digest, (CFIndex)digest_len);
	if (d == NULL) {
		return RSASIGN_ERR_SIGN;
	}
	CFErrorRef err = NULL;
	CFDataRef sig = SecKeyCreateSignature((SecKeyRef)key,
	                                      kSecKeyAlgorithmRSASignatureDigestPKCS1v15SHA256,
	                                      d, &err);
	CFRelease(d);
	if (sig == NULL) {
		if (err != NULL) {
			CFRelease(err);
		}
		return RSASIGN_ERR_SIGN;
	}
	CFIndex n = CFDataGetLength(sig);
	if ((size_t)n > out_cap) {
		CFRelease(sig);
		return RSASIGN_ERR_SPACE;
	}
	const unsigned char *p = CFDataGetBytePtr(sig);
	for (CFIndex i = 0; i < n; i++) {
		out[i] = p[i];
	}
	CFRelease(sig);
	*out_len = (size_t)n;
	return RSASIGN_OK;
}

void rsasign_free(void *key) {
	if (key != NULL) {
		CFRelease((CFTypeRef)key);
	}
}
