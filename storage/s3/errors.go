package s3

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/shibukawa/tinygodriver/cloud/aws"
)

// Sentinel errors. S3 error codes are mapped onto them so application code can
// branch with errors.Is without matching strings.
var (
	ErrNoSuchKey      = errors.New("s3: no such key")
	ErrNoSuchBucket   = errors.New("s3: no such bucket")
	ErrAccessDenied   = errors.New("s3: access denied")
	ErrBucketExists   = errors.New("s3: bucket already exists")
	ErrBucketNotEmpty = errors.New("s3: bucket not empty")
	ErrInvalidRange   = errors.New("s3: requested range not satisfiable")
	ErrBadCredentials = errors.New("s3: credentials rejected")
	ErrNoSuchUpload   = errors.New("s3: no such multipart upload")
	ErrInvalidPart    = errors.New("s3: invalid part")

	// Configuration failures are the shared ones, so errors.Is matches whether
	// the caller compares against s3 or cloud/aws.
	ErrNoCredentials   = aws.ErrNoCredentials
	ErrNoRegion        = aws.ErrNoRegion
	ErrTooManyRedirect = errors.New("s3: too many redirects")

	// ErrPresignExpiry reports a Presign expiry S3 would refuse: negative, or
	// more than MaxPresignExpiry.
	ErrPresignExpiry = errors.New("s3: presign expiry out of range")
)

// codeToSentinel maps the Code element of an S3 error document.
var codeToSentinel = map[string]error{
	"NoSuchKey":                    ErrNoSuchKey,
	"NoSuchBucket":                 ErrNoSuchBucket,
	"AccessDenied":                 ErrAccessDenied,
	"AllAccessDisabled":            ErrAccessDenied,
	"BucketAlreadyExists":          ErrBucketExists,
	"BucketAlreadyOwnedByYou":      ErrBucketExists,
	"BucketNotEmpty":               ErrBucketNotEmpty,
	"InvalidRange":                 ErrInvalidRange,
	"NoSuchUpload":                 ErrNoSuchUpload,
	"InvalidPart":                  ErrInvalidPart,
	"InvalidPartOrder":             ErrInvalidPart,
	"EntityTooSmall":               ErrInvalidPart,
	"SignatureDoesNotMatch":        ErrBadCredentials,
	"InvalidAccessKeyId":           ErrBadCredentials,
	"InvalidSecurity":              ErrBadCredentials,
	"ExpiredToken":                 ErrBadCredentials,
	"AuthorizationHeaderMalformed": ErrBadCredentials,
}

// statusToSentinel covers replies with no usable error document, which is what
// a HEAD request always returns.
var statusToSentinel = map[int]error{
	http.StatusNotFound:                     ErrNoSuchKey,
	http.StatusForbidden:                    ErrAccessDenied,
	http.StatusConflict:                     ErrBucketExists,
	http.StatusRequestedRangeNotSatisfiable: ErrInvalidRange,
}

// Error is a failed S3 operation. Code and Message come from the XML error
// document when the endpoint sent one.
type Error struct {
	Op         string // "Get", "Put", "List", ...
	Bucket     string
	Key        string
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	err        error
}

func (e *Error) Error() string {
	target := e.Bucket
	if e.Key != "" {
		target += "/" + e.Key
	}
	msg := fmt.Sprintf("s3: %s %s: %d", e.Op, target, e.StatusCode)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.RequestID != "" {
		msg += fmt.Sprintf(" (request id %s)", e.RequestID)
	}
	return msg
}

// Unwrap returns the sentinel this failure maps onto, so errors.Is works.
func (e *Error) Unwrap() error { return e.err }

// newError builds an Error from a response body, falling back to the status
// code when the body is empty or not an error document.
func newError(op, bucket, key string, status int, body []byte, requestID string) *Error {
	e := &Error{Op: op, Bucket: bucket, Key: key, StatusCode: status, RequestID: requestID}

	if code, message, docRequestID, ok := parseErrorDocument(body); ok {
		e.Code = code
		e.Message = message
		if docRequestID != "" {
			e.RequestID = docRequestID
		}
	}
	if sentinel, ok := codeToSentinel[e.Code]; ok {
		e.err = sentinel
		return e
	}
	if sentinel, ok := statusToSentinel[status]; ok {
		e.err = sentinel
	}
	return e
}
