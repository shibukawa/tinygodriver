package s3

import "os"

// Credentials are the values SigV4 signs with. SessionToken is empty for
// long-lived keys.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// valid reports whether both required fields are present.
func (c Credentials) valid() bool {
	return c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// CredentialsFromEnv reads AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY and
// AWS_SESSION_TOKEN. Missing variables yield a zero Credentials, which New
// rejects.
func CredentialsFromEnv() Credentials {
	return Credentials{
		AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
	}
}

// regionFromEnv reads AWS_REGION, then AWS_DEFAULT_REGION.
func regionFromEnv() string {
	if v := os.Getenv("AWS_REGION"); v != "" {
		return v
	}
	return os.Getenv("AWS_DEFAULT_REGION")
}

// endpointFromEnv reads AWS_ENDPOINT_URL_S3, then AWS_ENDPOINT_URL. Both are
// the names the AWS CLI uses for pointing a client at a non-AWS endpoint.
func endpointFromEnv() string {
	if v := os.Getenv("AWS_ENDPOINT_URL_S3"); v != "" {
		return v
	}
	return os.Getenv("AWS_ENDPOINT_URL")
}
