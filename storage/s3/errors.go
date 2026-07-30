package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors. S3 error codes are mapped onto them so application code can
// branch with errors.Is without matching strings.
var (
	ErrNoSuchKey       = errors.New("s3: no such key")
	ErrNoSuchBucket    = errors.New("s3: no such bucket")
	ErrAccessDenied    = errors.New("s3: access denied")
	ErrBucketExists    = errors.New("s3: bucket already exists")
	ErrBucketNotEmpty  = errors.New("s3: bucket not empty")
	ErrInvalidRange    = errors.New("s3: requested range not satisfiable")
	ErrBadCredentials  = errors.New("s3: credentials rejected")
	ErrNoCredentials   = errors.New("s3: no credentials configured")
	ErrNoRegion        = errors.New("s3: no region configured")
	ErrTooManyRedirect = errors.New("s3: too many redirects")
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

// errorDocument is the XML body S3 returns for a failed request.
type errorDocument struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	RequestID string   `xml:"RequestId"`
}

// newError builds an Error from a response body, falling back to the status
// code when the body is empty or not an error document.
func newError(op, bucket, key string, status int, body []byte, requestID string) *Error {
	e := &Error{Op: op, Bucket: bucket, Key: key, StatusCode: status, RequestID: requestID}

	var doc errorDocument
	if len(body) > 0 && xml.Unmarshal(body, &doc) == nil {
		e.Code = doc.Code
		e.Message = doc.Message
		if doc.RequestID != "" {
			e.RequestID = doc.RequestID
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
