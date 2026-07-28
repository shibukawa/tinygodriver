---
id: system:schannel
type: system
title: Schannel Backend
---
Windows TLS backend via SSPI, called through cgo; encrypts and decrypts buffers while Go keeps the socket.

```yaml
state: implemented
state_note: >
  cross-compiles, links and vets for windows/amd64. Partially exercised under
  wine 11: handshake, record I/O, STARTTLS upgrade and every rejection path
  pass; custom-anchor acceptance and client certificates are blocked by wine
  stubs, not by this code. Never run on Windows. See
  requirement:windows-tinygo-feasibility.
platform: windows
model: buffer_transform
language: c, called from go via cgo
cgo: true
cgo_note: >
  the original design said pure Go via syscall.NewLazyDLL. That is not
  available: tinygo 0.41 ships no windows syscall implementation at all, so
  there is no NewLazyDLL, no Syscall and no LoadLibrary. TinyGo reaches win32
  through cgo and //export declarations, which is what netdev/sys_windows.go
  already does for winsock, so this backend does the same.
availability: secur32.dll, crypt32.dll and ncrypt.dll ship with the OS; no install step
toolchain: mingw-w64, already required by system:tinygo-netdev on this platform
files:
  bridge: internal/schannel/sspibridge.c and .h
  binding: internal/schannel/schannel.go
  netdev_seam: netdev/tls_windows.go
  https_seam: https/dial_windows.go
header_policy: >
  system headers are included, unlike the darwin bridges. SSPI and crypt32
  layouts are too intricate to hand-declare safely on a platform that cannot be
  run here. The exceptions are SCHANNEL_CRED, SCH_CREDENTIALS, TLS_PARAMETERS
  and CERT_CHAIN_ENGINE_CONFIG, redeclared under private names because their
  presence varies across mingw-w64 releases.
naming_note: >
  the bridge is sspibridge.h, not schannel.h, because cgo puts the package
  directory on the include path and a local schannel.h shadows mingw's.
dlls:
  secur32: [AcquireCredentialsHandleW, InitializeSecurityContextW, QueryContextAttributesW, EncryptMessage, DecryptMessage, ApplyControlToken, DeleteSecurityContext, FreeCredentialsHandle, FreeContextBuffer]
  crypt32: [CertOpenStore, CertAddEncodedCertificateToStore, CertCreateCertificateContext, CertCreateCertificateChainEngine, CertGetCertificateChain, CertVerifyCertificateChainPolicy, CertSetCertificateContextProperty, CryptDecodeObjectEx]
  ncrypt: [NCryptOpenStorageProvider, NCryptImportKey, NCryptFreeObject]
credentials:
  primary: SCH_CREDENTIALS, dwVersion 5, min version via TLS_PARAMETERS.grbitDisabledProtocols
  fallback: SCHANNEL_CRED, dwVersion 4, min version via grbitEnabledProtocols
  why_fallback: >
    SCH_CREDENTIALS is the only structure that reaches TLS 1.3, but older
    Windows and Wine reject it. Falling back caps the connection at TLS 1.2
    rather than failing.
  flags: SCH_CRED_MANUAL_CRED_VALIDATION | SCH_CRED_NO_DEFAULT_CREDS | SCH_CRED_NO_SYSTEM_MAPPER | SCH_USE_STRONG_CRYPTO
handshake_loop:
  - AcquireCredentialsHandle, modern structure first
  - InitializeSecurityContext repeatedly while SEC_I_CONTINUE_NEEDED
  - this layer moves the token buffers over the socket between calls
  - SEC_E_INCOMPLETE_MESSAGE reads more; SECBUFFER_EXTRA is kept for the next call
  - SEC_I_INCOMPLETE_CREDENTIALS retries once with ISC_REQ_USE_SUPPLIED_CREDS,
    which is how a server that asks for a client certificate we do not have
    still gets an empty certificate list rather than a dropped connection
  - QueryContextAttributes SECPKG_ATTR_STREAM_SIZES for header, trailer, max message
record_io:
  send: EncryptMessage over header + data + trailer, contiguous so one write emits the record
  recv: DecryptMessage, handling SEC_E_INCOMPLETE_MESSAGE, SEC_I_RENEGOTIATE and SEC_I_CONTEXT_EXPIRED
  renegotiate: re-enters the same handshake loop with the buffered extra bytes
trust_store: Windows certificate store; custom CAs go in an in-memory HCERTSTORE
verification_control:
  when: after the handshake completes, before any application data
  why_after: >
    Schannel has no mid-handshake hook like Secure Transport's
    break-on-server-auth, so manual validation necessarily runs late. Every
    Schannel client has this shape.
  how: CertGetCertificateChain then CertVerifyCertificateChainPolicy CERT_CHAIN_POLICY_SSL
  root_cas_only: chain engine with hExclusiveRoot; the only way Windows treats a supplied root as the only root
  root_cas_additive: >
    system store first, then a second pass against an exclusive engine over the
    extra anchors. Windows has no additive "system roots plus these" mode.
  revocation: not checked, matching crypto/tls and the other two backends
client_cert:
  supported: RSA only
  how: CryptDecodeObjectEx to a legacy CAPI blob, NCryptImportKey as an ephemeral CNG key, CertSetCertificateContextProperty CERT_NCRYPT_KEY_HANDLE_PROP_ID
  why_ephemeral: >
    the legacy CryptAcquireContext path cannot import a private key without
    naming a key container, and a named container is a file in the user's
    profile. CNG import with no persist flag leaves nothing behind.
  not_supported: EC, which would need a hand-assembled BCRYPT_ECCPRIVATE_BLOB
  behavior: ErrClientCertificateUnsupported rather than silently sending nothing
```
