package google

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

// Configuration failures shared by every service client. A rejected token is a
// wire response and belongs to the service package that decoded it.
var (
	ErrNoCredentials = errors.New("google: no credentials configured")
	ErrNoProject     = errors.New("google: no project configured")
	ErrBadPrivateKey = errors.New("google: private key is not a usable PKCS#8 RSA key")
	ErrTokenExpired  = errors.New("google: token expired")
)

// Credentials are the fields of a service account key file that this package
// uses. The file carries more; the rest is not needed to mint a token.
type Credentials struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

// Valid reports whether the fields needed to sign are present.
func (c Credentials) Valid() bool {
	return c.ClientEmail != "" && c.PrivateKey != ""
}

// CredentialsFromJSON reads a service account key file.
func CredentialsFromJSON(b []byte) (Credentials, error) {
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return Credentials{}, ErrNoCredentials
	}
	if !c.Valid() {
		return Credentials{}, ErrNoCredentials
	}
	if c.TokenURI == "" {
		c.TokenURI = defaultTokenURI
	}
	return c, nil
}

// CredentialsFromFile reads a service account key file from disk.
func CredentialsFromFile(path string) (Credentials, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, ErrNoCredentials
	}
	return CredentialsFromJSON(b)
}

// CredentialsFromEnv reads the file named by GOOGLE_APPLICATION_CREDENTIALS.
//
// That is the whole of the resolution. The full Application Default
// Credentials search also consults a well-known gcloud config path, the GCE
// metadata server, and external account files; each is a different credential
// kind with a different failure mode, and a client this size is better off
// being told which one it has. MetadataTokenSource covers the GCE case
// explicitly.
func CredentialsFromEnv() (Credentials, error) {
	path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if path == "" {
		return Credentials{}, ErrNoCredentials
	}
	return CredentialsFromFile(path)
}

// ProjectIDFromEnv reads GOOGLE_CLOUD_PROJECT, then DATASTORE_PROJECT_ID, the
// latter being what the Datastore emulator sets.
func ProjectIDFromEnv() string {
	if v := os.Getenv("GOOGLE_CLOUD_PROJECT"); v != "" {
		return v
	}
	return os.Getenv("DATASTORE_PROJECT_ID")
}

// EmulatorHost returns the host:port an emulator for service is listening on,
// or the empty string. The variable name is the one the gcloud emulators set,
// so "datastore" reads DATASTORE_EMULATOR_HOST.
//
// The value carries no scheme. Emulators speak plain HTTP.
func EmulatorHost(service string) string {
	if service == "" {
		return ""
	}
	name := strings.ToUpper(strings.ReplaceAll(service, "-", "_")) + "_EMULATOR_HOST"
	return os.Getenv(name)
}
