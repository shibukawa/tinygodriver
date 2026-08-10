package dynamodb

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/shibukawa/tinygodriver/cloud/aws"
)

// Sentinel errors. DynamoDB exception names are mapped onto them so application
// code can branch with errors.Is without matching strings.
var (
	ErrItemNotFound        = errors.New("dynamodb: item not found")
	ErrResourceNotFound    = errors.New("dynamodb: resource not found")
	ErrConditionalCheck    = errors.New("dynamodb: conditional check failed")
	ErrThroughputExceeded  = errors.New("dynamodb: provisioned throughput exceeded")
	ErrThrottled           = errors.New("dynamodb: request throttled")
	ErrValidation          = errors.New("dynamodb: validation failed")
	ErrTableInUse          = errors.New("dynamodb: table in use")
	ErrTableNotFound       = errors.New("dynamodb: table not found")
	ErrRequestTooLarge     = errors.New("dynamodb: request too large")
	ErrTransactionConflict = errors.New("dynamodb: transaction conflict")
	ErrBadCredentials      = errors.New("dynamodb: credentials rejected")
	ErrChecksumMismatch    = errors.New("dynamodb: response checksum mismatch")
	ErrServerFailure       = errors.New("dynamodb: server failure")
	ErrHTTPClientOwnership = errors.New("dynamodb: WithHTTPClient cannot be combined with WithMaxIdleConns")

	// ErrNoCredentials and ErrNoRegion are the shared configuration errors, so
	// errors.Is matches whether the caller compares against this package or
	// cloud/aws.
	ErrNoCredentials = aws.ErrNoCredentials
	ErrNoRegion      = aws.ErrNoRegion
)

// typeToSentinel maps the exception name, which is what follows the "#" in the
// __type member.
//
// Both namespaces appear there: com.amazonaws.dynamodb.v20120810# for service
// exceptions and com.amazon.coral.service# for authentication failures, so the
// prefix is dropped rather than matched.
var typeToSentinel = map[string]error{
	"ResourceNotFoundException":                ErrResourceNotFound,
	"TableNotFoundException":                   ErrTableNotFound,
	"ConditionalCheckFailedException":          ErrConditionalCheck,
	"ProvisionedThroughputExceededException":   ErrThroughputExceeded,
	"ThrottlingException":                      ErrThrottled,
	"RequestLimitExceeded":                     ErrThrottled,
	"ValidationException":                      ErrValidation,
	"SerializationException":                   ErrValidation,
	"ResourceInUseException":                   ErrTableInUse,
	"TableAlreadyExistsException":              ErrTableInUse,
	"TableInUseException":                      ErrTableInUse,
	"ItemCollectionSizeLimitExceededException": ErrRequestTooLarge,
	"RequestEntityTooLarge":                    ErrRequestTooLarge,
	"TransactionConflictException":             ErrTransactionConflict,
	"InternalServerError":                      ErrServerFailure,
	"ServiceUnavailable":                       ErrServerFailure,
	"ServiceUnavailableException":              ErrServerFailure,
	"UnrecognizedClientException":              ErrBadCredentials,
	"InvalidSignatureException":                ErrBadCredentials,
	"MissingAuthenticationTokenException":      ErrBadCredentials,
	"IncompleteSignatureException":             ErrBadCredentials,
	"AccessDeniedException":                    ErrBadCredentials,
	"ExpiredTokenException":                    ErrBadCredentials,
}

// statusToSentinel covers a reply with no usable error document.
var statusToSentinel = map[int]error{
	http.StatusForbidden:             ErrBadCredentials,
	http.StatusRequestEntityTooLarge: ErrRequestTooLarge,
	http.StatusInternalServerError:   ErrServerFailure,
	http.StatusServiceUnavailable:    ErrServerFailure,
	http.StatusBadGateway:            ErrServerFailure,
	http.StatusGatewayTimeout:        ErrServerFailure,
}

// retryableTypes are the exceptions that mean "the same request may work in a
// moment". Throttling belongs here because it is a normal operating condition
// on a provisioned table, not a fault.
var retryableTypes = map[string]bool{
	"ProvisionedThroughputExceededException": true,
	"ThrottlingException":                    true,
	"ThrottlingError":                        true,
	"RequestLimitExceeded":                   true,
	"TransactionConflictException":           true,
	"InternalServerError":                    true,
	"ServiceUnavailable":                     true,
	"ServiceUnavailableException":            true,
}

// Error is a failed DynamoDB operation.
type Error struct {
	Op         string // "GetItem", "Query", ...
	Table      string
	StatusCode int
	Type       string // the exception name, without its namespace
	Message    string
	RequestID  string
	err        error
}

func (e *Error) Error() string {
	msg := "dynamodb: " + e.Op
	if e.Table != "" {
		msg += " " + e.Table
	}
	msg += fmt.Sprintf(": %d", e.StatusCode)
	if e.Type != "" {
		msg += " " + e.Type
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

// Retryable reports whether sending the same request again could succeed.
func (e *Error) Retryable() bool {
	if retryableTypes[e.Type] {
		return true
	}
	if e.Type != "" {
		// A named exception that is not on the list is a decision the server
		// already made; only the status codes below override that.
		return e.StatusCode >= 500
	}
	return e.StatusCode >= 500 || e.StatusCode == http.StatusTooManyRequests
}

// errorDocument is the JSON body DynamoDB returns for a failed request. The
// message member is spelled both ways in the wild.
type errorDocument struct {
	Type       string `json:"__type"`
	Message    string `json:"message"`
	AltMessage string `json:"Message"`
}

// newError builds an Error from a response body, falling back to the status
// code when the body is empty or not an error document.
func newError(op, table string, status int, body []byte, requestID string) *Error {
	e := &Error{Op: op, Table: table, StatusCode: status, RequestID: requestID}

	var doc errorDocument
	if len(body) > 0 && json.Unmarshal(body, &doc) == nil {
		e.Type = exceptionName(doc.Type)
		e.Message = doc.Message
		if e.Message == "" {
			e.Message = doc.AltMessage
		}
	}
	if sentinel, ok := typeToSentinel[e.Type]; ok {
		e.err = sentinel
		return e
	}
	if sentinel, ok := statusToSentinel[status]; ok {
		e.err = sentinel
	}
	return e
}

// exceptionName drops the namespace and any trailing version, turning
// "com.amazonaws.dynamodb.v20120810#ResourceNotFoundException" into
// "ResourceNotFoundException".
func exceptionName(t string) string {
	if i := strings.LastIndex(t, "#"); i >= 0 {
		t = t[i+1:]
	}
	if i := strings.LastIndex(t, ":"); i >= 0 {
		t = t[:i]
	}
	return t
}
