package telebirr

import (
	"errors"
	"io"
	"testing"
)

func TestErrorKind_String(t *testing.T) {
	tests := []struct {
		kind     ErrorKind
		expected string
	}{
		{ErrorDomain, "domain"},
		{ErrorTransport, "transport"},
		{ErrorCancelled, "cancelled"},
		{ErrorKind(99), "unknown"},
		{ErrorKind(0), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.expected {
				t.Errorf("String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestVerificationError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *VerificationError
		expected string
	}{
		{
			name:     "message only",
			err:      &VerificationError{Kind: ErrorTransport, Message: "relay down"},
			expected: "telebirr: relay down",
		},
		{
			name:     "message with details",
			err:      &VerificationError{Kind: ErrorDomain, Message: "not found", Details: "leul.et"},
			expected: "telebirr: not found (leul.et)",
		},
		{
			name: "message with details and cause",
			err: &VerificationError{
				Kind:    ErrorCancelled,
				Message: "cancelled",
				Details: "preferred",
				Err:     io.EOF,
			},
			expected: "telebirr: cancelled (preferred): EOF",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.expected {
				t.Errorf("Error() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestVerificationError_Unwrap(t *testing.T) {
	sentinel := errors.New("connection reset")
	err := &VerificationError{
		Kind:    ErrorTransport,
		Message: "the fallback relay is unreachable or timed out",
		Details: "community relay 1",
		Err:     sentinel,
	}

	var verificationErr *VerificationError
	if !errors.As(err, &verificationErr) {
		t.Fatal("errors.As(*VerificationError) = false, want true")
	}
	if verificationErr.Kind != ErrorTransport {
		t.Errorf("Kind = %v, want %v", verificationErr.Kind, ErrorTransport)
	}
	if !errors.Is(err, sentinel) {
		t.Error("errors.Is(cause) = false through the VerificationError chain")
	}
}

func TestRoute_String(t *testing.T) {
	tests := []struct {
		role     Role
		expected string
	}{
		{RolePreferred, "preferred"},
		{RoleFallback, "fallback"},
		{RoleUnknown, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.role.String(); got != tt.expected {
				t.Errorf("String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestRouteHealth_String(t *testing.T) {
	tests := []struct {
		health   RouteHealth
		expected string
	}{
		{RouteHealthOperational, "operational"},
		{RouteHealthUnavailable, "unavailable"},
		{RouteHealthUnknown, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.health.String(); got != tt.expected {
				t.Errorf("String() = %q, want %q", got, tt.expected)
			}
		})
	}
}
