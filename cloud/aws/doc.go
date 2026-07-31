// Package aws holds what every AWS service client in this repository needs:
// SigV4 signing, credentials, environment resolution, and the HTTP client the
// build selects.
//
// It exists because the maintained Go SDK does not build with TinyGo —
// aws-sdk-go-v2 reaches for the full net/http.Transport API through
// smithy-go, which TinyGo declares as an empty struct — so the services here
// speak their REST APIs directly. Signing is the part they share, and a second
// copy of it would be a second chance to break the rule that the signature must
// cover exactly what goes on the wire.
//
//	req, _ := http.NewRequest("POST", endpoint, body)
//	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
//	aws.Sign(req, creds, aws.SignRequest{
//		Service:     "dynamodb",
//		Region:      "ap-northeast-1",
//		PayloadHash: aws.SHA256Hex(payload),
//	})
//
// Credentials are static values or environment variables. There is no shared
// credentials file, no SSO, and no IMDS lookup.
//
// The signer is usable for AWS services this repository has no client for: the
// request is an ordinary *http.Request, and Sign only reads and sets headers.
// Two rules matter when doing that. The service name in SignRequest enters both
// the credential scope and the signing key, so it must be the real one. And for
// any service with a path other than "/", set DoubleEncodePath: S3 is the
// exception the other services are not.
package aws
