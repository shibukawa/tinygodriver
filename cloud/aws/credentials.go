package aws

import (
	"errors"
	"os"
	"strings"
)

// Configuration failures shared by every service client. A rejected signature
// is a wire response and belongs to the service package that decoded it.
var (
	ErrNoCredentials = errors.New("aws: no credentials configured")
	ErrNoRegion      = errors.New("aws: no region configured")
)

// Credentials are the values SigV4 signs with. SessionToken is empty for
// long-lived keys.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Valid reports whether both required fields are present.
func (c Credentials) Valid() bool {
	return c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// CredentialsFromEnv reads AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY and
// AWS_SESSION_TOKEN. Missing variables yield a zero Credentials, which every
// client constructor rejects.
//
// There is no shared credentials file, no SSO, and no metadata service: those
// need an INI parser, a browser flow, and a link-local HTTP call respectively,
// none of which belong in a client this size.
func CredentialsFromEnv() Credentials {
	return Credentials{
		AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
	}
}

// RegionFromEnv reads AWS_REGION, then AWS_DEFAULT_REGION.
func RegionFromEnv() string {
	if v := os.Getenv("AWS_REGION"); v != "" {
		return v
	}
	return os.Getenv("AWS_DEFAULT_REGION")
}

// EndpointFromEnv reads AWS_ENDPOINT_URL_<SERVICE>, then AWS_ENDPOINT_URL,
// which are the names the AWS CLI uses for pointing a client at a non-AWS
// endpoint. The service is spelled as in a SigV4 credential scope, so "s3"
// reads AWS_ENDPOINT_URL_S3 and "dynamodb" reads AWS_ENDPOINT_URL_DYNAMODB.
func EndpointFromEnv(service string) string {
	if service != "" {
		name := "AWS_ENDPOINT_URL_" + strings.ToUpper(strings.ReplaceAll(service, "-", "_"))
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return os.Getenv("AWS_ENDPOINT_URL")
}
