package datastore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Sentinels for the canonical error codes Datastore returns. Match on these
// with errors.Is rather than on the HTTP status: ABORTED and ALREADY_EXISTS are
// both 409 and mean opposite things, one retryable and one terminal.
var (
	ErrNoSuchEntity       = errors.New("datastore: no such entity")
	ErrAlreadyExists      = errors.New("datastore: entity already exists")
	ErrAborted            = errors.New("datastore: transaction aborted by contention")
	ErrFailedPrecondition = errors.New("datastore: failed precondition")
	ErrInvalidArgument    = errors.New("datastore: invalid argument")
	ErrPermissionDenied   = errors.New("datastore: permission denied")
	ErrUnauthenticated    = errors.New("datastore: unauthenticated")
	ErrUnavailable        = errors.New("datastore: service unavailable")
	ErrDeadlineExceeded   = errors.New("datastore: deadline exceeded")
	ErrResourceExhausted  = errors.New("datastore: resource exhausted")
	ErrInternal           = errors.New("datastore: internal server error")
)

// Error is a failure reported by the service.
//
// Status is the canonical code name, which is the only reliable discriminator:
// the HTTP code is ambiguous by itself.
type Error struct {
	Op         string
	Kind       string
	StatusCode int
	Status     string
	Message    string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("datastore: ")
	b.WriteString(e.Op)
	if e.Kind != "" {
		b.WriteString(" ")
		b.WriteString(e.Kind)
	}
	b.WriteString(": ")
	if e.Status != "" {
		b.WriteString(e.Status)
	} else {
		fmt.Fprintf(&b, "HTTP %d", e.StatusCode)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	return b.String()
}

// Unwrap maps the status to a sentinel so errors.Is works.
func (e *Error) Unwrap() error { return sentinelFor(e.Status) }

// Retryable reports whether sending the same request again could work.
//
// ABORTED is deliberately absent: outside a transaction it is terminal, and
// inside one the whole closure re-runs rather than the request. That decision
// belongs to RunInTransaction, not here.
func (e *Error) Retryable() bool {
	switch e.Status {
	case "UNAVAILABLE", "DEADLINE_EXCEEDED", "RESOURCE_EXHAUSTED":
		return true
	case "INTERNAL":
		// Google documents "do not retry more than once". The retry loop
		// enforces the single attempt; this only reports that one exists.
		return true
	}
	return false
}

func sentinelFor(status string) error {
	switch status {
	case "NOT_FOUND":
		return ErrNoSuchEntity
	case "ALREADY_EXISTS":
		return ErrAlreadyExists
	case "ABORTED":
		return ErrAborted
	case "FAILED_PRECONDITION":
		return ErrFailedPrecondition
	case "INVALID_ARGUMENT":
		return ErrInvalidArgument
	case "PERMISSION_DENIED":
		return ErrPermissionDenied
	case "UNAUTHENTICATED":
		return ErrUnauthenticated
	case "UNAVAILABLE":
		return ErrUnavailable
	case "DEADLINE_EXCEEDED":
		return ErrDeadlineExceeded
	case "RESOURCE_EXHAUSTED":
		return ErrResourceExhausted
	case "INTERNAL":
		return ErrInternal
	}
	return nil
}

// statusFallback names a status for a reply whose body carried none, so error
// classification does not depend on the server being well behaved.
func statusFallback(httpStatus int) string {
	switch httpStatus {
	case 400:
		return "INVALID_ARGUMENT"
	case 401:
		return "UNAUTHENTICATED"
	case 403:
		return "PERMISSION_DENIED"
	case 404:
		return "NOT_FOUND"
	case 409:
		// Ambiguous on the wire, and guessing wrong in the retryable direction
		// would retry a duplicate insert forever. The safe guess is terminal.
		return "ALREADY_EXISTS"
	case 429:
		return "RESOURCE_EXHAUSTED"
	case 500:
		return "INTERNAL"
	case 503:
		return "UNAVAILABLE"
	case 504:
		return "DEADLINE_EXCEEDED"
	}
	return ""
}

// parseError builds an Error from a non-200 reply.
func parseError(op, kind string, httpStatus int, body []byte) *Error {
	e := &Error{Op: op, Kind: kind, StatusCode: httpStatus}
	var envelope struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		e.Status = envelope.Error.Status
		e.Message = envelope.Error.Message
	}
	if e.Status == "" {
		e.Status = statusFallback(httpStatus)
	}
	if e.Message == "" && len(body) > 0 {
		e.Message = truncate(strings.TrimSpace(string(body)), 256)
	}
	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
