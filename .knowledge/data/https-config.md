---
id: data:https-config
type: data
title: Client TLS Config
---
Backend-neutral TLS client configuration; deliberately not `crypto/tls.Config`, because TinyGo builds must not import `crypto/tls`.

```yaml
type: |
  type Config struct {
      RootCAs            [][]byte  // PEM-encoded certs, appended to system trust
      RootCAsOnly        bool      // ignore system trust store
      Certificates       []KeyPair // client certs for mTLS
      InsecureSkipVerify bool
      ServerName         string    // SNI override, default = URL host
      MinVersion         Version   // default VersionTLS12
  }
  type KeyPair struct { CertPEM, KeyPEM []byte }
encoding:
  format: PEM bytes only
  reason: >
    the one representation all of system:mbedtls, system:schannel, and
    system:network-framework accept; avoids crypto/x509 on tinygo
options:
  - WithRootCAPEM(pem []byte) Option
  - WithRootCAFile(path string) Option
  - WithClientCertificate(certPEM, keyPEM []byte) Option
  - WithInsecureSkipVerify(bool) Option
  - WithServerName(string) Option
  - WithMinVersion(Version) Option
std_go_mapping: converted field-by-field into crypto/tls.Config
system_anchors:
  darwin_windows: the OS store supplies them
  linux: loaded by Go from the distro bundle, per rule:linux-trust-store
zero_value: valid and secure; see rule:certificate-verification-default
satisfies: requirement:tls-client-config
