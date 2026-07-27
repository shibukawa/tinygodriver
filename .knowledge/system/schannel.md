---
id: system:schannel
type: system
title: Schannel Backend
---
Windows TLS backend via SSPI, called from pure Go through `syscall`; encrypts and decrypts buffers while Go keeps the socket.

```yaml
state: provisional
state_note: >
  design intent from reading the API, not measured; see
  requirement:windows-tinygo-feasibility
platform: windows
model: buffer_transform
language: go
cgo: false
availability: secur32.dll and crypt32.dll ship with the OS; no install step
rationale_for_pure_go:
  - SSPI is a flat C ABI with no callbacks, so syscall covers it fully
  - removes the mingw toolchain requirement from windows builds
  - avoids rule:cgo-bridge-contract entirely on this platform
dlls:
  secur32: [AcquireCredentialsHandleW, InitializeSecurityContextW, QueryContextAttributesW, EncryptMessage, DecryptMessage, DeleteSecurityContext, FreeCredentialsHandle]
  crypt32: [CertCreateCertificateContext, CertOpenStore, CertAddCertificateContextToStore, CertGetCertificateChain, CertVerifyCertificateChainPolicy, CertFreeCertificateContext]
handshake_loop:
  - AcquireCredentialsHandle with SCH_CREDENTIALS
  - InitializeSecurityContext repeatedly while SEC_I_CONTINUE_NEEDED
  - caller moves the token buffers over the socket between calls
  - QueryContextAttributes SECPKG_ATTR_STREAM_SIZES for header, trailer, max message
record_io:
  send: EncryptMessage over header + data + trailer buffers
  recv: DecryptMessage, handling SEC_E_INCOMPLETE_MESSAGE and SEC_I_RENEGOTIATE
trust_store: Windows certificate store; custom CA needs a temporary in-memory HCERTSTORE
verification_control:
  manual: SCH_CRED_MANUAL_CRED_VALIDATION plus CertGetCertificateChain and CertVerifyCertificateChainPolicy
  needed_for: custom CA and InsecureSkipVerify in requirement:tls-client-config
client_cert: PFX or CERT_CONTEXT with CryptAcquireCertificatePrivateKey
open_question: >
  does tinygo support syscall.NewLazyDLL and stdcall on windows/amd64;
  see requirement:windows-tinygo-feasibility
