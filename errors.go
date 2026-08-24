package telebirr

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorKind classifies a [VerificationError].
type ErrorKind int

const (
	// ErrorDomain means the relay answered normally but rejected the lookup
	// at the application level, e.g. "receipt not found". The receipt
	// reference may be wrong or the relay may simply not know it.
	ErrorDomain ErrorKind = iota + 1

	// ErrorTransport means the relay could not be reached at all: network
	// failure, timeout, connection refused, or an HTTP 5xx status.
	ErrorTransport

	// ErrorCancelled means the caller's context was cancelled before the
	// verification completed.
	ErrorCancelled
)

// String implements fmt.Stringer.
func (k ErrorKind) String() string {
	switch k {
	case ErrorDomain:
		return "domain"
	case ErrorTransport:
		return "transport"
	case ErrorCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// VerificationError is the error type returned by verification and probe
// operations. Use errors.As to inspect it and its Kind to branch on the
// failure class.
type VerificationError struct {
	Kind    ErrorKind // failure class; never zero on returned values
	Message string    // human-readable summary
	Details string    // optional extra context, e.g. relay label or cause
	Err     error     // wrapped cause, if any
}

// Error implements the error interface.
func (e *VerificationError) Error() string {
	var b strings.Builder
	b.WriteString("telebirr: ")
	b.WriteString(e.Message)
	if e.Details != "" {
		b.WriteString(" (")
		b.WriteString(e.Details)
		b.WriteString(")")
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

// Unwrap exposes the wrapped cause to errors.Is and errors.As.
func (e *VerificationError) Unwrap() error { return e.Err }

// Sentinels returned by Verify.
var (
	// ErrNotFound is returned when every configured source was consulted
	// without transport failures but none of them knows the receipt.
	ErrNotFound = errors.New("telebirr: receipt not found")

	// ErrInvalidReference is returned when a reference number fails basic
	// sanity checks before it is placed into an upstream URL.
	ErrInvalidReference = errors.New("telebirr: invalid reference")

	// ErrNoRoutes is returned when verification was asked to skip the
	// primary source but no fallback routes are configured.
	ErrNoRoutes = errors.New("telebirr: no fallback routes configured")
)

func newTransportError(details string, cause error) *VerificationError {
	return &VerificationError{
		Kind:    ErrorTransport,
		Message: "the fallback relay is unreachable or timed out",
		Details: details,
		Err:     cause,
	}
}

func newCancelledError(cause error) *VerificationError {
	return &VerificationError{
		Kind:    ErrorCancelled,
		Message: "the request was cancelled",
		Err:     cause,
	}
}
